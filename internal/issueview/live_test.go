package issueview

import (
	"errors"
	"strings"
	"testing"
)

// td-312e4e: the issue card must follow the store instead of snapshotting it,
// and must do so without disturbing what the user is reading.

// loadedCard returns a card that has completed one load, as if it were open on
// screen showing data.
func loadedCard(t *testing.T, data *Data) *Model {
	t.Helper()
	m := New(nil)
	m.SetSize(80, 20)
	// Load issues a command we do not run; SetResult is what installs a result,
	// and the generation it expects is the one Load just bumped to.
	_ = m.Load(1, t.TempDir(), data.ID, 7)
	if !m.SetResult(LoadedMsg{
		ModelID: 1, RequestGeneration: m.requestGeneration, Epoch: 7,
		IssueID: data.ID, Data: data,
	}) {
		t.Fatal("SetResult() = false for the initial load")
	}
	return m
}

// refreshResult builds the message a Refresh command would have produced.
func refreshResult(m *Model, data *Data, err error) LoadedMsg {
	return LoadedMsg{
		ModelID: m.modelID, RequestGeneration: m.requestGeneration, Epoch: m.epoch,
		IssueID: m.issueID, Data: data, Error: err, Refresh: true,
	}
}

func epic(children ...string) *Data {
	d := &Data{ID: "td-312e4e", Title: "epic", Status: "in_progress"}
	for _, c := range children {
		d.Children = append(d.Children, Ref{ID: c, Title: c, Status: "open"})
	}
	return d
}

func TestRefreshIsNotOfferedUntilTheStoreMoves(t *testing.T) {
	m := loadedCard(t, epic("a"))
	if cmd := m.Refresh(false); cmd != nil {
		t.Fatal("Refresh() returned a command with no change observed; an idle card must not spawn td")
	}
}

func TestRefreshIsNotOfferedBeforeTheFirstLoad(t *testing.T) {
	m := New(nil)
	m.Observe()
	if cmd := m.Refresh(false); cmd != nil {
		t.Fatal("Refresh() returned a command for a card that has never loaded")
	}
}

func TestRefreshIsOfferedOnceTheStoreMoves(t *testing.T) {
	m := loadedCard(t, epic("a"))
	m.Observe()
	if cmd := m.Refresh(false); cmd == nil {
		t.Fatal("Refresh() = nil after the store moved")
	}
	// The single in-flight slot is claimed.
	m.Observe()
	if cmd := m.Refresh(false); cmd != nil {
		t.Fatal("Refresh() returned a second command while one was in flight")
	}
}

func TestRefreshSuppressedStaysOwed(t *testing.T) {
	m := loadedCard(t, epic("a"))
	m.Observe()
	if cmd := m.Refresh(true); cmd != nil {
		t.Fatal("Refresh(suppressed=true) issued a command")
	}
	if !m.RefreshPending() {
		t.Fatal("a suppressed refresh was dropped instead of deferred")
	}
	if cmd := m.Refresh(false); cmd == nil {
		t.Fatal("the deferred refresh did not land once the veto lifted")
	}
}

// The core of the ticket: a child appearing under an open epic must show up.
// Children are tagged json:"-" on Data because they are not part of `td show`
// output, so a fingerprint taken over Data alone would miss exactly this.
func TestRefreshSeesAnAddedChild(t *testing.T) {
	m := loadedCard(t, epic("a"))
	m.Observe()
	m.Refresh(false)

	if !m.SetResult(refreshResult(m, epic("a", "b"), nil)) {
		t.Fatal("SetResult() = false for a refresh that added a child")
	}
	if got := len(m.Data().Children); got != 2 {
		t.Fatalf("Children = %d after refresh, want 2", got)
	}
}

func TestRefreshSeesAStatusTransition(t *testing.T) {
	m := loadedCard(t, epic("a"))
	m.Observe()
	m.Refresh(false)

	changed := epic("a")
	changed.Status = "done"
	if !m.SetResult(refreshResult(m, changed, nil)) {
		t.Fatal("SetResult() = false for a status change")
	}
	if m.Data().Status != "done" {
		t.Fatalf("Status = %q after refresh, want done", m.Data().Status)
	}
}

// The "refreshing flash": an unchanged re-read must not reach the view. The td
// store's mtime moves whenever any issue anywhere changes, so an open card sees
// signals constantly that have nothing to do with it.
func TestUnchangedRefreshDoesNotRepaint(t *testing.T) {
	data := epic("a")
	m := loadedCard(t, data)
	m.Observe()
	m.Refresh(false)

	if m.SetResult(refreshResult(m, epic("a"), nil)) {
		t.Fatal("SetResult() = true for a refresh that found nothing new; the pane would flash")
	}
}

