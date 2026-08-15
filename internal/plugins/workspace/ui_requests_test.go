package workspace

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestUIRequests_PendingViewLifecycle(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	p := &Plugin{
		shells: []*ShellSession{
			{TmuxName: "sidecar-sh-sidecar-1", Name: "Shell 1"},
			{TmuxName: "sidecar-sh-sidecar-2", Name: "Shell 2"},
		},
		selectedShellIdx: 0,
		shellSelected:    true,
	}

	// Request for unselected shell: should queue and ack StatusQueued
	req := uirequest.Request{
		ID:        "req-1",
		Action:    uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(),
		TTLMs:     5000,
		Origin: uirequest.Origin{
			TmuxSession: "sidecar-sh-sidecar-2",
		},
		Target: uirequest.Target{
			Kind:  uirequest.TargetKindFile,
			Value: "README.md",
			Line:  10,
		},
	}

	cmd := p.handleUIRequest(req)
	if cmd != nil {
		t.Errorf("handleUIRequest on unselected shell returned non-nil cmd: %v", cmd)
	}

	badge, hasBadge := p.pendingViewBadge("sidecar-sh-sidecar-2")
	if !hasBadge || badge == "" {
		t.Errorf("expected pending view badge on shell 2, got %q, %v", badge, hasBadge)
	}

	// Read acks
	t.Logf("config.StateDir() = %s, stateHome = %s", config.StateDir(), filepath.Join(stateHome, "sidecar"))
	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil {
		t.Fatalf("ReadAcks error: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d", len(acks))
	}
	if acks[0].Status != uirequest.StatusQueued {
		t.Errorf("expected status %s, got %s", uirequest.StatusQueued, acks[0].Status)
	}

	// Foreign shell: ignore silently
	foreignReq := uirequest.Request{
		ID:     "req-foreign",
		Action: uirequest.ActionOpen,
		Origin: uirequest.Origin{
			TmuxSession: "other-session",
		},
		Target: uirequest.Target{
			Kind:  uirequest.TargetKindFile,
			Value: "foo.go",
		},
	}
	foreignCmd := p.handleUIRequest(foreignReq)
	if foreignCmd != nil {
		t.Errorf("expected nil cmd for foreign shell, got %v", foreignCmd)
	}
	foreignAcks, _ := uirequest.ReadAcks(filepath.Join(stateHome, "sidecar"), foreignReq.ID, foreignReq.Action)
	if len(foreignAcks) > 0 {
		t.Errorf("expected 0 acks for foreign shell, got %d", len(foreignAcks))
	}
}

// An open that puts nothing on screen must be acknowledged as declined. The
// agent's exit code is the only thing telling it whether the user can see the
// file, so a pane that never opened may never be reported as opened.
func TestUIRequests_SelectedShellDeclinesWhenNothingOpens(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	workDir := t.TempDir()
	p := &Plugin{
		ctx: &plugin.Context{WorkDir: workDir},
		shells: []*ShellSession{
			{TmuxName: "sidecar-sh-sidecar-1", Name: "Shell 1", WorkDir: workDir},
		},
		selectedShellIdx: 0,
		shellSelected:    true,
	}

	req := uirequest.Request{
		ID:        "req-decline",
		Action:    uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(),
		TTLMs:     5000,
		Origin:    uirequest.Origin{TmuxSession: "sidecar-sh-sidecar-1"},
		Target:    uirequest.Target{Kind: uirequest.TargetKindFile, Value: "README.md"},
	}

	// No pane tree: the open cannot land anywhere.
	p.handleUIRequest(req)

	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil {
		t.Fatalf("ReadAcks error: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d", len(acks))
	}
	if acks[0].Status != uirequest.StatusDeclined {
		t.Errorf("expected status %s, got %s", uirequest.StatusDeclined, acks[0].Status)
	}
}

// Both pane hosts live in one process, so their acks must not share a file
// name — otherwise one host's answer silently overwrites the other's.
func TestUIRequests_InstanceIDIsPerHost(t *testing.T) {
	if hostInstanceID() == uirequest.InstanceID("overview") {
		t.Errorf("workspace and overview hosts share instance id %q", hostInstanceID())
	}
}

func TestUIRequests_ExpiredPendingView(t *testing.T) {
	p := &Plugin{
		pendingViews: map[string]*pendingView{
			"sh-1": {
				Target:    uirequest.Target{Kind: uirequest.TargetKindFile, Value: "a.txt"},
				CreatedAt: time.Now().Add(-10 * time.Minute),
				TTLMs:     1000,
			},
		},
	}

	if _, has := p.pendingViewBadge("sh-1"); has {
		t.Errorf("expected expired pending view to have no badge")
	}

	cmd := p.consumePendingView("sh-1")
	if cmd != nil {
		t.Errorf("expected nil cmd from expired pending view, got %v", cmd)
	}
}

