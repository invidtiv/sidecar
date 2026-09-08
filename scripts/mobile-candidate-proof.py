#!/usr/bin/env python3
import base64
import json
import os
import queue
import statistics
import subprocess
import sys
import threading
import time


root, tmux_bin = sys.argv[1:3]
binary = os.path.join(root, "sidecar")
config = os.path.join(root, "config", "config.json")
wrapper = os.path.join(root, "serve")
repo = os.path.join(root, "repo")
socket = os.path.join(root, "tmux", f"tmux-{os.getuid()}", "default")
env = os.environ.copy()
env.pop("TMUX", None)
env.pop("TMUX_PANE", None)
env["XDG_STATE_HOME"] = os.path.join(root, "state")
env["TMUX_TMPDIR"] = os.path.join(root, "tmux")
env["SIDECAR_ISOLATED_STATE"] = "1"


def tmux(*args, capture=False):
    result = subprocess.run(
        [tmux_bin, "-S", socket, *args], check=True, env=env,
        stdout=subprocess.PIPE if capture else subprocess.DEVNULL, text=True,
    )
    return result.stdout.strip() if capture else ""


def start_session(name):
    tmux(
        "new-session", "-d", "-s", name, "-x", "80", "-y", "24", "-c", repo,
        "env BASH_SILENCE_DEPRECATION_WARNING=1 PS1='candidate> ' /bin/bash --noprofile --norc",
    )


