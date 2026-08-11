package tasks

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	tasksui "github.com/marcus/tasks/pkg/tui"
)

// fixture is a minimal but valid Tasks store: the plugin never needs a large
// one, and writing our own keeps the test independent of the Tasks checkout.
const fixture = `{"type":"meta","version":2}
{"type":"section","id":"1a2b3c01","title":"Inbox","body":"Capture here first."}
{"type":"task","id":"1a2b3c02","parent":"1a2b3c01","state":"NEXT","priority":"A","title":"Wire the sidecar tab"}
`

// configuredEnv builds an isolated Tasks environment rooted in a temp dir. Every
// path Tasks resolves — config, state, task data — lives under it, so no test
// can read or write the developer's real ~/.tasks or tasks.jsonl.
func configuredEnv(t *testing.T) (root string, env map[string]string) {
	t.Helper()

	root = t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "tasks.jsonl"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, map[string]string{
		"HOME":            root,
		"TASKS_DIR":       data,
		"XDG_CONFIG_HOME": filepath.Join(root, "config"),
		"XDG_STATE_HOME":  filepath.Join(root, "state"),
	}
}

// writeTasksConfig drops a Tasks config file into an isolated environment, so a
// test can assert against colours (or any other setting) the *user* configured
// rather than only the ones sidecar supplies.
func writeTasksConfig(t *testing.T, root, text string) {
	t.Helper()

	dir := filepath.Join(root, "config", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// brokenStoreEnv is a configured Tasks whose store exists but is not valid
// Tasks JSONL. The model builds fine; it just cannot read anything.
func brokenStoreEnv(t *testing.T) map[string]string {
	t.Helper()

	root, env := configuredEnv(t)
	store := filepath.Join(root, "data", "tasks.jsonl")
	if err := os.WriteFile(store, []byte("{{{ not json at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return env
}

// unconfiguredEnv points Tasks at an empty home with no config file and no
// TASKS_DIR, which is exactly the state a first-run user is in.
func unconfiguredEnv(t *testing.T) map[string]string {
	t.Helper()

	root := t.TempDir()
	return map[string]string{
		"HOME":            root,
		"XDG_CONFIG_HOME": filepath.Join(root, "config"),
		"XDG_STATE_HOME":  filepath.Join(root, "state"),
	}
}

func testContext(t *testing.T) *plugin.Context {
	t.Helper()

	// A non-zero epoch is the realistic case: sidecar bumps it on every project
	// switch, and a test that only ever runs at zero cannot tell "carries the
	// host's epoch" from "hard-codes nothing".
	return &plugin.Context{
		WorkDir: t.TempDir(),
		Epoch:   7,
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
}

// newConfigured returns an initialized plugin bound to an isolated, configured
// Tasks environment, plus that environment's root.
func newConfigured(t *testing.T) (*Plugin, string, *plugin.Context) {
	t.Helper()

	root, env := configuredEnv(t)
	p := New()
	p.environment = env
	ctx := testContext(t)
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return p, root, ctx
}

// startAndSettle runs the async build the way the Bubble Tea loop would: Start()
// returns a command, and the resulting TasksReadyMsg is fed back through
// Update(). Building the model is deliberately not done in Init().
func startAndSettle(t *testing.T, p *Plugin) TasksReadyMsg {
	t.Helper()

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command; expected the tasks build")
	}
	ready, ok := cmd().(TasksReadyMsg)
	if !ok {
		t.Fatalf("Start() command produced something other than TasksReadyMsg")
	}
	if p.ctx != nil && ready.Epoch != p.ctx.Epoch {
		t.Fatalf("ready message carries epoch %d, want the host's %d", ready.Epoch, p.ctx.Epoch)
	}
	p.Update(ready)
	return ready
}

// tree lists every path under root, so a test can prove nothing was created.
func tree(t *testing.T, root string) []string {
	t.Helper()

	var paths []string
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

func TestPluginIdentity(t *testing.T) {
	p := New()
	if p.ID() != "tasks" {
		t.Errorf("ID = %q, want \"tasks\"", p.ID())
	}
	if p.Name() != "tasks" {
		t.Errorf("Name = %q, want \"tasks\"", p.Name())
	}
	if p.Icon() == "" {
		t.Error("Icon should not be empty")
	}
	if p.Icon() == "T" {
		t.Error("Icon must not collide with the td monitor tab")
	}
}

func TestLifecycle(t *testing.T) {
	p, _, _ := newConfigured(t)

	if p.model != nil {
		t.Fatal("Init must not build the tasks model")
	}
	if !p.loading {
		t.Fatal("Init should leave the plugin waiting for its async build")
	}

	startAndSettle(t, p)

	if p.model == nil {
		t.Fatalf("model should exist after the ready message (%s)", p.unavailable)
	}
	if p.loading {
		t.Error("loading should be cleared once the model is adopted")
	}

	view := p.View(80, 24)
	if strings.Count(view, "\n") >= 24 {
		t.Errorf("View exceeded its allotted height: %d lines", strings.Count(view, "\n")+1)
	}

	p.Stop()
	if p.model != nil {
		t.Error("Stop should release the model")
	}
}

func TestRestartAfterStop(t *testing.T) {
	p, _, ctx := newConfigured(t)
	startAndSettle(t, p)
	p.Stop()

	if err := p.Init(ctx); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	if p.model != nil || !p.loading {
		t.Fatal("re-Init should return the plugin to its pre-build state")
	}
	startAndSettle(t, p)
	defer p.Stop()

	if p.model == nil {
		t.Fatalf("model should be rebuilt after restart (%s)", p.unavailable)
	}
}

// TestInitDoesNoIO proves the requirement that nothing is read, opened, or
// spawned before sidecar's first frame: after Init the isolated Tasks
// environment is byte-for-byte what the test wrote, and only running the
// command Start() returned changes that.
func TestInitDoesNoIO(t *testing.T) {
	root, env := configuredEnv(t)
	before := tree(t, root)

	p := New()
	p.environment = env
	if err := p.Init(testContext(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if got := tree(t, root); !equalPaths(got, before) {
		t.Errorf("Init touched the tasks environment:\nbefore %v\nafter  %v", before, got)
	}
	if p.model != nil {
		t.Error("Init constructed the tasks model")
	}

	// Construction happens only inside the Start command.
	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command")
	}
	if p.model != nil {
		t.Error("Start() itself must not construct the model; its command does")
	}
	ready, ok := cmd().(TasksReadyMsg)
	if !ok {
		t.Fatal("Start() command produced something other than TasksReadyMsg")
	}
	if ready.Err != nil || ready.Model == nil {
		t.Fatalf("build failed: %v", ready.Err)
	}
	_ = ready.Model.Discard()
}

// TestModelIsBuiltOnlyByTheStartCommand pins the acceptance criterion directly:
// no store open and no agent queue before sidecar's first frame. Comparing the
// filesystem before and after Init cannot establish this — a read-only open, a
// stat walk, or a spawned process leaves no new paths behind — so this counts
// the builder calls instead.
func TestModelIsBuiltOnlyByTheStartCommand(t *testing.T) {
	_, env := configuredEnv(t)

	calls := 0
	p := New()
	p.environment = env
	p.newEmbedded = func(options tasksui.EmbeddedOptions) (*tasksui.Model, error) {
		calls++
		return tasksui.NewEmbedded(options)
	}

	if err := p.Init(testContext(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if calls != 0 {
		t.Fatalf("Init built the tasks model (%d calls); it must not run before the first frame", calls)
	}

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command")
	}
	if calls != 0 {
		t.Fatalf("Start() itself built the model (%d calls); only its command may", calls)
	}

	ready, ok := cmd().(TasksReadyMsg)
	if !ok {
		t.Fatal("Start() command produced something other than TasksReadyMsg")
	}
	if calls != 1 {
		t.Fatalf("builder called %d times, want exactly 1", calls)
	}
	if ready.Err != nil || ready.Model == nil {
		t.Fatalf("build failed: %v", ready.Err)
	}
	_ = ready.Model.Discard()
}

func equalPaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStaleReadyIsDiscarded covers a project switch landing while the build is
// in flight. The model must be released — dropping it would leak its agent
// queue — but released via Discard, not Close: it was never presented, so its
// state must not reach the shared session file. The observable difference is
// exactly that no session is written.
func TestStaleReadyIsDiscarded(t *testing.T) {
	p, root, ctx := newConfigured(t)

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command")
	}
	ready, ok := cmd().(TasksReadyMsg)
	if !ok {
		t.Fatal("Start() command produced something other than TasksReadyMsg")
	}
	if ready.Err != nil || ready.Model == nil {
		t.Fatalf("build failed: %v", ready.Err)
	}

	session := filepath.Join(root, "state", "tasks", "hosts", sessionNamespace, "tui.json")
	if _, err := os.Stat(session); err == nil {
		t.Fatal("session file existed before the model was closed")
	}

	ctx.Epoch++ // a project switch happened while the build was in flight
	p.Update(ready)

	if p.model != nil {
		t.Error("a stale model should be dropped, not adopted")
	}
	if !p.loading {
		t.Error("a stale message should not clear the loading state")
	}
	if _, err := os.Stat(session); !os.IsNotExist(err) {
		t.Errorf("a never-presented model wrote the shared session file: %v", err)
	}
}

// TestReadyAfterStopIsDiscarded is the same race, but where Stop() already tore
// the plugin down.
func TestReadyAfterStopIsDiscarded(t *testing.T) {
	p, root, _ := newConfigured(t)

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command")
	}
	msg := cmd()

	p.Stop()
	p.Update(msg)

	if p.model != nil {
		t.Error("a model built for a stopped plugin should be dropped")
	}
	session := filepath.Join(root, "state", "tasks", "hosts", sessionNamespace, "tui.json")
	if _, err := os.Stat(session); !os.IsNotExist(err) {
		t.Errorf("a never-presented model wrote the shared session file: %v", err)
	}
}

// TestLateModelDiscardDoesNotOverwriteTheLiveSession is the exact confirmed
// repro for the HIGH bug this packet closes.
//
// Every model built for the sidecar namespace shares one session file. A model
// that lands after the user has moved on carries the session state as it was
// when that model was *constructed*. Releasing it with Close wrote that stale
// state over the session the live model had just saved: the user left the tab
// on Quadrants and came back to Agenda.
func TestLateModelDiscardDoesNotOverwriteTheLiveSession(t *testing.T) {
	p, root, _ := newConfigured(t)
	startAndSettle(t, p)
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	// The live model, A, is moved off its default view so its saved state is
	// distinguishable from any other model's.
	p.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	if got := p.model.CurrentView(); got != tasksui.ViewQuadrants {
		t.Fatalf("test invalid: live model is on the %q view, want quadrants", got)
	}

	// B is built now — before A saves — so it holds the pre-Quadrants session,
	// exactly like an in-flight build the user has already moved past. p.loading
	// is false, so delivering B later takes the stale path.
	build := p.buildModel()
	if build == nil {
		t.Fatal("buildModel returned no command")
	}
	late, ok := build().(TasksReadyMsg)
	if !ok || late.Err != nil || late.Model == nil {
		t.Fatalf("late build failed: %+v", late)
	}
	if late.Model.CurrentView() == tasksui.ViewQuadrants {
		t.Fatal("test invalid: the late model already shares the live model's view")
	}

	// A is presented, so A saves.
	p.Stop()
	session := filepath.Join(root, "state", "tasks", "hosts", sessionNamespace, "tui.json")
	saved, err := os.ReadFile(session)
	if err != nil {
		t.Fatalf("the live model did not save its session: %v", err)
	}
	if !strings.Contains(string(saved), "quadrants") {
		t.Fatalf("the live model saved the wrong view:\n%s", saved)
	}

	// B lands late and is dropped. Byte-for-byte, the live session must survive.
	p.Update(late)
	if p.model != nil {
		t.Fatal("a late model must not be adopted after Stop")
	}
	after, err := os.ReadFile(session)
	if err != nil {
		t.Fatalf("reading the session after the late drop: %v", err)
	}
	if string(after) != string(saved) {
		t.Errorf("the discarded model rewrote the live session:\nbefore %s\nafter  %s", saved, after)
	}
}

// TestInitDiscardsALiveModelWithoutSaving is the same rule on the defensive
// path in Init: reaching there means Stop did not complete, so this model's
// state is not what the user last saw and must not become the session its
// replacement loads.
func TestInitDiscardsALiveModelWithoutSaving(t *testing.T) {
	p, root, ctx := newConfigured(t)
	startAndSettle(t, p)
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	session := filepath.Join(root, "state", "tasks", "hosts", sessionNamespace, "tui.json")
	if _, err := os.Stat(session); err == nil {
		t.Fatal("session existed before any release")
	}

	// Init without a preceding Stop, the shape safeStop's panic recovery leaves.
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.model != nil {
		t.Error("Init should not leave the old model installed")
	}
	if _, err := os.Stat(session); !os.IsNotExist(err) {
		t.Errorf("Init saved a doomed model's session: %v", err)
	}
}

// TestBrokenStoreIsReportedAsADiagnostic covers a store that exists but cannot
// be read. The model builds and renders, so this is not the `unavailable` path;
// without consulting LoadError the tab would report "ok" while showing what
// looks like an authoritative empty list.
//
// Sidecar deliberately does NOT repaint the tab here: Tasks owns its own frame
// and already paints a read-error banner in its footer, so replacing the frame
// (or adding a second banner) would say the same thing twice and throw away a
// still-usable Tasks UI. Sidecar's job is to carry the condition to the surface
// Tasks cannot reach — Diagnostics and the log.
func TestBrokenStoreIsReportedAsADiagnostic(t *testing.T) {
	p := New()
	p.environment = brokenStoreEnv(t)
	if err := p.Init(testContext(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Stop()

	ready := startAndSettle(t, p)
	if ready.Err != nil || p.model == nil {
		t.Fatalf("a broken store must still build a model, got %v", ready.Err)
	}
	if p.loadError == nil {
		t.Fatal("a corrupt store reported no load error")
	}
	if p.unavailable != "" {
		t.Errorf("a broken store is not a build failure, got unavailable = %q", p.unavailable)
	}

	diags := p.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Status != "error" {
		t.Errorf("diagnostic status = %q, want \"error\"", diags[0].Status)
	}
	if !strings.Contains(diags[0].Detail, "cannot read the task store") {
		t.Errorf("diagnostic should name the read failure, got %q", diags[0].Detail)
	}
	if !strings.Contains(diags[0].Detail, storeReadHint) {
		t.Errorf("diagnostic should carry the repair hint, got %q", diags[0].Detail)
	}
	// The unconfigured hint is the wrong advice here: nothing needs configuring.
	if strings.Contains(diags[0].Detail, setupHint) {
		t.Errorf("a broken store must not be told to configure Tasks: %q", diags[0].Detail)
	}

	// Tasks' own banner is what the user sees, and it appears exactly once.
	view := p.View(100, 30)
	if !strings.Contains(view, "cannot read the task store") {
		t.Errorf("Tasks' read-error banner is missing from the tab:\n%s", view)
	}
	if n := strings.Count(view, "cannot read the task store"); n != 1 {
		t.Errorf("the read error is shown %d times; sidecar must not duplicate Tasks' banner", n)
	}
	if strings.Contains(view, "Tasks is unavailable") {
		t.Error("a readable-but-broken store must not replace the Tasks frame")
	}
}

// TestHealthyStoreReportsNoLoadError is the other half: an ordinary store must
// not trip the diagnostic.
func TestHealthyStoreReportsNoLoadError(t *testing.T) {
	p, _, _ := newConfigured(t)
	startAndSettle(t, p)
	defer p.Stop()
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	if p.loadError != nil {
		t.Fatalf("a healthy store reported %v", p.loadError)
	}
	if diags := p.Diagnostics(); diags[0].Status != "ok" {
		t.Errorf("diagnostic = %+v, want ok", diags[0])
	}
}

// TestUserColorsSurviveUnderSidecarsOverlay covers the overlay contract: sidecar
// overrides the handful of slots that must agree with its chrome, and every slot
// it does not name keeps whatever the user configured for their own Tasks.
// buildTheme must never set ReplaceColors.
func TestUserColorsSurviveUnderSidecarsOverlay(t *testing.T) {
	root, env := configuredEnv(t)
	// accent IS one of sidecar's slots; tab_active is not.
	writeTasksConfig(t, root, "color.tab_active = #ff00ff\ncolor.accent = #ffaa00\n")

	p := New()
	p.environment = env
	if err := p.Init(testContext(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	startAndSettle(t, p)
	defer p.Stop()
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	view := p.View(100, 30)
	if !strings.Contains(view, "255;0;255") {
		t.Errorf("sidecar's palette destroyed the user's own tab_active colour:\n%q", view)
	}
	// The user's accent must lose, but asserting only its absence is vacuous:
	// it is absent under an empty overlay too. Assert sidecar's value is the
	// one actually rendered.
	if want := rgbTriple(t, styles.GetCurrentTheme().Colors.Primary); !strings.Contains(view, want) {
		t.Errorf("sidecar's accent %q (%s) is not in the rendered frame:\n%q",
			styles.GetCurrentTheme().Colors.Primary, want, view)
	}
	if strings.Contains(view, "255;170;0") {
		t.Errorf("sidecar's override of accent did not win:\n%q", view)
	}
	if buildTheme().ReplaceColors {
		t.Error("buildTheme must not opt into wholesale colour replacement")
	}
}

// TestBuildThemeOverlaysSidecarSlots pins the adapter itself. The rendered-frame
// test above only exercises whichever slots that frame happens to paint, so it
// cannot see a slot that is dropped, misrouted, or a base theme that clobbers
// the user's.
func TestBuildThemeOverlaysSidecarSlots(t *testing.T) {
	c := styles.GetCurrentTheme().Colors
	theme := buildTheme()

	if theme.Name != "" {
		t.Errorf("Name = %q, want empty so the user's own base theme survives", theme.Name)
	}
	if theme.ReplaceColors {
		t.Error("ReplaceColors must stay false; sidecar overlays a few slots, it does not own the palette")
	}

	for slot, want := range map[string]string{
		"accent":      c.Primary,
		"prompt":      "bold " + c.Primary,
		"error":       c.Error,
		"warning":     c.Warning,
		"muted":       c.TextMuted,
		"border":      c.BorderNormal,
		"context":     "bold " + c.Secondary,
		"project":     c.Accent,
		"state_next":  c.Info,
		"due_overdue": c.Error,
	} {
		if got := theme.Colors[slot]; got != want {
			t.Errorf("slot %q = %q, want %q", slot, got, want)
		}
	}
	if _, ok := theme.Colors["tab_active"]; ok {
		t.Error("sidecar must not name tab_active; unnamed slots belong to the user")
	}
}

// TestNewEmbeddedIsCalledFromOneSiteOnly backs the call-counting test above.
// That test can only see construction that goes through p.newEmbedded, so this
// one pins the other half: the plugin constructs Tasks in exactly one place,
// inside the command Start() returns. Without it, moving the build into Init
// as a direct tasksui.NewEmbedded call would bypass the seam unnoticed.
//
// A build has no observable footprint to assert instead — measured: it starts
// no goroutines (the agent queue is lazy) and leaves no descriptors open (it
// closes what it reads) — so the structure is what there is to check.
func TestNewEmbeddedIsCalledFromOneSiteOnly(t *testing.T) {
	source, err := os.ReadFile("plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	const call = "tasksui.NewEmbedded"

	var sites []int
	for line, text := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(strings.TrimSpace(text), "//") {
			continue
		}
		if strings.Contains(text, call) {
			sites = append(sites, line+1)
		}
	}
	if len(sites) != 1 {
		t.Fatalf("%s appears at lines %v, want exactly one site; every build must go through p.newEmbedded so it stays off the startup path",
			call, sites)
	}

	// The one site must be the fallback inside buildModel's returned command,
	// not the body of Init, Start, or buildModel itself.
	body := string(source)
	buildModel := body[strings.Index(body, "func (p *Plugin) buildModel()"):]
	if !strings.Contains(buildModel[:strings.Index(buildModel, "\n}\n")], call) {
		t.Errorf("%s is called outside buildModel; construction must not run before the first frame", call)
	}
}

// rgbTriple converts a #rrggbb colour into the "r;g;b" form that appears in a
// rendered SGR sequence.
func rgbTriple(t *testing.T, hex string) string {
	t.Helper()
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		t.Fatalf("theme colour %q is not #rrggbb: %v", hex, err)
	}
	return fmt.Sprintf("%d;%d;%d", r, g, b)
}

// TestSessionNamespaceIsSidecarSpecific proves the embedded model can never
// overwrite the standalone tasks-tui session.
func TestSessionNamespaceIsSidecarSpecific(t *testing.T) {
	if sessionNamespace != "sidecar" {
		t.Fatalf("sessionNamespace = %q, want \"sidecar\"", sessionNamespace)
	}

	p, root, _ := newConfigured(t)
	startAndSettle(t, p)
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}
	p.Stop() // Close() writes the session

	hosted := filepath.Join(root, "state", "tasks", "hosts", "sidecar", "tui.json")
	if _, err := os.Stat(hosted); err != nil {
		t.Errorf("embedded session not written to the sidecar namespace: %v", err)
	}
	standalone := filepath.Join(root, "state", "tasks", "tui.json")
	if _, err := os.Stat(standalone); err == nil {
		t.Error("embedded model wrote the standalone tasks-tui session")
	}
}

// TestUnconfiguredTasks covers a first-run user: the tab must explain itself
// and say what to do, never render an empty list that looks authoritative.
func TestUnconfiguredTasks(t *testing.T) {
	p := New()
	p.environment = unconfiguredEnv(t)
	if err := p.Init(testContext(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Stop()

	ready := startAndSettle(t, p)
	if ready.Err == nil {
		t.Fatal("expected an error building tasks against an unconfigured environment")
	}
	if p.model != nil {
		t.Fatal("no model should be adopted when tasks is unconfigured")
	}

	view := p.View(80, 24)
	if !strings.Contains(view, "unavailable") {
		t.Errorf("view should name the problem, got:\n%s", view)
	}
	// Tasks' own configuration-required message, reflowed by lipgloss, so
	// match on a distinctive fragment rather than the whole sentence.
	if !strings.Contains(view, "not configured") {
		t.Errorf("view should carry Tasks' own diagnosis, got:\n%s", view)
	}
	if !strings.Contains(view, "restart sidecar") {
		t.Errorf("view should carry a setup hint, got:\n%s", view)
	}

	diags := p.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Status != "error" {
		t.Errorf("diagnostic status = %q, want \"error\"", diags[0].Status)
	}
	if !strings.Contains(diags[0].Detail, "not configured") ||
		!strings.Contains(diags[0].Detail, setupHint) {
		t.Errorf("diagnostic should carry the diagnosis and the hint, got %q", diags[0].Detail)
	}
}

// TestUpdateForwardsHostMessages walks the message categories the plugin is
// required to forward. Tasks ignores what it doesn't recognise, so the
// assertion is that forwarding is safe and the model stays live.
func TestUpdateForwardsHostMessages(t *testing.T) {
	p, _, _ := newConfigured(t)
	startAndSettle(t, p)
	defer p.Stop()
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	messages := []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 40},
		tea.KeyPressMsg{Code: 'j', Text: "j"},
		tea.MouseWheelMsg{Button: tea.MouseWheelDown},
		tea.PasteMsg{Content: "pasted"},
	}
	for _, msg := range messages {
		if _, cmd := p.Update(msg); cmd != nil {
			cmd() // draining must not panic
		}
	}

	if p.width != 100 || p.height != 40 {
		t.Errorf("window size not tracked: %dx%d", p.width, p.height)
	}
	if p.model == nil {
		t.Error("forwarding should not tear down the model")
	}
}

// TestNestedQuitDoesNotQuitSidecar covers the quit boundary: whatever Tasks
// does with `q`, the plugin must never hand tea.Quit back to the host.
func TestNestedQuitDoesNotQuitSidecar(t *testing.T) {
	p, _, _ := newConfigured(t)
	startAndSettle(t, p)
	defer p.Stop()
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	// Tasks now genuinely latches under SuppressQuit, so pressing `q` twice
	// exercises latch → clear → latch → clear rather than a single refusal.
	for press := range 2 {
		_, cmd := p.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		if quitEscapes(cmd) {
			t.Errorf("press %d: nested quit escaped to the host", press+1)
		}
		if p.model == nil {
			t.Fatalf("press %d: a nested quit closed the embedded model", press+1)
		}
		if p.model.QuitRequested() {
			t.Errorf("press %d: quit request should be cleared, not left latched", press+1)
		}
	}

	// And the tab is still usable afterwards.
	if _, cmd := p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}); quitEscapes(cmd) {
		t.Error("an ordinary key after a nested quit produced tea.Quit")
	}
	if got := p.FocusContext(); !strings.HasPrefix(got, "tasks-") {
		t.Errorf("Tasks left a non-Tasks focus context after quit: %q", got)
	}
}

func TestCommandsProjectTasksRegistry(t *testing.T) {
	// Commands come from Tasks' exported registry, so they are available
	// without a live model.
	cmds := New().Commands()
	if len(cmds) == 0 {
		t.Fatal("expected exported tasks commands")
	}
	for _, cmd := range cmds {
		if cmd.ID == "" {
			t.Errorf("command with empty ID: %+v", cmd)
		}
		if cmd.Category == "" {
			t.Errorf("command %q has no category", cmd.ID)
		}
	}
}

func TestFocusContextWithoutModel(t *testing.T) {
	p := New()
	if got := p.FocusContext(); got != pluginID {
		t.Errorf("FocusContext = %q, want %q", got, pluginID)
	}
	if p.ConsumesTextInput() {
		t.Error("a plugin without a model consumes no text input")
	}
}

func TestFocusContextFromModel(t *testing.T) {
	p, _, _ := newConfigured(t)
	startAndSettle(t, p)
	defer p.Stop()
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	if got := p.FocusContext(); !strings.HasPrefix(got, "tasks-") {
		t.Errorf("FocusContext = %q, want a tasks-owned context", got)
	}
}

func TestViewWhileLoading(t *testing.T) {
	p := New()
	if err := p.Init(testContext(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if view := p.View(80, 24); !strings.Contains(view, "Loading") {
		t.Errorf("expected a loading view, got:\n%s", view)
	}
}

func TestViewIsConstrainedToItsBox(t *testing.T) {
	p := New()
	p.unavailable = strings.Repeat("a very long diagnostic line ", 40)
	view := p.View(40, 6)

	lines := strings.Split(view, "\n")
	if len(lines) != 6 {
		t.Errorf("view rendered %d lines, want 6", len(lines))
	}
}

// TestViewAtNonPositiveSizes covers S1: lipgloss ignores Width/Height/MaxWidth/
// MaxHeight when the value is <= 0, so the constraint silently disappears and
// Tasks' full frame escapes into sidecar's header and footer. Reachable in
// production whenever the terminal is no taller than sidecar's chrome.
func TestViewAtNonPositiveSizes(t *testing.T) {
	sizes := [][2]int{{0, 0}, {-3, -3}, {0, 24}, {80, 0}, {-1, 10}, {10, -1}}

	for _, name := range []string{"unavailable", "loading", "unstarted"} {
		for _, size := range sizes {
			p := New()
			switch name {
			case "unavailable":
				p.unavailable = strings.Repeat("a very long diagnostic line ", 40)
			case "loading":
				p.loading = true
			}
			if got := p.View(size[0], size[1]); got != "" {
				t.Errorf("%s View(%d,%d) rendered %d bytes / %d lines, want empty",
					name, size[0], size[1], len(got), strings.Count(got, "\n")+1)
			}
		}
	}
}

// TestViewFillsItsBoxExactly is the regression guard for the fix above: the
// clamp must not cost the exactness that already holds at every positive size.
func TestViewFillsItsBoxExactly(t *testing.T) {
	sizes := [][2]int{{1, 1}, {2, 3}, {20, 3}, {40, 6}, {80, 24}, {300, 100}}

	for _, size := range sizes {
		width, height := size[0], size[1]
		p := New()
		p.unavailable = strings.Repeat("a very long diagnostic line ", 40)

		lines := strings.Split(p.View(width, height), "\n")
		if len(lines) != height {
			t.Errorf("View(%d,%d) rendered %d lines, want %d", width, height, len(lines), height)
			continue
		}
		for i, line := range lines {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("View(%d,%d) line %d is %d cells wide", width, height, i, w)
			}
		}
	}
}

// TestStaleReadyAfterStopInitWithoutEpochBump covers S2. The registry bumps
// ctx.Epoch on project switch, but nothing guarantees a Stop/Init pair does;
// without a per-plugin generation the plugin adopts the previous lifecycle's
// model and closes the fresh one.
func TestStaleReadyAfterStopInitWithoutEpochBump(t *testing.T) {
	p, _, ctx := newConfigured(t)

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command")
	}
	old, ok := cmd().(TasksReadyMsg)
	if !ok || old.Err != nil || old.Model == nil {
		t.Fatalf("build failed: %+v", old)
	}

	// A full recycle with the epoch deliberately left alone, and the new build
	// still in flight — so the loading flag cannot distinguish the lifecycles.
	epoch := ctx.Epoch
	p.Stop()
	if err := p.Init(ctx); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	next := p.Start()
	if next == nil {
		t.Fatal("Start() returned no command")
	}
	defer p.Stop()

	if ctx.Epoch != epoch || old.Epoch != epoch {
		t.Fatalf("test invalid: epoch moved (ctx %d, msg %d, want %d)", ctx.Epoch, old.Epoch, epoch)
	}
	if !p.loading {
		t.Fatal("test invalid: the new build already settled")
	}

	p.Update(old)

	if p.model != nil {
		t.Fatal("a model from the previous lifecycle was adopted")
	}
	if !p.loading {
		t.Error("the previous lifecycle's message cancelled the live build")
	}

	// The lifecycle that is actually current still settles normally.
	ready, ok := next().(TasksReadyMsg)
	if !ok {
		t.Fatal("Start() command produced something other than TasksReadyMsg")
	}
	p.Update(ready)
	if p.model == nil {
		t.Errorf("the current lifecycle's model was not adopted (%s)", p.unavailable)
	}
}

// TestWindowSizeTrackedWhileLoading covers S4: the size-tracking block sat
// below the nil-model bail-out, making the late-arrival seeding it feeds dead
// code on exactly the path it documents.
func TestWindowSizeTrackedWhileLoading(t *testing.T) {
	p, _, _ := newConfigured(t)

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command")
	}
	if p.model != nil {
		t.Fatal("test invalid: model already built")
	}

	p.Update(tea.WindowSizeMsg{Width: 123, Height: 45})
	if p.width != 123 || p.height != 45 {
		t.Fatalf("size not tracked while loading: %dx%d", p.width, p.height)
	}

	// And the tracked size is what the late model is seeded with (S8: the
	// seeding update's command must be carried, not dropped).
	ready, ok := cmd().(TasksReadyMsg)
	if !ok {
		t.Fatal("Start() command produced something other than TasksReadyMsg")
	}
	_, adopt := p.Update(ready)
	defer p.Stop()
	if p.model == nil {
		t.Fatalf("model should be adopted (%s)", p.unavailable)
	}
	if adopt == nil {
		t.Error("adopting a model should return its init command")
	} else {
		adopt() // draining must not panic
	}
}

// TestAdoptedModelKeepsBothCommands covers S8. adoptModel used to call the
// seeding Update for its side effect and drop the command it returned; today's
// Tasks happens to return nil there, so the guarantee is asserted on the
// combining rule itself rather than on Tasks' current behaviour.
func TestAdoptedModelKeepsBothCommands(t *testing.T) {
	var ran []string
	mark := func(name string) tea.Cmd {
		return func() tea.Msg { ran = append(ran, name); return nil }
	}

	if got := combine(nil, nil); got != nil {
		t.Error("combine(nil, nil) should be nil")
	}
	for _, pair := range [][2]tea.Cmd{{mark("init"), nil}, {nil, mark("seed")}} {
		ran = nil
		cmd := combine(pair[0], pair[1])
		if cmd == nil {
			t.Fatal("combine dropped the only command it was given")
		}
		cmd()
		if len(ran) != 1 {
			t.Errorf("expected 1 command to run, ran %v", ran)
		}
	}

	ran = nil
	cmd := combine(mark("init"), mark("seed"))
	if cmd == nil {
		t.Fatal("combine returned nil for two commands")
	}
	// A batch yields its members as commands for the runtime to run.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("combine of two commands produced %T, want tea.BatchMsg", cmd())
	}
	for _, c := range batch {
		c()
	}
	if len(ran) != 2 {
		t.Errorf("both the init and the seeding command should run, ran %v", ran)
	}
}

// TestQuitIsSuppressedInsideBatches covers S5. A bare tea.Quit was caught, but
// tea.Batch/tea.Sequence yield a slice-of-commands message that passed straight
// through to sidecar's runtime and killed the host.
func TestQuitIsSuppressedInsideBatches(t *testing.T) {
	marker := struct{ n int }{7}
	other := func() tea.Msg { return marker }

	cases := map[string]tea.Cmd{
		"bare":     tea.Quit,
		"batch":    tea.Batch(tea.Quit, other),
		"sequence": tea.Sequence(tea.Quit, other),
		"nested":   tea.Batch(other, tea.Sequence(other, tea.Batch(tea.Quit, other))),
	}

	for name, cmd := range cases {
		if quitEscapes(suppressQuit(cmd)) {
			t.Errorf("%s: tea.Quit escaped to the host", name)
		}
	}

	// The wrapper must not eat ordinary messages.
	if got := suppressQuit(other)(); got != tea.Msg(marker) {
		t.Errorf("ordinary message was swallowed: %#v", got)
	}
}

// quitEscapes drains a command the way bubbletea's runtime would, following
// batch and sequence messages into the commands they carry.
func quitEscapes(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if quitEscapes(c) {
				return true
			}
		}
	default:
		value := reflect.ValueOf(msg)
		if value.Kind() == reflect.Slice && value.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
			for i := range value.Len() {
				c, _ := value.Index(i).Interface().(tea.Cmd)
				if quitEscapes(c) {
					return true
				}
			}
		}
	}
	return false
}

