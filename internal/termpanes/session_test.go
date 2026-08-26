package termpanes

import "testing"

func TestSessionNameAndClosePolicy(t *testing.T) {
	if got := SessionName(" sidecar/main "); got != SessionPrefix+"sidecar-main" {
		t.Fatalf("SessionName = %q", got)
	}
	for _, test := range []struct {
		name, current, shell string
		want                 bool
	}{
		{name: "idle login shell", current: "zsh", shell: "/bin/zsh"},
		{name: "login argv zero", current: "-zsh", shell: "zsh"},
		{name: "running process", current: "/usr/local/bin/node", shell: "zsh", want: true},
		{name: "unknown shell", current: "node", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := CloseNeedsConfirm(test.current, test.shell); got != test.want {
				t.Fatalf("CloseNeedsConfirm(%q, %q) = %v", test.current, test.shell, got)
			}
		})
	}
}

func TestKillSessionRejectsUnownedTarget(t *testing.T) {
	if cmd := KillSession("user-session"); cmd != nil {
		t.Fatal("unowned tmux session produced a kill command")
	}
}
