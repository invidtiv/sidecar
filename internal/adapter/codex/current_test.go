package codex

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
)

func TestSessionsUsesReadOnlyStateIndex(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessionsDir, "2026", "08", "08", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionFile(rollout, []string{
		`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"thread-child","session_id":"thread-parent","cwd":"` + project + `","source":{"subagent":{"thread_spawn":{"parent_thread_id":"thread-parent"}}}}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:01Z","payload":{"type":"message","id":"indexed-user","role":"user","content":[{"type":"input_text","text":"indexed searchable needle"}]}}`,
	}); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "state_5.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, source TEXT NOT NULL, cwd TEXT NOT NULL, title TEXT NOT NULL, tokens_used INTEGER NOT NULL, archived INTEGER NOT NULL, first_user_message TEXT NOT NULL, thread_source TEXT, model TEXT, has_user_event INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC).Unix()
	_, err = db.Exec(`INSERT INTO threads VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, "thread-child", rollout, created, created+10, `{"subagent":"review"}`, project, "Indexed title", 42, 0, "First prompt", "subagent", "gpt-5", 0)
	if err != nil {
		t.Fatal(err)
	}
	emptyRollout := filepath.Join(sessionsDir, "empty.jsonl")
	if err := os.WriteFile(emptyRollout, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO threads VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, "empty-thread", emptyRollout, created, created, "cli", filepath.Join(root, "empty-project"), "", 0, 0, "", "user", "gpt-5", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	a := New()
	a.sessionsDir = sessionsDir
	a.stateDBPath = dbPath
	sessions, err := a.Sessions(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions=%d want 1", len(sessions))
	}
	s := sessions[0]
	if s.ID != "thread-child" || s.Name != "Indexed title" || !s.IsSubAgent || s.TotalTokens != 42 {
		t.Fatalf("unexpected session: %+v", s)
	}
	if s.MessageCount == 0 {
		t.Fatal("indexed user event was marked metadata-only")
	}
	matches, err := a.SearchMessages(s.ID, "searchable needle", adapter.DefaultSearchOptions())
	if err != nil || len(matches) != 1 {
		t.Fatalf("search matches=%+v err=%v", matches, err)
	}
	if s.Path != rollout || s.FileSize == 0 || s.AdapterID != adapterID {
		t.Fatalf("missing required fields: %+v", s)
	}
	resolved, err := a.SessionIDFromPath(rollout)
	if err != nil || resolved != "thread-child" {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	byID, err := a.SessionByID("thread-child")
	if err != nil || byID == nil || byID.Path != rollout {
		t.Fatalf("SessionByID=%+v err=%v", byID, err)
	}
	empty, err := a.SessionByID("empty-thread")
	if err != nil || empty == nil || empty.MessageCount != 0 {
		t.Fatalf("empty SessionByID=%+v err=%v", empty, err)
	}

	readOnly, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Exec(`DELETE FROM threads`); err == nil {
		t.Fatal("state database unexpectedly writable through read-only handle")
	}
	_ = readOnly.Close()
}

func TestSessionsStateIndexFiltersArchivedMissingAndOtherProject(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	project := filepath.Join(root, "project")
	linked := filepath.Join(root, "linked-worktree")
	other := filepath.Join(root, "other")
	for _, dir := range []string{project, linked, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(root, "state_5.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, source TEXT NOT NULL, cwd TEXT NOT NULL, title TEXT NOT NULL, tokens_used INTEGER NOT NULL, archived INTEGER NOT NULL, first_user_message TEXT NOT NULL, thread_source TEXT, model TEXT, has_user_event INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(id, cwd string, archived int, exists bool) {
		t.Helper()
		path := filepath.Join(sessionsDir, id+".jsonl")
		if exists {
			if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := writeSessionFile(path, []string{`{"type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `"}}`}); err != nil {
				t.Fatal(err)
			}
		}
		_, err := db.Exec(`INSERT INTO threads VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, path, 1, 2, "cli", cwd, id, 0, archived, "prompt", "user", "gpt", 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("main", project, 0, true)
	insert("archived", project, 1, true)
	insert("deleted", project, 0, false)
	insert("other", other, 0, true)
	insert("linked", linked, 0, true)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = sessionsDir
	a.stateDBPath = dbPath
	main, err := a.Sessions(project)
	if err != nil || len(main) != 1 || main[0].ID != "main" {
		t.Fatalf("main=%+v err=%v", main, err)
	}
	linkedSessions, err := a.Sessions(linked)
	if err != nil || len(linkedSessions) != 1 || linkedSessions[0].ID != "linked" {
		t.Fatalf("linked=%+v err=%v", linkedSessions, err)
	}
	if got := a.sessionFilePath("main"); got == "" {
		t.Fatal("later worktree query evicted earlier indexed session")
	}
}

