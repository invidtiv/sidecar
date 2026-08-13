// Package workspacediff is the shared Diff/Task preview model.
//
// The project Workspaces plugin and the global Workspaces preview both draw
// the same working-tree + commits view. This package owns snapshot load,
// first-commit load (short vs full hash), and that render. Neither surface
// constructs the other.
package workspacediff

import "strings"

// Tab is the Output / Diff / Task preview tab.
type Tab int

const (
	TabOutput Tab = iota
	TabDiff
	TabTask
)

// Scope names the three distinct questions answered by the Diff tab.
type Scope int

const (
	ScopeWorkingTree Scope = iota
	ScopeCommits
	ScopeAggregate
)

// ViewMode is how a per-file diff is drawn.
type ViewMode int

const (
	ViewUnified ViewMode = iota
	ViewSideBySide
	ViewFullFile
)

// Focus is which sub-pane of the Diff tab is active.
type Focus int

const (
	FocusFileList Focus = iota
	FocusDiff
	FocusCommitFiles
	FocusCommitDiff
)

// LoadState is the snapshot / view load lifecycle.
type LoadState int

const (
	LoadStateUnknown LoadState = iota
	LoadStateLoading
	LoadStateClean
	LoadStateReady
	LoadStateTruncated
	LoadStateError
)

// Snapshot is the three explicit diff views resolved from one worktree/base identity.
type Snapshot struct {
	State                 LoadState
	WorkingTree           string
	Commits               []CommitInfo
	AggregateCommitted    string
	AggregateUncommitted  string
	BaseRef               string
	MergeBase             string
	UntrackedShown        int
	UntrackedOmitted      int
	UntrackedBytesOmitted int64
	Truncated             bool
}

// CommitInfo is one row in the Diff tab commit list. Hash is git %h.
type CommitInfo struct {
	Hash    string
	Subject string
	Pushed  bool
	Merged  bool
}

// Task is the linked-task payload shown on the Task tab.
type Task struct {
	ID          string
	Title       string
	Status      string
	Priority    string
	Type        string
	Description string
	Acceptance  string
	CreatedAt   string
	UpdatedAt   string
}

const (
	MaxUntrackedFileSize   = 1 << 20
	MaxUntrackedFiles      = 50
	MaxUntrackedTotalBytes = 4 << 20
)

// CollapseThreshold is the minimum width for the two-pane Diff layout.
const CollapseThreshold = 120

// File is one working-tree path in the Diff tab list.
type File struct {
	Path      string
	Raw       string
	Additions int
	Deletions int
}

// CommitDetail is the file list for the commit under the cursor.
type CommitDetail struct {
	Hash      string
	ShortHash string
	Subject   string
	Files     []CommitFile
}

// CommitFile is one path inside a commit.
type CommitFile struct {
	Path      string
	Status    string
	Additions int
	Deletions int
}

// CommitDetailMatchesListHash reports whether a loaded commit is the list row.
// The list stores git %h; detail stores %H in Hash and %h in ShortHash.
func CommitDetailMatchesListHash(detail *CommitDetail, listHash string) bool {
	if detail == nil || listHash == "" {
		return false
	}
	if detail.Hash == listHash || detail.ShortHash == listHash {
		return true
	}
	return strings.HasPrefix(detail.Hash, listHash)
}
