package configui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentintegration"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// Configuration → Agents → Integrations.
//
// An integration is a small Sidecar-owned file installed beside a supported
// agent, which reports that agent's own lifecycle events so Sidecar does not
// have to read them off its screen.
//
// Every fact and every action on this route comes from
// [agentintegration.Service] — the same application service behind
// `sidecar agent integration ...`. Nothing here decides what an install does,
// what makes a status current, or when a mutation is refused. That is not
// tidiness: a surface that computed any of it would be a second answer, and the
// two would agree only until one of them changed.
//
// Two consequences worth stating, because they shape the code:
//
// Every mutation is confirmed against the service's own dry-run plan, so the
// files named in the confirmation are the files the mutation will touch — the
// same list, from the same call, that `--dry-run` prints.
//
// Nothing here runs on a render path. Discovery reads directories, hashes
// installed files, and looks up provider executables on PATH; all of it happens
// in a tea.Cmd, and the route paints a checking state until the answer arrives.

const (
	// regionAgentIntegrations is the row on the Agents page that opens this
	// route.
	regionAgentIntegrations = "config-agent-integrations"

	regionIntegrationRow     = "config-integration-row-"
	regionIntegrationAction  = "config-integration-action-"
	regionIntegrationRecheck = "config-integration-recheck"
)

// ChildAgentIntegrations is the focused route listing agent integrations.
const ChildAgentIntegrations ChildID = "agent-integrations"

// Action shortcut letters.
//
// The action pills are deliberately not cursor stops — they exist only while
// their row is focused, so a cursor that moved onto one would unfocus the row
// and unpaint the pill underneath itself, which is why Remote Hosts' row
// actions work the same way. That makes a shortcut the keyboard route to them,
// and the letters therefore have to be free.
//
// They are not all first letters, because i, d, and e are already spoken for by
// other pages and controlCommand maps a letter to a footer name globally: a
// pill labelled "Install" whose footer said "Init" is exactly the drift the
// shared key/label rule exists to prevent. Each label carries its own key.
const (
	keyIntegrationInstall   = "s"
	keyIntegrationUpdate    = "u"
	keyIntegrationRepair    = "p"
	keyIntegrationUninstall = "x"
)

func integrationActionKey(act agentintegration.Action) string {
	switch act {
	case agentintegration.ActionInstall:
		return keyIntegrationInstall
	case agentintegration.ActionUpdate:
		return keyIntegrationUpdate
	case agentintegration.ActionRepair:
		return keyIntegrationRepair
	case agentintegration.ActionUninstall:
		return keyIntegrationUninstall
	}
	return ""
}

func integrationActionLabel(act agentintegration.Action) string {
	key := strings.ToUpper(integrationActionKey(act))
	name := strings.ToUpper(string(act)[:1]) + string(act)[1:]
	if act == agentintegration.ActionUninstall {
		name = "Remove"
	}
	return key + "  " + name
}

// agentIntegrationsState is what the route knows between frames.
type agentIntegrationsState struct {
	// checking and checked are the tri-state that lets the route say
	// "Checking…" rather than "nothing is installed" before it has looked.
	checking bool
	checked  bool
	// generation drops the result of a probe the user has already superseded.
	generation uint64

	list   []agentintegration.Status
	err    string
	cursor int

	// notice is the outcome of the last mutation, shown until the next one.
	notice string
	// busy is the action in flight, so the route can say so rather than look
	// frozen while a write happens.
	busy string
}

func (m *Model) agentIntegrations() *agentIntegrationsState {
	if m.agentIntegrationsState == nil {
		m.agentIntegrationsState = &agentIntegrationsState{}
	}
	return m.agentIntegrationsState
}

// SetIntegrationService overrides the application service this route uses.
//
// It exists for tests, which must describe a machine rather than have one:
// without it every test of this route would inspect, and its mutation tests
// would rewrite, the developer's real provider configuration.
func (m *Model) SetIntegrationService(svc *agentintegration.Service) { m.integrationService = svc }

func (m *Model) integrations() agentintegration.Service {
	if m.integrationService != nil {
		return *m.integrationService
	}
	return agentintegration.NewService()
}

