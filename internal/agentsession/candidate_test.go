package agentsession

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/adapter/antigravity"
	"github.com/marcus/sidecar/internal/adapter/opencode"
)

type candidateReader struct {
	sessions []adapter.Session
	err      error
}

func (r candidateReader) Sessions(string) ([]adapter.Session, error) { return r.sessions, r.err }

func TestCandidateFinderNearestDistinctTimeWinsAndNamesAlternatives(t *testing.T) {
	lost := time.Date(2026, 9, 4, 18, 31, 46, 0, time.UTC)
	finder := NewCandidateFinderWithReaders(map[string]SessionReader{"claude": candidateReader{sessions: []adapter.Session{
		{ID: "older", Name: "reviewer", UpdatedAt: lost.Add(-2 * time.Minute)},
		{ID: "nearer", Name: "implementer", UpdatedAt: lost.Add(-10 * time.Second)},
	}}})

	got, ok, err := finder.Find(CandidateQuery{WorkDir: "/work", AgentKind: "claude-code", CreatedAt: lost.Add(-time.Hour), ServerLostAt: lost})
	if err != nil || !ok {
		t.Fatalf("Find() = (%#v, %v, %v), want candidate", got, ok, err)
	}
	if got.Ref.Value != "nearer" || got.Ref.Reported || got.Confidence != CandidateLikely || got.Picker {
		t.Fatalf("candidate = %#v", got)
	}
	for _, want := range []string{"nearer", "older", "implementer", "reviewer"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name %q", got.Reason, want)
		}
	}
}

func TestCandidateFinderTieUsesPickerAndNamesBothIDs(t *testing.T) {
	lost := time.Now().Truncate(time.Second)
	updated := lost.Add(-time.Minute)
	finder := NewCandidateFinderWithReaders(map[string]SessionReader{"grok": candidateReader{sessions: []adapter.Session{
		{ID: "grok-a", Name: "first", UpdatedAt: updated},
		{ID: "grok-b", Name: "second", UpdatedAt: updated},
	}}})

	got, ok, err := finder.Find(CandidateQuery{WorkDir: "/work", AgentKind: "grok", CreatedAt: lost.Add(-time.Hour), ServerLostAt: lost})
	if err != nil || !ok {
		t.Fatalf("Find() = (%#v, %v, %v), want picker", got, ok, err)
	}
	if !got.Picker || got.Confidence != CandidateAmbiguous || got.Ref.Value != "" || got.Ref.Reported {
		t.Fatalf("candidate = %#v", got)
	}
	if !strings.Contains(got.Reason, "grok-a") || !strings.Contains(got.Reason, "grok-b") {
		t.Fatalf("reason does not name both ids: %q", got.Reason)
	}
}

func TestCandidateFinderAliveWindowClaimAndWeakOpenCodeEvidence(t *testing.T) {
	lost := time.Now().Truncate(time.Second)
	created := lost.Add(-time.Hour)
	finder := NewCandidateFinderWithReaders(map[string]SessionReader{"opencode": candidateReader{sessions: []adapter.Session{
		{ID: "before", UpdatedAt: created.Add(-time.Second)},
		{ID: "claimed", UpdatedAt: lost.Add(-time.Second)},
		{ID: "usable", UpdatedAt: lost.Add(-time.Minute)},
		{ID: "after", UpdatedAt: lost.Add(time.Second)},
	}}})

	got, ok, err := finder.Find(CandidateQuery{WorkDir: "/work", AgentKind: "opencode", CreatedAt: created, ServerLostAt: lost, Claimed: []Ref{{Kind: RefID, Value: "claimed"}}})
	if err != nil || !ok || got.Ref.Value != "usable" {
		t.Fatalf("Find() = (%#v, %v, %v), want usable", got, ok, err)
	}
	if !strings.Contains(got.Reason, "time_updated") || got.Ref.Reported {
		t.Fatalf("candidate evidence = %#v", got)
	}
}

