package noteview

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/tdroot"
)

// StoreTargets returns what to watch for changes to the td store backing
// workDir, or nil when no store can be resolved. Notes live in the same
// SQLite directory as issues; watching the directory catches WAL writes.
func StoreTargets(workDir string) []livewatch.Target {
	dbPath := tdroot.ResolveDBPath(workDir)
	if dbPath == "" {
		return nil
	}
	return []livewatch.Target{livewatch.Dir(filepath.Dir(dbPath))}
}

// Observe records that the td store moved. It is cheap and idempotent.
func (m *Model) Observe() {
	if m == nil || m.noteID == "" || m.requestGeneration == 0 {
		return
	}
	m.live.Observe()
}

// Refresh re-reads the note in place, preserving scroll. Nil when nothing is
// owed, a re-read is already running, a Load is in flight, or suppressed.
func (m *Model) Refresh(suppressed bool) tea.Cmd {
	if m == nil || m.noteID == "" {
		return nil
	}
	if m.requestGeneration == 0 || m.loading {
		return nil
	}
	if !m.live.Begin(suppressed) {
		return nil
	}

	if m.loader != nil {
		return m.refreshFrom(m.loader(m.workDir, m.noteID, m.epoch, m.revision))
	}

	m.requestGeneration++
	generation := m.requestGeneration
	modelID, epoch, noteID := m.modelID, m.epoch, m.noteID
	fetch := Fetch(m.workDir, noteID)
	return func() tea.Msg {
		msg, _ := fetch().(FetchedMsg)
		return LoadedMsg{
			ModelID: modelID, RequestGeneration: generation, Epoch: epoch,
			NoteID: noteID, Data: msg.Data, Error: msg.Error,
			Refresh: true,
		}
	}
}

func (m *Model) refreshFrom(load tea.Cmd) tea.Cmd {
	m.requestGeneration++
	generation := m.requestGeneration
	modelID, epoch, noteID := m.modelID, m.epoch, m.noteID
	return func() tea.Msg {
		msg := adoptNoteMsg(load, LoadedMsg{
			ModelID: modelID, RequestGeneration: generation, Epoch: epoch,
			NoteID: noteID, Refresh: true,
		})
		msg.Refresh = true
		return msg
	}
}

// RefreshPending reports whether a re-read is owed but has not started.
func (m *Model) RefreshPending() bool {
	if m == nil {
		return false
	}
	return m.live.Pending()
}

func (m *Model) applyRefresh(msg LoadedMsg) bool {
	stillOwed := m.live.Done()
	defer func() {
		if stillOwed {
			m.live.Observe()
		}
	}()

	if msg.NotModified {
		return false
	}
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
	m.clampScroll()
	return true
}

func fingerprintData(d *Data) string {
	if d == nil {
		return ""
	}
	return livewatch.Fingerprint(d)
}
