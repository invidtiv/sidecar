package configui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configchecks"
)

// key presses the surface the way the host does.
func key(m *Model, name string) tea.Cmd {
	var msg tea.KeyPressMsg
	switch name {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	default:
		msg = tea.KeyPressMsg{Code: rune(name[0]), Text: name}
	}
	_, cmd := m.Key(msg)
	return cmd
}

// render paints a frame, which is also what rebuilds the pane's controls.
func render(m *Model) string { return ansi.Strip(m.View(160, 45)) }

// collect runs a command and every command it batches, returning the messages.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, inner := range batch {
			out = append(out, collect(inner)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func problemResults() configchecks.Results {
	return configchecks.Results{
		{ID: configchecks.CheckTerminalColors, Title: "Terminal colors", OK: true, Summary: "Truecolor available"},
		{
			ID: configchecks.CheckTmux, Title: "tmux", OK: false,
			Summary: "Not found on PATH · workspaces need tmux 3.4+", Evidence: []string{"tmux was not found on PATH."},
			Action: "Set up tmux", ActionDetail: "Workspaces and embedded shells need tmux 3.4+",
			Badge: configchecks.BadgeFix, Repair: configchecks.RepairTmux,
		},
		{ID: configchecks.CheckConfiguration, Title: "Configuration", OK: true, Summary: "Readable and valid"},
		{
			ID: configchecks.CheckProjects, Title: "Projects", OK: false,
			Summary: "No projects configured · add a project to get started",
			Action:  "Add a project", ActionDetail: "Tell Sidecar where your code lives",
			Badge: configchecks.BadgeAdd, Repair: configchecks.RepairAddProject,
		},
		{
			ID: configchecks.CheckAgentInstructions, Title: "Agent instructions", OK: true,
			Summary: "AGENTS.md connected", Badge: configchecks.BadgeOpen, Repair: configchecks.RepairAgentInstructions,
		},
	}
}

func healthyResults() configchecks.Results {
	results := problemResults()
	for i := range results {
		results[i].OK = true
		results[i].Summary = "Ready"
		if results[i].ID != configchecks.CheckAgentInstructions {
			results[i].Badge = ""
			results[i].Repair = configchecks.RepairNone
		}
	}
	return results
}

func seeded(t *testing.T, page PageID, results configchecks.Results) *Model {
	t.Helper()
	m := New()
	m.Open(page)
	m.SetInstallEnvironment(stubEnvironment(nil))
	m.SetCheckInput(configchecks.Input{Config: &config.Config{}})
	m.ApplyChecks(ChecksMsg{Results: results})
	render(m)
	return m
}

func TestSetupListsOnlyMeaningfulWork(t *testing.T) {
	m := seeded(t, PageSetup, problemResults())
	view := render(m)

	if !strings.Contains(view, "Needs attention") {
		t.Fatalf("Setup did not list the work:\n%s", view)
	}
	for _, want := range []string{"FIX", "Add a project", "Set up tmux", "Looking good", "Terminal colors"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Setup is missing %q:\n%s", want, view)
		}
	}
	// A healthy check never appears as work.
	needs := view[strings.Index(view, "Needs attention"):strings.Index(view, "Looking good")]
	if strings.Contains(needs, "Terminal colors") {
		t.Fatalf("a healthy check was listed as work:\n%s", needs)
	}
}

func TestSetupSaysSoWhenEverythingIsFine(t *testing.T) {
	m := seeded(t, PageSetup, healthyResults())
	view := render(m)
	if strings.Contains(view, "Needs attention") || strings.Contains(view, "FIX") {
		t.Fatalf("an all-healthy Setup still showed work:\n%s", view)
	}
	if !strings.Contains(view, "Sidecar is ready to work.") {
		t.Fatalf("an all-healthy Setup did not say so:\n%s", view)
	}
	if !strings.Contains(view, "Looking good") {
		t.Fatalf("an all-healthy Setup dropped its confirmation:\n%s", view)
	}
}

func TestSetupBeforeChecksCompleteSaysItIsChecking(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	view := render(m)
	if !strings.Contains(view, "Checking your setup") {
		t.Fatalf("Setup did not show a loading state before results arrived:\n%s", view)
	}
	if len(m.cursorControls()) != 0 {
		t.Fatalf("Setup offered rows to select before it had results: %#v", m.controls)
	}
}

