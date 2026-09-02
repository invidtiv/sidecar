// Package reposervice is the host-side repository read service behind
// `sidecar repo`.
//
// It exists because a viewing Sidecar needs one machine's repository state in
// one round trip, normalized to the model its Git pane already renders. It is
// not a git wrapper and must never become one: Sidecar does not own git, and an
// agent that wants to stage a file runs `git add`.
//
// It sits beside contentservice under the same rules — non-interactive,
// read-only, workspace-scoped, strictly enumerated, JSON-capped — and borrows
// that package's workspace identity, containment, error classification, and
// encoded-size cap rather than growing a second set. Every git invocation is a
// read and carries --no-optional-locks, so a viewer's read can never take
// .git/index.lock out from under a human working on the host.
package reposervice
