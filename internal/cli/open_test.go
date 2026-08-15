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

	"github.com/marcus/sidecar/internal/shellstate"
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

func TestOpenFromPlainTerminalUniqueInstance(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	if err := os.WriteFile(filepath.Join(workDir, "doc.md"), []byte("# Hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: os.Getpid(), ProjectKey: "sidecar", Project: "sidecar", WorkDir: workDir,
	}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--wait", "0", "doc.md"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run(open doc.md) = %v, %d; stderr: %q", handled, code, errOut.String())
	}
	req := readWrittenRequest(t, stateDir)
	if req.Origin.TmuxSession != "" {
		t.Fatalf("TmuxSession = %q, want empty", req.Origin.TmuxSession)
	}
	if req.Origin.ProjectKey != "sidecar" {
		t.Fatalf("ProjectKey = %q, want sidecar", req.Origin.ProjectKey)
	}
}

func TestOpenFromPlainTerminalNoInstance(t *testing.T) {
	setupIsolatedCLI(t)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "doc.md"}, &out, &errOut)
	if !handled || code != 3 {
		t.Fatalf("Run(open) = %v, %d; want true, 3 (stderr %q)", handled, code, errOut.String())
	}
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "no Sidecar instance is running") {
		t.Fatalf("refusal missing no-instance message: %q", combined)
	}
	if strings.Contains(combined, "not a Sidecar project shell") {
		t.Fatalf("old shell-only refusal leaked: %q", combined)
	}
}

func TestOpenFromPlainTerminalSeveralInstances(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	child := startDummyProcess(t)
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: os.Getpid(), ProjectKey: "sidecar", Project: "sidecar",
	}); err != nil {
		t.Fatal(err)
	}
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: child, ProjectKey: "braid", Project: "braid",
	}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "doc.md"}, &out, &errOut)
	if !handled || code != 3 {
		t.Fatalf("Run(open) = %v, %d; want true, 3 (stderr %q)", handled, code, errOut.String())
	}
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "--project sidecar") || !strings.Contains(combined, "--project braid") {
		t.Fatalf("refusal missing --project choices: %q", combined)
	}
	if strings.Contains(combined, "not a Sidecar project shell") {
		t.Fatalf("old shell-only refusal leaked: %q", combined)
	}
}

func TestOpenShellFlagFromClearedTMUX(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	writeProjectShell(t, stateDir, "sidecar", shellstate.Definition{
		TmuxName: "sidecar-sh-sidecar-1", DisplayName: "active task", Namespace: "/tmp/sock", WorkDir: workDir,
	})
	if err := os.WriteFile(filepath.Join(workDir, "doc.md"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--wait", "0", "--shell", "active task", "doc.md"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run(open --shell) = %v, %d; stderr: %q", handled, code, errOut.String())
	}
	req := readWrittenRequest(t, stateDir)
	if req.Origin.TmuxSession != "sidecar-sh-sidecar-1" {
		t.Fatalf("TmuxSession = %q", req.Origin.TmuxSession)
	}
	if req.Origin.ProjectKey != "sidecar" {
		t.Fatalf("ProjectKey = %q", req.Origin.ProjectKey)
	}
}

func TestOpenProjectFlagFromClearedTMUX(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	if err := os.WriteFile(filepath.Join(workDir, "doc.md"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--wait", "0", "--project", "sidecar", "doc.md"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run(open --project) = %v, %d; stderr: %q", handled, code, errOut.String())
	}
	req := readWrittenRequest(t, stateDir)
	if req.Origin.TmuxSession != "" {
		t.Fatalf("TmuxSession = %q, want empty", req.Origin.TmuxSession)
	}
	if req.Origin.ProjectKey != "sidecar" {
		t.Fatalf("ProjectKey = %q", req.Origin.ProjectKey)
	}
}

func TestOpenCurrentShellWinsOverInstance(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "active task")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	stateDir := filepath.Join(stateHome, "sidecar")
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	if err := os.WriteFile(filepath.Join(workDir, "doc.md"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: os.Getpid(), ProjectKey: "other", Project: "other", WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--wait", "0", "doc.md"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run(open) = %v, %d; stderr: %q", handled, code, errOut.String())
	}
	req := readWrittenRequest(t, stateDir)
	if req.Origin.TmuxSession != "sidecar-sh-sidecar-1" {
		t.Fatalf("TmuxSession = %q, want current shell", req.Origin.TmuxSession)
	}
	if req.Origin.ProjectKey != "sidecar" {
		t.Fatalf("ProjectKey = %q", req.Origin.ProjectKey)
	}
}

func TestOpenWorkDirIsProjectRootFromSubdir(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	rel := filepath.Join("internal", "cli", "open.go")
	if err := os.MkdirAll(filepath.Join(workDir, "internal", "cli"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, rel), []byte("package cli\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: os.Getpid(), ProjectKey: "sidecar", Project: "sidecar", WorkDir: workDir,
	}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(workDir, "internal", "cli"))

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--wait", "0", "internal/cli/open.go"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run(open internal/cli/open.go) = %v, %d; stderr: %q", handled, code, errOut.String())
	}
	req := readWrittenRequest(t, stateDir)
	if req.Target.Value != "internal/cli/open.go" {
		t.Fatalf("Target.Value = %q, want internal/cli/open.go", req.Target.Value)
	}
	if req.Origin.WorkDir != workDir {
		t.Fatalf("Origin.WorkDir = %q, want project root %q", req.Origin.WorkDir, workDir)
	}
}

func TestOpenProjectFlagRefusesDuplicateLiveInstances(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	if err := os.WriteFile(filepath.Join(workDir, "doc.md"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	child := startDummyProcess(t)
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: os.Getpid(), ProjectKey: "sidecar", Project: "sidecar", WorkDir: workDir,
	}); err != nil {
		t.Fatal(err)
	}
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: child, ProjectKey: "sidecar", Project: "sidecar", WorkDir: workDir,
	}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--project", "sidecar", "--wait", "0", "doc.md"}, &out, &errOut)
	if !handled || code != 3 {
		t.Fatalf("Run(open --project sidecar) = %v, %d; want true, 3 (stderr %q)", handled, code, errOut.String())
	}
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "--shell") {
		t.Fatalf("refusal missing --shell: %q", combined)
	}
	reqsDir := filepath.Join(stateDir, "requests")
	if entries, err := os.ReadDir(reqsDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") && !strings.Contains(e.Name(), ".tmp.") {
				t.Fatalf("wrote a request despite duplicate live instances: %s", e.Name())
			}
		}
	}
}

func TestOpenJSONIncludesResolvedDestination(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	if err := os.WriteFile(filepath.Join(workDir, "doc.md"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: os.Getpid(), ProjectKey: "sidecar", Project: "sidecar", WorkDir: workDir,
	}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--json", "--wait", "0", "doc.md"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run(open --json) = %v, %d; stderr: %q", handled, code, errOut.String())
	}
	var result uirequest.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid json result: %v\noutput: %s", err, out.String())
	}
	if result.Project != "sidecar" {
		t.Fatalf("project = %q, want sidecar", result.Project)
	}
	if result.Resolved != uirequest.ResolvedInstance {
		t.Fatalf("resolved = %q, want %q", result.Resolved, uirequest.ResolvedInstance)
	}
}

func readWrittenRequest(t *testing.T, stateDir string) uirequest.Request {
	t.Helper()
	reqsDir := filepath.Join(stateDir, "requests")
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

func startDummyProcess(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dummy process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
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
