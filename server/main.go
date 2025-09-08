package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

// main initializes and starts the RTSP streaming server
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
		api.DELETE("/streams/:streamId/force", sm.handleForceStopStream)
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
		log.Println("  DELETE /api/streams/:streamId - Stop a stream (only if no clients)")
		log.Println("  DELETE /api/streams/:streamId/force - Force stop a stream")
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
