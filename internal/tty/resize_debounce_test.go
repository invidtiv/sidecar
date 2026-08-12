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

// Once the window has closed the retry asserts the newest geometry.
func TestDeferredResizeAssertsTheNewestGeometry(t *testing.T) {
	m := debouncedModel(t, 10*time.Millisecond)
	m.SetDimensions(60, 20)
	m.SetDimensions(50, 18)
	m.State.LastResizeAt = time.Now().Add(-2 * ResizeDebounce)

	cmd := m.Update(deferredResizeMsg{Scope: m.Scope()})
	if cmd == nil {
		t.Fatal("the retry asserted nothing")
	}
	if m.State.LastResizeAt.IsZero() || time.Since(m.State.LastResizeAt) > ResizeDebounce {
		t.Fatal("the retry did not record the resize it issued")
	}
	if m.Width != 50 || m.Height != 18 {
		t.Fatalf("retry would assert %dx%d, want the newest 50x18", m.Width, m.Height)
	}
}

// A retry belonging to another activation is not this model's work.
func TestDeferredResizeIgnoresForeignScopes(t *testing.T) {
	m := debouncedModel(t, 2*ResizeDebounce)
	if cmd := m.Update(deferredResizeMsg{Scope: MessageScope{Owner: m.Scope().Owner + 1}}); cmd != nil {
		t.Fatal("a retry for another activation was answered")
	}
}
