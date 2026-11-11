#!/usr/bin/env python3
"""One real Teeworlds 0.7.5 UDP join, using the matching game client under Xvfb."""

import argparse
import os
import signal
import subprocess
import sys
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("-game", choices=["teeworlds"], required=True)
    parser.add_argument("-addr", required=True)
    parser.add_argument("-user", required=True)
    parser.add_argument("-deadline", required=True)
    parser.add_argument("--inside", action="store_true")
    args = parser.parse_args()
    if not args.inside:
        # The temporary container has no kubeconfig or Docker socket.
        os.execvp("docker", ["docker", "run", "--rm", "--network", "host", "--user", "65532",
                  "--cpus", "1", "--memory", "512m", "--pids-limit", "128",
                  "--entrypoint", "python3", "gameplane-test/wake-teeworlds:ci",
                  "/opt/teeworlds-join.py", "--inside", *sys.argv[1:]])
    deadline = time.monotonic() + max(0, int(args.deadline.removesuffix("ms")) / 1000 - 2)
    with tempfile.TemporaryDirectory() as tmp, tempfile.TemporaryFile() as log:
        env = dict(os.environ, HOME=tmp, XDG_DATA_HOME=tmp, XDG_CONFIG_HOME=tmp,
                   LIBGL_ALWAYS_SOFTWARE="1", SDL_AUDIODRIVER="dummy")
        command = ["xvfb-run", "-a", "stdbuf", "-oL", "/usr/games/teeworlds",
                   "cl_show_welcome 0", "snd_enable 0", "gfx_fullscreen 0", "gfx_screen_width 640",
                   "gfx_screen_height 480", "debug 1", "console_output_level 2",
                   f"player_name {args.user}", f"connect {args.addr}"]
        proc = subprocess.Popen(command, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            # In the 0.7.5 client, STATE_LOADING=2 -> STATE_ONLINE=3 occurs
            # when the second valid server snapshot has populated the world.
            while time.monotonic() < deadline and proc.poll() is None:
                if b"state change. last=2 current=3" in os.pread(log.fileno(), 1024*1024, 0):
                    print("VERDICT\tPASS\tJOINED\tTeeworlds 0.7.5 entered STATE_ONLINE after two server world snapshots")
                    return 0
                time.sleep(0.1)
        finally:
            if proc.poll() is None:
                os.killpg(proc.pid, signal.SIGTERM)
                try:
                    proc.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid, signal.SIGKILL)
                    proc.wait()
    return 1


if __name__ == "__main__":
    sys.exit(main())
