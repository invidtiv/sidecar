package agentcontrol

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

type scriptedRunner struct{ metadata []string }

func (r *scriptedRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if args[0] == "capture-pane" {
		return []byte("screen"), nil
	}
	if len(r.metadata) == 0 {
		return nil, fmt.Errorf("no metadata")
	}
	out := r.metadata[0]
	r.metadata = r.metadata[1:]
	return []byte(out), nil
}
func metadata(pane string) string {
	return strings.Join([]string{pane, "999999", "0", "0", "zsh", "title", "71"}, "\x1f") + "\n"
}

func TestLocalLaunchRevalidatesBeforeWriting(t *testing.T) {
	inc := tmuxserver.Combine(tmuxserver.Socket(), 71).String()
	snap := Snapshot{Target: Target{Host: "local", Project: "p", Session: "s", Namespace: tmuxenv.Namespace(), PaneID: "%1", PanePID: 999999, ServerIncarnation: inc}}
	pasted := false
	terminal := &LocalTerminal{Runner: &scriptedRunner{metadata: []string{metadata("%2")}}, Paste: func(string, string) error { pasted = true; return nil }, Key: func(string, string) error { return nil }}
	err := terminal.Launch(context.Background(), snap, []string{"fake"})
	var typed *Error
	if !AsError(err, &typed) || typed.Code != ErrReplaced {
		t.Fatalf("err = %T %v", err, err)
	}
	if pasted {
		t.Fatal("wrote bytes after replacement")
	}
}

func TestSubmitPreservesMultilineUnicodeAndMetacharacters(t *testing.T) {
	inc := tmuxserver.Combine(tmuxserver.Socket(), 71).String()
	snap := Snapshot{Target: Target{Host: "local", Project: "p", Session: "s", Namespace: tmuxenv.Namespace(), PaneID: "%1", PanePID: 999999, ServerIncarnation: inc}}
	want := "first line\n雪 $HOME ; 'quoted' #{pane_id}"
	got := ""
	key := ""
	terminal := &LocalTerminal{Runner: &scriptedRunner{metadata: []string{metadata("%1")}}, Paste: func(_ string, text string) error { got = text; return nil }, Key: func(_ string, k string) error { key = k; return nil }}
	if err := terminal.Submit(context.Background(), snap, want); err != nil {
		t.Fatal(err)
	}
	if got != want || key != "Enter" {
		t.Fatalf("got %q key %q", got, key)
	}
}
