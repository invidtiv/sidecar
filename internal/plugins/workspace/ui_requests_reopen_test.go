package workspace

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/uirequest"
)

// A repeat open of a target whose tab is already loaded focuses that tab and
// returns no command. The ack must still say opened/retargeted — nt-7c82c9:
// the demo showed "declined: window too small" (and, to the agent, a dead
// jump) for a pane that was already beside the shell.
func TestUIRequests_ReopenOfLoadedTabIsNotDeclined(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.width, p.height = 55, 45
	p.View(p.width, p.height)

	req := func(id string) uirequest.Request {
		return uirequest.Request{
			ID: id, Action: uirequest.ActionOpen,
			CreatedAt: time.Now().UTC(), TTLMs: 5000,
			Origin: uirequest.Origin{TmuxSession: "test-shell"},
			Target: uirequest.Target{Kind: uirequest.TargetKindNote, Value: "nt-abc123"},
		}
	}

	if cmd := p.handleUIRequest(req("req-1")); cmd == nil {
		t.Fatal("first open returned no cmd")
	}
	if _, leaf := p.activeNotePane(); leaf == nil {
		t.Fatal("note pane missing after first open")
	}

	// The demo gesture: Tab moves focus off the note pane; the next open must
	// not care where focus sits, only whether the pane is on screen.
	p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyTab})
	if cmd := p.handleUIRequest(req("req-2")); cmd == nil && !p.contentPaneOnScreen(panelayout.Note) {
		t.Log("second open returned no cmd")
	}

	acks, err := uirequest.ReadAcks(config.StateDir(), "req-2", uirequest.ActionOpen)
	if err != nil {
		t.Fatalf("ReadAcks: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d", len(acks))
	}
	switch acks[0].Status {
	case uirequest.StatusOpened, uirequest.StatusRetargeted:
	default:
		t.Fatalf("reopen of a loaded tab acked %q (%s), want opened or retargeted", acks[0].Status, acks[0].Reason)
	}
	if _, leaf := p.activeNotePane(); leaf == nil {
		t.Fatal("note pane vanished after reopen")
	}
}