// OpenAgentIntegrations pushes the route and starts discovery.
func (m *Model) OpenAgentIntegrations() {
	m.PushChild(ChildAgentIntegrations, "Integrations")
	m.queueIntegrationProbe()
}

// queueIntegrationProbe schedules discovery.
//
// It goes on the pending queue rather than being returned as a command because
// the callers are a route push and a control, neither of which has a tea.Cmd
// return path of its own that reaches the runtime. Nothing about it may happen
// synchronously: it stats directories, reads and hashes files, and looks up
// executables on PATH, and the Configuration surface paints on the frame that
// opens it.
func (m *Model) queueIntegrationProbe() {
	state := m.agentIntegrations()
	if state.checking {
		return
	}
	state.checking, state.checked, state.err = true, false, ""
	state.generation++
	generation := state.generation
	svc := m.integrations()
	m.pending = append(m.pending, func() tea.Msg {
		return agentIntegrationsMsg{Generation: generation, List: svc.List()}
	})
}

// agentIntegrationsMsg carries a completed discovery back to the route.
type agentIntegrationsMsg struct {
	Generation uint64
	List       []agentintegration.Status
	Err        string
}

func (agentIntegrationsMsg) configMsg() {}

// agentIntegrationPlanMsg carries the dry-run plan a confirmation is built
// from. The confirmation names the files the service says it will touch, rather
// than a description this page composed and hoped was accurate.
type agentIntegrationPlanMsg struct {
	Provider string
	Action   agentintegration.Action
	Plan     agentintegration.Plan
	Err      string
}

func (agentIntegrationPlanMsg) configMsg() {}

// agentIntegrationMutationMsg carries a completed mutation.
type agentIntegrationMutationMsg struct {
	Provider string
	Action   agentintegration.Action
	Plan     agentintegration.Plan
	Err      string
}

func (agentIntegrationMutationMsg) configMsg() {}

func (m *Model) applyIntegrationList(msg agentIntegrationsMsg) {
	state := m.agentIntegrations()
	if msg.Generation != 0 && msg.Generation != state.generation {
		return
	}
	state.checking, state.checked = false, true
	state.list, state.err = msg.List, msg.Err
	if state.cursor >= len(state.list) {
		state.cursor = max(0, len(state.list)-1)
	}
}

// applyIntegrationPlan raises the confirmation for a planned mutation.
func (m *Model) applyIntegrationPlan(msg agentIntegrationPlanMsg) tea.Cmd {
	state := m.agentIntegrations()
	state.busy = ""
	if msg.Err != "" {
		// A refusal is the service declining, with a reason. It is shown where
		// the outcome of an action is shown, not raised as a dialog: the user
		// asked for something Sidecar will not do, and the answer is one line.
		state.notice = msg.Err
		return nil
	}
	if msg.Plan.Unchanged {
		state.notice = fmt.Sprintf("%s is already %s; nothing to do.", msg.Provider, describeStatusShort(msg.Plan.StatusBefore))
		return nil
	}
	m.confirm = integrationConfirm(msg.Provider, msg.Action, msg.Plan, m.integrations())
	m.rowCursor = 0
	return nil
}

func (m *Model) applyIntegrationMutation(msg agentIntegrationMutationMsg) tea.Cmd {
	state := m.agentIntegrations()
	state.busy = ""
	if msg.Err != "" {
		state.notice = msg.Err
	} else {
		state.notice = describeMutation(msg.Provider, msg.Action, msg.Plan)
	}
	// The list is re-read rather than patched. What a mutation intended and
	// what the filesystem now holds are different claims, and only the second
	// one is worth showing.
	m.queueIntegrationProbe()
	return nil
}

func describeMutation(provider string, act agentintegration.Action, p agentintegration.Plan) string {
	if p.Unchanged {
		return fmt.Sprintf("%s %s: nothing needed changing.", provider, act)
	}
	files := len(p.Ops)
	noun := "files"
	if files == 1 {
		noun = "file"
	}
	return fmt.Sprintf("%s %s: %d %s changed; now %s.", provider, act, files, noun, p.StatusAfter)
}

