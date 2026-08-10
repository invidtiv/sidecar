package tasks

import (
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	tasksui "github.com/marcus/tasks/pkg/tui"
)

// recordingKeymap captures what the plugin registers with sidecar's keymap.
type recordedBinding struct{ Key, Command, Context string }

type recordingKeymap struct {
	bindings []recordedBinding
}

func (r *recordingKeymap) RegisterPluginBinding(key, command, context string) {
	r.bindings = append(r.bindings, recordedBinding{Key: key, Command: command, Context: context})
}

// liveModel returns a started plugin bound to an isolated store, with a keymap
// that recorded its bindings.
func liveModel(t *testing.T) (*Plugin, *recordingKeymap) {
	t.Helper()

	_, env := configuredEnv(t)
	p := New()
	p.environment = env
	ctx := testContext(t)
	km := &recordingKeymap{}
	ctx.Keymap = km
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	startAndSettle(t, p)
	if p.model == nil {
		t.Fatalf("model should exist (%s)", p.unavailable)
	}
	t.Cleanup(p.Stop)
	return p, km
}

// press drives one key into the embedded model the way sidecar would.
func press(t *testing.T, p *Plugin, key tea.KeyPressMsg) {
	t.Helper()
	p.Update(key)
}

func TestBindingsAreRegisteredWithTheHostKeymap(t *testing.T) {
	_, km := liveModel(t)

	if len(km.bindings) == 0 {
		t.Fatal("no tasks bindings reached sidecar's keymap")
	}

	index := map[string]map[string]string{}
	for _, b := range km.bindings {
		if b.Key == "" {
			t.Errorf("registered a binding with no key: %+v", b)
		}
		if index[b.Context] == nil {
			index[b.Context] = map[string]string{}
		}
		index[b.Context][b.Key+"->"+b.Command] = b.Command
	}

	for _, want := range []struct{ context, key, command string }{
		{"tasks-list", "@", "open-context-palette"},
		{"tasks-list", "tab", "focus-prompt"},
		{"tasks-list", "M", "toggle-model"},
		{"tasks-list", "A", "open-agent-activity"},
		{"tasks-list", "1", "view-agenda"},
		{"tasks-list", "6", "view-inbox"},
		{"tasks-detail", "e", "start-task-edit"},
		{"tasks-modal", "enter", "modal-confirm-default"},
	} {
		if index[want.context][want.key+"->"+want.command] == "" {
			t.Errorf("binding %q -> %q missing from context %q", want.key, want.command, want.context)
		}
	}

	// Host-owned commands must never be advertised as Tasks bindings: sidecar
	// owns quit and the merged help.
	for _, b := range km.bindings {
		if hostOwnedCommands[b.Command] {
			t.Errorf("host-owned command %q was registered as a tasks binding (%+v)", b.Command, b)
		}
	}
}

func TestRoutingTableIsDerivedFromTheTasksRegistry(t *testing.T) {
	// Every exported context is known to the routing table, and it is
	// classified exactly once.
	for _, meta := range tasksui.ExportContexts() {
		name := string(meta.Name)
		if !IsTasksContext(name) {
			t.Errorf("%q is not recognised as a tasks context", name)
		}
		if IsTextInputContext(name) != meta.ConsumesTextInput {
			t.Errorf("%q text-input = %v, want %v (Tasks' own metadata)",
				name, IsTextInputContext(name), meta.ConsumesTextInput)
		}
		if meta.ConsumesTextInput && IsRootContext(name) {
			t.Errorf("%q both takes text input and lets q quit sidecar", name)
		}
	}

	roots := []string{}
	for _, meta := range tasksui.ExportContexts() {
		if IsRootContext(string(meta.Name)) {
			roots = append(roots, string(meta.Name))
		}
	}
	sort.Strings(roots)
	want := []string{"tasks-detail", "tasks-list", "tasks-response", "tasks-response-detail"}
	if strings.Join(roots, ",") != strings.Join(want, ",") {
		t.Errorf("root contexts = %v, want %v", roots, want)
	}

	// A context that binds q to something of its own is an overlay, not a root.
	if IsRootContext("tasks-modal") {
		t.Error("tasks-modal binds q to its own dismissal; sidecar must not quit there")
	}
	if IsTasksContext("git-status") {
		t.Error("git-status must not be mistaken for a tasks context")
	}
}

