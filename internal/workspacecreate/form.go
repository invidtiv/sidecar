// Package workspacecreate owns the shared Create Workspace form: presentation
// and in-memory state. It does not talk to git or tmux. Hosts bind it and
// submit through workspaceops.
package workspacecreate

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// State accessors are package vars so tests can stub them without a real state file.
var (
	loadLastCreateAgent  = state.GetLastCreateAgent
	saveLastCreateAgent  = state.SetLastCreateAgent
	loadAgentAutoApprove = state.GetAgentAutoApprove
	saveAgentAutoApprove = state.SetAgentAutoApprove
)

// Kind is the workspace type the form will create.
type Kind int

const (
	KindShell Kind = iota
	KindWorktree
	// KindTerminalSplit creates a live terminal beside the workspace's own
	// terminal. It owns no branch and no agent, so it needs at most a name.
	KindTerminalSplit
)

// lastKind is the row the list was last left on. The modal remembers it across
// opens because the row a user picked once is the row they usually want again.
var lastKind = KindShell

// Stable field IDs shared by both hosts.
const (
	FieldKind    = "create-kind"
	FieldProject = "create-project"
	FieldName    = "create-name"
	FieldBase    = "create-base"
	FieldAgent   = "create-agent"
	FieldSkip    = "create-skip-permissions"
	ActionCreate = "create-submit"
	ActionCancel = "create-cancel"
)

const worktreeNamePlaceholder = "feature name"

// ProjectItem is one row in the global project combo.
type ProjectItem struct {
	Key   string
	Label string
}

// OpenOpts configures a new form. PreferredAgent and DefaultAgent are the
// already-resolved fallbacks from the host (.sidecar-agent / defaultAgentType);
// this package does not load them from disk.
type OpenOpts struct {
	Kind      Kind
	FocusKind bool
	// UseLastKind starts the list on the row it was last left on, when that
	// row is one this host offers.
	UseLastKind bool
	// AllowTerminalSplit offers the Terminal split row. A host without a pane
	// tree to place one in leaves it off.
	AllowTerminalSplit bool
	// TerminalSplitDisabled is why a terminal split cannot be created right
	// now — the on-screen live-terminal cap being the one reason today. When
	// it is set the row and its form render disabled with this one line, and
	// the create paths refuse: the modal states the rule instead of offering a
	// creation that would be refused after the fact.
	TerminalSplitDisabled string
	// TerminalName is the auto-name a terminal split takes when the name field
	// is left empty.
	TerminalName   string
	ShowProject    bool
	ProjectKey     string
	Name           string
	Projects       []ProjectItem
	Agents         []string
	Branches       []string
	NextShell      string
	PreferredAgent string
	DefaultAgent   string
}

// Form is the Create Workspace chooser: inputs, indexes, skip, error, and modal cache.
type Form struct {
	kind             Kind
	rows             []kindRow
	placement        Placement
	terminalName     string
	terminalDisabled string
	showProject      bool
	openedFocus      string

	projects     []ProjectItem
	projectKey   string
	projectIndex int
	projectInput textinput.Model

	nameInput textinput.Model
	nextShell string

	branches  []string
	baseInput textinput.Model
	baseIndex int

	allowlist      []string
	preferredAgent string
	defaultAgent   string
	agentType      string
	agentIndex     int
	agentInput     textinput.Model

	skip       bool
	loadedSkip bool
	lastAgent  string

	err string

	modal        *modal.Modal
	modalWidth   int
	cachedKind   Kind
	cachedBranch int
	pendingFocus string
}

