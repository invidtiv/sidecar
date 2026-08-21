package installui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/version"
)

type recordingRunner struct {
	commands []string
	err      error
	onRun    func()
}

func (s *recordingRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	s.commands = append(s.commands, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	if s.onRun != nil {
		s.onRun()
	}
	return "ok", s.err
}

func testEnv(present map[string]bool, runner version.Runner) *version.Environment {
	return &version.Environment{
		Runner: runner,
		LookPath: func(name string) (string, error) {
			if present[name] {
				return "/stub/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
	}
}

func TestButtonLabelIsPadded(t *testing.T) {
	if got := ButtonLabel(version.TdDescriptor()); got != " Install td " {
		t.Fatalf("td label = %q", got)
	}
	if got := ButtonLabel(version.TasksDescriptor()); got != " Install Tasks " {
		t.Fatalf("tasks label = %q", got)
	}
}

func TestModelShowsButtonAndCommand(t *testing.T) {
	present := map[string]bool{"brew": true}
	m := New(version.TdDescriptor(), testEnv(present, nil))
	if !m.CanInstall() {
		t.Fatal("expected a brew-backed plan")
	}
	view := ansi.Strip(m.RenderButton(true, false))
	if !strings.Contains(view, "Install td") {
		t.Fatalf("button missing: %q", view)
	}
	if m.DisplayCommand() != "brew install marcus/tap/td" {
		t.Fatalf("command = %q", m.DisplayCommand())
	}
}

func TestEnterRunsTheDisplayedCommand(t *testing.T) {
	present := map[string]bool{"brew": true}
	runner := &recordingRunner{}
	runner.onRun = func() { present["td"] = true }
	m := New(version.TdDescriptor(), testEnv(present, runner))
	displayed := m.DisplayCommand()
	cmd := m.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not start the install")
	}
	if m.Phase != PhaseRunning {
		t.Fatalf("phase = %v", m.Phase)
	}
	msg := cmd().(ResultMsg)
	if !msg.Outcome.Installed {
		t.Fatalf("outcome: %+v", msg.Outcome)
	}
	if len(runner.commands) != 1 || runner.commands[0] != displayed {
		t.Fatalf("ran %v, displayed %q", runner.commands, displayed)
	}
}

func TestGoFallbackWhenBrewMissing(t *testing.T) {
	present := map[string]bool{"go": true}
	m := New(version.TdDescriptor(), testEnv(present, nil))
	if m.Plan.Method != version.InstallMethodGo {
		t.Fatalf("method = %s", m.Plan.Method)
	}
	if m.DisplayCommand() != "go install github.com/marcus/td@latest" {
		t.Fatalf("command = %q", m.DisplayCommand())
	}
}

func TestNoButtonWhenNothingCanRun(t *testing.T) {
	m := New(version.TdDescriptor(), testEnv(nil, nil))
	if m.CanInstall() {
		t.Fatal("offered an install with no brew and no go")
	}
	if m.RenderButton(true, false) != "" {
		t.Fatal("painted a button that cannot run")
	}
}

func TestFailureStaysRetryableAndToasts(t *testing.T) {
	present := map[string]bool{"brew": true}
	runner := &recordingRunner{err: errors.New("formula not found")}
	m := New(version.TdDescriptor(), testEnv(present, runner))
	cmd := m.Start()
	msg := cmd().(ResultMsg)
	m.ApplyResult(msg.Outcome)
	if m.Phase != PhaseFailed {
		t.Fatalf("phase = %v", m.Phase)
	}
	if !strings.Contains(m.Problem, "formula not found") {
		t.Fatalf("problem = %q", m.Problem)
	}
	if FailureToast(msg.Outcome) == "" {
		t.Fatal("failure produced no toast")
	}
	if !m.CanInstall() {
		t.Fatal("a failure should still be retryable")
	}
}
