package agentcontrol

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// stageTerminal is a Terminal whose world a test can change between
// observations. Its screen is a small encoding — "kind:status[:stale]" — read
// by stageDetect, so lifecycle scenarios are written as the states they mean
// rather than as provider fixture text.
type stageTerminal struct {
	mu         sync.Mutex
	stage      string
	target     Target
	dead       bool
	copyMode   bool
	paneCount  int
	panePID    int
	inspects   int
	onInspect  func(t *stageTerminal, n int)
	submitted  []string
	submitErr  error
	keys       []string
	captures   []ReadRequest
	captureErr error
}

func newStage(stage string) *stageTerminal {
	return &stageTerminal{
		stage:     stage,
		target:    Target{Host: "local", Project: "p", Session: "s", Namespace: "n", PaneID: "%1", ServerPID: 7, ServerIncarnation: "server-1"},
		paneCount: 1,
		panePID:   42,
	}
}

func (t *stageTerminal) set(stage string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stage = stage
}

func (t *stageTerminal) Inspect(context.Context, Target) (Snapshot, error) {
	t.mu.Lock()
	t.inspects++
	n := t.inspects
	hook := t.onInspect
	t.mu.Unlock()
	if hook != nil {
		hook(t, n)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	target := t.target
	target.PanePID = t.panePID
	return Snapshot{
		Target:          target,
		Dead:            t.dead,
		CopyMode:        t.copyMode,
		PaneCount:       t.paneCount,
		CurrentCommand:  "fake",
		ProcessIdentity: "fake",
		Screen:          t.stage,
		CapturedAt:      time.Unix(100, int64(n)),
	}, nil
}

func (t *stageTerminal) Launch(context.Context, Snapshot, []string) error { return nil }

func (t *stageTerminal) Submit(_ context.Context, _ Snapshot, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.submitErr != nil {
		return t.submitErr
	}
	t.submitted = append(t.submitted, text)
	return nil
}

func (t *stageTerminal) SendKeys(_ context.Context, _ Snapshot, names []string) error {
	if err := ValidateKeys(names); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keys = append(t.keys, names...)
	return nil
}

func (t *stageTerminal) Capture(_ context.Context, snap Snapshot, req ReadRequest) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.captures = append(t.captures, req)
	if t.captureErr != nil {
		return "", t.captureErr
	}
	return string(req.Source) + " of " + snap.Screen, nil
}

func (t *stageTerminal) wrote() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.submitted...)
}

func stageDetect(s Snapshot, _ *agentactivity.Tracker) AgentState {
	parts := strings.Split(s.Screen, ":")
	state := AgentState{Freshness: "current", CapturedAt: s.CapturedAt, Evidence: "stage." + s.Screen}
	if len(parts) > 0 && parts[0] != "" {
		state.Kind = parts[0]
	}
	state.Status = StatusUnknown
	if len(parts) > 1 {
		state.Status = Status(parts[1])
	}
	if len(parts) > 2 && parts[2] == "stale" {
		state.Freshness = "stale"
	}
	state.InteractiveReady = state.Kind != "" && (state.Status == StatusIdle || state.Status == StatusDone)
	return state
}

func stageService(terminal *stageTerminal) Service {
	return Service{Terminal: terminal, Poll: time.Millisecond, Observe: time.Millisecond, Verify: time.Millisecond, StallAfter: 100 * time.Millisecond, Detect: stageDetect}
}

func codeOf(t *testing.T, err error) ErrorCode {
	t.Helper()
	var typed *Error
	if !AsError(err, &typed) {
		t.Fatalf("err = %T %v, want a typed agentcontrol error", err, err)
	}
	return typed.Code
}

func TestPromptRefusesEveryUnpromptableTargetWithoutWritingBytes(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*stageTerminal)
		want    ErrorCode
	}{
		{"blocked", func(s *stageTerminal) { s.stage = "fake:blocked" }, ErrBlocked},
		{"no provider identified", func(s *stageTerminal) { s.stage = ":unknown" }, ErrNotReady},
		{"stale status", func(s *stageTerminal) { s.stage = "fake:idle:stale" }, ErrNotReady},
		{"unknown status", func(s *stageTerminal) { s.stage = "fake:unknown" }, ErrNotReady},
		{"dead pane", func(s *stageTerminal) { s.dead = true }, ErrPaneBusy},
		{"copy mode", func(s *stageTerminal) { s.copyMode = true }, ErrPaneBusy},
		{"ambiguous session", func(s *stageTerminal) { s.paneCount = 2 }, ErrPaneBusy},
		{"incomplete identity", func(s *stageTerminal) { s.panePID = 0 }, ErrPaneBusy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			terminal := newStage("fake:idle")
			tc.prepare(terminal)
			_, err := stageService(terminal).Prompt(context.Background(), PromptRequest{Target: terminal.target, Text: "go"})
			if got := codeOf(t, err); got != tc.want {
				t.Fatalf("Prompt() code = %s, want %s", got, tc.want)
			}
			if wrote := terminal.wrote(); len(wrote) != 0 {
				t.Fatalf("refusal wrote %q to the pane", wrote)
			}
		})
	}
}