// Open builds form state and loads last-used agent and that agent's auto-approve.
// It does not persist last agent.
func Open(opts OpenOpts) *Form {
	f := &Form{
		kind:             opts.Kind,
		rows:             kindRowsFor(opts.AllowTerminalSplit),
		terminalName:     strings.TrimSpace(opts.TerminalName),
		terminalDisabled: strings.TrimSpace(opts.TerminalSplitDisabled),
		showProject:      opts.ShowProject,
		projects:         append([]ProjectItem(nil), opts.Projects...),
		projectKey:       opts.ProjectKey,
		allowlist:        append([]string(nil), opts.Agents...),
		branches:         append([]string(nil), opts.Branches...),
		nextShell:        opts.NextShell,
		preferredAgent:   opts.PreferredAgent,
		defaultAgent:     opts.DefaultAgent,
	}
	if opts.UseLastKind && kindLabel(f.rows, lastKind) != "" {
		f.kind = lastKind
	}
	if kindLabel(f.rows, f.kind) == "" {
		f.kind = f.rows[0].Kind
	}
	lastKind = f.kind
	f.nameInput = textinput.New()
	f.nameInput.Prompt = ""
	f.nameInput.CharLimit = 100
	if name := strings.TrimSpace(opts.Name); name != "" {
		f.nameInput.SetValue(name)
	}
	f.updateNamePlaceholder()

	f.projectInput = textinput.New()
	f.projectInput.Prompt = ""
	f.projectInput.CharLimit = 80

	f.baseInput = textinput.New()
	f.baseInput.Prompt = ""
	f.baseInput.CharLimit = 100

	f.agentInput = textinput.New()
	f.agentInput.Prompt = ""
	f.agentInput.CharLimit = 80

	f.resolveProjectIndex()
	f.prefillProjectInput()
	f.agentType = f.pickAgent()
	f.rematchAgentIndex()
	f.loadAutoApprove()
	f.lastAgent = f.agentType
	f.prefillAgentInput()

	if opts.FocusKind {
		f.openedFocus = FieldKind
	} else {
		f.openedFocus = FieldName
	}
	f.pendingFocus = f.openedFocus
	return f
}

// Build returns the cached modal, rebuilding when width or kind/branch-list visibility change.
func (f *Form) Build(width int) *modal.Modal {
	if f == nil {
		return nil
	}
	if width < 1 {
		width = 52
	}
	prevFocus := f.pendingFocus
	if f.modal != nil {
		if id := f.modal.FocusedID(); id != "" {
			prevFocus = id
		}
	}
	if f.modal != nil && f.modalWidth == width && f.cachedKind == f.kind && f.cachedBranch == len(f.branches) {
		return f.modal
	}
	if prevFocus == "" {
		prevFocus = f.openedFocus
	}
	f.build(width, prevFocus)
	f.pendingFocus = ""
	return f.modal
}

// RestoreFocus applies the pending focus after the modal has been rendered.
// No-op if focus is already managed by the modal.
func (f *Form) RestoreFocus() {
	if f == nil || f.modal == nil || f.pendingFocus == "" {
		return
	}
	f.modal.SetFocus(f.pendingFocus)
	f.pendingFocus = ""
}

// InitialFocusID is Name, or the kind toggle when Open was given FocusKind.
func (f *Form) InitialFocusID() string {
	if f == nil {
		return ""
	}
	return f.openedFocus
}

// SetBranches replaces the worktree base-branch list. Prefills current as the
// value unless the typed value is still a branch in the new list.
func (f *Form) SetBranches(branches []string, current string) {
	if f == nil {
		return
	}
	f.branches = append([]string(nil), branches...)
	typed := f.baseInput.Value()
	keep := false
	if typed != "" {
		for _, b := range f.branches {
			if b == typed {
				keep = true
				break
			}
		}
	}
	if !keep {
		f.baseInput.SetValue(current)
	}
	f.syncBaseIdx()
	f.invalidate()
}

// AgentItems is this kind's picker (None first for shells, last for worktrees).
func (f *Form) AgentItems() []modal.DropdownItem {
	if f == nil {
		return nil
	}
	ids := f.agentIDs()
	items := make([]modal.DropdownItem, len(ids))
	for i, id := range ids {
		label := agentcatalog.Label(id)
		items[i] = modal.DropdownItem{ID: "agent:" + id, Label: label, Value: label, Data: id}
	}
	return items
}

