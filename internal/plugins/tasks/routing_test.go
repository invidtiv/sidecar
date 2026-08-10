package tasks

import (
	"slices"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/keymap"
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

	// Keys the host structurally refuses must not be advertised at all. The
	// footer and the merged help are built from registered bindings, so a
	// binding here is a hint that lies: `1`-`6` switch sidecar tabs, `#` opens
	// the theme switcher, `q` quits.
	for _, b := range km.bindings {
		if registerableKey(b.Context, b.Key) {
			continue
		}
		t.Errorf("key %q reaches sidecar, not tasks, yet was registered for %q in %q",
			b.Key, b.Command, b.Context)
	}
}

// TestRefusedKeysAreNotRegisteredButStayReachable pins the two halves of the
// rule together: the keys sidecar keeps do not appear as Tasks bindings, and
// the commands behind them are still exported as commands (and so still reach
// the palette).
func TestRefusedKeysAreNotRegisteredButStayReachable(t *testing.T) {
	_, km := liveModel(t)

	// The keys sidecar keeps for itself, and the Tasks commands that used to
	// hide behind them in a root context.
	refused := []struct{ key, command string }{
		{"1", "view-agenda"},
		{"2", "view-next"},
		{"3", "view-quadrants"},
		{"4", "view-projects"},
		{"5", "view-outline"},
		{"6", "view-inbox"},
		{"K", "raise-priority"},
		{"W", "set-work-ref-selected"},
		{"#", "delete-selected"},
		{"q", "quit"},
		{"?", "open-help"},
	}

	for _, b := range km.bindings {
		if !IsRootContext(b.Context) {
			continue
		}
		for _, r := range refused {
			if b.Key == r.key {
				t.Errorf("refused key %q registered for %q in root context %q", b.Key, b.Command, b.Context)
			}
		}
	}

	// ...and the commands are still exported, so the palette can carry them
	// keyless. quit/open-help are excluded: those are hostOwnedCommands, which
	// sidecar deliberately does not adopt at all.
	exported := map[string]bool{}
	for _, cmd := range tasksui.ExportCommands() {
		exported[cmd.ID] = true
	}
	for _, r := range refused {
		if hostOwnedCommands[r.command] {
			continue
		}
		if !exported[r.command] {
			t.Errorf("command %q lost its key AND its palette entry", r.command)
		}
	}
}