func TestCodexCachesAreBounded(t *testing.T) {
	a := New()
	paths := make(map[string]string, sessionIndexMaxEntries+500)
	for i := 0; i < sessionIndexMaxEntries+500; i++ {
		paths[fmt.Sprintf("id-%d", i)] = fmt.Sprintf("/tmp/%d", i)
	}
	a.cacheSessionPaths(paths)
	for i := 0; i < totalUsageMaxEntries+50; i++ {
		a.cacheTotalUsage(fmt.Sprintf("usage-%d", i), &TokenUsage{InputTokens: i})
	}
	a.mu.RLock()
	indexLen, usageLen := len(a.sessionIndex), len(a.totalUsageCache)
	a.mu.RUnlock()
	if indexLen > sessionIndexMaxEntries || usageLen > totalUsageMaxEntries {
		t.Fatalf("unbounded caches index=%d usage=%d", indexLen, usageLen)
	}
}

func TestSubagentSourceClassificationMatrix(t *testing.T) {
	tests := []struct {
		name, source, threadSource string
		want                       bool
	}{
		{"object only", `{"subagent":{"other":"guardian"}}`, "", true},
		{"object thread spawn", `{"subagent":{"thread_spawn":{"parent_thread_id":"parent"}}}`, "", true},
		{"plain root", `"cli"`, "", false},
		{"thread source only", `"vscode"`, "subagent", true},
		{"unrelated object", `{"user":"direct"}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSubagentSource(tt.source, tt.threadSource); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestRepeatedStateIndexCallsDoNotLeakFDs(t *testing.T) {
	before, err := openFDCount()
	if err != nil {
		t.Skipf("FD count unavailable: %v", err)
	}
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessionsDir, "rollout.jsonl")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "state_5.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, source TEXT NOT NULL, cwd TEXT NOT NULL, title TEXT NOT NULL, tokens_used INTEGER NOT NULL, archived INTEGER NOT NULL, first_user_message TEXT NOT NULL, thread_source TEXT, model TEXT, has_user_event INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO threads VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "fd-thread", rollout, 1, 2, "cli", project, "FD", 0, 0, "prompt", "user", "gpt", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = sessionsDir
	a.stateDBPath = dbPath
	for i := 0; i < 100; i++ {
		if _, err := a.Sessions(project); err != nil {
			t.Fatal(err)
		}
		if _, err := a.SessionByID("fd-thread"); err != nil {
			t.Fatal(err)
		}
	}
	after, err := openFDCount()
	if err != nil {
		t.Fatal(err)
	}
	if delta := after - before; delta > 3 {
		t.Fatalf("adapter calls leaked file descriptors: before=%d after=%d delta=%d", before, after, delta)
	}
}

func openFDCount() (int, error) {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func TestWatcherEmitsStableSessionIDAndCloses(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	day := filepath.Join(root, now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = root
	a.stateDBPath = ""
	events, closer, err := a.Watch("/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-not-the-id.jsonl")
	if err := writeSessionFile(path, []string{`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"stable-thread-id","cwd":"/tmp/project"}}`}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.SessionID != "stable-thread-id" || (event.Type != adapter.EventSessionCreated && event.Type != adapter.EventMessageAdded) {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watcher event")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("events channel remained open")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not close")
	}
}

func TestWatcherDiscoversNewMonthUnderExistingYear(t *testing.T) {
	now := time.Now()
	next := now.AddDate(0, 1, 0)
	if next.Year() != now.Year() {
		t.Skip("month rollover crosses year")
	}
	root := t.TempDir()
	year := filepath.Join(root, now.Format("2006"))
	if err := os.MkdirAll(year, 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = root
	a.stateDBPath = ""
	events, closer, err := a.Watch("/tmp/project")
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	month := filepath.Join(year, next.Format("01"))
	if err := os.Mkdir(month, 0o755); err != nil {
		t.Fatal(err)
	}
	day := filepath.Join(month, "01")
	if err := os.Mkdir(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout.jsonl")
	if err := writeSessionFile(path, []string{`{"type":"session_meta","timestamp":"2026-09-01T00:00:00Z","payload":{"id":"rollover-thread","cwd":"/tmp/project"}}`}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.SessionID != "rollover-thread" {
			t.Fatalf("event=%+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new month event missed")
	}
}

func TestSessionsStateSchemaDriftFallsBackToJSONL(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessionsDir, "2026", "08", "08", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionFile(rollout, []string{`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"fallback-id","cwd":"` + project + `","source":"cli"}}`}); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "state_5.sqlite")
	db, _ := sql.Open("sqlite3", dbPath)
	if _, err := db.Exec(`CREATE TABLE unrelated (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	a := New()
	a.sessionsDir = sessionsDir
	a.stateDBPath = dbPath
	sessions, err := a.Sessions(project)
	if err != nil || len(sessions) != 1 || sessions[0].ID != "fallback-id" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}

