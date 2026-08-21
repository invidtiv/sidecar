package noteview

import (
	"testing"
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
