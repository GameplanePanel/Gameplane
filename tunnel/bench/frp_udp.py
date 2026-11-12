#!/usr/bin/env python3
"""Compare UDP echo with a real frpc/frps on an isolated Linux runner."""

import argparse
import base64
import contextlib
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.request


def port(sock_type):
    with socket.socket(socket.AF_INET, sock_type) as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


@contextlib.contextmanager
def child(args, cpu, logfile):
    with open(logfile, "wb") as log:
        proc = subprocess.Popen(["taskset", "-c", str(cpu), *args], stdout=log, stderr=log)
        try:
            yield proc
        finally:
            proc.terminate()
            try:
                proc.wait(5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()


def cpu_seconds(proc):
    # The command field may contain spaces. The remainder starts at field 3.
    fields = Path(f"/proc/{proc.pid}/stat").read_text().rsplit(")", 1)[1].split()
    return (int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK")


def proxy_bytes(request):
    with urllib.request.urlopen(request, timeout=3) as response:
        stats = json.load(response)
    return stats["todayTrafficIn"], stats["todayTrafficOut"]


def ready(port_number, processes):
    deadline = time.monotonic() + 15
    marker = secrets.token_bytes(16)
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as probe:
        probe.settimeout(0.15)
        while time.monotonic() < deadline:
            if any(p.poll() is not None for p in processes):
                break
            probe.sendto(marker, ("127.0.0.1", port_number))
            try:
                if probe.recv(64) == marker:
                    return
            except TimeoutError:
                continue
    raise RuntimeError("FRP did not return the UDP readiness echo")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for binary in ("relay-load", "frpc", "frps"):
        parser.add_argument("--" + binary, type=Path, required=True)
    parser.add_argument("--clients", type=int, nargs="+", default=[8, 32, 128])
    parser.add_argument("--hz", type=int, default=128)
    parser.add_argument("--bytes", type=int, default=256)
    parser.add_argument("--seconds", type=int, default=10)
    options = parser.parse_args()
    cpus = sorted(os.sched_getaffinity(0))
    if len(cpus) < 3:
        parser.error("requires three available logical CPUs for the driver, echo and FRP")
    client_cpu, echo_cpu, relay_cpu = cpus[:3]
    binaries = {name: getattr(options, name).resolve() for name in ("relay_load", "frpc", "frps")}
    digests = {}
    for name, path in binaries.items():
        with path.open("rb") as stream:
            digests[name] = hashlib.file_digest(stream, "sha256").hexdigest()
    print(json.dumps({"kernel": os.uname().release, "affinity": [client_cpu, echo_cpu, relay_cpu],
                      "sha256": digests}), flush=True)
    echo, remote, control = port(socket.SOCK_DGRAM), port(socket.SOCK_DGRAM), port(socket.SOCK_STREAM)
    while remote == echo:
        remote = port(socket.SOCK_DGRAM)
    dashboard = port(socket.SOCK_STREAM)
    while dashboard == control:
        dashboard = port(socket.SOCK_STREAM)
    token = secrets.token_hex(24)
    request = urllib.request.Request(f"http://127.0.0.1:{dashboard}/api/proxy/udp/udp")
    auth = base64.b64encode(f"bench:{token}".encode()).decode()
    request.add_header("Authorization", "Basic " + auth)
    with tempfile.TemporaryDirectory() as tmp, contextlib.ExitStack() as stack:
        root = Path(tmp)
        server_config, client_config = root / "frps.toml", root / "frpc.toml"
        server_config.write_text(f'bindAddr = "127.0.0.1"\nbindPort = {control}\n'
                                 f'proxyBindAddr = "127.0.0.1"\nauth.token = "{token}"\n'
                                 f'webServer.addr = "127.0.0.1"\nwebServer.port = {dashboard}\n'
                                 f'webServer.user = "bench"\nwebServer.password = "{token}"\n')
        client_config.write_text(f'serverAddr = "127.0.0.1"\nserverPort = {control}\n'
                                 f'auth.token = "{token}"\nloginFailExit = false\n'
                                 f'[[proxies]]\nname = "udp"\ntype = "udp"\nlocalIP = "127.0.0.1"\n'
                                 f'localPort = {echo}\nremotePort = {remote}\n')
        backend = stack.enter_context(child([binaries["relay_load"], "-echo", f"127.0.0.1:{echo}"], echo_cpu, root / "echo.log"))
        frps = stack.enter_context(child([binaries["frps"], "-c", server_config], relay_cpu, root / "frps.log"))
        frpc = stack.enter_context(child([binaries["frpc"], "-c", client_config], relay_cpu, root / "frpc.log"))
        ready(remote, [backend, frpc, frps])
        routes = [("direct", echo), ("frp", remote)]
        for count in options.clients:
            for iteration in range(2):
                for route, target in (routes if iteration == 0 else reversed(routes)):
                    before = [cpu_seconds(p) for p in (backend, frpc, frps)]
                    traffic_before = proxy_bytes(request)
                    output = subprocess.check_output(["taskset", "-c", str(client_cpu), binaries["relay_load"],
                        "-clients", str(count), "-hz", str(options.hz), "-bytes", str(options.bytes),
                        "-seconds", str(options.seconds), f"127.0.0.1:{target}"], text=True)
                    after = [cpu_seconds(p) for p in (backend, frpc, frps)]
                    traffic_after = proxy_bytes(request)
                    sample = json.loads(output)
                    sample.update(route=route, iteration=iteration+1,
                                  frps_payload_bytes={key: end-start for key, start, end in
                                                      zip(("in", "out"), traffic_before, traffic_after)},
                                  cpu_seconds={name: round(end-start, 3) for name, start, end in
                                               zip(("echo", "frpc", "frps"), before, after)})
                    print(json.dumps(sample), flush=True)


if __name__ == "__main__":
    main()