// A check run that never completed is exactly when Recheck matters, so R is a
// control on the loading state too.
func TestSetupRecheckWorksBeforeChecksComplete(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	render(m)

	handled, cmd := m.Key(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !handled {
		t.Fatal("R was inert while Setup was still checking")
	}
	if cmd == nil {
		t.Fatal("R raised no recheck command while Setup was still checking")
	}
	if !m.checking {
		t.Fatal("R did not start a check run")
	}
}

// Recheck is a control the user can see and click, not only a key that happens
// to be bound. It also says what it re-checks, so pressing it is not a guess.
func TestSetupRecheckIsVisibleAndClickable(t *testing.T) {
	m := seeded(t, PageSetup, problemResults())
	view := render(m)
	if !strings.Contains(view, "R  Recheck") {
		t.Fatalf("Setup painted no Recheck control:\n%s", view)
	}
	if !strings.Contains(view, "Looks again at tmux") {
		t.Fatalf("Setup's Recheck control did not say what it re-checks:\n%s", view)
	}

	clickable := false
	for _, region := range m.mouse.HitMap.Regions() {
		if region.ID == "setup-recheck" && region.Rect.W > 0 {
			clickable = true
		}
	}
	if !clickable {
		t.Fatalf("Setup's Recheck control has no mouse target: %#v", m.mouse.HitMap.Regions())
	}
	// Clicking that target is what a mouse user does, and it runs the recheck.
	if cmd := m.runControl(indexOfControl(m, "setup-recheck")); cmd == nil {
		t.Fatal("the Recheck control raised no command")
	}
	if !m.checking {
		t.Fatal("the Recheck control did not start a check run")
	}
}

// The hint shortens to fit, but it never disappears: a bare pill with nothing
// beside it is the unexplained control this page was fixed to stop showing, and
// 60 columns is as narrow as the app goes.
func TestSetupRecheckKeepsItsExplanationAtEveryWidth(t *testing.T) {
	m := seeded(t, PageSetup, problemResults())
	for _, width := range []int{160, 120, 100, 80, 70, 60} {
		view := ansi.Strip(m.View(width, 30))
		if !strings.Contains(view, "R  Recheck") {
			t.Fatalf("the Recheck pill was clipped at %d columns:\n%s", width, view)
		}
		var line string
		for _, candidate := range strings.Split(view, "\n") {
			if strings.Contains(candidate, "R  Recheck") {
				line = candidate
			}
		}
		// Everything left of the pill, back to the detail pane's own border, so
		// the sidebar beside it cannot stand in for the hint.
		left := line[:strings.Index(line, "R  Recheck")]
		if border := strings.LastIndex(left, "│"); border >= 0 {
			left = left[border+len("│"):]
		}
		if hint := strings.TrimSpace(left); hint == "" {
			t.Fatalf("the Recheck control lost its explanation at %d columns:\n%s", width, line)
		}
	}
}

func indexOfControl(m *Model, id string) int {
	for i, c := range m.controls {
		if c.id == id {
			return i
		}
	}
	return -1
}

func TestSetupRowOpensRepairAndBackRestoresTheParent(t *testing.T) {
	m := seeded(t, PageSetup, problemResults())

	// Tab moves into the page's own controls, Down selects the second item.
	key(m, "tab")
	render(m)
	key(m, "down")
	render(m)
	if m.rowCursor != 1 {
		t.Fatalf("row cursor = %d", m.rowCursor)
	}

	key(m, "enter")
	view := render(m)
	if route := m.Route(); route.Child != ChildRepairTmux {
		t.Fatalf("Enter on the tmux row opened %#v", route)
	}
	if m.Page() != PageSetup {
		t.Fatalf("the repair moved the sidebar destination to %q", m.Page())
	}
	if !strings.Contains(view, "Back to Sidecar Setup") {
		t.Fatalf("the repair has no parent-return control:\n%s", view)
	}

	if !m.Back() {
		t.Fatal("Back refused to return from the repair")
	}
	render(m)
	if m.Route().IsChild() {
		t.Fatalf("Back left a child route open: %#v", m.Route())
	}
	if m.rowCursor != 1 || !m.detailFocus {
		t.Fatalf("Back did not restore the parent's selection: cursor=%d focus=%v", m.rowCursor, m.detailFocus)
	}
}

func TestTmuxRepairPrefillsAndNeverRuns(t *testing.T) {
	m := seeded(t, PageSetup, problemResults())
	m.SetCheckInput(configchecks.Input{Env: configchecks.Env{
		Getenv:   func(string) string { return "" },
		LookPath: func(name string) (string, error) { return "/opt/homebrew/bin/brew", nil },
		GOOS:     "darwin",
	}})
	m.OpenRepair(configchecks.RepairTmux)
	view := render(m)

	for _, want := range []string{"brew install tmux", "never run automatically", "Open install shell", "Copy command", "Recheck"} {
		if !strings.Contains(view, want) {
			t.Fatalf("tmux repair is missing %q:\n%s", want, view)
		}
	}
	msgs := collect(key(m, "enter"))
	if len(msgs) != 1 {
		t.Fatalf("Enter produced %d messages", len(msgs))
	}
	open, ok := msgs[0].(OpenShellMsg)
	if !ok || open.Command != "brew install tmux" {
		t.Fatalf("Enter did not ask for a prefilled shell: %#v", msgs[0])
	}
	if strings.Contains(open.Command, "sudo") {
		t.Fatalf("the prefilled command used sudo: %q", open.Command)
	}

	msgs = collect(key(m, "c"))
	copyMsg, ok := msgs[0].(CopyMsg)
	if !ok || copyMsg.Text != "brew install tmux" {
		t.Fatalf("C did not copy the command: %#v", msgs[0])
	}
}

func TestColorRepairShowsTerminalSpecificSteps(t *testing.T) {
	m := seeded(t, PageSetup, problemResults())
	m.SetCheckInput(configchecks.Input{Env: configchecks.Env{
		Getenv: func(name string) string {
			if name == "TERM_PROGRAM" {
				return "Apple_Terminal"
			}
			return ""
		},
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		GOOS:     "darwin",
	}})
	m.OpenRepair(configchecks.RepairTerminalColors)
	view := render(m)
	if !strings.Contains(view, "Terminal.app") {
		t.Fatalf("the repair did not name the detected terminal:\n%s", view)
	}
	if strings.Contains(view, "does not support 24-bit color") {
		t.Fatalf("the steps were shown before the user asked for them:\n%s", view)
	}

	key(m, "enter")
	view = render(m)
	if !strings.Contains(view, "does not support 24-bit color") {
		t.Fatalf("Enter did not reveal the color setup steps:\n%s", view)
	}

	msgs := collect(key(m, "c"))
	if _, ok := msgs[0].(CopyMsg); !ok {
		t.Fatalf("C did not copy the instructions: %#v", msgs[0])
	}
}

func TestAgentInstructionsRequireConfirmationBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	original := "---\ntitle: demo\n---\n\n# Demo\n\nKeep me.\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	results := problemResults()
	for i := range results {
		if results[i].ID == configchecks.CheckAgentInstructions {
			results[i].OK = false
			results[i].Summary = "AGENTS.md needs Sidecar guidance"
			results[i].Action = "Connect agent instructions"
		}
	}
	m := seeded(t, PageDiagnostics, results)
	m.SetCheckInput(configchecks.Input{ProjectDir: dir, ProjectName: "demo"})
	m.OpenAgentInstructions()
	view := render(m)
	if !strings.Contains(view, "Review guidance") {
		t.Fatalf("the agent repair has no review action:\n%s", view)
	}

	// Review shows the exact addition and writes nothing.
	key(m, "enter")
	view = render(m)
	if m.confirm == nil {
		t.Fatal("Review did not open a confirmation")
	}
	if m.FocusContext() != ContextConfigConfirm {
		t.Fatalf("focus context during confirmation = %q", m.FocusContext())
	}
	if !strings.Contains(view, configchecks.AgentInstructionLine) {
		t.Fatalf("the confirmation did not show the exact addition:\n%s", view)
	}
	if current, _ := os.ReadFile(path); string(current) != original {
		t.Fatalf("reviewing changed the file:\n%s", current)
	}

	// Escape answers no, and still nothing is written.
	if !m.DismissConfirm() {
		t.Fatal("DismissConfirm refused to cancel")
	}
	if current, _ := os.ReadFile(path); string(current) != original {
		t.Fatalf("cancelling changed the file:\n%s", current)
	}

	// Confirming writes, preserving frontmatter and existing content.
	render(m)
	key(m, "enter")
	render(m)
	collect(key(m, "y"))
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	if !strings.HasPrefix(text, "---\ntitle: demo") || !strings.Contains(text, "Keep me.") {
		t.Fatalf("the write did not preserve the file:\n%s", text)
	}
	if !strings.Contains(text, configchecks.AgentInstructionLine) {
		t.Fatalf("the confirmed addition was not written:\n%s", text)
	}
}

