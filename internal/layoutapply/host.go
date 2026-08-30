// Package layoutapply owns the all-or-nothing layout apply verdict path:
// resolve descriptors, plan against a trial copy, fit-test floors once, then
// commit or decline with the first violation and the tree byte-for-byte
// untouched. Hosts (the project workspace and the global Sessions surface)
// supply tree access and the create paths their modal already drives.
package layoutapply

import (
	"encoding/json"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
)

// NotOnScreenReason is the decline a layout request earns when its destination
// is not showing. Layout never queues: a stale answer is worse than a refusal.
const NotOnScreenReason = "the origin shell is not on screen, and layout requests are never queued"

// SessionsNotOnScreenReason is the same rule said for the global Sessions
// surface: ScopeGlobal + the Sessions tab must be showing.
const SessionsNotOnScreenReason = "the Sessions surface is not on screen, and layout requests are never queued"

// EscapedGridReason explains a pane that landed in a tree with no grid answer,
// so an ack never carries a bare empty cell beside a success verdict.
const EscapedGridReason = "opened, but the resulting layout no longer resolves to grid columns, so it has no cell address; layout get reports \"grid\": null with the raw tree"

// SpecOriginRequired is the decline a new shell pane earns when there is no
// live session to split beside.
const SpecOriginRequired = "a new shell pane needs a Sidecar shell to split beside; run from inside one"

const tooSmall = "the window is too small to split"
const needsLarger = "the composed layout needs a larger window; layout left unchanged"

// Host is one surface's layout apply adapter. The engine never mutates a tree
// itself except trial copies; commit goes through the host's create paths.
type Host interface {
	PaneRoot() *panelayout.Node
	LastBoxes() map[int]panelayout.Box
	PeerBox() (panelayout.Box, bool)
	Floors() panelayout.Floors

	EnsureDeck()
	DeckTree() *panelayout.Node

	TerminalEnabled() bool
	TerminalOffReason() string
	ShellCapMessage() string
	ShellVisible() bool
	SplitOrigin() string
	TermPanelSessionName() string
	LiveShellSessions() map[string]bool

	// FocusedLeaf is the surface's focused pane leaf, or 0 when nothing on it
	// has pane focus. It answers `layout move --focused`.
	FocusedLeaf() int
	// CommitMove installs an accepted MovePlan on the live tree through the
	// host's own path — identity-preserving apply, deck adoption, persistence,
	// and the existing terminal geometry sync — and reports a user-visible
	// reason instead when it cannot. A non-empty reason means nothing changed.
	CommitMove(plan panelayout.MovePlan) (reason string, cmd tea.Cmd)

	ResolveTargets(kind panelayout.Kind, spec uirequest.LayoutPane) ([]uirequest.Target, string)

	CommitPassive(targets []uirequest.Target, plan panelayout.OpenPlan) (verdict, reason string, cmd tea.Cmd)
	CommitShell(spec uirequest.LayoutPane, plan panelayout.OpenPlan) (verdict, reason string, cmd tea.Cmd)

	RestoreSpec(layout *state.PaneLayoutJSON) tea.Cmd
	AdoptSpecShell(spec uirequest.LayoutPane) (verdict, reason string, cmd tea.Cmd)
	AfterSpecCommit()

	LandedLeaf(kind panelayout.Kind) int
	Ack(req uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, layout json.RawMessage)
}

// ItemPlan is one requested pane's journey through validation, placement
// planning, and commit.
type ItemPlan struct {
	Spec    uirequest.LayoutPane
	Kind    panelayout.Kind
	Targets []uirequest.Target
	Cell    panelayout.Cell
	Plan    panelayout.OpenPlan
	Verdict string
	Reason  string
}

// Apply is the host-side write path: a batch of --pane descriptors, one --spec,
// or one move, all-or-nothing. Get is the host's to answer; this package does
// not build reports.
func Apply(h Host, req uirequest.Request, payload uirequest.LayoutPayload, root, surface string) tea.Cmd {
	if payload.Mode == uirequest.LayoutModeMove {
		return applyMove(h, req, payload, surface)
	}
	if len(payload.Columns) > 0 {
		return applySpec(h, req, payload, root, surface)
	}
	return applyBatch(h, req, payload, root, surface)
}

func ackItemsVersion(items []uirequest.AckItem) int {
	if len(items) == 0 {
		return 0
	}
	return 1
}

// WriteAck is the shared ActionLayout acknowledgement writer. Hosts pass their
// instance id; layout carries the get report verbatim and Items on apply.
func WriteAck(stateDir, instance string, req uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, layout json.RawMessage) {
	_ = uirequest.WriteAck(stateDir, req.ID, req.Action, uirequest.Ack{
		Instance:     instance,
		Host:         uirequest.HostName(),
		PID:          os.Getpid(),
		Status:       status,
		Reason:       reason,
		At:           time.Now().UTC(),
		ItemsVersion: ackItemsVersion(items),
		Items:        items,
		Layout:       layout,
	})
}
