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

	// WorkspaceListSort is the global Workspaces list's chosen order, stored as
	// its display label ("Activity", "Project", "Recent", "Name") rather than
	// an ordinal, so the file reads plainly and the enum can be reordered.
	// Unrecognised or empty falls back to the default. Its per-project
	// counterpart lives on WorkspaceState, because the two scopes answer the
	// question separately.
	WorkspaceListSort string `json:"workspaceListSort,omitempty"`

	// LastCreateAgent is the last agent chosen when creating a worktree.
	LastCreateAgent string `json:"lastCreateAgent,omitempty"`
	// LastGlobalCreateProject is the stable project root last chosen from the
	// cross-project Workspaces create flow.
	LastGlobalCreateProject string `json:"lastGlobalCreateProject,omitempty"`

	// AgentAutoApprove is the last auto-approve checkbox value per agent type.
	// A missing key is treated as false.
	AgentAutoApprove map[string]bool `json:"agentAutoApprove,omitempty"`

	// SeenDefaultThemeNotice records that the one-time "the default theme
	// changed" toast has been shown.
	//
	// It lives here and not in config.json on purpose. Sidecar only writes
	// config.json when a setting changes, and an absent ui.theme block is
	// exactly the signal that identifies a user who is being restyled. Writing
	// the flag into the config would record a theme choice as a side effect and
	// disarm the very mechanism it is flagging.
	SeenDefaultThemeNotice bool `json:"seenDefaultThemeNotice,omitempty"`
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
	WorkspaceName     string                     `json:"workspaceName,omitempty"`     // Name of selected workspace
	ShellTmuxName     string                     `json:"shellTmuxName,omitempty"`     // TmuxName of selected shell (empty = workspace selected)
	ShellDisplayNames map[string]string          `json:"shellDisplayNames,omitempty"` // TmuxName -> display name
	PaneLayout        *PaneLayoutJSON            `json:"paneLayout,omitempty"`        // Read-only migrate into PaneLayouts
	PaneLayouts       map[string]*PaneLayoutJSON `json:"paneLayouts,omitempty"`       // surface → layout
	// ListSort is the sidebar's chosen order, stored as its display label
	// ("Manual", "Activity", "Recent", "Name") rather than an ordinal. A label
	// survives reordering the enum, reads plainly in the state file, and an
	// unrecognised one falls back to the default instead of selecting an
	// arbitrary mode. Empty means the project has never chosen.
	ListSort string `json:"listSort,omitempty"`
}

// PaneLayoutJSON is the persisted, presentation-neutral pane-tree shape. Doc
// tabs are a list from the first version so adding tab UI later is additive.
// Issue tabs are IssueTabs plus Active. Issue and Scroll are read-only legacy
// and are not written after the first save.
type PaneLayoutJSON struct {
	Root      string             `json:"root,omitempty"`
	Surface   string             `json:"surface,omitempty"`
	Kind      string             `json:"kind,omitempty"`
	Split     *PaneSplitJSON     `json:"split,omitempty"`
	Tabs      []PaneDocTabJSON   `json:"tabs,omitempty"`
	IssueTabs []PaneIssueTabJSON `json:"issueTabs,omitempty"`
	DiffTabs  []PaneDiffTabJSON  `json:"diffTabs,omitempty"`
	Active    int                `json:"active,omitempty"`
	// Open is true when restore should rebuild the split. False means this
	// surface still has tabs but the pane is hidden (q). Omitted on a legacy
	// record that still has a split is treated as open by MigratePaneLayouts.
	Open bool `json:"open,omitempty"`
	// Issue and Scroll are the pre-tab issue leaf. Decode treats them as a
	// one-tab list when IssueTabs is absent.
	Issue  string `json:"issue,omitempty"`
	Scroll int    `json:"scroll,omitempty"`
}

// PaneIssueTabJSON is one persisted issue tab. Restore re-fetches the issue
// and applies Scroll; the body is not cached.
type PaneIssueTabJSON struct {
	Issue  string `json:"issue"`
	Scroll int    `json:"scroll,omitempty"`
}

