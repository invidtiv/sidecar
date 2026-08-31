package agentcontrol

import (
	"context"
	"fmt"
	"strconv"
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
	return strings.Join([]string{pane, "999999", "0", "0", "zsh", "title", "71"}, "|") + "\n"
}

// recordingRunner keeps every tmux invocation so a test can assert on the
// arguments themselves rather than on what came back.
type recordingRunner struct {
	metadata []string
	calls    [][]string
}

func (r *recordingRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if args[0] == "capture-pane" {
		return []byte("one\ntwo\nthree\nfour\n"), nil
	}
	if len(r.metadata) == 0 {
		return nil, fmt.Errorf("no metadata")
	}
	out := r.metadata[0]
	r.metadata = r.metadata[1:]
	return []byte(out), nil
}

func (r *recordingRunner) lastCapture(t *testing.T) []string {
	t.Helper()
	for i := len(r.calls) - 1; i >= 0; i-- {
		if r.calls[i][0] == "capture-pane" {
			return r.calls[i]
		}
	}
	t.Fatal("no capture-pane invocation was recorded")
	return nil
}

func captureSnapshot() Snapshot {
	inc := tmuxserver.Combine(tmuxserver.Socket(), 71).String()
	return Snapshot{Target: Target{Host: "local", Project: "p", Session: "s", Namespace: tmuxenv.Namespace(), PaneID: "%1", PanePID: 999999, ServerPID: 71, ServerIncarnation: inc}}
}

// Each read source is one capture-pane invocation, and the flags are the whole
// contract: -J is what joins tmux's soft wraps back into readable text, and
// without it recent-unwrapped is just recent under a different name. Nothing
// checked the flags, so either could have been dropped silently.
func TestCaptureBuildsTheDocumentedFlagsForEverySource(t *testing.T) {
	snap := captureSnapshot()
	for _, tc := range []struct {
		name    string
		req     ReadRequest
		want    []string
		absent  []string
		wantErr bool
	}{
		{name: "visible", req: ReadRequest{Source: SourceVisible},
			want: []string{"capture-pane", "-p", "-t", "%1"}, absent: []string{"-J", "-S", "-e"}},
		{name: "recent", req: ReadRequest{Source: SourceRecent, Lines: 40},
			want: []string{"capture-pane", "-p", "-S", "-40", "-t", "%1"}, absent: []string{"-J"}},
		{name: "recent unwrapped joins soft wraps", req: ReadRequest{Source: SourceRecentUnwrapped, Lines: 40},
			want: []string{"capture-pane", "-p", "-S", "-40", "-J", "-t", "%1"}},
		{name: "recent defaults to the detection depth", req: ReadRequest{Source: SourceRecent},
			want: []string{"capture-pane", "-p", "-S", "-80", "-t", "%1"}},
		{name: "ansi is opt-in", req: ReadRequest{Source: SourceRecent, Lines: 5, ANSI: true},
			want: []string{"capture-pane", "-p", "-S", "-5", "-e", "-t", "%1"}},
		{name: "an unknown source is not a capture", req: ReadRequest{Source: ReadSource("scrollback")}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &recordingRunner{}
			terminal := &LocalTerminal{Runner: runner}
			_, err := terminal.Capture(context.Background(), snap, tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatal("an unknown source was captured")
				}
				if len(runner.calls) != 0 {
					t.Fatalf("an unknown source still ran %v", runner.calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(runner.lastCapture(t), " "); got != strings.Join(tc.want, " ") {
				t.Fatalf("args = %q, want %q", got, strings.Join(tc.want, " "))
			}
			for _, flag := range tc.absent {
				for _, arg := range runner.lastCapture(t) {
					if arg == flag {
						t.Fatalf("%s carried %q", tc.name, flag)
					}
				}
			}
		})
	}
}

// --source detection promises the detector's own slice, byte for byte, so that
// an argument about a status verdict is settled against the evidence the
// verdict actually used. That is a parity claim between two call sites, and the
// only way it stays true is for a test to compare them rather than to restate
// the flags in a second place.
func TestCaptureDetectionSourceIsExactlyTheSliceInspectReads(t *testing.T) {
	snap := captureSnapshot()

	inspectRunner := &recordingRunner{metadata: []string{metadata("%1")}}
	if _, err := (&LocalTerminal{Runner: inspectRunner}).Inspect(context.Background(), snap.Target); err != nil {
		t.Fatal(err)
	}
	captureRunner := &recordingRunner{}
	if _, err := (&LocalTerminal{Runner: captureRunner}).Capture(context.Background(), snap, ReadRequest{Source: SourceDetection}); err != nil {
		t.Fatal(err)
	}
	inspected := strings.Join(inspectRunner.lastCapture(t), " ")
	read := strings.Join(captureRunner.lastCapture(t), " ")
	if inspected != read {
		t.Fatalf("detection read %q, detector read %q", read, inspected)
	}
	if !strings.Contains(read, "-S -"+strconv.Itoa(DetectionScrollback)) {
		t.Fatalf("detection slice is not the documented %d lines: %q", DetectionScrollback, read)
	}

	// --ansi cannot double the -e the detection slice already carries; the
	// bytes the detector saw are not negotiable.
	ansiRunner := &recordingRunner{}
	if _, err := (&LocalTerminal{Runner: ansiRunner}).Capture(context.Background(), snap, ReadRequest{Source: SourceDetection, ANSI: true}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ansiRunner.lastCapture(t), " "); got != read {
		t.Fatalf("--ansi changed the detection slice: %q, want %q", got, read)
	}
}

func TestLocalLaunchRevalidatesBeforeWriting(t *testing.T) {
	inc := tmuxserver.Combine(tmuxserver.Socket(), 71).String()
	snap := Snapshot{Target: Target{Host: "local", Project: "p", Session: "s", Namespace: tmuxenv.Namespace(), PaneID: "%1", PanePID: 999999, ServerPID: 71, ServerIncarnation: inc}}
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
	snap := Snapshot{Target: Target{Host: "local", Project: "p", Session: "s", Namespace: tmuxenv.Namespace(), PaneID: "%1", PanePID: 999999, ServerPID: 71, ServerIncarnation: inc}}
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