// TestOverlayContextsKeepEveryBinding pins the other half of registerableKey:
// an overlay owns the keyboard (precedence level 2 forwards all but ctrl+c), so
// `q` in a Tasks modal really does cancel it and must stay advertised.
func TestOverlayContextsKeepEveryBinding(t *testing.T) {
	_, km := liveModel(t)

	want := map[string]bool{"modal-cancel-q": true, "close-modal": true}
	found := map[string]bool{}
	for _, b := range km.bindings {
		if b.Context == "tasks-modal" && b.Key == "q" {
			found[b.Command] = true
		}
		if b.Key == "ctrl+c" {
			t.Errorf("ctrl+c is never routable to a plugin, yet was registered for %q in %q", b.Command, b.Context)
		}
	}
	for command := range want {
		if !found[command] {
			t.Errorf("overlay binding q -> %q was dropped from tasks-modal", command)
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

	// Anything not on the root allow-list is an overlay, and the host must not
	// quit out from under one.
	if IsRootContext("tasks-modal") {
		t.Error("tasks-modal is an overlay; sidecar must not quit there")
	}
	if IsTasksContext("git-status") {
		t.Error("git-status must not be mistaken for a tasks context")
	}
}

func TestClaimsKeyFollowsTheConflictTable(t *testing.T) {
	p, _ := liveModel(t)

	claimed := []string{"@", "tab", "M", "/", "left", "right"}
	for _, key := range claimed {
		if !p.ClaimsKey(key) {
			t.Errorf("Tasks should win %q in %s", key, p.FocusContext())
		}
	}

	// The number row is sidecar's, revised after live use: tab switching by
	// number is muscle memory everywhere else, so it may not mean "switch Tasks
	// view" here. `←`/`→` above are what Tasks keeps for that.
	for _, key := range []string{"1", "2", "3", "4", "5", "6"} {
		if p.ClaimsKey(key) {
			t.Errorf("Tasks must not claim %q: it switches sidecar tabs", key)
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

// TestTheTabPaintsNoSecondKeyHintRow pins the end state Packet 1.3 asked for:
// sidecar owns the key-hint row, so the embedded Tasks model must not paint one
// of its own — and must still paint everything else in its footer stack.
//
// This is the counterpart to Tasks' own SuppressKeyHints contract test: it
// asserts that sidecar actually asks for it, from the plugin's rendered tab
// rather than from a hand-built EmbeddedOptions.
func TestTheTabPaintsNoSecondKeyHintRow(t *testing.T) {
	p, _ := liveModel(t)

	frame := p.View(100, 30)
	if strings.Contains(frame, "j/k") {
		t.Fatalf("Tasks painted its own key hint row under sidecar's footer:\n%s", frame)
	}
	// The rest of the footer stack is not sidecar's to erase: the prompt is
	// what `tab` focuses, and suppressing the whole footer made it an
	// invisible caret.
	if !strings.Contains(frame, "tab to ask the agent") {
		t.Fatalf("the prompt row is gone from the tab:\n%s", frame)
	}

	press(t, p, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := p.FocusContext(); got != string(tasksui.FocusPrompt) {
		t.Fatalf("tab left focus at %q, want the prompt", got)
	}
	press(t, p, tea.KeyPressMsg{Code: 'x', Text: "unified footer"})

	frame = p.View(100, 30)
	if !strings.Contains(frame, "unified footer") {
		t.Fatalf("the focused prompt does not show what was typed into it:\n%s", frame)
	}
	if strings.Contains(frame, "j/k") {
		t.Fatalf("the key hint row came back once the prompt was focused:\n%s", frame)
	}
}

// claimedGlobals lists, in order, every key sidecar binds globally that Tasks
// claims in its current context.
func claimedGlobals(p *Plugin) []string {
	var claimed []string
	for key := range keymap.GlobalKeys {
		if p.ClaimsKey(key) {
			claimed = append(claimed, key)
		}
	}
	sort.Strings(claimed)
	return claimed
}

// TestTheClaimedGlobalsDoNotDependOnTheSelection is the W2 fix.
//
// ClaimsKey used to be availability-aware for every key, so in the SAME
// tasks-list view it claimed [1 2 3 4 5 6 @] with nothing selected and
// [1 2 3 4 5 6 @ K W #] with something selected. `K`, `W` and `#` therefore
// meant sidecar's Overview, worktree switcher and theme switcher until you
// selected a task, at which point the same keys became raise-priority,
// set-work-ref and delete-selected. A key whose meaning depends on the
// selection is not a mapping anyone chose, and one of those meanings is
// destructive.
func TestTheClaimedGlobalsDoNotDependOnTheSelection(t *testing.T) {
	p, _ := liveModel(t)

	before := p.FocusContext()
	empty := claimedGlobals(p)

	// The fixture task is NEXT, so this view has a row to select.
	p.invoke("view-next")
	if got := p.FocusContext(); got != before {
		t.Fatalf("the context changed from %q to %q; this test compares one context", before, got)
	}
	if !p.available("delete-selected") {
		t.Fatal("test premise: nothing is selected, so there is no shadowing to test")
	}
	selected := claimedGlobals(p)

	if strings.Join(empty, " ") != strings.Join(selected, " ") {
		t.Errorf("the claimed sidecar globals in %s change with the selection:\n"+
			"  nothing selected: %v\n  task selected:    %v", before, empty, selected)
	}

	// And the set is the one the conflict table decided, as revised: `@` alone.
	want := []string{"@"}
	if strings.Join(selected, " ") != strings.Join(want, " ") {
		t.Errorf("claimed globals = %v, want the conflict table's %v", selected, want)
	}
}

// TestSidecarKeepsTheGlobalsTheConflictTableNeverGaveAway states the rule: a
// Tasks binding may shadow a sidecar global only if the plan's conflict table
// decided that collision. `K`, `W` and `#` were never in it — they collide by
// accident — so sidecar wins them and the Tasks commands stay reachable through
// `?` and the palette.
func TestSidecarKeepsTheGlobalsTheConflictTableNeverGaveAway(t *testing.T) {
	p, _ := liveModel(t)
	p.invoke("view-next")

	for _, key := range []string{"K", "W", "#"} {
		commands := commandsForKey("tasks-list", key)
		if len(commands) == 0 {
			t.Fatalf("test premise: Tasks no longer binds %q in the list context", key)
		}
		if p.ClaimsKey(key) {
			t.Errorf("Tasks claimed %q (%v); that collision was never decided, so sidecar keeps it",
				key, commands)
		}
		// The command is not lost, only unbound from that key.
		if !slices.ContainsFunc(p.Commands(), func(c plugin.Command) bool { return c.ID == commands[0] }) {
			t.Errorf("%q is unreachable: sidecar keeps the key and the palette does not offer %q",
				commands[0], commands[0])
		}
	}
}

// The specific accident worth its own test: `#` is sidecar's theme switcher and
// Tasks' delete-selected. A user reaching for the theme switcher must not get a
// task deleted.
func TestHashNeverDeletesATask(t *testing.T) {
	p, _ := liveModel(t)
	p.invoke("view-next")

	if got := commandsForKey("tasks-list", "#"); len(got) != 1 || got[0] != "delete-selected" {
		t.Fatalf("test premise: # in the list context now binds %v", got)
	}
	if !p.available("delete-selected") {
		t.Fatal("test premise: delete-selected cannot run, so nothing could be deleted anyway")
	}
	if !keymap.GlobalKeys["#"] {
		t.Fatal("test premise: sidecar no longer binds # globally")
	}
	if p.ClaimsKey("#") {
		t.Fatal("# is claimed by Tasks; sidecar's theme switcher would delete the selected task")
	}
}

// TestRootContextsAreAnAllowListThatFailsSafe is the W5 fix.
//
// Root-ness used to be inferred from "does not bind `q`", which classified any
// future non-text-input overlay that dismisses with `esc` (a preview, a diff
// viewer, a y/n confirm) as a root context — so sidecar's globals would fire
// underneath it and `q` would pop the quit confirmation on top of it. It is an
// allow-list now, so anything unknown is an overlay.
func TestRootContextsAreAnAllowListThatFailsSafe(t *testing.T) {
	if missing := contextsAreKnown(rootContexts); len(missing) != 0 {
		t.Fatalf("rootContexts names contexts Tasks no longer exports: %v; "+
			"they are being treated as overlays, which is safe but wrong", missing)
	}

	// The context Tasks might add tomorrow.
	for _, unknown := range []string{"tasks-diff-preview", "tasks-confirm", "tasks", ""} {
		if IsRootContext(unknown) {
			t.Errorf("unknown context %q was classified as root; sidecar globals would "+
				"fire underneath it", unknown)
		}
	}

	// And a plugin sitting in one blocks global keys, which is the behaviour
	// the classification exists to drive.
	p, _ := liveModel(t)
	if p.BlocksGlobalKeys() {
		t.Fatal("test premise: the list context should not block global keys")
	}
	p.model = nil
	if p.BlocksGlobalKeys() {
		t.Fatal("a plugin with no model blocks nothing")
	}
}

// TestRegisteringBindingsTwiceIsANoOp is the W4 fix. registerBindings runs from
// adoptModel, which runs on every project switch, while the host keymap lives
// for the process lifetime.
func TestRegisteringBindingsTwiceIsANoOp(t *testing.T) {
	p, _ := liveModel(t)

	registry := keymap.NewRegistry()
	p.ctx.Keymap = registry
	p.registerBindings()

	total := func() int {
		count := 0
		for _, context := range []string{
			"tasks-list", "tasks-detail", "tasks-modal", "tasks-prompt", "tasks-filter",
			"tasks-form", "tasks-picker", "tasks-context-picker", "tasks-task-edit",
			"tasks-modal-filter", "tasks-response", "tasks-response-detail",
			"tasks-agent-activity", "tasks-agent-activity-filter",
		} {
			count += len(registry.BindingsForContext(context))
		}
		return count
	}

	first := total()
	if first == 0 {
		t.Fatal("no bindings were registered")
	}

	p.registerBindings()
	p.registerBindings()

	if again := total(); again != first {
		t.Fatalf("bindings accumulated across re-registration: %d then %d", first, again)
	}
}

// TestTheViewCommandsSurviveLosingTheNumberRow is the other half of the
// revision that gave `1`-`6` back to sidecar's tab switcher: giving up the keys
// must not cost the commands. Every Tasks view stays in the palette projection
// with a handler that runs it, and `←`/`→` still step between them.
func TestTheViewCommandsSurviveLosingTheNumberRow(t *testing.T) {
	p, _ := liveModel(t)

	views := []string{"view-agenda", "view-next", "view-quadrants", "view-projects", "view-outline", "view-inbox"}
	for _, id := range views {
		var found *plugin.Command
		for i, cmd := range p.Commands() {
			if cmd.ID == id && cmd.Context == string(tasksui.FocusList) {
				found = &p.Commands()[i]
				break
			}
		}
		if found == nil {
			t.Errorf("%q is missing from the palette projection", id)
			continue
		}
		if found.Handler == nil {
			t.Errorf("%q has no handler, so the palette cannot run it", id)
		}
	}

	// The keys Tasks keeps for its views. `←`/`→` are not sidecar globals, so
	// nothing upstream can take them.
	for _, key := range []string{"left", "right"} {
		if keymap.GlobalKeys[key] {
			t.Fatalf("test premise: sidecar now binds %q globally", key)
		}
		if commands := commandsForKey(string(tasksui.FocusList), key); len(commands) == 0 {
			t.Errorf("Tasks no longer binds %q in the list context", key)
		}
		if !p.ClaimsKey(key) {
			t.Errorf("Tasks does not claim %q, so its views lost their last key", key)
		}
	}

	// And the numbers are sidecar's again, in every root context.
	for context := range rootContexts {
		for _, key := range []string{"1", "2", "3", "4", "5", "6"} {
			if mayShadowGlobal(key) {
				t.Errorf("%q may still shadow sidecar's tab switcher (context %s)", key, context)
			}
		}
	}
}