// abbreviateHome replaces the user's home directory with ~, so a path stays
// legible in a pane that is 34 columns wide.
func abbreviateHome(path, home string) string {
	if home == "" || !strings.HasPrefix(path, home+"/") {
		return path
	}
	return "~" + strings.TrimPrefix(path, home)
}

func describeStatusShort(s agentlifecycle.IntegrationStatus) string {
	switch s {
	case agentlifecycle.StatusCurrent:
		return "up to date"
	case agentlifecycle.StatusNotInstalled:
		return "not installed"
	}
	return string(s)
}

// integrationConfirm builds the confirmation for one mutation.
//
// It names every path, in order, with what happens to it. "Explicit" in the
// plan means the user sees the files before they change, and a dialog that said
// "Install the OpenCode integration?" would not be that.
func integrationConfirm(provider string, act agentintegration.Action, plan agentintegration.Plan, svc agentintegration.Service) *confirmState {
	title := strings.ToUpper(string(act)[:1]) + string(act)[1:] + " integration"
	intro := []string{Body(fmt.Sprintf("%s the %s integration?", strings.ToUpper(string(act)[:1])+string(act)[1:], provider))}

	// Paths are wrapped rather than painted as fixed lines. The detail pane
	// truncates, and a confirmation whose whole point is naming the file must
	// not be the place where a long path loses its last component to an
	// ellipsis. Home is abbreviated for the same reason: shorter is more
	// legible, and `~` is unambiguous.
	body := []string{IndentedMuted(fmt.Sprintf("%s -> %s", plan.StatusBefore, plan.StatusAfter)), ""}
	wrap := []string{}
	for _, op := range plan.Ops {
		wrap = append(wrap, fmt.Sprintf("%s  %s", op.Kind, abbreviateHome(op.Path, svc.Env.Home)))
		wrap = append(wrap, "    "+op.Note)
	}
	wrap = append(wrap, "")
	switch act {
	case agentintegration.ActionUninstall:
		wrap = append(wrap, "Only files Sidecar installed are removed. The provider's own configuration and every other plugin are left exactly as they are.")
	default:
		wrap = append(wrap, "Writes are atomic, anything replaced is backed up beside it, and unrelated configuration is untouched. The integration reports lifecycle facts only: never prompt text, response text, tool arguments or results, or credentials.")
	}

	return &confirmState{
		title:    title,
		intro:    intro,
		body:     body,
		wrapBody: wrap,
		apply: func(m *Model) tea.Cmd {
			state := m.agentIntegrations()
			state.busy = string(act)
			state.notice = ""
			return func() tea.Msg {
				applied, err := svc.Apply(provider, act)
				msg := agentIntegrationMutationMsg{Provider: provider, Action: act, Plan: applied}
				if err != nil {
					msg.Err = err.Error()
				}
				return msg
			}
		},
	}
}

// planIntegration asks the service what an action would do, so the
// confirmation can name it.
func (m *Model) planIntegration(provider string, act agentintegration.Action) tea.Cmd {
	state := m.agentIntegrations()
	state.busy = string(act)
	state.notice = ""
	svc := m.integrations()
	return func() tea.Msg {
		plan, err := svc.Plan(provider, act)
		msg := agentIntegrationPlanMsg{Provider: provider, Action: act, Plan: plan}
		if err != nil {
			msg.Err = err.Error()
		}
		return msg
	}
}

// buildAgentIntegrations paints the route.
func (m *Model) buildAgentIntegrations(b *paneBuilder) {
	state := m.agentIntegrations()

	if !state.checked && !state.checking {
		// Reached without going through OpenAgentIntegrations — a restored
		// route, or a direct push. Discovery still has to be asked for, and
		// still has to happen off this frame.
		m.queueIntegrationProbe()
	}

	b.lead("A small Sidecar-owned file installed beside an agent, so Sidecar learns what that agent is doing from its own lifecycle events instead of its screen.")
	b.blank()

	if state.checking && !state.checked {
		b.lead("Checking which agents are installed…")
		return
	}
	if state.err != "" {
		b.lead("Sidecar could not inspect the installed integrations: " + state.err)
		b.blank()
	}

	b.rightControl(Body("Agents")+"  "+Muted(summariseIntegrations(state.list)),
		regionIntegrationRecheck, "r", "R  Recheck", func(m *Model) tea.Cmd {
			m.queueIntegrationProbe()
			return m.drain(nil)
		})
	b.blank()

	if len(state.list) == 0 {
		b.lead("Sidecar ships no agent integrations in this build.")
		return
	}

	m.syncIntegrationCursor()
	for i := range state.list {
		m.buildIntegrationRow(b, i, state.list[i])
	}

	b.blank()
	if state.busy != "" {
		b.note("Working…")
		b.blank()
	} else if state.notice != "" {
		b.note(state.notice)
		b.blank()
	}
	b.note("Every fact and action here is also `sidecar agent integration`, with --dry-run and --json.")
}

