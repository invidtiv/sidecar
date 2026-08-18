package resourceview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// fakeHost records the order of the host acts a Pane drives, which is the
// property the click journey actually depends on.
type fakeHost struct {
	events   []string
	openedTo string
}

func (h *fakeHost) FocusLeaf()         { h.events = append(h.events, "focus") }
func (h *fakeHost) EnterFromTerminal() { h.events = append(h.events, "enter-from-terminal") }
func (h *fakeHost) Persist()           { h.events = append(h.events, "persist") }
func (h *fakeHost) OpenURL(url string) tea.Cmd {
	h.events = append(h.events, "open-url")
	h.openedTo = url
	return nil
}

func newPane() (*Pane, *fakeHost, *recorder) {
	rec := &recorder{}
	tabs := NewTabs(nil, rec.resolver())
	tabs.SetSize(60, 20)
	host := &fakeHost{}
	return NewPane(tabs, host), host, rec
}

func TestTerminalClickFreezesViewportBeforeTheLeafOpens(t *testing.T) {
	pane, host, _ := newPane()
	pane.ActivateFromTerminal(ref("CASH-1"))

	// The terminal ritual must precede focus: the viewport has to be captured
	// before a new leaf resizes tmux, or the clicked context is already gone.
	got := strings.Join(host.events, ",")
	if !strings.HasPrefix(got, "enter-from-terminal,focus") {
		t.Fatalf("events = %q, want the terminal ritual before focus", got)
	}
	if !strings.Contains(got, "persist") {
		t.Errorf("events = %q, want the new tab persisted", got)
	}
}

func TestCLIActivationSkipsTheTerminalRitual(t *testing.T) {
	pane, host, _ := newPane()
	pane.Activate(ref("CASH-1"))

	for _, e := range host.events {
		if e == "enter-from-terminal" {
			t.Fatal("a CLI request has no click to clear a selection or freeze a viewport")
		}
	}
	if len(host.events) == 0 || host.events[0] != "focus" {
		t.Errorf("events = %v, want focus first", host.events)
	}
}

func TestDocumentedKeysAreAnsweredHereRatherThanPerHost(t *testing.T) {
	pane, _, rec := newPane()
	pane.ActivateFromTerminal(ref("CASH-1"))
	before := len(rec.calls)

	if handled, _ := pane.HandleKey("r"); !handled {
		t.Error("r should refresh")
	}
	if len(rec.calls) != before+1 {
		t.Errorf("r should have started a resolve, calls %d → %d", before, len(rec.calls))
	}
	for _, key := range []string{"o", "{", "}", "up", "down", "pgup", "pgdown"} {
		if handled, _ := pane.HandleKey(key); !handled {
			t.Errorf("key %q should be answered by the shared pane", key)
		}
	}
	// q and esc are the one legitimate per-surface difference and must fall
	// through to the host's existing close/hide rule.
	for _, key := range []string{"q", "esc"} {
		if handled, _ := pane.HandleKey(key); handled {
			t.Errorf("key %q must be left to the host", key)
		}
	}
}

func TestKeysDoNothingWhenTheLeafIsEmpty(t *testing.T) {
	pane, _, _ := newPane()
	if handled, _ := pane.HandleKey("r"); handled {
		t.Error("an empty leaf should not claim keys")
	}
}

func TestOpenSourceUsesTheHostPathAndOnlyForResolvedDocuments(t *testing.T) {
	pane, host, rec := newPane()
	pane.ActivateFromTerminal(ref("CASH-1"))

	// Nothing resolved yet: o must not invent a URL.
	pane.OpenSource()
	if host.openedTo != "" {
		t.Fatalf("opened %q before anything resolved", host.openedTo)
	}

	id, gen, _ := rec.last()
	pane.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: docWithURL("CASH-1", "https://jira.example.test/browse/CASH-1")})
	pane.OpenSource()
	if host.openedTo != "https://jira.example.test/browse/CASH-1" {
		t.Errorf("opened %q, want the document's source URL", host.openedTo)
	}
}

func TestClosingTheLastTabReportsTheLeafEmpty(t *testing.T) {
	pane, _, _ := newPane()
	pane.ActivateFromTerminal(ref("CASH-1"))
	pane.ActivateFromTerminal(ref("CASH-2"))

	if empty, _ := pane.CloseActiveTab(); empty {
		t.Fatal("one of two tabs closed must not empty the leaf")
	}
	if empty, _ := pane.CloseActiveTab(); !empty {
		t.Error("closing the last tab must tell the host to collapse the split")
	}
}