func TestSessionsLockedStateDBFallsBackPromptly(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessionsDir, "2026", "08", "08", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionFile(rollout, []string{`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"locked-fallback","cwd":"` + project + `"}}`}); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "state_5.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, source TEXT NOT NULL, cwd TEXT NOT NULL, title TEXT NOT NULL, tokens_used INTEGER NOT NULL, archived INTEGER NOT NULL, first_user_message TEXT NOT NULL, thread_source TEXT, model TEXT, has_user_event INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`ROLLBACK`); _ = db.Close() }()
	a := New()
	a.sessionsDir = sessionsDir
	a.stateDBPath = dbPath
	started := time.Now()
	sessions, err := a.Sessions(project)
	elapsed := time.Since(started)
	if err != nil || len(sessions) != 1 || sessions[0].ID != "locked-fallback" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("locked fallback took %s", elapsed)
	}
}

func TestCurrentMessageFidelityAndDefensiveCache(t *testing.T) {
	root := t.TempDir()
	id := "thread-current"
	path := filepath.Join(root, "rollout.jsonl")
	lines := []string{
		`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"thread-current","session_id":"parent-thread","cwd":"/tmp","source":"cli"}}`,
		`{"type":"turn_context","timestamp":"2026-08-08T12:00:01Z","payload":{"model":"gpt-5.6"}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:02Z","payload":{"type":"message","id":"developer-id","role":"developer","content":[{"type":"input_text","text":"private instructions"}]}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:03Z","payload":{"type":"message","id":"user-stable","role":"user","content":[{"type":"input_text","text":"Please inspect"}]}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:04Z","payload":{"type":"reasoning","id":"reason-1","summary":[{"type":"summary_text","text":"Check the seam"}]}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:05Z","payload":{"type":"function_call","id":"item-call","call_id":"call-1","name":"inspect","arguments":"{\"path\":\"safe.txt\"}"}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:06Z","payload":{"type":"function_call_output","id":"item-output","call_id":"call-1","output":"ok"}}`,
		`{"type":"event_msg","timestamp":"2026-08-08T12:00:07Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":3,"cache_write_input_tokens":2,"output_tokens":4,"reasoning_output_tokens":1,"total_tokens":15},"total_token_usage":{"input_tokens":10,"cached_input_tokens":3,"cache_write_input_tokens":2,"output_tokens":4,"reasoning_output_tokens":1,"total_tokens":15}}}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:08Z","payload":{"type":"message","id":"assistant-stable","role":"assistant","content":[{"type":"output_text","text":"Finished"}]}}`,
	}
	if err := writeSessionFile(path, lines); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = root
	a.sessionIndex = map[string]string{id: path}
	msgs, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages=%d want 2: %+v", len(msgs), msgs)
	}
	if msgs[0].ID != "user-stable" || msgs[1].ID != "assistant-stable" || msgs[1].Model != "gpt-5.6" {
		t.Fatalf("identity/model mismatch: %+v", msgs)
	}
	if len(msgs[1].ContentBlocks) != 4 {
		t.Fatalf("blocks=%+v", msgs[1].ContentBlocks)
	}
	wantTypes := []string{"thinking", "tool_use", "tool_result", "text"}
	for i, want := range wantTypes {
		if msgs[1].ContentBlocks[i].Type != want {
			t.Fatalf("block %d=%q want %q", i, msgs[1].ContentBlocks[i].Type, want)
		}
	}
	if msgs[1].ContentBlocks[1].ToolUseID != "call-1" || msgs[1].ContentBlocks[2].ToolUseID != "call-1" || msgs[1].ToolUses[0].Output != "ok" {
		t.Fatalf("tool linkage mismatch: %+v", msgs[1])
	}
	if msgs[1].InputTokens != 10 || msgs[1].OutputTokens != 5 || msgs[1].CacheRead != 3 || msgs[1].CacheWrite != 2 {
		t.Fatalf("usage=%+v", msgs[1].TokenUsage)
	}
	msgs[1].ContentBlocks[0].Text = "mutated"
	msgs[1].ToolUses[0].Output = "mutated"
	again, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if again[1].ContentBlocks[0].Text == "mutated" || again[1].ToolUses[0].Output == "mutated" {
		t.Fatal("cache returned aliased nested state")
	}
}