func TestRefreshPreservesScrollAndCursor(t *testing.T) {
	// A card only scrolls if it is taller than its box, so give it a body.
	tall := epic("a", "b", "c", "d", "e")
	tall.Description = strings.Repeat("a line of description\n", 60)

	m := loadedCard(t, tall)
	m.SetActive(true)
	m.Scroll(3)
	m.moveCursor(1)
	m.moveCursor(1)

	scroll, cursor := m.ScrollOffset(), m.cursor
	if scroll == 0 || cursor < 0 {
		t.Fatalf("test setup did not move the viewport: scroll=%d cursor=%d", scroll, cursor)
	}

	grown := epic("a", "b", "c", "d", "e", "f")
	grown.Description = tall.Description

	m.Observe()
	m.Refresh(false)
	if !m.SetResult(refreshResult(m, grown, nil)) {
		t.Fatal("SetResult() = false for a changed refresh")
	}

	if got := m.ScrollOffset(); got != scroll {
		t.Errorf("scroll = %d after refresh, want %d preserved", got, scroll)
	}
	if m.cursor != cursor {
		t.Errorf("cursor = %d after refresh, want %d preserved", m.cursor, cursor)
	}
}

func TestRefreshDoesNotShowTheLoadingPlaceholder(t *testing.T) {
	m := loadedCard(t, epic("a"))
	m.Observe()
	m.Refresh(false)
	if m.Loading() {
		t.Fatal("Refresh() put the card into its loading state; the user would see it flash")
	}
}

func TestRefreshClampsACursorPastTheNewEnd(t *testing.T) {
	m := loadedCard(t, epic("a", "b", "c"))
	m.SetActive(true)
	for range 3 {
		m.moveCursor(1)
	}
	if m.cursor < 2 {
		t.Fatalf("test setup left cursor at %d, want it near the end", m.cursor)
	}

	m.Observe()
	m.Refresh(false)
	if !m.SetResult(refreshResult(m, epic("a"), nil)) {
		t.Fatal("SetResult() = false for a refresh that removed children")
	}
	if m.cursor >= len(m.navItems()) {
		t.Fatalf("cursor = %d with %d nav items; a shrunk epic left it out of range",
			m.cursor, len(m.navItems()))
	}
}

// A transient td failure — the store locked mid-write, the binary briefly gone
// during an upgrade — must not replace a rendered issue with an error.
func TestFailedRefreshKeepsTheRenderedIssue(t *testing.T) {
	m := loadedCard(t, epic("a"))
	m.Observe()
	m.Refresh(false)

	if m.SetResult(refreshResult(m, nil, errors.New("database is locked"))) {
		t.Fatal("SetResult() = true for a failed refresh")
	}
	if m.Data() == nil {
		t.Fatal("a failed refresh discarded the issue on screen")
	}
	if m.Err() != nil {
		t.Fatalf("a failed refresh surfaced an error: %v", m.Err())
	}
}

func TestRetargetingClearsTheRefreshGate(t *testing.T) {
	m := loadedCard(t, epic("a"))
	m.Observe()

	other := &Data{ID: "td-000000", Title: "other"}
	_ = m.Load(1, t.TempDir(), other.ID, 7)
	if m.RefreshPending() {
		t.Fatal("a refresh owed for the previous issue survived the retarget")
	}
	if !m.SetResult(LoadedMsg{
		ModelID: 1, RequestGeneration: m.requestGeneration, Epoch: 7,
		IssueID: other.ID, Data: other,
	}) {
		t.Fatal("SetResult() = false for the retargeted load")
	}
}

func TestRefreshRejectsAStaleResult(t *testing.T) {
	m := loadedCard(t, epic("a"))
	m.Observe()
	m.Refresh(false)

	stale := refreshResult(m, epic("a", "b"), nil)
	stale.RequestGeneration--
	if m.SetResult(stale) {
		t.Fatal("SetResult() accepted a result from a superseded generation")
	}
}

func TestStoreTargetsResolvesADirectory(t *testing.T) {
	// The td store is SQLite: in WAL mode a write lands in issues.db-wal and may
	// not touch issues.db at all until a checkpoint, so the target has to be the
	// containing directory.
	targets := StoreTargets(t.TempDir())
	if len(targets) != 1 {
		t.Fatalf("StoreTargets() returned %d targets, want 1", len(targets))
	}
	if !targets[0].Dir {
		t.Fatal("StoreTargets() returned a file target; a WAL write would be missed")
	}
}
