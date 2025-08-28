import os
import time
import threading
import subprocess
import signal
from typing import Optional, Dict, Any
import queue

import av
import numpy as np
import zmq
import cv2
from flask import Flask, Response, request, jsonify

# Env / defaults
ZMQ_BIND = os.environ.get("ZMQ_BIND", "tcp://127.0.0.1:5555")
MJPEG_PORT = int(os.environ.get("MJPEG_PORT", "8090"))
DEFAULT_PUB_FPS = float(os.environ.get("PUB_FPS", "20"))
DEFAULT_SCALE_WIDTH = int(os.environ.get("SCALE_WIDTH", "640"))
JPEG_QUALITY = int(os.environ.get("JPEG_QUALITY", "80"))
MEDIAMTX_RTSP = os.environ.get("MEDIAMTX_RTSP", "rtsp://127.0.0.1:8554")


app = Flask(__name__)


def cors(wrap):
    def _wrap(*args, **kwargs):
        resp = wrap(*args, **kwargs)
        if isinstance(resp, Response):
            r = resp
        else:
            r = app.make_response(resp)
        r.headers["Access-Control-Allow-Origin"] = "*"
        r.headers["Access-Control-Allow-Headers"] = "*"
        r.headers["Access-Control-Allow-Methods"] = "GET,POST,OPTIONS"
        return r

    _wrap.__name__ = wrap.__name__
    return _wrap


def open_input(rtsp_url: str) -> av.container.input.InputContainer:
    # Low-latency options for PyAV/FFmpeg
    opts = {
        "rtsp_transport": "tcp",
        "fflags": "nobuffer",
        "flags": "low_delay",
        "reorder_queue_size": "0",
        "max_delay": "0",
        "buffer_size": "1024",
        "stimeout": "5000000",  # 5s microseconds
        "rw_timeout": "5000000",
        "allowed_media_types": "video",
    }
    return av.open(rtsp_url, options=opts, timeout=5.0)


class CameraSession:
    def __init__(self, name: str, src_rtsp: str, pub_fps: float, scale_width: int):
        self.name = name
        self.src_rtsp = src_rtsp
        self.pub_fps = pub_fps
        self.scale_width = scale_width
        self.ffmpeg_proc = None
        self.pub_thread = None
        self.ffmpeg_watchdog_thread = None
        self.stop_event = threading.Event()
        self.latest_jpeg = None
        self.latest_lock = threading.Lock()

    def start_ffmpeg_publish(self):
        # Publish camera to MediaMTX as RTSP publisher at rtsp://localhost:8554/{name}
        out_url = f"{MEDIAMTX_RTSP}/{self.name}"
        args = [
            "ffmpeg",
            "-rtsp_transport", "tcp",
            "-fflags", "nobuffer",
            "-flags", "low_delay",
            "-stimeout", "5000000",
            "-i", self.src_rtsp,
            "-an",
            "-c:v", "copy",
            "-fflags", "+genpts",
            "-f", "rtsp",
            out_url,
        ]
        # Send stderr to DEVNULL to avoid pipe backpressure if not consumed
        self.ffmpeg_proc = subprocess.Popen(
            args,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            text=True,
        )

    def stop_ffmpeg(self):
        if self.ffmpeg_proc and self.ffmpeg_proc.poll() is None:
            try:
                self.ffmpeg_proc.send_signal(signal.SIGINT)
                try:
                    self.ffmpeg_proc.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    self.ffmpeg_proc.kill()
            except Exception:
                pass
        self.ffmpeg_proc = None

    def ffmpeg_watchdog(self):
        # Restart ffmpeg publish if it dies
        while not self.stop_event.is_set():
            if self.ffmpeg_proc is None or self.ffmpeg_proc.poll() is not None:
                try:
                    self.start_ffmpeg_publish()
                except Exception as e:
                    print(f"[gateway:{self.name}] ffmpeg start error: {e}")
                time.sleep(2)
            else:
                time.sleep(1)

    def publisher_loop(self):
        in_url = f"{MEDIAMTX_RTSP}/{self.name}"
        next_pub = time.time()
        while not self.stop_event.is_set():
            try:
                container = open_input(in_url)
                stream = next(s for s in container.streams if s.type == "video")
                stream.thread_type = "AUTO"
                stream.codec_context.skip_frame = "NONKEY"
                for frame in container.decode(stream):
                    if self.stop_event.is_set():
                        break
                    img = frame.to_ndarray(format="bgr24")
                    if self.scale_width > 0 and img.shape[1] > self.scale_width:
                        h = int(img.shape[0] * self.scale_width / img.shape[1])
                        img = cv2.resize(img, (self.scale_width, h), interpolation=cv2.INTER_AREA)
                    now = time.time()
                    if now >= next_pub:
                        ok, buf = cv2.imencode(".jpg", img, [int(cv2.IMWRITE_JPEG_QUALITY), JPEG_QUALITY])
                        if ok:
                            b = buf.tobytes()
                            # Enqueue for the global PUB thread (drop if full)
                            Gateway.get().publish(self.name.encode(), b)
                            with self.latest_lock:
                                self.latest_jpeg = b
                        next_pub = now + (1.0 / max(self.pub_fps, 1.0))
            except Exception as e:
                if self.stop_event.is_set():
                    break
                print(f"[gateway:{self.name}] decode error: {e}; retry in 2s")
                time.sleep(2)
            finally:
                try:
                    container.close()  # type: ignore[name-defined]
                except Exception:
                    pass

    def start(self):
        self.stop_event.clear()
        self.start_ffmpeg_publish()
        self.pub_thread = threading.Thread(target=self.publisher_loop, daemon=True)
        self.pub_thread.start()
        self.ffmpeg_watchdog_thread = threading.Thread(target=self.ffmpeg_watchdog, daemon=True)
        self.ffmpeg_watchdog_thread.start()

    def stop(self):
        self.stop_event.set()
        if self.pub_thread and self.pub_thread.is_alive():
            self.pub_thread.join(timeout=2)
        if self.ffmpeg_watchdog_thread and self.ffmpeg_watchdog_thread.is_alive():
            self.ffmpeg_watchdog_thread.join(timeout=2)
        self.stop_ffmpeg()


