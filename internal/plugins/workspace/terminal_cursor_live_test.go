package workspace

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
)

// The rest of this package's cursor tests build their buffers by hand, so the
// pane always sits at the tail of the buffer by construction — which is exactly
// the assumption that failed in the real app. These tests instead drive a real
// tmux pane through the real tty.Model seed-and-stream path and assert against
// tmux's own answer for which line the cursor is on.
//
// The defect they pin (td-d29821): a screen model frame serializes its grid as
// row-separated text, so a blank final row ends the string in a newline that the
// buffer read as a terminator. The buffer was then one row short of the pane,
// and every consumer that recovered pane row 0 as "line count minus pane height"
// drew the cursor one row above the line it belonged on.

// liveTerminalPane is one pane on this package's private tmux server.
type liveTerminalPane struct {
	t       *testing.T
	session string
	pane    string
}

func startLiveTerminalPane(t *testing.T, width, height int) *liveTerminalPane {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping tmux integration in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// TestMain points TMUX_TMPDIR at a throwaway server, so a bare tmux command
	// here cannot reach the developer's sessions.
	session := fmt.Sprintf("sidecar-cursor-%d", time.Now().UnixNano())
	p := &liveTerminalPane{t: t, session: session}
	// A bare sh, not the developer's login shell: tmux still spawns it through
	// $SHELL, so HOME and ZDOTDIR point at an empty directory. Otherwise the
	// developer's own rc files decide what this pane's screen looks like, and one
	// of them printing a prompt is enough to eat the test's keystrokes.
	home := t.TempDir()
	p.run("new-session", "-d", "-e", "HOME="+home, "-e", "ZDOTDIR="+home, "-e", "PS1=$ ",
		"-s", session, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height), "exec sh")
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	p.pane = strings.TrimSpace(p.run("display-message", "-p", "-t", session, "#{pane_id}"))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p.atPrompt() {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("pane never reached a prompt")
	return nil
}