func TestToolOutputStringCurrentArrayEncoding(t *testing.T) {
	raw := []byte(`[{"type":"input_text","text":"first"},{"type":"input_text","text":"second"},{"type":"input_image","image_url":"data:image/png;base64,private"}]`)
	if got := toolOutputString(raw); got != "first\nsecond\n[image]" {
		t.Fatalf("output=%q", got)
	}
}

func TestUsagePrefersFinalCumulativeTotals(t *testing.T) {
	root := t.TempDir()
	id := "usage-current"
	path := filepath.Join(root, "rollout.jsonl")
	lines := []string{
		`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"usage-current","cwd":"/tmp"}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:01Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one"}]}}`,
		`{"type":"event_msg","timestamp":"2026-08-08T12:00:02Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:03Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"two"}]}}`,
		`{"type":"event_msg","timestamp":"2026-08-08T12:00:04Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"cached_input_tokens":5,"cache_write_input_tokens":7,"output_tokens":3,"reasoning_output_tokens":1,"total_tokens":24},"total_token_usage":{"input_tokens":30,"cached_input_tokens":5,"cache_write_input_tokens":7,"output_tokens":5,"reasoning_output_tokens":1,"total_tokens":36}}}}`,
	}
	if err := writeSessionFile(path, lines); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = root
	a.sessionIndex = map[string]string{id: path}
	usage, err := a.Usage(id)
	if err != nil {
		t.Fatal(err)
	}
	if usage.TotalInputTokens != 30 || usage.TotalOutputTokens != 6 || usage.TotalCacheRead != 5 || usage.TotalCacheWrite != 7 {
		t.Fatalf("usage=%+v", usage)
	}
	// Keep the transcript hot in msgCache while evicting its independently
	// bounded cumulative usage entry. The exact cache-hit path must restore it.
	for i := 0; i < totalUsageMaxEntries+10; i++ {
		a.cacheTotalUsage(fmt.Sprintf("other-%d", i), &TokenUsage{InputTokens: i})
	}
	a.mu.Lock()
	delete(a.totalUsageCache, id)
	a.mu.Unlock()
	again, err := a.Usage(id)
	if err != nil {
		t.Fatal(err)
	}
	if again.TotalInputTokens != 30 || again.TotalOutputTokens != 6 || again.TotalCacheRead != 5 || again.TotalCacheWrite != 7 {
		t.Fatalf("usage after eviction=%+v", again)
	}
}

func TestIncrementalToolBlocksStablePendingIDAndShrink(t *testing.T) {
	root := t.TempDir()
	id := "tool-current"
	path := filepath.Join(root, "rollout.jsonl")
	initial := []string{
		`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"tool-current","cwd":"/tmp"}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:01Z","payload":{"type":"message","id":"u1","role":"user","content":[{"type":"input_text","text":"run"}]}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:02Z","payload":{"type":"custom_tool_call","call_id":"call-stable","name":"patch","input":"safe"}}`,
	}
	if err := writeSessionFile(path, initial); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = root
	a.sessionIndex = map[string]string{id: path}
	first, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	pendingID := first[1].ID
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"response_item","timestamp":"2026-08-08T12:00:03Z","payload":{"type":"message","id":"u2","role":"user","content":[{"type":"input_text","text":"continue"}]}}` + "\n" + strings.Repeat(" ", 2048) + "\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if second[1].ID != pendingID {
		t.Fatalf("pending ID changed %q -> %q", pendingID, second[1].ID)
	}
	replacement := []string{
		`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"tool-current","cwd":"/tmp"}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:01Z","payload":{"type":"message","id":"u-new","role":"user","content":[{"type":"input_text","text":"replacement needle"}]}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:02Z","payload":{"type":"function_call","call_id":"call-new","name":"inspect","arguments":"{}"}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:03Z","payload":{"type":"function_call_output","call_id":"call-new","output":"new output"}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:04Z","payload":{"type":"message","id":"a-new","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
	}
	if err := writeSessionFile(path, replacement); err != nil {
		t.Fatal(err)
	}
	shrunk, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(shrunk) != 2 || shrunk[0].ID != "u-new" {
		t.Fatalf("shrink did not reparse: %+v", shrunk)
	}
	blocks := shrunk[1].ContentBlocks
	if len(blocks) != 3 || blocks[0].ToolUseID != "call-new" || blocks[1].ToolUseID != "call-new" {
		t.Fatalf("linked blocks=%+v", blocks)
	}
	matches, err := a.SearchMessages(id, "replacement needle", adapter.DefaultSearchOptions())
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%+v err=%v", matches, err)
	}
}

