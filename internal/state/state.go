package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// State holds persistent user preferences.
type State struct {
	GitDiffMode       string `json:"gitDiffMode"`                 // "unified" or "side-by-side"
	WorkspaceDiffMode string `json:"workspaceDiffMode,omitempty"` // "unified" or "side-by-side"
	GitGraphEnabled   bool   `json:"gitGraphEnabled,omitempty"`   // Show commit graph in sidebar
	LineWrapEnabled   bool   `json:"lineWrapEnabled,omitempty"`   // Wrap long lines instead of truncating

	// Pane width preferences (percentage of total width, 0 = use default)
	FileBrowserTreeWidth   int    `json:"fileBrowserTreeWidth,omitempty"`
	GitStatusSidebarWidth  int    `json:"gitStatusSidebarWidth,omitempty"`
	ConversationsSideWidth int    `json:"conversationsSideWidth,omitempty"`
	WorkspaceSidebarWidth  int    `json:"workspaceSidebarWidth,omitempty"`
	DiffTabFileListWidth   int    `json:"diffTabFileListWidth,omitempty"`
	TermPanelSize          int    `json:"termPanelSize,omitempty"`    // Terminal panel split size (percentage, 0 = 50%)
	TermPanelLayout        string `json:"termPanelLayout,omitempty"`  // "bottom" or "right"
	TermPanelVisible       bool   `json:"termPanelVisible,omitempty"` // Whether terminal panel was visible at exit

	// Plugin-specific state (keyed by working directory path)
	FileBrowser  map[string]FileBrowserState `json:"fileBrowser,omitempty"`
	Workspace    map[string]WorkspaceState   `json:"workspace,omitempty"`
	Notes        map[string]NotesState       `json:"notes,omitempty"`
	ActivePlugin map[string]string           `json:"activePlugin,omitempty"`

	// Worktree state: maps main repo path -> last active worktree path
	LastWorktreePath map[string]string `json:"lastWorktreePath,omitempty"`

	// Last selected global tab ("agents", "workspaces", or "tasks").
	LastGlobalTab string `json:"lastGlobalTab,omitempty"`

	// ShowIdleWorktrees reveals "no session" rows on the global Workspaces list.
	// Fresh state leaves this off so the list is sessions by default.
	ShowIdleWorktrees bool `json:"showIdleWorktrees,omitempty"`

	// PinnedWorkspaceIDs is the ordered catalog IDs pinned to the top of the
	// global Workspaces list. First-pinned first. Gone IDs are dropped on sync.
	PinnedWorkspaceIDs []string `json:"pinnedWorkspaceIDs,omitempty"`
}

// FileBrowserTabState holds persistent tab state for the file browser.
type FileBrowserTabState struct {
	Path   string `json:"path,omitempty"`   // File path (relative)
	Scroll int    `json:"scroll,omitempty"` // Preview scroll offset
}

// FileBrowserState holds persistent file browser state.
type FileBrowserState struct {
	SelectedFile  string                `json:"selectedFile,omitempty"`  // Currently selected file path (relative)
	TreeScroll    int                   `json:"treeScroll,omitempty"`    // Tree pane scroll offset
	PreviewScroll int                   `json:"previewScroll,omitempty"` // Preview pane scroll offset
	ExpandedDirs  []string              `json:"expandedDirs,omitempty"`  // List of expanded directory paths
	ActivePane    string                `json:"activePane,omitempty"`    // "tree" or "preview"
	PreviewFile   string                `json:"previewFile,omitempty"`   // File being previewed (relative)
	TreeCursor    int                   `json:"treeCursor,omitempty"`    // Tree cursor position
	ShowIgnored   *bool                 `json:"showIgnored,omitempty"`   // Whether to show git-ignored files (nil = default true)
	Tabs          []FileBrowserTabState `json:"tabs,omitempty"`
	ActiveTab     int                   `json:"activeTab,omitempty"`
}

// WorkspaceState holds persistent workspace plugin state.
type WorkspaceState struct {
	WorkspaceName     string            `json:"workspaceName,omitempty"`     // Name of selected workspace
	ShellTmuxName     string            `json:"shellTmuxName,omitempty"`     // TmuxName of selected shell (empty = workspace selected)
	ShellDisplayNames map[string]string `json:"shellDisplayNames,omitempty"` // TmuxName -> display name
	PaneLayout        *PaneLayoutJSON   `json:"paneLayout,omitempty"`        // Structural document-pane layout for the selected terminal root
}

