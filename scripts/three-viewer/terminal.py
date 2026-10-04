#!/usr/bin/env python3
"""Private PTY viewer and byte sink; no third-party PTY dependency needed."""
import fcntl
import json
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time
import tty

if sys.argv[1] == "sink":
    tty.setraw(0)
    with open(sys.argv[2], "ab", buffering=0) as received:
        print("THREE_VIEWER_READY", flush=True)
        while True:
            data = os.read(0, 4096)
            if not data:
                break
            received.write(data)
            os.write(1, data)
else:
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 44, 160, 0, 0))
    child = subprocess.Popen(["tmux", "-S", sys.argv[2], "attach-session", "-t", "host"], stdin=slave, stdout=slave, stderr=slave, env={**os.environ, "TERM": "xterm-256color"}, start_new_session=True)
    os.close(slave)
    deadline = time.monotonic() + 240
    try:
        while child.poll() is None and time.monotonic() < deadline:
            ready, _, _ = select.select([master, sys.stdin], [], [], 1)
            if master in ready:
                os.read(master, 65536)
            if sys.stdin in ready:
                line = sys.stdin.readline()
                if not line:
                    break
                command = json.loads(line)
                os.write(master, b"\x1b[I" if command["focused"] else b"\x1b[O")
                print(json.dumps({"focused": command["focused"]}), flush=True)
    finally:
        child.terminate()
        try:
            child.wait(timeout=3)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait(timeout=3)
        os.close(master)
