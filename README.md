# RTSP Stream Server

A high-performance, single-ingest RTSP stream server built in Go that eliminates network congestion and CPU spikes by providing one RTSP connection per camera with fan-out to multiple clients. Perfect for computer vision applications, surveillance systems, and real-time video processing.

## Features

- **Single Ingest per Camera**: One RTSP connection per camera saves bandwidth and CPU
- **Fan-out Architecture**: Multiple clients can consume the same stream without duplicate camera pulls
- **Low Latency**: Direct frame distribution without HLS conversion
- **Multiple Client Support**: WebSocket for web/React apps, HTTP API for Python/OpenCV
- **Real-time Statistics**: Monitor stream health, FPS, and client connections
- **Automatic Reconnection**: Robust error handling and reconnection logic
- **Cross-Platform**: Works on macOS, Linux, and Windows

## Architecture

```
RTSP Camera → Go Server (Single Ingest) → Multiple Clients
                    ↓
              ┌─────────────┐
              │   FFmpeg    │ (RTSP to Raw Frames)
              └─────────────┘
                    ↓
              ┌─────────────┐
              │ Frame Buffer│ (BGR24 frames)
              └─────────────┘
                    ↓
           ┌────────┬────────┬────────┐
           │ Wails  │Python  │Python  │
           │ React  │OpenCV  │OpenCV  │
           │   App  │ Script │ Script │
           └────────┴────────┴────────┘
```

## Prerequisites

- Go 1.21 or higher
- FFmpeg installed and in PATH
- Python 3.7+ (for Python clients)
- OpenCV-Python (for Python clients)

### Installing FFmpeg

**macOS (using Homebrew):**
```bash
brew install ffmpeg
```

**Ubuntu/Debian:**
```bash
sudo apt update
sudo apt install ffmpeg
```

