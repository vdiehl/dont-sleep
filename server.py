"""
Don't Sleep - local web server (interface only)
-----------------------------------------------
Zero-dependency HTTP server (Python standard library) that exposes the shared
core logic over a small JSON API and serves the static web UI. All the actual
power-setting logic lives in core.py / powercfg_manager.py so the `dontsleep`
CLI can reuse it.

Run:  python server.py [--no-browser] [--port N]
"""

import argparse
import json
import os
import threading
import webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

import core
import powercfg_manager as pm

HOST = "127.0.0.1"
DEFAULT_PORT = 8765

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
STATIC_DIR = os.path.join(BASE_DIR, "static")

CONTENT_TYPES = {
    ".html": "text/html; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".js": "application/javascript; charset=utf-8",
    ".json": "application/json; charset=utf-8",
    ".ico": "image/x-icon",
    ".svg": "image/svg+xml",
}

# Serialise powercfg access so concurrent requests don't interleave writes.
_lock = threading.Lock()

# Set in main(); used by the /api/quit endpoint to stop a background instance.
_httpd = None


class Handler(BaseHTTPRequestHandler):
    server_version = "DontSleep/2.0"

    def log_message(self, fmt, *args):
        print(f"  {self.address_string()} - {fmt % args}")

    def _send_json(self, obj, status=200):
        body = json.dumps(obj).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read_json_body(self):
        length = int(self.headers.get("Content-Length") or 0)
        if not length:
            return {}
        try:
            return json.loads(self.rfile.read(length).decode("utf-8"))
        except (ValueError, UnicodeDecodeError):
            return {}

    def _send_file(self, rel_path):
        safe = os.path.normpath(rel_path).lstrip("\\/")
        full = os.path.join(STATIC_DIR, safe)
        if not os.path.abspath(full).startswith(os.path.abspath(STATIC_DIR)) or not os.path.isfile(full):
            self.send_error(404, "Not found")
            return
        ext = os.path.splitext(full)[1].lower()
        with open(full, "rb") as fh:
            body = fh.read()
        self.send_response(200)
        self.send_header("Content-Type", CONTENT_TYPES.get(ext, "application/octet-stream"))
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = urlparse(self.path).path
        if path == "/api/status":
            self._guarded(lambda: core.status())
            return
        if path in ("/", ""):
            self._send_file("index.html")
            return
        self._send_file(path)

    def do_POST(self):
        path = urlparse(self.path).path
        if path == "/api/prevent":
            self._guarded(lambda: core.prevent())
        elif path == "/api/restore-previous":
            self._guarded(lambda: core.restore("previous"))
        elif path == "/api/restore-defaults":
            self._guarded(lambda: core.restore("defaults"))
        elif path == "/api/config":
            body = self._read_json_body()

            def _update():
                core.set_enabled(body.get("enabled", {}))
                return core.status()

            self._guarded(_update)
        elif path == "/api/quit":
            self._send_json({"ok": True})
            if _httpd is not None:
                threading.Thread(target=_httpd.shutdown, daemon=True).start()
        else:
            self.send_error(404, "Not found")

    def _guarded(self, fn):
        try:
            with _lock:
                self._send_json(fn())
        except FileNotFoundError as exc:
            self._send_json({"error": str(exc)}, status=400)
        except pm.PowercfgError as exc:
            self._send_json({"error": str(exc)}, status=500)
        except Exception as exc:  # noqa: BLE001 - surface anything else to the UI
            self._send_json({"error": f"Unexpected error: {exc}"}, status=500)


def main():
    parser = argparse.ArgumentParser(description="Don't Sleep local web server")
    parser.add_argument("--no-browser", action="store_true", help="do not auto-open the browser")
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    args = parser.parse_args()

    global _httpd
    os.makedirs(core.STATE_DIR, exist_ok=True)
    httpd = ThreadingHTTPServer((HOST, args.port), Handler)
    _httpd = httpd
    url = f"http://{HOST}:{args.port}/"
    print("=" * 52)
    print("  Don't Sleep  -  Windows power control")
    print(f"  Serving at {url}")
    print("  Press Ctrl+C to stop.")
    print("=" * 52)
    if not args.no_browser:
        threading.Timer(0.6, lambda: webbrowser.open(url)).start()
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down.")
    finally:
        httpd.server_close()


if __name__ == "__main__":
    main()