func (f *Form) Kind() Kind {
	if f == nil {
		return KindShell
	}
	return f.kind
}

func (f *Form) SetKind(k Kind) {
	if f == nil || f.kind == k {
		return
	}
	f.kind = k
	lastKind = k
	f.applyKindChange()
}

// SetKindFromClickX picks the row under a click on the kind toggle.
func (f *Form) SetKindFromClickX(x, regionX, regionW int) {
	if f == nil {
		return
	}
	f.SetKind(kindFromClickX(f.rows, f.kind, x, regionX, regionW))
}

// Placement is the segmented row's current value; Auto until a placement button
// is clicked.
func (f *Form) Placement() Placement {
	if f == nil {
		return PlacementAuto
	}
	return f.placement
}

// SetPlacement records the placement a button click asked for.
func (f *Form) SetPlacement(placement Placement) {
	if f == nil {
		return
	}
	f.placement = placement
	f.invalidate()
}

// PlacementSplit is the `--split` value for the current placement.
func (f *Form) PlacementSplit() string {
	if f == nil {
		return "auto"
	}
	return PlacementSplit(f.placement)
}

// KindDisabledReason is why the selected row cannot be created right now, or
// empty when it can. It is the one gate every create path asks — keyboard,
// click, and placement button alike — so a disabled row cannot be created
// through a path that forgot to check.
func (f *Form) KindDisabledReason() string {
	if f == nil {
		return ""
	}
	return f.kindDisabledReason(f.kind)
}

func (f *Form) kindDisabledReason(kind Kind) string {
	if f == nil || kind != KindTerminalSplit {
		return ""
	}
	return f.terminalDisabled
}

// ApplyPlacementAction records a placement button click and reports that the
// form should now be submitted. One click is the whole gesture.
func (f *Form) ApplyPlacementAction(action string) bool {
	placement, ok := PlacementFromAction(action)
	if !ok || f == nil {
		return false
	}
	if f.KindDisabledReason() != "" {
		return false
	}
	f.placement = placement
	return true
}

// ShowPlacement reports that the placement row belongs on screen: only a pane
// this modal places has somewhere to put it.
func (f *Form) ShowPlacement() bool {
	return f != nil && f.kind == KindTerminalSplit
}

// TerminalName is the name a created terminal split takes: what was typed, or
// the auto-name.
func (f *Form) TerminalName() string {
	if f == nil {
		return ""
	}
	if name := strings.TrimSpace(f.nameInput.Value()); name != "" {
		return name
	}
	return f.terminalName
}

func (f *Form) Name() string {
	if f == nil {
		return ""
	}
	return f.nameInput.Value()
}

func (f *Form) Agent() string {
	if f == nil {
		return ""
	}
	return f.agentType
}

func (f *Form) SkipPerms() bool {
	if f == nil {
		return false
	}
	return f.skip
}

func (f *Form) BaseBranch() string {
	if f == nil {
		return ""
	}
	return f.baseInput.Value()
}

func (f *Form) ProjectKey() string {
	if f == nil {
		return ""
	}
	return f.projectKey
}

func (f *Form) ProjectIndex() int {
	if f == nil {
		return 0
	}
	return f.projectIndex
}

func (f *Form) Error() string {
	if f == nil {
		return ""
	}
	return f.err
}

func (f *Form) SetError(msg string) {
	if f == nil {
		return
	}
	f.err = msg
	if f.modal != nil {
		f.modal.Invalidate()
	}
}

func (f *Form) ShowSkip() bool {
	if f == nil {
		return false
	}
	return workspaceops.AgentSkipFlag(f.selectedAgent()) != ""
}

