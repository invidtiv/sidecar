package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
)

func TestAdapterInterface(t *testing.T) {
	a := NewWithSessionsDir(t.TempDir())
	var _ adapter.Adapter = a
	var _ adapter.WatchScopeProvider = a
	var _ adapter.TargetedRefresher = a
	var _ adapter.ProjectDiscoveryWatcher = a
	var _ adapter.SessionPathResolver = a
	var _ adapter.ProjectDiscoverer = a

	if a.ID() != "grok" {
		t.Errorf("ID = %q, want grok", a.ID())
	}
	if a.Name() != "Grok" {
		t.Errorf("Name = %q, want Grok", a.Name())
	}
	if a.Icon() != "✦" {
		t.Errorf("Icon = %q, want ✦", a.Icon())
	}
	caps := a.Capabilities()
	if !caps[adapter.CapSessions] || !caps[adapter.CapMessages] || !caps[adapter.CapWatch] {
		t.Errorf("missing capabilities: %+v", caps)
	}
	if caps[adapter.CapUsage] {
		t.Error("usage should be unsupported")
	}
	if a.WatchScope() != adapter.WatchScopeGlobal {
		t.Error("want global watch scope")
	}
	if !a.WatchForProjectDiscovery() {
		t.Error("want project discovery watcher")
	}
}

func TestEncodeDecodeProjectKey(t *testing.T) {
	paths := []string{
		"/Users/marcus/code/sidecar",
		"/home/user/project",
		"/tmp/foo bar",
		"/private/tmp/x",
	}
	for _, p := range paths {
		enc := encodeProjectKey(p)
		if strings.Contains(enc, "/") {
			t.Errorf("encoded key still has slash: %q", enc)
		}
		if !strings.HasPrefix(enc, "%2F") && strings.HasPrefix(p, "/") {
			t.Errorf("expected leading %%2F for %q, got %q", p, enc)
		}
		dec, err := decodeProjectKey(enc)
		if err != nil {
			t.Fatalf("decode %q: %v", enc, err)
		}
		if dec != p {
			t.Errorf("roundtrip %q -> %q -> %q", p, enc, dec)
		}
	}
	// Match known Grok layout
	got := encodeProjectKey("/Users/marcus/code/sidecar")
	want := "%2FUsers%2Fmarcus%2Fcode%2Fsidecar"
	if got != want {
		t.Errorf("encode = %q, want %q", got, want)
	}
}

func setupFixtureSessions(t *testing.T) (*Adapter, string) {
	t.Helper()
	root := t.TempDir()
	projectRoot := "/home/user/project"
	projDir := filepath.Join(root, encodeProjectKey(projectRoot))

	// Main session
	mainID := "019f0000-aaaa-7000-8000-000000000001"
	mainDir := filepath.Join(projDir, mainID)
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "testdata/summary_main.json", filepath.Join(mainDir, "summary.json"))
	copyFile(t, "testdata/valid_chat.jsonl", filepath.Join(mainDir, "chat_history.jsonl"))

	// Subagent session
	subID := "019f0000-bbbb-7000-8000-000000000002"
	subDir := filepath.Join(projDir, subID)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "testdata/summary_sub.json", filepath.Join(subDir, "summary.json"))
	copyFile(t, "testdata/tool_linking.jsonl", filepath.Join(subDir, "chat_history.jsonl"))

	// Other project (must not leak)
	other := filepath.Join(root, encodeProjectKey("/home/user/other"))
	otherDir := filepath.Join(other, "other-session")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "testdata/summary_main.json", filepath.Join(otherDir, "summary.json"))
	copyFile(t, "testdata/valid_chat.jsonl", filepath.Join(otherDir, "chat_history.jsonl"))

	return NewWithSessionsDir(root), projectRoot
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func TestDetectAndSessions(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)

	found, err := a.Detect(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected Detect true")
	}

	found, err = a.Detect("/home/user/missing")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("expected Detect false for missing project")
	}

	// Relative vs absolute: resolve to same sessions when cwd matches.
	// Use the encoded absolute path only — relative roots depend on process cwd.
	sessions, err := a.Sessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	// Sorted by UpdatedAt desc: main (10:30) before sub (10:20)
	if sessions[0].ID != "019f0000-aaaa-7000-8000-000000000001" {
		t.Errorf("first session = %s", sessions[0].ID)
	}
	if sessions[0].Name != "Fix the login bug" {
		t.Errorf("name = %q", sessions[0].Name)
	}
	if sessions[0].AdapterID != "grok" || sessions[0].AdapterName != "Grok" || sessions[0].AdapterIcon != "✦" {
		t.Errorf("identity fields incomplete: %+v", sessions[0])
	}
	if sessions[0].FileSize == 0 {
		t.Error("FileSize should be set")
	}
	if sessions[0].Path == "" || !strings.HasSuffix(sessions[0].Path, "chat_history.jsonl") {
		t.Errorf("Path = %q", sessions[0].Path)
	}
	if sessions[0].CreatedAt.IsZero() || sessions[0].UpdatedAt.IsZero() {
		t.Error("timestamps required")
	}
	if sessions[0].IsSubAgent {
		t.Error("main session should not be subagent")
	}
	if !sessions[1].IsSubAgent {
		t.Error("sub session should be subagent")
	}
	// Must not include other project
	for _, s := range sessions {
		if s.ID == "other-session" {
			t.Error("leaked other project session")
		}
	}
}

