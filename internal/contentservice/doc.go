// Package contentservice is the read-only file resolve/read application
// service a viewing Sidecar invokes on a host.
//
// The same functions power the local Document adapter and the
// `sidecar content` CLI, so containment, revision, and typed refusals cannot
// drift by surface. It is not a generic remote executor: kind and operation
// are strictly enumerated, every verb is non-interactive, and nothing here
// writes, talks to tmux, or runs an arbitrary shell.
package contentservice
