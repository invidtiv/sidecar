package tasks

import (
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

	return &plugin.Context{
		WorkDir: t.TempDir(),
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
	_ = ready.Model.Close()
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

// TestStaleReadyIsClosedAndDiscarded covers a project switch landing while the
// build is in flight. Dropping the model without closing it would leak its
// agent queue, so the test asserts closure by its observable effect: Close
// writes the embedded session file.
func TestStaleReadyIsClosedAndDiscarded(t *testing.T) {
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
	if _, err := os.Stat(session); err != nil {
		t.Errorf("discarded model was not closed: %v", err)
	}
}

// TestReadyAfterStopIsClosedAndDiscarded is the same race, but where Stop()
// already tore the plugin down.
func TestReadyAfterStopIsClosedAndDiscarded(t *testing.T) {
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
	if _, err := os.Stat(session); err != nil {
		t.Errorf("discarded model was not closed: %v", err)
	}
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

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Error("nested quit escaped to the host")
		}
	}
	if p.model.QuitRequested() {
		t.Error("quit request should be cleared, not left latched")
	}
	if p.model == nil {
		t.Error("a nested quit must not close the embedded model")
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

// TestInitClosesALiveModel covers S3. Registry.Reinit normally stops first, but
// safeStop recovers panics, so a Close() that panicked leaves a live model in
// place and the next Init would drop it — agent queue still running.
func TestInitClosesALiveModel(t *testing.T) {
	p, root, ctx := newConfigured(t)
	startAndSettle(t, p)
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	session := filepath.Join(root, "state", "tasks", "hosts", sessionNamespace, "tui.json")
	if _, err := os.Stat(session); err == nil {
		t.Fatal("session existed before any close")
	}

	// Init without a preceding Stop, the shape safeStop's panic recovery leaves.
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.model != nil {
		t.Error("Init should not leave the old model installed")
	}
	if _, err := os.Stat(session); err != nil {
		t.Errorf("Init dropped a live model without closing it: %v", err)
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

// TestFooterIsNotSuppressedYet pins the Packet 1.1 scope correction (S9):
// suppressing Tasks' footer belongs with sidecar's unified footer in 1.3, and
// shipping it early leaves the tab with no key hints at all.
func TestFooterIsNotSuppressedYet(t *testing.T) {
	p, _, _ := newConfigured(t)
	startAndSettle(t, p)
	defer p.Stop()
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}

	view := p.View(100, 30)
	for _, hint := range []string{"j/k move", "search"} {
		if !strings.Contains(view, hint) {
			t.Errorf("tasks tab has no key hints (missing %q); with sidecar's "+
				"unified footer still unbuilt, suppressing Tasks' own footer "+
				"leaves the tab with no footer at all", hint)
		}
	}
}

func TestDiagnosticsBeforeStart(t *testing.T) {
	diags := New().Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Status != "disabled" {
		t.Errorf("status = %q, want \"disabled\"", diags[0].Status)
	}
}
