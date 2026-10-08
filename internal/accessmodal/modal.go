// Package accessmodal is the TUI's approval surface for browsers asking to
// sign in to this machine's Sidecar UI API. It is a projection of the API's
// access-request routes over the Local socket: it lists pending requests for
// context and approves by the code the person types. Selecting a row never
// approves, and every rule (code matching, limits, who may approve) stays in
// internal/uiapi.
package accessmodal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/uiapi"
)

const (
	// ActionApprove submits the typed code; ActionCancel closes the modal.
	ActionApprove = "approve"
	ActionCancel  = "cancel"

	codeInputID  = "access-code"
	modalWidth   = 72
	minWidth     = 36
	callTimeout  = 10 * time.Second
	maxListedRow = 8
)

// Client is the slice of the Local-socket API the modal uses.
type Client interface {
	ListAccessRequests(ctx context.Context) (uiapi.AccessRequestList, error)
	ApproveAccess(ctx context.Context, code, surface string) (uiapi.AccessApproval, error)
}

// Host is one open approval modal.
type Host struct {
	stateDir string
	client   Client
	now      func() time.Time

	requests  []uiapi.AccessRequestInfo
	loaded    bool
	loadErr   string
	status    string
	approving bool

	input textinput.Model
	modal *modal.Modal
	width int
	mouse *mouse.Handler
}

// LoadedMsg carries the pending requests for the modal that asked.
type LoadedMsg struct {
	Host     *Host
	Requests []uiapi.AccessRequestInfo
	Err      error
}

// ApprovedMsg carries the outcome of an approval.
type ApprovedMsg struct {
	Host     *Host
	Approval uiapi.AccessApproval
	Err      error
}

// New opens a modal for the API server whose state lives in stateDir. A nil
// client discovers the running server through its endpoint file on each call.
func New(stateDir string, client Client) *Host {
	ti := textinput.New()
	ti.Placeholder = "K7Q-4MX"
	ti.CharLimit = 16
	ti.SetWidth(16)
	ti.Focus()
	return &Host{stateDir: stateDir, client: client, now: time.Now, input: ti, mouse: mouse.NewHandler()}
}

