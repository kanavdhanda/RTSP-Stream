package main

import (
	"crypto/md5"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// getUpgrader returns a WebSocket upgrader configured to allow all origins
func getUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins in development
		},
	}
}

// handleWebSocket upgrades HTTP connection to WebSocket for real-time frame streaming
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

	upgrader := getUpgrader()
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

// handleStartStream starts a new RTSP stream with specified ID
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

// handleStartStreamWithURL starts a new RTSP stream with auto-generated ID
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

// handleStopStream stops a stream if no clients are connected
func (sm *StreamManager) handleStopStream(c *gin.Context) {
	streamID := c.Param("streamId")

	// Check if there are still active clients
	sm.mu.RLock()
	stream, exists := sm.streams[streamID]
	if !exists {
		sm.mu.RUnlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "Stream not found"})
		return
	}

	stream.clientsMu.RLock()
	clientCount := len(stream.clients)
	stream.clientsMu.RUnlock()
	sm.mu.RUnlock()

	if clientCount > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":        fmt.Sprintf("Cannot stop stream %s: %d client(s) still connected", streamID, clientCount),
			"client_count": clientCount,
		})
		return
	}

	err := sm.StopStream(streamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Stream stopped successfully",
		"stream_id": streamID,
	})
}

// handleForceStopStream forcefully stops a stream regardless of connected clients
func (sm *StreamManager) handleForceStopStream(c *gin.Context) {
	streamID := c.Param("streamId")

	err := sm.StopStream(streamID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Stream force-stopped successfully",
		"stream_id": streamID,
	})
}

// handleGetStreamStats returns statistics about a specific stream
func (sm *StreamManager) handleGetStreamStats(c *gin.Context) {
	streamID := c.Param("streamId")

	stats, err := sm.GetStreamStats(streamID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleListStreams returns a list of all active streams
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

// handleGetFrame returns a single frame from the stream buffer (for Python clients)
func (sm *StreamManager) handleGetFrame(c *gin.Context) {
	streamID := c.Param("streamId")

	sm.mu.RLock()
	stream, exists := sm.streams[streamID]
	sm.mu.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Stream not found"})
		return
	}

	// Check if stream is actually running
	stream.mu.RLock()
	isRunning := stream.isRunning
	stream.mu.RUnlock()

	if !isRunning {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Stream not running"})
		return
	}

	// Wait for a frame with timeout
	timeout := time.After(5 * time.Second)
	select {
	case frame, ok := <-stream.frameBuffer:
		if !ok {
			// Channel closed
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Stream buffer closed"})
			return
		}
		// Return frame as binary data with headers
		c.Header("Content-Type", "application/octet-stream")
		c.Header("X-Frame-Timestamp", strconv.FormatInt(time.Now().UnixNano(), 10))
		c.Data(http.StatusOK, "application/octet-stream", frame)
	case <-timeout:
		// Instead of 408, return 204 No Content for smoother client experience
		c.Status(http.StatusNoContent)
	}
}
