# RTSP single-ingest, multi-consumer pipeline

This folder contains a lightweight gateway and clients that pull each camera RTSP once and fan out to:

- Python CV tasks: via a low-latency ZeroMQ PUB/SUB of decoded frames, with a drop-to-latest policy to prevent gray/blocked frames under load.
- Frontend: via MediaMTX (rtsp-simple-server) providing WebRTC (sub-second) and LL-HLS fallback.
Optional: an MJPEG endpoint you can open in a browser to verify delivery.

Why: OpenCV’s direct RTSP capture often gray-screens when CPU spikes or network jitter occurs because the decoder misses reference frames. By centralizing ingest and decoding (PyAV/FFmpeg or GStreamer) and enforcing small buffers and “drop late frames,” consumers stay real-time and robust.

## Overview

- Single ingest: MediaMTX pulls RTSP from the camera. It can re-publish as RTSP/WebRTC/HLS.
- Python gateway (gateway.py) subscribes from MediaMTX (or directly from camera) using PyAV with low-latency flags and publishes JPEG frames over ZeroMQ PUB.
- CV apps subscribe (consumer_example.py or your existing pipeline) and always get the latest frame with minimal buffering.
- Frontend connects to MediaMTX via WebRTC (WHEP) for sub-second latency, or LL-HLS as fallback.

## Components

- gateway.py — ingest per camera (ffmpeg copy), decode with PyAV, publish JPEG via ZeroMQ; exposes HTTP API and MJPEG debug.
- capture.py — Python VideoCapture-like adapter backed by the gateway (start via HTTP, frames via ZMQ).
- consumer_example.py — Low-level ZMQ example (optional).
- ReactWhepSample.tsx — Self-contained React WebRTC (WHEP) sample component.
- requirements.txt — Python dependencies for gateway/clients.

## Quick start

1) Install MediaMTX (rtsp-simple-server)
- macOS: download binary from https://github.com/bluenviron/mediamtx/releases and place it in your PATH.
- Copy `mediamtx.yaml` somewhere writable and set your RTSP URL.

2) MediaMTX
- You can run with default ports; a custom mediamtx.yaml is optional (only needed for custom ports/auth/TLS).

3) Python gateway
- Create a venv and install requirements.
- Run `gateway.py` to publish frames and optional MJPEG.

4) CV consumer
- Use `consumer_example.py` or wire the `FrameSubscriber` into your existing recognition loop instead of OpenCV VideoCapture. It always yields the most recent frame with no backlog.

5) Frontend
- Connect to MediaMTX via WebRTC (WHEP) for lowest latency, or LL-HLS as fallback. See ReactWhepSample.tsx below.

## HTTP API (gateway)

Base URL: http://127.0.0.1:8090

- POST /cameras/start
	- Body JSON: { "name": "lobby-1", "rtsp": "rtsp://user:pass@ip/stream", "pub_fps"?: 20, "scale_width"?: 640 }
	- Returns: { ok, name, mediamtx_path, webrtc_whep, hls, zmq_topic }

- POST /cameras/stop
	- Body JSON: { "name": "lobby-1" }
	- Returns: { ok }

- GET /cameras
	- Returns: { ok, cameras: [ { name, webrtc_whep, hls, zmq_topic, ... } ] }

- GET /mjpeg?cam=name
	- Returns MJPEG multipart stream for quick debugging.

Notes
- CORS is enabled. You can call these endpoints from your Wails/React app.
- Each camera is restreamed as RTSP at rtsp://127.0.0.1:8554/{name} by MediaMTX, and also exposed via WebRTC WHEP and LL-HLS.

## Replace OpenCV VideoCapture (Python)

```python
from capture import open_capture

cap = open_capture(
	rtsp_url="rtsp://user:pass@192.168.1.10/Streaming/Channels/101",
	timeout_ms=2000,
	pub_fps=20,
	scale_width=640,
)

while True:
	ok, frame = cap.read()
	if not ok:
		continue
	# ... process frame (BGR ndarray) ...
```

Notes
- This will POST /cameras/start first (idempotent, de-dup by RTSP URL), then consume latest frames via ZeroMQ.
- Multiple Python services can read the same topic with no extra decode cost.

## Frontend integration (React WHEP)

Use WebRTC WHEP for sub-second latency with a video element that autoplays without controls.

Steps:
1) Call POST /cameras/start from your UI to ensure the camera is running (or rely on a backend trigger).
2) Use a small WHEP helper to attach the stream to a <video> element.
3) Keep the <video> muted, playsInline, and autoplay for seamless playback.

See `ReactWhepSample.tsx` in this folder. It ensures the camera is started and connects to WHEP with auto-reconnect.

Behavior
- No play/pause controls; it connects and plays continuously, auto-reconnecting on errors.
- If WebRTC is unavailable, you can fall back to LL-HLS using hls.js, but latency will increase.

## Low-level Python integration

If you prefer manual control, see `consumer_example.py`.

## Scaling to 8+ cameras

- ffmpeg restream uses copy mode (no transcode) by default → low CPU.
- PyAV decoders use low-latency flags and drop-late; publisher drops frames when needed -> no backlog.
- Use scale_width=640–960 and pub_fps=15–20 for efficient processing.
- Each camera has a watchdog to restart ffmpeg if it dies.

## Tuning options

- PUB_FPS: lower to reduce CPU/network; raise for smoother motion.
- SCALE_WIDTH: scale down to reduce CPU; models often work fine at 640p.
- JPEG_QUALITY: 75–85 for balance.
- If a camera requires transcode for compatibility, adjust the ffmpeg args in `gateway.py` for that camera.

## Troubleshooting

- If you see import errors for av/zmq, run: `pip install -r rtsp/requirements.txt` in your venv.
- If WebRTC doesn’t connect in browser, check MediaMTX is running and your browser can reach http://127.0.0.1:8889, and test on the same machine to avoid NAT issues.
- For LL-HLS, ensure MediaMTX HLS is enabled and reachable at http://127.0.0.1:8888/{name}/index.m3u8.

## Tuning tips (latency/robustness)

- Prefer RTSP over TCP (not UDP) for reliability: `rtsp_transport=tcp`.
- Keep decode buffer tiny; drop late frames (we set PyAV options, and use a queue size=1 with overwrite).
- If CPU becomes a bottleneck, scale down in the gateway (`SCALE_WIDTH`) and publish fewer FPS (`PUB_FPS`).
- For web playback, WebRTC is the only practical sub-second option; LL-HLS usually sits around 2–5s.

## Notes

- The gateway de-duplicates by RTSP URL; starting the same URL again returns the existing canonical name.
- MediaMTX defaults are fine; custom config is optional for ports/auth/TLS.
# RTSP-Stream
