import os
import time
from typing import Optional
import zmq
import cv2
import numpy as np

ZMQ_CONNECT = os.environ.get("ZMQ_CONNECT", "tcp://127.0.0.1:5555")
TOPIC = os.environ.get("TOPIC", "cam1").encode()


class FrameSubscriber:
    def __init__(self, connect: str, topic: bytes):
        ctx = zmq.Context.instance()
        self.sock = ctx.socket(zmq.SUB)
        self.sock.setsockopt(zmq.RCVHWM, 1)
        self.sock.setsockopt(zmq.CONFLATE, 1)  # keep only latest
        self.sock.connect(connect)
        self.sock.setsockopt(zmq.SUBSCRIBE, topic)

    def recv(self, timeout_ms: int = 1000) -> Optional[np.ndarray]:
        poller = zmq.Poller()
        poller.register(self.sock, zmq.POLLIN)
        socks = dict(poller.poll(timeout_ms))
        if self.sock in socks:
            parts = self.sock.recv_multipart(flags=zmq.NOBLOCK)
            # [topic, jpeg]
            if len(parts) >= 2:
                jpg = parts[1]
                img = cv2.imdecode(np.frombuffer(jpg, dtype=np.uint8), cv2.IMREAD_COLOR)
                return img
        return None


class ZmqCapture:
    """
    Drop-in-ish replacement for cv2.VideoCapture using ZeroMQ latest-frame delivery.
    Usage:
        cap = ZmqCapture(ZMQ_CONNECT, TOPIC, timeout_ms=1000)
        ok, frame = cap.read()
        cap.release()
    """

    def __init__(self, connect: str, topic: bytes, timeout_ms: int = 1000):
        self.sub = FrameSubscriber(connect, topic)
        self.timeout_ms = timeout_ms
        self._closed = False

    def isOpened(self) -> bool:
        return not self._closed

    def read(self):
        if self._closed:
            return False, None
        frame = self.sub.recv(timeout_ms=self.timeout_ms)
        if frame is None:
            return False, None
        return True, frame

    def release(self):
        # ZeroMQ sockets are GC'd; explicit close optional
        self._closed = True


def demo():
    cap = ZmqCapture(ZMQ_CONNECT, TOPIC, timeout_ms=2000)
    print(f"[consumer] Subscribed to {ZMQ_CONNECT} topic {TOPIC}")
    fps_n = 0
    t0 = time.time()
    while True:
        ok, frame = cap.read()
        if not ok:
            print("[consumer] timeout waiting frame")
            continue
        fps_n += 1
        if fps_n % 30 == 0:
            dt = time.time() - t0
            fps = fps_n / max(dt, 1e-3)
            print(f"[consumer] FPS ~ {fps:.1f}")
        cv2.imshow("consumer", frame)
        if cv2.waitKey(1) & 0xFF == ord('q'):
            break
    cap.release()


if __name__ == "__main__":
    demo()
