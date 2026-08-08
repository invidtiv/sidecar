// Package tmuxenv identifies the tmux server this process talks to.
//
// Sidecar never passes -L or -S to tmux, and cmd/sidecar/main.go unsets TMUX
// before anything runs, so every tmux child resolves the *default* socket for
// the current user under TMUX_TMPDIR (falling back to /tmp). That makes
// TMUX_TMPDIR the only lever that separates one Sidecar's tmux namespace from
// another's, and it makes the resolved socket path a faithful identity for
// "the server whose sessions this process can see".
//
// The identity is computed from the environment rather than from a
// `tmux display-message` subprocess on purpose: no startup latency, no
// dependency on tmux being installed, and no flakiness when it is not running.
//
// Values are recomputed on every call (no sync.Once) so tests can move the
// namespace with t.Setenv.
package tmuxenv

import (
	"os"
	"path/filepath"
	"strconv"
)

// SocketPath returns the path of the default tmux socket this process would use.
func SocketPath() string {
	return filepath.Join(tmpDir(), "tmux-"+strconv.Itoa(os.Getuid()), "default")
}

// Namespace returns a stable identity for the tmux server this process talks
// to, in the form "<hostname>:<socket path>". Two Sidecar instances share a
// namespace exactly when they can see each other's sessions.
func Namespace() string {
	return hostname() + ":" + SocketPath()
}

func tmpDir() string {
	if dir := os.Getenv("TMUX_TMPDIR"); dir != "" {
		return dir
	}
	return "/tmp"
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}