// Validate reports why Create cannot proceed. Worktree name is required;
// shell name is optional. Empty return means the form is submittable.
func (f *Form) Validate() string {
	if f == nil {
		return ""
	}
	if reason := f.KindDisabledReason(); reason != "" {
		return reason
	}
	if f.kind != KindWorktree {
		return ""
	}
	name := strings.TrimSpace(f.nameInput.Value())
	if name == "" {
		return "Name is required"
	}
	if workspaceops.SlugifyWorktreeName(name) == "" {
		return "Name does not produce a valid git branch"
	}
	return ""
}

// PersistLastAgent writes the current agent after a successful modal create.
func (f *Form) PersistLastAgent() {
	if f == nil {
		return
	}
	f.syncAgentFromIdx()
	_ = saveLastCreateAgent(f.agentType)
}

// SyncAfterInput rematches combo indexes, reloads auto-approve when the agent
// changes, and persists skip immediately on toggle.
func (f *Form) SyncAfterInput() {
	if f == nil {
		return
	}
	f.syncProjectFromIdx()
	prev := f.lastAgent
	f.syncAgentFromIdx()
	if f.agentType != prev {
		f.loadAutoApprove()
		f.lastAgent = f.agentType
		return
	}
	if f.skip != f.loadedSkip {
		f.persistSkip()
	}
}

func (f *Form) Modal() *modal.Modal {
	if f == nil {
		return nil
	}
	return f.modal
}

func (f *Form) build(width int, prevFocus string) {
	if prevFocus != FieldProject {
		f.prefillProjectInput()
	}
	if prevFocus != FieldAgent {
		f.prefillAgentInput()
	}
	if prevFocus != FieldBase {
		f.syncBaseIdx()
	}

	f.modalWidth = width
	f.cachedKind = f.kind
	f.cachedBranch = len(f.branches)

	sections := []modal.Section{
		kindToggle(FieldKind, f.rows, &f.kind, f.applyKindChange, f.kindDisabledReason),
		modal.Spacer(),
	}
	if f.showProject {
		projectItems := f.projectItems()
		sections = append(sections,
			modal.Text("Project"),
			modal.Combo(FieldProject, &f.projectInput, projectItems, &f.projectIndex,
				modal.WithComboFilter(comboExactOrAllFilter(projectItems))),
		)
	}
	sections = append(sections, modal.InputWithLabel(FieldName, "Name", &f.nameInput))
	sections = append(sections, f.slugHintSection())
	if f.kind == KindWorktree {
		branchItems := f.branchItems()
		sections = append(sections,
			modal.Text("Base Branch"),
			modal.Combo(FieldBase, &f.baseInput, branchItems, &f.baseIndex,
				modal.WithComboFilter(comboExactOrAllFilter(branchItems))),
		)
	}
	if f.kind == KindTerminalSplit {
		disabled := f.KindDisabledReason()
		reason := f.errorSection()
		if disabled != "" {
			reason = modal.Text(styles.Muted.Render(disabled))
		}
		sections = append(sections,
			reason,
			modal.Spacer(),
			placementButtons(disabled != ""),
			modal.Spacer(),
			modal.Buttons(
				createButton(disabled != ""),
				modal.Btn(" Cancel ", ActionCancel),
			),
		)
		f.assemble(width, prevFocus, sections)
		return
	}
	agentItems := f.AgentItems()
	sections = append(sections,
		modal.Text("Agent"),
		modal.Combo(FieldAgent, &f.agentInput, agentItems, &f.agentIndex,
			modal.WithComboFilter(comboExactOrAllFilter(agentItems))),
		modal.When(f.ShowSkip, modal.Checkbox(FieldSkip, "Auto-approve all actions", &f.skip)),
		f.skipHintSection(),
		f.errorSection(),
		modal.Spacer(),
		modal.Buttons(
			modal.Btn(" Create ", ActionCreate, modal.BtnPrimary()),
			modal.Btn(" Cancel ", ActionCancel),
		),
	)

	f.assemble(width, prevFocus, sections)
}