func TestEveryMutationPersists(t *testing.T) {
	// Persistence is what survives relaunch, so a mutation that forgets to
	// persist is a silently lost tab. Each of these must reach the host.
	cases := []struct {
		name string
		run  func(p *Pane)
	}{
		{"open", func(p *Pane) { p.ActivateFromTerminal(ref("CASH-9")) }},
		{"select", func(p *Pane) { p.SelectTab(0) }},
		{"cycle", func(p *Pane) { p.CycleTab(1) }},
		{"close", func(p *Pane) { p.CloseActiveTab() }},
		{"scroll", func(p *Pane) { p.Scroll(1) }},
	}
	for _, tc := range cases {
		pane, host, rec := newPane()
		pane.ActivateFromTerminal(ref("CASH-1"))
		pane.ActivateFromTerminal(ref("CASH-2"))
		// Give the active tab enough content that a scroll actually moves.
		id, gen, _ := rec.last()
		pane.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: longDoc("CASH-2")})
		host.events = nil

		tc.run(pane)
		if !contains(host.events, "persist") {
			t.Errorf("%s: events = %v, want a persist", tc.name, host.events)
		}
	}
}

func TestCommandsAreOneSharedList(t *testing.T) {
	cmds := Commands()
	if len(cmds) == 0 {
		t.Fatal("a focused Resource leaf must advertise its keys")
	}
	for _, c := range cmds {
		if strings.Contains(c.Name, " ") {
			t.Errorf("command %q should be one word so the footer does not wrap", c.Name)
		}
	}
}

func TestTabStripLabelsMatchTheOpenTabs(t *testing.T) {
	pane, _, _ := newPane()
	pane.ActivateFromTerminal(ref("CASH-1"))
	pane.ActivateFromTerminal(ref("GRES-2"))

	strip := LayoutTabStrip(pane.Tabs, 40, true)
	if len(strip.Tabs) != 2 {
		t.Fatalf("strip has %d tabs, want 2", len(strip.Tabs))
	}
	// The strip styles each cell individually, so the text is only visible
	// once the styling is stripped.
	row := ansi.Strip(strip.Row)
	if !strings.Contains(row, "CASH-1") || !strings.Contains(row, "GRES-2") {
		t.Errorf("strip row = %q, want both locators", row)
	}
}

func TestNilTabStripIsSafe(t *testing.T) {
	if got := LayoutTabStrip(nil, 40, true); len(got.Tabs) != 0 {
		t.Error("a nil tab set has no strip")
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func TestReArmRescuesATabWhoseAnswerWasDiscarded(t *testing.T) {
	pane, _, rec := newPane()
	pane.ActivateFromTerminal(ref("CASH-1"))
	staleID, staleGen, _ := rec.last()

	// The host switches workspace rows and stops routing results. Without a
	// re-arm the tab waits on an answer nobody will deliver.
	if n := pane.ReArmPending(); n != 1 {
		t.Fatalf("re-armed %d tabs, want 1", n)
	}
	m := pane.Tabs.Active()
	if m.State() != StateArmed {
		t.Fatalf("state = %v, want armed rather than stuck loading", m.State())
	}

	// The discarded answer must not be able to land afterwards either.
	if m.Apply(ResolvedMsg{ModelID: staleID, Generation: staleGen, Document: doc("CASH-1", "late")}) {
		t.Error("an answer from before the re-arm must not apply")
	}

	// And r still works.
	before := len(rec.calls)
	if handled, _ := pane.HandleKey("r"); !handled {
		t.Fatal("r should still resolve a re-armed tab")
	}
	if len(rec.calls) != before+1 {
		t.Error("r should have started a fresh resolve")
	}
}

func TestReArmKeepsAnExistingDocumentWhenARefreshIsAbandoned(t *testing.T) {
	pane, _, rec := newPane()
	pane.ActivateFromTerminal(ref("CASH-1"))
	id, gen, _ := rec.last()
	pane.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: doc("CASH-1", "Kept")})

	pane.Refresh()
	pane.ReArmPending()

	m := pane.Tabs.Active()
	if m.State() != StateReady {
		t.Fatalf("state = %v, want the document kept", m.State())
	}
	if got, ok := m.Document(); !ok || got.Title != "Kept" {
		t.Errorf("document = %+v ok=%v, want the last good one", got, ok)
	}
}

func TestScrollAtBoundaryReportsBothEnds(t *testing.T) {
	pane, _, rec := newPane()
	pane.ActivateFromTerminal(ref("CASH-1"))
	id, gen, _ := rec.last()
	pane.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: longDoc("CASH-1")})

	if !pane.ScrollAtBoundary(-1) {
		t.Error("at the top, scrolling up moves nothing")
	}
	if pane.ScrollAtBoundary(1) {
		t.Error("at the top of a long document, scrolling down should move")
	}
	for pane.Scroll(10) {
	}
	if !pane.ScrollAtBoundary(1) {
		t.Error("at the bottom, scrolling down moves nothing")
	}
}
