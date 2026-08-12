package workspace

import (
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// RefreshMsg triggers a worktree list refresh.
type RefreshMsg struct{}

// RefreshDoneMsg signals that refresh has completed.
type RefreshDoneMsg struct {
	OperationScope
	Worktrees      []*Worktree
	Snapshot       *RepoSnapshot
	Err            error
	Conflicts      []Conflict
	Duration       time.Duration
	Processes      int
	MaxConcurrency int
}

// GetEpoch implements plugin.EpochMessage.
func (m RefreshDoneMsg) GetEpoch() uint64 { return m.Epoch }

// WatchEventMsg signals a filesystem change was detected.
type WatchEventMsg struct {
	Path string
}

// WatcherStartedMsg signals the file watcher is running.
type WatcherStartedMsg struct{}

// WatcherErrorMsg signals a file watcher error.
type WatcherErrorMsg struct {
	Err error
}

// AgentOutputMsg delivers new agent output.
type AgentOutputMsg struct {
	WorkspaceName string
	Generation    int
	AgentType     AgentType // Live provider inferred from the captured pane
	Output        string
	Status        WorktreeStatus
	WaitingFor    string
	// Cursor position captured atomically with output (only set in interactive mode)
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	HasCursor     bool // True if cursor position was captured
	PaneHeight    int  // Tmux pane height for cursor offset calculation
	PaneWidth     int  // Tmux pane width for display alignment
	HistorySize   int
	CaptureBase   int
	HasHistory    bool
	// RowsJoined says the capture was taken with -J, so it carries no usable
	// history/pane split.
	RowsJoined bool
	// MouseReporting is tmux's #{mouse_any_flag} for the pane, captured with the
	// cursor metadata. Only meaningful when HasCursor is set.
	MouseReporting bool
	Activity       agentactivity.Result
	CapturedAt     time.Time
	PaneTitle      string
	CurrentCommand string
}

// AgentStoppedMsg signals an agent has stopped.
type AgentStoppedMsg struct {
	WorkspaceName string
	Generation    int
	Err           error
}

// TmuxAttachFinishedMsg signals return from tmux attach.
type TmuxAttachFinishedMsg struct {
	WorkspaceName string
	Err           error
}

// DiffLoadedMsg delivers diff content for a worktree.
type DiffLoadedMsg struct {
	OperationScope
	WorkspaceName string
	Content       string
	Raw           string
	Snapshot      *DiffSnapshot
}

// GetEpoch implements plugin.EpochMessage.
func (m DiffLoadedMsg) GetEpoch() uint64 { return m.Epoch }

// DiffErrorMsg signals diff loading failed.
type DiffErrorMsg struct {
	OperationScope
	WorkspaceName string
	Err           error
	Command       string
	BaseRef       string
}

func (m DiffErrorMsg) GetEpoch() uint64 { return m.Epoch }

// StatsLoadedMsg delivers git stats for a worktree.
type StatsLoadedMsg struct {
	OperationScope
	WorkspaceName string
	Stats         *GitStats
}

// GetEpoch implements plugin.EpochMessage.
func (m StatsLoadedMsg) GetEpoch() uint64 { return m.Epoch }

// StatsErrorMsg signals stats loading failed.
type StatsErrorMsg struct {
	OperationScope
	WorkspaceName string
	Err           error
	Command       string
}

func (m StatsErrorMsg) GetEpoch() uint64 { return m.Epoch }

// CreateWorktreeMsg requests worktree creation.
type CreateWorktreeMsg struct {
	Name       string
	BaseBranch string
	TaskID     string
}

// CreateDoneMsg signals worktree creation completed.
type CreateDoneMsg struct {
	OperationScope
	Worktree  *Worktree
	AgentType AgentType // Agent selected at creation
	SkipPerms bool      // Whether to skip permissions
	Prompt    *Prompt   // Selected prompt template (nil if none)
	Err       error
}

// CreatePlanResolvedMsg completes the non-mutating Git-plumbing preflight.
type CreatePlanResolvedMsg struct {
	OperationScope
	Plan *CreateOperationPlan
	Err  error
}

// CreateWorktreeAddedMsg means Git created the worktree. Setup is a separate,
// recoverable phase and may still produce warnings or a required failure.
type CreateWorktreeAddedMsg struct {
	OperationScope
	Plan     *CreateOperationPlan
	Worktree *Worktree
	Err      error
}

type CreateSetupDoneMsg struct {
	OperationScope
	Plan   *CreateOperationPlan
	Result *CreateSetupResult
}

type CreateRecoveryDeleteDoneMsg struct {
	OperationScope
	Result CreateRecoveryDeleteResult
}

type CreateRecoveryDeleteResult struct {
	WorktreeRemoved bool
	BranchDeleted   bool
	BranchRetained  bool
	Err             error
}

type CreateOpenAnywayMsg struct{ OperationScope }

// DeleteWorktreeMsg requests worktree deletion.
type DeleteWorktreeMsg struct {
	Name  string
	Force bool
}

// DeleteDoneMsg signals worktree deletion completed.
type DeleteDoneMsg struct {
	OperationScope
	Name     string
	Err      error
	Warnings []string // Non-fatal warnings (e.g., branch deletion failures)
}

// RemoteCheckDoneMsg signals remote branch existence check completed.
type RemoteCheckDoneMsg struct {
	OperationScope
	WorkspaceName string
	Branch        string
	Exists        bool
}

// PushMsg requests pushing a worktree branch.
type PushMsg struct {
	WorkspaceName string
	Force         bool
	SetUpstream   bool
}

// PushDoneMsg signals push operation completed.
type PushDoneMsg struct {
	OperationScope
	WorkspaceName string
	Err           error
}

// TaskSearchResultsMsg delivers task search results.
type TaskSearchResultsMsg struct {
	OperationScope
	Tasks []Task
	Err   error
}

// BranchListMsg delivers available branches.
type BranchListMsg struct {
	OperationScope
	Branches []string
	Err      error
}

// TaskLinkedMsg signals a task was linked to a worktree.
type TaskLinkedMsg struct {
	OperationScope
	WorkspaceName string
	TaskID        string
	Err           error
}

// Task represents a TD task for linking.
type Task struct {
	ID          string
	Title       string
	Status      string
	Description string
	EpicTitle   string // Parent epic title for search
}

// TaskDetails contains full task information for preview pane.
type TaskDetails struct {
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

// TaskDetailsLoadedMsg delivers task details for the preview pane.
type TaskDetailsLoadedMsg struct {
	OperationScope
	TaskID  string
	Details *TaskDetails
	Err     error
}

// restartAgentMsg signals that an agent should be restarted after stopping.
type restartAgentMsg struct {
	worktree *Worktree
}

// restartAgentWithOptionsMsg signals that an agent should be restarted with specific options.
type restartAgentWithOptionsMsg struct {
	worktree  *Worktree
	agentType AgentType
	skipPerms bool
	prompt    *Prompt
}

// CommitStatusLoadedMsg delivers commit status info for the diff view header.
type CommitStatusLoadedMsg struct {
	Epoch         uint64 // Epoch when request was issued (for stale detection)
	WorkspaceName string
	Commits       []CommitStatusInfo
	Err           error
}

// GetEpoch implements plugin.EpochMessage.
func (m CommitStatusLoadedMsg) GetEpoch() uint64 { return m.Epoch }

// OpenCreateModalWithTaskMsg opens create modal pre-filled with task data.
// Sent from td-monitor plugin when user presses send-to-worktree hotkey.
type OpenCreateModalWithTaskMsg struct {
	TaskID    string
	TaskTitle string
}

// ResumeConversationMsg requests resuming a conversation in a new shell or worktree.
// Sent from conversations plugin when user presses O key.
type ResumeConversationMsg struct {
	SessionID string // Adapter session ID for resume command
	AdapterID string // Adapter type (claude-code, codex, etc.)
	ResumeCmd string // Full resume command (e.g., "claude --resume xyz")
	Type      string // "shell" or "worktree"
	// Worktree-specific fields (only used when Type == "worktree")
	WorktreeName string    // Branch name for new worktree
	BaseBranch   string    // Base branch to create from
	AgentType    AgentType // Agent to start (matches adapter or user selection)
	SkipPerms    bool      // Whether to auto-approve agent actions
}

// cursorPositionMsg delivers async cursor position updates for interactive mode (td-648af4).
// Queried during poll handler when output changes, not during View() rendering.
type cursorPositionMsg struct {
	Row     int  // 0-indexed row in visible pane
	Col     int  // 0-indexed column
	Visible bool // Whether cursor should be rendered
}

// paneResizedMsg signals that a tmux pane was resized to match preview dimensions.
// Triggers a fresh poll so captured content reflects the new width/wrapping.
type paneResizedMsg struct{}

// FetchPRListMsg delivers the list of open PRs from gh CLI.
type FetchPRListMsg struct {
	OperationScope
	PRs []PRListItem
	Err error
}

// FetchPRDoneMsg signals that a PR branch was fetched and worktree created.
type FetchPRDoneMsg struct {
	OperationScope
	Worktree     *Worktree
	AlreadyLocal bool   // branch already existed locally
	Branch       string // for finding existing worktree when Worktree is nil
	Err          error
}

// PRListItem represents an open pull request for the fetch modal.
type PRListItem struct {
	Number     int          `json:"number"`
	NodeID     string       `json:"id"`
	Title      string       `json:"title"`
	Branch     string       `json:"headRefName"`
	HeadOID    string       `json:"headRefOid"`
	BaseBranch string       `json:"baseRefName"`
	HeadRepo   ghRepository `json:"headRepository"`
	HeadOwner  ghOwner      `json:"headRepositoryOwner"`
	Repository string       `json:"-"`
	Author     prAuthor     `json:"author"`
	URL        string       `json:"url"`
	CreatedAt  string       `json:"createdAt"`
	IsDraft    bool         `json:"isDraft"`
}

func (p PRListItem) identity() PRIdentity {
	return PRIdentity{Number: p.Number, URL: p.URL, NodeID: p.NodeID, Repository: p.Repository,
		HeadRef: p.Branch, HeadOwner: p.HeadOwner.Login, HeadRepo: p.HeadRepo.NameWithOwner,
		HeadOID: p.HeadOID, BaseRef: p.BaseBranch, State: "OPEN"}
}

// prAuthor represents the author field from gh pr list --json.
type prAuthor struct {
	Login string `json:"login"`
}

// InteractivePasteResultMsg reports clipboard paste results for interactive mode.
type InteractivePasteResultMsg struct {
	Err         error
	Empty       bool
	SessionDead bool
}
