package broadcastmodal

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentbroadcast"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
)

const (
	CommandID          = "broadcast-agents"
	CommandName        = "Broadcast"
	CommandDescription = "Broadcast to agents"

	ActionSend   = "send"
	ActionCancel = "cancel"

	scopeID   = "scope"
	listID    = "recipients"
	messageID = "message"

	hintText   = "enter send   space toggle   a all   n none   esc cancel"
	rcptPrefix = "rcpt:"
	maxList    = 8
)

type Scope int

const (
	ScopeThisProject Scope = iota
	ScopeAllProjects
)

// Host is the one Broadcast-to-agents modal. Both Workspaces surfaces overlay
// it; they do not fork a second copy.
type Host struct {
	ProjectKey string
	StateDir   string
	RemoteNote bool
	Service    agentbroadcast.Service

	width, height int
	scopeIdx      int
	input         textinput.Model
	selected      map[string]bool
	plan          agentbroadcast.Plan
	planErr       error
	planning      bool
	gen           int
	listCursor    int
	listOffset    int
	modal         *modal.Modal
	cacheKey      string
	sending       bool
}

type PlannedMsg struct {
	Gen  int
	Plan agentbroadcast.Plan
	Err  error
}

type SentMsg struct {
	Host   *Host
	Result agentbroadcast.Result
	Err    error
}

func New(projectKey, stateDir string, scope Scope, remoteNote bool) *Host {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = ""
	ti.CharLimit = 240
	ti.Focus()
	return &Host{
		ProjectKey: projectKey,
		StateDir:   stateDir,
		RemoteNote: remoteNote,
		scopeIdx:   int(scope),
		input:      ti,
		selected:   map[string]bool{},
	}
}

func Enabled() bool {
	return features.IsEnabled(features.AgentControl.Name)
}

func Command(context string) plugin.Command {
	return plugin.Command{
		ID: CommandID, Name: CommandName, Description: CommandDescription,
		Context: context, Priority: 21,
	}
}

func (h *Host) service() agentbroadcast.Service {
	if h.Service.Candidates != nil || h.Service.Control.Terminal != nil {
		return h.Service
	}
	stateDir := h.StateDir
	if stateDir == "" {
		stateDir = config.StateDir()
	}
	return agentbroadcast.Service{
		Control:    agentcontrol.Service{Terminal: agentcontrol.NewLocalTerminal()},
		Candidates: agentbroadcast.CandidatesFromState(stateDir),
	}
}

func (h *Host) planRequest() agentbroadcast.PlanRequest {
	req := agentbroadcast.PlanRequest{FromUser: true}
	if h.scopeIdx == int(ScopeAllProjects) {
		req.ScopeKind = agentbroadcast.ScopeAll
	} else {
		req.ScopeKind = agentbroadcast.ScopeProject
		req.Project = h.ProjectKey
	}
	return req
}

func (h *Host) Replan() tea.Cmd {
	h.planning = true
	h.gen++
	gen := h.gen
	req := h.planRequest()
	svc := h.service()
	return func() tea.Msg {
		plan, err := svc.Plan(context.Background(), req)
		return PlannedMsg{Gen: gen, Plan: plan, Err: err}
	}
}

func (h *Host) ApplyPlan(msg PlannedMsg) {
	if msg.Gen != h.gen {
		return
	}
	h.planning = false
	h.planErr = msg.Err
	h.plan = msg.Plan
	h.selected = selectedFromPlan(msg.Plan)
	h.listCursor = 0
	h.listOffset = 0
	h.modal = nil
}

func selectedFromPlan(plan agentbroadcast.Plan) map[string]bool {
	out := make(map[string]bool, len(plan.Recipients))
	for _, row := range plan.Recipients {
		out[agentbroadcast.RecipientID(row)] = row.Outcome == agentbroadcast.OutcomeWouldSend
	}
	return out
}

func (h *Host) Ensure(width int) {
	if h == nil {
		return
	}
	h.width = width
	modalW := 72
	if maxW := width - 4; maxW > 0 && modalW > maxW {
		modalW = maxW
	}
	if modalW < 20 {
		modalW = 20
	}
	key := fmt.Sprintf("%d|%d|%d|%v|%q", modalW, h.scopeIdx, h.gen, h.RemoteNote, errString(h.planErr))
	if h.modal != nil && h.cacheKey == key {
		return
	}
	focus := messageID
	if h.modal != nil {
		if id := h.modal.FocusedID(); id != "" {
			focus = id
		}
	}
	h.cacheKey = key
	h.modal = h.build(modalW)
	h.modal.SetFocus(focus)
}

