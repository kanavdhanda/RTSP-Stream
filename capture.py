"""
Gateway-backed VideoCapture replacement.

- Starts a camera via HTTP on the gateway (dedup by RTSP URL).
- Subscribes to the gateway ZeroMQ PUB to receive latest frames.
- Exposes a minimal cv2.VideoCapture-like API: isOpened(), read(), release().

Requires gateway.py + MediaMTX running, and Python deps from rtsp/requirements.txt.
"""

from __future__ import annotations

import os
import time
from dataclasses import dataclass
from typing import Any, Dict, Optional, Tuple

import cv2
import numpy as np
import requests
import zmq


DEFAULT_GATEWAY_BASE = os.getenv("GATEWAY_BASE", "http://127.0.0.1:8090")
DEFAULT_ZMQ_CONNECT = os.getenv("ZMQ_CONNECT", "tcp://127.0.0.1:5555")


def start_camera(
    name: str,
    rtsp: str,
    base: str = DEFAULT_GATEWAY_BASE,
    pub_fps: int = 20,
    scale_width: int = 640,
    retries: int = 3,
    backoff_ms: int = 500,
) -> Dict[str, Any]:
    url = f"{base}/cameras/start"
    payload = {"name": name, "rtsp": rtsp, "pub_fps": pub_fps, "scale_width": scale_width}
    last_err: Dict[str, Any] | None = None
    for attempt in range(retries + 1):
        try:
            r = requests.post(url, json=payload, timeout=5)
            if r.ok:
                return r.json()
            last_err = {"ok": False, "status": r.status_code, "text": r.text}
        except Exception as e:  # noqa: BLE001
            last_err = {"ok": False, "error": str(e)}
        if attempt < retries:
            delay = min((backoff_ms / 1000.0) * (2**attempt), 5.0)
            time.sleep(delay)
    return last_err or {"ok": False, "error": "unknown"}


class _FrameSubscriber:
    def __init__(self, connect: str, topic: bytes):
        ctx = zmq.Context.instance()
        self.sock = ctx.socket(zmq.SUB)
        self.sock.setsockopt(zmq.RCVHWM, 1)
        self.sock.setsockopt(zmq.CONFLATE, 1)
        self.sock.connect(connect)
        self.sock.setsockopt(zmq.SUBSCRIBE, topic)

    def recv(self, timeout_ms: int) -> Optional[np.ndarray]:
        poller = zmq.Poller()
        poller.register(self.sock, zmq.POLLIN)
        socks = dict(poller.poll(timeout_ms))
        if self.sock in socks:
            parts = self.sock.recv_multipart(flags=zmq.NOBLOCK)
            if len(parts) >= 2:
                jpg = parts[1]
                img = cv2.imdecode(np.frombuffer(jpg, dtype=np.uint8), cv2.IMREAD_COLOR)
                return img
        return None


@dataclass
class CaptureInfo:
    name: str
    topic: bytes
    whep_url: Optional[str]
    hls_url: Optional[str]


class GatewayVideoCapture:
    """
    Minimal cv2.VideoCapture-like adapter backed by gateway ZeroMQ frames.
    """

    def __init__(
        self,
        rtsp_url: str,
        name: Optional[str] = None,
        gateway_base: str = DEFAULT_GATEWAY_BASE,
        zmq_connect: str = DEFAULT_ZMQ_CONNECT,
        timeout_ms: int = 1000,
        pub_fps: int = 20,
        scale_width: int = 640,
    ) -> None:
        # choose a deterministic stream name if not provided
        if not name:
            # sanitize from URL host/path
            base = rtsp_url.split("@")[ -1 ] if "@" in rtsp_url else rtsp_url
            base = base.replace("rtsp://", "").replace("/", "-").replace(":", "-")
            name = base[:48].lower()

        info = start_camera(name=name, rtsp=rtsp_url, base=gateway_base, pub_fps=pub_fps, scale_width=scale_width)
        if not info or not info.get("ok", True):
            raise RuntimeError(f"failed to start camera: {info}")
        can_name = info.get("name", name)
        topic = can_name.encode()
        self.info = CaptureInfo(
            name=can_name,
            topic=topic,
            whep_url=info.get("webrtc_whep"),
            hls_url=info.get("hls"),
        )
        self.sub = _FrameSubscriber(zmq_connect, topic)
        self.timeout_ms = timeout_ms
        self._closed = False

    def isOpened(self) -> bool:  # noqa: N802 (OpenCV method casing)
        return not self._closed

    def read(self) -> Tuple[bool, Optional[np.ndarray]]:  # noqa: N802
        if self._closed:
            return False, None
        frame = self.sub.recv(self.timeout_ms)
        if frame is None:
            return False, None
        return True, frame

    def release(self) -> None:  # noqa: N802
        self._closed = True


def open_capture(
    rtsp_url: str,
    name: Optional[str] = None,
    gateway_base: str = DEFAULT_GATEWAY_BASE,
    zmq_connect: str = DEFAULT_ZMQ_CONNECT,
    timeout_ms: int = 1000,
    pub_fps: int = 20,
    scale_width: int = 640,
) -> GatewayVideoCapture:
    return GatewayVideoCapture(
        rtsp_url=rtsp_url,
        name=name,
        gateway_base=gateway_base,
        zmq_connect=zmq_connect,
        timeout_ms=timeout_ms,
        pub_fps=pub_fps,
        scale_width=scale_width,
    )
