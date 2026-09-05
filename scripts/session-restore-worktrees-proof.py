#!/usr/bin/env python3
"""Prove worktree and terminal-split cold recovery on the private harness server."""
import datetime
import json
import os
import pathlib
import subprocess
import sys
import urllib.parse


def main():
    socket, binary, config = sys.argv[1:]
    root = pathlib.Path(config).parent.resolve()
    if os.environ.get("SIDECAR_ISOLATED_STATE") != "1" or not pathlib.Path(socket).resolve().is_relative_to(root):
        raise RuntimeError("proof requires reboot-harness isolation")
    env = dict(os.environ)
    for key in ("TMUX", "TMUX_PANE", "SIDECAR_TMUX_SERVER"):
        env.pop(key, None)
    # Keep prompt settlement deterministic; interactive zsh themes may update
    # asynchronously after the executor's empty-input observation.
    env["SHELL"] = "/bin/bash"

    state_dir = pathlib.Path(env["XDG_STATE_HOME"]) / "sidecar"
    worktree = root / "project" / "proof-worktree"
    missing = root / "project" / "deleted-worktree"
    worktree.mkdir(parents=True)
    ws, tp, gone = "sidecar-ws-proof", "sidecar-tp-proof", "sidecar-ws-missing"

    def tmux(*args, check=True):
        result = subprocess.run(["tmux", "-S", socket, *args], env=env, capture_output=True, text=True, timeout=15)
        if check and result.returncode:
            raise RuntimeError(result.stdout + result.stderr)
        return result

    def cli(*args):
        result = subprocess.run([binary, "-config", config, "session", *args], env=env, capture_output=True, text=True, timeout=60)
        if result.returncode:
            raise RuntimeError(result.stdout + result.stderr)
        return result.stdout

    def stamp(value):
        return value.isoformat().replace("+00:00", "Z")

    # These are the durable artifacts left by the real creation paths. Seed
    # them explicitly so the proof can model the crash boundary without a TUI.
    for session in (ws, tp):
        tmux("new-session", "-d", "-s", session, "-c", str(worktree))
    server = tmux("display-message", "-p", "#{pid}").stdout.strip()
    now = datetime.datetime.now(datetime.timezone.utc)
    created = now - datetime.timedelta(hours=1)
    manifest = {
        "version": 3,
        "shells": [
            {"tmuxName": name, "displayName": label, "namespace": socket,
             "createdAt": stamp(created), "workDir": str(path),
             "restore": {"eligible": True, "lastSeenServer": "pid=" + server, "lastSeenAliveAt": stamp(now)}}
            for name, label, path in ((ws, "proof worktree", worktree), (tp, "proof split", worktree), (gone, "deleted worktree", missing))
        ],
    }
    (state_dir / "recovery-sessions.json").write_text(json.dumps(manifest, indent=2) + "\n")
    layout = {"root": str(worktree), "projectRoot": str(root / "project"),
            "workspaceKind": "worktree", "open": True,
            "split": {"axis": "cols", "a": {"kind": "terminal"},
                      "b": {"kind": "shell", "session": tp, "name": "proof split"}}}
    pathlib.Path(config).with_name("state.json").write_text(json.dumps({
        "sessionsPaneLayouts": {"proof": layout},
        "workspace": {str(worktree): {"paneLayouts": {"local": layout}}},
    }))

    # Two native conversations in one provider store let claimed-reference
    # filtering assign one candidate to each recovered pane.
    store = pathlib.Path(env["HOME"]) / ".grok/sessions" / urllib.parse.quote(str(worktree.resolve()), safe="")
    for index, identity in enumerate(("019f0000-1111-7000-8000-000000000001", "019f0000-2222-7000-8000-000000000002")):
        directory = store / identity
        directory.mkdir(parents=True, exist_ok=True)
        summary = {"info": {"id": identity, "cwd": str(worktree.resolve())}, "session_summary": "worktree proof",
                   "created_at": stamp(created), "updated_at": stamp(now - datetime.timedelta(minutes=index + 1)),
                   "num_messages": 1, "num_chat_messages": 1}
        (directory / "summary.json").write_text(json.dumps(summary))

    marker = root / "provider-executed"
    fakebin = root / "fakebin"
    fakebin.mkdir(exist_ok=True)
    provider = fakebin / "grok"
    provider.write_text('#!/bin/sh\nprintf executed >> "$SIDECAR_PROOF_EXECUTION_MARKER"\n')
    provider.chmod(0o755)
    env["PATH"] = str(fakebin) + os.pathsep + env["PATH"]
    env["SIDECAR_PROOF_EXECUTION_MARKER"] = str(marker)

    tmux("kill-server")
    status = cli("status", "--json")
    if gone not in status or "missing_workdir" not in status:
        raise RuntimeError("missing worktree was not refused: " + status)
    restored = cli("restore", "--prefill", "--json")
    print(restored.strip())
    for session in (ws, tp):
        if tmux("has-session", "-t", session, check=False).returncode:
            raise RuntimeError("session was not recreated: " + session + "\n" + restored)
        screen = tmux("capture-pane", "-p", "-t", session).stdout
        if "grok" not in screen or "--resume" not in screen:
            raise RuntimeError("candidate was not prefilled in " + session + ": " + screen)
    if tmux("has-session", "-t", gone, check=False).returncode == 0:
        raise RuntimeError("missing-directory session was recreated")
    if marker.exists():
        raise RuntimeError("prefill executed the provider")
    repeated = cli("restore", "--prefill", "--json")
    for session in (ws, tp):
        screen = tmux("capture-pane", "-p", "-t", session).stdout
        if screen.count("--resume") != 1:
            raise RuntimeError("repeat restore changed prefilled input in " + session + ": " + screen + "\n" + repeated)
    if marker.exists():
        raise RuntimeError("repeat restore executed the provider")
    print("WORKTREE GATE PASSED: worktree+split recreated and prefilled once; missing cwd refused; provider never executed; repeat converged")
    tmux("kill-server", check=False)


if __name__ == "__main__":
    main()