func TestUIRequests_PendingDiffLastWriteWins(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.shells = append(p.shells, &ShellSession{
		TmuxName: "sidecar-sh-sidecar-2", Name: "Shell 2",
		Agent: &Agent{TmuxPane: "%902", OutputBuf: tty.NewOutputBuffer(20)},
	})
	p.selectedShellIdx = 0

	first := uirequest.Request{
		ID: "req-diff-1", Action: uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{TmuxSession: "sidecar-sh-sidecar-2"},
		Target: uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "wt"},
	}
	second := first
	second.ID = "req-diff-2"
	second.Target.Value = "c:abc1234"

	if cmd := p.handleUIRequest(first); cmd != nil {
		t.Fatal("first queued open returned a cmd")
	}
	if cmd := p.handleUIRequest(second); cmd != nil {
		t.Fatal("second queued open returned a cmd")
	}
	pv := p.pendingViews["sidecar-sh-sidecar-2"]
	if pv == nil || pv.Target.Value != "c:abc1234" {
		t.Fatalf("pending = %+v, want last-write-wins c:abc1234", pv)
	}
	if len(p.pendingViews) != 1 {
		t.Fatalf("pending slots = %d, want one", len(p.pendingViews))
	}

	p.selectedShellIdx = 1
	if cmd := p.consumePendingView("sidecar-sh-sidecar-2"); cmd == nil {
		t.Fatal("consume opened nothing")
	}
	diff, _ := p.activeDiffPane()
	if diff == nil {
		t.Fatal("queued Diff did not open")
	}
	if keys := diffTabKeys(diff); !reflect.DeepEqual(keys, []string{"c:abc1234"}) {
		t.Fatalf("opened tabs = %v, want only the last queued target", keys)
	}
}

func TestUIRequests_OpenRequestEqualsHashClick(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	root := t.TempDir()
	clicked := docPaneTestPlugin(t, root, true)
	clicked.terminalSpecResolver = func(_, raw string) (string, bool) { return raw, raw == "abc1234" }
	if _, ok := clicked.activateDiffLink("abc1234"); !ok {
		t.Fatal("hash click failed")
	}

	opened := docPaneTestPlugin(t, root, true)
	req := uirequest.Request{
		ID: "req-parity", Action: uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{TmuxSession: "test-shell"},
		Target: uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "c:abc1234"},
	}
	if cmd := opened.handleUIRequest(req); cmd == nil {
		t.Fatal("open request opened nothing")
	}

	if got, want := paneTreeShape(opened.paneRoot), paneTreeShape(clicked.paneRoot); got != want {
		t.Fatalf("request tree %s, click tree %s", got, want)
	}
	clickDiff, _ := clicked.activeDiffPane()
	openDiff, _ := opened.activeDiffPane()
	if clickDiff == nil || openDiff == nil {
		t.Fatal("missing Diff leaf")
	}
	if !reflect.DeepEqual(diffTabKeys(openDiff), diffTabKeys(clickDiff)) {
		t.Fatalf("request tabs = %v, click tabs = %v", diffTabKeys(openDiff), diffTabKeys(clickDiff))
	}
}

func TestUIRequests_SplitBelowAfterFileAndIssueKeepsTerminalHeight(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n\nfile body\n")
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update("wrote clicked.md:1\nfollow-up is td-1a2b3c\n")
	deliverLoads(t, p, clickTerminalLink(t, p, "clicked.md"))
	if cmd := clickTerminalLink(t, p, "td-1a2b3c"); cmd == nil {
		t.Fatal("issue click opened nothing")
	}
	before, content := paneLeafBoxes(t, p)
	termH := before[PaneTerminal].H

	req := uirequest.Request{
		ID: "req-split", Action: uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{TmuxSession: "test-shell"},
		Target:  uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "wt"},
		Options: uirequest.Options{Split: "below"},
	}
	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("--split below opened nothing")
	}
	boxes, _ := paneLeafBoxes(t, p)
	if boxes[PaneTerminal].H != termH || boxes[PaneTerminal].H != content.H {
		t.Fatalf("terminal H changed: before %d after %#v content %#v", termH, boxes[PaneTerminal], content)
	}
	if p.paneRoot.Split == nil || p.paneRoot.Split.A.Kind != PaneTerminal {
		t.Fatalf("--split below retargeted onto the terminal: %#v", p.paneRoot)
	}
}

func TestUIRequests_SplitIgnoredOnRetarget(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("first Diff open failed")
	}
	shape := paneTreeShape(p.paneRoot)
	req := uirequest.Request{
		ID: "req-retarget", Action: uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{TmuxSession: "test-shell"},
		Target:  uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "c:abc1234"},
		Options: uirequest.Options{Split: "below"},
	}
	if cmd := p.handleUIRequest(req); cmd == nil {
		t.Fatal("retarget open failed")
	}
	if got := paneTreeShape(p.paneRoot); got != shape {
		t.Fatalf("--split on retarget grew the tree: %s -> %s", shape, got)
	}
	diff, _ := p.activeDiffPane()
	if diff == nil || diff.tabs.Find("c:abc1234") < 0 {
		t.Fatalf("retarget did not open the new tab: %v", diffTabKeys(diff))
	}
}

func paneTreeShape(n *PaneNode) string {
	if n == nil {
		return "nil"
	}
	if n.Split != nil {
		axis := "cols"
		if n.Split.Axis == SplitRows {
			axis = "rows"
		}
		return "(" + paneTreeShape(n.Split.A) + " " + axis + " " + paneTreeShape(n.Split.B) + ")"
	}
	switch n.Kind {
	case PaneTerminal:
		return "T"
	case PaneDoc:
		return "D"
	case PaneIssue:
		return "I"
	case PaneDiff:
		return "F"
	default:
		return "?"
	}
}
