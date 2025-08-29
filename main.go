package main

import (
	"bufio"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// StreamManager manages multiple RTSP streams with single ingest per camera
type StreamManager struct {
	streams     map[string]*Stream
	clients     map[string]map[string]*Client
	mu          sync.RWMutex
	clientIDGen int64
}

// Stream represents a single RTSP stream with multiple consumers
type Stream struct {
	rtspURL       string
	streamID      string
	cmd           *exec.Cmd
	frameBuffer   chan []byte
	clients       map[string]*Client
	clientsMu     sync.RWMutex
	isRunning     bool
	cancelFunc    context.CancelFunc
	lastFrameTime time.Time
	frameCount    int64
	mu            sync.RWMutex
}

// Client represents a connected client consuming a stream
type Client struct {
	id       string
	streamID string
	conn     *websocket.Conn
	send     chan []byte
	manager  *StreamManager
}

// FrameMessage represents the frame data sent to clients
type FrameMessage struct {
	StreamID  string `json:"stream_id"`
	Timestamp int64  `json:"timestamp"`
	FrameData []byte `json:"frame_data"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins in development
	},
}

func NewStreamManager() *StreamManager {
	return &StreamManager{
		streams: make(map[string]*Stream),
		clients: make(map[string]map[string]*Client),
	}
}

func (sm *StreamManager) generateClientID() string {
	sm.clientIDGen++
	return fmt.Sprintf("client_%d", sm.clientIDGen)
}

// StartStream starts a new RTSP stream ingestion
func (sm *StreamManager) StartStream(streamID, rtspURL string, width, height int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.streams[streamID]; exists {
		return fmt.Errorf("stream %s already exists", streamID)
	}

	ctx, cancel := context.WithCancel(context.Background())

	stream := &Stream{
		rtspURL:     rtspURL,
		streamID:    streamID,
		frameBuffer: make(chan []byte, 100), // Buffer up to 100 frames
		clients:     make(map[string]*Client),
		cancelFunc:  cancel,
		isRunning:   false,
	}

	sm.streams[streamID] = stream
	sm.clients[streamID] = make(map[string]*Client)

	go sm.runFFmpegStream(ctx, stream, width, height)
	go sm.distributeFrames(stream)

	log.Printf("Started stream %s from %s", streamID, rtspURL)
	return nil
}

// runFFmpegStream runs FFmpeg to capture RTSP stream and output raw frames
func (sm *StreamManager) runFFmpegStream(ctx context.Context, stream *Stream, width, height int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			err := sm.startFFmpeg(ctx, stream, width, height)
			if err != nil {
				log.Printf("FFmpeg error for stream %s: %v", stream.streamID, err)
				time.Sleep(2 * time.Second) // Wait before retry
			}
		}
	}
}

func (sm *StreamManager) startFFmpeg(ctx context.Context, stream *Stream, width, height int) error {
	// FFmpeg command to convert RTSP to raw BGR24 frames
	args := []string{
		"-rtsp_transport", "tcp",
		"-i", stream.rtspURL,
		"-vf", fmt.Sprintf("scale=%d:%d", width, height),
		"-f", "rawvideo",
		"-pix_fmt", "bgr24",
		"-an", // No audio
		"-",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	stream.mu.Lock()
	stream.cmd = cmd
	stream.isRunning = true
	stream.mu.Unlock()

	// Start FFmpeg
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start FFmpeg: %v", err)
	}

	// Read stderr in a separate goroutine for logging
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("FFmpeg [%s]: %s", stream.streamID, scanner.Text())
		}
	}()

	// Read frames from stdout
	frameSize := width * height * 3 // BGR24 = 3 bytes per pixel
	frameData := make([]byte, frameSize)

	for {
		select {
		case <-ctx.Done():
			cmd.Process.Kill()
			return nil
		default:
			_, err := io.ReadFull(stdout, frameData)
			if err != nil {
				if err != io.EOF {
					log.Printf("Error reading frame from stream %s: %v", stream.streamID, err)
				}
				return err
			}

			// Create frame with metadata
			frame := make([]byte, len(frameData))
			copy(frame, frameData)

			// Send frame to buffer (non-blocking)
			select {
			case stream.frameBuffer <- frame:
				stream.mu.Lock()
				stream.lastFrameTime = time.Now()
				stream.frameCount++
				stream.mu.Unlock()
			default:
				// Buffer full, drop frame
				log.Printf("Frame buffer full for stream %s, dropping frame", stream.streamID)
			}
		}
	}
}

// distributeFrames sends frames from buffer to all connected clients
func (sm *StreamManager) distributeFrames(stream *Stream) {
	for frame := range stream.frameBuffer {
		stream.clientsMu.RLock()
		clients := make([]*Client, 0, len(stream.clients))
		for _, client := range stream.clients {
			clients = append(clients, client)
		}
		stream.clientsMu.RUnlock()

		// Send frame to all clients
		for _, client := range clients {
			select {
			case client.send <- frame:
			default:
				// Client buffer full, skip
				log.Printf("Client %s buffer full, skipping frame", client.id)
			}
		}
	}
}

// StopStream stops a running stream
func (sm *StreamManager) StopStream(streamID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	stream, exists := sm.streams[streamID]
	if !exists {
		return fmt.Errorf("stream %s not found", streamID)
	}

	// Cancel the context to stop FFmpeg
	stream.cancelFunc()

	// Close frame buffer
	close(stream.frameBuffer)

	// Disconnect all clients
	for _, client := range sm.clients[streamID] {
		close(client.send)
		client.conn.Close()
	}

	// Cleanup
	delete(sm.streams, streamID)
	delete(sm.clients, streamID)

	log.Printf("Stopped stream %s", streamID)
	return nil
}

// AddClient adds a new WebSocket client to a stream
func (sm *StreamManager) AddClient(streamID string, conn *websocket.Conn) (*Client, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	stream, exists := sm.streams[streamID]
	if !exists {
		return nil, fmt.Errorf("stream %s not found", streamID)
	}

	clientID := sm.generateClientID()
	client := &Client{
		id:       clientID,
		streamID: streamID,
		conn:     conn,
		send:     make(chan []byte, 10), // Buffer up to 10 frames per client
		manager:  sm,
	}

	stream.clientsMu.Lock()
	stream.clients[clientID] = client
	stream.clientsMu.Unlock()

	sm.clients[streamID][clientID] = client

	go client.writePump()
	go client.readPump()

	log.Printf("Added client %s to stream %s", clientID, streamID)
	return client, nil
}

// RemoveClient removes a client from a stream
func (sm *StreamManager) RemoveClient(client *Client) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if stream, exists := sm.streams[client.streamID]; exists {
		stream.clientsMu.Lock()
		delete(stream.clients, client.id)
		stream.clientsMu.Unlock()
	}

	delete(sm.clients[client.streamID], client.id)
	close(client.send)

	log.Printf("Removed client %s from stream %s", client.id, client.streamID)
}

// GetStreamStats returns statistics for a stream
func (sm *StreamManager) GetStreamStats(streamID string) (map[string]interface{}, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	stream, exists := sm.streams[streamID]
	if !exists {
		return nil, fmt.Errorf("stream %s not found", streamID)
	}

	stream.mu.RLock()
	stats := map[string]interface{}{
		"stream_id":       streamID,
		"rtsp_url":        stream.rtspURL,
		"is_running":      stream.isRunning,
		"frame_count":     stream.frameCount,
		"last_frame_time": stream.lastFrameTime,
		"client_count":    len(stream.clients),
		"buffer_size":     len(stream.frameBuffer),
	}
	stream.mu.RUnlock()

	return stats, nil
}

// Client methods

func (c *Client) readPump() {
	defer func() {
		c.manager.RemoveClient(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error for client %s: %v", c.id, err)
			}
			break
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case frame, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Send frame as binary data
			if err := c.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				log.Printf("Write error for client %s: %v", c.id, err)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// HTTP Handlers

func (sm *StreamManager) handleWebSocket(c *gin.Context) {
	streamID := c.Param("streamId")

	// Check if stream exists and is running
	sm.mu.RLock()
	stream, exists := sm.streams[streamID]
	sm.mu.RUnlock()

	if !exists {
		log.Printf("WebSocket connection failed: stream %s not found", streamID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Stream not found"})
		return
	}

	// Check if stream is actually running
	stream.mu.RLock()
	isRunning := stream.isRunning
	stream.mu.RUnlock()

	if !isRunning {
		log.Printf("WebSocket connection failed: stream %s not running", streamID)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Stream not running"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client, err := sm.AddClient(streamID, conn)
	if err != nil {
		log.Printf("Error adding client: %v", err)
		conn.Close()
		return
	}

	log.Printf("WebSocket client %s connected to stream %s", client.id, streamID)
}

func (sm *StreamManager) handleStartStream(c *gin.Context) {
	var req struct {
		StreamID string `json:"stream_id" binding:"required"`
		RTSPURL  string `json:"rtsp_url" binding:"required"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default resolution if not specified
	if req.Width == 0 {
		req.Width = 640
	}
	if req.Height == 0 {
		req.Height = 480
	}

	err := sm.StartStream(req.StreamID, req.RTSPURL, req.Width, req.Height)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Stream started successfully",
		"stream_id": req.StreamID,
		"rtsp_url":  req.RTSPURL,
		"width":     req.Width,
		"height":    req.Height,
	})
}