func TestSessionsMissingDir(t *testing.T) {
	a := NewWithSessionsDir(filepath.Join(t.TempDir(), "nope"))
	sessions, err := a.Sessions("/home/user/project")
	if err != nil {
		t.Fatal(err)
	}
	if sessions != nil && len(sessions) != 0 {
		t.Fatalf("want empty, got %d", len(sessions))
	}
	found, err := a.Detect("/home/user/project")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("detect should be false")
	}
}

func TestMessagesAndToolLinking(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)
	if _, err := a.Sessions(projectRoot); err != nil {
		t.Fatal(err)
	}

	msgs, err := a.Messages("019f0000-aaaa-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected messages")
	}
	// system + harness-only user turns skipped; first user is cleaned <user_query>
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i] = m.Role
	}
	if roles[0] != "user" {
		t.Fatalf("first role = %s, want user; roles=%v", roles[0], roles)
	}
	if msgs[0].Content != "Please fix the login bug in auth.go" {
		t.Fatalf("cleaned user content = %q", msgs[0].Content)
	}
	for _, m := range msgs {
		if m.Role == "user" && (strings.Contains(m.Content, "<system-reminder>") || strings.Contains(m.Content, "<user_info>")) {
			t.Fatalf("harness XML leaked into user content: %q", m.Content)
		}
	}
	// Find assistant with tool use
	var foundTool bool
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		if len(m.ToolUses) > 0 {
			foundTool = true
			if m.ToolUses[0].Name != "read_file" {
				t.Errorf("tool name = %q", m.ToolUses[0].Name)
			}
			if m.ToolUses[0].Output == "" {
				t.Error("tool output should be linked")
			}
			// ContentBlocks tool_use/result parity
			var hasUse bool
			for _, cb := range m.ContentBlocks {
				if cb.Type == "tool_use" && cb.ToolUseID == m.ToolUses[0].ID {
					hasUse = true
					if cb.ToolOutput == "" {
						t.Error("content block tool output missing")
					}
				}
				if cb.Type == "thinking" && cb.Text == "" {
					t.Error("empty thinking block")
				}
			}
			if !hasUse {
				t.Error("missing tool_use content block")
			}
			if len(m.ThinkingBlocks) == 0 {
				t.Error("expected thinking blocks on first assistant")
			}
		}
		if m.Model != "" && !strings.HasPrefix(m.Model, "grok") {
			t.Errorf("unexpected model %q", m.Model)
		}
	}
	if !foundTool {
		t.Error("expected a tool-using assistant message")
	}

	// Multi-tool linking
	toolMsgs, err := a.Messages("019f0000-bbbb-7000-8000-000000000002")
	if err != nil {
		t.Fatal(err)
	}
	var multi *adapter.Message
	for i := range toolMsgs {
		if len(toolMsgs[i].ToolUses) == 2 {
			multi = &toolMsgs[i]
			break
		}
	}
	if multi == nil {
		t.Fatal("expected multi-tool assistant message")
	}
	for _, tu := range multi.ToolUses {
		if tu.Output == "" {
			t.Errorf("tool %s missing linked output", tu.ID)
		}
	}
}

func TestMessagesCacheHitAndIncremental(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)
	if _, err := a.Sessions(projectRoot); err != nil {
		t.Fatal(err)
	}
	id := "019f0000-aaaa-7000-8000-000000000001"

	m1, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(m1) != len(m2) {
		t.Fatalf("cache hit length mismatch %d vs %d", len(m1), len(m2))
	}
	// Mutating returned slice must not affect cache
	if len(m2) > 0 {
		m2[0].Content = "MUTATED"
	}
	m3, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if m3[0].Content == "MUTATED" {
		t.Fatal("cache returned shared slice")
	}

	// Append a new user message (incremental)
	sessionDir := a.sessionDirPath(id)
	chatPath := filepath.Join(sessionDir, "chat_history.jsonl")
	f, err := os.OpenFile(chatPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","content":[{"type":"text","text":"appended later"}]}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	// Ensure mtime changes on coarse FS
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(chatPath, future, future)

	m4, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(m4) <= len(m1) {
		t.Fatalf("incremental should grow messages: before=%d after=%d", len(m1), len(m4))
	}
	last := m4[len(m4)-1]
	if last.Role != "user" || last.Content != "appended later" {
		t.Errorf("last message = %+v", last)
	}
}