func TestClaimsKeyFollowsTheConflictTable(t *testing.T) {
	p, _ := liveModel(t)

	claimed := []string{"@", "tab", "M", "1", "2", "3", "4", "5", "6", "/"}
	for _, key := range claimed {
		if !p.ClaimsKey(key) {
			t.Errorf("Tasks should win %q in %s", key, p.FocusContext())
		}
	}

	// With a task actually selected, the selection-dependent keys are claimed
	// too. The fixture task is NEXT, so the Next view has a row to stand on.
	p.invoke("view-next")
	for _, key := range []string{"enter", "c", "j", "k"} {
		if !p.ClaimsKey(key) {
			t.Errorf("Tasks should win %q with a task selected in %s", key, p.FocusContext())
		}
	}

	// Sidecar keeps these whatever Tasks binds them to.
	for _, key := range []string{"q", "?", "ctrl+c"} {
		if p.ClaimsKey(key) {
			t.Errorf("Tasks must not claim the host-reserved key %q", key)
		}
	}

	// A key whose command is conditional is claimed exactly when Tasks says the
	// command can run — agent activity is the M/A pair's other half.
	if got, want := p.ClaimsKey("A"), p.available("open-agent-activity"); got != want {
		t.Errorf("ClaimsKey(A) = %v but availability says %v", got, want)
	}

	// A key Tasks does not bind at all falls through to sidecar.
	if p.ClaimsKey("ctrl+g") {
		t.Error("Tasks claimed a key it does not bind")
	}
}

// Availability, not just the presence of a binding, decides a claim. `r` is
// bound twice in the list context (reject a proposal, edit a recurrence) and
// neither is available with an ordinary task selected, so sidecar's refresh
// still gets the key.
func TestClaimsKeyRespectsCommandAvailability(t *testing.T) {
	p, _ := liveModel(t)

	if len(commandsForKey("tasks-list", "r")) < 2 {
		t.Fatal("test premise: r is no longer a conditional pair in the list context")
	}
	available := false
	for _, id := range commandsForKey("tasks-list", "r") {
		if p.available(id) {
			available = true
		}
	}
	if p.ClaimsKey("r") != available {
		t.Errorf("ClaimsKey(r) = %v but availability says %v", p.ClaimsKey("r"), available)
	}
}

func TestClaimsKeyWithoutAModel(t *testing.T) {
	p := New()
	for _, key := range []string{"@", "tab", "M"} {
		if p.ClaimsKey(key) {
			t.Errorf("a plugin with no model claimed %q", key)
		}
	}
	if p.BlocksGlobalKeys() {
		t.Error("a plugin with no model blocks nothing")
	}
	if !p.QuitKeyExits() {
		t.Error("with no model, q must still reach sidecar's quit flow")
	}
}

