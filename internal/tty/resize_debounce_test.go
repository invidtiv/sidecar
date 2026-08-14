package tty

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// debouncedModel is a live terminal that has just asserted a resize, which is
// the state a second layout change inside the debounce window arrives in.
func debouncedModel(t *testing.T, since time.Duration) *Model {
	t.Helper()
	m := New(nil)
	m.State = &State{
		Active:       true,
		TargetPane:   "%1",
		LastResizeAt: time.Now().Add(-since),
	}
	m.scopeTarget = "%1"
	m.Width, m.Height = 80, 24
	return m
}

// frozenDebouncedModel is the same model on a clock the test moves by hand, so
// a burst of sizes can be driven through one debounce window without the window
// closing partway through it.
func frozenDebouncedModel(t *testing.T, since time.Duration) (*Model, *time.Time) {
	t.Helper()
	m := debouncedModel(t, since)
	now := time.Now()
	m.nowFn = func() time.Time { return now }
	m.State.LastResizeAt = now.Add(-since)
	return m, &now
}

// Two layout changes inside one debounce window — ctrl+t then alt+t — must not
// leave the pane at the first one's geometry. The debounce bounds how often
// tmux is asked, never whether the pane is given the size at all.
func TestDebouncedResizeIsRetriedRatherThanSwallowed(t *testing.T) {
	m := debouncedModel(t, 10*time.Millisecond)

	cmd := m.SetDimensions(60, 20)
	if cmd == nil {
		t.Fatal("a resize inside the debounce window was dropped, so the pane keeps the old geometry")
	}
	if m.Width != 60 || m.Height != 20 {
		t.Fatalf("model holds %dx%d, want the size it still owes the pane", m.Width, m.Height)
	}

	msg := cmd()
	deferred, ok := msg.(deferredResizeMsg)
	if !ok {
		t.Fatalf("debounced resize produced %T, want a retry", msg)
	}
	if deferred.Scope != m.Scope() {
		t.Fatalf("retry scope %+v, want this activation's %+v", deferred.Scope, m.Scope())
	}
}

// A deferred call must not consume the budget it is waiting on, or its own
// retry is pushed out of reach every time.
func TestDeferredResizeDoesNotConsumeTheDebounceBudget(t *testing.T) {
	m := debouncedModel(t, 10*time.Millisecond)
	before := m.State.LastResizeAt

	m.SetDimensions(60, 20)

	if !m.State.LastResizeAt.Equal(before) {
		t.Fatal("a debounced call recorded a resize it never issued")
	}
}

// Once the window has closed the retry asserts the newest geometry — and a
// burst of sizes arms exactly one retry to do it with. A window drag delivers a
// size per frame; a retry per size would chain a resize every debounce window,
// each one querying and resizing the pane, which is what the budget exists to
// prevent.
func TestDeferredResizeAssertsTheNewestGeometry(t *testing.T) {
	m, clock := frozenDebouncedModel(t, 10*time.Millisecond)
	sizes := [][2]int{{60, 20}, {58, 20}, {56, 19}, {52, 18}, {50, 18}}
	// The burst is driven without wall-clock time passing inside it: a loop that
	// lets the debounce window close between sizes issues real resizes and never
	// exercises coalescing at all.
	armed := 0
	for _, size := range sizes {
		if cmd := m.SetDimensions(size[0], size[1]); cmd != nil {
			armed++
		}
	}
	if armed != 1 {
		t.Fatalf("a burst of %d sizes armed %d retries, want exactly 1", len(sizes), armed)
	}
	if !m.resizeRetryPending {
		t.Fatal("the burst armed a retry the model does not know about")
	}

	*clock = clock.Add(2 * ResizeDebounce)
	cmd := m.Update(deferredResizeMsg{Scope: m.Scope()})
	if cmd == nil {
		t.Fatal("the retry asserted nothing")
	}
	if m.resizeRetryPending {
		t.Fatal("the retry stayed armed after firing, so the next burst arms nothing")
	}
	if !m.State.LastResizeAt.Equal(*clock) {
		t.Fatal("the retry did not record the resize it issued")
	}
	if m.Width != 50 || m.Height != 18 {
		t.Fatalf("retry would assert %dx%d, want the newest 50x18", m.Width, m.Height)
	}
}

