package agentbroadcast

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/managedtarget"
)

// stageTerminal is a multi-target Terminal modelled on agentcontrol's
// unexported stageTerminal. Screen is "kind:status[:stale]".
type stageTerminal struct {
	mu       sync.Mutex
	panes    map[string]*stagePane
	launches int
	submits  int
}

type stagePane struct {
	stage           string
	target          agentcontrol.Target
	dead            bool
	copyMode        bool
	paneCount       int
	panePID         int
	currentCommand  string
	processIdentity string
	submitErr       error
	submitted       []string
}

func newStage() *stageTerminal {
	return &stageTerminal{panes: map[string]*stagePane{}}
}

func (t *stageTerminal) add(session, name, project, stage string) *stagePane {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := &stagePane{
		stage: stage,
		target: agentcontrol.Target{
			Host: "local", Project: project, Session: session, Name: name, Namespace: "n",
			PaneID: "%" + session, ServerPID: 7, ServerIncarnation: "server-1",
		},
		paneCount:       1,
		panePID:         40 + len(t.panes),
		currentCommand:  "fake",
		processIdentity: "fake",
	}
	t.panes[session] = p
	return p
}

func (t *stageTerminal) pane(session string) *stagePane {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.panes[session]
}

func (t *stageTerminal) Inspect(_ context.Context, target agentcontrol.Target) (agentcontrol.Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pane, ok := t.panes[target.Session]
	if !ok {
		return agentcontrol.Snapshot{}, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: "no pane for " + target.Session}
	}
	tgt := pane.target
	tgt.PanePID = pane.panePID
	return agentcontrol.Snapshot{
		Target:          tgt,
		Dead:            pane.dead,
		CopyMode:        pane.copyMode,
		PaneCount:       pane.paneCount,
		CurrentCommand:  pane.currentCommand,
		ProcessIdentity: pane.processIdentity,
		Screen:          pane.stage,
		CapturedAt:      time.Unix(100, 0),
	}, nil
}

func (t *stageTerminal) Launch(context.Context, agentcontrol.Snapshot, []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.launches++
	return nil
}

func (t *stageTerminal) Submit(_ context.Context, snap agentcontrol.Snapshot, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.submits++
	pane, ok := t.panes[snap.Session]
	if !ok {
		return &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: "no pane for " + snap.Session}
	}
	if pane.submitErr != nil {
		return pane.submitErr
	}
	pane.submitted = append(pane.submitted, text)
	// Flip idle/done so Prompt's stall check sees a lifecycle move.
	parts := strings.Split(pane.stage, ":")
	status := ""
	if len(parts) > 1 {
		status = parts[1]
	}
	if status == string(agentcontrol.StatusIdle) || status == string(agentcontrol.StatusDone) {
		kind := "fake"
		if parts[0] != "" {
			kind = parts[0]
		}
		pane.stage = kind + ":" + string(agentcontrol.StatusWorking)
	}
	return nil
}

func (t *stageTerminal) SendKeys(context.Context, agentcontrol.Snapshot, []string) error {
	return nil
}

func (t *stageTerminal) Capture(context.Context, agentcontrol.Snapshot, agentcontrol.ReadRequest) (string, error) {
	return "", nil
}

func (t *stageTerminal) submitted(session string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	pane := t.panes[session]
	if pane == nil {
		return nil
	}
	return append([]string(nil), pane.submitted...)
}

func (t *stageTerminal) launchCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.launches
}

func (t *stageTerminal) submitCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.submits
}

func stageDetect(s agentcontrol.Snapshot, _ *agentactivity.Tracker) agentcontrol.AgentState {
	parts := strings.Split(s.Screen, ":")
	state := agentcontrol.AgentState{Freshness: "current", CapturedAt: s.CapturedAt, Evidence: "stage." + s.Screen}
	if len(parts) > 0 && parts[0] != "" {
		state.Kind = parts[0]
	}
	state.Status = agentcontrol.StatusUnknown
	if len(parts) > 1 {
		state.Status = agentcontrol.Status(parts[1])
	}
	if len(parts) > 2 && parts[2] == "stale" {
		state.Freshness = "stale"
	}
	state.InteractiveReady = state.Kind != "" && (state.Status == agentcontrol.StatusIdle || state.Status == agentcontrol.StatusDone)
	return state
}

func testControl(term *stageTerminal) agentcontrol.Service {
	return agentcontrol.Service{
		Terminal:   term,
		Detect:     stageDetect,
		Poll:       time.Millisecond,
		Observe:    time.Millisecond,
		Verify:     time.Millisecond,
		StallAfter: 80 * time.Millisecond,
	}
}

func testService(term *stageTerminal, cands []managedtarget.Target) Service {
	return Service{
		Control: testControl(term),
		Candidates: func(context.Context, PlanRequest) ([]managedtarget.Target, error) {
			return cands, nil
		},
	}
}

func managed(session, name, project string) managedtarget.Target {
	return managedtarget.Target{
		Host: "local", Project: project, Kind: "shell",
		Session: session, Name: name, Namespace: "n",
	}
}
