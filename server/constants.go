package main

import "time"

// Server configuration constants
const (
	// ServerPort is the port on which the RTSP server listens
	ServerPort = ":8091"

	// FrameBufferSize is the maximum number of frames to buffer per stream
	FrameBufferSize = 100

	// ClientBufferSize is the maximum number of frames to buffer per client
	ClientBufferSize = 10

	// DefaultWidth is the default frame width when not specified
	DefaultWidth = 640

	// DefaultHeight is the default frame height when not specified
	DefaultHeight = 480

	// HealthCheckInterval is how often to check stream health
	HealthCheckInterval = 5 * time.Second

	// MaxStallDuration is the maximum time allowed without frames before restart
	MaxStallDuration = 10 * time.Second

	// FFmpegRestartDelay is the delay before restarting FFmpeg after an error
	FFmpegRestartDelay = 2 * time.Second

	// GracefulShutdownDelay is the time to wait for FFmpeg to stop gracefully
	GracefulShutdownDelay = 100 * time.Millisecond

	// WebSocketPingInterval is how often to send ping messages to clients
	WebSocketPingInterval = 54 * time.Second

	// WebSocketReadDeadline is the deadline for reading WebSocket messages
	WebSocketReadDeadline = 60 * time.Second

	// WebSocketWriteDeadline is the deadline for writing WebSocket messages
	WebSocketWriteDeadline = 10 * time.Second

	// WebSocketReadLimit is the maximum message size for incoming WebSocket messages
	WebSocketReadLimit = 512

	// FrameRequestTimeout is the timeout for HTTP frame requests
	FrameRequestTimeout = 5 * time.Second
)
