package tdmonitor

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	tdnotes "github.com/marcus/td/pkg/notes"

	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tdroot"
	"github.com/marcus/sidecar/internal/tdsetup"
)

func TestNew(t *testing.T) {
	p := New()
	if p == nil {
		t.Fatal("expected non-nil plugin")
	}
}

// startAndSettle runs the plugin's async monitor build to completion, the way
// the Bubble Tea loop would: Start() returns a command, and the resulting
// MonitorReadyMsg is fed back through Update(). Building the monitor is
// deliberately not done in Init() (td-9c7bf2), so tests that need a live model
// must go through this.
func startAndSettle(t *testing.T, p *Plugin) {
	t.Helper()

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command; expected the monitor build")
	}
	msg := cmd()
	ready, ok := msg.(MonitorReadyMsg)
	if !ok {
		t.Fatalf("Start() command produced %T, want MonitorReadyMsg", msg)
	}
	p.Update(ready)
}

func TestPluginID(t *testing.T) {
	p := New()
	if id := p.ID(); id != "td-monitor" {
		t.Errorf("expected ID 'td-monitor', got %q", id)
	}
}

func TestPluginName(t *testing.T) {
	p := New()
	if name := p.Name(); name != "td" {
		t.Errorf("expected Name 'td', got %q", name)
	}
}

func TestPluginIcon(t *testing.T) {
	p := New()
	if icon := p.Icon(); icon != "T" {
		t.Errorf("expected Icon 'T', got %q", icon)
	}
}

func TestFocusContext(t *testing.T) {
	p := New()

	// Without model, should return default
	if ctx := p.FocusContext(); ctx != "td-monitor" {
		t.Errorf("expected context 'td-monitor', got %q", ctx)
	}
}

func TestDiagnosticsNoDatabase(t *testing.T) {
	p := New()
	diags := p.Diagnostics()

	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}

	if diags[0].Status != "disabled" {
		t.Errorf("expected status 'disabled', got %q", diags[0].Status)
	}
}

func TestFormatCount(t *testing.T) {
	tests := []struct {
		count    int
		expected string
	}{
		{1, "1 issue"},
		{5, "5 issues"},
		{10, "10 issues"},
		{100, "100 issues"},
	}

	for _, tt := range tests {
		result := formatCount(tt.count, "issue", "issues")
		if result != tt.expected {
			t.Errorf("formatCount(%d) = %q, expected %q",
				tt.count, result, tt.expected)
		}
	}
}

func TestInitWithNonExistentDatabase(t *testing.T) {
	p := New()
	ctx := &plugin.Context{
		WorkDir: "/nonexistent/path",
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	// Init should NOT return an error even if database doesn't exist
	// This is silent degradation - plugin loads but shows "no database"
	err := p.Init(ctx)
	if err != nil {
		t.Errorf("Init should not return error for missing database, got: %v", err)
	}

	// Plugin should still be usable but model should be nil
	if p.ctx == nil {
		t.Error("context should be set")
	}
	if p.model != nil {
		t.Error("model should be nil when database not found")
	}
}

// findProjectRootWithDB returns a temp project root holding a freshly
// initialized td database. It must NEVER resolve to the real repository:
// the plugin boots td's embedded monitor, whose read-write connection on
// the developer's live .todos/issues.db is exactly the concurrent-access
// pattern that corrupted it (td-adbf16).
func findProjectRootWithDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	store, err := tdnotes.Init(dir)
	if err != nil {
		t.Fatalf("initialize isolated td database: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close isolated td database: %v", err)
	}
	if _, err := os.Stat(tdroot.ResolveDBPath(dir)); err != nil {
		t.Fatalf("isolated td database missing: %v", err)
	}
	return dir
}