func TestMessagesShrinkFallsBackToFullParse(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)
	if _, err := a.Sessions(projectRoot); err != nil {
		t.Fatal(err)
	}
	id := "019f0000-aaaa-7000-8000-000000000001"
	if _, err := a.Messages(id); err != nil {
		t.Fatal(err)
	}

	sessionDir := a.sessionDirPath(id)
	chatPath := filepath.Join(sessionDir, "chat_history.jsonl")
	// Shrink / rotate
	if err := os.WriteFile(chatPath, []byte(`{"type":"user","content":[{"type":"text","text":"only"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(chatPath, past, past)

	msgs, err := a.Messages(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "only" {
		t.Fatalf("after shrink got %+v", msgs)
	}
}

func TestMalformedAndEmptyChat(t *testing.T) {
	root := t.TempDir()
	projectRoot := "/home/user/project"
	projDir := filepath.Join(root, encodeProjectKey(projectRoot))

	emptyID := "empty-session"
	emptyDir := filepath.Join(projDir, emptyID)
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Empty chat + zero summary counts → skipped from Sessions
	sum := `{"info":{"id":"empty-session","cwd":"/home/user/project"},"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z","num_messages":0,"num_chat_messages":0}`
	if err := os.WriteFile(filepath.Join(emptyDir, "summary.json"), []byte(sum), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emptyDir, "chat_history.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	malID := "malformed-session"
	malDir := filepath.Join(projDir, malID)
	if err := os.MkdirAll(malDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum2 := `{"info":{"id":"malformed-session","cwd":"/home/user/project"},"session_summary":"x","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z","num_messages":1,"num_chat_messages":1}`
	if err := os.WriteFile(filepath.Join(malDir, "summary.json"), []byte(sum2), 0o644); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "testdata/malformed.jsonl", filepath.Join(malDir, "chat_history.jsonl"))

	a := NewWithSessionsDir(root)
	sessions, err := a.Sessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.ID == emptyID {
			t.Error("empty session should be skipped")
		}
	}
	msgs, err := a.Messages(malID)
	if err != nil {
		t.Fatal(err)
	}
	// One valid user line after malformed
	if len(msgs) != 1 || msgs[0].Content != "ok" {
		t.Fatalf("malformed parse = %+v", msgs)
	}
}

func TestSessionByIDAndPathResolver(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)
	if _, err := a.Sessions(projectRoot); err != nil {
		t.Fatal(err)
	}
	id := "019f0000-aaaa-7000-8000-000000000001"
	s, err := a.SessionByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.ID != id {
		t.Fatalf("SessionByID = %+v", s)
	}
	resolved, err := a.SessionIDFromPath(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != id {
		t.Errorf("SessionIDFromPath = %q", resolved)
	}
}

func TestDiscoverRelatedProjectDirs(t *testing.T) {
	root := t.TempDir()
	main := "/home/user/myrepo"
	wt := "/home/user/myrepo-feature"
	other := "/home/user/unrelated"
	for _, p := range []string{main, wt, other} {
		if err := os.MkdirAll(filepath.Join(root, encodeProjectKey(p), "sess"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := NewWithSessionsDir(root)
	related, err := a.DiscoverRelatedProjectDirs(main)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range related {
		if r == wt {
			found = true
		}
		if r == other {
			t.Error("unrelated project leaked")
		}
		if r == main {
			t.Error("main itself should not be related")
		}
	}
	if !found {
		t.Fatalf("expected worktree %q in %v", wt, related)
	}
}

func TestWatcherEmitsSessionID(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)
	// Ensure sessions dir is watched (exists)
	ch, closer, err := a.Watch(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	id := "019f0000-aaaa-7000-8000-000000000001"
	sessionDir := a.sessionDirPath(id)
	if sessionDir == "" {
		// populate index
		if _, err := a.Sessions(projectRoot); err != nil {
			t.Fatal(err)
		}
		sessionDir = a.sessionDirPath(id)
	}
	chatPath := filepath.Join(sessionDir, "chat_history.jsonl")
	f, err := os.OpenFile(chatPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","content":[{"type":"text","text":"watch me"}]}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.SessionID == id {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for watch event with session ID")
		}
	}
}

func TestWatcherCleanup(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)
	ch, closer, err := a.Watch(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	// Channel should close
	select {
	case _, ok := <-ch:
		if ok {
			// may still drain one event; wait for close
			for ok {
				_, ok = <-ch
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close")
	}
}

func TestSearchMessages(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)
	if _, err := a.Sessions(projectRoot); err != nil {
		t.Fatal(err)
	}
	matches, err := a.SearchMessages("019f0000-aaaa-7000-8000-000000000001", "login bug", adapter.SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("expected search hits")
	}
}

func TestUsageEmpty(t *testing.T) {
	a, projectRoot := setupFixtureSessions(t)
	if _, err := a.Sessions(projectRoot); err != nil {
		t.Fatal(err)
	}
	stats, err := a.Usage("019f0000-aaaa-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if stats == nil {
		t.Fatal("want non-nil stats")
	}
}

func TestCleanUserContent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"user_query", "<user_info>x</user_info>\n<user_query>\nfix me\n</user_query>", "fix me"},
		{"harness_only_reminder", "<system-reminder>\nskills\n</system-reminder>", ""},
		{"harness_only_info", "<user_info>os</user_info><git_status>## main</git_status>", ""},
		{"empty", "  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanUserContent(tt.in)
			if got != tt.want {
				t.Fatalf("cleanUserContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPathInProjectIsolation(t *testing.T) {
	projectDir := "/tmp/sessions/%2FUsers%2Fmarcus%2Fcode%2Fsidecar"
	prefix := projectDir + string(filepath.Separator)

	cases := []struct {
		path string
		want bool
	}{
		{projectDir, true},
		{projectDir + "/019f-session/chat_history.jsonl", true},
		{"/tmp/sessions/%2FUsers%2Fmarcus%2Fcode%2Fsidecar-grok-conversations/x/chat_history.jsonl", false},
		{"/tmp/sessions/%2FUsers%2Fmarcus%2Fcode%2Fsidecar-agent-status/y/summary.json", false},
		{"/tmp/sessions/%2FUsers%2Fmarcus%2Fcode%2Fother/z", false},
	}
	for _, tc := range cases {
		if got := pathInProject(tc.path, projectDir, prefix); got != tc.want {
			t.Errorf("pathInProject(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestRelativeAndAbsoluteProjectRoot(t *testing.T) {
	// Build fixtures under an absolute temp project path that we can chdir-adjacent resolve.
	root := t.TempDir()
	// Create a real directory tree to act as the project root.
	projectAbs := filepath.Join(root, "proj")
	if err := os.MkdirAll(projectAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	// Resolve the same way the adapter does (EvalSymlinks).
	resolved, err := filepath.EvalSymlinks(projectAbs)
	if err != nil {
		resolved = projectAbs
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}

	sessionsRoot := filepath.Join(root, "sessions")
	sessID := "rel-abs-session"
	sessDir := filepath.Join(sessionsRoot, encodeProjectKey(resolved), sessID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := `{"info":{"id":"rel-abs-session","cwd":` + mustJSON(resolved) + `},"session_summary":"relabs","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z","num_messages":1,"num_chat_messages":1}`
	if err := os.WriteFile(filepath.Join(sessDir, "summary.json"), []byte(sum), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessDir, "chat_history.jsonl"), []byte(`{"type":"user","content":[{"type":"text","text":"hi"}]`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewWithSessionsDir(sessionsRoot)

	// Absolute
	found, err := a.Detect(resolved)
	if err != nil || !found {
		t.Fatalf("Detect(abs)=%v %v", found, err)
	}
	absSessions, err := a.Sessions(resolved)
	if err != nil || len(absSessions) != 1 {
		t.Fatalf("Sessions(abs)=%d %v", len(absSessions), err)
	}

	// Relative: chdir into parent and use basename
	parent := filepath.Dir(resolved)
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	rel := filepath.Base(resolved)
	found, err = a.Detect(rel)
	if err != nil || !found {
		t.Fatalf("Detect(rel)=%v %v", found, err)
	}
	relSessions, err := a.Sessions(rel)
	if err != nil || len(relSessions) != 1 {
		t.Fatalf("Sessions(rel)=%d %v", len(relSessions), err)
	}
	if relSessions[0].ID != absSessions[0].ID {
		t.Fatalf("rel/abs session ID mismatch: %s vs %s", relSessions[0].ID, absSessions[0].ID)
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestIncrementalToolResultLinking(t *testing.T) {
	root := t.TempDir()
	projectRoot := "/home/user/project"
	sessID := "incr-tool"
	sessDir := filepath.Join(root, encodeProjectKey(projectRoot), sessID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := `{"info":{"id":"incr-tool","cwd":"/home/user/project"},"session_summary":"t","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z","num_messages":2,"num_chat_messages":2}`
	if err := os.WriteFile(filepath.Join(sessDir, "summary.json"), []byte(sum), 0o644); err != nil {
		t.Fatal(err)
	}
	chatPath := filepath.Join(sessDir, "chat_history.jsonl")
	initial := strings.Join([]string{
		`{"type":"user","content":[{"type":"text","text":"run tool"}]}`,
		`{"type":"assistant","content":"ok","tool_calls":[{"id":"call-z","name":"read_file","arguments":"{\"target_file\":\"a.go\"}"}],"model_id":"grok-4.5"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(chatPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewWithSessionsDir(root)
	if _, err := a.Sessions(projectRoot); err != nil {
		t.Fatal(err)
	}
	msgs, err := a.Messages(sessID)
	if err != nil {
		t.Fatal(err)
	}
	var asst *adapter.Message
	for i := range msgs {
		if len(msgs[i].ToolUses) == 1 {
			asst = &msgs[i]
			break
		}
	}
	if asst == nil {
		t.Fatal("expected tool use")
	}
	if asst.ToolUses[0].Output != "" {
		t.Fatal("output should be empty before result arrives")
	}

	// Append tool_result after cache is warm (incremental path)
	f, err := os.OpenFile(chatPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"tool_result","tool_call_id":"call-z","content":"package a"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(chatPath, future, future)

	msgs2, err := a.Messages(sessID)
	if err != nil {
		t.Fatal(err)
	}
	var linked bool
	for _, m := range msgs2 {
		for _, tu := range m.ToolUses {
			if tu.ID == "call-z" {
				if tu.Output != "package a" {
					t.Fatalf("incremental tool link output = %q", tu.Output)
				}
				linked = true
			}
		}
		for _, cb := range m.ContentBlocks {
			if cb.Type == "tool_use" && cb.ToolUseID == "call-z" && cb.ToolOutput != "package a" {
				t.Fatalf("content block not linked: %+v", cb)
			}
		}
	}
	if !linked {
		t.Fatal("tool result not linked after incremental append")
	}
}

func TestLiveUserContentCleaned(t *testing.T) {
	home, _ := os.UserHomeDir()
	sessionsDir := filepath.Join(home, ".grok", "sessions")
	if _, err := os.Stat(sessionsDir); err != nil {
		t.Skip("no local grok sessions")
	}
	a := NewWithSessionsDir(sessionsDir)
	root := "/Users/marcus/code/sidecar-grok-conversations"
	ok, _ := a.Detect(root)
	if !ok {
		t.Skip("no sessions for this repo")
	}
	sessions, err := a.Sessions(root)
	if err != nil || len(sessions) == 0 {
		t.Skip("no sessions")
	}
	msgs, err := a.Messages(sessions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var userN int
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		userN++
		if strings.Contains(m.Content, "<system-reminder>") ||
			strings.Contains(m.Content, "<user_info>") ||
			strings.Contains(m.Content, "<user_query>") {
			t.Fatalf("live user content still has harness XML: %q", truncateForTest(m.Content, 120))
		}
	}
	if userN == 0 {
		t.Fatal("expected at least one cleaned user message")
	}
}

func truncateForTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func TestLiveSessionsIfPresent(t *testing.T) {
	home, _ := os.UserHomeDir()
	sessionsDir := filepath.Join(home, ".grok", "sessions")
	if _, err := os.Stat(sessionsDir); err != nil {
		t.Skip("no local grok sessions")
	}
	a := NewWithSessionsDir(sessionsDir)
	// Prefer this repo if present
	candidates := []string{
		"/Users/marcus/code/sidecar-grok-conversations",
		"/Users/marcus/code/sidecar",
		"/Users/marcus/code/braid",
	}
	var root string
	for _, c := range candidates {
		if ok, _ := a.Detect(c); ok {
			root = c
			break
		}
	}
	if root == "" {
		t.Skip("no matching grok project sessions among candidates")
	}
	sessions, err := a.Sessions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Fatal("detect true but zero sessions")
	}
	t.Logf("project=%s sessions=%d first=%s name=%q", root, len(sessions), sessions[0].ID, sessions[0].Name)
	msgs, err := a.Messages(sessions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected messages in live session")
	}
	t.Logf("messages=%d first_role=%s", len(msgs), msgs[0].Role)
}
