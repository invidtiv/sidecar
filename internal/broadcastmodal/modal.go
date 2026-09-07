package broadcastmodal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentbroadcast"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
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

	messageHint = "ctrl+s send   enter newline   tab move   esc cancel"
	listHint    = "space toggle   a all   n none   ctrl+s send   esc cancel"
	rcptPrefix  = "rcpt:"
	maxList     = 8
	// messageLines is how tall the message field is before it scrolls. A
	// broadcast is usually a sentence; four lines shows a short paragraph
	// whole without turning the modal into an editor.
	messageLines = 4
	// barColumns is what the recipients list reserves on its right: one column
	// for the scrollbar and one of air before it, so a row's last column does
	// not touch the rail. Reserved whether or not the list scrolls, so the
	// columns beside it do not shift as rows arrive.
	barColumns = 2
	// Column floors. Below these a column stops being readable, so the row
	// truncates rather than shrinking further.
	minNameCol   = 8
	minReasonCol = 6
)

// planTimeout bounds a Replan cmd even if Plan ignores cancel. Tests may lower it.
var planTimeout = 12 * time.Second

type Scope int

const (
	ScopeThisProject Scope = iota
	ScopeAllProjects
)

// Surface names the Workspaces surface a modal was opened from. Both surfaces
// see every plan and every result (see overview.IsSharedBroadcastMessage), so
// the answer has to say who asked: without it both of them reported the same
// send, and one broadcast arrived as two notifications.
const (
	SurfaceProject  = "project"
	SurfaceSessions = "sessions"
)

// Host is the one Broadcast-to-agents modal. Both Workspaces surfaces overlay
// it; they do not fork a second copy.
type Host struct {
	ProjectKey string
	StateDir   string
	RemoteNote bool
	Service    agentbroadcast.Service
	// Surface is the surface that opened this modal, one of the Surface
	// constants. A surface answers only for its own results.
	Surface string

	width, height int
	scopeIdx      int
	input         textarea.Model
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

// PlannedMsg carries a plan back to the host that asked for it. Host is that
// host: both Workspaces surfaces receive this message (see
// overview.IsSharedBroadcastMessage), so a modal left open on the surface the
// user is not looking at must not adopt the other one's plan.
type PlannedMsg struct {
	Host *Host
	Gen  int
	Plan agentbroadcast.Plan
	Err  error
}

type SentMsg struct {
	Host   *Host
	Result agentbroadcast.Result
	Err    error
}

// New builds a modal for one surface. surface is SurfaceProject or
// SurfaceSessions and decides which surface reports the result.
func New(surface, projectKey, stateDir string, scope Scope, remoteNote bool) *Host {
	ta := textarea.New()
	ta.Prompt = ""
	ta.Placeholder = "Message every selected agent receives"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	taStyles := ta.Styles()
	taStyles.Focused.Placeholder = lipgloss.NewStyle().Foreground(styles.TextSecondary)
	ta.SetStyles(taStyles)
	ta.SetHeight(messageLines)
	ta.Focus()
	return &Host{
		Surface:    surface,
		ProjectKey: projectKey,
		StateDir:   stateDir,
		RemoteNote: remoteNote,
		scopeIdx:   int(scope),
		input:      ta,
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
	host := h
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), planTimeout)
		ch := make(chan PlannedMsg, 1)
		go func() {
			defer cancel()
			plan, err := svc.Plan(ctx, req)
			if err != nil && ctx.Err() != nil {
				err = fmt.Errorf("timed out looking up agents")
			}
			ch <- PlannedMsg{Host: host, Gen: gen, Plan: plan, Err: err}
		}()
		select {
		case msg := <-ch:
			return msg
		case <-time.After(planTimeout):
			cancel()
			return PlannedMsg{Host: host, Gen: gen, Err: fmt.Errorf("timed out looking up agents")}
		}
	}
}

func (h *Host) ApplyPlan(msg PlannedMsg) {
	if msg.Host != nil && msg.Host != h {
		return
	}
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
	focus := messageID
	if h.modal != nil {
		if id := h.modal.FocusedID(); id != "" {
			focus = id
		}
	}
	// The hint answers for whatever has focus, so the key it names is the key
	// that is live: enter is a newline in the message and a send in the list.
	key := fmt.Sprintf("%d|%d|%d|%v|%q|%s", modalW, h.scopeIdx, h.gen, h.RemoteNote, errString(h.planErr), focus)
	if h.modal != nil && h.cacheKey == key {
		return
	}
	h.cacheKey = key
	h.modal = h.build(modalW, focus)
	h.modal.SetFocus(focus)
}