// TestUnavailableSpendsHeightOnContent covers S6: at a small size the panel used
// to spend its whole budget on the title and blank separators, showing the user
// "Tasks is unavailable" and neither the reason nor what to do about it.
func TestUnavailableSpendsHeightOnContent(t *testing.T) {
	p := New()
	p.unavailable = "tasks is not configured"

	for _, height := range []int{3, 4, 6, 24} {
		view := p.View(20, height)
		// The reason reflows at width 20, so match a fragment that survives it.
		if !strings.Contains(view, "tasks is not") {
			t.Errorf("height %d: reason missing from:\n%s", height, view)
		}
		if !strings.Contains(view, "Configure") {
			t.Errorf("height %d: setup hint missing from:\n%s", height, view)
		}
	}

	// The panel is inset, not merely wrapped short.
	for _, line := range strings.Split(p.View(40, 10), "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "  ") {
			t.Errorf("panel line is flush-left, not inset: %q", line)
		}
	}
}

// TestStopPreservesTheDiagnostic covers S7: clearing p.unavailable made an
// unconfigured Tasks report "disabled / not started" instead of the real reason.
func TestStopPreservesTheDiagnostic(t *testing.T) {
	p := New()
	p.environment = unconfiguredEnv(t)
	if err := p.Init(testContext(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	startAndSettle(t, p)
	if p.unavailable == "" {
		t.Fatal("expected an unavailable reason for an unconfigured environment")
	}

	p.Stop()

	diags := p.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Status != "error" || !strings.Contains(diags[0].Detail, "not configured") {
		t.Errorf("Stop erased the diagnostic: %+v", diags[0])
	}
}

// The Packet 1.1 guard that lived here (TestFooterIsNotSuppressedYet) required
// Tasks to keep painting its own key hints until sidecar had a unified footer.
// Sidecar has one now, and Tasks v1.3.0 has SuppressKeyHints, so the hint row
// belongs to the host: TestTheTabPaintsNoSecondKeyHintRow in routing_test.go
// asserts the opposite end state, including the parts of Tasks' footer stack
// (the prompt, the transcript, the banner) that must survive.

func TestDiagnosticsBeforeStart(t *testing.T) {
	diags := New().Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Status != "disabled" {
		t.Errorf("status = %q, want \"disabled\"", diags[0].Status)
	}
}

// TestTasksIsFramedLikeEveryOtherTab covers the border sidecar draws around the
// tab. Tasks stopped painting its own frame — correct when it owns the screen,
// wrong inside sidecar, where every other plugin sits in the gradient panel.
func TestTasksIsFramedLikeEveryOtherTab(t *testing.T) {
	p, _, _ := newConfigured(t)
	startAndSettle(t, p)
	if p.model == nil {
		t.Fatal("no model to render")
	}

	lines := strings.Split(p.View(80, 24), "\n")
	if len(lines) != 24 {
		t.Fatalf("view is %d rows, want 24", len(lines))
	}
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[0], "╮") {
		t.Errorf("top border missing: %q", lines[0])
	}
	if !strings.Contains(lines[23], "╰") || !strings.Contains(lines[23], "╯") {
		t.Errorf("bottom border missing: %q", lines[23])
	}
	for i, line := range lines[1:23] {
		if !strings.Contains(line, "│") {
			t.Errorf("row %d has no side border: %q", i+1, line)
		}
	}
}

// TestMouseIsTranslatedIntoTheFrame covers the other half of the border: Tasks
// answers clicks from the geometry it was rendered at, so a host coordinate
// that is not shifted past the frame selects the wrong row.
func TestMouseIsTranslatedIntoTheFrame(t *testing.T) {
	p, _, _ := newConfigured(t)
	startAndSettle(t, p)
	p.View(80, 24)

	shifted, inside := p.offsetMouse(tea.MouseClickMsg{X: 10, Y: 5, Button: tea.MouseLeft})
	if !inside {
		t.Fatal("a click in the interior was dropped")
	}
	if got := shifted.Mouse(); got.X != 8 || got.Y != 4 {
		t.Errorf("translated to (%d,%d), want (8,4)", got.X, got.Y)
	}
	if _, ok := shifted.(tea.MouseClickMsg); !ok {
		t.Errorf("concrete type lost: %T", shifted)
	}

	// The frame itself belongs to no row: a press there is reported outside,
	// and Update drops it rather than folding it onto the nearest task.
	for _, pt := range []struct{ x, y int }{{0, 5}, {5, 0}, {79, 5}, {10, 23}} {
		if _, inside := p.offsetMouse(tea.MouseClickMsg{X: pt.x, Y: pt.y}); inside {
			t.Errorf("press on the frame at (%d,%d) reported inside", pt.x, pt.y)
		}
	}

	// A release at the far edge of a drag is still clamped into the interior:
	// Tasks clears its rail drag only on release, so swallowing one strands the
	// pane on the pointer for every gesture after it.
	release, _ := p.offsetMouse(tea.MouseReleaseMsg{X: 0, Y: 23})
	if got := release.Mouse(); got.X != 0 || got.Y != 21 {
		t.Errorf("release clamped to (%d,%d), want (0,21)", got.X, got.Y)
	}
	if _, ok := release.(tea.MouseReleaseMsg); !ok {
		t.Errorf("release type lost: %T", release)
	}
}