// A retry the model never receives must not leave the flag set: the flag is the
// only thing standing between a burst and a chain of resizes, so a stuck one
// swallows every later resize with nothing left to arm a retry — strictly worse
// than the swallow the mechanism exists to remove.
func TestDroppedDeferredResizeDoesNotWedgeTheRetryFlag(t *testing.T) {
	armBurst := func(m *Model, clock *time.Time) {
		t.Helper()
		*clock = clock.Add(2 * ResizeDebounce)
		m.SetDimensions(70, 22)
		if cmd := m.SetDimensions(60, 20); cmd == nil || !m.resizeRetryPending {
			t.Fatalf("a resize inside the debounce window armed no retry (pending=%v)", m.resizeRetryPending)
		}
	}

	t.Run("foreign scope", func(t *testing.T) {
		m, clock := frozenDebouncedModel(t, 10*time.Millisecond)
		armBurst(m, clock)
		m.Update(deferredResizeMsg{Scope: MessageScope{Owner: m.Scope().Owner + 1}})
		if m.resizeRetryPending {
			t.Fatal("a retry dropped for a foreign scope left the flag armed forever")
		}
		armBurst(m, clock)
	})

	t.Run("inactive model", func(t *testing.T) {
		m, clock := frozenDebouncedModel(t, 10*time.Millisecond)
		armBurst(m, clock)
		state := m.State
		m.State = nil
		m.Update(deferredResizeMsg{Scope: m.Scope()})
		if m.resizeRetryPending {
			t.Fatal("a retry dropped by an inactive model left the flag armed forever")
		}
		m.State = state
		armBurst(m, clock)
	})

	t.Run("reactivation", func(t *testing.T) {
		m, clock := frozenDebouncedModel(t, 10*time.Millisecond)
		armBurst(m, clock)
		m.Width, m.Height = 0, 0
		m.Enter("sidecar-resize-wedge-test", "")
		if m.resizeRetryPending {
			t.Fatal("a fresh activation inherited the previous one's armed retry")
		}
	})
}

// A retry belonging to another activation is not this model's work.
func TestDeferredResizeIgnoresForeignScopes(t *testing.T) {
	m := debouncedModel(t, 2*ResizeDebounce)
	if cmd := m.Update(deferredResizeMsg{Scope: MessageScope{Owner: m.Scope().Owner + 1}}); cmd != nil {
		t.Fatal("a retry for another activation was answered")
	}
}

// controlOwnedModel is a live terminal whose pane is being streamed by a control
// transport. The subscription handle carries no manager, so restarting it is
// inert here: what is under test is whether the pane is still given the size,
// not what the transport does about it.
func controlOwnedModel(t *testing.T, since time.Duration) *Model {
	t.Helper()
	m := debouncedModel(t, since)
	m.visible = true
	m.subscription = &ControlSubscription{}
	return m
}

// The defect this pins: a control-owned pane was resized by restarting the
// transport and nothing else. That re-seeds the pane model at the size the model
// holds and sizes the control client with it, but tmux takes a window's geometry
// only from resize-window — so the pane kept its old size, every capture went on
// reporting it, and the viewport letterboxed the terminal inside a box the user
// had already dragged. It came right when something else asserted the geometry,
// which in practice meant clicking into the pane.
//
// The command is deliberately not run: it addresses a bare tmux target, and this
// package's tests share the machine's default server. LastResizeAt is the honest
// marker either way — it is recorded only where the resize is issued.
func TestControlOwnedPaneIsStillGivenItsGeometry(t *testing.T) {
	m := controlOwnedModel(t, 2*ResizeDebounce)
	before := m.State.LastResizeAt

	cmd := m.SetDimensions(60, 20)

	if cmd == nil {
		t.Fatal("a control-owned pane was told nothing about its new size")
	}
	if !m.State.LastResizeAt.After(before) {
		t.Fatal("a control-owned pane was never given the size: restarting the transport is not a resize")
	}
}

