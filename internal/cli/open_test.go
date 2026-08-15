package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/uirequest"
)

func TestOpenValidation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		code     int
		contains string
	}{
		{"missing target", []string{"open"}, 2, "open requires exactly one target"},
		{"multiple targets", []string{"open", "a", "b"}, 2, "open requires exactly one target"},
		{"diff with two targets", []string{"open", "--diff", "a", "b"}, 2, "open accepts at most one target"},
		{"invalid line flag", []string{"open", "--line", "abc", "foo.txt"}, 2, "invalid line number"},
		{"missing line value", []string{"open", "--line"}, 2, "--line requires a line number argument"},
		{"invalid split flag", []string{"open", "--split", "diagonal", "foo.txt"}, 2, "invalid split option"},
		{"invalid wait flag", []string{"open", "--wait", "invalid", "foo.txt"}, 2, "invalid wait duration"},
		{"unknown option", []string{"open", "--bogus", "foo.txt"}, 2, "unknown option"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != tt.code {
				t.Fatalf("Run(%v) = handled %v, code %d; want true, %d", tt.args, handled, code, tt.code)
			}
			combined := out.String() + errOut.String()
			if !strings.Contains(combined, tt.contains) {
				t.Fatalf("output for %v missing %q; got %q", tt.args, tt.contains, combined)
			}
		})
	}
}

func TestOpenExecution(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "active task")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")

	workDir := t.TempDir()
	docFile := filepath.Join(workDir, "doc.md")
	if err := os.WriteFile(docFile, []byte("# Hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Update meta.json in project to point to workDir
	projectDir := filepath.Join(stateHome, "sidecar", "projects", "sidecar")
	if err := os.WriteFile(filepath.Join(projectDir, "meta.json"), []byte(`{"path":`+quoteJSON(t, workDir)+`}`), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Target outside workspace root -> validation error (code 2)
	{
		var out, errOut bytes.Buffer
		handled, code := Run([]string{"open", "/some/other/path.txt"}, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("expected code 2 for outside path, got %v, %d (%s)", handled, code, errOut.String())
		}
	}

	// 2. Target not found -> validation error (code 2)
	{
		var out, errOut bytes.Buffer
		handled, code := Run([]string{"open", "missing.txt"}, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("expected code 2 for missing file, got %v, %d (%s)", handled, code, errOut.String())
		}
	}

	// 3. Instance acknowledges with opened (code 0)
	{
		done := make(chan struct{})
		go func() {
			defer close(done)
			reqsDir := filepath.Join(stateHome, "sidecar", "requests")
			for i := 0; i < 40; i++ {
				time.Sleep(25 * time.Millisecond)
				entries, err := os.ReadDir(reqsDir)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".json") && !strings.Contains(e.Name(), ".tmp.") {
						req, err := uirequest.ReadRequest(filepath.Join(reqsDir, e.Name()))
						if err == nil {
							_ = uirequest.WriteAck(filepath.Join(stateHome, "sidecar"), req.ID, req.Action, uirequest.Ack{
								Instance: "test-instance",
								Host:     "localhost",
								PID:      12345,
								Status:   uirequest.StatusOpened,
								Surface:  "shell:sidecar-sh-sidecar-1",
								Pane:     3,
								At:       time.Now().UTC(),
							})
							return
						}
					}
				}
			}
		}()

		var out, errOut bytes.Buffer
		handled, code := Run([]string{"open", "--wait", "1s", "doc.md:5"}, &out, &errOut)
		<-done
		if !handled || code != 0 {
			t.Fatalf("Run(open doc.md:5) = %v, %d; stderr: %q", handled, code, errOut.String())
		}
		if !strings.Contains(out.String(), "Opened doc.md in a split beside \"active task\"") {
			t.Fatalf("unexpected output: %q", out.String())
		}
	}

	// 4. JSON output with queued ack
	{
		done := make(chan struct{})
		go func() {
			defer close(done)
			reqsDir := filepath.Join(stateHome, "sidecar", "requests")
			for i := 0; i < 40; i++ {
				time.Sleep(25 * time.Millisecond)
				entries, err := os.ReadDir(reqsDir)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".json") && !strings.Contains(e.Name(), ".tmp.") {
						req, err := uirequest.ReadRequest(filepath.Join(reqsDir, e.Name()))
						if err == nil {
							_ = uirequest.WriteAck(filepath.Join(stateHome, "sidecar"), req.ID, req.Action, uirequest.Ack{
								Instance: "test-instance-2",
								Host:     "localhost",
								PID:      12346,
								Status:   uirequest.StatusQueued,
								Surface:  "shell:sidecar-sh-sidecar-1",
								At:       time.Now().UTC(),
							})
							return
						}
					}
				}
			}
		}()

		var out, errOut bytes.Buffer
		handled, code := Run([]string{"open", "--json", "--wait", "1s", "td-1234abcd"}, &out, &errOut)
		<-done
		if !handled || code != 0 {
			t.Fatalf("Run(open --json td-1234abcd) = %v, %d; stderr: %q", handled, code, errOut.String())
		}
		var result uirequest.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("invalid json result: %v\noutput: %s", err, out.String())
		}
		if result.Action != uirequest.ActionOpen || result.Target.Kind != uirequest.TargetKindIssue || result.Target.Value != "td-1234abcd" {
			t.Fatalf("unexpected result: %+v", result)
		}
		if result.Delivered != 1 || result.Results[0].Status != uirequest.StatusQueued {
			t.Fatalf("unexpected acks in result: %+v", result)
		}
	}

	// 5. Declined ack -> exit code 4
	{
		done := make(chan struct{})
		go func() {
			defer close(done)
			reqsDir := filepath.Join(stateHome, "sidecar", "requests")
			for i := 0; i < 40; i++ {
				time.Sleep(25 * time.Millisecond)
				entries, err := os.ReadDir(reqsDir)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), ".json") && !strings.Contains(e.Name(), ".tmp.") {
						req, err := uirequest.ReadRequest(filepath.Join(reqsDir, e.Name()))
						if err == nil {
							_ = uirequest.WriteAck(filepath.Join(stateHome, "sidecar"), req.ID, req.Action, uirequest.Ack{
								Instance: "test-instance-3",
								Host:     "localhost",
								PID:      12347,
								Status:   uirequest.StatusDeclined,
								Reason:   "window too narrow",
								Surface:  "shell:sidecar-sh-sidecar-1",
								At:       time.Now().UTC(),
							})
							return
						}
					}
				}
			}
		}()

		var out, errOut bytes.Buffer
		handled, code := Run([]string{"open", "--wait", "1s", "doc.md"}, &out, &errOut)
		<-done
		if !handled || code != 4 {
			t.Fatalf("expected code 4 for declined, got %v, %d (err: %s)", handled, code, errOut.String())
		}
		if !strings.Contains(errOut.String(), "declined") || !strings.Contains(errOut.String(), "window too narrow") {
			t.Fatalf("unexpected err output: %s", errOut.String())
		}
	}

	// 6. No ack before timeout -> exit code 3
	{
		var out, errOut bytes.Buffer
		handled, code := Run([]string{"open", "--wait", "100ms", "doc.md"}, &out, &errOut)
		if !handled || code != 3 {
			t.Fatalf("expected code 3 for timeout/no instances, got %v, %d (err: %s)", handled, code, errOut.String())
		}
	}
}

