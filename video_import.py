#!/usr/bin/env python3
"""
Simple drop-in replacement for cv2.VideoCapture
Just import this function and use it exactly like cv2.VideoCapture
"""

import cv2
import numpy as np
import requests
import time
import threading
from queue import Queue, Empty
import hashlib
import logging

# Set up logging
logging.basicConfig(level=logging.WARNING)  # Only show warnings/errors
logger = logging.getLogger(__name__)

class SimpleVideoCapture:
    """Drop-in replacement for cv2.VideoCapture that uses RTSP server"""
    
    # Class-level cache to track active streams
    _active_streams = {}
    _stream_lock = threading.Lock()
    
    def __init__(self, rtsp_url, server_url="http://localhost:8091"):
        self.rtsp_url = rtsp_url
        self.server_url = server_url.rstrip('/')
        self.width = 640
        self.height = 480
        self.frame_queue = Queue(maxsize=2)
        self.current_frame = None
        self.running = False
        self.session = requests.Session()
        self.session.timeout = (2, 5)
        
        # Generate stream ID from URL (same as server)
        hasher = hashlib.md5()
        hasher.update(rtsp_url.encode())
        self.stream_id = f"stream_{hasher.hexdigest()[:16]}"  # Match server format
        
        # Start the stream (will reuse if already exists)
        self._ensure_stream_running()
    
    def _ensure_stream_running(self):
        """Ensure stream is running on server (reuses existing if available)"""
        with self._stream_lock:
            # Check if we already know this stream is active
            if self.stream_id in self._active_streams:
                logger.info(f"Reusing existing stream: {self.stream_id}")
                self.running = True
                self._start_fetching()
                return True
        
        try:
            # Always call start-with-url - server will reuse existing stream
            payload = {
                "rtsp_url": self.rtsp_url,
                "width": self.width,
                "height": self.height
            }
            
            response = self.session.post(f"{self.server_url}/api/streams/start-with-url", json=payload)
            if response.status_code == 200:
                result = response.json()
                self.stream_id = result['stream_id']
                
                with self._stream_lock:
                    self._active_streams[self.stream_id] = time.time()
                
                # Check if it was already running or newly created
                if "already running" in result.get('message', ''):
                    logger.info(f"Connected to existing stream: {self.stream_id}")
                else:
                    logger.info(f"Started new stream: {self.stream_id}")
                    time.sleep(2)  # Wait for new stream to initialize
                
                self._start_fetching()
                return True
                
        except Exception as e:
            logger.error(f"Failed to ensure stream: {e}")
        return False
    
    def _start_fetching(self):
        """Start background thread to fetch frames"""
        self.running = True
        self.fetch_thread = threading.Thread(target=self._fetch_frames, daemon=True)
        self.fetch_thread.start()
    
    def _fetch_frames(self):
        """Fetch frames in background"""
        while self.running:
            try:
                response = self.session.get(
                    f"{self.server_url}/api/streams/{self.stream_id}/frame",
                    timeout=2
                )
                
                if response.status_code == 200:
                    frame_data = response.content
                    expected_size = self.width * self.height * 3
                    
                    if len(frame_data) == expected_size:
                        frame = np.frombuffer(frame_data, dtype=np.uint8)
                        frame = frame.reshape((self.height, self.width, 3))
                        
                        self.current_frame = frame
                        
                        # Add to queue
                        if self.frame_queue.full():
                            try:
                                self.frame_queue.get_nowait()
                            except Empty:
                                pass
                        self.frame_queue.put(frame)
                        
            except Exception as e:
                if self.running:
                    time.sleep(0.1)
    
    def read(self):
        """Read a frame - same interface as cv2.VideoCapture"""
        try:
            frame = self.frame_queue.get(timeout=1.0)
            return True, frame
        except Empty:
            if self.current_frame is not None:
                return True, self.current_frame.copy()
            return False, None
    
    def isOpened(self):
        """Check if capture is open - same interface as cv2.VideoCapture"""
        return self.running and self.current_frame is not None
    
    def get(self, prop_id):
        """Get property - same interface as cv2.VideoCapture"""
        if prop_id == cv2.CAP_PROP_FRAME_WIDTH:
            return float(self.width)
        elif prop_id == cv2.CAP_PROP_FRAME_HEIGHT:
            return float(self.height)
        elif prop_id == cv2.CAP_PROP_FPS:
            return 30.0  # Default FPS
        elif prop_id == cv2.CAP_PROP_BUFFERSIZE:
            return 2.0   # Our queue size
        elif prop_id == cv2.CAP_PROP_FRAME_COUNT:
            return -1.0  # Infinite for live stream
        elif prop_id == cv2.CAP_PROP_POS_FRAMES:
            return 0.0   # Not applicable for live stream
        return 0.0
    
    def set(self, prop_id, value):
        """Set property - same interface as cv2.VideoCapture"""
        if prop_id == cv2.CAP_PROP_BUFFERSIZE:
            # Adjust our queue size
            try:
                new_size = max(1, min(10, int(value)))
                self.frame_queue = Queue(maxsize=new_size)
                return True
            except:
                return False
        elif prop_id == cv2.CAP_PROP_FPS:
            # Can't really change FPS of server stream, but return True for compatibility
            return True
        elif prop_id == cv2.CAP_PROP_FRAME_WIDTH or prop_id == cv2.CAP_PROP_FRAME_HEIGHT:
            # Can't change resolution after stream started
            return False
        return False
    
    def release(self):
        """Release capture - same interface as cv2.VideoCapture"""
        self.running = False
        # Note: We don't remove from _active_streams or stop the server stream
        # because other clients might still be using it
    
    @classmethod
    def cleanup_inactive_streams(cls):
        """Clean up tracking of old streams (call periodically)"""
        with cls._stream_lock:
            now = time.time()
            # Remove streams older than 5 minutes from our tracking
            expired = [sid for sid, ts in cls._active_streams.items() if now - ts > 300]
            for sid in expired:
                del cls._active_streams[sid]
    
    def __enter__(self):
        return self
    
    def __exit__(self, exc_type, exc_val, exc_tb):
        self.release()

# The main function you'll import
def VideoCapture(source):
    """
    Drop-in replacement for cv2.VideoCapture
    
    Usage:
        from video_import import VideoCapture
        cap = VideoCapture("rtsp://camera-url")
        ret, frame = cap.read()
    """
    if isinstance(source, str) and source.startswith('rtsp://'):
        return SimpleVideoCapture(source)
    else:
        # For non-RTSP sources, use regular OpenCV
        return cv2.VideoCapture(source)
