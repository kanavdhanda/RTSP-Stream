import os
import time
import argparse
from typing import Dict, Any
import requests

GATEWAY_BASE = os.getenv("GATEWAY_BASE", "http://127.0.0.1:8090")

def start_camera(
    name: str,
    rtsp: str,
    pub_fps: int = 20,
    scale_width: int = 640,
    base: str = GATEWAY_BASE,
    retries: int = 3,
    backoff_ms: int = 500,
) -> Dict[str, Any]:
    url = f"{base}/cameras/start"
    payload = {"name": name, "rtsp": rtsp, "pub_fps": pub_fps, "scale_width": scale_width}

    for attempt in range(retries + 1):
        try:
            r = requests.post(url, json=payload, timeout=5)
            if r.ok:
                data = r.json()
                # Use the canonical name returned by the gateway (handles URL de-dup)
                cam_name = data.get("name", name)
                return {
                    "ok": True,
                    "name": cam_name,
                    "message": data.get("message"),
                    "webrtc_whep": data.get("webrtc_whep"),
                    "hls": data.get("hls"),
                    "mediamtx_path": data.get("mediamtx_path"),
                    "zmq_topic": data.get("zmq_topic", cam_name),
                }
            else:
                err = {"ok": False, "status": r.status_code, "text": r.text}
        except Exception as e:
            err = {"ok": False, "error": str(e)}

        if attempt < retries:
            # exponential backoff with small jitter
            delay = (backoff_ms / 1000.0) * (2 ** attempt)
            time.sleep(min(delay, 5.0))
        else:
            return err

def stop_camera(name: str, base: str = GATEWAY_BASE) -> Dict[str, Any]:
    url = f"{base}/cameras/stop"
    r = requests.post(url, json={"name": name}, timeout=5)
    return r.json() if r.ok else {"ok": False, "status": r.status_code, "text": r.text}

def list_cameras(base: str = GATEWAY_BASE) -> Dict[str, Any]:
    url = f"{base}/cameras"
    r = requests.get(url, timeout=5)
    return r.json() if r.ok else {"ok": False, "status": r.status_code, "text": r.text}

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Gateway camera control")
    parser.add_argument("--name", required=False, help="Preferred camera name")
    parser.add_argument("--rtsp", required=False, help="RTSP URL for the camera")
    parser.add_argument("--fps", type=int, default=20, help="Publish FPS")
    parser.add_argument("--width", type=int, default=640, help="Scale width")
    parser.add_argument("--base", default=GATEWAY_BASE, help="Gateway base URL")
    parser.add_argument("--stop", action="store_true", help="Stop camera by name")
    args = parser.parse_args()

    if args.stop:
        if not args.name:
            raise SystemExit("--stop requires --name")
        print(stop_camera(args.name, base=args.base))
        raise SystemExit(0)

    if not args.name or not args.rtsp:
        print("Usage: python new.py --name <name> --rtsp <rtsp-url> [--fps 20] [--width 640] [--base http://127.0.0.1:8090]")
        print("List:", list_cameras(base=args.base))
        raise SystemExit(2)

    resp = start_camera(name=args.name, rtsp=args.rtsp, pub_fps=args.fps, scale_width=args.width, base=args.base)
    print("start:", resp)

    # Show canonical name if dedup occurred
    cname = resp.get("name", args.name)
    if cname != args.name:
        print(f"canonical-name: {cname}")