// Waiting is not dropping, for a control-owned pane too. The budget bounds how
// often tmux is asked; a size that arrives inside the window is still owed to the
// pane, and the retry is the only thing left to pay it with.
func TestControlOwnedResizeInsideTheWindowArmsTheRetry(t *testing.T) {
	m := controlOwnedModel(t, 10*time.Millisecond)

	cmd := m.SetDimensions(60, 20)

	if cmd == nil {
		t.Fatal("a control-owned resize inside the debounce window was dropped")
	}
	if !m.resizeRetryPending {
		t.Fatal("a control-owned resize inside the window armed no retry, so the pane keeps the old geometry")
	}
	if _, ok := cmd().(deferredResizeMsg); !ok {
		t.Fatal("a control-owned resize inside the window produced something other than a retry")
	}
}

// The same defect at the component's other resize entrance. WindowSizeMsg takes
// this path, and a control-owned pane here was resized by restarting the
// transport and nothing else — td-73fa86's letterboxing through a second door.
// The command is not run, for the reason the sibling test gives.
func TestControlOwnedImmediateResizeIsGivenItsGeometryToo(t *testing.T) {
	m := controlOwnedModel(t, 2*ResizeDebounce)
	before := m.State.LastResizeAt

	cmd := m.ResizeAndPollImmediate(60, 20)

	if cmd == nil {
		t.Fatal("an immediate resize told a control-owned pane nothing about its new size")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("immediate control-owned resize = %T of %d, want the transport restart plus the resize",
			cmd(), len(batch))
	}
	if !m.State.LastResizeAt.After(before) {
		t.Fatal("an immediate control-owned resize was never issued: restarting the transport is not a resize")
	}
	if m.modelLive {
		t.Fatal("the immediate resize kept model-backed presentation across a geometry change")
	}
}

// What the debounce window costs a control-owned pane, said out loud. The
// transport restart sits inside the budget for the same reason the resize does
// — a divider drag delivers a size per frame, and a stop/start per frame tears
// down and re-seeds the pane model faster than it can publish one — so a size
// arriving inside the window leaves the pane on its old geometry until the
// retry. The guarantee is that the retry pays both debts, and that neither is
// paid early: a future early return that stamps the clock without asserting
// anything fails here rather than passing.
func TestControlOwnedDeferredResizePaysBothDebtsWhenTheRetryFires(t *testing.T) {
	m, clock := frozenDebouncedModel(t, 2*ResizeDebounce)
	m.visible = true
	m.subscription = &ControlSubscription{}

	if cmd := m.SetDimensions(70, 22); cmd == nil {
		t.Fatal("the first size, with the budget open, asserted nothing")
	}
	generation, asserted := m.controlGen, m.State.LastResizeAt
	// That restart left the fixture without a transport — its handle carries no
	// manager, so nothing re-seeds it — and its presentation provisional. A live
	// pane has both back by the time the next size arrives, which is the state
	// the deferred one has to be answered in.
	m.subscription = &ControlSubscription{}
	m.modelLive = true

	if cmd := m.SetDimensions(60, 20); cmd == nil || !m.resizeRetryPending {
		t.Fatalf("a size inside the window armed no retry (pending=%v)", m.resizeRetryPending)
	}
	if m.controlGen != generation || !m.State.LastResizeAt.Equal(asserted) {
		t.Fatalf("a deferred resize spent the budget it was waiting on: gen=%d at=%v",
			m.controlGen, m.State.LastResizeAt)
	}

	*clock = clock.Add(2 * ResizeDebounce)
	if cmd := m.Update(deferredResizeMsg{Scope: m.Scope()}); cmd == nil {
		t.Fatal("the retry asserted nothing, so the pane keeps the pre-drag geometry")
	}
	if m.controlGen == generation || m.modelLive {
		t.Fatalf("the retry skipped the generation boundary: gen=%d live=%v", m.controlGen, m.modelLive)
	}
	if !m.State.LastResizeAt.After(asserted) || m.Width != 60 || m.Height != 20 {
		t.Fatalf("the retry did not give the pane the newest geometry: %dx%d at %v",
			m.Width, m.Height, m.State.LastResizeAt)
	}
}
