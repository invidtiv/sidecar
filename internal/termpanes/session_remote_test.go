package termpanes

import (
	"fmt"
	"strings"
	"testing"
)

func TestEnsureRemoteSessionCreatesThenReuses(t *testing.T) {
	var calls [][]string
	panes := map[string]string{}
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		switch args[0] {
		case "list-panes":
			session := ""
			for i, arg := range args {
				if arg == "-t" && i+1 < len(args) {
					session = args[i+1]
				}
			}
			if pane := panes[session]; pane != "" {
				return []byte(pane + "\n"), nil
			}
			return nil, fmt.Errorf("can't find session")
		case "new-session":
			session, workDir := "", ""
			for i, arg := range args {
				if arg == "-s" && i+1 < len(args) {
					session = args[i+1]
				}
				if arg == "-c" && i+1 < len(args) {
					workDir = args[i+1]
				}
			}
			if session == "" || workDir == "" || !containsArg(args, "-d") {
				t.Fatalf("new-session argv = %v", args)
			}
			panes[session] = "%host"
			return nil, nil
		default:
			t.Fatalf("unexpected tmux %v", args)
			return nil, nil
		}
	}

	pane, err := EnsureRemoteSession(run, "sidecar-tp-api", "/home/me/api")
	if err != nil {
		t.Fatal(err)
	}
	if pane != "%host" {
		t.Fatalf("pane = %q", pane)
	}
	if len(calls) < 2 || calls[1][0] != "new-session" {
		t.Fatalf("calls = %v, want list-panes then new-session", calls)
	}

	calls = nil
	pane, err = EnsureRemoteSession(run, "sidecar-tp-api", "/home/me/api")
	if err != nil {
		t.Fatal(err)
	}
	if pane != "%host" {
		t.Fatalf("reuse pane = %q", pane)
	}
	for _, call := range calls {
		if call[0] == "new-session" {
			t.Fatalf("reused session still ran new-session: %v", calls)
		}
	}
}

func TestEnsureRemoteSessionNeverLooksLikeLocalNewSession(t *testing.T) {
	run := func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "tmux") {
			t.Fatalf("runner received a tmux prefix: %v", args)
		}
		if args[0] == "list-panes" {
			return nil, fmt.Errorf("missing")
		}
		return nil, fmt.Errorf("boom")
	}
	if _, err := EnsureRemoteSession(run, "sidecar-tp-api", "/tmp"); err == nil {
		t.Fatal("expected error")
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
