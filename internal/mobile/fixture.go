package mobile

import (
	"context"
	"fmt"
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
	target := FixtureTarget(mobileproto.TargetIdentity{WorkspaceID: workspace, WorkspaceKind: "shell", Session: session, Pane: pane})
	return targetIdentity(CatalogIdentity{HubID: hub, OwnerHostID: owner, OwnerConfigGeneration: config}, target)
}

// EchoTerminal is a per-stream deterministic terminal adapter. It echoes bytes,
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
}
type echoSubscription struct{ pane *echoPane }
type echoGeometry struct {
	pane  *echoPane
	owner string
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
func (e *EchoTerminal) ClaimGeometry(expected tty.HeadlessTargetIdentity, owner string, cols, rows int) (LeaseGeometry, error) {
	e.mu.Lock()
	pane := e.panes[expected.Session+"\x00"+expected.Pane]
	e.mu.Unlock()
	if pane == nil {
		return nil, fmt.Errorf("fixture pane is not open")
	}
	pane.mu.Lock()
	pane.owner = owner
	pane.touched = time.Now()
	pane.snapshot.PaneWidth = cols
	pane.snapshot.PaneHeight = rows
	pane.snapshot.PaneRows = rows
	pane.mu.Unlock()
	// Service requests the replacement after acknowledging control/reset.
	return &echoGeometry{pane: pane, owner: owner}, nil
}
func (g *echoGeometry) requireOwnerLocked() error {
	if g.pane.owner != g.owner {
		return fmt.Errorf("fixture lease released or held by another attachment")
	}
	if time.Since(g.pane.touched) > time.Duration(mobileproto.PresenceTimeoutMS)*time.Millisecond {
		g.pane.owner = ""
		return fmt.Errorf("fixture presence expired")
	}
	g.pane.touched = time.Now()
	return nil
}
func (g *echoGeometry) Resize(cols, rows int) error {
	g.pane.mu.Lock()
	defer g.pane.mu.Unlock()
	if err := g.requireOwnerLocked(); err != nil {
		return err
	}
	g.pane.snapshot.PaneWidth = cols
	g.pane.snapshot.PaneHeight = rows
	g.pane.snapshot.PaneRows = rows
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
		g.pane.owner = ""
	}
	return expired, nil
}
func (g *echoGeometry) Release() error {
	g.pane.mu.Lock()
	if g.pane.owner == g.owner {
		g.pane.owner = ""
	}
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
