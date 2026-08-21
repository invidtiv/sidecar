package tdmonitor

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/installui"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
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

func stubEnv(present map[string]bool, runner version.Runner) *version.Environment {
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

func TestNotInstalledViewOffersInstallButton(t *testing.T) {
	m := NewNotInstalledModelWithEnv(stubEnv(map[string]bool{"brew": true}, nil))
	view := ansi.Strip(m.View(100, 40))
	if !strings.Contains(view, "Install td") {
		t.Fatalf("missing install button:\n%s", view)
	}
	if !strings.Contains(view, "brew install marcus/tap/td") {
		t.Fatalf("missing brew command:\n%s", view)
	}
	if !strings.Contains(view, "go install github.com/marcus/td@latest") {
		t.Fatalf("manual go command should still be copyable:\n%s", view)
	}
}

func TestNotInstalledEnterRunsDisplayedCommand(t *testing.T) {
	present := map[string]bool{"brew": true}
	runner := &recordingRunner{onRun: func() { present["td"] = true }}
	m := NewNotInstalledModelWithEnv(stubEnv(present, runner))
	_ = m.View(100, 40)
	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not start install")
	}
	msg := cmd().(installui.ResultMsg)
	if !msg.Outcome.Installed {
		t.Fatalf("outcome: %+v", msg.Outcome)
	}
	if len(runner.commands) != 1 || runner.commands[0] != "brew install marcus/tap/td" {
		t.Fatalf("ran %v", runner.commands)
	}
}

func TestNotInstalledIKeyInstalls(t *testing.T) {
	present := map[string]bool{"brew": true}
	runner := &recordingRunner{onRun: func() { present["td"] = true }}
	m := NewNotInstalledModelWithEnv(stubEnv(present, runner))
	cmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if cmd == nil {
		t.Fatal("i did not start install")
	}
	_ = cmd()
	if len(runner.commands) != 1 {
		t.Fatalf("ran %v", runner.commands)
	}
}

func TestNotInstalledGoFallbackWhenBrewMissing(t *testing.T) {
	m := NewNotInstalledModelWithEnv(stubEnv(map[string]bool{"go": true}, nil))
	view := ansi.Strip(m.View(100, 40))
	if !strings.Contains(view, "Install td") {
		t.Fatalf("missing button:\n%s", view)
	}
	if !strings.Contains(view, "Runs: GOWORK=off go install github.com/marcus/td@latest") {
		t.Fatalf("did not advertise go install:\n%s", view)
	}
}

func TestNotInstalledFailureToastsAndStays(t *testing.T) {
	present := map[string]bool{"brew": true}
	runner := &recordingRunner{err: errors.New("formula not found")}
	p := New()
	p.SetInstallEnvironment(stubEnv(present, runner))
	p.ctx = &plugin.Context{
		WorkDir: t.TempDir(),
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	p.notInstalled = NewNotInstalledModelWithEnv(p.environment())
	p.loadingModel = false

	view := ansi.Strip(p.View(100, 40))
	if !strings.Contains(view, "Install td") {
		t.Fatalf("button missing before failure:\n%s", view)
	}

	cmd := p.notInstalled.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(installui.ResultMsg)
	_, toastCmd := p.Update(msg)
	if toastCmd == nil {
		t.Fatal("failure produced no toast")
	}
	if p.notInstalled == nil {
		t.Fatal("failure left the not-installed view")
	}
	if view := ansi.Strip(p.View(100, 40)); !strings.Contains(view, "Install td") {
		t.Fatalf("failure did not keep the install button:\n%s", view)
	}
	posted := toastCmd()
	alert, ok := posted.(notify.PostMsg)
	if !ok {
		t.Fatalf("toast was %T", posted)
	}
	if !strings.Contains(alert.Notification.Title, "formula not found") {
		t.Fatalf("toast = %q", alert.Notification.Title)
	}
}

func TestNotInstalledSuccessReprobesIntoSetup(t *testing.T) {
	present := map[string]bool{"brew": true}
	runner := &recordingRunner{onRun: func() { present["td"] = true }}
	p := New()
	env := stubEnv(present, runner)
	p.SetInstallEnvironment(env)
	p.ctx = &plugin.Context{
		WorkDir: t.TempDir(),
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Epoch:   1,
	}
	p.notInstalled = NewNotInstalledModelWithEnv(env)
	p.loadingModel = false
	p.tdOnPath = false

	cmd := p.notInstalled.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(installui.ResultMsg)
	_, next := p.Update(msg)
	if p.notInstalled != nil {
		t.Fatal("success did not drop the not-installed view")
	}
	if !p.tdOnPath {
		t.Fatal("success did not re-probe td on PATH")
	}
	if next == nil {
		t.Fatal("success did not start td setup")
	}
}

func TestNotInstalledCommandsAndContext(t *testing.T) {
	p := New()
	p.notInstalled = NewNotInstalledModelWithEnv(stubEnv(map[string]bool{"brew": true}, nil))
	if p.FocusContext() != notInstalledContext {
		t.Fatalf("context = %q", p.FocusContext())
	}
	cmds := p.Commands()
	if len(cmds) != 1 || cmds[0].Name != "Install" {
		t.Fatalf("commands = %+v", cmds)
	}
}

func TestNotInstalledMouseClickStartsInstall(t *testing.T) {
	present := map[string]bool{"brew": true}
	runner := &recordingRunner{onRun: func() { present["td"] = true }}
	m := NewNotInstalledModelWithEnv(stubEnv(present, runner))
	_ = m.View(100, 40)
	var region *mouse.Region
	for _, r := range m.mouseHandler.HitMap.Regions() {
		if r.ID == installui.RegionInstall {
			rr := r
			region = &rr
			break
		}
	}
	if region == nil {
		t.Fatal("install button has no hit region")
	}
	cmd := m.Update(tea.MouseClickMsg(tea.Mouse{
		X: region.Rect.X, Y: region.Rect.Y, Button: tea.MouseLeft,
	}))
	if cmd == nil {
		t.Fatal("click did not start install")
	}
	_ = cmd()
	if len(runner.commands) != 1 {
		t.Fatalf("ran %v", runner.commands)
	}
}