// Driving the model into its prompt is the case that matters: sidecar must see
// a text-input context, an overlay that blocks its globals, and a q that is not
// its own.
func TestPromptContextConsumesTextInput(t *testing.T) {
	p, _ := liveModel(t)

	if p.ConsumesTextInput() {
		t.Fatalf("the %s context should not consume text input", p.FocusContext())
	}
	if p.BlocksGlobalKeys() {
		t.Fatalf("the %s context should not block sidecar's globals", p.FocusContext())
	}
	if !p.QuitKeyExits() {
		t.Fatalf("q in %s should reach sidecar's quit flow", p.FocusContext())
	}

	press(t, p, tea.KeyPressMsg{Code: tea.KeyTab})

	if got := p.FocusContext(); got != string(tasksui.FocusPrompt) {
		t.Fatalf("FocusContext after tab = %q, want %q", got, tasksui.FocusPrompt)
	}
	if !p.ConsumesTextInput() {
		t.Fatal("the prompt must consume text input, or sidecar steals typed characters")
	}
	if !p.BlocksGlobalKeys() {
		t.Fatal("the prompt must block sidecar's global keys")
	}
	if p.QuitKeyExits() {
		t.Fatal("q belongs to the prompt while it is open")
	}

	// Typed characters reach the prompt rather than being swallowed.
	for _, r := range "buy milk" {
		press(t, p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if !strings.Contains(p.View(100, 30), "buy milk") {
		t.Errorf("typed text never reached the prompt:\n%s", p.View(100, 30))
	}
}

// The filter is the other text-input layer sidecar must not type over.
func TestFilterContextConsumesTextInput(t *testing.T) {
	p, _ := liveModel(t)

	press(t, p, tea.KeyPressMsg{Code: '/', Text: "/"})

	if got := p.FocusContext(); got != string(tasksui.FocusFilter) {
		t.Fatalf("FocusContext after / = %q, want %q", got, tasksui.FocusFilter)
	}
	if !p.ConsumesTextInput() {
		t.Fatal("the filter must consume text input")
	}
}

func TestInvokeRunsACommandByID(t *testing.T) {
	p, _ := liveModel(t)

	if got := string(p.model.CurrentView()); got != string(tasksui.ViewAgenda) {
		t.Fatalf("test premise: starting view = %q", got)
	}

	p.invoke("view-next")

	if got := string(p.model.CurrentView()); got != string(tasksui.ViewNext) {
		t.Errorf("view after invoking view-next = %q, want %q", got, tasksui.ViewNext)
	}
}

func TestInvokeAnUnknownCommandIsANoOp(t *testing.T) {
	p, _ := liveModel(t)

	if cmd := p.invoke("no-such-command"); cmd != nil {
		t.Error("an unknown command must not produce a command")
	}
	if got := string(p.model.CurrentView()); got != string(tasksui.ViewAgenda) {
		t.Errorf("an unknown command changed the view to %q", got)
	}
}

func TestCommandsCarryRunnableHandlers(t *testing.T) {
	p, _ := liveModel(t)

	var toggleModel *plugin.Command
	for i, cmd := range p.Commands() {
		if cmd.ID == "toggle-model" && cmd.Context == string(tasksui.FocusList) {
			toggleModel = &p.Commands()[i]
			break
		}
	}
	if toggleModel == nil {
		t.Fatal("the model selector (M) is missing from the palette projection")
	}
	if toggleModel.Handler == nil {
		t.Fatal("a palette entry with no handler cannot be run")
	}
	if toggleModel.Priority == 0 {
		t.Error("footer priority was dropped in the projection")
	}

	var agentActivity bool
	for _, cmd := range p.Commands() {
		if cmd.ID == "open-agent-activity" {
			agentActivity = true
		}
	}
	if !agentActivity {
		t.Error("agent activity (A) is missing from the palette projection")
	}

	// The handler runs the command; view-next is the one with a visible effect.
	for _, cmd := range p.Commands() {
		if cmd.ID == "view-next" && cmd.Context == string(tasksui.FocusList) {
			cmd.Handler()
		}
	}
	if got := string(p.model.CurrentView()); got != string(tasksui.ViewNext) {
		t.Errorf("running the palette handler did not switch the view (got %q)", got)
	}
}

func TestCommandsWithdrawHostOwnedEntries(t *testing.T) {
	for _, cmd := range New().Commands() {
		if hostOwnedCommands[cmd.ID] {
			t.Errorf("%q is sidecar's to offer, not Tasks'", cmd.ID)
		}
	}
}

// Commands for the live context are filtered by Tasks' own availability check,
// so the palette does not offer two meanings of one key at once.
func TestCommandsForTheLiveContextAreAvailable(t *testing.T) {
	p, _ := liveModel(t)

	current := p.FocusContext()
	seen := 0
	for _, cmd := range p.Commands() {
		if cmd.Context != current {
			continue
		}
		seen++
		if !p.available(cmd.ID) {
			t.Errorf("%q is offered in %s but Tasks says it is unavailable", cmd.ID, current)
		}
	}
	if seen == 0 {
		t.Fatalf("no commands were projected for the live context %q", current)
	}

	// Commands belonging to other contexts are still listed: they are the
	// palette's other layers, and availability only answers for the live focus.
	others := 0
	for _, cmd := range p.Commands() {
		if cmd.Context != current {
			others++
		}
	}
	if others == 0 {
		t.Error("the palette lost every command outside the current context")
	}
}

// Command metadata must be projected field for field. A test that only checks
// "something non-empty" survives Name and Description being swapped.
func TestCommandMetadataMatchesTheTasksRegistry(t *testing.T) {
	exported := map[string]tasksui.Command{}
	for _, cmd := range tasksui.ExportCommands() {
		exported[cmd.ID+":"+string(cmd.Context)] = cmd
	}

	checked := 0
	for _, cmd := range New().Commands() {
		source, ok := exported[cmd.ID+":"+cmd.Context]
		if !ok {
			t.Errorf("%q@%s is not a Tasks command", cmd.ID, cmd.Context)
			continue
		}
		checked++
		if cmd.Name != source.FooterLabel {
			t.Errorf("%q Name = %q, want the footer label %q", cmd.ID, cmd.Name, source.FooterLabel)
		}
		if cmd.Description != source.Description {
			t.Errorf("%q Description = %q, want %q", cmd.ID, cmd.Description, source.Description)
		}
		if cmd.Name == cmd.Description && source.FooterLabel != source.Description {
			t.Errorf("%q collapsed name and description onto one value", cmd.ID)
		}
		if cmd.Priority != source.FooterPriority {
			t.Errorf("%q Priority = %d, want %d", cmd.ID, cmd.Priority, source.FooterPriority)
		}
	}
	if checked == 0 {
		t.Fatal("no commands were checked")
	}

	// Spot-check one command end to end so a wholesale re-labelling in Tasks
	// cannot pass by agreeing with itself.
	for _, cmd := range New().Commands() {
		if cmd.ID == "focus-prompt" && cmd.Context == string(tasksui.FocusList) {
			if cmd.Name != "Ask" {
				t.Errorf("focus-prompt footer label = %q, want \"Ask\"", cmd.Name)
			}
			if !strings.Contains(cmd.Description, "ask the agent") {
				t.Errorf("focus-prompt description = %q", cmd.Description)
			}
		}
	}
}

// categorize must actually discriminate: a constant would still give every
// command "a category".
func TestCategorizeDiscriminatesByContext(t *testing.T) {
	cases := map[tasksui.FocusContext]plugin.Category{
		tasksui.FocusList:                plugin.CategoryNavigation,
		tasksui.FocusDetail:              plugin.CategoryNavigation,
		tasksui.FocusResponseDetail:      plugin.CategoryNavigation,
		tasksui.FocusFilter:              plugin.CategorySearch,
		tasksui.FocusModalFilter:         plugin.CategorySearch,
		tasksui.FocusAgentActivityFilter: plugin.CategorySearch,
		tasksui.FocusTaskEdit:            plugin.CategoryEdit,
		tasksui.FocusForm:                plugin.CategoryEdit,
		tasksui.FocusModal:               plugin.CategoryActions,
		tasksui.FocusPicker:              plugin.CategoryActions,
	}
	for context, want := range cases {
		if got := categorize(context); got != want {
			t.Errorf("categorize(%s) = %q, want %q", context, got, want)
		}
	}

	distinct := map[plugin.Category]bool{}
	for _, cmd := range New().Commands() {
		distinct[cmd.Category] = true
	}
	if len(distinct) < 3 {
		t.Errorf("the projection produced %d distinct categories; grouping is meaningless", len(distinct))
	}
}

func TestFooterStatusCarriesTheStoreReadError(t *testing.T) {
	p := New()
	p.environment = brokenStoreEnv(t)
	if err := p.Init(testContext(t)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Stop()
	startAndSettle(t, p)
	if p.loadError == nil {
		t.Fatal("test premise: the broken store reported no read error")
	}

	text, isError := p.FooterStatus()
	if !strings.Contains(text, "cannot read the task store") {
		t.Errorf("FooterStatus = %q, want the store-read failure", text)
	}
	if !isError {
		t.Error("a store sidecar cannot read is an error, not a note")
	}
}

func TestFooterStatusIsSilentWhenHealthy(t *testing.T) {
	p, _ := liveModel(t)

	if text, isError := p.FooterStatus(); text != "" || isError {
		t.Errorf("FooterStatus = (%q, %v), want silence", text, isError)
	}
	if text, _ := New().FooterStatus(); text != "" {
		t.Errorf("a plugin with no model reported %q", text)
	}
}

// TestSuppressFooterStillHidesThePrompt pins the Tasks-side gap that keeps
// EmbeddedOptions.SuppressFooter false in buildModel.
//
// Packet 1.3 wants Tasks to stop painting its key-hint row now that sidecar
// renders one. Tasks' switch is all-or-nothing: the same Footer() call renders
// the prompt input, the agent transcript, the store-read banner, and the filter
// lines. Suppressing it makes `tab` focus an invisible caret. When Tasks grows a
// finer control, this test fails, and that is the signal to flip the option.
func TestSuppressFooterStillHidesThePrompt(t *testing.T) {
	_, env := configuredEnv(t)

	build := func(suppress bool) *tasksui.Model {
		t.Helper()
		model, err := tasksui.NewEmbedded(tasksui.EmbeddedOptions{
			SessionNamespace: "sidecar-test",
			SuppressFooter:   suppress,
			SuppressQuit:     true,
			Environment:      env,
		})
		if err != nil {
			t.Fatalf("NewEmbedded(suppress=%v): %v", suppress, err)
		}
		t.Cleanup(func() { _ = model.Discard() })
		return model
	}

	const promptAffordance = "tab to ask the agent"

	if view := build(false).View(100, 30); !strings.Contains(view, promptAffordance) {
		t.Fatalf("test premise: Tasks' own footer no longer carries the prompt:\n%s", view)
	}
	if view := build(true).View(100, 30); strings.Contains(view, promptAffordance) {
		t.Fatal("Tasks now keeps the prompt under SuppressFooter: flip " +
			"EmbeddedOptions.SuppressFooter to true in buildModel and delete this test")
	}
}
