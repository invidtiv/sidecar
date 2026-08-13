package tty

import (
	"testing"
	"time"
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
