from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse


CHUNK_SIZE = 64 * 1024
ZERO_CHUNK = b"\0" * CHUNK_SIZE


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/ping":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"pong")
            return

        if parsed.path != "/download":
            self.send_response(404)
            self.end_headers()
            return

        qs = parse_qs(parsed.query)
        total = int(qs.get("bytes", [str(50 * 1024 * 1024)])[0])

        self.send_response(200)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Content-Length", str(total))
        self.end_headers()

        left = total
        while left > 0:
            n = min(left, CHUNK_SIZE)
            self.wfile.write(ZERO_CHUNK[:n])
            left -= n

    def do_POST(self):
        if self.path != "/upload":
            self.send_response(404)
            self.end_headers()
            return

        length = int(self.headers.get("Content-Length", "0"))
        left = length
        while left > 0:
            n = min(left, CHUNK_SIZE)
            chunk = self.rfile.read(n)
            if not chunk:
                break
            left -= len(chunk)

        recv = length - left
        body = f"received={recv}\n".encode()

        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    server = ThreadingHTTPServer(("0.0.0.0", 8080), Handler)
    server.serve_forever()
