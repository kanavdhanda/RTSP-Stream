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
        
        # Generate stream ID from URL
        hasher = hashlib.md5()
        hasher.update(rtsp_url.encode())  # Fixed: use update() instead of Write()
        self.stream_id = f"stream_{hasher.hexdigest()[:8]}"
        
        # Start the stream
        self._start_stream()
    
    def _start_stream(self):
        """Start stream on server"""
        payload = {
            "rtsp_url": self.rtsp_url,
            "width": self.width,
            "height": self.height
        }
        
        try:
            response = self.session.post(f"{self.server_url}/api/streams/start-with-url", json=payload)
            if response.status_code == 200:
                result = response.json()
                self.stream_id = result['stream_id']
                time.sleep(2)  # Wait for stream to start
                self._start_fetching()
                return True
            else:
                logger.error(f"Server returned status code: {response.status_code}")
        except Exception as e:
            logger.error(f"Failed to start stream: {e}")
            logger.error("Make sure the RTSP server is running on http://localhost:8091")
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
        return self.running
    
    def get(self, prop_id):
        """Get property - same interface as cv2.VideoCapture"""
        if prop_id == cv2.CAP_PROP_FRAME_WIDTH:
            return float(self.width)
        elif prop_id == cv2.CAP_PROP_FRAME_HEIGHT:
            return float(self.height)
        elif prop_id == cv2.CAP_PROP_FPS:
            return 30.0  # Default FPS
        elif prop_id == cv2.CAP_PROP_BUFFERSIZE:
            return float(self.frame_queue.maxsize)
        elif prop_id == cv2.CAP_PROP_FRAME_COUNT:
            return -1.0  # Infinite for live stream
        elif prop_id == cv2.CAP_PROP_POS_FRAMES:
            return 0.0   # Not applicable for live stream
        elif prop_id == cv2.CAP_PROP_POS_MSEC:
            return 0.0   # Not applicable for live stream
        elif prop_id == cv2.CAP_PROP_POS_AVI_RATIO:
            return 0.0   # Not applicable for live stream
        elif prop_id == cv2.CAP_PROP_FOURCC:
            return cv2.VideoWriter_fourcc('M', 'J', 'P', 'G')
        elif prop_id == cv2.CAP_PROP_FORMAT:
            return 16.0  # CV_8UC3
        elif prop_id == cv2.CAP_PROP_MODE:
            return 0.0   # Color mode
        elif prop_id == cv2.CAP_PROP_BRIGHTNESS:
            return 0.0
        elif prop_id == cv2.CAP_PROP_CONTRAST:
            return 0.0
        elif prop_id == cv2.CAP_PROP_SATURATION:
            return 0.0
        elif prop_id == cv2.CAP_PROP_HUE:
            return 0.0
        elif prop_id == cv2.CAP_PROP_GAIN:
            return 0.0
        elif prop_id == cv2.CAP_PROP_EXPOSURE:
            return 0.0
        elif prop_id == cv2.CAP_PROP_CONVERT_RGB:
            return 1.0   # We provide RGB frames
        elif prop_id == cv2.CAP_PROP_WHITE_BALANCE_BLUE_U:
            return 0.0
        elif prop_id == cv2.CAP_PROP_RECTIFICATION:
            return 0.0
        elif prop_id == cv2.CAP_PROP_MONOCHROME:
            return 0.0
        elif prop_id == cv2.CAP_PROP_SHARPNESS:
            return 0.0
        elif prop_id == cv2.CAP_PROP_AUTO_EXPOSURE:
            return 0.0
        elif prop_id == cv2.CAP_PROP_GAMMA:
            return 0.0
        elif prop_id == cv2.CAP_PROP_TEMPERATURE:
            return 0.0
        elif prop_id == cv2.CAP_PROP_TRIGGER:
            return 0.0
        elif prop_id == cv2.CAP_PROP_TRIGGER_DELAY:
            return 0.0
        elif prop_id == cv2.CAP_PROP_WHITE_BALANCE_RED_V:
            return 0.0
        elif prop_id == cv2.CAP_PROP_ZOOM:
            return 1.0
        elif prop_id == cv2.CAP_PROP_FOCUS:
            return 0.0
        elif prop_id == cv2.CAP_PROP_GUID:
            return 0.0
        elif prop_id == cv2.CAP_PROP_ISO_SPEED:
            return 0.0
        elif prop_id == cv2.CAP_PROP_BACKLIGHT:
            return 0.0
        elif prop_id == cv2.CAP_PROP_PAN:
            return 0.0
        elif prop_id == cv2.CAP_PROP_TILT:
            return 0.0
        elif prop_id == cv2.CAP_PROP_ROLL:
            return 0.0
        elif prop_id == cv2.CAP_PROP_IRIS:
            return 0.0
        elif hasattr(cv2, 'CAP_PROP_SETTINGS') and prop_id == cv2.CAP_PROP_SETTINGS:
            return 0.0
        elif hasattr(cv2, 'CAP_PROP_AUTOFOCUS') and prop_id == cv2.CAP_PROP_AUTOFOCUS:
            return 0.0
        elif hasattr(cv2, 'CAP_PROP_CHANNEL') and prop_id == cv2.CAP_PROP_CHANNEL:
            return 0.0
        elif hasattr(cv2, 'CAP_PROP_AUTO_WB') and prop_id == cv2.CAP_PROP_AUTO_WB:
            return 0.0
        elif hasattr(cv2, 'CAP_PROP_WB_TEMPERATURE') and prop_id == cv2.CAP_PROP_WB_TEMPERATURE:
            return 0.0
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
        elif prop_id == cv2.CAP_PROP_FRAME_WIDTH:
            # Can't change resolution after stream started
            return False
        elif prop_id == cv2.CAP_PROP_FRAME_HEIGHT:
            # Can't change resolution after stream started
            return False
        elif prop_id == cv2.CAP_PROP_POS_FRAMES:
            # Not applicable for live stream
            return False
        elif prop_id == cv2.CAP_PROP_POS_MSEC:
            # Not applicable for live stream
            return False
        elif prop_id == cv2.CAP_PROP_POS_AVI_RATIO:
            # Not applicable for live stream
            return False
        elif prop_id == cv2.CAP_PROP_FOURCC:
            # Return True for compatibility but don't actually change
            return True
        elif prop_id in [cv2.CAP_PROP_BRIGHTNESS, cv2.CAP_PROP_CONTRAST, 
                        cv2.CAP_PROP_SATURATION, cv2.CAP_PROP_HUE,
                        cv2.CAP_PROP_GAIN, cv2.CAP_PROP_EXPOSURE,
                        cv2.CAP_PROP_WHITE_BALANCE_BLUE_U, cv2.CAP_PROP_WHITE_BALANCE_RED_V,
                        cv2.CAP_PROP_ZOOM, cv2.CAP_PROP_FOCUS, cv2.CAP_PROP_PAN, 
                        cv2.CAP_PROP_TILT, cv2.CAP_PROP_ROLL, cv2.CAP_PROP_IRIS]:
            # Camera control properties - return True for compatibility
            return True
        elif prop_id == cv2.CAP_PROP_CONVERT_RGB:
            # Return True for compatibility
            return True
        return False
    
    def release(self):
        """Release capture - same interface as cv2.VideoCapture"""
        self.running = False
        if hasattr(self, 'fetch_thread'):
            self.fetch_thread = None
    
    def grab(self):
        """Grab frame - same interface as cv2.VideoCapture"""
        # For compatibility - our read() method already handles grabbing
        return self.isOpened()
    
    def retrieve(self, flag=0):
        """Retrieve frame - same interface as cv2.VideoCapture"""
        try:
            if self.current_frame is not None:
                return True, self.current_frame.copy()
            frame = self.frame_queue.get_nowait()
            return True, frame
        except Empty:
            return False, None
    
    def getBackendName(self):
        """Get backend name - same interface as cv2.VideoCapture"""
        return "Custom RTSP Server"
    
    def setExceptionMode(self, enable):
        """Set exception mode - same interface as cv2.VideoCapture"""
        # Not implemented but return True for compatibility
        return True
    
    def getExceptionMode(self):
        """Get exception mode - same interface as cv2.VideoCapture"""
        return False
    
    def waitAny(self, streams, timeout=0):
        """Wait for any stream - same interface as cv2.VideoCapture"""
        # Not applicable for single stream, but return for compatibility
        return (0, True) if self.isOpened() else (-1, False)
    
    @staticmethod
    def waitAny(streams, timeout=0):
        """Static method - same interface as cv2.VideoCapture"""
        for i, stream in enumerate(streams):
            if hasattr(stream, 'isOpened') and stream.isOpened():
                return (i, True)
        return (-1, False)
    
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