func (h *Host) build(width int) *modal.Modal {
	scopeItems := []modal.SelectItem{
		{ID: "this-project", Label: "this project"},
		{ID: "all-projects", Label: "all projects"},
	}
	m := modal.New("Broadcast to agents",
		modal.WithWidth(width),
		modal.WithPrimaryAction(ActionSend),
		modal.WithHintText(hintText),
		modal.WithCloseOnBackdropClick(true),
	).
		AddSection(modal.Text("SCOPE")).
		AddSection(modal.Select(scopeID, scopeItems, &h.scopeIdx, modal.WithShape(modal.ShapeSegmented))).
		AddSection(modal.Spacer()).
		AddSection(h.recipientsSection()).
		AddSection(modal.When(func() bool { return h.RemoteNote },
			modal.Text("Remote agents are not yet recipients."))).
		AddSection(modal.Spacer()).
		AddSection(modal.Text("MESSAGE")).
		AddSection(modal.Input(messageID, &h.input))
	return m
}

func (h *Host) recipientsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		return h.renderRecipients(contentWidth, focusID, hoverID)
	}, func(msg tea.Msg, focusID string) (string, tea.Cmd) {
		return h.updateRecipients(msg, focusID)
	})
}

func (h *Host) lines() []Line {
	group := h.scopeIdx == int(ScopeAllProjects)
	return Checklist(h.plan, group, h.selected)
}

func (h *Host) renderRecipients(contentWidth int, focusID, hoverID string) modal.RenderedSection {
	lines := h.lines()
	checked, total := selectedCount(lines)
	header := fmt.Sprintf("RECIPIENTS  %d of %d selected", checked, total)
	if h.planning {
		header = "RECIPIENTS  loading…"
	} else if h.planErr != nil {
		header = "RECIPIENTS  " + h.planErr.Error()
	}

	var b strings.Builder
	b.WriteString(styles.Muted.Render(fit(header, contentWidth)))
	focusables := []modal.FocusableInfo{{
		ID: listID, OffsetX: 0, OffsetY: 0, Width: max(1, contentWidth), Height: 1,
	}}

	rows := recipientLines(lines)
	if len(rows) == 0 && !h.planning && h.planErr == nil {
		b.WriteByte('\n')
		b.WriteString(styles.Muted.Render("No live agents in scope"))
		return modal.RenderedSection{Content: b.String(), Focusables: focusables}
	}

	h.clampList(len(rows))
	start, end := 0, len(rows)
	if len(rows) > maxList {
		start = h.listOffset
		end = start + maxList
		if end > len(rows) {
			end = len(rows)
		}
	}

	y := 1
	rowIndex := -1
	var pending Line
	for _, line := range lines {
		switch line.Kind {
		case lineSection:
			pending = line
		case lineRow:
			rowIndex++
			if rowIndex < start || rowIndex >= end {
				continue
			}
			if pending.Kind == lineSection {
				b.WriteByte('\n')
				b.WriteString(styles.Muted.Render(fit(pending.Label, contentWidth)))
				pending = Line{}
				y++
			}
			b.WriteByte('\n')
			cursor := rowIndex == h.listCursor && focusID == listID
			b.WriteString(h.renderRow(line, contentWidth, cursor, hoverID == rcptPrefix+line.ID))
			focusables = append(focusables, modal.FocusableInfo{
				ID: rcptPrefix + line.ID, OffsetX: 0, OffsetY: y,
				Width: max(1, contentWidth), Height: 1, MouseOnly: true,
			})
			y++
		case lineCount:
			b.WriteByte('\n')
			b.WriteString(styles.Muted.Render("    " + line.Label))
			y++
		}
	}
	focusables[0].Height = y
	return modal.RenderedSection{Content: b.String(), Focusables: focusables}
}

func (h *Host) renderRow(row Line, width int, cursor, hover bool) string {
	box := "[ ]"
	if row.Checked {
		box = "[x]"
	}
	nameW, kindW, statusW := 18, 8, 8
	reasonW := width - 4 - nameW - kindW - statusW - 3
	if reasonW < 6 {
		reasonW = 6
		nameW = max(8, width-4-kindW-statusW-reasonW-3)
	}
	line := box + " " + fit(row.Name, nameW) + " " + fit(row.Agent, kindW) + " " + fit(row.Status, statusW)
	if row.Reason != "" && !row.Checked {
		line += " " + fit(row.Reason, reasonW)
	}
	style := styles.Body
	if cursor {
		style = styles.ButtonFocused
	} else if hover {
		style = styles.ButtonHover
	} else if !row.Checked {
		style = styles.Muted
	}
	return style.Render(fit(line, width))
}

