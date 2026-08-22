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

// HostingPane returns the tmux pane ID this process is running in, from
// TMUX_PANE. Empty when sidecar was launched outside tmux.
//
// main unsets TMUX early so sidecar's own tmux sessions stay independent of
// the outer one, but it deliberately leaves TMUX_PANE alone: which pane hosts
// this process is a fact about the outside world that several components need
// (the pane inventory must never correlate a workspace row to it — a preview
// bound to the hosting pane resizes the window sidecar itself draws in), and
// the environment keeps that answer with no subprocess and no startup cost.
func HostingPane() string {
	return os.Getenv("TMUX_PANE")
}

// Namespace returns a stable identity for the tmux server this process talks
// to: the resolved socket path. Two Sidecar instances share a namespace exactly
// when they can see each other's sessions.
//
// The hostname is deliberately NOT part of the identity. shells.json is
// per-machine state, the socket path already distinguishes every server on that
// machine, and macOS rewrites the host name when networks change ("aerie" ->
// "aerie-2"). Including it would make every existing manifest entry
// unrecognizable — and therefore unprunable — after a DHCP rename.
func Namespace() string {
	return SocketPath()
}

func tmpDir() string {
	if dir := os.Getenv("TMUX_TMPDIR"); dir != "" {
		return dir
	}
	return "/tmp"
}