func TestOpenDiffWritesWorkingTreeRequest(t *testing.T) {
	req := runOpenFireAndForget(t, t.TempDir(), []string{"open", "--diff", "--wait", "0"})
	if req.Target.Kind != uirequest.TargetKindDiff || req.Target.Value != "wt" {
		t.Fatalf("sidecar open --diff = %+v, want kind=diff value=wt", req.Target)
	}
}

func TestOpenDiffHEADWritesCommitNotWorkingTree(t *testing.T) {
	dir, oid := initOpenGitRepo(t)
	req := runOpenFireAndForget(t, dir, []string{"open", "--diff", "HEAD", "--wait", "0"})
	if req.Target.Kind != uirequest.TargetKindDiff || req.Target.Value != "c:"+oid {
		t.Fatalf("sidecar open --diff HEAD = %+v, want c:%s", req.Target, oid)
	}
	if req.Target.Value == "wt" {
		t.Fatal("--diff HEAD must not be wt")
	}
}

func TestOpenHashWithoutDiffFlag(t *testing.T) {
	dir, oid := initOpenGitRepo(t)
	short := oid[:7]
	req := runOpenFireAndForget(t, dir, []string{"open", "--wait", "0", short})
	if req.Target.Kind != uirequest.TargetKindDiff || req.Target.Value != "c:"+oid {
		t.Fatalf("sidecar open %s = %+v, want c:%s", short, req.Target, oid)
	}
}

func runOpenFireAndForget(t *testing.T, workDir string, args []string) uirequest.Request {
	t.Helper()
	stateHome, socket := setupShellCLI(t, "open-diff")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	projectDir := filepath.Join(stateHome, "sidecar", "projects", "sidecar")
	if err := os.WriteFile(filepath.Join(projectDir, "meta.json"), []byte(`{"path":`+quoteJSON(t, workDir)+`}`), 0644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run(args, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run(%v) = %v, %d; stderr: %q", args, handled, code, errOut.String())
	}

	reqsDir := filepath.Join(stateHome, "sidecar", "requests")
	entries, err := os.ReadDir(reqsDir)
	if err != nil {
		t.Fatalf("read requests: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !strings.Contains(e.Name(), ".tmp.") {
			req, err := uirequest.ReadRequest(filepath.Join(reqsDir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			return req
		}
	}
	t.Fatal("no request written")
	return uirequest.Request{}
}

func initOpenGitRepo(t *testing.T) (dir, oid string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.example",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@t.example")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-m", "init")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}