// PaneLayoutJSON is the persisted, presentation-neutral pane-tree shape. Doc
// tabs are a list from the first version so adding tab UI later is additive.
type PaneLayoutJSON struct {
	Root    string           `json:"root,omitempty"`
	Surface string           `json:"surface,omitempty"`
	Kind    string           `json:"kind,omitempty"`
	Split   *PaneSplitJSON   `json:"split,omitempty"`
	Tabs    []PaneDocTabJSON `json:"tabs,omitempty"`
	Active  int              `json:"active,omitempty"`
	// Issue is an issue leaf's durable target: the td ID a restore re-fetches.
	Issue string `json:"issue,omitempty"`
}

type PaneSplitJSON struct {
	Axis  string          `json:"axis"`
	Ratio int             `json:"ratio"`
	A     *PaneLayoutJSON `json:"a"`
	B     *PaneLayoutJSON `json:"b"`
}

type PaneDocTabJSON struct {
	Path string `json:"path"`
	Mode string `json:"mode,omitempty"`
}

// NotesState holds persistent notes plugin state.
type NotesState struct {
	ListWidth    int    `json:"listWidth,omitempty"`    // Width of list pane
	LastNoteID   string `json:"lastNoteID,omitempty"`   // Last selected note ID
	ShowArchived bool   `json:"showArchived,omitempty"` // Whether to show archived notes
}

var (
	current *State
	mu      sync.RWMutex
	path    string
)

// Init loads state from the default location.
func Init() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return InitWithDir(filepath.Join(home, ".config", "sidecar"))
}

// InitWithDir loads state from a specified directory.
// This is primarily for testing to avoid reading real user state.
func InitWithDir(dir string) error {
	path = filepath.Join(dir, "state.json")
	return Load()
}

// Load reads state from disk.
func Load() error {
	mu.Lock()
	defer mu.Unlock()

	current = &State{
		GitDiffMode: "unified", // default
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // no state file yet, use defaults
	}
	if err != nil {
		return err
	}

	return json.Unmarshal(data, current)
}

// Save writes state to disk.
func Save() error {
	mu.RLock()
	defer mu.RUnlock()

	if current == nil {
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// GetGitDiffMode returns the saved diff mode.
func GetGitDiffMode() string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return "unified"
	}
	return current.GitDiffMode
}

// SetGitDiffMode saves the diff mode preference.
func SetGitDiffMode(mode string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.GitDiffMode = mode
	mu.Unlock()
	return Save()
}

// GetWorkspaceDiffMode returns the saved workspace diff mode.
func GetWorkspaceDiffMode() string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || current.WorkspaceDiffMode == "" {
		return "unified"
	}
	return current.WorkspaceDiffMode
}

// SetWorkspaceDiffMode saves the workspace diff mode preference.
func SetWorkspaceDiffMode(mode string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.WorkspaceDiffMode = mode
	mu.Unlock()
	return Save()
}

// GetGitGraphEnabled returns whether the commit graph is enabled.
func GetGitGraphEnabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return false
	}
	return current.GitGraphEnabled
}

// SetGitGraphEnabled saves the commit graph preference.
func SetGitGraphEnabled(enabled bool) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.GitGraphEnabled = enabled
	mu.Unlock()
	return Save()
}

// GetLineWrapEnabled returns whether line wrapping is enabled.
func GetLineWrapEnabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return false
	}
	return current.LineWrapEnabled
}

// SetLineWrapEnabled saves the line wrap preference.
func SetLineWrapEnabled(enabled bool) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.LineWrapEnabled = enabled
	mu.Unlock()
	return Save()
}

// GetFileBrowserTreeWidth returns the saved file browser tree pane width.
// Returns 0 if no preference is saved (use default).
func GetFileBrowserTreeWidth() int {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return 0
	}
	return current.FileBrowserTreeWidth
}

// SetFileBrowserTreeWidth saves the file browser tree pane width.
func SetFileBrowserTreeWidth(width int) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.FileBrowserTreeWidth = width
	mu.Unlock()
	return Save()
}

// GetGitStatusSidebarWidth returns the saved git status sidebar width.
// Returns 0 if no preference is saved (use default).
func GetGitStatusSidebarWidth() int {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return 0
	}
	return current.GitStatusSidebarWidth
}

// SetGitStatusSidebarWidth saves the git status sidebar width.
func SetGitStatusSidebarWidth(width int) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.GitStatusSidebarWidth = width
	mu.Unlock()
	return Save()
}

