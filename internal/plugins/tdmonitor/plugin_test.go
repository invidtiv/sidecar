package tdmonitor

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tdnotes "github.com/marcus/td/pkg/notes"

	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tdroot"
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
