package mobile

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
)

// FixtureTarget supplies a synthetic identity produced by the real resolver's
// wire mapping; it has no authority over a machine's tmux server.
func FixtureTarget(identity mobileproto.TargetIdentity) ResolvedTarget {
	return ResolvedTarget{WorkspaceID: identity.WorkspaceID, WorkspaceKind: identity.WorkspaceKind, Session: identity.Session, Pane: identity.Pane,
		ServerPID: 42, SessionID: "$1", SessionCreated: "1", DurableSessionCreated: "2026-10-03T00:00:00Z", Width: 80, Height: 24, PaneCount: 1}
}

// FixtureIdentity returns the real service's stable identity for a synthetic shell.
func FixtureIdentity(hub, owner, config, workspace, session, pane string) mobileproto.TargetIdentity {
	return FixtureWorkspaceIdentity(hub, owner, config, workspace, "shell", session, pane)
}

// FixtureWorkspaceIdentity supplies the same deterministic echo authority for
// shells and worktree terminal candidates, without changing legacy identities.
func FixtureWorkspaceIdentity(hub, owner, config, workspace, kind, session, pane string) mobileproto.TargetIdentity {
	target := FixtureTarget(mobileproto.TargetIdentity{WorkspaceID: workspace, WorkspaceKind: kind, Session: session, Pane: pane})
	return targetIdentity(CatalogIdentity{HubID: hub, OwnerHostID: owner, OwnerConfigGeneration: config}, target)
}

// EchoTerminal is a deterministic terminal adapter. It echoes bytes,
// never launches a process, and lets Service own every protocol decision.
type EchoTerminal struct {
	mu    sync.Mutex
	panes map[string]*echoPane
}
type echoPane struct {
	mu          sync.Mutex
	snapshot    tty.ControlSnapshot
	subscribers map[*echoSubscription]func(tty.ControlSnapshot)
	owner       string
	touched     time.Time
	lastInput   time.Time
	lastWrite   time.Time
	ownerIdle   time.Duration
	revision    uint64
	kind, label string
}
type echoSubscription struct{ pane *echoPane }
type echoGeometry struct {
	pane          *echoPane
	owner         string
	kind, label   string
	observed      string
	observedSince time.Time
	observations  int
}

func (e *EchoTerminal) Subscribe(r tty.ControlRequest) (CaptureSubscription, error) {
	e.mu.Lock()
	if e.panes == nil {
		e.panes = map[string]*echoPane{}
	}
	key := r.Session + "\x00" + r.Pane
	pane := e.panes[key]
	if pane == nil {
		pane = &echoPane{snapshot: tty.ControlSnapshot{Session: r.Session, Pane: r.Pane, ServerPID: 42, SessionID: "$1", SessionCreated: "1", PaneWidth: 80, PaneHeight: 24, PaneRows: 24, InputModesKnown: true, CursorVisible: true}, subscribers: map[*echoSubscription]func(tty.ControlSnapshot){}}
		e.panes[key] = pane
	}
	e.mu.Unlock()
	sub := &echoSubscription{pane: pane}
	pane.mu.Lock()
	pane.subscribers[sub] = r.OnSnapshot
	pane.mu.Unlock()
	sub.RequestSnapshot()
	return sub, nil
}
func (s *echoSubscription) RequestSnapshot() {
	s.pane.mu.Lock()
	snapshot, callback := s.pane.snapshot, s.pane.subscribers[s]
	s.pane.mu.Unlock()
	if callback != nil {
		callback(snapshot)
	}
}
func (s *echoSubscription) Close() {
	s.pane.mu.Lock()
	delete(s.pane.subscribers, s)
	s.pane.mu.Unlock()
}
func (p *echoPane) publish() {
	p.mu.Lock()
	snap := p.snapshot
	callbacks := make([]func(tty.ControlSnapshot), 0, len(p.subscribers))
	for _, callback := range p.subscribers {
		callbacks = append(callbacks, callback)
	}
	p.mu.Unlock()
	for _, callback := range callbacks {
		if callback != nil {
			callback(snap)
		}
	}
}

