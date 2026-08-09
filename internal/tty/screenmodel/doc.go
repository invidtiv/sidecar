// Package screenmodel owns Sidecar's byte-fed pane screen model.
//
// It is the ONLY package permitted to import github.com/charmbracelet/x/vt.
// Everything it exposes is Sidecar-shaped: canonical [Cell] values, a [Frame]
// that can populate tty.ControlSnapshot, and plain bools/ints for cursor and
// mode state. No vt.Emulator, uv.Cell, or ansi.Mode value ever crosses the
// package boundary, so the dependency can be replaced or deleted without
// touching workspace code.
//
// # Ownership
//
// A [Model] is single-actor: exactly one goroutine may call Seed, Write,
// Resize, Frame, or Close at a time. The type does not serialize for you. It
// detects violations instead — a concurrent entry returns
// [ErrConcurrentUse] rather than corrupting emulator state — so a misuse
// shows up as a loud error in tests instead of a rare rendering bug.
//
// # Status
//
// Slice 0 of docs/plans/active/td-64c916-byte-fed-tmux-screen-model.md. There
// is deliberately no UI consumer: the model exists so the deterministic byte
// corpus can be run against it with tmux as the oracle. Known emulator gaps
// are recorded in the slice 0 evidence document rather than patched here.
package screenmodel
