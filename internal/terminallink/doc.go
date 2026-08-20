// Package terminallink is the compatibility facade for terminal callers of
// the presentation-neutral contentlink recognition core.
//
// New rendered surfaces should import internal/contentlink directly. Existing
// Workspaces callers retain this package while they migrate in small slices.
//
// KindIssue is emitted for td-<hex> ids so a later terminal | file + td split
// can bind a live td pane. It must not open internal/app/issue_preview.go.
// KindDiff is emitted for existence-gated lowercase hex revs, "commit <rev>",
// and A..B / A...B ranges. HEAD and branch names are not scanned.
package terminallink