class Gateway:
    _instance = None

    def __init__(self):
        self.ctx = zmq.Context.instance()
        self.pub_socket = self.ctx.socket(zmq.PUB)
        self.pub_socket.setsockopt(zmq.SNDHWM, 1)
        self.pub_socket.bind(ZMQ_BIND)
        self.sessions: Dict[str, CameraSession] = {}
        # Map RTSP URL -> camera name to avoid duplicates
        self.by_url: Dict[str, str] = {}
        self.lock = threading.Lock()
        # Sender thread and queue for ZeroMQ PUB
        self._queue = queue.Queue(maxsize=1000)  # type: ignore[var-annotated]
        self._stop = threading.Event()
        self._sender = threading.Thread(target=self._pub_sender, daemon=True)
        self._sender.start()

    @classmethod
    def get(cls):
        if cls._instance is None:
            cls._instance = Gateway()
        return cls._instance

    def _pub_sender(self):
        # Owns pub_socket in a single thread (ZeroMQ sockets are not thread-safe)
        while not self._stop.is_set():
            try:
                topic, payload = self._queue.get(timeout=0.5)
            except queue.Empty:
                continue
            try:
                self.pub_socket.send_multipart([topic, payload], flags=zmq.NOBLOCK)
            except zmq.Again:
                pass

    def publish(self, topic: bytes, payload: bytes):
        try:
            self._queue.put_nowait((topic, payload))
        except queue.Full:
            # drop frame if queue is full to keep latency low
            pass

    def start_camera(self, name: str, rtsp: str, pub_fps: Optional[float] = None, scale_width: Optional[int] = None) -> Dict[str, Any]:
        with self.lock:
            # If this exact RTSP URL is already active under another name, return that session
            existing_by_url = self.by_url.get(rtsp)
            if existing_by_url:
                n = existing_by_url
                s = self.sessions.get(n)
                return {
                    "ok": True,
                    "message": "already running (by url)",
                    "name": n,
                    "rtsp_in": s.src_rtsp if s else rtsp,
                    "mediamtx_path": f"{MEDIAMTX_RTSP}/{n}",
                    "webrtc_whep": f"http://127.0.0.1:8889/whep/{n}",
                    "hls": f"http://127.0.0.1:8888/{n}/index.m3u8",
                    "zmq_topic": n,
                }

            # If the requested name is already running, return it as-is (don't mutate existing)
            if name in self.sessions:
                s = self.sessions[name]
                return {
                    "ok": True,
                    "message": "already running",
                    "name": name,
                    "rtsp_in": s.src_rtsp,
                    "mediamtx_path": f"{MEDIAMTX_RTSP}/{name}",
                    "webrtc_whep": f"http://127.0.0.1:8889/whep/{name}",
                    "hls": f"http://127.0.0.1:8888/{name}/index.m3u8",
                    "zmq_topic": name,
                }
            pub_fps = pub_fps if pub_fps is not None else DEFAULT_PUB_FPS
            scale_width = scale_width if scale_width is not None else DEFAULT_SCALE_WIDTH
            sess = CameraSession(name, rtsp, pub_fps, scale_width)
            self.sessions[name] = sess
            self.by_url[rtsp] = name
        sess.start()
        return {
            "ok": True,
            "name": name,
            "rtsp_in": rtsp,
            "mediamtx_path": f"{MEDIAMTX_RTSP}/{name}",
            "webrtc_whep": f"http://127.0.0.1:8889/whep/{name}",
            "hls": f"http://127.0.0.1:8888/{name}/index.m3u8",
            "zmq_topic": name,
        }

    def stop_camera(self, name: str) -> Dict[str, Any]:
        with self.lock:
            sess = self.sessions.pop(name, None)
        if not sess:
            return {"ok": True, "message": "not running"}
        # remove URL index if matching
        with self.lock:
            if self.by_url.get(sess.src_rtsp) == name:
                self.by_url.pop(sess.src_rtsp, None)
        sess.stop()
        return {"ok": True}

    def list_cameras(self) -> Dict[str, Any]:
        with self.lock:
            out = []
            for n, s in self.sessions.items():
                out.append({
                    "name": n,
                    "rtsp_in": s.src_rtsp,
                    "pub_fps": s.pub_fps,
                    "scale_width": s.scale_width,
                    "webrtc_whep": f"http://127.0.0.1:8889/whep/{n}",
                    "hls": f"http://127.0.0.1:8888/{n}/index.m3u8",
                    "zmq_topic": n,
                })
        return {"ok": True, "cameras": out}


