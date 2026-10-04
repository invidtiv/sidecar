// Package workspacewire defines the JSON resources shared by CLI and UI API.
package workspacewire

import (
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspaceops"
)

type Project struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Key    string `json:"key"`
	Theme  string `json:"theme,omitempty"`
	OpenIn string `json:"openIn,omitempty"`
	// AddedAt is when the project was registered with Sidecar, absent for a
	// project registered before Sidecar recorded it. It is a registration date,
	// not a creation date, and it is reported rather than computed: an agent
	// reading this gets the same fact the switcher's "Date added" column shows.
	AddedAt string `json:"addedAt,omitempty"`
}
type Projects struct {
	Projects []Project `json:"projects"`
	Shell    *Project  `json:"shell,omitempty"`
	Visible  *Project  `json:"visible,omitempty"`
	Aligned  bool      `json:"aligned"`
}
type ShellInfo struct {
	DisplayName string `json:"displayName"`
	Session     string `json:"session"`
	WorkDir     string `json:"workDir"`
}
type ShellCreated struct {
	Shell ShellInfo `json:"shell"`
	// Project is the registered project slug the shell belongs to: the value
	// `--project` on every other verb accepts, so a caller holding this result
	// can address what it created without guessing the selector.
	Project   string `json:"project,omitempty"`
	Acked     bool   `json:"acked"`
	Surface   string `json:"surface,omitempty"`
	Placement string `json:"placement"`
}
type SetupOutcome struct {
	Kind     string `json:"kind"`
	Action   string `json:"action"`
	Required bool   `json:"required"`
	Error    string `json:"error,omitempty"`
}
type WorktreeCreated struct {
	Shell ShellInfo `json:"shell"`
	// Project is the registered project slug the worktree was created under.
	// It is the selector the agent verbs' --project accepts, put in the result
	// because none of path, branch, or displayName was one before --project
	// learned to resolve a worktree to its project (td-c906c1).
	Project   string         `json:"project,omitempty"`
	Path      string         `json:"path"`
	Branch    string         `json:"branch"`
	Setup     []SetupOutcome `json:"setup"`
	Acked     bool           `json:"acked"`
	Surface   string         `json:"surface,omitempty"`
	Placement string         `json:"placement"`
}
type ShellDeleted struct {
	Shell   string `json:"shell"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status"`
	Deleted bool   `json:"deleted"`
}
type ShellList struct {
	Shells []ShellRecord `json:"shells"`
}
type ShellRecord struct {
	Shell     string     `json:"shell"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace,omitempty"`
	AgentType string     `json:"agentType,omitempty"`
	SkipPerms bool       `json:"skipPerms,omitempty"`
	WorkDir   string     `json:"workDir,omitempty"`
	Status    string     `json:"status"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
	// OrphanedRoot is the removed worktree a live record's WorkDir lies in,
	// when that worktree was removed outside Sidecar (td-0b90da). `sidecar
	// worktree prune-sessions` closes such a shell along with the worktree's
	// own session.
	OrphanedRoot string `json:"orphanedRoot,omitempty"`
}
type ShellRestored struct {
	Shell  string `json:"shell"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
}

// Workspace retains the shared catalog ordering, grouping and agent vocabulary.
type Workspace struct {
	Project Project                     `json:"project"`
	Catalog mobileproto.CatalogSnapshot `json:"catalog"`
	Shells  []ShellRecord               `json:"shells"`
}
type WorkspaceRef struct {
	Project string `json:"project"`
	Host    string `json:"host,omitempty"`
}

// WorkspaceEvent invalidates resources; consumers refetch only the ones open.
type WorkspaceEvent struct {
	Projects   Projects       `json:"projects"`
	Workspaces []WorkspaceRef `json:"workspaces"`
}

func (r ShellDeleted) ValidRemoteResult() bool { return r.Shell != "" && r.Status != "" }

type WorktreeDeletePlan struct {
	Project              string                     `json:"project"`
	Name                 string                     `json:"name"`
	Path                 string                     `json:"path"`
	Branch               string                     `json:"branch"`
	HeadOID              string                     `json:"headOid"`
	BranchOID            string                     `json:"branchOid"`
	DeleteState          string                     `json:"deleteState"`
	Dirtiness            string                     `json:"dirtiness"`
	HasRemoteBranch      bool                       `json:"hasRemoteBranch"`
	DeleteLocalBranch    bool                       `json:"deleteLocalBranch"`
	DeleteRemoteBranch   bool                       `json:"deleteRemoteBranch"`
	PendingCreation      bool                       `json:"pendingCreation"`
	PendingCreationPlan  *workspaceops.WorktreePlan `json:"-"`
	ExpectedDeleteState  string                     `json:"-"`
	ResolvedWorktreePath string                     `json:"-"`
}
type WorktreeDeleted struct {
	Status   string             `json:"status"`
	Deleted  bool               `json:"deleted"`
	Plan     WorktreeDeletePlan `json:"plan"`
	Warnings []string           `json:"warnings,omitempty"`
}

// Remote decoding must reject unrelated JSON emitted by login profiles.
func (r Projects) ValidRemoteResult() bool { return r.Projects != nil }
func (r Workspace) ValidRemoteResult() bool {
	return r.Project.Key != "" && r.Catalog.HubID != "" && r.Shells != nil
}
