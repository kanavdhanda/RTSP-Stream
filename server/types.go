package main

import (
	"context"
	"os/exec"
	"sync"
	"time"

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
	rtspURL        string
	streamID       string
	cmd            *exec.Cmd
	frameBuffer    chan []byte
	clients        map[string]*Client
	clientsMu      sync.RWMutex
	isRunning      bool
	cancelFunc     context.CancelFunc
	lastFrameTime  time.Time
	frameCount     int64
	mu             sync.RWMutex
	healthStopChan chan struct{}
}

// Client represents a connected client consuming a stream
type Client struct {
	id       string
	streamID string
	conn     *websocket.Conn
	send     chan []byte
	manager  *StreamManager
	closed   bool
	mu       sync.Mutex
}

// FrameMessage represents the frame data sent to clients
type FrameMessage struct {
	StreamID  string `json:"stream_id"`
	Timestamp int64  `json:"timestamp"`
	FrameData []byte `json:"frame_data"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}
