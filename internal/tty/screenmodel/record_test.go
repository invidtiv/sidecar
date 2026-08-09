package screenmodel

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// recordFlag re-records the tmux oracle for every corpus fixture.
//
//	go test ./internal/tty/screenmodel -run TestRecordCorpus -record
var recordFlag = flag.Bool("record", false, "re-record tmux fixtures for the deterministic byte corpus")

const fixtureDir = "testdata/corpus"

// fixture is the recorded tmux oracle for one corpus entry.
type fixture struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	TmuxVersion string `json:"tmuxVersion"`

	// PaneWidth/PaneHeight are the pane geometry tmux reports at capture time,
	// which is what the capture text and cursor coordinates refer to.
	PaneWidth  int `json:"paneWidth"`
	PaneHeight int `json:"paneHeight"`

	Capture      string `json:"capture"`
	CursorX      int    `json:"cursorX"`
	CursorY      int    `json:"cursorY"`
	CursorFlag   bool   `json:"cursorFlag"`
	AlternateOn  bool   `json:"alternateOn"`
	MouseAnyFlag bool   `json:"mouseAnyFlag"`
	HistorySize  int    `json:"historySize"`
}

func fixturePath(name string) string {
	return filepath.Join(fixtureDir, name+".json")
}

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	data, err := os.ReadFile(fixturePath(name))
	if err != nil {
		t.Fatalf("read fixture %s: %v (re-record with: go test ./internal/tty/screenmodel -run TestRecordCorpus -record)", name, err)
	}
	var f fixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return f
}

// tmuxServer is a throwaway tmux server.
//
// Every command carries an explicit -S pointing inside the test's own temp
// directory, so nothing here can reach the developer's live sessions. The
// socket path is asserted to live under that directory before any command
// runs, and teardown targets the same explicit path — a bare `kill-server`
// would trust the ambient environment and is never used.
type tmuxServer struct {
	t    *testing.T
	sock string
	root string
	conf string
}

// tmuxConf is loaded when the throwaway server starts.
//
// Note: tmux 3.6 no longer has a window-size server option — setting it makes
// the server exit at startup. A detached session takes its geometry from
// new-session -x/-y and accepts resize-window mid-recording without it.
const tmuxConf = `set -g history-limit 5000
set -g status off
set -g default-terminal "tmux-256color"
set -ga terminal-features ",tmux-256color:RGB"
`

func startTmuxServer(t *testing.T) *tmuxServer {
	t.Helper()
	root := t.TempDir()
	sock := filepath.Join(root, "tmux.sock")
	rel, err := filepath.Rel(root, sock)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("refusing to use socket outside the test temp dir: %q", sock)
	}
	conf := filepath.Join(root, "tmux.conf")
	if err := os.WriteFile(conf, []byte(tmuxConf), 0o644); err != nil { //nolint:gosec
		t.Fatalf("write tmux.conf: %v", err)
	}
	s := &tmuxServer{t: t, sock: sock, root: root, conf: conf}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", sock, "kill-server").Run() //nolint:gosec
	})
	return s
}

func (s *tmuxServer) cmd(args ...string) *exec.Cmd {
	full := append([]string{"-f", s.conf, "-S", s.sock}, args...)
	c := exec.Command("tmux", full...) //nolint:gosec
	// TMUX set (tests run from inside tmux) would let tmux resolve targets
	// against the outer server.
	c.Env = append(os.Environ(), "TMUX=")
	return c
}