func summariseIntegrations(list []agentintegration.Status) string {
	installed := 0
	for _, st := range list {
		switch st.Status {
		case agentlifecycle.StatusCurrent, agentlifecycle.StatusOutdated, agentlifecycle.StatusNeedsRepair:
			installed++
		}
	}
	return fmt.Sprintf("%d of %d installed", installed, len(list))
}

// syncIntegrationCursor keeps the route's own selection and the pane's row
// cursor naming the same provider.
func (m *Model) syncIntegrationCursor() {
	state := m.agentIntegrations()
	prefix := regionIntegrationRow
	if !strings.HasPrefix(m.focusedID, prefix) {
		return
	}
	var index int
	if _, err := fmt.Sscanf(strings.TrimPrefix(m.focusedID, prefix), "%d", &index); err == nil && index >= 0 && index < len(state.list) {
		state.cursor = index
	}
}

// buildIntegrationRow paints one provider: its name, its status as a badge, and
// — under the row the user is on — where its files are and what can be done
// about them.
//
// One line per row while unfocused is deliberate. The detail pane truncates
// rather than scrolling, and the row cursor still walks onto rows that were
// cut, so a list that spent three lines per provider would silently lose its
// selection off the bottom at 60x24.
func (m *Model) buildIntegrationRow(b *paneBuilder, index int, st agentintegration.Status) {
	id := fmt.Sprintf("%s%d", regionIntegrationRow, index)

	detailFor := func(rowState State) string {
		if !rowState.Focused && !rowState.Hovered {
			return integrationOneLiner(st)
		}
		return integrationDetail(st, m.integrations().Env.Home)
	}

	rowState := b.declare(id, "", true, func(m *Model) tea.Cmd {
		m.agentIntegrations().cursor = index
		return nil
	})
	block := PanelRow(st.Provider, integrationBadge(st), detailFor(rowState), "", b.inner, rowState)
	lines := strings.Split(block, "\n")
	y := len(b.lines)
	b.lines = append(b.lines, lines...)
	b.m.mouse.HitMap.AddRect(id, b.originX, 1+y, RowWidth(b.inner), len(lines), nil)

	if !rowState.Focused && !rowState.Hovered {
		return
	}
	m.buildIntegrationActions(b, index, st)
}

// buildIntegrationActions paints the verbs the service says it would accept.
//
// Offered comes from the service asking its own planner, so a pill that is on
// screen is a pill that will not refuse when it is pressed.
func (m *Model) buildIntegrationActions(b *paneBuilder, index int, st agentintegration.Status) {
	if len(st.Offered) == 0 {
		return
	}
	specs := make([]buttonSpec, 0, len(st.Offered))
	for _, act := range st.Offered {
		act := act
		specs = append(specs, buttonSpec{
			id:      fmt.Sprintf("%s%d-%s", regionIntegrationAction, index, act),
			key:     integrationActionKey(act),
			label:   integrationActionLabel(act),
			primary: act == agentintegration.ActionInstall || act == agentintegration.ActionRepair,
			run: func(m *Model) tea.Cmd {
				return m.planIntegration(st.Provider, act)
			},
		})
	}
	b.buttons(specs...)
}

func integrationBadge(st agentintegration.Status) string {
	switch st.Status {
	case agentlifecycle.StatusCurrent:
		return Badge("current", false)
	case agentlifecycle.StatusOutdated:
		return Badge("outdated", true)
	case agentlifecycle.StatusNeedsRepair:
		return Badge("needs repair", true)
	case agentlifecycle.StatusProviderMissing:
		return Badge("not installed", false)
	case agentlifecycle.StatusUnsupported:
		return Badge("unsupported", false)
	}
	return Badge("available", false)
}