func TestCodexBootstrapBlocksFilteredNarrowly(t *testing.T) {
	root := t.TempDir()
	id := "bootstrap-current"
	path := filepath.Join(root, "rollout.jsonl")
	agents := `# AGENTS.md instructions for /tmp/project

<INSTRUCTIONS>
secret-bootstrap directive
</INSTRUCTIONS>`
	environment := `<environment_context><cwd>/tmp/project</cwd><shell>zsh</shell></environment_context>`
	lines := []string{
		`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"bootstrap-current","cwd":"/tmp/project"}}`,
		fmt.Sprintf(`{"type":"response_item","timestamp":"2026-08-08T12:00:01Z","payload":{"type":"message","id":"bootstrap-only","role":"user","content":[{"type":"input_text","text":%q},{"type":"input_text","text":%q}]}}`, agents, environment),
		fmt.Sprintf(`{"type":"response_item","timestamp":"2026-08-08T12:00:02Z","payload":{"type":"message","id":"mixed","role":"user","content":[{"type":"input_text","text":%q},{"type":"input_text","text":"actual human prompt"}]}}`, environment),
		`{"type":"response_item","timestamp":"2026-08-08T12:00:03Z","payload":{"type":"message","id":"discussion","role":"user","content":[{"type":"input_text","text":"Please explain how AGENTS.md instructions work"}]}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:04Z","payload":{"type":"message","id":"normal-multi","role":"user","content":[{"type":"input_text","text":"first normal block"},{"type":"input_text","text":"second normal block"}]}}`,
	}
	if err := writeSessionFile(path, lines); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = root
	a.sessionIndex = map[string]string{id: path}
	msgs, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages=%+v", msgs)
	}
	if msgs[0].ID != "mixed" || msgs[0].Content != "actual human prompt" || len(msgs[0].ContentBlocks) != 1 || msgs[0].ContentBlocks[0].Text != "actual human prompt" {
		t.Fatalf("mixed message=%+v", msgs[0])
	}
	if msgs[1].ID != "discussion" || !strings.Contains(msgs[1].Content, "AGENTS.md") {
		t.Fatalf("genuine discussion removed: %+v", msgs[1])
	}
	if msgs[2].Content != "first normal block\nsecond normal block" || len(msgs[2].ContentBlocks) != 2 {
		t.Fatalf("normal multi-block changed: %+v", msgs[2])
	}
	hidden, err := a.SearchMessages(id, "secret-bootstrap", adapter.DefaultSearchOptions())
	if err != nil || len(hidden) != 0 {
		t.Fatalf("bootstrap searchable: %+v err=%v", hidden, err)
	}
	visible, err := a.SearchMessages(id, "actual human prompt", adapter.DefaultSearchOptions())
	if err != nil || len(visible) != 1 {
		t.Fatalf("human prompt search=%+v err=%v", visible, err)
	}
	meta, err := a.parseSessionMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.FirstUserMessage != "actual human prompt" || meta.MsgCount != 3 {
		t.Fatalf("metadata=%+v", meta)
	}
}

func TestCodexBootstrapOnlyThenIncrementalHumanPrompt(t *testing.T) {
	root := t.TempDir()
	id := "bootstrap-incremental"
	path := filepath.Join(root, "rollout.jsonl")
	initial := []string{
		`{"type":"session_meta","timestamp":"2026-08-08T12:00:00Z","payload":{"id":"bootstrap-incremental","cwd":"/tmp"}}`,
		`{"type":"response_item","timestamp":"2026-08-08T12:00:01Z","payload":{"type":"message","id":"bootstrap","role":"user","content":[{"type":"input_text","text":"<environment_context><cwd>/tmp</cwd></environment_context>"}]}}`,
	}
	if err := writeSessionFile(path, initial); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.sessionsDir = root
	a.sessionIndex = map[string]string{id: path}
	msgs, err := a.Messages(id)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("initial=%+v err=%v", msgs, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"response_item","timestamp":"2026-08-08T12:00:02Z","payload":{"type":"message","id":"human","role":"user","content":[{"type":"input_text","text":"arrived later"}]}}` + "\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	msgs, err = a.Messages(id)
	if err != nil || len(msgs) != 1 || msgs[0].ID != "human" || msgs[0].Content != "arrived later" {
		t.Fatalf("incremental=%+v err=%v", msgs, err)
	}
}
