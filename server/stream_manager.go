package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"time"

	"github.com/gorilla/websocket"
)

// NewStreamManager creates a new instance of StreamManager
func NewStreamManager() *StreamManager {
	return &StreamManager{
		streams: make(map[string]*Stream),
		clients: make(map[string]map[string]*Client),
	}
}

// generateClientID generates a unique client ID
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
		rtspURL:        rtspURL,
		streamID:       streamID,
		frameBuffer:    make(chan []byte, 100), // Buffer up to 100 frames
		clients:        make(map[string]*Client),
		cancelFunc:     cancel,
		isRunning:      false,
		healthStopChan: make(chan struct{}),
	}

	sm.streams[streamID] = stream
	sm.clients[streamID] = make(map[string]*Client)

	go sm.runFFmpegStream(ctx, stream, width, height)
	go sm.distributeFrames(stream)
	go sm.monitorStreamHealth(stream, width, height)

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

// startFFmpeg initializes and starts the FFmpeg process for a stream
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

			// Improved buffer: drop oldest frame if full
			select {
			case stream.frameBuffer <- frame:
				stream.mu.Lock()
				stream.lastFrameTime = time.Now()
				stream.frameCount++
				stream.mu.Unlock()
			default:
				// Buffer full, drop oldest frame and insert new
				select {
				case <-stream.frameBuffer:
				default:
				}
				stream.frameBuffer <- frame
				stream.mu.Lock()
				stream.lastFrameTime = time.Now()
				stream.frameCount++
				stream.mu.Unlock()
				log.Printf("Frame buffer full for stream %s, dropped oldest frame", stream.streamID)
			}
		}
	}
}

// distributeFrames sends frames from buffer to all connected clients
func (sm *StreamManager) distributeFrames(stream *Stream) {
	defer log.Printf("Frame distribution stopped for stream %s", stream.streamID)

	for frame := range stream.frameBuffer {
		stream.clientsMu.RLock()
		clients := make([]*Client, 0, len(stream.clients))
		for _, client := range stream.clients {
			clients = append(clients, client)
		}
		stream.clientsMu.RUnlock()

		// Send frame to all clients
		for _, client := range clients {
			// Check if client is still active before sending
			client.mu.Lock()
			if !client.closed {
				select {
				case client.send <- frame:
				default:
					// Client buffer full, skip
					log.Printf("Client %s buffer full, skipping frame", client.id)
				}
			}
			client.mu.Unlock()
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

	// Stop health monitor
	close(stream.healthStopChan)

	// Wait a bit for FFmpeg to stop gracefully
	time.Sleep(100 * time.Millisecond)

	// Close frame buffer
	close(stream.frameBuffer)

	// Disconnect all clients safely
	for _, client := range sm.clients[streamID] {
		client.mu.Lock()
		if !client.closed {
			client.closed = true
			close(client.send)
		}
		client.mu.Unlock()
		client.conn.Close()
	}

	// Cleanup
	delete(sm.streams, streamID)
	delete(sm.clients, streamID)

	log.Printf("Stopped stream %s", streamID)
	log.Printf("Frame distribution stopped for stream %s", streamID)
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

	// Protect against double removal
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return
	}
	client.closed = true
	client.mu.Unlock()

	if stream, exists := sm.streams[client.streamID]; exists {
		stream.clientsMu.Lock()
		delete(stream.clients, client.id)
		stream.clientsMu.Unlock()

		// Auto-cleanup: if no clients left, optionally stop the stream
		// This is commented out to prevent automatic cleanup, but can be enabled if desired
		/*
			clientCount := len(stream.clients)
			if clientCount == 0 {
				log.Printf("No clients left for stream %s, stopping stream", client.streamID)
				go func() {
					// Use a goroutine to avoid deadlock since we already hold sm.mu
					time.Sleep(100 * time.Millisecond) // Small delay to ensure cleanup
					sm.StopStream(client.streamID)
				}()
			}
		*/
	}

	delete(sm.clients[client.streamID], client.id)

	// Safely close the send channel
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

// monitorStreamHealth checks if frames are being received and restarts FFmpeg if stalled
func (sm *StreamManager) monitorStreamHealth(stream *Stream, width, height int) {
	const healthCheckInterval = 5 * time.Second
	const maxStallDuration = 10 * time.Second
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stream.healthStopChan:
			return
		case <-ticker.C:
			stream.mu.RLock()
			lastFrame := stream.lastFrameTime
			running := stream.isRunning
			stream.mu.RUnlock()
			if running && time.Since(lastFrame) > maxStallDuration {
				log.Printf("Health monitor: Stream %s stalled, restarting FFmpeg", stream.streamID)
				// Restart FFmpeg by cancelling and starting again
				stream.cancelFunc()
				// Create new context and cancelFunc
				ctx, cancel := context.WithCancel(context.Background())
				stream.mu.Lock()
				stream.cancelFunc = cancel
				stream.isRunning = false
				stream.mu.Unlock()
				go sm.runFFmpegStream(ctx, stream, width, height)
			}
		}
	}
}