// integrationOneLiner is the unfocused row's second line: the shortest true
// thing about this provider.
func integrationOneLiner(st agentintegration.Status) string {
	switch st.Status {
	case agentlifecycle.StatusUnsupported:
		return "No integration ships for this agent yet."
	case agentlifecycle.StatusProviderMissing:
		return "The " + st.Provider + " command was not found on PATH."
	case agentlifecycle.StatusNotInstalled:
		return "Available to install."
	case agentlifecycle.StatusCurrent:
		return "Version " + st.InstalledVersion + ", driving state at the " + string(st.EffectiveTier) + " tier."
	}
	return st.Message
}

// integrationDetail is what the focused row says: the authority this
// integration can exercise, where its files are, and how many known gaps its
// evidence records.
//
// It is deliberately short. The detail pane truncates rather than scrolling, so
// a focused row that spent fifteen lines on a provider's recorded gaps would
// push every row below it off a 60x24 terminal — which is how the first version
// of this route lost its own list. The full prose lives in
// `sidecar agent integration status PROVIDER` and in the capability matrix,
// and the row says where to find it.
func integrationDetail(st agentintegration.Status, home string) string {
	var parts []string
	if st.Message != "" {
		parts = append(parts, strings.TrimSuffix(st.Message, ".")+".")
	}

	switch st.Status {
	case agentlifecycle.StatusCurrent, agentlifecycle.StatusOutdated:
		tier := "Authority: " + string(st.EffectiveTier)
		if st.TierReason != "" {
			tier += " (" + string(st.TierReason) + ")"
		}
		if st.ProviderVersion != "" {
			proved := ", provider version unproved"
			if st.ProviderInTestedRange {
				proved = ", provider version proved"
			}
			tier += ", " + st.Provider + " " + st.ProviderVersion + proved
		}
		parts = append(parts, tier+".")
	case agentlifecycle.StatusNeedsRepair:
		parts = append(parts, "Reports are not being accepted; this pane falls back to screen detection.")
	}

	// The detail is one wrapped paragraph, not a list. PanelRow reflows it to
	// the row's width, so newlines here would not survive: two facts on
	// separate lines would come back joined mid-sentence, which is how the
	// paths and the gap count first ran into each other.
	if len(st.TargetPaths) > 0 {
		shown := make([]string, 0, len(st.TargetPaths))
		for _, path := range st.TargetPaths {
			shown = append(shown, abbreviateHome(path, home))
		}
		parts = append(parts, "Files: "+strings.Join(shown, ", ")+".")
	}
	if r := st.LastReport; r != nil {
		what := string(r.State)
		if r.Kind != agentlifecycle.KindState {
			what = string(r.Kind)
			if r.Outcome != "" {
				what += " " + string(r.Outcome)
			}
		}
		parts = append(parts, fmt.Sprintf("Last report: %s on pane %s, %s ago.", what, r.PaneID, r.Age))
	}
	if n := len(st.KnownGaps); n > 0 {
		gaps := "1 known gap"
		if n > 1 {
			gaps = fmt.Sprintf("%d known gaps", n)
		}
		parts = append(parts, gaps+", listed by `sidecar agent integration status "+st.Provider+"`.")
	}
	return strings.Join(parts, " ")
}

// buildAgentIntegrationsRow is the row on the Agents page that leads here.
func (m *Model) buildAgentIntegrationsRow(b *paneBuilder) {
	detail := "Let a supported agent report its own lifecycle, instead of Sidecar reading its screen."
	state := b.declare(regionAgentIntegrations, "", true, func(m *Model) tea.Cmd {
		m.OpenAgentIntegrations()
		return m.drain(nil)
	})
	arrow := Muted("→")
	block := PanelRow("Integrations", "", detail, arrow, b.inner, state)
	lines := strings.Split(block, "\n")
	y := len(b.lines)
	b.lines = append(b.lines, lines...)
	rowWidth := RowWidth(b.inner)
	b.m.mouse.HitMap.AddRect(regionAgentIntegrations, b.originX, 1+y, rowWidth, len(lines), nil)
}