func (p *liveTerminalPane) run(args ...string) string {
	p.t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput() //nolint:gosec
	if err != nil {
		p.t.Fatalf("tmux %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// typeLiteral sends text without a newline, so the cursor comes to rest at the
// end of the line the text is on rather than on a fresh prompt.
func (p *liveTerminalPane) typeLiteral(text string) {
	p.t.Helper()
	p.run("send-keys", "-t", p.pane, "-l", "--", text)
}

func (p *liveTerminalPane) sendEnter() {
	p.t.Helper()
	p.run("send-keys", "-t", p.pane, "Enter")
}

// cursorPos and lineAt are tmux's own answers, and the oracle every assertion
// below is made against: nothing in them shares an assumption with the code
// under test.
func (p *liveTerminalPane) cursorPos() (row, col int) {
	p.t.Helper()
	meta := strings.TrimSpace(p.run("display-message", "-p", "-t", p.pane, "#{cursor_y},#{cursor_x}"))
	parts := strings.Split(meta, ",")
	if len(parts) != 2 {
		p.t.Fatalf("cursor metadata = %q", meta)
	}
	row, errRow := strconv.Atoi(parts[0])
	col, errCol := strconv.Atoi(parts[1])
	if errRow != nil || errCol != nil {
		p.t.Fatalf("cursor metadata = %q", meta)
	}
	return row, col
}

// lineAt is the text of one pane row. -S 0 starts the capture at pane row 0, so
// the row indexes it directly.
func (p *liveTerminalPane) lineAt(row int) string {
	p.t.Helper()
	lines := strings.Split(strings.TrimSuffix(p.run("capture-pane", "-p", "-t", p.pane, "-S", "0"), "\n"), "\n")
	if row >= len(lines) {
		// A plain capture stops at the last written row; the cursor is below it,
		// on a blank one.
		return ""
	}
	return strings.TrimRight(lines[row], " ")
}

// fillHistory scrolls rows of output off the top of the pane and then clears
// the screen, leaving the pane mostly blank with real history above it. That is
// the shape of the reported defect: pane row 0 is well inside the buffer, and
// the grid's final row is blank — which is what makes a frame's serialized grid
// end in a newline that reads as a terminator.
func (p *liveTerminalPane) fillHistory(rows int) {
	p.t.Helper()
	p.runCommand(fmt.Sprintf("i=0; while [ $i -lt %d ]; do echo history-line-$i; i=$((i+1)); done", rows))
	// ESC[H ESC[2J homes and erases the screen without touching scrollback, the
	// way `clear` would if it were not also clearing history.
	p.runCommand(`printf '\033[H\033[2J'`)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if p.historySize() >= rows {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatalf("history_size = %d after %d lines, want at least %d", p.historySize(), rows, rows)
}

// runCommand runs one shell command and waits for the prompt to come back.
func (p *liveTerminalPane) runCommand(command string) {
	p.t.Helper()
	p.typeLiteral(command)
	p.sendEnter()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if p.atPrompt() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatalf("command %q never finished", command)
}

// atPrompt reports that the pane's last written row is a bare prompt, which is
// this shell's only observable "idle" signal.
func (p *liveTerminalPane) atPrompt() bool {
	p.t.Helper()
	lines := strings.Split(strings.TrimRight(p.run("capture-pane", "-p", "-t", p.pane), "\n"), "\n")
	return len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "$"
}

func (p *liveTerminalPane) historySize() int {
	p.t.Helper()
	size, err := strconv.Atoi(strings.TrimSpace(p.run("display-message", "-p", "-t", p.pane, "#{history_size}")))
	if err != nil {
		p.t.Fatalf("history size: %v", err)
	}
	return size
}

// terminalModelDriver pumps a tty.Model headlessly: it runs the commands the
// model returns and feeds their messages back into Update, which is what a
// Bubble Tea program would do. The model itself is only ever touched from the
// test goroutine.
type terminalModelDriver struct {
	t     *testing.T
	model *tty.Model
	msgs  chan tea.Msg
}

func newTerminalModelDriver(t *testing.T, model *tty.Model) *terminalModelDriver {
	return &terminalModelDriver{t: t, model: model, msgs: make(chan tea.Msg, 64)}
}

func (d *terminalModelDriver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				d.run(sub)
			}
			return
		}
		select {
		case d.msgs <- msg:
		case <-time.After(5 * time.Second):
		}
	}()
}

// waitFor pumps messages until condition holds.
func (d *terminalModelDriver) waitFor(timeout time.Duration, what string, condition func() bool) {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("timed out waiting for %s", what)
		}
		select {
		case msg := <-d.msgs:
			d.run(d.model.Update(msg))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// assertCursorOnItsLine is the whole point: the row the viewport draws the
// native cursor on must be the row holding the line tmux says the cursor is on.
//
// It first waits for the model to catch up with tmux — the two observe the pane
// independently, and a lagging model is not the defect under test — and only
// then asks which rendered row the cursor landed on.
func assertCursorOnItsLine(t *testing.T, d *terminalModelDriver, pane *liveTerminalPane, marker string, width, height int) {
	t.Helper()
	model := d.model
	var wantRow, wantCol int
	d.waitFor(15*time.Second, "the model to catch up with tmux", func() bool {
		wantRow, wantCol = pane.cursorPos()
		return model.State.PaneHeight > 0 &&
			model.State.CursorRow == wantRow && model.State.CursorCol == wantCol &&
			strings.Contains(ansi.Strip(model.State.OutputBuf.String()), marker)
	})
	wantLine := pane.lineAt(wantRow)

	in := terminalViewportInput{
		Buffer:        model.State.OutputBuf,
		Width:         width,
		Height:        height,
		Follow:        true,
		Interactive:   true,
		CursorRow:     model.State.CursorRow,
		CursorCol:     model.State.CursorCol,
		CursorVisible: model.State.CursorVisible,
		PaneHeight:    model.State.PaneHeight,
		PaneWidth:     model.State.PaneWidth,
		NativeCursor:  true,
		AbsoluteBase:  model.History().LoadedStart,
	}
	x, y, ok := terminalViewportCursorPosition(in)
	if !ok {
		t.Fatalf("%s: cursor not placed: paneHeight=%d lines=%d cursorRow=%d",
			marker, in.PaneHeight, in.Buffer.LineCount(), in.CursorRow)
	}
	if got := renderedTerminalLine(t, in, y); got != wantLine {
		t.Fatalf("%s: cursor drawn on rendered row %d, which shows %q; tmux says the cursor is on pane row %d, %q",
			marker, y, got, wantRow, wantLine)
	}
	if x != wantCol {
		t.Fatalf("%s: cursor column = %d, want %d", marker, x, wantCol)
	}
}

// assertCursorTracksTyping types into the pane a few times, asserting after
// each. Presentation changes hands while a terminal is live — a control-mode
// capture bootstraps the screen and the byte-fed model takes over a moment
// later — so a single assertion would only ever prove one of them right.
func assertCursorTracksTyping(t *testing.T, d *terminalModelDriver, pane *liveTerminalPane, marker string, width, height int) {
	t.Helper()
	pane.typeLiteral(marker)
	assertCursorOnItsLine(t, d, pane, marker, width, height)
	for _, suffix := range []string{"plus-two", "plus-three"} {
		time.Sleep(1500 * time.Millisecond)
		pane.typeLiteral(suffix)
		marker += suffix
		assertCursorOnItsLine(t, d, pane, marker, width, height)
	}
}

// The ordinary case, and the one the user reported: a live line with blank pane
// rows below it, and real scrollback above it. The frame's final grid row is
// blank, which is what made the buffer a row short of the pane; the scrollback
// is what makes that shortfall visible, because it puts pane row 0 somewhere
// other than the top of the buffer.
func TestLiveTerminalCursorLandsOnItsOwnLine(t *testing.T) {
	const width, height = 100, 20
	pane := startLiveTerminalPane(t, width, height)
	pane.fillHistory(height + 5)

	model := tty.New(&tty.Config{ScrollbackLines: 600})
	model.Width, model.Height = width, height
	driver := newTerminalModelDriver(t, model)
	driver.run(model.Enter(pane.session, pane.pane))
	t.Cleanup(model.Exit)

	assertCursorTracksTyping(t, driver, pane, "cursor-marker", width, height)
}

// The disagreement case. The pane's history is deeper than the window Sidecar
// loads, so the model's loaded history row count and tmux's history_size are
// different numbers on purpose: anything that places the cursor from
// history_size, or that re-derives the split by subtracting the pane height
// from the loaded row count, gets a different answer than the content shows.
func TestLiveTerminalCursorSurvivesHistoryDisagreement(t *testing.T) {
	const width, height = 100, 20
	const loaded = 40
	pane := startLiveTerminalPane(t, width, height)
	pane.fillHistory(height + loaded*3)

	model := tty.New(&tty.Config{ScrollbackLines: loaded})
	model.Width, model.Height = width, height
	driver := newTerminalModelDriver(t, model)
	driver.run(model.Enter(pane.session, pane.pane))
	t.Cleanup(model.Exit)

	assertCursorTracksTyping(t, driver, pane, "after-history", width, height)

	history := model.History()
	if loadedRows := history.LoadedEnd - history.LoadedStart; loadedRows >= history.HistorySize {
		t.Fatalf("loaded rows %d and history_size %d agree, so this case is not exercising a disagreement",
			loadedRows, history.HistorySize)
	}
}
