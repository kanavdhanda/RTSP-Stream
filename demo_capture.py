import os
import time
import cv2
from capture import open_capture


def main():
    rtsp = os.getenv("RTSP", "rtsp://user:pass@192.168.1.10/Streaming/Channels/101")
    cap = open_capture(rtsp_url=rtsp, timeout_ms=2000, pub_fps=20, scale_width=640)
    print("capture:", cap.info)
    n = 0
    t0 = time.time()
    while True:
        ok, frame = cap.read()
        if not ok:
            print("timeout waiting frame")
            continue
        n += 1
        if n % 30 == 0:
            dt = time.time() - t0
            print(f"~{n/dt:.1f} FPS")
        cv2.imshow("gateway-capture", frame)
        if cv2.waitKey(1) & 0xFF == ord("q"):
            break
    cap.release()


if __name__ == "__main__":
    main()