def query_catalog():
    started = time.monotonic()
    result = subprocess.run(
        [binary, "-config", config, "mobile", "sessions", "--json", "--sort", "name"],
        check=True, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    snapshot = json.loads(result.stdout)
    rows = [row for section in snapshot["sections"] for row in section["rows"]]
    row = next(row for row in rows if row["workspace_kind"] == "worktree" and row["project_name"] == "Candidate proof")
    return snapshot, row, (time.monotonic() - started) * 1000


class Service:
    def __init__(self):
        self.process = subprocess.Popen(
            [wrapper], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, env=env, bufsize=1,
        )
        self.lines = queue.Queue()
        self.frames = []
        self.resets = []
        self.index = 0
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        for line in self.process.stdout:
            self.lines.put(line)
        self.lines.put(None)

    def receive(self, timeout=8):
        try:
            line = self.lines.get(timeout=timeout)
        except queue.Empty as error:
            raise RuntimeError("timed out waiting for candidate service response") from error
        if not line:
            raise RuntimeError("candidate service closed: " + self.process.stderr.read())
        message = json.loads(line)
        if message["type"] == "frame":
            self.frames.append(message)
        elif message["type"] == "reset":
            self.resets.append(message)
        return message

    def request(self, kind, expect_error=False, **fields):
        self.index += 1
        request_id = f"candidate-proof-{self.index}"
        message = {"version": 0, "type": kind, "request_id": request_id, **fields}
        self.process.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
        self.process.stdin.flush()
        while True:
            response = self.receive()
            if response.get("request_id") != request_id:
                continue
            if response["type"] == "error" and not expect_error:
                raise RuntimeError(json.dumps(response))
            return response

    def close(self):
        self.process.stdin.close()
        code = self.process.wait(timeout=8)
        stderr = self.process.stderr.read()
        if code != 0 or stderr:
            raise RuntimeError(f"candidate service exit={code} stderr={stderr!r}")


zero_snapshot, zero_row, zero_ms = query_catalog()
if zero_row.get("candidates") or zero_row.get("attachment_ready") or zero_row["attach_state"] != "unavailable":
    raise RuntimeError("zero-candidate workspace was not explicitly unavailable")

start_session("candidate-one")
one_snapshot, one_row, one_ms = query_catalog()
if len(one_row.get("candidates", [])) != 1 or not one_row.get("attachment_ready") or one_row.get("ambiguous"):
    raise RuntimeError("one-candidate workspace was not direct exact authority")

start_session("candidate-two")
many_snapshot, many_row, many_ms = query_catalog()
if len(many_row.get("candidates", [])) != 2 or many_row.get("attachment_ready") or not many_row.get("ambiguous"):
    raise RuntimeError("multi-candidate workspace did not require an exact choice")
old_candidate = next(candidate for candidate in many_row["candidates"] if candidate["session"] == "candidate-two")
tmux("kill-session", "-t", "candidate-two")

stale = Service()
stale.request("hello")
stale_response = stale.request("resolve", expect_error=True, target=old_candidate["selector"], expected_target=old_candidate["expected_target"])
if stale_response["type"] != "error" or stale_response["error"]["code"] != "identity_changed":
    raise RuntimeError("removed candidate selection did not fail closed")
stale.close()

start_session("candidate-two")
fresh_snapshot, fresh_row, fresh_ms = query_catalog()
fresh_candidate = next(candidate for candidate in fresh_row["candidates"] if candidate["session"] == "candidate-two")
if fresh_candidate["selector"] == old_candidate["selector"] or fresh_candidate["pane"] == old_candidate["pane"]:
    raise RuntimeError("same-name pane replacement retained old candidate authority")

service = Service()
hello = service.request("hello")
resolved = service.request("resolve", target=fresh_candidate["selector"], expected_target=fresh_candidate["expected_target"])
opened = service.request("open", target_handle=resolved["target"]["handle"], attachment_id="candidate-proof")
while not service.frames:
    service.receive()
frame = service.frames[-1]
attachment = opened["attachment_handle"]
operation = 1
control = service.request(
    "control", attachment_handle=attachment, operation_sequence=operation,
    last_reset_generation=frame["reset_generation"], last_output_sequence=frame["output_sequence"],
    columns=frame["geometry"]["columns"], rows=frame["geometry"]["rows"],
)
if not control.get("control"):
    raise RuntimeError("selected candidate did not acquire control")

pane = fresh_candidate["pane"]
output_command = "i=0; while [ $i -lt 120 ]; do printf 'CANDIDATE-OUTPUT-%03d\\n' \"$i\"; i=$((i+1)); sleep 0.08; done &"
tmux("send-keys", "-l", "-t", pane, output_command)
tmux("send-keys", "-t", pane, "Enter")
latencies = []
input_count = 0
heartbeat_count = 0
for index in range(30):
    operation += 1
    latest = service.frames[-1]
    started = time.monotonic()
    if index % 2 == 0:
        response = service.request(
            "input", attachment_handle=attachment, operation_sequence=operation,
            last_reset_generation=latest["reset_generation"], last_output_sequence=latest["output_sequence"],
            data_base64=base64.b64encode(b" ").decode(),
        )
        input_count += 1
        if response["type"] != "accepted":
            raise RuntimeError("candidate input was not accepted")
    else:
        response = service.request(
            "heartbeat", attachment_handle=attachment, operation_sequence=operation,
            last_reset_generation=latest["reset_generation"], last_output_sequence=latest["output_sequence"],
        )
        heartbeat_count += 1
        if response["type"] != "heartbeat":
            raise RuntimeError("candidate heartbeat was not accepted")
    latencies.append((time.monotonic() - started) * 1000)

deadline = time.monotonic() + 4
while time.monotonic() < deadline:
    if service.frames and b"CANDIDATE-OUTPUT" in base64.b64decode(service.frames[-1]["render_vt_base64"]):
        break
    service.receive()
else:
    raise RuntimeError("newest synthetic output did not reach selected candidate")

old_frame = service.frames[-1]
resets_before_replacement = len(service.resets)
if resets_before_replacement:
    raise RuntimeError("ordinary candidate output or operations reset the stream")
ordered_sequences = [frame["output_sequence"] for frame in service.frames]
if any(right <= left for left, right in zip(ordered_sequences, ordered_sequences[1:])):
    raise RuntimeError("candidate output sequence was not strictly increasing")

start_session("candidate-three")
operation += 1
membership_changed = service.request(
    "heartbeat", expect_error=True, attachment_handle=attachment, operation_sequence=operation,
    last_reset_generation=old_frame["reset_generation"], last_output_sequence=old_frame["output_sequence"],
)
if membership_changed["type"] != "error" or membership_changed["error"]["code"] != "identity_changed":
    raise RuntimeError("complete candidate membership change did not revoke old attachment")
service.request("close", attachment_handle=attachment)
service.close()
tmux("kill-session", "-t", "candidate-three")

incarnation = Service()
incarnation_hello = incarnation.request("hello")
incarnation_resolved = incarnation.request("resolve", target=fresh_candidate["selector"], expected_target=fresh_candidate["expected_target"])
incarnation_opened = incarnation.request("open", target_handle=incarnation_resolved["target"]["handle"], attachment_id="candidate-incarnation-proof")
while not incarnation.frames:
    incarnation.receive()
incarnation_frame = incarnation.frames[-1]
incarnation_attachment = incarnation_opened["attachment_handle"]
incarnation.request(
    "control", attachment_handle=incarnation_attachment, operation_sequence=1,
    last_reset_generation=incarnation_frame["reset_generation"], last_output_sequence=incarnation_frame["output_sequence"],
    columns=incarnation_frame["geometry"]["columns"], rows=incarnation_frame["geometry"]["rows"],
)
tmux("kill-session", "-t", "candidate-two")
start_session("candidate-two")
replaced = incarnation.request(
    "heartbeat", expect_error=True, attachment_handle=incarnation_attachment, operation_sequence=2,
    last_reset_generation=incarnation_frame["reset_generation"], last_output_sequence=incarnation_frame["output_sequence"],
)
if replaced["type"] != "error" or replaced["error"]["code"] != "identity_changed":
    raise RuntimeError("same-name live session replacement did not revoke old attachment")
incarnation.request("close", attachment_handle=incarnation_attachment)
incarnation.close()

start_session("candidate-split")
tmux(
    "split-window", "-h", "-t", "candidate-split", "-c", repo,
    "env BASH_SILENCE_DEPRECATION_WARNING=1 PS1='sibling> ' /bin/bash --noprofile --norc",
)
split_snapshot, split_row, split_catalog_ms = query_catalog()
split_candidates = [candidate for candidate in split_row["candidates"] if candidate["session"] == "candidate-split"]
if len(split_candidates) != 2:
    raise RuntimeError("split window did not produce two exact selectable candidates")
split_candidate = split_candidates[0]
split_sibling = split_candidates[1]
split_service = Service()
split_service.request("hello")
split_resolved = split_service.request("resolve", target=split_candidate["selector"], expected_target=split_candidate["expected_target"])
split_opened = split_service.request("open", target_handle=split_resolved["target"]["handle"], attachment_id="candidate-split-proof")
while not split_service.frames:
    split_service.receive()
split_initial = split_service.frames[-1]
split_control = split_service.request(
    "control", attachment_handle=split_opened["attachment_handle"], operation_sequence=1,
    last_reset_generation=split_initial["reset_generation"], last_output_sequence=split_initial["output_sequence"],
    columns=46, rows=23,
)
deadline = time.monotonic() + 8
split_frame = None
while time.monotonic() < deadline:
    message = split_service.receive()
    if message["type"] == "frame" and message["reset_generation"] == split_control["reset_generation"]:
        split_frame = message
        break
if split_frame is None or split_frame["geometry"] != {"columns": 46, "rows": 23}:
    raise RuntimeError("split selection did not emit its verified accepted pane geometry")
control_geometry = tmux("display-message", "-p", "-t", split_candidate["pane"], "#{pane_width}x#{pane_height}", capture=True)
sibling_control_geometry = tmux("display-message", "-p", "-t", split_sibling["pane"], "#{pane_width}x#{pane_height}", capture=True)
if control_geometry != "46x23" or not sibling_control_geometry or split_sibling["pane"] == split_candidate["pane"]:
    raise RuntimeError("split geometry targeted the wrong pane or removed its sibling")
split_resize = split_service.request(
    "resize", attachment_handle=split_opened["attachment_handle"], operation_sequence=2,
    last_reset_generation=split_frame["reset_generation"], last_output_sequence=split_frame["output_sequence"],
    columns=50, rows=20,
)
deadline = time.monotonic() + 8
while time.monotonic() < deadline:
    message = split_service.receive()
    if message["type"] == "frame" and message["reset_generation"] == split_resize["reset_generation"]:
        split_frame = message
        break
else:
    raise RuntimeError("split resize did not emit its replacement frame")
resize_geometry = tmux("display-message", "-p", "-t", split_candidate["pane"], "#{pane_width}x#{pane_height}", capture=True)
sibling_resize_geometry = tmux("display-message", "-p", "-t", split_sibling["pane"], "#{pane_width}x#{pane_height}", capture=True)
if split_frame["geometry"] != {"columns": 50, "rows": 20} or resize_geometry != "50x20" or not sibling_resize_geometry:
    raise RuntimeError("split resize did not verify the selected pane's accepted geometry")
split_marker = b"printf 'SPLIT-SELECTED\\n'\r"
split_service.request(
    "input", attachment_handle=split_opened["attachment_handle"], operation_sequence=3,
    last_reset_generation=split_frame["reset_generation"], last_output_sequence=split_frame["output_sequence"],
    data_base64=base64.b64encode(split_marker).decode(),
)
deadline = time.monotonic() + 8
while time.monotonic() < deadline:
    message = split_service.receive()
    if message["type"] == "frame" and b"SPLIT-SELECTED" in base64.b64decode(message["render_vt_base64"]):
        split_frame = message
        break
else:
    raise RuntimeError("split input did not reach the selected pane")
sibling_capture = subprocess.run(
    [tmux_bin, "-S", socket, "capture-pane", "-p", "-t", split_sibling["pane"]],
    check=True, env=env, text=True, stdout=subprocess.PIPE,
).stdout
if "SPLIT-SELECTED" in sibling_capture:
    raise RuntimeError("split input reached the sibling pane")
split_service.request(
    "release", attachment_handle=split_opened["attachment_handle"], operation_sequence=4,
    last_reset_generation=split_frame["reset_generation"], last_output_sequence=split_frame["output_sequence"],
)
split_service.request("close", attachment_handle=split_opened["attachment_handle"])
split_service.close()

owner_result = subprocess.run(
    [tmux_bin, "-S", socket, "show-options", "-v", "-t", "candidate-two", "@sidecar-owner"],
    env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
)
owner = owner_result.stdout.strip()
split_owner_result = subprocess.run(
    [tmux_bin, "-S", socket, "show-options", "-v", "-t", "candidate-split", "@sidecar-owner"],
    env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
)
split_owner = split_owner_result.stdout.strip()
if owner or split_owner:
    raise RuntimeError("candidate proof left a mobile owner")

print(json.dumps({
    "schema": "sidecar.mobile.candidate-proof.v0",
    "api_instance": hello["api_instance"],
    "incarnation_api_instance": incarnation_hello["api_instance"],
    "api_instances_distinct": hello["api_instance"] != incarnation_hello["api_instance"],
    "catalog_ms": {"zero": round(zero_ms, 3), "one": round(one_ms, 3), "many": round(many_ms, 3), "fresh": round(fresh_ms, 3)},
    "candidate_counts": {"zero": 0, "one": 1, "many": 2},
    "direct_one_ready": True,
    "many_requires_choice": True,
    "removed_selection_error": stale_response["error"]["code"],
    "same_name_replacement_changed_selector": True,
    "complete_membership_change_error": membership_changed["error"]["code"],
    "same_name_live_attachment_error": replaced["error"]["code"],
    "split_window": {
        "catalog_candidates": len(split_row["candidates"]),
        "selected_pane": split_candidate["pane"],
        "sibling_pane": split_sibling["pane"],
        "control_geometry": control_geometry,
        "sibling_control_geometry": sibling_control_geometry,
        "resize_geometry": resize_geometry,
        "sibling_resize_geometry": sibling_resize_geometry,
        "selected_input_visible": True,
        "sibling_input_absent": True,
        "catalog_ms": round(split_catalog_ms, 3),
        "catalog_generation": split_snapshot["generation"],
    },
    "selected_session": fresh_candidate["session"],
    "selected_pane": fresh_candidate["pane"],
    "operations_under_output": len(latencies),
    "input_operations": input_count,
    "heartbeat_operations": heartbeat_count,
    "operation_latency_ms": {
        "median": round(statistics.median(latencies), 3),
        "p95": round(sorted(latencies)[int(len(latencies) * 0.95) - 1], 3),
        "max": round(max(latencies), 3),
    },
    "emitted_frames_before_membership_change": len(ordered_sequences),
    "first_output_sequence": ordered_sequences[0],
    "last_output_sequence": ordered_sequences[-1],
    "newest_synthetic_output_visible": True,
    "resets_before_replacement": resets_before_replacement,
    "owner_after_replacement": "empty",
    "catalog_generations": [zero_snapshot["generation"], one_snapshot["generation"], many_snapshot["generation"], fresh_snapshot["generation"]],
}, indent=2, sort_keys=True))
