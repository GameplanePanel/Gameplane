"""CI-only upload smoke test through the dashboard's real nginx config."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import json
import re
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
MIB = 1 << 20


class UploadSink(BaseHTTPRequestHandler):
    received = []
    get_requests = []

    def do_GET(self):
        self.get_requests.append(self.path)
        self.send_response(200)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"ok")

    def do_POST(self):
        length = int(self.headers["Content-Length"])
        remaining = length
        while remaining:
            chunk = self.rfile.read(min(remaining, MIB))
            if not chunk:
                self.send_error(400, "incomplete upload")
                return
            remaining -= len(chunk)
        self.received.append(length)
        body = json.dumps({"bytes": length}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


def main():
    template = (ROOT / "web/nginx.conf.template").read_text()
    api = (ROOT / "api/internal/ws/dialer.go").read_text()
    agent = (ROOT / "agent/internal/mods/mods.go").read_text()
    request_mib = int(re.search(r'agentHTTPLimit\("/mods/upload",\s*(\d+)\s*<<\s*20\)', api)[1])
    file_mib = int(re.search(r'defaultMaxBytes\s*=\s*(\d+)\s*<<\s*20', agent)[1])
    nginx_mib = int(re.search(r'client_max_body_size\s+(\d+)m;', template)[1])
    ingress_mib = int(re.search(r'proxy-body-size:\s*"(\d+)m"', (ROOT / "charts/gameplane/values.yaml").read_text())[1])
    assert nginx_mib == ingress_mib == request_mib, (nginx_mib, ingress_mib, request_mib)
    assert file_mib < request_mib, "default file needs room for multipart framing"
    image = re.findall(r'^FROM (nginxinc/\S+)', (ROOT / "web/Dockerfile").read_text(), re.M)[0]
    sink = ThreadingHTTPServer(("127.0.0.1", 0), UploadSink)
    threading.Thread(target=sink.serve_forever, daemon=True).start()
    name = "gameplane-upload-" + uuid.uuid4().hex
    try:
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / "default.conf"
            config.write_text(template.replace("${API_UPSTREAM}", f"http://127.0.0.1:{sink.server_port}")
                              .replace("${NGINX_RESOLVER}", "127.0.0.1"))
            config.chmod(0o644)
            subprocess.run(["docker", "run", "-d", "--rm", "--network", "host", "--name", name,
                            "-v", f"{config}:/etc/nginx/conf.d/default.conf:ro", image], check=True)
            deadline = time.monotonic() + 30
            while True:
                try:
                    with urllib.request.urlopen("http://127.0.0.1:8080/nginx-health", timeout=1) as response:
                        assert response.status == 200
                    break
                except (urllib.error.URLError, TimeoutError):
                    if time.monotonic() >= deadline:
                        subprocess.run(["docker", "logs", name], check=False)
                        raise
                    time.sleep(0.2)
            # A live upstream and working ordinary proxy are positive controls:
            # only the kubelet's database readiness path must stop at nginx.
            with urllib.request.urlopen(f"http://127.0.0.1:{sink.server_port}/readyz", timeout=5) as response:
                assert response.status == 200
            with urllib.request.urlopen("http://127.0.0.1:8080/healthz", timeout=5) as response:
                assert response.status == 200
            for path in ("/readyz", "/readyz?probe=public", "/%72eadyz", "//readyz"):
                for accept in ("application/json", "text/html", "*/*"):
                    request = urllib.request.Request("http://127.0.0.1:8080" + path,
                                                     headers={"Accept": accept})
                    try:
                        urllib.request.urlopen(request, timeout=5)
                    except urllib.error.HTTPError as error:
                        assert error.code == 404, (path, accept, error.code)
                        error.close()
                    else:
                        raise AssertionError(("public readiness request succeeded", path, accept))
            assert UploadSink.get_requests == ["/readyz", "/healthz"], UploadSink.get_requests
            upload = Path(directory) / "default-limit.jar"
            with upload.open("wb") as stream:
                stream.truncate(file_mib * MIB)
            result = subprocess.run(["curl", "--fail-with-body", "--silent", "--show-error", "--max-time", "120",
                                     "-H", "Accept: application/json", "-F", f"file=@{upload}",
                                     "http://127.0.0.1:8080/servers/upload-limit/mods/upload"],
                                    capture_output=True, text=True, check=True)
            length = json.loads(result.stdout)["bytes"]
            assert file_mib * MIB < length <= request_mib * MIB, length
            # nginx must reject an oversized complete request from its headers,
            # before accepting a body or forwarding anything to the API.
            with socket.create_connection(("127.0.0.1", 8080), timeout=5) as connection:
                connection.sendall(("POST /servers/upload-limit/mods/upload HTTP/1.1\r\n"
                                    "Host: localhost\r\nAccept: application/json\r\n"
                                    f"Content-Length: {request_mib * MIB + 1}\r\n"
                                    "Expect: 100-continue\r\nConnection: close\r\n\r\n").encode())
                with connection.makefile("rb") as response:
                    assert b" 413 " in response.readline(), "oversized request was accepted"
            assert UploadSink.received == [length], UploadSink.received
    finally:
        subprocess.run(["docker", "rm", "-f", name], check=False)
        sink.shutdown()
        sink.server_close()


if __name__ == "__main__":
    main()