// PaneDiffTabJSON is one persisted Diff tab. Restore reloads the target
// spec; the diff body is not cached.
type PaneDiffTabJSON struct {
	Spec   string `json:"spec"`
	Path   string `json:"path,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Scroll int    `json:"scroll,omitempty"`
}

// MigratePaneLayouts copies a legacy single-slot PaneLayout into PaneLayouts
// when the map is empty. The legacy field is left for the writer to drop.
func MigratePaneLayouts(s *WorkspaceState) {
	if s == nil || len(s.PaneLayouts) > 0 || s.PaneLayout == nil || s.PaneLayout.Surface == "" {
		return
	}
	// Legacy writes omitted Open. Those records still wanted the split back;
	// hide is a later, explicit Open=false write into the map.
	if s.PaneLayout.Split != nil {
		s.PaneLayout.Open = true
	}
	s.PaneLayouts = map[string]*PaneLayoutJSON{s.PaneLayout.Surface: s.PaneLayout}
}

// PaneLayoutOpen reports whether restore should rebuild the split. Open=true
// restores. Open=false is hide: tabs stay in the map, the live tree does not.
func PaneLayoutOpen(l *PaneLayoutJSON) bool {
	return l != nil && l.Open
}

// PaneLayoutFor returns the layout stored for surface, migrating a legacy
// single-slot record first. The receiver is a copy; the stored state is not written.
func (s WorkspaceState) PaneLayoutFor(surface string) *PaneLayoutJSON {
	MigratePaneLayouts(&s)
	if surface == "" || s.PaneLayouts == nil {
		return nil
	}
	return s.PaneLayouts[surface]
}

// RekeyPaneLayout moves a saved surface to its canonical identity. If both
// identities exist, the canonical record wins and the duplicate legacy key is
// dropped. The returned bool reports whether the state needs writing.
func RekeyPaneLayout(s *WorkspaceState, legacySurface, canonicalSurface string) (*PaneLayoutJSON, bool) {
	if s == nil || canonicalSurface == "" {
		return nil, false
	}
	MigratePaneLayouts(s)
	if s.PaneLayouts == nil {
		return nil, false
	}
	canonical := s.PaneLayouts[canonicalSurface]
	if legacySurface == "" || legacySurface == canonicalSurface {
		return canonical, false
	}
	legacy := s.PaneLayouts[legacySurface]
	if canonical != nil {
		if legacy != nil {
			delete(s.PaneLayouts, legacySurface)
			return canonical, true
		}
		return canonical, false
	}
	if legacy == nil {
		return nil, false
	}
	delete(s.PaneLayouts, legacySurface)
	legacy.Surface = canonicalSurface
	s.PaneLayouts[canonicalSurface] = legacy
	return legacy, true
}

// ForgetPaneLayouts removes only the named surfaces, including a matching
// legacy single-slot record. It reports whether anything changed so callers
// can avoid unrelated state writes while still writing a last-entry removal.
func ForgetPaneLayouts(s *WorkspaceState, surfaces ...string) bool {
	if s == nil || len(surfaces) == 0 {
		return false
	}
	wanted := make(map[string]struct{}, len(surfaces))
	for _, surface := range surfaces {
		if surface != "" {
			wanted[surface] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return false
	}
	MigratePaneLayouts(s)
	changed := false
	for surface := range wanted {
		if _, ok := s.PaneLayouts[surface]; ok {
			delete(s.PaneLayouts, surface)
			changed = true
		}
	}
	if len(s.PaneLayouts) == 0 && s.PaneLayouts != nil {
		s.PaneLayouts = nil
	}
	if s.PaneLayout != nil {
		if _, ok := wanted[s.PaneLayout.Surface]; ok {
			s.PaneLayout = nil
			changed = true
		}
	}
	return changed
}

type PaneSplitJSON struct {
	Axis  string          `json:"axis"`
	Ratio int             `json:"ratio"`
	A     *PaneLayoutJSON `json:"a"`
	B     *PaneLayoutJSON `json:"b"`
}

type PaneDocTabJSON struct {
	Path   string `json:"path"`
	Mode   string `json:"mode,omitempty"`
	Wrap   bool   `json:"wrap,omitempty"`
	Scroll int    `json:"scroll,omitempty"`
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

// GetWorkspaceListSort returns the global Workspaces list's saved order label,
// or "" when the user has never chosen one.
func GetWorkspaceListSort() string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return ""
	}
	return current.WorkspaceListSort
}

// SetWorkspaceListSort saves the global Workspaces list's chosen order.
func SetWorkspaceListSort(label string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.WorkspaceListSort = label
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

// GetSeenDefaultThemeNotice reports whether the one-time new-default-theme
// toast has already been shown. Fresh state has not seen it.
func GetSeenDefaultThemeNotice() bool {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return false
	}
	return current.SeenDefaultThemeNotice
}

// SetSeenDefaultThemeNotice records that the notice has been shown, so it never
// appears again.
func SetSeenDefaultThemeNotice(seen bool) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.SeenDefaultThemeNotice = seen
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

// GetLastCreateAgent returns the last agent chosen when creating a worktree.
func GetLastCreateAgent() string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return ""
	}
	return current.LastCreateAgent
}

// SetLastCreateAgent saves the last agent chosen when creating a worktree.
func SetLastCreateAgent(agent string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.LastCreateAgent = agent
	mu.Unlock()
	return Save()
}

// GetLastGlobalCreateProject returns the last project root chosen in global
// Workspaces creation.
func GetLastGlobalCreateProject() string {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return ""
	}
	return current.LastGlobalCreateProject
}

// SetLastGlobalCreateProject persists the last project root chosen in global
// Workspaces creation.
func SetLastGlobalCreateProject(project string) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	current.LastGlobalCreateProject = project
	mu.Unlock()
	return Save()
}

// GetAgentAutoApprove returns the persisted auto-approve preference for agent.
// A missing key is false.
func GetAgentAutoApprove(agent string) bool {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil || current.AgentAutoApprove == nil {
		return false
	}
	return current.AgentAutoApprove[agent]
}

// SetAgentAutoApprove saves the auto-approve preference for agent.
func SetAgentAutoApprove(agent string, on bool) error {
	mu.Lock()
	if current == nil {
		current = &State{}
	}
	if current.AgentAutoApprove == nil {
		current.AgentAutoApprove = make(map[string]bool)
	}
	current.AgentAutoApprove[agent] = on
	mu.Unlock()
	return Save()
}