func TestPromptReportsStalledWhenTheLifecycleNeverMoves(t *testing.T) {
	terminal := newStage("fake:idle")
	_, err := stageService(terminal).Prompt(context.Background(), PromptRequest{Target: terminal.target, Text: "review the diff"})
	if got := codeOf(t, err); got != ErrPromptStalled {
		t.Fatalf("Prompt() code = %s, want %s", got, ErrPromptStalled)
	}
	// The bytes did go out — a stall is a report about the agent, not a claim
	// that nothing was written.
	if wrote := terminal.wrote(); len(wrote) != 1 || wrote[0] != "review the diff" {
		t.Fatalf("submitted = %q", wrote)
	}
}

func TestPromptReturnsAtTheObservedLifecycleChangeWithoutWait(t *testing.T) {
	terminal := newStage("fake:idle")
	terminal.onInspect = func(s *stageTerminal, n int) {
		if n >= 2 {
			s.set("fake:working")
		}
	}
	got, err := stageService(terminal).Prompt(context.Background(), PromptRequest{Target: terminal.target, Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Status != StatusWorking {
		t.Fatalf("agent = %+v, want the observed working transition", got.Agent)
	}
}

func TestPromptIntoAWorkingAgentMakesNoTurnClaim(t *testing.T) {
	terminal := newStage("fake:working")
	got, err := stageService(terminal).Prompt(context.Background(), PromptRequest{Target: terminal.target, Text: "and also this"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Status != StatusWorking {
		t.Fatalf("agent = %+v", got.Agent)
	}
	if terminal.inspects != 1 {
		t.Fatalf("inspected %d times; an already-working prompt has nothing honest to observe", terminal.inspects)
	}
}

func TestPromptWaitRequiresAnExplicitTimeout(t *testing.T) {
	terminal := newStage("fake:idle")
	_, err := stageService(terminal).Prompt(context.Background(), PromptRequest{Target: terminal.target, Text: "go", Wait: true})
	if got := codeOf(t, err); got != ErrNotReady {
		t.Fatalf("code = %s", got)
	}
	if wrote := terminal.wrote(); len(wrote) != 0 {
		t.Fatalf("a usage refusal wrote %q", wrote)
	}
	if _, err := stageService(terminal).Wait(context.Background(), WaitRequest{Target: terminal.target}); codeOf(t, err) != ErrNotReady {
		t.Fatalf("Wait() accepted an implicit timeout")
	}
}

func TestPromptWaitRunsSubmissionAndSettleUnderOnePin(t *testing.T) {
	terminal := newStage("fake:idle")
	terminal.onInspect = func(s *stageTerminal, n int) {
		switch {
		case n >= 4:
			s.set("fake:done")
		case n >= 2:
			s.set("fake:working")
		}
	}
	got, err := stageService(terminal).Prompt(context.Background(), PromptRequest{Target: terminal.target, Text: "go", Wait: true, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Status != StatusDone || got.Target.PaneID != "%1" {
		t.Fatalf("agent = %+v", got)
	}
}

func TestWaitAcceptsOnlyTheNamedStates(t *testing.T) {
	terminal := newStage("fake:working")
	terminal.onInspect = func(s *stageTerminal, n int) {
		if n >= 3 {
			s.set("fake:blocked")
		}
	}
	// The default set settles on blocked as well as idle and done: a wait that
	// ignored blocked would hang until its timeout on the exact case a caller
	// most needs to hear about.
	got, err := stageService(terminal).Wait(context.Background(), WaitRequest{Target: terminal.target, Timeout: 5 * time.Second})
	if err != nil || got.Agent.Status != StatusBlocked {
		t.Fatalf("Wait() = %+v, %v", got, err)
	}

	narrowed := newStage("fake:blocked")
	_, err = stageService(narrowed).Wait(context.Background(), WaitRequest{Target: narrowed.target, Until: []Status{StatusDone}, Timeout: 80 * time.Millisecond})
	if got := codeOf(t, err); got != ErrTimeout {
		t.Fatalf("narrowed wait code = %s, want %s", got, ErrTimeout)
	}
}

func TestWaitCannotBeSatisfiedByAReplacementOccupant(t *testing.T) {
	t.Run("a different provider takes the composer", func(t *testing.T) {
		terminal := newStage("fake:working")
		terminal.onInspect = func(s *stageTerminal, n int) {
			if n >= 3 {
				s.set("other:idle")
			}
		}
		_, err := stageService(terminal).Wait(context.Background(), WaitRequest{Target: terminal.target, Timeout: 5 * time.Second})
		if got := codeOf(t, err); got != ErrReplaced {
			t.Fatalf("code = %s, want %s", got, ErrReplaced)
		}
	})

	t.Run("the pane itself is replaced", func(t *testing.T) {
		terminal := newStage("fake:working")
		terminal.onInspect = func(s *stageTerminal, n int) {
			if n >= 3 {
				s.mu.Lock()
				s.panePID = 4242
				s.stage = "fake:done"
				s.mu.Unlock()
			}
		}
		_, err := stageService(terminal).Wait(context.Background(), WaitRequest{Target: terminal.target, Timeout: 5 * time.Second})
		if got := codeOf(t, err); got != ErrReplaced {
			t.Fatalf("code = %s, want %s", got, ErrReplaced)
		}
	})

	t.Run("the pane dies", func(t *testing.T) {
		terminal := newStage("fake:working")
		terminal.onInspect = func(s *stageTerminal, n int) {
			if n >= 3 {
				s.mu.Lock()
				s.dead = true
				s.stage = "fake:done"
				s.mu.Unlock()
			}
		}
		_, err := stageService(terminal).Wait(context.Background(), WaitRequest{Target: terminal.target, Timeout: 5 * time.Second})
		if got := codeOf(t, err); got != ErrReplaced {
			t.Fatalf("code = %s, want %s", got, ErrReplaced)
		}
	})
}

func TestWaitRefusesATargetWithNoIdentifiedProvider(t *testing.T) {
	terminal := newStage(":unknown")
	_, err := stageService(terminal).Wait(context.Background(), WaitRequest{Target: terminal.target, Timeout: time.Second})
	if got := codeOf(t, err); got != ErrNotReady {
		t.Fatalf("code = %s", got)
	}
}

func TestWaitReportsCallerCancellationAsTransportNotTimeout(t *testing.T) {
	terminal := newStage("fake:working")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := stageService(terminal).Wait(ctx, WaitRequest{Target: terminal.target, Timeout: 5 * time.Second})
	if got := codeOf(t, err); got != ErrTransport {
		t.Fatalf("code = %s, want %s", got, ErrTransport)
	}
}

func TestSendKeysValidatesTheWholeListBeforeWritingAny(t *testing.T) {
	terminal := newStage("fake:blocked")
	svc := stageService(terminal)
	_, err := svc.SendKeys(context.Background(), KeysRequest{Target: terminal.target, Keys: []string{"down", "enter", "cmd+q"}})
	if err == nil {
		t.Fatal("SendKeys accepted an unencodable key")
	}
	if len(terminal.keys) != 0 {
		t.Fatalf("rejected sequence still wrote %q", terminal.keys)
	}

	// A blocked agent is exactly what send-keys is for, so it is not refused.
	got, err := svc.SendKeys(context.Background(), KeysRequest{Target: terminal.target, Keys: []string{"down", "enter"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Status != StatusBlocked || strings.Join(terminal.keys, ",") != "down,enter" {
		t.Fatalf("agent = %+v keys = %q", got.Agent, terminal.keys)
	}

	if _, err := svc.SendKeys(context.Background(), KeysRequest{Target: terminal.target}); err == nil {
		t.Fatal("SendKeys accepted an empty sequence")
	}
}

func TestSendKeysRefusesAPaneWithNoIdentifiedProvider(t *testing.T) {
	terminal := newStage(":unknown")
	_, err := stageService(terminal).SendKeys(context.Background(), KeysRequest{Target: terminal.target, Keys: []string{"enter"}})
	if got := codeOf(t, err); got != ErrNotReady {
		t.Fatalf("code = %s", got)
	}
	if len(terminal.keys) != 0 {
		t.Fatalf("wrote %q into an unidentified pane", terminal.keys)
	}
}

func TestReadPassesEverySourceThroughAndBoundsLines(t *testing.T) {
	terminal := newStage("fake:idle")
	svc := stageService(terminal)
	for _, source := range []ReadSource{SourceVisible, SourceRecent, SourceRecentUnwrapped, SourceDetection} {
		got, err := svc.Read(context.Background(), ReadRequest{Target: terminal.target, Source: source, Lines: 40, ANSI: true})
		if err != nil {
			t.Fatalf("Read(%s): %v", source, err)
		}
		if got.Source != source || !strings.HasPrefix(got.Text, string(source)+" of ") {
			t.Fatalf("Read(%s) = %+v", source, got)
		}
		if got.Kind != "fake" || got.Status != StatusIdle {
			t.Fatalf("Read(%s) lost the lifecycle context: %+v", source, got)
		}
	}
	last := terminal.captures[len(terminal.captures)-1]
	if last.Lines != 40 || !last.ANSI {
		t.Fatalf("capture request = %+v", last)
	}
	if _, err := svc.Read(context.Background(), ReadRequest{Target: terminal.target, Source: "screenshot"}); codeOf(t, err) != ErrNotReady {
		t.Fatal("Read accepted an unknown source")
	}
	// An omitted source is the visible screen, not an error.
	if got, err := svc.Read(context.Background(), ReadRequest{Target: terminal.target}); err != nil || got.Source != SourceVisible {
		t.Fatalf("default source = %+v, %v", got, err)
	}
}

type fixedTranscript struct {
	messages []TranscriptMessage
	err      error
}

func (f fixedTranscript) SessionMessages(context.Context, Target, int) ([]TranscriptMessage, error) {
	return f.messages, f.err
}

func TestTranscriptReadIsUnavailableUntilAnExactBindingExists(t *testing.T) {
	terminal := newStage("fake:idle")
	_, err := stageService(terminal).Read(context.Background(), ReadRequest{Target: terminal.target, Source: SourceTranscript})
	if got := codeOf(t, err); got != ErrTranscriptUnavailable {
		t.Fatalf("code = %s, want %s", got, ErrTranscriptUnavailable)
	}
	if len(terminal.captures) != 0 {
		t.Fatal("an unavailable transcript fell back to scraping the terminal")
	}

	svc := stageService(terminal)
	svc.Transcript = fixedTranscript{messages: []TranscriptMessage{{Role: "user", Text: "review the diff"}, {Role: "assistant", Text: "two findings"}}}
	got, err := svc.Read(context.Background(), ReadRequest{Target: terminal.target, Source: SourceTranscript, Lines: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || got.Messages[1].Text != "two findings" || got.Text != "" {
		t.Fatalf("transcript = %+v", got)
	}

	svc.Transcript = fixedTranscript{err: errors.New("bound session no longer exists")}
	if _, err := svc.Read(context.Background(), ReadRequest{Target: terminal.target, Source: SourceTranscript}); codeOf(t, err) != ErrTranscriptUnavailable {
		t.Fatal("a failing reader did not degrade to transcript_unavailable")
	}
}

func TestParseHelpersHoldTheFrozenVocabulary(t *testing.T) {
	for _, name := range []string{"idle", "WORKING", " blocked ", "done"} {
		if _, err := ParseStatus(name); err != nil {
			t.Fatalf("ParseStatus(%q): %v", name, err)
		}
	}
	if _, err := ParseStatus("settled"); err == nil {
		t.Fatal("ParseStatus accepted an invented status")
	}
	for _, source := range ReadSources() {
		if _, err := ParseReadSource(string(source)); err != nil {
			t.Fatalf("ParseReadSource(%q): %v", source, err)
		}
	}
	if _, err := ParseReadSource("screen"); err == nil {
		t.Fatal("ParseReadSource accepted an unknown source")
	}
}

func TestLastLinesKeepsTheTail(t *testing.T) {
	if got := lastLines("a\nb\nc\nd\n", 2); got != "c\nd\n" {
		t.Fatalf("lastLines = %q", got)
	}
	if got := lastLines("a\nb", 0); got != "a\nb" {
		t.Fatalf("unbounded lastLines = %q", got)
	}
	if got := lastLines("a\nb", 9); got != "a\nb" {
		t.Fatalf("short lastLines = %q", got)
	}
}