// Geometry must not claim: read-only holder polling uses this constructor too.
func (e *EchoTerminal) Geometry(expected tty.HeadlessTargetIdentity, owner string) (LeaseGeometry, error) {
	e.mu.Lock()
	pane := e.panes[expected.Session+"\x00"+expected.Pane]
	e.mu.Unlock()
	if pane == nil {
		return nil, fmt.Errorf("fixture pane is not open")
	}
	return &echoGeometry{pane: pane, owner: owner}, nil
}
func (g *echoGeometry) claimLocked(cols, rows int, idle time.Duration) {
	g.pane.owner, g.pane.kind, g.pane.label = g.owner, g.kind, g.label
	g.pane.touched = time.Now()
	g.pane.lastInput = g.pane.touched.Add(-idle)
	g.pane.lastWrite = g.pane.touched
	g.pane.ownerIdle = idle
	g.pane.revision++
	g.resizeLocked(cols, rows)
}
func (g *echoGeometry) resizeLocked(cols, rows int) {
	g.pane.snapshot.PaneWidth = cols
	g.pane.snapshot.PaneHeight = rows
	g.pane.snapshot.PaneRows = rows
}
func (g *echoGeometry) ClaimResize(cols, rows int) error {
	if err := fixtureGeometryBounds(cols, rows); err != nil {
		return err
	}
	g.pane.mu.Lock()
	changed := g.pane.snapshot.PaneWidth != cols || g.pane.snapshot.PaneHeight != rows
	defer func() {
		g.pane.mu.Unlock()
		if changed {
			g.pane.publish()
		}
	}()
	g.claimLocked(cols, rows, 0)
	return nil
}
func (g *echoGeometry) SetHolderLabel(kind, label string) error {
	if err := mobileproto.ValidateClientHello(&mobileproto.ClientCapabilities{HolderLabels: true}, &mobileproto.Viewer{Kind: kind, Label: label}); err != nil {
		return err
	}
	g.pane.mu.Lock()
	g.kind, g.label = kind, label
	g.pane.mu.Unlock()
	return nil
}
func (g *echoGeometry) Presence(focused, visible bool, idle time.Duration, cols, rows int, force bool) (bool, error) {
	if err := fixtureGeometryBounds(cols, rows); err != nil {
		return false, err
	}
	if idle < 0 {
		return false, fmt.Errorf("fixture negative presence idle")
	}
	g.pane.mu.Lock()
	changed := false
	defer func() {
		g.pane.mu.Unlock()
		if changed {
			g.pane.publish()
		}
	}()
	if !focused || !visible {
		g.releaseLocked()
		return false, nil
	}
	now := time.Now()
	token := ""
	// Observation is of the last published heartbeat token. The idle duration
	// stays fixed between owner writes, just as it does in the live tmux option.
	if g.pane.owner != "" {
		token = g.pane.owner + ":" + strconv.FormatUint(g.pane.revision, 10) + ":" + strconv.FormatInt(int64(g.pane.ownerIdle/time.Second), 10)
	}
	if token != g.observed || g.observedSince.IsZero() {
		g.observed, g.observedSince, g.observations = token, now, 0
	} else {
		g.observations++
	}
	policy := tty.DefaultLeasePolicy
	policy.StaleTicks = 0
	decision := tty.DecideGeometryLease(tty.LeaseObservation{SelfID: g.owner, Token: token, Focused: true, UnchangedFor: now.Sub(g.observedSince), UnchangedTicks: g.observations, SinceWrite: now.Sub(g.pane.lastWrite), SelfIdle: idle, OwnerIdle: g.pane.ownerIdle, OwnerIdleKnown: token != ""}, policy)
	if !force && !decision.Resize {
		return false, nil
	}
	changed = g.pane.snapshot.PaneWidth != cols || g.pane.snapshot.PaneHeight != rows
	if force || decision.Write {
		g.claimLocked(cols, rows, idle)
	} else {
		g.pane.touched = now
		g.pane.lastInput = now.Add(-idle)
		g.resizeLocked(cols, rows)
	}
	return true, nil
}
func (g *echoGeometry) Holder() (string, string, error) {
	g.pane.mu.Lock()
	defer g.pane.mu.Unlock()
	if g.pane.owner == "" {
		return "", "", nil
	}
	if g.pane.kind == "" {
		return "unknown", "Another viewer", nil
	}
	return g.pane.kind, g.pane.label, nil
}
func (g *echoGeometry) ClaimInput(data []byte, cols, rows int, paste bool) error {
	if err := fixtureGeometryBounds(cols, rows); err != nil {
		return err
	}
	if paste {
		data = tty.NormalizeHeadlessPaste(data)
	}
	if len(data) == 0 {
		return fmt.Errorf("fixture empty input")
	}
	g.pane.mu.Lock()
	g.claimLocked(cols, rows, 0)
	g.pane.snapshot.Output = string(data)
	g.pane.mu.Unlock()
	g.pane.publish()
	return nil
}
func (g *echoGeometry) Paste(data []byte) error {
	data = tty.NormalizeHeadlessPaste(data)
	if len(data) == 0 {
		return fmt.Errorf("fixture empty paste")
	}
	return g.SendLiteral(data)
}
func (g *echoGeometry) releaseLocked() {
	if g.pane.owner == g.owner {
		g.pane.owner, g.pane.kind, g.pane.label = "", "", ""
	}
}
func (g *echoGeometry) requireOwnerLocked() error {
	if g.pane.owner != g.owner {
		return fmt.Errorf("fixture lease released or held by another attachment")
	}
	if time.Since(g.pane.touched) > time.Duration(mobileproto.PresenceTimeoutMS)*time.Millisecond {
		g.releaseLocked()
		return fmt.Errorf("fixture presence expired")
	}
	g.pane.touched = time.Now()
	g.pane.lastWrite = g.pane.touched
	g.pane.ownerIdle = g.pane.touched.Sub(g.pane.lastInput)
	g.pane.revision++
	return nil
}
func (g *echoGeometry) Resize(cols, rows int) error {
	if err := fixtureGeometryBounds(cols, rows); err != nil {
		return err
	}
	g.pane.mu.Lock()
	changed := false
	defer func() {
		g.pane.mu.Unlock()
		if changed {
			g.pane.publish()
		}
	}()
	if err := g.requireOwnerLocked(); err != nil {
		return err
	}
	changed = g.pane.snapshot.PaneWidth != cols || g.pane.snapshot.PaneHeight != rows
	g.resizeLocked(cols, rows)
	return nil
}
func (g *echoGeometry) Heartbeat() error {
	g.pane.mu.Lock()
	defer g.pane.mu.Unlock()
	return g.requireOwnerLocked()
}
func (g *echoGeometry) SendLiteral(data []byte) error {
	g.pane.mu.Lock()
	if err := g.requireOwnerLocked(); err != nil {
		g.pane.mu.Unlock()
		return err
	}
	g.pane.lastInput = time.Now()
	g.pane.lastWrite = g.pane.lastInput
	g.pane.ownerIdle = 0
	g.pane.revision++
	g.pane.snapshot.Output = string(data)
	g.pane.mu.Unlock()
	g.pane.publish()
	return nil
}
func (g *echoGeometry) ExpirePresence() (bool, error) {
	g.pane.mu.Lock()
	defer g.pane.mu.Unlock()
	expired := g.pane.owner == g.owner && time.Since(g.pane.touched) > time.Duration(mobileproto.PresenceTimeoutMS)*time.Millisecond
	if expired {
		g.releaseLocked()
	}
	return expired, nil
}
func (g *echoGeometry) Release() error {
	g.pane.mu.Lock()
	g.releaseLocked()
	g.pane.mu.Unlock()
	return nil
}

// FixtureResolver only resolves catalog-supplied selectors and exact identities.
func FixtureResolver(targets map[string]mobileproto.TargetIdentity) Resolver {
	return func(_ context.Context, selector string) (ResolvedTarget, error) {
		identity, ok := targets[selector]
		if !ok {
			return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorNotFound, Message: "Choose a target from the fixture Sessions catalog."}
		}
		return FixtureTarget(identity), nil
	}
}

func fixtureGeometryBounds(cols, rows int) error {
	if cols < 2 || cols > mobileproto.MaxColumns || rows < 1 || rows > mobileproto.MaxRows {
		return fmt.Errorf("fixture geometry outside terminal bounds")
	}
	return nil
}