func (s *tmuxServer) run(args ...string) string {
	s.t.Helper()
	out, err := s.cmd(args...).CombinedOutput()
	if err != nil {
		s.t.Fatalf("tmux %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (s *tmuxServer) output(args ...string) string {
	s.t.Helper()
	out, err := s.cmd(args...).Output()
	if err != nil {
		s.t.Fatalf("tmux %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

// driverScript feeds pre-written byte chunks into the pane one step at a time,
// waiting for the recorder to release each step. Output post-processing is
// disabled so the pty passes the corpus bytes through untouched — with ONLCR
// left on, every LF in the corpus would reach tmux as CR LF and the LF-only
// fixtures would be silently rewritten.
const driverScript = `#!/bin/sh
stty -opost -echo 2>/dev/null
d="$1"
i=0
while [ -f "$d/step.$i" ]; do
  while [ ! -f "$d/ready.$i" ]; do sleep 0.02; done
  cat "$d/step.$i"
  : > "$d/done.$i"
  i=$((i+1))
done
: > "$d/finished"
sleep 600
`

func TestRecordCorpus(t *testing.T) {
	if !*recordFlag {
		t.Skip("pass -record to regenerate the tmux oracle fixtures")
	}
	requireTmux(t)
	srv := startTmuxServer(t)
	version := strings.TrimSpace(srv.output("-V"))

	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("mkdir fixtures: %v", err)
	}
	for _, entry := range corpus {
		t.Run(entry.Name, func(t *testing.T) {
			f := recordEntry(t, srv, entry, version)
			data, err := json.MarshalIndent(f, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := os.WriteFile(fixturePath(entry.Name), append(data, '\n'), 0o644); err != nil { //nolint:gosec
				t.Fatalf("write fixture: %v", err)
			}
		})
	}
}

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

func recordEntry(t *testing.T, srv *tmuxServer, entry corpusEntry, version string) fixture {
	t.Helper()
	dir := filepath.Join(srv.root, entry.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir step dir: %v", err)
	}
	for i, step := range entry.Steps {
		body := []byte(step.Write)
		if step.isResize() {
			body = nil
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("step.%d", i)), body, 0o644); err != nil { //nolint:gosec
			t.Fatalf("write step: %v", err)
		}
	}
	script := filepath.Join(dir, "drive.sh")
	if err := os.WriteFile(script, []byte(driverScript), 0o755); err != nil { //nolint:gosec
		t.Fatalf("write script: %v", err)
	}

	session := "fx"
	srv.run("new-session", "-d", "-s", session,
		"-x", strconv.Itoa(entry.Width), "-y", strconv.Itoa(entry.Height),
		"sh", script, dir)
	defer func() {
		_ = srv.cmd("kill-session", "-t", session).Run()
	}()
	waitForPane(t, srv, session)

	for i, step := range entry.Steps {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("ready.%d", i)), nil, 0o644); err != nil { //nolint:gosec
			t.Fatalf("write ready marker: %v", err)
		}
		waitForFile(t, filepath.Join(dir, fmt.Sprintf("done.%d", i)))
		if step.isResize() {
			srv.run("resize-window", "-t", session,
				"-x", strconv.Itoa(step.ResizeW), "-y", strconv.Itoa(step.ResizeH))
		}
		waitSettled(t, srv, session)
	}
	waitSettled(t, srv, session)

	capture := srv.output("capture-pane", "-p", "-e", "-t", session)
	meta := parseMeta(t, srv, session)
	return fixture{
		Name:         entry.Name,
		Fingerprint:  entry.fingerprint(),
		TmuxVersion:  version,
		PaneWidth:    meta.width,
		PaneHeight:   meta.height,
		Capture:      capture,
		CursorX:      meta.cursorX,
		CursorY:      meta.cursorY,
		CursorFlag:   meta.cursorFlag,
		AlternateOn:  meta.alternateOn,
		MouseAnyFlag: meta.mouseAny,
		HistorySize:  meta.historySize,
	}
}

type paneMeta struct {
	cursorX, cursorY      int
	cursorFlag            bool
	alternateOn, mouseAny bool
	historySize           int
	width, height         int
}

const metaFormat = "#{cursor_x}\t#{cursor_y}\t#{cursor_flag}\t#{alternate_on}\t" +
	"#{mouse_any_flag}\t#{history_size}\t#{pane_width}\t#{pane_height}"

func parseMeta(t *testing.T, srv *tmuxServer, session string) paneMeta {
	t.Helper()
	raw := strings.TrimRight(srv.output("display-message", "-p", "-t", session, "-F", metaFormat), "\n")
	parts := strings.Split(raw, "\t")
	if len(parts) != 8 {
		t.Fatalf("unexpected metadata %q", raw)
	}
	num := func(s string) int {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("metadata field %q: %v", s, err)
		}
		return n
	}
	return paneMeta{
		cursorX:     num(parts[0]),
		cursorY:     num(parts[1]),
		cursorFlag:  parts[2] == "1",
		alternateOn: parts[3] == "1",
		mouseAny:    parts[4] == "1",
		historySize: num(parts[5]),
		width:       num(parts[6]),
		height:      num(parts[7]),
	}
}

func waitForPane(t *testing.T, srv *tmuxServer, session string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := srv.cmd("has-session", "-t", session).Run(); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tmux session %s never appeared", session)
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// waitSettled blocks until tmux's rendered pane state stops changing. The
// driver's done marker only proves the bytes left the writer; tmux processes
// the pty asynchronously, so the recording has to observe quiescence instead.
func waitSettled(t *testing.T, srv *tmuxServer, session string) {
	t.Helper()
	const stableRuns = 3
	deadline := time.Now().Add(10 * time.Second)
	last := ""
	stable := 0
	for time.Now().Before(deadline) {
		cur := srv.output("capture-pane", "-p", "-e", "-t", session) +
			"\x00" + srv.output("display-message", "-p", "-t", session, "-F", metaFormat)
		if cur == last {
			stable++
			if stable >= stableRuns {
				return
			}
		} else {
			stable = 0
			last = cur
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane never settled")
}