func (h *Host) updateRecipients(msg tea.Msg, focusID string) (string, tea.Cmd) {
	if focusID != listID {
		return "", nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return "", nil
	}
	rows := recipientLines(h.lines())
	if len(rows) == 0 {
		if key.String() == "enter" {
			return "modal.submit-primary", nil
		}
		return "", nil
	}
	h.clampList(len(rows))
	switch key.String() {
	case "j", "down":
		if h.listCursor < len(rows)-1 {
			h.listCursor++
			h.ensureListVisible(len(rows))
		}
		return "modal.overlay-idle", nil
	case "k", "up":
		if h.listCursor > 0 {
			h.listCursor--
			h.ensureListVisible(len(rows))
		}
		return "modal.overlay-idle", nil
	case " ", "space":
		h.toggle(rows[h.listCursor].ID)
		return "modal.overlay-idle", nil
	case "enter":
		return "modal.submit-primary", nil
	}
	return "", nil
}

func (h *Host) clampList(n int) {
	if n <= 0 {
		h.listCursor = 0
		h.listOffset = 0
		return
	}
	if h.listCursor >= n {
		h.listCursor = n - 1
	}
	if h.listCursor < 0 {
		h.listCursor = 0
	}
	h.ensureListVisible(n)
}

func (h *Host) ensureListVisible(n int) {
	if n <= maxList {
		h.listOffset = 0
		return
	}
	if h.listCursor < h.listOffset {
		h.listOffset = h.listCursor
	}
	if h.listCursor >= h.listOffset+maxList {
		h.listOffset = h.listCursor - maxList + 1
	}
}

func (h *Host) toggle(id string) {
	if id == "" {
		return
	}
	h.selected[id] = !h.selected[id]
}

func (h *Host) selectAll(on bool) {
	for _, row := range h.plan.Recipients {
		h.selected[agentbroadcast.RecipientID(row)] = on
	}
}

func (h *Host) HandleKey(msg tea.KeyPressMsg) (close bool, cmd tea.Cmd) {
	h.Ensure(h.width)
	if h.modal == nil {
		return false, nil
	}
	if h.sending {
		if msg.String() == "esc" {
			return true, nil
		}
		return false, nil
	}
	if h.modal.FocusedID() != messageID {
		switch msg.String() {
		case "a":
			h.selectAll(true)
			return false, nil
		case "n":
			h.selectAll(false)
			return false, nil
		}
	}
	prevScope := h.scopeIdx
	action, cmd := h.modal.HandleKey(msg)
	if h.scopeIdx != prevScope {
		return false, tea.Batch(cmd, h.Replan())
	}
	switch {
	case action == ActionCancel:
		return true, nil
	case action == ActionSend:
		return false, tea.Batch(cmd, h.send())
	case strings.HasPrefix(action, rcptPrefix):
		h.toggle(strings.TrimPrefix(action, rcptPrefix))
		return false, cmd
	}
	return false, cmd
}

func (h *Host) HandleMouse(msg tea.MouseMsg, handler *mouse.Handler) (close bool, cmd tea.Cmd) {
	h.Ensure(h.width)
	if h.modal == nil {
		return false, nil
	}
	action := h.modal.HandleMouse(msg, handler)
	switch {
	case action == ActionCancel:
		return true, nil
	case action == ActionSend:
		return false, h.send()
	case strings.HasPrefix(action, rcptPrefix):
		h.toggle(strings.TrimPrefix(action, rcptPrefix))
	}
	return false, nil
}

func (h *Host) HandlePaste(text string) {
	if h == nil || h.sending {
		return
	}
	h.Ensure(h.width)
	if h.modal == nil || h.modal.FocusedID() != messageID {
		return
	}
	h.input.SetValue(h.input.Value() + text)
}

func (h *Host) WheelAtBoundary(msg tea.MouseWheelMsg, handler *mouse.Handler) bool {
	h.Ensure(h.width)
	if h.modal == nil {
		return true
	}
	return h.modal.WheelAtBoundary(msg, handler)
}

func (h *Host) Render(width, height int, handler *mouse.Handler) string {
	h.width, h.height = width, height
	h.Ensure(width)
	if h.modal == nil {
		return ""
	}
	return h.modal.Render(width, height, handler)
}

func (h *Host) send() tea.Cmd {
	text := strings.TrimSpace(h.input.Value())
	if text == "" || h.sending {
		return nil
	}
	h.sending = true
	plan := h.plan.WithRequested(func(row agentbroadcast.Recipient) bool {
		return h.selected[agentbroadcast.RecipientID(row)]
	})
	plan.FromUser = true
	svc := h.service()
	host := h
	return func() tea.Msg {
		result, err := svc.Send(context.Background(), plan, text)
		return SentMsg{Host: host, Result: result, Err: err}
	}
}

func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w == n {
		return s
	}
	if w > n {
		return ansi.Truncate(s, n, "…")
	}
	return s + strings.Repeat(" ", n-w)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