func (h *Host) build(width int, focus string) *modal.Modal {
	scopeItems := []modal.SelectItem{
		{ID: "this-project", Label: "this project"},
		{ID: "all-projects", Label: "all projects"},
	}
	hint := listHint
	if focus == messageID {
		hint = messageHint
	}
	m := modal.New("Broadcast to agents",
		modal.WithWidth(width),
		modal.WithPrimaryAction(ActionSend),
		modal.WithHintText(hint),
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
		AddSection(modal.Textarea(messageID, &h.input, messageLines, modal.WithTextareaScrollbar()))
	return m
}

func (h *Host) recipientsSection() modal.Section {
	return modal.ScrollingCustom(
		func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
			return h.renderRecipients(contentWidth, focusID, hoverID)
		},
		func(msg tea.Msg, focusID string) (string, tea.Cmd) {
			return h.updateRecipients(msg, focusID)
		},
		func(regionID string) bool {
			return regionID == listID || strings.HasPrefix(regionID, rcptPrefix)
		},
		func(delta int) bool { return !h.canScrollList(delta) },
	)
}

func (h *Host) lines() []Line {
	group := h.scopeIdx == int(ScopeAllProjects)
	return Checklist(h.plan, group, h.selected)
}

// columns is the width of each recipient column for one render. Every column
// takes the width its own content asks for and the name column absorbs what is
// left, so a row truncates only when the modal genuinely runs out of space.
type columns struct{ name, kind, status, reason int }

func columnWidths(rows []Line, width int) columns {
	var c columns
	reasons := false
	for _, row := range rows {
		c.name = max(c.name, ansi.StringWidth(row.Name))
		c.kind = max(c.kind, ansi.StringWidth(row.Agent))
		c.status = max(c.status, ansi.StringWidth(row.Status))
		if reason := rowReason(row); reason != "" {
			reasons = true
			c.reason = max(c.reason, ansi.StringWidth(reason))
		}
	}
	fixed := 4 + 2 // "[x] ", plus the single space before kind and before status
	if reasons {
		fixed++
	}
	avail := max(0, width-fixed)
	need := c.name + c.kind + c.status + c.reason
	if need <= avail {
		c.name += avail - need
		return c
	}
	over := need - avail
	if take := min(over, max(0, c.reason-minReasonCol)); take > 0 {
		c.reason -= take
		over -= take
	}
	if take := min(over, max(0, c.name-minNameCol)); take > 0 {
		c.name -= take
		over -= take
	}
	if over > 0 {
		// Narrower than both floors: the reason goes first — it explains a row
		// rather than naming one — and the name takes whatever remains.
		if take := min(over, c.reason); take > 0 {
			c.reason -= take
			over -= take
		}
		c.name = max(1, c.name-over)
	}
	return c
}

// rowReason is the short explanation a row carries, shown only for rows that
// are not being sent to: a checked row's status is the whole story.
func rowReason(row Line) string {
	if row.Checked {
		return ""
	}
	return row.Reason
}

func (h *Host) renderRecipients(contentWidth int, focusID, hoverID string) modal.RenderedSection {
	lines := h.lines()
	checked, total := selectedCount(lines)
	header := fmt.Sprintf("RECIPIENTS  %d of %d selected", checked, total)
	if h.planning {
		header = "RECIPIENTS  looking up live agents…"
	} else if h.planErr != nil {
		header = "RECIPIENTS  " + h.planErr.Error()
	}

	var b strings.Builder
	b.WriteString(styles.Muted.Render(fit(header, contentWidth)))
	focusables := []modal.FocusableInfo{{
		ID: listID, OffsetX: 0, OffsetY: 0, Width: max(1, contentWidth), Height: 1,
	}}

	rows := recipientLines(lines)
	if len(rows) == 0 {
		b.WriteByte('\n')
		b.WriteString(styles.Muted.Render(fit(h.emptyReason(), contentWidth)))
		y := 1
		for _, line := range lines {
			if line.Kind != lineCount {
				continue
			}
			b.WriteByte('\n')
			b.WriteString(styles.Muted.Render("    " + line.Label))
			y++
		}
		focusables[0].Height = y + 1
		return modal.RenderedSection{Content: b.String(), Focusables: focusables}
	}

	h.clampList(len(rows))
	start, end := h.window(len(rows))
	listWidth := max(1, contentWidth-barColumns)
	cols := columnWidths(rows, listWidth)

	var block []string
	var regions []modal.FocusableInfo
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
				block = append(block, styles.Muted.Render(fit(pending.Label, listWidth)))
				pending = Line{}
			}
			cursor := rowIndex == h.listCursor && focusID == listID
			block = append(block, h.renderRow(line, cols, listWidth, cursor, hoverID == rcptPrefix+line.ID))
			regions = append(regions, modal.FocusableInfo{
				ID: rcptPrefix + line.ID, OffsetX: 0, OffsetY: len(block) - 1,
				Width: listWidth, Height: 1, MouseOnly: true,
			})
		}
	}

	gap := make([]string, len(block))
	for i := range gap {
		gap[i] = " "
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(block, "\n"), strings.Join(gap, "\n"),
		ui.RenderScrollbar(ui.ScrollbarParams{
			TotalItems:   len(rows),
			ScrollOffset: h.listOffset,
			VisibleItems: min(maxList, len(rows)),
			TrackHeight:  len(block),
		}))
	b.WriteByte('\n')
	b.WriteString(body)

	// The header occupies the section's first row; every region below it is
	// offset by that one line.
	for i := range regions {
		regions[i].OffsetY++
	}
	focusables = append(focusables, regions...)
	y := 1 + len(block)
	for _, line := range lines {
		if line.Kind != lineCount {
			continue
		}
		b.WriteByte('\n')
		b.WriteString(styles.Muted.Render("    " + line.Label))
		y++
	}
	focusables[0].Height = y
	return modal.RenderedSection{Content: b.String(), Focusables: focusables}
}

