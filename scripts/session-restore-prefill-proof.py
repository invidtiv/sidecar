#!/usr/bin/env python3
"""Prove prefill and provider candidates inside the reboot harness only."""
import datetime
import json
import os
import pathlib
import subprocess
import sys
import time
import urllib.parse


def main():
    socket, binary, config, mode = sys.argv[1:]
    root = pathlib.Path(config).parent.resolve()
    if os.environ.get("SIDECAR_ISOLATED_STATE") != "1" or not pathlib.Path(socket).resolve().is_relative_to(root):
        raise RuntimeError("proof requires reboot-harness isolation")
    env = dict(os.environ)
    for key in ("TMUX", "TMUX_PANE", "SIDECAR_TMUX_SERVER"):
        env.pop(key, None)
    fakebin = root / "fakebin"
    fakebin.mkdir(exist_ok=True)
    marker = root / "provider-executed"
    for provider in ("codex", "grok"):
        script = fakebin / provider
        script.write_text('#!/bin/sh\nprintf executed >> "$SIDECAR_PROOF_EXECUTION_MARKER"\n')
        script.chmod(0o755)
    env["PATH"] = str(fakebin) + os.pathsep + env["PATH"]
    env["SIDECAR_PROOF_EXECUTION_MARKER"] = str(marker)
    manifest = pathlib.Path(env["XDG_STATE_HOME"]) / "sidecar/projects/harness/shells.json"
    original = json.loads(manifest.read_text())

    def tmux(*args):
        return subprocess.run(["tmux", "-S", socket, *args], env=env, capture_output=True, text=True, timeout=10)

    def cli(*args):
        result = subprocess.run([binary, "-config", config, "session", *args], env=env, capture_output=True, text=True, timeout=60)
        if result.returncode:
            raise RuntimeError(result.stdout + result.stderr)
        return result.stdout

    def capture(session):
        result = tmux("capture-pane", "-p", "-t", session)
        if result.returncode:
            raise RuntimeError(result.stderr)
        return result.stdout

    def stamp(value):
        return value.isoformat().replace("+00:00", "Z")

    def assert_no_execution():
        if marker.exists():
            raise RuntimeError("prefill executed a provider")

    def assert_idempotent(session):
        def completion():
            saved = next(s for s in json.loads(manifest.read_text())["shells"] if s["tmuxName"] == session)
            restore = saved.get("restore", {})
            markers = (restore.get("prefillClaimedAt"), restore.get("prefilledAt"))
            if not all(value and not value.startswith("0001-") for value in markers):
                raise RuntimeError("successful prefill lost its durable claim/completion markers")
            return markers
        markers = completion()
        before = capture(session)
        cli("restore", "--prefill", "--json")
        after = capture(session)
        if before != after:
            raise RuntimeError("second restore changed pane bytes")
        if completion() != markers:
            raise RuntimeError("second restore changed durable prefill markers")
        assert_no_execution()

    try:
        if mode == "prefill":
            tmux("kill-server")
            result = cli("restore", "--prefill", "--json")
            print(result.strip())
            reviewer = next(s for s in original["shells"] if s["displayName"] == "reviewer")
            expected = "codex resume " + reviewer["agent"]["session"]["value"]
            screen = capture(reviewer["tmuxName"])
            print(screen.rstrip())
            if expected not in screen or '"prefilled"' not in result:
                raise RuntimeError("exact bound resume command was not prefilled")
            assert_idempotent(reviewer["tmuxName"])
            print("PREFILL GATE PASSED: exact argv visible, no execution, repeat byte-identical")
            return

        pathlib.Path(config).write_text(json.dumps({"plugins": {"workspace": {"sessionRestore": {"recreateShells": True, "resumeAgents": "auto"}}}}))
        for tied in (False, True):
            tmux("kill-server")
            doc = json.loads(json.dumps(original))
            shell = next(s for s in doc["shells"] if s["displayName"] == "builder")
            doc["shells"] = [shell]
            now = datetime.datetime.now(datetime.timezone.utc)
            shell["createdAt"] = stamp(now - datetime.timedelta(hours=1))
            shell["agentType"] = "grok"
            shell.pop("agent", None)  # Incident-era record: agentType only.
            shell["restore"]["serverLostAt"] = stamp(now)
            manifest.write_text(json.dumps(doc))
            workdir = str(pathlib.Path(shell["workDir"]).resolve())
            store = pathlib.Path(env["HOME"]) / ".grok/sessions" / urllib.parse.quote(workdir, safe="")
            for index, identity in enumerate(("019f0000-aaaa-7000-8000-000000000001", "019f0000-bbbb-7000-8000-000000000002")):
                directory = store / identity
                directory.mkdir(parents=True, exist_ok=True)
                updated = now - datetime.timedelta(minutes=1 if tied or index else 2)
                summary = {"info": {"id": identity, "cwd": workdir}, "session_summary": "candidate " + str(index),
                           "created_at": shell["createdAt"], "updated_at": stamp(updated), "num_messages": 1, "num_chat_messages": 1}
                (directory / "summary.json").write_text(json.dumps(summary))
            status = cli("status", "--json")
            for identity in ("019f0000-aaaa-7000-8000-000000000001", "019f0000-bbbb-7000-8000-000000000002"):
                if identity not in status:
                    raise RuntimeError("candidate reason did not name both considered conversations: " + status)
            # Even --agents under auto must only TYPE discovered candidates.
            result = cli("restore", "--agents", "--json")
            print(("tied" if tied else "nearest") + " candidate restore:", result.strip())
            screen = capture(shell["tmuxName"])
            print(screen.rstrip())
            if "grok" not in screen or "--resume" not in screen or '"prefilled"' not in result:
                raise RuntimeError("candidate/picker was not prefilled")
            if tied:
                if "019f0000" in screen:
                    raise RuntimeError("ambiguous candidate typed an exact ID instead of picker")
            elif "019f0000-bbbb-7000-8000-000000000002" not in screen:
                raise RuntimeError("nearest candidate was not selected")
            after = json.loads(manifest.read_text())["shells"][0]
            if after.get("agent", {}).get("session", {}).get("reported"):
                raise RuntimeError("candidate became a reported binding")
            assert_idempotent(shell["tmuxName"])
        print("CANDIDATE GATE PASSED: nearest and tied picker, auto never executes, repeat byte-identical")
    finally:
        tmux("kill-server")


if __name__ == "__main__":
    main()
