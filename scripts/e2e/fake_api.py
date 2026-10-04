"""A fake API for the end-to-end test to call.

Every request is logged as one JSON line to the file given as argument.
  /fail...  answers 500
  /slow...  waits 5 seconds before answering
  anything else answers 200 "done"
"""
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOG_FILE = sys.argv[1]
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 15001


class Handler(BaseHTTPRequestHandler):
    def handle_any(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length).decode()
        with open(LOG_FILE, "a") as f:
            f.write(json.dumps({
                "method": self.command,
                "path": self.path,
                "token": self.headers.get("X-Token"),
                "body": body,
            }) + "\n")

        if self.path.startswith("/fail"):
            self.reply(500, b"boom")
            return
        if self.path.startswith("/slow"):
            time.sleep(5)
        self.reply(200, b"done")

    def reply(self, status, body):
        try:
            self.send_response(status)
            self.end_headers()
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            # The scheduler already gave up (job timeout). That is expected.
            pass

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = handle_any

    def log_message(self, *args):
        pass  # keep the test output clean


ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
