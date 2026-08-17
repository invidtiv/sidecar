package issueview

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/tdroot"
)

// This file is the issue card's binding to internal/livewatch. Every host that
// shows an issue — the workspace pane, the global overview preview, the app's
// preview modal — refreshes through it, so none of them has to re-derive when a
// re-read is owed or when a result is worth painting.
//
// The card is read-only, so a live refresh has no buffer to lose. What it does
// have is a reading position: an epic with forty subtasks is scrolled, and the
// user is usually watching one row of it. Load resets scroll, cursor and hover
// by design, because Load means "show me a different issue". Refresh must not,
// because it means "show me the same issue, only true".

// StoreTargets returns what to watch for changes to the td store backing
// workDir, or nil when no store can be resolved.
//
// td keeps issues in SQLite. In WAL mode a write lands in issues.db-wal and may
// not touch issues.db until a checkpoint, so watching the database file alone
// would miss most changes and catch them late. Watching the containing
// directory catches the database, its WAL and its shared-memory file with one
// registration, and the directory holds only those, so there is no noise to
// filter.
//
// This resolves a path; it must not run on the startup path.
func StoreTargets(workDir string) []livewatch.Target {
	dbPath := tdroot.ResolveDBPath(workDir)
	if dbPath == "" {
		return nil
	}
	return []livewatch.Target{livewatch.Dir(filepath.Dir(dbPath))}
}

// Observe records that the td store moved. It is cheap and idempotent: a burst
// of signals owes exactly one re-read.
func (m *Model) Observe() {
	if m == nil {
		return
	}
	m.live.Observe()
}

// Refresh returns a command that re-reads the issue in place, or nil when no
// re-read is owed.
//
// Unlike [Model.Load] it leaves the card exactly as it is: same scroll, same
// cursor, same hover, same data on screen, and no "Loading issue…" placeholder.
// The user should not be able to tell a refresh happened unless something
// actually changed.
//
// It returns nil when nothing is owed, when a re-read is already running, when
// a Load is still in flight, or when the host vetoes with suppressed. A vetoed
// refresh stays owed and lands on the next call, so a host may pass its own
// "the user is mid-interaction" state without dropping the update.
func (m *Model) Refresh(suppressed bool) tea.Cmd {
	if m == nil || m.issueID == "" {
		return nil
	}
	// A card that has never loaded has nothing to refresh; the host's normal
	// load path owns that. A card mid-load would have its result discarded by
	// the generation bump below.
	if m.requestGeneration == 0 || m.loading {
		return nil
	}
	if !m.live.Begin(suppressed) {
		return nil
	}

	m.requestGeneration++
	generation := m.requestGeneration
	modelID, epoch, issueID := m.modelID, m.epoch, m.issueID
	fetch := Fetch(m.workDir, issueID)
	return func() tea.Msg {
		msg, _ := fetch().(FetchedMsg)
		return LoadedMsg{
			ModelID: modelID, RequestGeneration: generation, Epoch: epoch,
			IssueID: issueID, Data: msg.Data, Error: msg.Error,
			Refresh: true,
		}
	}
}

// RefreshPending reports whether a re-read is owed but has not started, so a
// host can re-offer a refresh once its veto lifts.
func (m *Model) RefreshPending() bool {
	if m == nil {
		return false
	}
	return m.live.Pending()
}

// applyRefresh handles a result produced by [Model.Refresh]. It reports whether
// the card changed and therefore needs repainting.
//
// The card is not touched at all when the fetch produced the issue already on
// screen. That is the whole point: the td store's mtime moves whenever any
// issue anywhere changes, so an open card sees signals constantly that have
// nothing to do with it. Repainting on each of them is the visible flash the
// ticket asked to remove.
//
// A failed refresh is also dropped rather than applied. Losing a rendered issue
// to a transient `td` failure — the store locked mid-write, the binary briefly
// unavailable during an upgrade — would be strictly worse than showing content
// that is one refresh out of date, and the next signal retries anyway.
func (m *Model) applyRefresh(msg LoadedMsg) bool {
	stillOwed := m.live.Done()
	defer func() {
		if stillOwed {
			m.live.Observe()
		}
	}()

	if msg.Error != nil || msg.Data == nil {
		return false
	}
	if !m.live.Changed(fingerprintData(msg.Data)) {
		return false
	}

	m.data = msg.Data
	m.err = nil
	m.loading = false
	m.invalidateRender()
	// Scroll, cursor and hover deliberately survive. clampScroll is still
	// needed: the issue may have lost rows, and a scroll offset past the new
	// end would render an empty card.
	m.clampScroll()
	m.clampCursor()
	return true
}

// clampCursor keeps the navigation cursor inside the rebuilt row set. A child
// that was closed and filtered out must not leave the cursor pointing past the
// end of the list.
func (m *Model) clampCursor() {
	items := m.navItems()
	if m.cursor >= len(items) {
		m.cursor = len(items) - 1
	}
	if m.cursor < -1 {
		m.cursor = -1
	}
	if m.hover >= len(items) {
		m.hover = -1
	}
}

// fingerprintData reduces an issue to a change detector.
//
// It cannot use the Data struct directly: Parent, Children and Siblings are
// tagged json:"-" because they are not part of `td show` output, so a JSON
// fingerprint of Data would ignore exactly the change the ticket is about — a
// child appearing under an open epic. Everything the card renders is listed
// here explicitly for that reason.
func fingerprintData(d *Data) string {
	if d == nil {
		return ""
	}
	return livewatch.Fingerprint(struct {
		Core     *Data
		Parent   *Ref
		Children []Ref
		Siblings []Ref
	}{d, d.Parent, d.Children, d.Siblings})
}
