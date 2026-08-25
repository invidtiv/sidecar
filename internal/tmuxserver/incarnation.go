// Package tmuxserver identifies one tmux server process bound to a socket.
//
// A socket path (tmuxenv.Namespace) outlives the servers that bind it. Every
// liveness signal Sidecar has is a statement about one server process, so
// callers need an identity that changes when that process is replaced. This
// package is state-free: no tea.Cmd, no plugin state.
package tmuxserver

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/marcus/sidecar/internal/tmuxenv"
)

// Incarnation is an opaque comparable identity of one tmux server process.
//
// Three states are distinguishable and that is the point of the type:
//
//   - Present: a specific server, identified by socket (inode, ctime) and/or
//     the tmux server #{pid}.
//   - Absent: tmux answered "no server running" — a fact about the server, not
//     about any shell.
//   - Unknown: we could not ask. The zero value is Unknown.
//
// Any transition between distinct values invalidates prior liveness evidence.
// If a tmux version reuses the socket inode, the stat source degrades to
// Unknown rather than claiming the same incarnation: inode without ctime is
// not a safe identity.
type Incarnation struct {
	kind  kind
	inode uint64
	ctime int64 // socket ctime, unix nanoseconds
	pid   int
}

type kind uint8

const (
	kindUnknown kind = iota
	kindAbsent
	kindPresent
)

// Unknown is the safe default: we could not observe a server.
func Unknown() Incarnation { return Incarnation{} }

// Absent is tmux answering that no server is running on this socket.
func Absent() Incarnation { return Incarnation{kind: kindAbsent} }

// Present builds a Present incarnation from the identity sources we have.
//
// inode without a ctime cannot distinguish a reused inode from the same
// socket, so it is dropped. If nothing remains (no ctime-qualified inode and
// no pid), the result is Unknown, not a fake Present.
func Present(inode uint64, ctime int64, pid int) Incarnation {
	if pid < 0 {
		pid = 0
	}
	if inode != 0 && ctime == 0 {
		inode = 0
	}
	if inode == 0 && pid == 0 {
		return Unknown()
	}
	return Incarnation{kind: kindPresent, inode: inode, ctime: ctime, pid: pid}
}

// IsUnknown reports the zero value and any observation that produced no id.
func (i Incarnation) IsUnknown() bool { return i.kind == kindUnknown }

// IsAbsent reports that tmux answered "no server running".
func (i Incarnation) IsAbsent() bool { return i.kind == kindAbsent }

// IsPresent reports a concrete server identity.
func (i Incarnation) IsPresent() bool { return i.kind == kindPresent }

func (i Incarnation) String() string {
	switch i.kind {
	case kindAbsent:
		return "absent"
	case kindPresent:
		return fmt.Sprintf("present inode=%d ctime=%d pid=%d", i.inode, i.ctime, i.pid)
	default:
		return "unknown"
	}
}

// Socket observes tmuxenv.SocketPath with one stat and no subprocess.
// Missing or unreadable sockets, and inodes we cannot qualify with ctime,
// are Unknown — Absent is reserved for tmux's own "no server running".
func Socket() Incarnation {
	return FromPath(tmuxenv.SocketPath())
}

// FromPath stats path and returns Present(inode, ctime, 0) when both are
// available. Any failure is Unknown.
func FromPath(path string) Incarnation {
	info, err := os.Stat(path)
	if err != nil {
		return Unknown()
	}
	return FromFileInfo(info)
}

// FromFileInfo is the table-testable form of the socket-stat source.
func FromFileInfo(info os.FileInfo) Incarnation {
	if info == nil {
		return Unknown()
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Unknown()
	}
	inode := uint64(st.Ino)
	ctime := ctimeNano(st)
	return Present(inode, ctime, 0)
}

// Combine merges a socket-stat observation with a #{pid} parsed from the
// same list-sessions invocation. Absent wins: a pid is meaningless if tmux
// already said the server is gone. Otherwise a pid fills in a Present or
// promotes Unknown to Present.
func Combine(socket Incarnation, pid int) Incarnation {
	if socket.IsAbsent() {
		return Absent()
	}
	if pid <= 0 {
		return socket
	}
	if socket.IsPresent() {
		socket.pid = pid
		return socket
	}
	return Present(0, 0, pid)
}

// ParsePID reads a #{pid} format field. The empty string, the unresolved
// literal "#{pid}", and non-positive values are not a pid.
func ParsePID(field string) (int, bool) {
	field = strings.TrimSpace(field)
	if field == "" || strings.Contains(field, "#{") {
		return 0, false
	}
	n, err := strconv.Atoi(field)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ListSessionsFormat is the list-sessions -F value that yields the session
// name and the server pid in one invocation. #{pid} is server-scoped and
// was verified to expand in list-sessions on an isolated socket (td-e27291).
// Do not use #{session_id}: those restart from $0 on a new server.
const ListSessionsFormat = "#{session_name}\t#{pid}"
