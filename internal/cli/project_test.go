package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/uirequest"
)

func projectTestEnv(t *testing.T) (Env, *bytes.Buffer, *bytes.Buffer, string, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	stateDir := filepath.Join(dir, "state")

	cfg := config.Default()
	cfg.Projects.List = nil
	if err := os.WriteFile(configPath, []byte(`{"projects":{"list":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	config.SetTestConfigPath(configPath)
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)

	var out, errOut bytes.Buffer
	env := Env{
		Stdout:   &out,
		Stderr:   &errOut,
		StateDir: stateDir,
	}
	return env, &out, &errOut, configPath, stateDir
}

func setupProjectShellOrigin(t *testing.T, stateDir, tmuxName, projectKey, workDir string) {
	t.Helper()
	t.Setenv(shellstate.SessionEnv, tmuxName)

	projDir := filepath.Join(stateDir, "projects", projectKey)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := struct {
		Version int                     `json:"version"`
		Shells  []shellstate.Definition `json:"shells"`
	}{
		Version: 1,
		Shells: []shellstate.Definition{
			{
				TmuxName:    tmuxName,
				DisplayName: "test-shell",
				WorkDir:     workDir,
				Namespace:   tmuxenv.Namespace(),
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "shells.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	meta := struct {
		Path string `json:"path"`
	}{Path: workDir}
	metaData, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(projDir, "meta.json"), metaData, 0644)
}

// ----------------------------------------------------------------------------
// Phase M0 Tests: Read operations (current and list)
// ----------------------------------------------------------------------------

func TestProjectCurrent_Aligned(t *testing.T) {
	env, out, errOut, _, stateDir := projectTestEnv(t)
	dir := t.TempDir()
	projPath := filepath.Join(dir, "proj-a")
	_ = os.MkdirAll(projPath, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-a", Path: projPath},
	}
	_ = config.Save(cfg)

	setupProjectShellOrigin(t, stateDir, "sidecar-sh-a", "proj-a", projPath)

	_ = uirequest.Announce(stateDir, uirequest.Instance{
		PID:        os.Getpid(),
		Host:       uirequest.HostName(),
		ProjectKey: "proj-a",
		Project:    "proj-a",
		WorkDir:    projPath,
	})

	// JSON mode
	code := runProjectCurrent(env, []string{"--json"})
	if code != 0 {
		t.Fatalf("current --json failed: %d, stderr: %s", code, errOut.String())
	}
	var res projectCurrentJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json: %v, raw: %s", err, out.String())
	}
	if !res.Aligned {
		t.Errorf("expected aligned=true, got false")
	}
	if res.Shell == nil || res.Shell.Name != "proj-a" {
		t.Errorf("expected shell proj-a, got %+v", res.Shell)
	}
	if res.Visible == nil || res.Visible.Name != "proj-a" {
		t.Errorf("expected visible proj-a, got %+v", res.Visible)
	}

	// Human mode (aligned prints only 1 line)
	out.Reset()
	errOut.Reset()
	code = runProjectCurrent(env, nil)
	if code != 0 {
		t.Fatalf("current failed: %d, stderr: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line when aligned, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "proj-a") {
		t.Errorf("expected proj-a in line 0: %q", lines[0])
	}
}

func TestProjectCurrent_Unaligned(t *testing.T) {
	env, out, errOut, _, stateDir := projectTestEnv(t)
	dir := t.TempDir()
	projAPath := filepath.Join(dir, "proj-a")
	projBPath := filepath.Join(dir, "proj-b")
	_ = os.MkdirAll(projAPath, 0755)
	_ = os.MkdirAll(projBPath, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-a", Path: projAPath},
		{Name: "proj-b", Path: projBPath},
	}
	_ = config.Save(cfg)

	setupProjectShellOrigin(t, stateDir, "sidecar-sh-a", "proj-a", projAPath)

	_ = uirequest.Announce(stateDir, uirequest.Instance{
		PID:        os.Getpid(),
		Host:       uirequest.HostName(),
		ProjectKey: "proj-b",
		Project:    "proj-b",
		WorkDir:    projBPath,
	})

	// JSON mode
	code := runProjectCurrent(env, []string{"--json"})
	if code != 0 {
		t.Fatalf("current --json failed: %d, stderr: %s", code, errOut.String())
	}
	var res projectCurrentJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json: %v, raw: %s", err, out.String())
	}
	if res.Aligned {
		t.Errorf("expected aligned=false, got true")
	}
	if res.Shell == nil || res.Shell.Name != "proj-a" {
		t.Errorf("expected shell proj-a, got %+v", res.Shell)
	}
	if res.Visible == nil || res.Visible.Name != "proj-b" {
		t.Errorf("expected visible proj-b, got %+v", res.Visible)
	}

	// Human mode (unaligned prints 2 lines)
	out.Reset()
	errOut.Reset()
	code = runProjectCurrent(env, nil)
	if code != 0 {
		t.Fatalf("current failed: %d, stderr: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines when unaligned, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "proj-a") {
		t.Errorf("expected line 0 to contain proj-a: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "visible:") || !strings.Contains(lines[1], "proj-b") {
		t.Errorf("expected line 1 to be visible proj-b: %q", lines[1])
	}
}

func TestProjectCurrent_NoTUI(t *testing.T) {
	env, out, errOut, _, stateDir := projectTestEnv(t)
	dir := t.TempDir()
	projPath := filepath.Join(dir, "proj-a")
	_ = os.MkdirAll(projPath, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-a", Path: projPath},
	}
	_ = config.Save(cfg)

	setupProjectShellOrigin(t, stateDir, "sidecar-sh-a", "proj-a", projPath)

	// No instance presence written

	// JSON mode
	code := runProjectCurrent(env, []string{"--json"})
	if code != 0 {
		t.Fatalf("current --json failed: %d, stderr: %s", code, errOut.String())
	}
	var res projectCurrentJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json: %v, raw: %s", err, out.String())
	}
	if res.Aligned {
		t.Errorf("expected aligned=false, got true")
	}
	if res.Shell == nil || res.Shell.Name != "proj-a" {
		t.Errorf("expected shell proj-a, got %+v", res.Shell)
	}
	if res.Visible != nil {
		t.Errorf("expected visible=nil, got %+v", res.Visible)
	}

	// Human mode
	out.Reset()
	errOut.Reset()
	code = runProjectCurrent(env, nil)
	if code != 0 {
		t.Fatalf("current failed: %d, stderr: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line when no TUI, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "proj-a") {
		t.Errorf("expected proj-a in line: %q", lines[0])
	}
}

func TestProjectCurrent_UnmanagedCwd(t *testing.T) {
	env, out, errOut, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	projPath := filepath.Join(dir, "proj-a")
	subPath := filepath.Join(projPath, "subdir")
	_ = os.MkdirAll(subPath, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-a", Path: projPath},
	}
	_ = config.Save(cfg)

	t.Setenv(shellstate.SessionEnv, "")
	t.Setenv("TMUX", "")
	t.Chdir(subPath)

	code := runProjectCurrent(env, []string{"--json"})
	if code != 0 {
		t.Fatalf("current from cwd failed: %d, stderr: %s", code, errOut.String())
	}
	var res projectCurrentJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Shell == nil || res.Shell.Name != "proj-a" {
		t.Errorf("expected shell proj-a, got %+v", res.Shell)
	}
}

func TestProjectCurrent_UnmanagedOutsideProjects(t *testing.T) {
	env, _, errOut, _, _ := projectTestEnv(t)
	outsideDir := t.TempDir()

	t.Setenv(shellstate.SessionEnv, "")
	t.Setenv("TMUX", "")
	t.Chdir(outsideDir)

	code := runProjectCurrent(env, []string{"--json"})
	if code != 1 {
		t.Fatalf("expected exit 1 when not in project shell/cwd, got %d", code)
	}
	if !strings.Contains(errOut.String(), "not in a Sidecar project shell") {
		t.Errorf("unexpected error message: %s", errOut.String())
	}
}

func TestProjectList_Empty(t *testing.T) {
	env, out, errOut, _, _ := projectTestEnv(t)

	code := runProjectList(env, nil)
	if code != 0 {
		t.Fatalf("list failed: %d, stderr: %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "No configured projects." {
		t.Errorf("expected 'No configured projects.', got %q", out.String())
	}

	out.Reset()
	code = runProjectList(env, []string{"--json"})
	if code != 0 {
		t.Fatalf("list --json failed: %d, stderr: %s", code, errOut.String())
	}
	var res projectListJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(res.Projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(res.Projects))
	}
}

func TestProjectList_WithMarkers(t *testing.T) {
	env, out, errOut, _, stateDir := projectTestEnv(t)
	dir := t.TempDir()
	projAPath := filepath.Join(dir, "proj-a")
	projBPath := filepath.Join(dir, "proj-b")
	projCPath := filepath.Join(dir, "proj-c")
	_ = os.MkdirAll(projAPath, 0755)
	_ = os.MkdirAll(projBPath, 0755)
	_ = os.MkdirAll(projCPath, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-a", Path: projAPath},
		{Name: "proj-b", Path: projBPath},
		{Name: "proj-c", Path: projCPath},
	}
	_ = config.Save(cfg)

	setupProjectShellOrigin(t, stateDir, "sidecar-sh-a", "proj-a", projAPath)

	_ = uirequest.Announce(stateDir, uirequest.Instance{
		PID:        os.Getpid(),
		Host:       uirequest.HostName(),
		ProjectKey: "proj-b",
		Project:    "proj-b",
		WorkDir:    projBPath,
	})

	// Human mode
	code := runProjectList(env, nil)
	if code != 0 {
		t.Fatalf("list failed: %d, stderr: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "(shell)") {
		t.Errorf("expected (shell) on line 0: %q", lines[0])
	}
	if !strings.Contains(lines[1], "(visible)") {
		t.Errorf("expected (visible) on line 1: %q", lines[1])
	}
	if strings.Contains(lines[2], "(") {
		t.Errorf("expected no markers on line 2: %q", lines[2])
	}

	// JSON mode
	out.Reset()
	code = runProjectList(env, []string{"--json"})
	if code != 0 {
		t.Fatalf("list --json failed: %d, stderr: %s", code, errOut.String())
	}
	var res projectListJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(res.Projects) != 3 {
		t.Errorf("expected 3 projects, got %d", len(res.Projects))
	}
	if res.Shell == nil || res.Shell.Name != "proj-a" {
		t.Errorf("expected shell proj-a, got %+v", res.Shell)
	}
	if res.Visible == nil || res.Visible.Name != "proj-b" {
		t.Errorf("expected visible proj-b, got %+v", res.Visible)
	}
	if res.Aligned {
		t.Errorf("expected aligned=false")
	}
}

// ----------------------------------------------------------------------------
// Phase M1 Tests: Write operations (add, set, remove)
// ----------------------------------------------------------------------------

func TestProjectAdd_Success(t *testing.T) {
	env, out, errOut, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	newPath := filepath.Join(dir, "my-new-project")
	_ = os.MkdirAll(newPath, 0755)

	code := runProjectAdd(env, []string{"my-project", "--path", newPath, "--theme", "Nord", "--open-in", "code", "--json"})
	if code != 0 {
		t.Fatalf("add failed: %d, stderr: %s", code, errOut.String())
	}

	var res projectAddJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v, raw: %s", err, out.String())
	}
	if res.Name != "my-project" || res.Path != newPath || res.Theme != "Nord" || res.OpenIn != "code" {
		t.Errorf("unexpected add JSON: %+v", res)
	}
	if res.Switched {
		t.Errorf("expected switched=false without --switch")
	}

	// Check config file on disk
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects.List) != 1 {
		t.Fatalf("expected 1 project in config, got %d", len(cfg.Projects.List))
	}
	p := cfg.Projects.List[0]
	if p.Name != "my-project" || p.Path != newPath || p.Theme == nil || p.Theme.Name != "Nord" || p.OpenIn != "code" {
		t.Errorf("unexpected project in config: %+v", p)
	}
}

func TestProjectAdd_ValidationErrors(t *testing.T) {
	env, _, errOut, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid")
	_ = os.MkdirAll(validPath, 0755)

	filePath := filepath.Join(dir, "not-a-dir")
	_ = os.WriteFile(filePath, []byte("test"), 0644)

	// Missing arguments
	if code := runProjectAdd(env, []string{}); code != 2 {
		t.Errorf("missing args exit: %d, want 2", code)
	}
	if code := runProjectAdd(env, []string{"proj"}); code != 2 {
		t.Errorf("missing --path exit: %d, want 2", code)
	}

	// Path does not exist
	errOut.Reset()
	code := runProjectAdd(env, []string{"proj", "--path", filepath.Join(dir, "absent")})
	if code != exitInputRejected {
		t.Errorf("path does not exist exit: %d, want %d", code, exitInputRejected)
	}
	if !strings.Contains(errOut.String(), "Path does not exist") {
		t.Errorf("expected 'Path does not exist', got %q", errOut.String())
	}

	// Path is not a directory
	errOut.Reset()
	code = runProjectAdd(env, []string{"proj", "--path", filePath})
	if code != exitInputRejected {
		t.Errorf("path is file exit: %d, want %d", code, exitInputRejected)
	}
	if !strings.Contains(errOut.String(), "Path is not a directory") {
		t.Errorf("expected 'Path is not a directory', got %q", errOut.String())
	}

	// Add once
	if code := runProjectAdd(env, []string{"proj-one", "--path", validPath}); code != 0 {
		t.Fatalf("first add failed: %d", code)
	}

	// Duplicate project name
	errOut.Reset()
	dir2 := filepath.Join(dir, "valid2")
	_ = os.MkdirAll(dir2, 0755)
	code = runProjectAdd(env, []string{"proj-one", "--path", dir2})
	if code != exitInputRejected {
		t.Errorf("duplicate name exit: %d, want %d", code, exitInputRejected)
	}
	if !strings.Contains(errOut.String(), "Project name already exists") {
		t.Errorf("expected 'Project name already exists', got %q", errOut.String())
	}

	// Duplicate project path
	errOut.Reset()
	code = runProjectAdd(env, []string{"proj-two", "--path", validPath})
	if code != exitInputRejected {
		t.Errorf("duplicate path exit: %d, want %d", code, exitInputRejected)
	}
	if !strings.Contains(errOut.String(), "Project path already configured") {
		t.Errorf("expected 'Project path already configured', got %q", errOut.String())
	}
}

func TestProjectSet_Success(t *testing.T) {
	env, out, errOut, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	path2 := filepath.Join(dir, "proj-2")
	_ = os.MkdirAll(path1, 0755)
	_ = os.MkdirAll(path2, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-1", Path: path1, Theme: &config.ThemeConfig{Name: "OldTheme"}},
	}
	_ = config.Save(cfg)

	// Rename and update settings
	code := runProjectSet(env, []string{"proj-1", "--name", "renamed-1", "--path", path2, "--theme", "NewTheme", "--open-in", "goland", "--json"})
	if code != 0 {
		t.Fatalf("set failed: %d, stderr: %s", code, errOut.String())
	}

	var res projectJSONItem
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Name != "renamed-1" || res.Path != path2 || res.Theme != "NewTheme" || res.OpenIn != "goland" {
		t.Errorf("unexpected set JSON: %+v", res)
	}

	// Clear theme
	out.Reset()
	code = runProjectSet(env, []string{"renamed-1", "--clear-theme", "--json"})
	if code != 0 {
		t.Fatalf("clear-theme failed: %d, stderr: %s", code, errOut.String())
	}
	res = projectJSONItem{}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Theme != "" {
		t.Errorf("expected empty theme after clear-theme, got %q", res.Theme)
	}
}

func TestProjectSet_ValidationErrors(t *testing.T) {
	env, _, errOut, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	path2 := filepath.Join(dir, "proj-2")
	_ = os.MkdirAll(path1, 0755)
	_ = os.MkdirAll(path2, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-1", Path: path1},
		{Name: "proj-2", Path: path2},
	}
	_ = config.Save(cfg)

	// No flags
	if code := runProjectSet(env, []string{"proj-1"}); code != 2 {
		t.Errorf("no change flags exit: %d, want 2", code)
	}

	// Mutually exclusive flags
	if code := runProjectSet(env, []string{"proj-1", "--theme", "Nord", "--clear-theme"}); code != 2 {
		t.Errorf("conflicting theme flags exit: %d, want 2", code)
	}

	// Unknown project
	errOut.Reset()
	if code := runProjectSet(env, []string{"nosuch", "--name", "new"}); code != exitInputRejected {
		t.Errorf("unknown project exit: %d, want %d", code, exitInputRejected)
	}

	// Duplicate name
	errOut.Reset()
	if code := runProjectSet(env, []string{"proj-1", "--name", "proj-2"}); code != exitInputRejected {
		t.Errorf("duplicate name exit: %d, want %d", code, exitInputRejected)
	}
	if !strings.Contains(errOut.String(), "Project name already exists") {
		t.Errorf("expected 'Project name already exists', got %q", errOut.String())
	}
}

func TestProjectRemove_RequiresYes(t *testing.T) {
	env, _, _, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	_ = os.MkdirAll(path1, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{{Name: "proj-1", Path: path1}}
	_ = config.Save(cfg)

	if code := runProjectRemove(env, []string{"proj-1"}); code != 2 {
		t.Errorf("remove without --yes exit: %d, want 2", code)
	}
}

func TestProjectRemove_VisibleRefused(t *testing.T) {
	env, _, errOut, _, stateDir := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	_ = os.MkdirAll(path1, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{{Name: "proj-1", Path: path1}}
	_ = config.Save(cfg)

	_ = uirequest.Announce(stateDir, uirequest.Instance{
		PID:        os.Getpid(),
		Host:       uirequest.HostName(),
		ProjectKey: "proj-1",
		Project:    "proj-1",
		WorkDir:    path1,
	})

	code := runProjectRemove(env, []string{"proj-1", "--yes"})
	if code != exitInputRejected {
		t.Fatalf("remove visible project exit: %d, want %d", code, exitInputRejected)
	}
	if !strings.Contains(errOut.String(), "currently visible") || !strings.Contains(errOut.String(), "project switch") {
		t.Errorf("expected refusal mentioning project switch, got %q", errOut.String())
	}
}

func TestProjectRemove_Success(t *testing.T) {
	env, out, errOut, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	_ = os.MkdirAll(path1, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{{Name: "proj-1", Path: path1}}
	_ = config.Save(cfg)

	code := runProjectRemove(env, []string{"proj-1", "--yes", "--json"})
	if code != 0 {
		t.Fatalf("remove failed: %d, stderr: %s", code, errOut.String())
	}

	var res projectRemoveJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Removed || res.Name != "proj-1" {
		t.Errorf("unexpected remove JSON: %+v", res)
	}

	updated, _ := config.Load()
	if len(updated.Projects.List) != 0 {
		t.Errorf("expected 0 projects in config, got %d", len(updated.Projects.List))
	}
}

// ----------------------------------------------------------------------------
// Phase M2 Tests: Switch operations (project switch and add --switch)
// ----------------------------------------------------------------------------

func TestProjectSwitch_NoInstance(t *testing.T) {
	env, _, errOut, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	_ = os.MkdirAll(path1, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{{Name: "proj-1", Path: path1}}
	_ = config.Save(cfg)

	code := runProjectSwitch(env, []string{"proj-1"})
	if code != 3 {
		t.Fatalf("expected exit 3 when no instance running, got %d", code)
	}
	if !strings.Contains(errOut.String(), "no Sidecar instance is running") {
		t.Errorf("unexpected error: %s", errOut.String())
	}
}

func TestProjectSwitch_UnknownProject(t *testing.T) {
	env, _, errOut, _, stateDir := projectTestEnv(t)

	_ = uirequest.Announce(stateDir, uirequest.Instance{
		PID:     os.Getpid(),
		Host:    uirequest.HostName(),
		WorkDir: t.TempDir(),
	})

	code := runProjectSwitch(env, []string{"nosuch"})
	if code != exitInputRejected {
		t.Fatalf("expected exit %d for unknown project, got %d", exitInputRejected, code)
	}
	if !strings.Contains(errOut.String(), "unknown project") {
		t.Errorf("unexpected error: %s", errOut.String())
	}
}

func TestProjectSwitch_SuccessWithAck(t *testing.T) {
	env, out, errOut, _, stateDir := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	_ = os.MkdirAll(path1, 0755)

	cfg, _ := config.Load()
	cfg.Projects.List = []config.ProjectConfig{{Name: "proj-1", Path: path1}}
	_ = config.Save(cfg)

	_ = uirequest.Announce(stateDir, uirequest.Instance{
		PID:        os.Getpid(),
		Host:       uirequest.HostName(),
		ProjectKey: "proj-other",
		WorkDir:    t.TempDir(),
	})

	// Background worker simulating host ack
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				time.Sleep(10 * time.Millisecond)
				requestsDir := filepath.Join(stateDir, "requests")
				entries, err := os.ReadDir(requestsDir)
				if err == nil && len(entries) > 0 {
					for _, e := range entries {
						if strings.HasSuffix(e.Name(), "-switch-project.json") {
							id := strings.TrimSuffix(e.Name(), "-switch-project.json")
							_ = uirequest.WriteAck(stateDir, id, uirequest.ActionSwitchProject, uirequest.Ack{
								Instance: uirequest.InstanceID("test-app"),
								Host:     uirequest.HostName(),
								PID:      os.Getpid(),
								Status:   uirequest.StatusOpened,
								Surface:  "workspace",
							})
							return
						}
					}
				}
			}
		}
	}()
	defer close(stop)

	code := runProjectSwitch(env, []string{"proj-1", "--json"})
	if code != 0 {
		t.Fatalf("switch failed: %d, stderr: %s", code, errOut.String())
	}

	var res projectSwitchJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Switched || res.Name != "proj-1" {
		t.Errorf("unexpected switch JSON: %+v", res)
	}
}

func TestProjectAdd_SwitchWithoutTUI(t *testing.T) {
	env, out, errOut, _, _ := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	_ = os.MkdirAll(path1, 0755)

	// With --switch and no TUI running: add succeeds (exit 0), switched: false
	code := runProjectAdd(env, []string{"proj-1", "--path", path1, "--switch", "--json"})
	if code != 0 {
		t.Fatalf("add --switch failed: %d, stderr: %s", code, errOut.String())
	}

	var res projectAddJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Switched {
		t.Errorf("expected switched=false when no TUI is running")
	}
}

func TestProjectAdd_SwitchWithTUI(t *testing.T) {
	env, out, errOut, _, stateDir := projectTestEnv(t)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "proj-1")
	_ = os.MkdirAll(path1, 0755)

	_ = uirequest.Announce(stateDir, uirequest.Instance{
		PID:        os.Getpid(),
		Host:       uirequest.HostName(),
		ProjectKey: "proj-old",
		WorkDir:    t.TempDir(),
	})

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				time.Sleep(10 * time.Millisecond)
				requestsDir := filepath.Join(stateDir, "requests")
				entries, _ := os.ReadDir(requestsDir)
				for _, e := range entries {
					if strings.HasSuffix(e.Name(), "-switch-project.json") {
						id := strings.TrimSuffix(e.Name(), "-switch-project.json")
						_ = uirequest.WriteAck(stateDir, id, uirequest.ActionSwitchProject, uirequest.Ack{
							Instance: uirequest.InstanceID("test-app"),
							Host:     uirequest.HostName(),
							PID:      os.Getpid(),
							Status:   uirequest.StatusOpened,
							Surface:  "workspace",
						})
						return
					}
				}
			}
		}
	}()
	defer close(stop)

	code := runProjectAdd(env, []string{"proj-1", "--path", path1, "--switch", "--json"})
	if code != 0 {
		t.Fatalf("add --switch failed: %d, stderr: %s", code, errOut.String())
	}

	var res projectAddJSON
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Switched {
		t.Errorf("expected switched=true when ack received")
	}
}

// ----------------------------------------------------------------------------
// Phase M3 Tests: Discoverability
// ----------------------------------------------------------------------------

func TestProjectAgentsDoc(t *testing.T) {
	root := RootCommand()
	agents := RenderAgents(root)

	wantIntroClause := "project current/list/add/switch act on Sidecar's configured projects, not on the filesystem."
	if !strings.Contains(agents, wantIntroClause) {
		t.Errorf("RenderAgents intro missing clause %q:\n%s", wantIntroClause, agents)
	}

	for _, invocation := range []string{
		"sidecar project current",
		"sidecar project list --json",
		"sidecar project add <name> --path PATH [--switch]",
		"sidecar project switch <name>",
		"sidecar project set <name>",
		"sidecar project remove <name> --yes",
	} {
		if !strings.Contains(agents, invocation) {
			t.Errorf("RenderAgents missing invocation %q:\n%s", invocation, agents)
		}
	}
}

func TestProjectAddHelpSentence(t *testing.T) {
	root := RootCommand()
	projCmd := root.FindSubcommand("project")
	if projCmd == nil {
		t.Fatal("no project command")
	}
	addCmd := projCmd.FindSubcommand("add")
	if addCmd == nil {
		t.Fatal("no project add command")
	}

	wantSentence := "Adding a project does not initialize a Git repository or a td project."
	if !strings.Contains(addCmd.Long, wantSentence) {
		t.Errorf("add.Long missing %q:\n%s", wantSentence, addCmd.Long)
	}
	if !strings.Contains(addCmd.Agent.Summary, "Adding a project does not initialize a Git repository or a td project.") &&
		!strings.Contains(addCmd.Agent.Summary, "adding a project does not initialize a Git repository or a td project") {
		t.Errorf("add.Agent.Summary missing Git/td note:\n%s", addCmd.Agent.Summary)
	}
}