func TestInitWithValidDatabase(t *testing.T) {
	projectRoot := findProjectRootWithDB(t)

	p := New()
	ctx := &plugin.Context{
		WorkDir: projectRoot,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	err := p.Init(ctx)
	if err != nil {
		t.Errorf("Init failed: %v", err)
	}

	// The model is built asynchronously, so it must not exist yet.
	if p.model != nil {
		t.Error("model should not be built during Init")
	}

	startAndSettle(t, p)

	if p.model == nil {
		t.Error("model should be created when database exists")
	}

	// Cleanup
	p.Stop()
}

func TestDatabaseReplacementReopensMonitorAndReplaysKey(t *testing.T) {
	projectRoot := findProjectRootWithDB(t)
	p := New()
	ctx := &plugin.Context{
		WorkDir: projectRoot,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer p.Stop()
	startAndSettle(t, p)

	openedInfo := p.dbFileInfo
	if openedInfo == nil {
		t.Fatal("monitor did not record the database file it opened")
	}

	// Match td sync's snapshot installation: atomically replace issues.db while
	// the embedded monitor still has the previous inode open.
	replacementRoot := findProjectRootWithDB(t)
	dbPath := tdroot.ResolveDBPath(projectRoot)
	if err := os.Rename(tdroot.ResolveDBPath(replacementRoot), dbPath); err != nil {
		t.Fatalf("replace database: %v", err)
	}
	currentInfo, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat replacement database: %v", err)
	}
	if os.SameFile(openedInfo, currentInfo) {
		t.Fatal("test did not replace the database inode")
	}

	key := tea.KeyPressMsg{Code: 'n', Text: "n"}
	_, rebuildCmd := p.Update(key)
	if rebuildCmd == nil {
		t.Fatal("database replacement did not schedule a monitor rebuild")
	}
	if p.model != nil || !p.loadingModel {
		t.Fatal("stale monitor remained active while replacement was reopening")
	}
	if p.pendingTDMessage != key {
		t.Fatalf("triggering key was not retained for replay: %#v", p.pendingTDMessage)
	}

	ready, ok := rebuildCmd().(MonitorReadyMsg)
	if !ok {
		t.Fatal("rebuild command did not return MonitorReadyMsg")
	}
	if ready.Err != nil {
		t.Fatalf("reopen replacement database: %v", ready.Err)
	}
	_, replayCmd := p.Update(ready)
	if replayCmd == nil {
		t.Fatal("adopting replacement monitor did not schedule init and key replay")
	}
	if p.model == nil || p.loadingModel || p.pendingTDMessage != nil {
		t.Fatal("replacement monitor was not fully adopted")
	}
	if p.dbFileInfo == nil || !os.SameFile(p.dbFileInfo, currentInfo) {
		t.Fatal("replacement monitor did not capture the current database identity")
	}
}

func TestKeyDuringDatabaseReopenIsRetained(t *testing.T) {
	p := New()
	p.loadingModel = true
	key := tea.KeyPressMsg{Code: 'R', Text: "R"}

	p.Update(key)

	if p.pendingTDMessage != key {
		t.Fatalf("key pressed during database reopen was not retained: %#v", p.pendingTDMessage)
	}

	// First press wins: the triggering key is the one the user aimed at a
	// responsive UI, and later keys must not displace it.
	p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if p.pendingTDMessage != key {
		t.Fatalf("a later key displaced the retained one: %#v", p.pendingTDMessage)
	}
}

// The replay must not be sequenced behind the monitor's Init. Init is a batch
// containing scheduleTick — a tea.Tick for the whole refresh interval — and
// tea.Sequence waits for every member of a batch, which would hold the key for
// a full poll interval (10s by default) before the user's action took effect.
func TestReplayedKeyIsNotGatedBehindTheRefreshTick(t *testing.T) {
	projectRoot := findProjectRootWithDB(t)
	p := New()
	ctx := &plugin.Context{
		WorkDir: projectRoot,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer p.Stop()

	key := tea.KeyPressMsg{Code: 'n', Text: "n"}
	p.pendingTDMessage = key
	p.loadingModel = true

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command")
	}
	ready, ok := cmd().(MonitorReadyMsg)
	if !ok || ready.Err != nil {
		t.Fatalf("monitor build = %#v", ready)
	}
	_, replay := p.Update(ready)
	if replay == nil {
		t.Fatal("adopting the monitor scheduled no replay")
	}

	// Drive the command the way bubbletea's runtime does, but concurrently, so
	// the assertion is about when the key is reachable rather than about the
	// tick. A Sequence would not surface the key here at all.
	batch, ok := replay().(tea.BatchMsg)
	if !ok {
		t.Fatalf("replay command produced %T, want tea.BatchMsg (a Sequence would block on the tick)", replay())
	}
	found := make(chan tea.Msg, len(batch))
	for _, c := range batch {
		go func(c tea.Cmd) {
			if c != nil {
				found <- c()
			}
		}(c)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-found:
			if got, ok := msg.(tea.KeyPressMsg); ok && got == key {
				return
			}
		case <-deadline:
			t.Fatal("replayed key did not arrive within 2s; it is gated behind the refresh tick")
		}
	}
}