// GetConversationsSideWidth returns the saved conversations sidebar width.
// Returns 0 if no preference is saved (use default).
func GetConversationsSideWidth() int {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return 0
	}
	return current.ConversationsSideWidth
}

// SetConversationsSideWidth saves the conversations sidebar width.
func SetConversationsSideWidth(width int) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.ConversationsSideWidth = width
	mu.Unlock()
	return Save()
}

// GetWorkspaceSidebarWidth returns the saved workspace sidebar width.
// Returns 0 if no preference is saved (use default).
func GetWorkspaceSidebarWidth() int {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return 0
	}
	return current.WorkspaceSidebarWidth
}

// SetWorkspaceSidebarWidth saves the workspace sidebar width.
func SetWorkspaceSidebarWidth(width int) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.WorkspaceSidebarWidth = width
	mu.Unlock()
	return Save()
}

// GetDiffTabFileListWidth returns the saved diff tab file list width (in pixels).
// Returns 0 if no preference is saved (use default).
func GetDiffTabFileListWidth() int {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return 0
	}
	return current.DiffTabFileListWidth
}

// SetDiffTabFileListWidth saves the diff tab file list width (in pixels).
func SetDiffTabFileListWidth(width int) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.DiffTabFileListWidth = width
	mu.Unlock()
	return Save()
}

// GetTermPanelSize returns the saved terminal panel split size (percentage).
func GetTermPanelSize() int {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return 0
	}
	return current.TermPanelSize
}

// SetTermPanelSize saves the terminal panel split size (percentage).
func SetTermPanelSize(size int) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.TermPanelSize = size
	mu.Unlock()
	return Save()
}

// GetTermPanelLayout returns the saved terminal panel layout ("bottom" or "right").
func GetTermPanelLayout() string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return ""
	}
	return current.TermPanelLayout
}

// SetTermPanelLayout saves the terminal panel layout ("bottom" or "right").
func SetTermPanelLayout(layout string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.TermPanelLayout = layout
	mu.Unlock()
	return Save()
}

// GetTermPanelVisible returns whether the terminal panel was visible at last exit.
func GetTermPanelVisible() bool {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return false
	}
	return current.TermPanelVisible
}

// SetTermPanelVisible saves the terminal panel visibility state.
func SetTermPanelVisible(visible bool) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.TermPanelVisible = visible
	mu.Unlock()
	return Save()
}

// GetFileBrowserState returns the saved file browser state for a given working directory.
func GetFileBrowserState(workdir string) FileBrowserState {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || current.FileBrowser == nil {
		return FileBrowserState{}
	}
	return current.FileBrowser[workdir]
}

// GetFileBrowserStateForWorkDir returns content state keyed by the concrete
// worktree. Older Sidecar versions keyed this state by the repository root; on
// first access, copy that legacy value forward only when the worktree has no
// value of its own. The root entry is deliberately retained for rollback.
func GetFileBrowserStateForWorkDir(workdir, projectRoot string) FileBrowserState {
	mu.Lock()
	if current == nil || current.FileBrowser == nil {
		mu.Unlock()
		return FileBrowserState{}
	}
	if value, ok := current.FileBrowser[workdir]; ok {
		mu.Unlock()
		return value
	}
	legacy, ok := current.FileBrowser[projectRoot]
	if !ok || workdir == projectRoot {
		mu.Unlock()
		return FileBrowserState{}
	}
	current.FileBrowser[workdir] = legacy
	mu.Unlock()
	_ = Save()
	return legacy
}

// SetFileBrowserState saves the file browser state for a given working directory.
func SetFileBrowserState(workdir string, fbState FileBrowserState) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	if current.FileBrowser == nil {
		current.FileBrowser = make(map[string]FileBrowserState)
	}
	current.FileBrowser[workdir] = fbState
	mu.Unlock()
	return Save()
}

// GetWorkspaceState returns the saved workspace state for a given working directory.
func GetWorkspaceState(workdir string) WorkspaceState {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || current.Workspace == nil {
		return WorkspaceState{}
	}
	return current.Workspace[workdir]
}

// SetWorkspaceState saves the workspace state for a given working directory.
func SetWorkspaceState(workdir string, wtState WorkspaceState) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	if current.Workspace == nil {
		current.Workspace = make(map[string]WorkspaceState)
	}
	current.Workspace[workdir] = wtState
	mu.Unlock()
	return Save()
}