func (sm *StreamManager) handleStartStreamWithURL(c *gin.Context) {
	var req struct {
		RTSPURL string `json:"rtsp_url" binding:"required"`
		Width   int    `json:"width"`
		Height  int    `json:"height"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Generate stream ID from URL hash for consistency
	hasher := md5.New()
	hasher.Write([]byte(req.RTSPURL))
	streamID := fmt.Sprintf("stream_%x", hasher.Sum(nil))[:16]

	// Default resolution if not specified
	if req.Width == 0 {
		req.Width = 640
	}
	if req.Height == 0 {
		req.Height = 480
	}

	// Check if stream already exists
	sm.mu.RLock()
	if _, exists := sm.streams[streamID]; exists {
		sm.mu.RUnlock()
		c.JSON(http.StatusOK, gin.H{
			"message":   "Stream already running",
			"stream_id": streamID,
			"rtsp_url":  req.RTSPURL,
			"width":     req.Width,
			"height":    req.Height,
		})
		return
	}
	sm.mu.RUnlock()

	err := sm.StartStream(streamID, req.RTSPURL, req.Width, req.Height)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Stream started successfully",
		"stream_id": streamID,
		"rtsp_url":  req.RTSPURL,
		"width":     req.Width,
		"height":    req.Height,
	})
}

func (sm *StreamManager) handleStopStream(c *gin.Context) {
	streamID := c.Param("streamId")

	err := sm.StopStream(streamID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Stream stopped successfully",
		"stream_id": streamID,
	})
}

func (sm *StreamManager) handleGetStreamStats(c *gin.Context) {
	streamID := c.Param("streamId")

	stats, err := sm.GetStreamStats(streamID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}

func (sm *StreamManager) handleListStreams(c *gin.Context) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	streams := make([]map[string]interface{}, 0, len(sm.streams))
	for streamID, stream := range sm.streams {
		stream.mu.RLock()
		streamInfo := map[string]interface{}{
			"stream_id":    streamID,
			"rtsp_url":     stream.rtspURL,
			"is_running":   stream.isRunning,
			"client_count": len(stream.clients),
			"frame_count":  stream.frameCount,
		}
		stream.mu.RUnlock()
		streams = append(streams, streamInfo)
	}

	c.JSON(http.StatusOK, gin.H{"streams": streams})
}

// HTTP endpoint for raw frame access (for Python clients)
func (sm *StreamManager) handleGetFrame(c *gin.Context) {
	streamID := c.Param("streamId")

	sm.mu.RLock()
	stream, exists := sm.streams[streamID]
	sm.mu.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Stream not found"})
		return
	}

	// Wait for a frame with timeout
	timeout := time.After(5 * time.Second)
	select {
	case frame := <-stream.frameBuffer:
		// Return frame as binary data with headers
		c.Header("Content-Type", "application/octet-stream")
		c.Header("X-Frame-Timestamp", strconv.FormatInt(time.Now().UnixNano(), 10))
		c.Data(http.StatusOK, "application/octet-stream", frame)
	case <-timeout:
		c.JSON(http.StatusRequestTimeout, gin.H{"error": "Timeout waiting for frame"})
	}
}

func main() {
	// Check if FFmpeg is available
	if err := exec.Command("ffmpeg", "-version").Run(); err != nil {
		log.Fatal("FFmpeg is not installed or not in PATH. Please install FFmpeg to run this server.")
	}

	sm := NewStreamManager()

	// Set up Gin router
	r := gin.Default()

	// CORS middleware
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// API routes
	api := r.Group("/api")
	{
		api.POST("/streams", sm.handleStartStream)
		api.POST("/streams/start-with-url", sm.handleStartStreamWithURL)
		api.DELETE("/streams/:streamId", sm.handleStopStream)
		api.GET("/streams", sm.handleListStreams)
		api.GET("/streams/:streamId/stats", sm.handleGetStreamStats)
		api.GET("/streams/:streamId/frame", sm.handleGetFrame)
	}

	// WebSocket route
	r.GET("/ws/:streamId", sm.handleWebSocket)

	// Static files for iframe viewer
	r.Static("/static", "./")
	r.GET("/viewer", func(c *gin.Context) {
		c.File("./stream_viewer.html")
	})

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().Unix(),
		})
	})

	// Graceful shutdown
	srv := &http.Server{
		Addr:    ":8091",
		Handler: r,
	}

	go func() {
		log.Println("RTSP Stream Server starting on :8091")
		log.Println("API endpoints:")
		log.Println("  POST /api/streams - Start a new stream")
		log.Println("  DELETE /api/streams/:streamId - Stop a stream")
		log.Println("  GET /api/streams - List all streams")
		log.Println("  GET /api/streams/:streamId/stats - Get stream statistics")
		log.Println("  GET /api/streams/:streamId/frame - Get latest frame (HTTP)")
		log.Println("  WS /ws/:streamId - WebSocket connection for real-time frames")

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Stop all streams
	sm.mu.Lock()
	for streamID := range sm.streams {
		sm.StopStream(streamID)
	}
	sm.mu.Unlock()

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exited")
}