**Windows:**
Download from [https://ffmpeg.org/download.html](https://ffmpeg.org/download.html) and add to PATH.

## Quick Start

1. **Clone and build the server:**
```bash
git clone <repository-url>
cd rtsp-stream-server
go mod tidy
go build -o rtsp-server main.go
```

2. **Start the server:**
```bash
./rtsp-server
```

The server will start on `http://localhost:8080`

3. **Start a stream:**
```bash
curl -X POST http://localhost:8080/api/streams \
  -H "Content-Type: application/json" \
  -d '{
    "stream_id": "camera1",
    "rtsp_url": "rtsp://your-camera-ip:554/stream1",
    "width": 640,
    "height": 480
  }'
```

4. **Connect clients:**
   - **Python/OpenCV**: Run `python3 python_client.py`
   - **Web/React**: Include `js_client.js` and use `RTSPStreamViewer` component

## API Reference

### Start Stream
```http
POST /api/streams
Content-Type: application/json

{
  "stream_id": "camera1",
  "rtsp_url": "rtsp://admin:password@192.168.1.100:554/stream1",
  "width": 640,
  "height": 480
}
```

### Stop Stream
```http
DELETE /api/streams/{streamId}
```

### List Streams
```http
GET /api/streams
```

### Get Stream Statistics
```http
GET /api/streams/{streamId}/stats
```

### Get Latest Frame (HTTP - for Python)
```http
GET /api/streams/{streamId}/frame
```

### WebSocket Connection (for JavaScript/React)
```
WS /ws/{streamId}
```

## Client Usage

### Python/OpenCV Client

```python
from python_client import RTSPStreamClient

# Replace with your RTSP URL
RTSP_URL = "rtsp://admin:password@192.168.1.100:554/stream1"

with RTSPStreamClient("http://localhost:8080", "camera1") as client:
    # Start the stream
    client.start_stream(RTSP_URL, width=640, height=480)
    
    # Start receiving frames
    client.start_receiving()
    
    # Process frames
    while True:
        frame = client.get_frame(timeout=2.0)
        if frame is None:
            continue
            
        # Your computer vision processing here
        gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
        edges = cv2.Canny(gray, 50, 150)
        
        cv2.imshow('Original', frame)
        cv2.imshow('Edges', edges)
        
        if cv2.waitKey(1) & 0xFF == ord('q'):
            break
```

### JavaScript/React Client

```javascript
import RTSPStreamViewer from './RTSPStreamViewer';

function App() {
  return (
    <div>
      <RTSPStreamViewer
        serverUrl="ws://localhost:8080"
        streamId="camera1"
        rtspUrl="rtsp://admin:password@192.168.1.100:554/stream1"
        width={640}
        height={480}
        autoStart={true}
      />
    </div>
  );
}
```

### Wails Integration

1. Include the JavaScript client in your Wails frontend:
```html
<script src="js_client.js"></script>
```

2. Use the React component or raw JavaScript API:
```javascript
const client = new RTSPStreamClient('ws://localhost:8080', 'camera1');
await client.startStream('rtsp://your-camera-url', 640, 480);
client.connect();
```

## Configuration

### Environment Variables

- `PORT`: Server port (default: 8080)
- `LOG_LEVEL`: Logging level (debug, info, warn, error)

### Stream Parameters

- **width/height**: Output resolution (default: 640x480)
- **frame_buffer_size**: Frames to buffer per stream (default: 100)
- **client_buffer_size**: Frames to buffer per client (default: 10)

## Performance Optimization

### For High Frame Rates (>30 FPS)
- Increase buffer sizes
- Use lower resolution if possible
- Consider hardware acceleration

### For Multiple Cameras
- Each camera uses one FFmpeg process
- Memory usage: ~20MB per camera stream
- CPU usage: ~5-10% per camera on modern hardware

### Network Optimization
- Use TCP transport for RTSP (default in this server)
- Monitor buffer sizes to prevent memory buildup
- Consider frame dropping for slow clients

## Troubleshooting

### Common Issues

1. **"FFmpeg not found"**
   - Install FFmpeg and ensure it's in your PATH
   - Test with: `ffmpeg -version`

2. **"Stream failed to start"**
   - Verify RTSP URL is accessible
   - Check camera credentials and network connectivity
   - Try different RTSP transport methods

3. **"Frames arriving too slowly"**
   - Check network latency to camera
   - Reduce resolution or frame rate
   - Verify server has sufficient CPU/memory

4. **"WebSocket connection failed"**
   - Ensure server is running on correct port
   - Check firewall settings
   - Verify stream is active before connecting

### Debug Mode

Start the server with debug logging:
```bash
LOG_LEVEL=debug ./rtsp-server
```

### Testing with Sample Streams

Use public test streams for development:
```bash
# Big Buck Bunny test stream
curl -X POST http://localhost:8080/api/streams \
  -H "Content-Type: application/json" \
  -d '{
    "stream_id": "test",
    "rtsp_url": "rtsp://wowzaec2demo.streamlock.net/vod/mp4:BigBuckBunny_115k.mp4",
    "width": 640,
    "height": 480
  }'
```

## Production Deployment

### Docker Deployment

```dockerfile
FROM golang:1.21-alpine AS builder
RUN apk add --no-cache ffmpeg
WORKDIR /app
COPY . .
RUN go mod tidy && go build -o rtsp-server main.go

FROM alpine:latest
RUN apk add --no-cache ffmpeg
WORKDIR /app
COPY --from=builder /app/rtsp-server .
EXPOSE 8080
CMD ["./rtsp-server"]
```

### Systemd Service

```ini
[Unit]
Description=RTSP Stream Server
After=network.target

[Service]
Type=simple
User=rtsp
WorkingDirectory=/opt/rtsp-server
ExecStart=/opt/rtsp-server/rtsp-server
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### Load Balancing

For high availability, run multiple instances behind a load balancer:
- Use sticky sessions for WebSocket connections
- Share stream state via Redis or database
- Implement health checks

## License

MIT License - see LICENSE file for details.

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## Support

- GitHub Issues: Report bugs and feature requests
- Discussions: Ask questions and share ideas
- Wiki: Additional documentation and examples