func (f *Form) assemble(width int, prevFocus string, sections []modal.Section) {
	// Enter resolves to Create, and Create is refused while the selected kind is
	// disabled — so the hint line must not promise a confirm that is a no-op.
	hints := modal.WithHints(true)
	if f.KindDisabledReason() != "" {
		hints = modal.WithHintText("Tab to switch · Esc to cancel")
	}
	m := modal.New("Create Workspace",
		modal.WithWidth(width),
		modal.WithPrimaryAction(ActionCreate),
		hints,
		modal.WithInitialFocus(prevFocus),
	)
	for _, section := range sections {
		m.AddSection(section)
	}
	f.modal = m
}

// placementButtons is the segmented Auto/Right/Below row. Each button is a
// create action, so one click both chooses the placement and creates.
func placementButtons(disabled bool) modal.Section {
	buttons := make([]modal.ButtonDef, 0, len(placementCatalog))
	for _, row := range placementCatalog {
		opts := []modal.BtnOption{}
		if row.Placement == PlacementAuto {
			opts = append(opts, modal.BtnPrimary())
		}
		if disabled {
			opts = append(opts, modal.BtnDisabled())
		}
		buttons = append(buttons, modal.Btn(" "+row.Label+" ", row.Action, opts...))
	}
	return modal.Buttons(buttons...)
}

// createButton is the modal's Create action, disabled when the selected row
// cannot be created. A disabled Create is unfocusable and inert, so the row's
// refusal is visible before the click rather than in a toast after it.
func createButton(disabled bool) modal.ButtonDef {
	if disabled {
		return modal.Btn(" Create ", ActionCreate, modal.BtnDisabled())
	}
	return modal.Btn(" Create ", ActionCreate, modal.BtnPrimary())
}

func (f *Form) applyKindChange() {
	f.rematchAgentIndex()
	f.updateNamePlaceholder()
	f.invalidate()
}

func (f *Form) invalidate() {
	if f.modal != nil {
		if id := f.modal.FocusedID(); id != "" {
			f.pendingFocus = id
		}
	}
	f.modal = nil
	f.modalWidth = 0
}

func (f *Form) updateNamePlaceholder() {
	if f.kind == KindTerminalSplit {
		ph := f.terminalName
		if ph == "" {
			ph = "Terminal"
		}
		f.nameInput.Placeholder = ph
		return
	}
	if f.kind == KindWorktree {
		f.nameInput.Placeholder = worktreeNamePlaceholder
		return
	}
	ph := strings.TrimSpace(f.nextShell)
	if ph == "" {
		ph = "Shell 1"
	}
	f.nameInput.Placeholder = ph
}

func (f *Form) agentIDs() []string {
	return agentcatalog.ResolvePicker(f.allowlist, f.kind == KindShell)
}

func (f *Form) selectedAgent() string {
	agents := f.agentIDs()
	if f.agentIndex >= 0 && f.agentIndex < len(agents) {
		return agents[f.agentIndex]
	}
	return f.agentType
}

func (f *Form) pickAgent() string {
	agents := f.agentIDs()
	if last := strings.TrimSpace(loadLastCreateAgent()); last != "" && containsString(agents, last) {
		return last
	}
	if pref := strings.TrimSpace(f.preferredAgent); pref != "" && containsString(agents, pref) {
		return pref
	}
	if def := strings.TrimSpace(f.defaultAgent); def != "" && containsString(agents, def) {
		return def
	}
	if f.kind == KindWorktree {
		for _, at := range agents {
			if at != "" {
				return at
			}
		}
	}
	return ""
}

func (f *Form) rematchAgentIndex() {
	agents := f.agentIDs()
	idx := indexOfString(agents, f.agentType)
	if idx < 0 {
		f.agentType = f.pickAgent()
		idx = indexOfString(agents, f.agentType)
	}
	if idx < 0 {
		idx = 0
		if len(agents) > 0 {
			f.agentType = agents[0]
		}
	}
	f.agentIndex = idx
}