func TestCandidateFinderUnsupportedInvalidAndReaderError(t *testing.T) {
	lost := time.Now()
	finder := NewCandidateFinderWithReaders(map[string]SessionReader{"claude": candidateReader{err: errors.New("broken")}})
	if _, ok, err := finder.Find(CandidateQuery{WorkDir: "/work", AgentKind: "unknown", CreatedAt: lost.Add(-time.Hour), ServerLostAt: lost}); err != nil || ok {
		t.Fatalf("unsupported provider = (%v, %v), want no candidate", ok, err)
	}
	if _, ok, err := finder.Find(CandidateQuery{WorkDir: "/work", AgentKind: "claude", CreatedAt: lost, ServerLostAt: lost.Add(-time.Hour)}); err != nil || ok {
		t.Fatalf("invalid window = (%v, %v), want no candidate", ok, err)
	}
	if _, _, err := finder.Find(CandidateQuery{WorkDir: "/work", AgentKind: "claude", CreatedAt: lost.Add(-time.Hour), ServerLostAt: lost}); err == nil {
		t.Fatal("reader error = nil")
	}
}

func TestCandidateFinderAntigravityUsesExactWorkspaceStoreEvidence(t *testing.T) {
	root := t.TempDir()
	wantDir := filepath.Join(root, "wanted")
	foreignDir := filepath.Join(root, "foreign")
	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	index, _ := json.Marshal(map[string]string{wantDir: "conv-wanted", foreignDir: "conv-foreign"})
	if err := os.WriteFile(filepath.Join(cacheDir, "last_conversations.json"), index, 0o644); err != nil {
		t.Fatal(err)
	}
	lost := time.Date(2026, 9, 4, 18, 31, 46, 0, time.UTC)
	history := strings.Join([]string{
		fmtJSON(t, map[string]any{"conversationId": "conv-foreign", "workspace": foreignDir, "timestamp": lost.Add(-time.Second).UnixMilli()}),
		fmtJSON(t, map[string]any{"conversationId": "conv-wanted", "workspace": wantDir, "timestamp": lost.Add(-time.Minute).UnixMilli()}),
	}, "\n") + "\n"
	historyPath := filepath.Join(root, "history.jsonl")
	if err := os.WriteFile(historyPath, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	finder := NewCandidateFinderWithReaders(map[string]SessionReader{"antigravity": antigravity.NewWithRecoveryPaths(cacheDir, historyPath)})
	got, ok, err := finder.Find(CandidateQuery{WorkDir: wantDir, AgentKind: "antigravity", CreatedAt: lost.Add(-time.Hour), ServerLostAt: lost})
	if err != nil || !ok || got.Ref.Value != "conv-wanted" {
		t.Fatalf("Find() = (%#v, %v, %v), want exact-workspace conversation", got, ok, err)
	}
}

func TestCandidateFinderOpenCodeRejectsForeignDirectoryAndSubagent(t *testing.T) {
	root := t.TempDir()
	wantDir := filepath.Join(root, "wanted")
	foreignDir := filepath.Join(root, "foreign")
	dbPath := filepath.Join(root, "opencode.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT, parent_id TEXT, directory TEXT, time_created INTEGER, time_updated INTEGER)`); err != nil {
		t.Fatal(err)
	}
	lost := time.Date(2026, 9, 4, 18, 31, 46, 0, time.UTC)
	rows := []struct {
		id, parent, dir string
		updated         time.Time
	}{
		{"foreign", "", foreignDir, lost.Add(-time.Second)},
		{"subagent", "parent", wantDir, lost.Add(-2 * time.Second)},
		{"wanted", "", wantDir, lost.Add(-time.Minute)},
	}
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO session VALUES (?, ?, ?, ?, ?, ?)`, row.id, row.id, row.parent, row.dir, lost.Add(-time.Hour).UnixMilli(), row.updated.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	finder := NewCandidateFinderWithReaders(map[string]SessionReader{"opencode": opencode.NewWithDBPath(dbPath)})
	got, ok, err := finder.Find(CandidateQuery{WorkDir: wantDir, AgentKind: "opencode", CreatedAt: lost.Add(-2 * time.Hour), ServerLostAt: lost})
	if err != nil || !ok || got.Ref.Value != "wanted" {
		t.Fatalf("Find() = (%#v, %v, %v), want exact-directory parent conversation", got, ok, err)
	}
}

func fmtJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
