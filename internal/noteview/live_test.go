package noteview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func loadedCard(t *testing.T, data *Data) *Model {
	t.Helper()
	m := New(nil)
	m.SetSize(80, 20)
	_ = m.Load(1, t.TempDir(), data.ID, 7)
	if !m.SetResult(LoadedMsg{
		ModelID: 1, RequestGeneration: m.requestGeneration, Epoch: 7,
		NoteID: data.ID, Data: data,
	}) {
		t.Fatal("SetResult() = false for the initial load")
	}
	return m
}

func TestRefreshIsNotOfferedUntilTheStoreMoves(t *testing.T) {
	m := loadedCard(t, &Data{ID: "nt-abc123", Title: "T", Content: "body"})
	if cmd := m.Refresh(false); cmd != nil {
		t.Fatal("Refresh() returned a command with no change observed")
	}
}

func TestRefreshIsOfferedOnceTheStoreMoves(t *testing.T) {
	m := loadedCard(t, &Data{ID: "nt-abc123", Title: "T", Content: "body"})
	m.Observe()
	if cmd := m.Refresh(false); cmd == nil {
		t.Fatal("Refresh() = nil after the store moved")
	}
}

func TestNotModifiedRefreshDoesNotReplaceContent(t *testing.T) {
	m := loadedCard(t, &Data{ID: "nt-abc123", Title: "T", Content: "body"})
	m.SetLoader(func(string, string, uint64, string) tea.Cmd {
		return func() tea.Msg {
			return NotModified{NoteID: m.noteID, Epoch: m.epoch, Revision: "r2"}
		}
	})
	m.Observe()
	cmd := m.Refresh(false)
	if cmd == nil {
		t.Fatal("Refresh() = nil after Observe")
	}
	msg, ok := cmd().(LoadedMsg)
	if !ok {
		t.Fatalf("Refresh returned %T", cmd())
	}
	if m.SetResult(msg) {
		t.Fatal("NotModified refresh replaced content")
	}
	if m.Data() == nil || m.Data().Content != "body" {
		t.Fatal("NotModified refresh dropped the note on screen")
	}
}

func TestRefreshSendsLastAdoptedRevision(t *testing.T) {
	m := New(nil)
	var lastIf string
	m.SetLoader(func(_, id string, epoch uint64, ifRevision string) tea.Cmd {
		lastIf = ifRevision
		return func() tea.Msg {
			return LoadedMsg{
				NoteID: id, Epoch: epoch, Revision: "rev-1",
				Data: &Data{ID: id, Title: "T", Content: "body"},
			}
		}
	})
	cmd := m.Load(1, t.TempDir(), "nt-abc123", 7)
	msg := cmd().(LoadedMsg)
	if !m.SetResult(msg) {
		t.Fatal("SetResult() = false for the initial load")
	}
	if lastIf != "" {
		t.Fatalf("first load IfRevision = %q, want empty", lastIf)
	}
	m.Observe()
	refresh := m.Refresh(false)
	if refresh == nil {
		t.Fatal("Refresh() = nil after Observe")
	}
	_ = refresh()
	if lastIf != "rev-1" {
		t.Fatalf("refresh IfRevision = %q, want rev-1", lastIf)
	}
}

func TestRefreshPreservesScrollWhenUnchanged(t *testing.T) {
	m := loadedCard(t, &Data{ID: "nt-abc123", Title: "T", Content: stringsRepeat("line\n", 40)})
	m.Scroll(8)
	before := m.ScrollOffset()
	m.Observe()
	changed := m.SetResult(LoadedMsg{
		ModelID: m.modelID, RequestGeneration: m.requestGeneration, Epoch: m.epoch,
		NoteID: m.noteID, Data: m.data, Refresh: true,
	})
	if changed {
		t.Fatal("unchanged refresh reported a paint")
	}
	if m.ScrollOffset() != before {
		t.Fatalf("scroll moved from %d to %d", before, m.ScrollOffset())
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
