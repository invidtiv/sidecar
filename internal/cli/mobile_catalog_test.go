package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestParseMobileCatalogArgs(t *testing.T) {
	var stderr bytes.Buffer
	env := Env{Stdout: &bytes.Buffer{}, Stderr: &stderr, Ctx: context.Background()}
	query, code := parseMobileCatalogArgs(env, []string{
		"--json", "--sort=recent", "--search", "sidecar blocked", "--host", "local:aerie", "--host=remote:studio",
		"--provider", "codex", "--state", "working", "--state=ready", "--show-idle-sessions=false",
	}, "help")
	showIdle := false
	want := mobileproto.CatalogQuery{Sort: "recent", Search: "sidecar blocked", Hosts: []string{"local:aerie", "remote:studio"}, Providers: []string{"codex"}, States: []string{"working", "ready"}, ShowIdleSessions: &showIdle}
	if code != 0 || stderr.Len() != 0 || !reflect.DeepEqual(query, want) {
		t.Fatalf("parse = %+v code=%d stderr=%q, want %+v", query, code, stderr.String(), want)
	}
}

func TestCurrentMobileConfigGenerationReadsTheConfiguredFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config.SetTestConfigPath(path)
	t.Cleanup(config.ResetTestConfigPath)
	if err := os.WriteFile(path, []byte(`{"projects":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := currentMobileConfigGeneration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"projects":[{"name":"changed"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := currentMobileConfigGeneration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("config generation did not change: %q", first)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := currentMobileConfigGeneration(context.Background()); err == nil {
		t.Fatal("missing current config was accepted")
	}
}

func TestRunMobileSessionsFailsClosedWithoutCurrentConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-config.json")
	config.SetTestConfigPath(path)
	t.Cleanup(config.ResetTestConfigPath)
	var stdout, stderr bytes.Buffer
	code := runMobileSessions(Env{Ctx: context.Background(), Stdout: &stdout, Stderr: &stderr, StateDir: t.TempDir()}, []string{"--json"})
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "missing-config.json") {
		t.Fatalf("missing config result code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestParseMobileCatalogArgsRequiresStructuredOutputAndValues(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "structured output", args: []string{"--sort", "name"}, want: "--json is required"},
		{name: "missing value", args: []string{"--json", "--host"}, want: "--host requires a value"},
		{name: "invalid idle value", args: []string{"--json", "--show-idle-sessions=maybe"}, want: "--show-idle-sessions must be true or false"},
		{name: "unknown", args: []string{"--json", "--wat=one"}, want: "unknown mobile sessions flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_, code := parseMobileCatalogArgs(Env{Stderr: &stderr}, test.args, "help")
			if code != 2 || !bytes.Contains(stderr.Bytes(), []byte(test.want)) {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestMobileSessionsCommandPublishesCatalogContract(t *testing.T) {
	command := mobileCommand().FindSubcommand("sessions")
	if command == nil || command.Agent.Invocation != "sidecar mobile sessions --json" {
		t.Fatalf("sessions command = %+v", command)
	}
	for _, name := range []string{"--sort", "--search", "--host", "--provider", "--state", "--show-idle-sessions"} {
		found := false
		for _, flag := range command.Flags {
			found = found || flag.Name == name
		}
		if !found {
			t.Fatalf("sessions command omits %s", name)
		}
	}
}

func TestMobileCatalogShellResolverScansProjectsOncePerSnapshot(t *testing.T) {
	stateDir := t.TempDir()
	countPath := filepath.Join(t.TempDir(), "git-count")
	binDir := t.TempDir()
	gitScript := `#!/bin/sh
printf x >> "$MOBILE_GIT_COUNT"
root=
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-C" ]; then
    shift
    root=$1
  fi
  shift
done
printf 'worktree %s\nHEAD 0123456789abcdef\nbranch refs/heads/main\n' "$root"
`
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOBILE_GIT_COUNT", countPath)
	created := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	var sessions []string
	for projectIndex := 1; projectIndex <= 2; projectIndex++ {
		key := fmt.Sprintf("project-%d", projectIndex)
		root := filepath.Join(t.TempDir(), key)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		projectState := filepath.Join(stateDir, "projects", key)
		if err := os.MkdirAll(projectState, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectState, "meta.json"), []byte(fmt.Sprintf(`{"path":%q}`, root)), 0o644); err != nil {
			t.Fatal(err)
		}
		definitions := make([]string, 0, 3)
		for shellIndex := 1; shellIndex <= 3; shellIndex++ {
			session := fmt.Sprintf("sidecar-sh-%d-%d", projectIndex, shellIndex)
			sessions = append(sessions, session)
			definitions = append(definitions, fmt.Sprintf(`{"tmuxName":%q,"displayName":%q,"namespace":%q,"createdAt":%q,"agentType":"codex"}`,
				session, session, tmuxenv.Namespace(), created.Format(time.RFC3339Nano)))
		}
		manifest := fmt.Sprintf(`{"version":3,"shells":[%s]}`, strings.Join(definitions, ","))
		if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inspections := 0
	resolver := newMobileCatalogShellResolver(Env{Ctx: context.Background(), StateDir: stateDir}, func(_ context.Context, session string) (tty.HeadlessTargetIdentity, error) {
		inspections++
		return tty.HeadlessTargetIdentity{ServerPID: 42, SessionID: "$1", SessionCreated: "1700000000", Session: session, Pane: "%1", Width: 80, Height: 24}, nil
	})
	for _, session := range sessions {
		if _, err := resolver(context.Background(), session); err != nil {
			t.Fatalf("resolve %s: %v", session, err)
		}
	}
	count, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(count), 2; got != want {
		t.Fatalf("git discovery calls = %d, want %d projects for %d shells", got, want, len(sessions))
	}
	if inspections != len(sessions) {
		t.Fatalf("pane inspections = %d, want %d", inspections, len(sessions))
	}
}

func TestMobileCatalogProviderPreservesUnknownActivityAgeAcrossReads(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	projectState, err := projectdir.ResolveWithBase(stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 7, 16, 0, 0, 0, time.UTC)
	manifest := fmt.Sprintf(`{"version":3,"shells":[{"tmuxName":"sidecar-sh-repo-1","displayName":"Agent","namespace":%q,"createdAt":%q,"agentType":"codex"}]}`, tmuxenv.Namespace(), created.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	screen := "› Write tests for @filename"
	runner := mobileCatalogRunner{root: root}
	collector := workspaceinventory.Collector{
		Runner: runner,
		Capture: func(string, int) (string, tty.PaneState, error) {
			return screen, tty.PaneState{}, nil
		},
		Now: func() time.Time { return clock },
	}.WithDefaults()
	provider := newMobileCatalogProvider(Env{StateDir: stateDir}, func() ([]hostserve.Project, error) {
		return []hostserve.Project{{Name: "Repo", Path: root}}, nil
	}, collector, nil, func() time.Time { return clock })
	first, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(134 * time.Millisecond)
	second, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstChanged, secondChanged := shellChangedAt(t, first), shellChangedAt(t, second)
	if !firstChanged.IsZero() || !secondChanged.IsZero() {
		t.Fatalf("provider invented activity age without shared history: %v -> %v", firstChanged, secondChanged)
	}
	screen = "• Working (1s • esc to interrupt)"
	clock = clock.Add(time.Second)
	third, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if changed := shellChangedAt(t, third); !changed.Equal(clock) {
		t.Fatalf("provider hid an observed activity transition at %v, want %v", changed, clock)
	}
}

func TestMobileCatalogProviderColdProcessesKeepGenerationStable(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	projectState, err := projectdir.ResolveWithBase(stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 7, 16, 0, 0, 0, time.UTC)
	manifest := fmt.Sprintf(`{"version":3,"shells":[{"tmuxName":"sidecar-sh-repo-1","displayName":"Agent","namespace":%q,"createdAt":%q,"agentType":"codex"}]}`, tmuxenv.Namespace(), created.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	identity := mobile.CatalogIdentity{HubID: host, OwnerHostID: "local:" + host, OwnerConfigGeneration: "config-stable"}
	resolver := func(context.Context, string) (mobile.ResolvedTarget, error) {
		return mobile.ResolvedTarget{
			WorkspaceID: "repo", ProjectRoot: root, WorkspaceKind: string(workspaceinventory.KindShell),
			Session: "sidecar-sh-repo-1", Pane: "%1", DurableSessionCreated: created.Format(time.RFC3339Nano),
			ServerPID: 101, SessionID: "$1", SessionCreated: "1700000000",
		}, nil
	}
	read := func(clock time.Time) mobileproto.CatalogSnapshot {
		collector := workspaceinventory.Collector{
			Runner: mobileCatalogRunner{root: root},
			Capture: func(string, int) (string, tty.PaneState, error) {
				return "› Write tests for @filename", tty.PaneState{}, nil
			},
			Now: func() time.Time { return clock },
		}.WithDefaults()
		provider := newMobileCatalogProvider(Env{StateDir: stateDir}, func() ([]hostserve.Project, error) {
			return []hostserve.Project{{Name: "Repo", Path: root}}, nil
		}, collector, nil, func() time.Time { return clock })
		snapshot, err := mobile.QueryCatalog(context.Background(), provider, resolver, mobileproto.CatalogQuery{}, identity)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	first := read(time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC))
	second := read(time.Date(2026, 9, 7, 18, 0, 1, 0, time.UTC))
	if first.Generation != second.Generation {
		t.Fatalf("cold read generation drifted without activity history: %q -> %q", first.Generation, second.Generation)
	}
	firstChanged := first.Sections[0].Rows[0].ChangedAt
	secondChanged := second.Sections[0].Rows[0].ChangedAt
	if firstChanged != "" || secondChanged != "" {
		t.Fatalf("cold reads invented activity age: %q -> %q", firstChanged, secondChanged)
	}
}

func TestCandidateRevalidationUsesOnlyBoundProjectInventory(t *testing.T) {
	base := t.TempDir()
	root, worktree := filepath.Join(base, "repo"), filepath.Join(base, "worktree")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &mobileCandidateRunner{root: root, worktree: worktree}
	collector := workspaceinventory.Collector{Runner: runner}.WithDefaults()
	loadProjects := func() ([]hostserve.Project, error) {
		return []hostserve.Project{{Name: "Repo", Path: root}}, nil
	}
	source := newMobileCandidateWorkspaceProvider(loadProjects, collector)
	workspaceID := canonicalMobileSourcePath(root) + ":worktree:" + canonicalMobileSourcePath(worktree)
	seed := mobile.ResolvedTarget{WorkspaceID: workspaceID, WorkspaceKind: string(workspaceinventory.KindWorktree),
		SourceProjectKey: canonicalMobileSourcePath(root), SourceWorkspacePath: canonicalMobileSourcePath(worktree)}
	workspace, err := source(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}
	generation, candidates, err := mobile.WorkspaceCandidates(workspace, "local:hub")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("initial candidates generation=%q candidates=%+v err=%v", generation, candidates, err)
	}
	previous := seed
	previous.ProjectRoot, previous.Selector, previous.SourceGeneration = canonicalMobileSourcePath(worktree), candidates[0].Selector, generation
	previous.Session, previous.Pane = candidates[0].Session, candidates[0].Pane
	previous.ServerPID, previous.SessionID, previous.SessionCreated = 202, "$1", "1700000000"
	runner.gitCalls, runner.paneCalls = 0, 0
	got, err := mobile.RevalidateCatalogCandidate(context.Background(), previous, "local:hub", source, func(context.Context, string) (tty.HeadlessTargetIdentity, error) {
		return tty.HeadlessTargetIdentity{ServerPID: 202, SessionID: "$1", SessionCreated: "1700000000", Session: "agent", Pane: "%7", Width: 80, Height: 24}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Selector != previous.Selector || runner.gitCalls != 2 || runner.paneCalls != 2 {
		t.Fatalf("narrow revalidation target=%+v git_calls=%d pane_calls=%d", got, runner.gitCalls, runner.paneCalls)
	}
}

type mobileCatalogRunner struct{ root string }

func (r mobileCatalogRunner) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	switch name {
	case "tmux":
		return []byte(fmt.Sprintf("%%1\tsidecar-sh-repo-1\t%s\tcodex\trepo\t0\t101\t202\t24\n", r.root)), nil
	case "git":
		return []byte(fmt.Sprintf("worktree %s\nHEAD 0123456789abcdef\nbranch refs/heads/main\n", r.root)), nil
	default:
		return nil, fmt.Errorf("unexpected command %s", name)
	}
}

type mobileCandidateRunner struct {
	root, worktree      string
	gitCalls, paneCalls int
}

func (r *mobileCandidateRunner) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	switch name {
	case "git":
		r.gitCalls++
		return []byte(fmt.Sprintf("worktree %s\nHEAD 0123456789abcdef\nbranch refs/heads/main\n\nworktree %s\nHEAD fedcba9876543210\nbranch refs/heads/codex/mobile\n", r.root, r.worktree)), nil
	case "tmux":
		r.paneCalls++
		return []byte(fmt.Sprintf("%%7\tagent\t%s\tcodex\tAgent\t0\t101\t202\t24\n", r.worktree)), nil
	default:
		return nil, fmt.Errorf("unexpected command %s", name)
	}
}

func shellChangedAt(t *testing.T, input mobile.CatalogInput) time.Time {
	t.Helper()
	for _, project := range input.Projects {
		for _, workspace := range project.Result.Workspaces {
			if workspace.Kind == workspaceinventory.KindShell {
				return workspace.Presentation.ChangedAt
			}
		}
	}
	t.Fatal("catalog provider omitted managed shell")
	return time.Time{}
}