func (h *Host) localClient() (Client, error) {
	if h.client != nil {
		return h.client, nil
	}
	client, err := uiapi.NewLocalClient(h.stateDir)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Load fetches the pending requests.
func (h *Host) Load() tea.Cmd {
	return func() tea.Msg {
		client, err := h.localClient()
		if err != nil {
			return LoadedMsg{Host: h, Err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		list, err := client.ListAccessRequests(ctx)
		return LoadedMsg{Host: h, Requests: list.Requests, Err: err}
	}
}

// ApplyLoaded records a list result addressed to this modal.
func (h *Host) ApplyLoaded(msg LoadedMsg) {
	if msg.Host != h {
		return
	}
	h.loaded = true
	h.loadErr = ""
	if msg.Err != nil {
		h.loadErr = msg.Err.Error()
		h.requests = nil
	} else {
		h.requests = msg.Requests
	}
	h.invalidate()
}

// Submit approves the typed code. A code that cannot be one is refused here
// with the same rule the server applies, without a round trip.
func (h *Host) Submit() tea.Cmd {
	if h.approving {
		return nil
	}
	typed := strings.TrimSpace(h.input.Value())
	if _, ok := uiapi.NormalizeAccessCode(typed); !ok {
		h.status = "Type the six-character code the new browser shows, like K7Q-4MX."
		h.invalidate()
		return nil
	}
	h.approving = true
	h.status = ""
	h.invalidate()
	return func() tea.Msg {
		client, err := h.localClient()
		if err != nil {
			return ApprovedMsg{Host: h, Err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		approval, err := client.ApproveAccess(ctx, typed, "tui")
		return ApprovedMsg{Host: h, Approval: approval, Err: err}
	}
}

// ApplyApproved records an approval result. It reports whether the modal is
// done (the browser was let in) and the line to flash; on a refusal it keeps
// the modal open with the server's reason and reloads the list.
func (h *Host) ApplyApproved(msg ApprovedMsg) (done bool, flash string, cmd tea.Cmd) {
	if msg.Host != h {
		return false, "", nil
	}
	h.approving = false
	if msg.Err != nil {
		var refusal *uiapi.APIError
		if errors.As(msg.Err, &refusal) {
			h.status = refusal.Message
		} else {
			h.status = msg.Err.Error()
		}
		h.input.SetValue("")
		h.invalidate()
		return false, "", h.Load()
	}
	return true, fmt.Sprintf("Approved %s from %s. It signs itself in within a few seconds.", claimedName(msg.Approval.Label), msg.Approval.Address), nil
}

func claimedName(label string) string {
	if label == "" {
		return "the browser"
	}
	return fmt.Sprintf("%q", label)
}

func (h *Host) invalidate() {
	if h.modal != nil {
		h.modal.Invalidate()
	}
}

// Ensure builds the modal for the terminal width.
func (h *Host) Ensure(termWidth int) {
	width := modalWidth
	if width > termWidth-4 {
		width = termWidth - 4
	}
	if width < minWidth {
		width = minWidth
	}
	if h.modal != nil && h.width == width {
		return
	}
	h.width = width
	footer := styles.KeyHint.Render("enter") + styles.Muted.Render(" approve  ") + styles.KeyHint.Render("esc") + styles.Muted.Render(" cancel")
	h.modal = modal.New("Browser access requested",
		modal.WithWidth(width),
		modal.WithVariant(modal.VariantWarning),
		modal.WithPrimaryAction(ActionApprove),
		modal.WithHints(false),
		modal.WithCustomFooter(footer),
		modal.WithInitialFocus(codeInputID),
	).
		AddSection(modal.Text("A browser is asking to sign in to this Sidecar, which gives it your terminals. Type the code that browser shows to let it in. Names are what each browser claims, not proof.")).
		AddSection(modal.Spacer()).
		AddSection(modal.Custom(h.renderRequests, nil)).
		AddSection(modal.Spacer()).
		AddSection(modal.InputWithLabel(codeInputID, "Code:", &h.input, modal.WithSubmitAction(ActionApprove))).
		AddSection(modal.Custom(h.renderStatus, nil)).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Approve ", ActionApprove, modal.BtnPrimary()),
			modal.Btn(" Cancel ", ActionCancel),
		))
}

func (h *Host) renderRequests(contentWidth int, _, _ string) modal.RenderedSection {
	switch {
	case !h.loaded:
		return modal.RenderedSection{Content: styles.Muted.Render("Loading waiting browsers…")}
	case h.loadErr != "":
		return modal.RenderedSection{Content: lipgloss.NewStyle().Foreground(styles.Error).Width(contentWidth).Render("Could not list waiting browsers: " + h.loadErr)}
	case len(h.requests) == 0:
		return modal.RenderedSection{Content: styles.Muted.Render("No browsers are waiting right now. A code from an expired request will not work.")}
	}
	lines := []string{}
	if warning := uiapi.MultipleWaiting(len(h.requests)); warning != "" {
		// Behind a proxy every request shows the same address, so the list
		// cannot tell them apart; only the code can.
		lines = append(lines, lipgloss.NewStyle().Foreground(styles.Warning).Width(contentWidth).Render(warning), "")
	}
	lines = append(lines, styles.Muted.Render(fmt.Sprintf("Waiting (%d):", len(h.requests))))
	now := h.now()
	for i, r := range h.requests {
		if i == maxListedRow {
			lines = append(lines, styles.Muted.Render(fmt.Sprintf("  …and %d more", len(h.requests)-maxListedRow)))
			break
		}
		left := r.ExpiresAt.Sub(now).Truncate(time.Second)
		if left < 0 {
			left = 0
		}
		lines = append(lines,
			truncate(fmt.Sprintf("  %s  expires in %s", requestName(r.Label), left), contentWidth),
			styles.Muted.Render(truncate(fmt.Sprintf("    from %s via %s", r.Address, r.Origin), contentWidth)))
	}
	return modal.RenderedSection{Content: strings.Join(lines, "\n")}
}

func requestName(label string) string {
	if label == "" {
		return "(unnamed browser)"
	}
	return fmt.Sprintf("%q", label)
}

func truncate(s string, width int) string {
	if width <= 1 || lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func (h *Host) renderStatus(contentWidth int, _, _ string) modal.RenderedSection {
	switch {
	case h.approving:
		return modal.RenderedSection{Content: styles.Muted.Render("Approving…")}
	case h.status != "":
		return modal.RenderedSection{Content: lipgloss.NewStyle().Foreground(styles.Error).Width(contentWidth).Render(h.status)}
	}
	return modal.RenderedSection{}
}

// Render draws the modal.
func (h *Host) Render(width, height int) string {
	h.Ensure(width)
	return h.modal.Render(width, height, h.mouse)
}

// HandleKey routes a key and returns an action, if one was chosen.
func (h *Host) HandleKey(msg tea.KeyPressMsg, termWidth int) (string, tea.Cmd) {
	h.Ensure(termWidth)
	before := h.input.Value()
	action, cmd := h.modal.HandleKey(msg)
	if h.status != "" && h.input.Value() != before {
		// Typing a new code clears the last refusal.
		h.status = ""
		h.invalidate()
	}
	return action, cmd
}

// HandleMouse routes a mouse event and returns an action, if one was chosen.
func (h *Host) HandleMouse(msg tea.MouseMsg, termWidth int) string {
	h.Ensure(termWidth)
	return h.modal.HandleMouse(msg, h.mouse)
}

// Paste routes a bracketed paste into the code field.
func (h *Host) Paste(msg tea.PasteMsg) tea.Cmd {
	var cmd tea.Cmd
	h.input, cmd = h.input.Update(msg)
	h.invalidate()
	return cmd
}

// MouseHandler is the hit map the last Render built.
func (h *Host) MouseHandler() *mouse.Handler { return h.mouse }

// WheelAtBoundary reports whether a wheel event would not scroll the modal.
func (h *Host) WheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if h.modal == nil {
		return false
	}
	return h.modal.WheelAtBoundary(msg, h.mouse)
}