// GetActivePlugin returns the saved active plugin ID for a given working directory.
func GetActivePlugin(workdir string) string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || current.ActivePlugin == nil {
		return ""
	}
	return current.ActivePlugin[workdir]
}

// GetActivePluginForWorkDir performs the additive migration from the former
// repository-root key to a concrete worktree key. It never overwrites an
// existing worktree choice and retains the legacy entry.
func GetActivePluginForWorkDir(workdir, projectRoot string) string {
	mu.Lock()
	if current == nil || current.ActivePlugin == nil {
		mu.Unlock()
		return ""
	}
	if value, ok := current.ActivePlugin[workdir]; ok {
		mu.Unlock()
		return value
	}
	legacy, ok := current.ActivePlugin[projectRoot]
	if !ok || workdir == projectRoot {
		mu.Unlock()
		return ""
	}
	current.ActivePlugin[workdir] = legacy
	mu.Unlock()
	_ = Save()
	return legacy
}

// SetActivePlugin saves the active plugin ID for a given working directory.
func SetActivePlugin(workdir, pluginID string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	if current.ActivePlugin == nil {
		current.ActivePlugin = make(map[string]string)
	}
	current.ActivePlugin[workdir] = pluginID
	mu.Unlock()
	return Save()
}

// GetLastWorktreePath returns the last active worktree path for a main repo.
func GetLastWorktreePath(mainRepoPath string) string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || current.LastWorktreePath == nil {
		return ""
	}
	return current.LastWorktreePath[mainRepoPath]
}

// SetLastWorktreePath saves the last active worktree path for a main repo.
func SetLastWorktreePath(mainRepoPath, worktreePath string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	if current.LastWorktreePath == nil {
		current.LastWorktreePath = make(map[string]string)
	}
	current.LastWorktreePath[mainRepoPath] = worktreePath
	mu.Unlock()
	return Save()
}

// GetLastGlobalTab returns the saved global tab ID, or empty if none is saved.
func GetLastGlobalTab() string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return ""
	}
	return current.LastGlobalTab
}

// SetLastGlobalTab saves the last selected global tab ID.
func SetLastGlobalTab(tab string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.LastGlobalTab = tab
	mu.Unlock()
	return Save()
}

// GetShowIdleWorktrees reports whether the global list should include idle
// worktrees. A missing or fresh state is off.
func GetShowIdleWorktrees() bool {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return false
	}
	return current.ShowIdleWorktrees
}

// SetShowIdleWorktrees saves the global idle-worktree visibility preference.
func SetShowIdleWorktrees(show bool) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.ShowIdleWorktrees = show
	mu.Unlock()
	return Save()
}

// GetPinnedWorkspaceIDs returns the saved global pin order, or nil if none.
func GetPinnedWorkspaceIDs() []string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || len(current.PinnedWorkspaceIDs) == 0 {
		return nil
	}
	return append([]string(nil), current.PinnedWorkspaceIDs...)
}

// SetPinnedWorkspaceIDs saves the global Workspaces pin order.
func SetPinnedWorkspaceIDs(ids []string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.PinnedWorkspaceIDs = uniquePinnedIDs(ids)
	mu.Unlock()
	return Save()
}

func uniquePinnedIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// ClearLastWorktreePath removes the saved worktree path for a main repo.
func ClearLastWorktreePath(mainRepoPath string) error {
	mu.Lock()
	if current == nil || current.LastWorktreePath == nil {
		mu.Unlock()
		return nil
	}
	delete(current.LastWorktreePath, mainRepoPath)
	mu.Unlock()
	return Save()
}

// GetNotesState returns the saved notes state for a given working directory.
func GetNotesState(workdir string) NotesState {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || current.Notes == nil {
		return NotesState{}
	}
	return current.Notes[workdir]
}

// SetNotesState saves the notes state for a given working directory.
func SetNotesState(workdir string, notesState NotesState) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	if current.Notes == nil {
		current.Notes = make(map[string]NotesState)
	}
	current.Notes[workdir] = notesState
	mu.Unlock()
	return Save()
}

// SetNotesListWidth saves just the notes list width for a given working directory.
func SetNotesListWidth(width int) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	if current.Notes == nil {
		current.Notes = make(map[string]NotesState)
	}
	// Use empty workdir as global setting
	notesState := current.Notes[""]
	notesState.ListWidth = width
	current.Notes[""] = notesState
	mu.Unlock()
	return Save()
}