func TestDiagnosticsBadgesAndRoutes(t *testing.T) {
	m := seeded(t, PageDiagnostics, problemResults())
	view := render(m)

	for _, want := range []string{"Environment", "Data", "R  Recheck", "FIX", "ADD", "OPEN"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Diagnostics is missing %q:\n%s", want, view)
		}
	}
	// A healthy, non-navigable row stays quiet: no badge, no cursor stop.
	for _, c := range m.controls {
		if c.id == "diagnostics-"+string(configchecks.CheckConfiguration) {
			t.Fatal("a healthy Configuration row was rendered as a control")
		}
	}
	// Agent instructions is navigable even though it is healthy.
	found := false
	for _, c := range m.controls {
		if c.id == "diagnostics-"+string(configchecks.CheckAgentInstructions) {
			found = true
		}
	}
	if !found {
		t.Fatal("a healthy Agent instructions row was not navigable")
	}

	// The tmux problem opens its focused repair.
	key(m, "tab")
	render(m)
	key(m, "enter")
	render(m)
	if m.Route().Child != ChildRepairTmux {
		t.Fatalf("the tmux row opened %#v", m.Route())
	}
	if m.Page() != PageDiagnostics {
		t.Fatalf("the repair moved the sidebar destination to %q", m.Page())
	}
	m.Back()

	// No projects is not a repair route yet: it lands on the page that owns it.
	m.rowCursor = 0
	render(m)
	m.activateRepair(configchecks.RepairAddProject)
	if m.Page() != PageProjects {
		t.Fatalf("the empty-projects row landed on %q", m.Page())
	}
}