func (h *Host) renderRow(row Line, cols columns, width int, cursor, hover bool) string {
	box := "[ ]"
	if row.Checked {
		box = "[x]"
	}
	line := box + " " + fit(row.Name, cols.name) + " " + fit(row.Agent, cols.kind) + " " + fit(row.Status, cols.status)
	if reason := rowReason(row); reason != "" && cols.reason > 0 {
		line += " " + fit(reason, cols.reason)
	}
	// A row is a list row, not a button: the pointer and the cursor colour it
	// where it already sits rather than indenting it under them.
	style := styles.Body
	switch {
	case cursor:
		style = styles.ListItemFocused
	case hover:
		style = styles.ListItemSelected
	case !row.Checked:
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

// window is the half-open range of recipient rows currently drawn.
func (h *Host) window(n int) (int, int) {
	if n <= maxList {
		return 0, n
	}
	start := h.listOffset
	end := min(start+maxList, n)
	return start, end
}

// clampList keeps the cursor and the window inside a list of n rows. It does
// not pull the window back to the cursor: a wheel scroll leaves the cursor
// where it was, and a render that dragged the window back would undo the
// gesture on the next frame.
func (h *Host) clampList(n int) {
	if n <= 0 {
		h.listCursor = 0
		h.listOffset = 0
		return
	}
	h.listCursor = clampInt(h.listCursor, 0, n-1)
	h.listOffset = clampInt(h.listOffset, 0, max(0, n-maxList))
}

// ensureListVisible scrolls the window so the cursor is on screen. It runs
// when the cursor moves, not on every render.
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
	h.listOffset = clampInt(h.listOffset, 0, n-maxList)
}

// canScrollList reports whether the recipient window can still move delta rows.
// It is read-only: WheelAtBoundary asks it before anything is rebuilt.
func (h *Host) canScrollList(delta int) bool {
	n := len(recipientLines(h.lines()))
	if n <= maxList || delta == 0 {
		return false
	}
	next := clampInt(h.listOffset+delta, 0, n-maxList)
	return next != h.listOffset
}

// scrollList moves the recipient window without moving the cursor, which is
// what a wheel over the list means.
func (h *Host) scrollList(delta int) {
	n := len(recipientLines(h.lines()))
	if n <= maxList {
		h.listOffset = 0
		return
	}
	h.listOffset = clampInt(h.listOffset+delta, 0, n-maxList)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (h *Host) emptyReason() string {
	if h.planning {
		return "Checking every managed shell in scope."
	}
	if h.planErr != nil {
		return h.planErr.Error()
	}
	if h.scopeIdx == int(ScopeThisProject) && strings.TrimSpace(h.ProjectKey) == "" {
		return "Select a workspace so this project has a scope, or switch to all projects."
	}
	return "No live agents to send to."
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
	// The message field is a text area: enter opens a line there, so the send
	// key has to be one the field does not want. It is the same pair the
	// commit modal uses, and it works from every focus.
	switch msg.String() {
	case "ctrl+s", "ctrl+enter":
		return false, h.send()
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
	if h.wheelOverList(msg, handler) {
		return false, nil
	}
	prevScope := h.scopeIdx
	action := h.modal.HandleMouse(msg, handler)
	// A scope click changes what the plan is a plan of, exactly as the key
	// does; without this the segmented control only relabels the old answer.
	if h.scopeIdx != prevScope {
		return false, h.Replan()
	}
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

// wheelOverList scrolls the recipient window when the wheel turns over it, and
// reports that it consumed the event. The modal body would otherwise absorb a
// wheel it has nothing to scroll.
func (h *Host) wheelOverList(msg tea.MouseMsg, handler *mouse.Handler) bool {
	wheel, ok := msg.(tea.MouseWheelMsg)
	if !ok || handler == nil || handler.HitMap == nil {
		return false
	}
	mm := wheel.Mouse()
	var delta int
	switch mm.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return false
	}
	region := handler.HitMap.Test(mm.X, mm.Y)
	if region == nil {
		return false
	}
	if region.ID != listID && !strings.HasPrefix(region.ID, rcptPrefix) {
		return false
	}
	h.scrollList(delta)
	return true
}

func (h *Host) HandlePaste(text string) {
	if h == nil || h.sending {
		return
	}
	h.Ensure(h.width)
	if h.modal == nil || h.modal.FocusedID() != messageID {
		return
	}
	h.input.InsertString(text)
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

// OwnedBy reports whether this modal belongs to the given surface, so a
// surface can ignore the other one's plan and result.
func (h *Host) OwnedBy(surface string) bool {
	return h != nil && h.Surface == surface
}
