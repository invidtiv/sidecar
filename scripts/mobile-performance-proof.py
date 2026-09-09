#!/usr/bin/env python3
"""Measure synthetic terminal latency in a prepared, private mobile proof.

Prepare with scripts/mobile-service-proof.sh prepare ROOT. This proof replaces
only that fixture's named pane and can compare --binary BASELINE to a new build.
All timings exclude SSH, physical keyboards, and the native renderer.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import queue
import re
import statistics
import subprocess
import threading
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("root", type=Path)
parser.add_argument("--binary", type=Path)
parser.add_argument("--samples", type=int, default=40)
parser.add_argument("--projects", type=int, default=21)
parser.add_argument("--candidate", action="store_true")
args = parser.parse_args()
root = Path(os.path.abspath(args.root))
if not re.fullmatch(r"[A-Za-z0-9_./-]+", str(root)):
    parser.error("ROOT contains unsupported characters")
if not str(root).startswith("/private/tmp/sidecar-mobile-") and not str(root).startswith("/tmp/sidecar-mobile-"):
    parser.error("ROOT must be a private /tmp/sidecar-mobile-* proof")
if not (root / ".sidecar-mobile-proof-owned").is_file():
    parser.error("ROOT was not prepared by mobile-service-proof.sh")
if not 1 <= args.samples <= 40 or not 1 <= args.projects <= 128:
    parser.error("samples must be 1..40 and projects must be 1..128")
binary = (args.binary or root / "sidecar").resolve()
socket = root / "tmux" / f"tmux-{os.getuid()}" / "default"
env = os.environ.copy()
env.pop("TMUX", None)
env.pop("TMUX_PANE", None)
env.update(XDG_STATE_HOME=str(root / "state"), TMUX_TMPDIR=str(root / "tmux"), SIDECAR_ISOLATED_STATE="1")


def tmux(*words):
    return subprocess.run(["tmux", "-S", str(socket), *words], env=env, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.strip()


# Distinct synthetic Git registrations expose the old per-project subprocess
# cost without reading or copying the user's real Sidecar state.
for index in range(args.projects - 1):
    project = root / "synthetic-projects" / str(index)
    if not project.exists():
        project.mkdir(parents=True)
        subprocess.run(["git", "-C", str(project), "init", "-q"], check=True)
    state = root / "state" / "sidecar" / "projects" / f"perf-{index}"
    state.mkdir(parents=True, exist_ok=True)
    (state / "meta.json").write_text(json.dumps({"path": str(project)}))
    (state / "shells.json").write_text('{"version":1,"shells":[]}')

session = "sidecar-sh-mobile-m0c"
if args.candidate:
    project = root / "candidate-project"
    if not project.exists():
        project.mkdir()
        subprocess.run(["git", "-C", str(project), "init", "-q"], check=True)
        subprocess.run(["git", "-C", str(project), "-c", "user.name=Sidecar", "-c", "user.email=proof@invalid", "commit", "--allow-empty", "-qm", "initial"], check=True)
    (root / "config" / "config.json").write_text(json.dumps({"projects": {"list": [{"name": "Performance candidate", "path": str(project)}]}}))
    session = "mobile-perf-candidate"
    if subprocess.run(["tmux", "-S", str(socket), "has-session", "-t", "=" + session], env=env, capture_output=True).returncode:
        tmux("new-session", "-d", "-s", session, "-c", str(project), "-x", "80", "-y", "24", "/bin/bash --noprofile --norc")

harness = root / "synthetic-harness.py"
harness.write_text('''import os, sys, termios, tty
old=termios.tcgetattr(0)
tty.setraw(0)
def render(n):
 os.write(1, ("\\x1b[H" + "".join(f"\\x1b[{r};1Hrow={r:02d} event={n:08d} " + "x"*48 for r in range(2,24)) + f"\\x1b[1;1HEVENT={n:08d}\\x1b[K").encode())
try:
 os.write(1,b"\\x1b[?1049h\\x1b[?1000h\\x1b[?1006h")
 render(0)
 n=0
 while os.read(0,64):
  n+=1
  render(n)
finally:
 termios.tcsetattr(0,termios.TCSANOW,old)
''')


class Service:
    def __init__(self):
        self.process = subprocess.Popen([str(binary), "-config", str(root / "config" / "config.json"), "mobile", "serve", "--stdio"], env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
        self.messages = queue.Queue()
        self.index, self.frame = 0, None
        def reader():
            for line in self.process.stdout:
                self.messages.put(json.loads(line))
            self.messages.put(None)
        threading.Thread(target=reader, daemon=True).start()

    def receive(self):
        message = self.messages.get(timeout=15)
        if message is None:
            raise RuntimeError("service exited: " + self.process.stderr.read())
        if message["type"] == "error":
            raise RuntimeError(message)
        if message["type"] == "frame":
            self.frame = message
        return message

    def request(self, kind, **fields):
        self.index += 1
        identifier = str(self.index)
        self.process.stdin.write(json.dumps(dict(version=0, type=kind, request_id=identifier, **fields)) + "\n")
        self.process.stdin.flush()
        while True:
            message = self.receive()
            if message.get("request_id") == identifier:
                return message

    def operation(self, kind, **fields):
        self.sequence += 1
        return self.request(kind, attachment_handle=self.attachment, operation_sequence=self.sequence, last_reset_generation=self.frame["reset_generation"], last_output_sequence=self.frame["output_sequence"], **fields)

    def open(self):
        self.request("hello")
        selector = dict(target=session)
        if args.candidate:
            catalog = self.request("sessions")["catalog"]
            candidates = [candidate for section in catalog["sections"] for row in section["rows"] for candidate in row.get("candidates", []) if candidate["session"] == session]
            if len(candidates) != 1:
                raise RuntimeError("fixture candidate is not unique")
            selector = dict(target=candidates[0]["selector"], expected_target=candidates[0]["expected_target"])
        target = self.request("resolve", **selector)["target"]
        self.attachment = self.request("open", target_handle=target["handle"], attachment_id="performance-proof")["attachment_handle"]
        while self.frame is None:
            self.receive()
        self.sequence = 0
        self.operation("control", columns=80, rows=24)

    def close(self):
        self.process.stdin.close()
        if self.process.wait(timeout=8):
            raise RuntimeError(self.process.stderr.read())


def summary(samples):
    ordered = sorted(samples)
    return dict(mean_ms=round(statistics.mean(samples), 3), median_ms=round(statistics.median(samples), 3), p95_ms=round(ordered[max(0, int(len(samples) * .95) - 1)], 3), max_ms=round(max(samples), 3))


def measure(workload):
    if workload == "empty_shell":
        command = "env BASH_SILENCE_DEPRECATION_WARNING=1 PS1='proof> ' /bin/bash --noprofile --norc"
    else:
        # The fixture root alphabet is constrained by its prepare script.
        command = f"/usr/bin/python3 '{harness}'"
    tmux("respawn-pane", "-k", "-t", session + ":0.0", command)
    time.sleep(.15)
    service = Service()
    acknowledgement, echo = [], []
    expected = b""
    try:
        service.open()
        for index in range(args.samples):
            if workload == "harness_wheel":
                data = b"\x1b[<64;10;10M" if index % 2 == 0 else b"\x1b[<65;10;10M"
            else:
                data = bytes([97 + index % 26])
            expected = expected + data if workload == "empty_shell" else f"EVENT={index + 1:08d}".encode()
            started = time.monotonic()
            service.operation("input", data_base64=base64.b64encode(data).decode())
            acknowledgement.append((time.monotonic() - started) * 1000)
            while expected not in base64.b64decode(service.frame["render_vt_base64"]):
                service.receive()
            echo.append((time.monotonic() - started) * 1000)
            time.sleep(.045)
        service.operation("release")
    finally:
        service.close()
    owner = tmux("display-message", "-p", "-t", session, "#{@sidecar-owner}")
    if owner:
        raise RuntimeError("fixture retained input ownership")
    return dict(workload=workload, samples=args.samples, acknowledgement=summary(acknowledgement), visible_echo=summary(echo))

result = dict(schema="sidecar.mobile.performance.v0", binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), registered_projects=sum((path / "meta.json").is_file() for path in (root / "state" / "sidecar" / "projects").iterdir()), target_kind="worktree" if args.candidate else "shell", transport="local_stdio", workloads=[measure(kind) for kind in ["empty_shell", "harness_typing", "harness_wheel"]])
print(json.dumps(result, indent=2))
