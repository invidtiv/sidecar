package tty

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The slice-2 evidence harness: it runs the real application matrix through the
// real Sidecar control transport, with capture-pane still the delivered frame,
// and compares the shadow model against tmux at every capture.
//
// tmux isolation, restated because the cost of getting it wrong is a
// destroyed live session: every invocation carries an explicit -S inside the
// test's own temp dir, the socket path is asserted to be under that directory
// before any command runs, TMUX is scrubbed from every child environment, and
// teardown targets that same explicit socket. The default tmux server is never
// contacted. The panes also run with HOME pointed at the temp dir and `zsh -f`,
// so no personal shell configuration can reach a capture.

var runMatrix = flag.Bool("screencompare", false,
	"run the slice-2 shadow comparison matrix (slow; needs tmux)")

var writeEvidence = flag.String("screencompare-out", "",
	"write the shadow comparison evidence report to this path")

// compareTmux is an isolated tmux server whose pane runs a configuration-free
// shell.
type compareTmux struct {
	t       *testing.T
	root    string
	sock    string
	conf    string
	home    string
	session string
	pane    string
}

const compareTmuxConf = `set -g status off
set -g default-terminal "tmux-256color"
set -g history-limit 5000
set -g mouse off
`

func startCompareTmux(t *testing.T, cols, rows int) *compareTmux {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	root, err := os.MkdirTemp("", "sccmp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	sock := filepath.Join(root, "s")
	if !strings.HasPrefix(sock, root) {
		t.Fatalf("refusing to run: socket %q is not inside the test temp dir %q", sock, root)
	}
	if len(sock) > 100 {
		t.Fatalf("socket path too long for a unix socket: %q", sock)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(root, "tmux.conf")
	if err := os.WriteFile(conf, []byte(compareTmuxConf), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &compareTmux{t: t, root: root, sock: sock, conf: conf, home: home, session: "cmp"}
	t.Cleanup(func() { _ = s.cmd("kill-server").Run() })

	shell := "/bin/zsh"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	s.run("new-session", "-d", "-s", s.session,
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows),
		"-c", home,
		"-e", "HOME="+home, "-e", "TERM=xterm-256color", "-e", "PS1=$ ", "-e", "TMUX=",
		shell, "-f")
	s.pane = strings.TrimSpace(s.run("display-message", "-p", "-t", s.session, "#{pane_id}"))
	if !controlPanePattern.MatchString(s.pane) {
		t.Fatalf("pane id = %q", s.pane)
	}
	return s
}

func (s *compareTmux) cmd(args ...string) *exec.Cmd {
	full := append([]string{"-f", s.conf, "-S", s.sock}, args...)
	c := exec.Command("tmux", full...) //nolint:gosec
	c.Env = append(os.Environ(), "TMUX=", "HOME="+s.home)
	return c
}

func (s *compareTmux) run(args ...string) string {
	s.t.Helper()
	out, err := s.cmd(args...).CombinedOutput()
	if err != nil {
		s.t.Fatalf("tmux %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (s *compareTmux) literal(text string) {
	s.t.Helper()
	s.run("send-keys", "-t", s.pane, "-l", text)
}

func (s *compareTmux) keys(keys ...string) {
	s.t.Helper()
	args := append([]string{"send-keys", "-t", s.pane}, keys...)
	s.run(args...)
}

// typeLine sends text then Enter, pacing so an application has time to redraw.
func (s *compareTmux) typeLine(text string) {
	s.t.Helper()
	s.literal(text)
	time.Sleep(60 * time.Millisecond)
	s.keys("Enter")
	time.Sleep(250 * time.Millisecond)
}

func (s *compareTmux) currentCommand() string {
	return strings.TrimSpace(s.run("display-message", "-p", "-t", s.pane, "#{pane_current_command}"))
}

// compareHarness wires the real ControlManager to the isolated tmux with a
// subscription shaped exactly like the workspace plugin's: OnSnapshot and
// OnFallback only. Nothing opts into model frames, so the model exists purely
// because shadow mode built it.
type compareHarness struct {
	t       *testing.T
	tmux    *compareTmux
	manager *ControlManager
	sub     *ControlSubscription
	// Both counters are written from the control client's goroutine and read
	// from the test goroutine.
	snapshots atomic.Int64
	fallbacks atomic.Int64
}

func newCompareHarness(t *testing.T, cols, rows int) *compareHarness {
	t.Helper()
	server := startCompareTmux(t, cols, rows)
	h := &compareHarness{t: t, tmux: server}
	h.manager = newControlManager(func(session string) (controlChannel, error) {
		return newProcessControlChannelForSocket(server.sock, session)
	}, 12*time.Millisecond)
	t.Cleanup(h.manager.Stop)
	return h
}

func (h *compareHarness) subscribe() {
	h.t.Helper()
	sub, err := h.manager.Subscribe(ControlRequest{
		Session: h.tmux.session, Pane: h.tmux.pane,
		Width: 80, Height: 24, Scrollback: 600, Visible: true, Focused: true,
		OnSnapshot: func(ControlSnapshot) { h.snapshots.Add(1) },
		OnFallback: func(err error) {
			h.fallbacks.Add(1)
			h.t.Logf("control fallback: %v", err)
		},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.sub = sub
}

// settle waits for output to stop and one more capture to land, so a scenario's
// final state is compared rather than only its transitions.
func (h *compareHarness) settle(d time.Duration) {
	time.Sleep(d)
}

// scenarioResult is one row of the evidence table.
type scenarioResult struct {
	Name        string
	Available   bool
	Skipped     string
	Stats       ScreenCompareSnapshot
	Duration    time.Duration
	Description string
}

func (r scenarioResult) unexplained() int {
	total := 0
	for class, n := range r.Stats.MismatchesByClass {
		if class == gapClassUnexplained {
			total += n
		}
	}
	return total
}

// runScenario resets the counters, runs body, and returns the delta.
func runScenario(t *testing.T, name, description string, body func()) scenarioResult {
	t.Helper()
	ResetScreenCompare()
	start := time.Now()
	body()
	res := scenarioResult{
		Name: name, Available: true, Description: description,
		Stats: screenCompareStats.Snapshot(), Duration: time.Since(start),
	}
	t.Logf("scenario %-26s bursts=%d captures=%d comparisons=%d clean=%d mismatched=%d unexplained-cells=%d seeds=%d faults=%d",
		name, res.Stats.RawEvents, res.Stats.Captures, res.Stats.Comparisons, res.Stats.ComparisonsClean,
		res.Stats.FramesWithMismatch, res.unexplained(), res.Stats.Seeds, res.Stats.Faults)
	return res
}

// TestScreenCompareRealApplicationMatrix is the slice-2 evidence run. It is
// gated behind -screencompare because it drives real applications for tens of
// seconds; `go test ./...` must stay fast.
func TestScreenCompareRealApplicationMatrix(t *testing.T) {
	if !*runMatrix {
		t.Skip("pass -screencompare to run the slice-2 evidence matrix")
	}
	forceScreenCompare(t, true)

	h := newCompareHarness(t, 80, 24)
	h.subscribe()
	// Give the seed transaction time to complete before the first scenario.
	waitUntil(t, 20*time.Second, "the first shadow comparison", func() bool {
		return screenCompareStats.Snapshot().Comparisons > 0
	})

	var results []scenarioResult
	availability := map[string]string{}
	lookup := func(name string) (string, bool) {
		path, err := exec.LookPath(name)
		if err != nil {
			availability[name] = "NOT INSTALLED"
			return "", false
		}
		availability[name] = path
		return path, true
	}

	// --- zsh: prompt editing, multiline, completion, long wrapped output ----
	results = append(results, runScenario(t, "zsh-prompt-editing",
		"line editing, kill/yank, multiline continuation, TAB completion, wrapped output", func() {
			h.tmux.typeLine("PS1='$ '")
			// Line editing: type, move to start, edit, move to end.
			h.tmux.literal("echo alpha bravo charlie")
			h.tmux.keys("C-a")
			h.tmux.literal("# ")
			h.tmux.keys("C-e")
			h.tmux.literal(" delta")
			h.tmux.keys("C-u")
			h.settle(300 * time.Millisecond)
			// Multiline continuation.
			h.tmux.typeLine("for i in 1 2 3; do")
			h.tmux.typeLine("printf 'iter %d\\n' $i")
			h.tmux.typeLine("done")
			// Completion (zsh -f has no compinit; TAB still triggers the
			// built-in path completer, which is what a user sees on a bare shell).
			h.tmux.literal("ls /usr/bi")
			h.tmux.keys("Tab")
			h.settle(300 * time.Millisecond)
			h.tmux.keys("C-u")
			// Long wrapped output, wide/CJK/emoji text, and colour.
			h.tmux.typeLine("printf 'x%.0s' {1..500}; echo")
			h.tmux.typeLine("printf '\\033[31mred\\033[0m \\033[1;4mbold-underline\\033[0m\\n'")
			h.tmux.typeLine("printf '\\033[38;5;208m256\\033[0m \\033[38;2;10;200;30mtruecolor\\033[0m\\n'")
			h.tmux.typeLine("printf 'wide:\\u4f60\\u597d combining:e\\u0301 emoji:\\U0001F600\\n'")
			h.tmux.typeLine("seq 1 200")
			h.settle(1500 * time.Millisecond)
		}))

	// --- vim / nvim: editing, splits, search, resize -----------------------
	editor := ""
	if path, ok := lookup("nvim"); ok {
		editor = path
	} else if path, ok := lookup("vim"); ok {
		editor = path
	}
	if editor == "" {
		results = append(results, scenarioResult{
			Name: "editor-nvim-or-vim", Skipped: "neither nvim nor vim is installed",
		})
	} else {
		results = append(results, runScenario(t, "editor-nvim-or-vim",
			"alternate screen, insert, split, search, live resize, quit", func() {
				h.tmux.typeLine(editor + " -u NONE -i NONE " + filepath.Join(h.tmux.home, "scratch.txt"))
				h.settle(1200 * time.Millisecond)
				h.tmux.keys("i")
				h.tmux.literal("alpha bravo charlie\ndelta echo foxtrot\ngolf hotel india\n")
				h.tmux.keys("Escape")
				h.settle(400 * time.Millisecond)
				h.tmux.typeLine(":vsplit")
				h.settle(600 * time.Millisecond)
				h.tmux.typeLine("/echo")
				h.settle(400 * time.Millisecond)
				// Live resize while a full-screen application owns the screen.
				h.tmux.run("resize-window", "-t", h.tmux.session, "-x", "100", "-y", "30")
				h.sub.Resize(100, 30)
				h.settle(1200 * time.Millisecond)
				h.tmux.run("resize-window", "-t", h.tmux.session, "-x", "80", "-y", "24")
				h.sub.Resize(80, 24)
				h.settle(1200 * time.Millisecond)
				h.tmux.typeLine(":qa!")
				h.settle(800 * time.Millisecond)
			}))
	}

	// --- less --------------------------------------------------------------
	if _, ok := lookup("less"); !ok {
		results = append(results, scenarioResult{Name: "less", Skipped: "less is not installed"})
	} else {
		results = append(results, runScenario(t, "less",
			"alternate screen pager: paging, search, quit", func() {
				h.tmux.typeLine("seq 1 500 | less")
				h.settle(800 * time.Millisecond)
				h.tmux.keys("Space")
				h.settle(300 * time.Millisecond)
				h.tmux.keys("Space")
				h.settle(300 * time.Millisecond)
				h.tmux.literal("/123")
				h.tmux.keys("Enter")
				h.settle(500 * time.Millisecond)
				h.tmux.keys("G")
				h.settle(400 * time.Millisecond)
				h.tmux.keys("q")
				h.settle(800 * time.Millisecond)
			}))
	}

	// --- a continuously updating program ------------------------------------
	if _, ok := lookup("top"); !ok {
		results = append(results, scenarioResult{Name: "top", Skipped: "top is not installed"})
	} else {
		results = append(results, runScenario(t, "top-continuous-update",
			"continuously repainting full-screen program", func() {
				h.tmux.typeLine("top -l 0 -s 1")
				h.settle(6 * time.Second)
				h.tmux.keys("q")
				h.settle(1 * time.Second)
				h.tmux.keys("C-c")
				h.settle(500 * time.Millisecond)
			}))
	}

	// --- a mouse-aware TUI ---------------------------------------------------
	if _, ok := lookup("fzf"); !ok {
		results = append(results, scenarioResult{Name: "fzf-mouse", Skipped: "fzf is not installed"})
	} else {
		results = append(results, runScenario(t, "fzf-mouse-aware",
			"mouse tracking modes enabled and disabled by a real TUI", func() {
				h.tmux.typeLine("seq 1 200 | fzf --height 100%")
				h.settle(1200 * time.Millisecond)
				h.tmux.literal("12")
				h.settle(600 * time.Millisecond)
				h.tmux.keys("C-n")
				h.settle(300 * time.Millisecond)
				h.tmux.keys("Escape")
				h.settle(1 * time.Second)
				h.tmux.keys("C-c")
				h.settle(500 * time.Millisecond)
			}))
	}

	// --- repeated alternate screen entry/exit, then restored history ---------
	if editor == "" {
		results = append(results, scenarioResult{
			Name: "alt-screen-cycling", Skipped: "no editor available to cycle the alternate screen",
		})
	} else {
		results = append(results, runScenario(t, "alt-screen-cycling",
			"four alternate-screen round trips, then the restored shell history", func() {
				for i := range 4 {
					h.tmux.typeLine(fmt.Sprintf("echo before-%d", i))
					h.tmux.typeLine(editor + " -u NONE -i NONE +q")
					h.settle(700 * time.Millisecond)
					h.tmux.typeLine(fmt.Sprintf("echo after-%d", i))
				}
				h.settle(1 * time.Second)
			}))
	}

	// --- an agent TUI --------------------------------------------------------
	results = append(results, agentScenario(t, h, lookup))

	// --- attach / switch away and back / model restart -----------------------
	results = append(results, runScenario(t, "attach-switch-restart",
		"hide and show the subscription, then drop and recreate it entirely", func() {
			h.tmux.typeLine("i=0; while [ $i -lt 400 ]; do printf 'S%03d\\n' $i; i=$((i+1)); sleep 0.02; done &")
			h.settle(1 * time.Second)
			h.sub.SetVisible(false)
			h.settle(700 * time.Millisecond)
			h.sub.SetVisible(true)
			h.settle(1500 * time.Millisecond)
			h.sub.Close()
			h.settle(400 * time.Millisecond)
			h.subscribe()
			h.settle(2 * time.Second)
			h.tmux.keys("C-c")
			h.settle(500 * time.Millisecond)
		}))

	// --- forced control-client failure and reconnect -------------------------
	results = append(results, runScenario(t, "forced-control-failure",
		"kill the tmux control client, take the fallback, then reattach", func() {
			before := h.fallbacks.Load()
			h.tmux.run("kill-session", "-t", h.tmux.session)
			waitUntil(t, 15*time.Second, "the control client to fail", func() bool {
				return h.fallbacks.Load() > before
			})
			h.settle(500 * time.Millisecond)
		}))

	report := renderMatrixReport(t, results, availability)
	t.Log("\n" + report)
	if *writeEvidence != "" {
		if err := os.WriteFile(*writeEvidence, []byte(report), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// agentScenario exercises a real agent TUI when one can be run without the
// user's credentials or network access. Availability is recorded honestly: a
// synthetic full-screen program is never substituted for an agent.
func agentScenario(t *testing.T, h *compareHarness, lookup func(string) (string, bool)) scenarioResult {
	t.Helper()
	var agent string
	for _, name := range []string{"claude", "codex"} {
		if path, ok := lookup(name); ok && agent == "" {
			agent = path
		}
	}
	if agent == "" {
		return scenarioResult{Name: "agent-tui", Skipped: "no supported agent CLI is installed"}
	}
	if os.Getenv("SIDECAR_SCREENCOMPARE_AGENT") != "1" {
		return scenarioResult{
			Name: "agent-tui",
			Skipped: "agent CLI installed at " + agent + " but NOT RUN: starting it needs the " +
				"developer's real credentials and network. Set SIDECAR_SCREENCOMPARE_AGENT=1 to run it " +
				"deliberately; no synthetic program is substituted.",
		}
	}
	return runScenario(t, "agent-tui", "real agent TUI: idle, streaming, interrupt, exit", func() {
		h.tmux.typeLine(agent)
		h.settle(6 * time.Second)
		h.tmux.literal("say hello in five words")
		h.settle(500 * time.Millisecond)
		h.tmux.keys("Enter")
		h.settle(8 * time.Second)
		h.tmux.keys("Escape")
		h.settle(2 * time.Second)
		h.tmux.keys("C-c")
		h.settle(500 * time.Millisecond)
		h.tmux.keys("C-c")
		h.settle(1 * time.Second)
	})
}

func renderMatrixReport(t *testing.T, results []scenarioResult, availability map[string]string) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# Slice 2 shadow comparison matrix\n\n")
	fmt.Fprintf(&b, "tmux %s · %s/%s · Go %s\n\n",
		strings.TrimSpace(tmuxVersionForReport()), runtime.GOOS, runtime.GOARCH, runtime.Version())

	fmt.Fprintf(&b, "## Application availability\n\n| Program | Resolved |\n| --- | --- |\n")
	for _, name := range sortedStringKeys(availability) {
		fmt.Fprintf(&b, "| %s | %s |\n", name, availability[name])
	}

	fmt.Fprintf(&b, "\n## Per-scenario result\n\n")
	fmt.Fprintf(&b, "| Scenario | Comparisons | Clean | With mismatch | Unexplained cells | Known-gap cells | Open discard window | Metadata race | Seeds |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	total := scenarioResult{Name: "TOTAL", Stats: ScreenCompareSnapshot{
		MismatchesByClass: map[string]int{}, MismatchesBySignature: map[string]int{}, Resyncs: map[string]int{},
	}}
	for _, r := range results {
		if r.Skipped != "" {
			fmt.Fprintf(&b, "| %s | _skipped: %s_ | | | | | | | |\n", r.Name, r.Skipped)
			continue
		}
		known := 0
		for class, n := range r.Stats.MismatchesByClass {
			if class != gapClassUnexplained && !strings.HasPrefix(class, "discard-window/") {
				known += n
			}
			total.Stats.MismatchesByClass[class] += n
		}
		for sig, n := range r.Stats.MismatchesBySignature {
			total.Stats.MismatchesBySignature[sig] += n
		}
		for reason, n := range r.Stats.Resyncs {
			total.Stats.Resyncs[reason] += n
		}
		total.Stats.Comparisons += r.Stats.Comparisons
		total.Stats.ComparisonsClean += r.Stats.ComparisonsClean
		total.Stats.FramesWithMismatch += r.Stats.FramesWithMismatch
		total.Stats.ComparisonsOpenWin += r.Stats.ComparisonsOpenWin
		total.Stats.ComparisonsMetaRaced += r.Stats.ComparisonsMetaRaced
		total.Stats.UncomparableCells += r.Stats.UncomparableCells
		total.Stats.Seeds += r.Stats.Seeds
		total.Stats.Captures += r.Stats.Captures
		total.Stats.CapturesWhileModelLive += r.Stats.CapturesWhileModelLive
		total.Stats.MetadataQueries += r.Stats.MetadataQueries
		total.Stats.SeedCaptures += r.Stats.SeedCaptures
		total.Stats.RawEvents += r.Stats.RawEvents
		total.Stats.RawBytes += r.Stats.RawBytes
		total.Stats.DiscardedBytes += r.Stats.DiscardedBytes
		total.Stats.ModelFrames += r.Stats.ModelFrames
		total.Stats.Faults += r.Stats.Faults
		total.Stats.Fallbacks += r.Stats.Fallbacks
		if r.Stats.ModelBytesPeak > total.Stats.ModelBytesPeak {
			total.Stats.ModelBytesPeak = r.Stats.ModelBytesPeak
		}
		mergeLatency(&total.Stats.OutputToFrameUS, r.Stats.OutputToFrameUS)
		mergeLatency(&total.Stats.OutputToCaptureUS, r.Stats.OutputToCaptureUS)
		mergeLatency(&total.Stats.ModelWriteUS, r.Stats.ModelWriteUS)
		mergeLatency(&total.Stats.ModelRenderUS, r.Stats.ModelRenderUS)
		mergeLatency(&total.Stats.CompareUS, r.Stats.CompareUS)

		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			r.Name, r.Stats.Comparisons, r.Stats.ComparisonsClean, r.Stats.FramesWithMismatch,
			r.unexplained(), known, r.Stats.ComparisonsOpenWin, r.Stats.ComparisonsMetaRaced,
			r.Stats.Seeds)
	}
	fmt.Fprintf(&b, "| **%s** | %d | %d | %d | %d | — | %d | %d | %d |\n",
		total.Name, total.Stats.Comparisons, total.Stats.ComparisonsClean,
		total.Stats.FramesWithMismatch, total.unexplained(),
		total.Stats.ComparisonsOpenWin, total.Stats.ComparisonsMetaRaced, total.Stats.Seeds)

	fmt.Fprintf(&b, "\n## Per-scenario mismatch detail\n\n")
	for _, r := range results {
		if r.Skipped != "" || len(r.Stats.MismatchesBySignature) == 0 {
			continue
		}
		fmt.Fprintf(&b, "**%s** — %s\n\n", r.Name, r.Description)
		fmt.Fprintf(&b, "| Signature | Cells | | Class | Cells |\n| --- | --- | --- | --- | --- |\n")
		sigs := sortedStringKeys(intKeys(r.Stats.MismatchesBySignature))
		classes := sortedStringKeys(intKeys(r.Stats.MismatchesByClass))
		for i := 0; i < len(sigs) || i < len(classes); i++ {
			sig, sigN, class, classN := "", "", "", ""
			if i < len(sigs) {
				sig, sigN = sigs[i], fmt.Sprint(r.Stats.MismatchesBySignature[sigs[i]])
			}
			if i < len(classes) {
				class, classN = classes[i], fmt.Sprint(r.Stats.MismatchesByClass[classes[i]])
			}
			fmt.Fprintf(&b, "| %s | %s | | %s | %s |\n", sig, sigN, class, classN)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "\n## Aggregate mismatch classes\n\n| Class | Cells |\n| --- | --- |\n")
	for _, class := range sortedStringKeys(intKeys(total.Stats.MismatchesByClass)) {
		fmt.Fprintf(&b, "| %s | %d |\n", class, total.Stats.MismatchesByClass[class])
	}
	fmt.Fprintf(&b, "\n## Aggregate mismatch signatures\n\n| Signature | Cells |\n| --- | --- |\n")
	for _, sig := range sortedStringKeys(intKeys(total.Stats.MismatchesBySignature)) {
		fmt.Fprintf(&b, "| %s | %d |\n", sig, total.Stats.MismatchesBySignature[sig])
	}
	fmt.Fprintf(&b, "\n## Aggregate resync reasons\n\n| Reason | Count |\n| --- | --- |\n")
	for _, reason := range sortedStringKeys(intKeys(total.Stats.Resyncs)) {
		fmt.Fprintf(&b, "| %s | %d |\n", reason, total.Stats.Resyncs[reason])
	}

	fmt.Fprintf(&b, "\n## Commands, latency and memory (all scenarios)\n\n")
	fmt.Fprintf(&b, "| Measure | Value |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| %%output bursts | %d |\n", total.Stats.RawEvents)
	fmt.Fprintf(&b, "| raw bytes fed to the model | %d |\n", total.Stats.RawBytes)
	fmt.Fprintf(&b, "| capture-pane transactions | %d |\n", total.Stats.Captures)
	fmt.Fprintf(&b, "| display-message metadata queries | %d |\n", total.Stats.MetadataQueries)
	fmt.Fprintf(&b, "| captures issued while a live model held the screen | %d |\n", total.Stats.CapturesWhileModelLive)
	fmt.Fprintf(&b, "| seed transactions (the expected exception) | %d |\n", total.Stats.SeedCaptures)
	fmt.Fprintf(&b, "| capture-pane per %%output burst, today | %.3f |\n", ratio(total.Stats.Captures, total.Stats.RawEvents))
	fmt.Fprintf(&b, "| capture-pane per burst a byte-fed authority would issue | %.3f |\n",
		ratio(total.Stats.Captures-total.Stats.CapturesWhileModelLive, total.Stats.RawEvents))
	fmt.Fprintf(&b, "| model frames rendered | %d |\n", total.Stats.ModelFrames)
	fmt.Fprintf(&b, "| discarded bytes (client_discarded growth) | %d |\n", total.Stats.DiscardedBytes)
	fmt.Fprintf(&b, "| model faults | %d |\n", total.Stats.Faults)
	fmt.Fprintf(&b, "| control fallbacks | %d |\n", total.Stats.Fallbacks)
	fmt.Fprintf(&b, "| model memory, peak estimate (bytes) | %d |\n", total.Stats.ModelBytesPeak)
	fmt.Fprintf(&b, "| cells capture-pane could not describe (trailing blanks) | %d |\n", total.Stats.UncomparableCells)

	fmt.Fprintf(&b, "\n| Path | n | mean us | max us |\n| --- | --- | --- | --- |\n")
	for _, l := range []struct {
		name string
		stat latencyStat
	}{
		{"output -> model frame", total.Stats.OutputToFrameUS},
		{"output -> capture snapshot (baseline)", total.Stats.OutputToCaptureUS},
		{"model write", total.Stats.ModelWriteUS},
		{"model render", total.Stats.ModelRenderUS},
		{"shadow compare (diagnostic overhead only)", total.Stats.CompareUS},
	} {
		fmt.Fprintf(&b, "| %s | %d | %.1f | %d |\n", l.name, l.stat.N, l.stat.Mean(), l.stat.Max)
	}
	return b.String()
}

func mergeLatency(into *latencyStat, from latencyStat) {
	into.N += from.N
	into.Sum += from.Sum
	if from.Max > into.Max {
		into.Max = from.Max
	}
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func intKeys(m map[string]int) map[string]string {
	out := make(map[string]string, len(m))
	for k := range m {
		out[k] = ""
	}
	return out
}

func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func tmuxVersionForReport() string {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return "unknown"
	}
	return string(out)
}

// TestAltScreenAttachCannotRestoreTheMainScreen pins the slice-2 headline
// finding as evidence rather than prose.
//
// A seed built from `capture-pane` while an application owns the alternate
// screen can only carry the *alternate* grid, so the model's main screen is
// empty. tmux kept the real one. The moment the application exits the alternate
// screen the two disagree about the whole visible grid, and nothing resyncs, so
// the divergence is permanent. This is the plan's "mid-stream attach is the
// central risk" made concrete.
//
// The test also records the remedy: tmux's `capture-pane -a` returns the saved
// main screen while the pane is on the alternate screen, so a seed transaction
// that captured both could converge. That is a slice-3 design change and is
// deliberately not made here.
func TestAltScreenAttachCannotRestoreTheMainScreen(t *testing.T) {
	if !*runMatrix {
		t.Skip("pass -screencompare to run the slice-2 evidence matrix")
	}
	forceScreenCompare(t, true)
	h := newCompareHarness(t, 80, 24)

	// Main-screen content that exists before Sidecar ever attaches.
	h.tmux.typeLine("printf 'MAINMARK-%d\\n' 1 2 3")
	h.settle(600 * time.Millisecond)
	// An application takes the alternate screen, still before the attach.
	h.tmux.typeLine("printf '\\033[?1049h\\033[H\\033[JALTMARK\\r\\n'")
	h.settle(800 * time.Millisecond)
	if got := strings.TrimSpace(h.tmux.run("display-message", "-p", "-t", h.tmux.pane, "#{alternate_on}")); got != "1" {
		t.Fatalf("pane is not on the alternate screen (alternate_on=%q)", got)
	}

	// The remedy, measured: while the pane is on the alternate screen, tmux can
	// still hand over the saved main screen.
	saved := h.tmux.run("capture-pane", "-p", "-a", "-t", h.tmux.pane)
	if !strings.Contains(saved, "MAINMARK-3") {
		t.Fatalf("capture-pane -a did not return the saved main screen; the remedy this " +
			"finding records is not available on this tmux")
	}
	if plain := h.tmux.run("capture-pane", "-p", "-t", h.tmux.pane); strings.Contains(plain, "MAINMARK-3") {
		t.Fatal("plain capture-pane returned main-screen content while on the alternate screen; " +
			"the premise of this finding does not hold")
	}
	t.Log("recorded: `capture-pane -a` returns the saved main screen while alternate_on=1, " +
		"so a two-capture seed is a viable slice-3 remedy")

	// Now attach mid-alternate-screen, exactly as a pane switch or a Sidecar
	// restart does.
	ResetScreenCompare()
	h.subscribe()
	waitUntil(t, 20*time.Second, "the attach seed", func() bool {
		return screenCompareStats.Snapshot().Seeds > 0
	})
	h.settle(1 * time.Second)

	// Leave the alternate screen. tmux restores the real main screen; the model
	// restores the empty one it was seeded with.
	ResetScreenCompare()
	h.tmux.typeLine("printf '\\033[?1049l'")
	h.settle(1500 * time.Millisecond)

	stats := screenCompareStats.Snapshot()
	t.Logf("after leaving the alternate screen: comparisons=%d clean=%d mismatched=%d cells=%v",
		stats.Comparisons, stats.ComparisonsClean, stats.FramesWithMismatch, stats.MismatchesBySignature)
	if stats.Comparisons == 0 {
		t.Fatal("no comparison ran after the alternate-screen exit")
	}
	if stats.FramesWithMismatch == 0 {
		t.Fatal("EXPECTED FAILURE DID NOT REPRODUCE: the model restored the main screen after " +
			"a mid-alternate-screen attach. If this is a real fix, the slice-2 evidence must be " +
			"rewritten, not this assertion relaxed.")
	}
	if stats.MismatchesBySignature["cell/grapheme"] == 0 {
		t.Errorf("expected whole-grid content divergence, got %v", stats.MismatchesBySignature)
	}
}

// TestScreenCompareSustainedOutputSoak measures retained model memory and
// per-burst cost under continuous fast output, which is the decision gate's
// memory-bound and latency criterion.
func TestScreenCompareSustainedOutputSoak(t *testing.T) {
	if !*runMatrix {
		t.Skip("pass -screencompare to run the slice-2 evidence matrix")
	}
	forceScreenCompare(t, true)
	h := newCompareHarness(t, 80, 24)
	h.subscribe()
	waitUntil(t, 20*time.Second, "the first shadow comparison", func() bool {
		return screenCompareStats.Snapshot().Comparisons > 0
	})

	ResetScreenCompare()
	h.tmux.typeLine("i=0; while [ $i -lt 2000000 ]; do printf 'soak %d filler filler filler\\n' $i; i=$((i+1)); done")

	var series []int64
	for range 12 {
		time.Sleep(2 * time.Second)
		series = append(series, screenCompareStats.Snapshot().ModelBytesLast)
	}
	h.tmux.keys("C-c")
	h.settle(1 * time.Second)
	stats := screenCompareStats.Snapshot()

	t.Logf("soak: bursts=%d bytes=%d comparisons=%d clean=%d mismatched=%d discarded=%d",
		stats.RawEvents, stats.RawBytes, stats.Comparisons, stats.ComparisonsClean,
		stats.FramesWithMismatch, stats.DiscardedBytes)
	t.Logf("soak: mismatch classes=%v signatures=%v", stats.MismatchesByClass, stats.MismatchesBySignature)
	t.Logf("soak: captures=%d metadata=%d seeds=%d captures-a-byte-fed-authority-would-avoid=%d",
		stats.Captures, stats.MetadataQueries, stats.SeedCaptures, stats.CapturesWhileModelLive)
	t.Logf("soak: model memory series (bytes, 2s apart) = %v peak=%d", series, stats.ModelBytesPeak)
	t.Logf("soak: output->frame mean=%.1fus max=%dus | output->capture mean=%.1fus max=%dus",
		stats.OutputToFrameUS.Mean(), stats.OutputToFrameUS.Max,
		stats.OutputToCaptureUS.Mean(), stats.OutputToCaptureUS.Max)
	t.Logf("soak: model write mean=%.1fus | model render mean=%.1fus | compare mean=%.1fus",
		stats.ModelWriteUS.Mean(), stats.ModelRenderUS.Mean(), stats.CompareUS.Mean())

	if len(series) >= 6 {
		early, late := series[2], series[len(series)-1]
		t.Logf("soak: retained model memory %d -> %d bytes over %v", early, late,
			time.Duration(len(series)-3)*2*time.Second)
		if early > 0 && late > early*4 {
			t.Errorf("retained model memory grew unbounded: %d -> %d bytes", early, late)
		}
	}
	if stats.Comparisons == 0 {
		t.Fatal("no comparisons happened during the soak")
	}
}
