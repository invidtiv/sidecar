package tty

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A holder label is client-supplied display text that travels inside a
// nested tmux command (if-shell around set-option). No label may run a tmux
// command, expand a format, or come back changed.
func TestHolderLabelCannotInjectTmuxCommandsAgainstPrivateTmux(t *testing.T) {
	srv := startPasteTmux(t)
	t.Setenv("TMUX", srv.sock+",0,0")
	t.Setenv("TMUX_PANE", "")
	srv.startSink("labels", true)
	run := func(args ...string) string { return strings.TrimSpace(srv.run(args...)) }
	pane := run("display-message", "-p", "-t", "labels", "#{pane_id}")
	identity, err := parseHeadlessTargetIdentity(run("display-message", "-p", "-t", pane, headlessTargetFormat), "labels", pane)
	if err != nil {
		t.Fatal(err)
	}
	manager := newControlManager(func(session string) (controlChannel, error) {
		return newProcessControlChannelForSocket(srv.sock, session)
	}, 5*time.Millisecond)
	t.Cleanup(manager.Stop)
	sub, err := manager.Subscribe(ControlRequest{Session: identity.Session, Pane: pane, Visible: true, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	waitFor(t, sub.UsingControl)
	host, pid := hostAndPID()
	labels := []string{
		`"; set-option -g @inj1 yes ; '`,
		`' ; set-option -g @inj2 yes ; '`,
		`\' ; set-option -g @inj3 yes ; \'`,
		`\"; set-option -g @inj4 yes ; "`,
		`} ; set-option -g @inj5 yes ; {`,
		`#{pid} $HOME ~ ${HOME} ` + "`id`",
		`$HOME \$HOME \\044 | '" #{pid} 行 🦉`,
		`x \; set-option -g @inj6 yes`,
		`;`,
		`-t`,
		`\`,
		`'`,
		`"`,
		`#`,
		`%1`,
	}
	for i, label := range labels {
		g, e := NewHeadlessGeometry(manager, identity, fmt.Sprintf("%s-mobile-%012x-%d", host, i+1, pid))
		if e != nil {
			t.Fatal(e)
		}
		if e = g.SetHolderLabel("browser", label); e != nil {
			t.Fatalf("label %q: %v", label, e)
		}
		if e = g.ClaimInput([]byte("x"), 80, 24, false); e != nil {
			t.Fatalf("label %q: claim: %v", label, e)
		}
		kind, got, e := g.Holder()
		if e != nil || kind != "browser" || got != label {
			t.Fatalf("label %q came back as %q %q: %v", label, kind, got, e)
		}
		if session, _, exists := (tmuxLeaseStore{}).read(pane); !exists || session != "labels" {
			t.Fatalf("desktop holder read failed: %q %v", session, exists)
		}
		stored, ok := geometryHolders.Load("labels")
		if !ok || stored.(geometryHolder).label != label {
			t.Fatalf("desktop holder label = %#v, want %q", stored, label)
		}
	}
	for i := 1; i <= 6; i++ {
		if got := run("show-options", "-gqv", fmt.Sprintf("@inj%d", i)); got != "" {
			t.Fatalf("label injected tmux command %d: %q", i, got)
		}
	}
}
