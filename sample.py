#!/usr/bin/env python3
"""
Sample RTSP client using the video_import module

This demonstrates how to use the video_import module as a drop-in 
replacement for cv2.VideoCapture with RTSP streams.

Usage:
    python3 sample.py [rtsp_url]

If no RTSP URL is provided, it will use a default test stream.
"""

import cv2
import sys
import time
from video_import import VideoCapture

def main():
    # Default test stream (you can replace with your camera's RTSP URL)
    default_rtsp_url = "rtsp://192.168.29.233"
    
    # Use command line argument if provided, otherwise use default
    rtsp_url = sys.argv[1] if len(sys.argv) > 1 else default_rtsp_url
    
    print(f"Connecting to RTSP stream: {rtsp_url}")
    print("Press 'q' to quit, 'r' to reset stats")
    
    # Use our VideoCapture - it's a drop-in replacement for cv2.VideoCapture!
    cap = VideoCapture(rtsp_url)
    
    # Statistics
    frame_count = 0
    start_time = time.time()
    last_fps_time = start_time
    last_frame_count = 0
    
    try:
        while True:
            # Read frame - same API as cv2.VideoCapture
            ret, frame = cap.read()
            
            if not ret:
                print("Failed to read frame, retrying...")
                time.sleep(0.1)
                continue
            
            frame_count += 1
            current_time = time.time()
            
            # Calculate FPS every second
            if current_time - last_fps_time >= 1.0:
                fps = (frame_count - last_frame_count) / (current_time - last_fps_time)
                elapsed = current_time - start_time
                avg_fps = frame_count / elapsed if elapsed > 0 else 0
                
                print(f"Frames: {frame_count}, Current FPS: {fps:.1f}, Avg FPS: {avg_fps:.1f}")
                
                last_fps_time = current_time
                last_frame_count = frame_count
            
            # Add frame counter overlay
            cv2.putText(frame, f"Frame: {frame_count}", (10, 30), 
                       cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 255, 0), 2)
            
            # Add timestamp
            timestamp = time.strftime("%Y-%m-%d %H:%M:%S")
            cv2.putText(frame, timestamp, (10, frame.shape[0] - 10), 
                       cv2.FONT_HERSHEY_SIMPLEX, 0.5, (255, 255, 255), 1)
            
            # Optional: Add some computer vision processing
            # Uncomment the lines below to see edge detection
            
            # gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
            # edges = cv2.Canny(gray, 50, 150)
            # cv2.imshow('Edges', edges)
            
            # Display the frame
            cv2.imshow('RTSP Stream', frame)
            
            # Handle key presses
            key = cv2.waitKey(1) & 0xFF
            if key == ord('q'):
                print("Quitting...")
                break
            elif key == ord('r'):
                print("Resetting statistics...")
                frame_count = 0
                start_time = time.time()
                last_fps_time = start_time
                last_frame_count = 0
            elif key == ord(' '):
                print("Paused - press any key to continue")
                cv2.waitKey(0)
    
    except KeyboardInterrupt:
        print("\nInterrupted by user")
    
    except Exception as e:
        print(f"Error: {e}")
    
    finally:
        # Clean up
        cap.release()
        cv2.destroyAllWindows()
        
        # Final statistics
        total_time = time.time() - start_time
        if total_time > 0:
            final_fps = frame_count / total_time
            print(f"\nFinal Statistics:")
            print(f"Total frames: {frame_count}")
            print(f"Total time: {total_time:.1f} seconds")
            print(f"Average FPS: {final_fps:.1f}")

if __name__ == "__main__":
    main()