#!/usr/bin/env python3
"""Reboot-harness proof: a blocked control client keeps tmux exit-pending.

The caller supplies its isolated socket, binary, and config. No default socket
is queried. The control client's parent stays alive for the refusal check, then
exits so the same client becomes an orphan that restore may terminate.
"""
import os
import pathlib
import subprocess
import sys
import time


def main():
    socket, binary, config = sys.argv[1:]
    root = pathlib.Path(config).parent.resolve()
    if os.environ.get("SIDECAR_ISOLATED_STATE") != "1" or not pathlib.Path(socket).resolve().is_relative_to(root):
        raise RuntimeError("proof requires the reboot harness's private socket and state")
    env = dict(os.environ)
    for key in ("TMUX", "TMUX_PANE", "SIDECAR_TMUX_SERVER"):
        env.pop(key, None)

    def tmux(*args):
        return subprocess.run(["tmux", "-S", socket, *args], env=env, capture_output=True, text=True, timeout=10)

    def cli(*args):
        return subprocess.run([binary, "-config", config, "session", *args], env=env, capture_output=True, text=True, timeout=30)

    session = "sidecar-sh-exit-pending-proof"
    result = tmux("new-session", "-d", "-s", session)
    if result.returncode:
        raise RuntimeError(result.stderr)
    input_r, input_w = os.pipe()
    output_r, output_w = os.pipe()
    parent = None
    client_pid = None
    try:
        # Deliberately never drain output_r. The pipe fills with control-mode
        # output and prevents the client from acknowledging server shutdown.
        launcher = (
            "import subprocess,sys; "
            "p=subprocess.Popen(sys.argv[3:],stdin=int(sys.argv[1]),stdout=int(sys.argv[2]),stderr=subprocess.DEVNULL); "
            "print(p.pid,flush=True); sys.stdin.read()"
        )
        parent = subprocess.Popen(
            [sys.executable, "-c", launcher, str(input_r), str(output_w),
             "tmux", "-S", socket, "-C", "attach-session", "-f", "ignore-size", "-t", session],
            env=env, pass_fds=(input_r, output_w), stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, text=True,
        )
        client_pid = int(parent.stdout.readline().strip())
        time.sleep(0.2)
        command = "python3 -c \"import sys; sys.stdout.write('x'*10000000); sys.stdout.flush()\""
        if tmux("send-keys", "-t", session, command, "Enter").returncode:
            raise RuntimeError("could not fill the private control output")
        time.sleep(1)
        if tmux("kill-server").returncode:
            raise RuntimeError("could not terminate the harness server")
        result = tmux("list-sessions")
        if "server exited unexpectedly" not in result.stderr:
            raise RuntimeError("fixture did not reach exit-pending: " + result.stderr)
        result = cli("status", "--json")
        combined = result.stdout + result.stderr
        print("live-parent status:", combined.strip())
        if str(client_pid) not in combined:
            raise RuntimeError("status did not identify the blocking client")
        create = subprocess.run(
            [binary, "-config", config, "create", "shell", "--project", "harness", "--name", "blocked proof", "--wait", "0", "--json"],
            env=env, capture_output=True, text=True, timeout=15,
        )
        print("stuck create:", (create.stdout + create.stderr).strip())
        if create.returncode == 0 or "shutting down" not in create.stdout + create.stderr:
            raise RuntimeError("Create Shell did not explain the shutting-down server")
        result = cli("restore", "--json")
        print("live-parent restore:", (result.stdout + result.stderr).strip())
        os.kill(client_pid, 0)
        if result.returncode == 0:
            raise RuntimeError("restore did not refuse a live-parent blocker")
        # Only the fixture's launcher is terminated. The control client keeps
        # its output pipe open and is reparented to init.
        parent.terminate()
        parent.wait(timeout=5)
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            ppid = subprocess.check_output(["ps", "-o", "ppid=", "-p", str(client_pid)], text=True).strip()
            if ppid == "1":
                break
            time.sleep(0.05)
        else:
            raise RuntimeError("control client did not become an orphan")
        result = cli("restore", "--json")
        print("orphan restore:", (result.stdout + result.stderr).strip())
        if result.returncode:
            raise RuntimeError("restore did not recover the orphan-blocked server")
        result = cli("status", "--json")
        print("recovered status:", (result.stdout + result.stderr).strip())
        if result.returncode or tmux("list-sessions").returncode:
            raise RuntimeError("seeded sessions did not return on the new server")
        print("STUCK GATE PASSED: live parent refused, orphan cleared, seeded shells restored")
    finally:
        if parent is not None and parent.poll() is None:
            parent.terminate()
            parent.wait(timeout=5)
        # Closing both ends releases a fixture client even when an assertion
        # fails, without signalling a PID that might have been recycled.
        for fd in (input_r, input_w, output_r, output_w):
            os.close(fd)
        tmux("kill-server")


if __name__ == "__main__":
    main()