func TestRecheckRunsInACommandAndClosesAResolvedRepair(t *testing.T) {
	m := seeded(t, PageSetup, problemResults())
	m.SetCheckInput(configchecks.Input{Config: &config.Config{}, ConfigPath: filepath.Join(t.TempDir(), "config.json")})
	m.OpenRepair(configchecks.RepairTmux)
	render(m)

	// R runs everything that answers a question about the machine: the checks,
	// the integration probe, and the install provenance.
	msgs := collect(key(m, "r"))
	var ranChecks bool
	for _, msg := range msgs {
		if _, ok := msg.(ChecksMsg); ok {
			ranChecks = true
		}
	}
	if !ranChecks {
		t.Fatalf("R did not run the checks in a command: %#v", msgs)
	}

	// A run that still reports the problem leaves the repair open.
	m.ApplyChecks(ChecksMsg{Results: problemResults()})
	if m.Route().Child != ChildRepairTmux {
		t.Fatal("an unresolved recheck closed the repair")
	}

	// A run that finds it fixed returns the user to the page that sent them.
	m.ApplyChecks(ChecksMsg{Results: healthyResults()})
	if m.Route().IsChild() {
		t.Fatalf("a resolved repair stayed open: %#v", m.Route())
	}
	if m.Page() != PageSetup {
		t.Fatalf("a resolved repair returned to %q", m.Page())
	}
}

func TestPageActionsAppearInTheSurfaceCommands(t *testing.T) {
	m := seeded(t, PageDiagnostics, problemResults())
	render(m)
	names := map[string]bool{}
	for _, command := range m.Commands() {
		names[command.ID] = true
	}
	if !names["recheck"] {
		t.Fatalf("Diagnostics did not advertise Recheck: %#v", m.Commands())
	}

	m.OpenAgentInstructions()
	render(m)
	names = map[string]bool{}
	for _, command := range m.Commands() {
		names[command.ID] = true
	}
	for _, want := range []string{"recheck", "copy-guidance", "open-file"} {
		if !names[want] {
			t.Fatalf("the agent repair did not advertise %q: %#v", want, m.Commands())
		}
	}
}

func TestConfirmationOwnsTheKeyboard(t *testing.T) {
	dir := t.TempDir()
	m := seeded(t, PageDiagnostics, problemResults())
	m.SetCheckInput(configchecks.Input{ProjectDir: dir, ProjectName: "demo"})
	m.OpenAgentInstructions()
	render(m)
	m.reviewAgentInstructions(filepath.Join(dir, "AGENTS.md"), "AGENTS.md")
	render(m)

	// While a confirmation is open, navigation keys do nothing at all.
	before := m.Page()
	key(m, "j")
	key(m, "/")
	if m.Page() != before || m.SearchFocused() {
		t.Fatalf("a key reached the surface behind the confirmation: page=%q search=%v", m.Page(), m.SearchFocused())
	}
	if m.confirm == nil {
		t.Fatal("the confirmation was dismissed by an unrelated key")
	}
}
