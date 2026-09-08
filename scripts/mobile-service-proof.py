#!/usr/bin/env python3
import base64
import json
import os
import queue
import re
import subprocess
import sys
import threading
import time

root, transport = sys.argv[1:3]
wrapper = os.path.join(root, "serve")
transcript_path = os.path.join(root, "transcript.jsonl")
records = []


def start():
    if transport == "ssh":
        argv = ["ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "aerie.local", wrapper]
        env = os.environ.copy()
    else:
        argv = [wrapper]
        env = os.environ.copy()
    env.pop("TMUX", None)
    env.pop("TMUX_PANE", None)
    process = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env, bufsize=1)
    process.lines = queue.Queue()
    def read_lines():
        for line in process.stdout:
            process.lines.put(line)
        process.lines.put(None)
    threading.Thread(target=read_lines, daemon=True).start()
    return process


def receive(process):
    try:
        line = process.lines.get(timeout=8)
    except queue.Empty:
        raise RuntimeError("timed out waiting for service response")
    if not line:
        raise RuntimeError("service closed: " + process.stderr.read())
    message = json.loads(line)
    records.append({"direction": "server_to_client", "message": message})
    return message


def request(process, kind, **fields):
    message = {"version": 0, "type": kind, "request_id": f"proof-{len(records) + 1}", **fields}
    records.append({"direction": "client_to_server", "message": message})
    process.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
    process.stdin.flush()
    while True:
        response = receive(process)
        if response.get("request_id") == message["request_id"]:
            if response["type"] == "error":
                raise RuntimeError(json.dumps(response))
            return response


def event(process, kind):
    while True:
        message = receive(process)
        if message["type"] == kind:
            return message


started = time.monotonic()
first = start()
hello1 = request(first, "hello")
resolved = request(first, "resolve", target="sidecar-sh-mobile-m0c")
target = resolved["target"]
opened = request(first, "open", target_handle=target["handle"], attachment_id="proof-phone")
initial = event(first, "frame")
open_ms = round((time.monotonic() - started) * 1000, 3)
attachment = opened["attachment_handle"]
reset = initial["reset_generation"]
output = initial["output_sequence"]
request(first, "control", attachment_handle=attachment, operation_sequence=1, last_reset_generation=reset,
        last_output_sequence=output, columns=80, rows=24)
token = b"SIDECAR_MOBILE_SYNTHETIC_PROOF"
input_started = time.monotonic()
request(first, "input", attachment_handle=attachment, operation_sequence=2, last_reset_generation=reset,
        last_output_sequence=output,
        data_base64=base64.b64encode(b"printf '\\033[41m  \\033[0m" + " é 界 ".encode("utf-8") + token + b"\\n'\r").decode())
while True:
    before_disconnect = event(first, "frame")
    rendered = base64.b64decode(before_disconnect["render_vt_base64"])
    if token in rendered:
        break
if re.search(rb"\x1b\[[0-9;]*41[0-9;]*m", rendered) is None and b"48;5;1" not in rendered:
    raise RuntimeError("normalized frame omitted the colored blank")
if "é".encode("utf-8") not in rendered or "界".encode("utf-8") not in rendered:
    raise RuntimeError("normalized frame omitted combining or wide Unicode")
input_ms = round((time.monotonic() - input_started) * 1000, 3)
reset = before_disconnect["reset_generation"]
output = before_disconnect["output_sequence"]
resize_started = time.monotonic()
resized = request(first, "resize", attachment_handle=attachment, operation_sequence=3, last_reset_generation=reset,
                  last_output_sequence=output, columns=70, rows=20)
reset_event = event(first, "reset")
while True:
    resized_frame = event(first, "frame")
    if resized_frame["reset_generation"] == resized["reset_generation"]:
        break
resize_ms = round((time.monotonic() - resize_started) * 1000, 3)
request(first, "heartbeat", attachment_handle=attachment, operation_sequence=4,
        last_reset_generation=resized_frame["reset_generation"], last_output_sequence=resized_frame["output_sequence"])
first.stdin.close()
first_exit = first.wait(timeout=8)
if first_exit != 0:
    raise RuntimeError(first.stderr.read())

second = start()
hello2 = request(second, "hello")
identity_keys = ["hub_id", "owner_host_id", "owner_config_generation", "workspace_id", "workspace_kind", "session", "pane", "server_incarnation", "target_generation"]
expected = {key: target[key] for key in identity_keys}
reconnected = request(second, "reconnect", target="sidecar-sh-mobile-m0c", expected_target=expected,
                      previous_attachment_generation=opened["attachment_generation"], attachment_id="proof-phone",
                      last_reset_generation=resized_frame["reset_generation"], last_output_sequence=resized_frame["output_sequence"])
after_reconnect = event(second, "frame")
before_count = base64.b64decode(resized_frame["render_vt_base64"]).count(token)
after_count = base64.b64decode(after_reconnect["render_vt_base64"]).count(token)
new_attachment = reconnected["attachment_handle"]
request(second, "control", attachment_handle=new_attachment, operation_sequence=1,
        last_reset_generation=after_reconnect["reset_generation"], last_output_sequence=after_reconnect["output_sequence"],
        columns=70, rows=20)
request(second, "release", attachment_handle=new_attachment, operation_sequence=2,
        last_reset_generation=after_reconnect["reset_generation"], last_output_sequence=after_reconnect["output_sequence"])
request(second, "close", attachment_handle=new_attachment)
second.stdin.close()
second_exit = second.wait(timeout=8)
if second_exit != 0:
    raise RuntimeError(second.stderr.read())

with open(transcript_path, "w", encoding="utf-8") as stream:
    for record in records:
        stream.write(json.dumps(record, separators=(",", ":"), sort_keys=True) + "\n")

summary = {
    "schema": 1,
    "transport": transport,
    "api_instances_distinct": hello1["api_instance"] != hello2["api_instance"],
    "attachment_generations": [opened["attachment_generation"], reconnected["attachment_generation"]],
    "first_exit": first_exit,
    "second_exit": second_exit,
    "no_input_replay": before_count == after_count and before_count > 0,
    "synthetic_token_occurrences": {"before_disconnect": before_count, "after_reconnect": after_count},
    "reset_transition": [initial["reset_generation"], reset_event["reset_generation"], resized_frame["reset_generation"]],
    "final_geometry": after_reconnect["geometry"],
    "latency_ms": {"hello_resolve_open_frame": open_ms, "input_to_visible_frame": input_ms, "resize_to_replacement_frame": resize_ms},
    "transcript": transcript_path,
}
if not summary["api_instances_distinct"]:
    raise RuntimeError("fresh SSH exec reused api_instance")
if summary["attachment_generations"] != [1, 2]:
    raise RuntimeError("attachment generation did not advance across reconnect")
if not summary["no_input_replay"]:
    raise RuntimeError("synthetic input was replayed or lost across reconnect")
if summary["reset_transition"] != [1, 2, 2]:
    raise RuntimeError("resize reset/replacement progression is invalid")
print(json.dumps(summary, indent=2, sort_keys=True))
