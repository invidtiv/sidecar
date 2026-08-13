// Package terminallink detects URL, file, and issue spans in terminal text.
//
// Detection lives here so the project Workspaces plugin and the global
// Workspaces preview share one matcher. Hosts activate the kinds they
// understand and ignore the rest. Adding a kind therefore does not require
// editing two hosts.
//
// KindIssue is emitted for td-<hex> ids so a later terminal | file + td split
// can bind a live td pane. It must not open internal/app/issue_preview.go.
package terminallink
