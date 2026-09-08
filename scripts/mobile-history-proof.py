#!/usr/bin/env python3
import base64
import json
import os
import queue
import subprocess
import sys
import threading
import time


root, tmux_bin = sys.argv[1:3]
wrapper = os.path.join(root, "serve")
socket = os.path.join(root, "tmux", f"tmux-{os.getuid()}", "default")
env = os.environ.copy()
env.pop("TMUX", None)
env.pop("TMUX_PANE", None)


def tmux(*args):
    subprocess.run([tmux_bin, "-S", socket, *args], check=True, env=env, stdout=subprocess.DEVNULL)


def send_shell(command):
    tmux("send-keys", "-l", "-t", "sidecar-sh-mobile-m0c:0.0", command)
    tmux("send-keys", "-t", "sidecar-sh-mobile-m0c:0.0", "Enter")


send_shell("python3 -c '[(print(f\"HISTORY-{i:03d}\", flush=True)) for i in range(48)]'")
time.sleep(0.2)

process = subprocess.Popen(
    [wrapper], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    text=True, env=env, bufsize=1,
)
lines = queue.Queue()


def read_lines():
    for line in process.stdout:
        lines.put(line)
    lines.put(None)


threading.Thread(target=read_lines, daemon=True).start()
frames = []
resets = []
request_index = 0


def receive(timeout=8):
    try:
        line = lines.get(timeout=timeout)
    except queue.Empty as error:
        raise RuntimeError("timed out waiting for history service response") from error
    if not line:
        raise RuntimeError("history service closed: " + process.stderr.read())
    message = json.loads(line)
    if message["type"] == "frame":
        frames.append(message)
    elif message["type"] == "reset":
        resets.append(message)
    return message


def request(kind, expect_error=False, **fields):
    global request_index
    request_index += 1
    request_id = f"history-proof-{request_index}"
    message = {"version": 0, "type": kind, "request_id": request_id, **fields}
    process.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
    process.stdin.flush()
    while True:
        response = receive()
        if response.get("request_id") != request_id:
            continue
        if response["type"] == "error" and not expect_error:
            raise RuntimeError(json.dumps(response))
        return response


hello = request("hello")
if not hello["capabilities"].get("history_snapshots") or hello["capabilities"].get("maximum_history_rows") != 600:
    raise RuntimeError("history capability missing or incorrectly bounded")
resolved = request("resolve", target="sidecar-sh-mobile-m0c")
opened = request("open", target_handle=resolved["target"]["handle"], attachment_id="history-proof")
while not frames:
    receive()
initial = frames[-1]
if "history_size" not in initial:
    raise RuntimeError("live frame omitted advisory history size")

marker = "HISTORY_REFRESH_é_界"
send_shell("printf '\\033[41m  \\033[0m " + marker + "\\n'")
deadline = time.monotonic() + 8
while time.monotonic() < deadline:
    receive()
    if frames[-1]["output_sequence"] > initial["output_sequence"] and marker.encode() in base64.b64decode(frames[-1]["render_vt_base64"]):
        break
else:
    raise RuntimeError("newest synthetic output did not reach the live stream")

geometry = initial["geometry"]
history = request(
    "history", attachment_handle=opened["attachment_handle"],
    last_reset_generation=initial["reset_generation"],
    last_output_sequence=initial["output_sequence"],
    columns=geometry["columns"], rows=geometry["rows"], history_rows=10,
)
if history["type"] != "history" or history["output_sequence"] != initial["output_sequence"]:
    raise RuntimeError("history response did not echo the applied checkpoint")
snapshot = history["history"]
if snapshot["history_rows"] < 1 or snapshot["history_rows"] > 10:
    raise RuntimeError("history response exceeded the requested row bound")
payload = base64.b64decode(snapshot["render_vt_base64"])
expected_advances = snapshot["history_rows"] + geometry["rows"] - 1
if payload.endswith(b"\r\n") or payload.count(b"\r\n") != expected_advances:
    raise RuntimeError("history payload did not preserve the exact row split")
if marker.encode() not in payload or "é".encode() not in payload or "界".encode() not in payload or b"48;5;1" not in payload:
    raise RuntimeError("history snapshot lost newest content, Unicode, or colored blanks")
if resets:
    raise RuntimeError("read-only history unexpectedly reset the live stream")

current = frames[-1]
request(
    "control", attachment_handle=opened["attachment_handle"], operation_sequence=1,
    last_reset_generation=current["reset_generation"], last_output_sequence=current["output_sequence"],
    columns=current["geometry"]["columns"], rows=current["geometry"]["rows"],
)
request(
    "release", attachment_handle=opened["attachment_handle"], operation_sequence=2,
    last_reset_generation=current["reset_generation"], last_output_sequence=current["output_sequence"],
)
request("close", attachment_handle=opened["attachment_handle"])
process.stdin.close()
exit_code = process.wait(timeout=8)
stderr = process.stderr.read()
if exit_code != 0 or stderr:
    raise RuntimeError(f"history service exit={exit_code} stderr={stderr!r}")

owner = subprocess.run(
    [tmux_bin, "-S", socket, "show-options", "-v", "-t", "sidecar-sh-mobile-m0c", "@sidecar-owner"],
    env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
).stdout.strip()
if owner:
    raise RuntimeError("read-only history or release left a mobile owner")

print(json.dumps({
    "schema": "sidecar.mobile.history-proof.v0",
    "api_instance": hello["api_instance"],
    "applied_checkpoint": initial["output_sequence"],
    "latest_live_sequence": frames[-1]["output_sequence"],
    "lagging_checkpoint_accepted": initial["output_sequence"] < frames[-1]["output_sequence"],
    "requested_history_rows": 10,
    "delivered_history_rows": snapshot["history_rows"],
    "history_size": snapshot["history_size"],
    "history_start": snapshot["start_line"],
    "history_end": snapshot["end_line"],
    "payload_bytes": len(payload),
    "row_advances": expected_advances,
    "newest_unicode_color_visible": True,
    "resets": len(resets),
    "control_after_history": True,
    "owner_after_release": "empty",
    "service_exit": exit_code,
}, indent=2, sort_keys=True))