func (f *Form) syncAgentFromIdx() {
	agents := f.agentIDs()
	if f.agentIndex >= 0 && f.agentIndex < len(agents) {
		f.agentType = agents[f.agentIndex]
		return
	}
	f.rematchAgentIndex()
}

func (f *Form) loadAutoApprove() {
	f.skip = loadAgentAutoApprove(f.agentType)
	f.loadedSkip = f.skip
}

func (f *Form) persistSkip() {
	if f.agentType == "" {
		return
	}
	_ = saveAgentAutoApprove(f.agentType, f.skip)
	f.loadedSkip = f.skip
}

func (f *Form) prefillAgentInput() {
	label := agentcatalog.Label(f.agentType)
	if f.agentInput.Value() != label {
		f.agentInput.SetValue(label)
	}
}

func (f *Form) projectItems() []modal.DropdownItem {
	items := make([]modal.DropdownItem, 0, len(f.projects))
	for _, p := range f.projects {
		items = append(items, modal.DropdownItem{
			ID:    "project:" + p.Key,
			Label: p.Label,
			Value: p.Label,
			Data:  p.Key,
		})
	}
	return items
}

func (f *Form) resolveProjectIndex() {
	if f.projectKey != "" {
		for i, p := range f.projects {
			if p.Key == f.projectKey {
				f.projectIndex = i
				return
			}
		}
	}
	if len(f.projects) > 0 {
		f.projectIndex = 0
		f.projectKey = f.projects[0].Key
	}
}

func (f *Form) syncProjectFromIdx() {
	if f.projectIndex >= 0 && f.projectIndex < len(f.projects) {
		f.projectKey = f.projects[f.projectIndex].Key
	}
}

func (f *Form) prefillProjectInput() {
	label := ""
	if f.projectIndex >= 0 && f.projectIndex < len(f.projects) {
		label = f.projects[f.projectIndex].Label
	}
	if f.projectInput.Value() != label {
		f.projectInput.SetValue(label)
	}
}

func (f *Form) branchItems() []modal.DropdownItem {
	items := make([]modal.DropdownItem, len(f.branches))
	for i, branch := range f.branches {
		items[i] = modal.DropdownItem{ID: branch, Label: branch, Value: branch}
	}
	return items
}

func (f *Form) syncBaseIdx() {
	val := f.baseInput.Value()
	for i, branch := range f.branches {
		if branch == val {
			f.baseIndex = i
			return
		}
	}
	if f.baseIndex >= len(f.branches) {
		f.baseIndex = 0
	}
}

func (f *Form) slugHintSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if f.kind != KindWorktree {
			return modal.RenderedSection{}
		}
		display := strings.TrimSpace(f.nameInput.Value())
		slug := workspaceops.SlugifyWorktreeName(display)
		if slug == "" || slug == display {
			return modal.RenderedSection{}
		}
		return modal.RenderedSection{Content: styles.Muted.Render("git: " + slug)}
	}, nil)
}

func (f *Form) skipHintSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if !f.ShowSkip() {
			return modal.RenderedSection{}
		}
		flag := workspaceops.AgentSkipFlag(f.selectedAgent())
		return modal.RenderedSection{Content: styles.Muted.Render(fmt.Sprintf("      (Adds %s)", flag))}
	}, nil)
}

func (f *Form) errorSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if f.err == "" {
			return modal.RenderedSection{}
		}
		errStyle := lipgloss.NewStyle().Foreground(styles.Error)
		return modal.RenderedSection{Content: errStyle.Render("Error: " + f.err)}
	}, nil)
}

func containsString(list []string, id string) bool {
	return indexOfString(list, id) >= 0
}

func indexOfString(list []string, id string) int {
	for i, at := range list {
		if at == id {
			return i
		}
	}
	return -1
}