// tempTdProject returns a fresh directory with an initialized td database, so
// tests don't depend on the checkout having a usable one.
func tempTdProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	cmd := exec.Command("td", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("td init failed (td not installed?): %s: %v", out, err)
	}
	return dir
}

func TestInitSetsClipboardFn(t *testing.T) {
	tmpDir := tempTdProject(t)

	p := New()
	ctx := &plugin.Context{
		WorkDir: tmpDir,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer p.Stop()

	startAndSettle(t, p)

	if p.model == nil {
		t.Fatal("model should be created when database exists")
	}

	// ClipboardFn must be set so sidecar's clipboard (atotto/clipboard) is used
	// instead of td's built-in one, which doesn't handle WSL.
	if p.model.ClipboardFn == nil {
		t.Error("model.ClipboardFn should be set to sidecar's clipboard implementation")
	}
}

func TestDiagnosticsWithDatabase(t *testing.T) {
	projectRoot := findProjectRootWithDB(t)

	p := New()
	ctx := &plugin.Context{
		WorkDir: projectRoot,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	_ = p.Init(ctx)
	defer p.Stop()
	startAndSettle(t, p)

	diags := p.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}

	// With database, status should be "ok"
	if diags[0].Status != "ok" {
		t.Errorf("expected status 'ok' with database, got %q", diags[0].Status)
	}
}

func TestNotInstalledModel(t *testing.T) {
	m := NewNotInstalledModel()
	if m == nil {
		t.Fatal("expected non-nil model")
	}

	// Test View renders content
	result := m.View(80, 24)
	if result == "" {
		t.Error("expected non-empty view")
	}

	// Check it contains expected content
	if !strings.Contains(result, "External memory") {
		t.Error("expected view to contain pitch text")
	}
}

func TestCommands(t *testing.T) {
	p := New()

	// Without model, should return nil
	cmds := p.Commands()
	if cmds != nil {
		t.Errorf("expected nil commands without model, got %d", len(cmds))
	}
}

func TestStartWithoutModel(t *testing.T) {
	p := New()

	// Start without model should return nil
	cmd := p.Start()
	if cmd != nil {
		t.Error("expected nil command without model")
	}
}

func TestViewWithoutModel(t *testing.T) {
	p := New()

	// View without model should show "no database" message
	view := p.View(80, 24)
	if view == "" {
		t.Error("expected non-empty view")
	}
}

func TestInitWithTodosFileConflict(t *testing.T) {
	// Create temp directory with .todos as a regular FILE (not directory)
	tmpDir, err := os.MkdirTemp("", "tdmonitor-conflict-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	todosFile := filepath.Join(tmpDir, ".todos")
	if err := os.WriteFile(todosFile, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("failed to write .todos file: %v", err)
	}

	p := New()
	ctx := &plugin.Context{
		WorkDir: tmpDir,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	// Init should not return an error (silent degradation)
	if err := p.Init(ctx); err != nil {
		t.Errorf("Init should not return error, got: %v", err)
	}

	// Plugin should detect the conflict
	if !p.todosConflict {
		t.Error("expected todosConflict to be true when .todos is a file")
	}

	// Model should be nil (no monitor created)
	if p.model != nil {
		t.Error("model should be nil when .todos is a file")
	}

	// Setup modal should NOT be shown (the conflict takes priority)
	if p.setupModal != nil {
		t.Error("setupModal should be nil when .todos is a file")
	}

	// View should contain the conflict error message
	view := p.View(80, 24)
	if !strings.Contains(view, "file where a directory is expected") {
		t.Errorf("expected conflict error in view, got: %s", view)
	}

	// Diagnostics should report the error
	diags := p.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Status != "error" {
		t.Errorf("expected diagnostic status 'error', got %q", diags[0].Status)
	}
	if !strings.Contains(diags[0].Detail, "file, not a directory") {
		t.Errorf("expected diagnostic detail about file conflict, got %q", diags[0].Detail)
	}
}

// TestMonitorReadyAfterStopIsDropped covers the async build racing a project
// switch: Stop() clears loadingModel, so a MonitorReadyMsg that lands
// afterwards must be discarded rather than installed for the wrong project.
func TestMonitorReadyAfterStopIsDropped(t *testing.T) {
	p := New()
	ctx := &plugin.Context{
		WorkDir: tempTdProject(t),
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command; expected the monitor build")
	}
	msg := cmd()

	p.Stop() // project switch lands before the build finishes
	p.Update(msg)

	if p.model != nil {
		t.Error("monitor built for a stopped plugin should be dropped, not adopted")
	}
}

// TestMonitorReadyWithStaleEpochIsDropped covers the same race across a
// project switch, where the plugin has been reinitialized under a new epoch.
func TestMonitorReadyWithStaleEpochIsDropped(t *testing.T) {
	p := New()
	ctx := &plugin.Context{
		WorkDir: tempTdProject(t),
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer p.Stop()

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command; expected the monitor build")
	}
	msg := cmd()

	ctx.Epoch++ // a project switch happened while the build was in flight
	p.Update(msg)

	if p.model != nil {
		t.Error("monitor from a stale epoch should be dropped, not adopted")
	}
	if !p.loadingModel {
		t.Error("a stale message should not clear the loading state")
	}
}

func TestNotesInitializationSuccessRefreshesTDMonitor(t *testing.T) {
	root := tempTdProject(t)
	p := New()
	ctx := &plugin.Context{
		WorkDir:     root,
		ProjectRoot: root,
		Epoch:       9,
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	p.loadingModel = false

	_, cmd := p.Update(tdsetup.ResultMsg{Origin: tdsetup.OriginNotes, Epoch: 9, ProjectRoot: root})
	if cmd == nil || !p.loadingModel {
		t.Fatal("Notes-owned td initialization did not rebuild the td monitor")
	}

	// A failed Notes attempt is rendered by Notes and must not disturb td.
	p.loadingModel = false
	_, cmd = p.Update(tdsetup.ResultMsg{Origin: tdsetup.OriginNotes, Epoch: 9, Err: os.ErrPermission})
	if cmd != nil || p.loadingModel {
		t.Fatal("Notes-owned setup failure leaked into the td monitor")
	}
}

func TestTDMonitorInitializationFailurePostsDurableTDAlert(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 9}

	_, cmd := p.Update(tdsetup.ResultMsg{
		Origin: tdsetup.OriginTDMonitor,
		Epoch:  9,
		Err:    errors.New("td init failed"),
	})
	if cmd == nil {
		t.Fatal("TD-owned setup failure returned no notification command")
	}
	got := cmd()
	post, ok := got.(notify.PostMsg)
	if !ok {
		t.Fatalf("TD-owned setup failure returned %T, want notify.PostMsg", got)
	}
	if post.Notification.Source != notify.SourceTD || post.Notification.Severity != notify.SeverityError {
		t.Fatalf("notification = source %q severity %q, want td/error", post.Notification.Source, post.Notification.Severity)
	}
	if post.Notification.Title != "td init failed" {
		t.Fatalf("notification title = %q", post.Notification.Title)
	}

	_, cmd = p.Update(tdsetup.ResultMsg{
		Origin: tdsetup.OriginNotes,
		Epoch:  9,
		Err:    errors.New("notes-owned failure"),
	})
	if cmd != nil {
		t.Fatal("Notes-owned setup failure leaked into td notifications")
	}
}