gateway = Gateway.get()


@app.route("/cameras/start", methods=["POST", "OPTIONS"])
@cors
def http_start():
    if request.method == "OPTIONS":
        return ("", 200)
    data = request.get_json(silent=True) or {}
    name = data.get("name")
    rtsp = data.get("rtsp")
    pub_fps = data.get("pub_fps")
    scale_width = data.get("scale_width")
    if not name or not rtsp:
        return jsonify({"ok": False, "error": "name and rtsp required"}), 400
    # Simple name validation
    safe = all(c.isalnum() or c in ("-", "_") for c in name)
    if not safe:
        return jsonify({"ok": False, "error": "invalid name"}), 400
    res = gateway.start_camera(name, rtsp, pub_fps, scale_width)
    return jsonify(res)


@app.route("/cameras/stop", methods=["POST", "OPTIONS"])
@cors
def http_stop():
    if request.method == "OPTIONS":
        return ("", 200)
    data = request.get_json(silent=True) or {}
    name = data.get("name")
    if not name:
        return jsonify({"ok": False, "error": "name required"}), 400
    res = gateway.stop_camera(name)
    return jsonify(res)


@app.route("/cameras", methods=["GET", "OPTIONS"])
@cors
def http_list():
    if request.method == "OPTIONS":
        return ("", 200)
    return jsonify(gateway.list_cameras())


@app.route("/mjpeg", methods=["GET", "OPTIONS"])
@cors
def mjpeg():
    if request.method == "OPTIONS":
        return ("", 200)
    name = request.args.get("cam")
    if not name:
        return jsonify({"ok": False, "error": "cam query param required"}), 400
    with gateway.lock:
        sess = gateway.sessions.get(name)
    if not sess:
        return jsonify({"ok": False, "error": "camera not running"}), 404

    def gen():
        boundary = "frame"
        while True:
            if sess.stop_event.is_set():
                break
            with sess.latest_lock:
                b = sess.latest_jpeg
            if b is None:
                time.sleep(0.05)
                continue
            yield (b"--" + boundary.encode() + b"\r\n"
                   b"Content-Type: image/jpeg\r\n\r\n" + b + b"\r\n")
            time.sleep(1.0 / max(sess.pub_fps, 1.0))

    return Response(gen(), mimetype="multipart/x-mixed-replace; boundary=frame")


def main():
    print(f"[gateway] ZMQ PUB on {ZMQ_BIND} | MJPEG http://127.0.0.1:{MJPEG_PORT}/mjpeg?cam=<name> | MediaMTX {MEDIAMTX_RTSP}")
    app.run(host="0.0.0.0", port=MJPEG_PORT, debug=False, threaded=True)


if __name__ == "__main__":
    main()