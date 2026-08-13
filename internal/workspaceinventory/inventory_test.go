package workspaceinventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tty"
)

type fakeRunner struct {
	git     map[string]string
	gitErr  map[string]error
	tmux    string
	tmuxErr error
	list    int
}

func (r *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" {
		r.list++
		return []byte(r.tmux), r.tmuxErr
	}
	for i, arg := range args {
		if arg == "-C" && i+1 < len(args) {
			if err := r.gitErr[args[i+1]]; err != nil {
				return nil, err
			}
			return []byte(r.git[args[i+1]]), nil
		}
	}
	return nil, fmt.Errorf("unexpected command %s %v", name, args)
}

func TestCollectorDistinguishesMissingAndNonGitProjects(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	nonGit := t.TempDir()
	runner := &fakeRunner{gitErr: map[string]error{nonGit: fmt.Errorf("not a git repository")}}
	collector := Collector{Runner: runner}
	if result := collector.CollectProject(context.Background(), "missing", missing, []string{missing}, nil); result.Err == nil || !strings.Contains(result.Err.Error(), "missing") {
		t.Fatalf("missing result = %#v", result)
	}
	if result := collector.CollectProject(context.Background(), "plain", nonGit, []string{nonGit}, nil); result.Err == nil || !strings.Contains(result.Err.Error(), "not a Git repository") {
		t.Fatalf("non-Git result = %#v", result)
	}
}

func TestMissingTmuxServerIsAnEmptyInventory(t *testing.T) {
	runner := &fakeRunner{tmux: "error connecting to /tmp/private/default (No such file or directory)", tmuxErr: fmt.Errorf("exit status 1")}
	panes, err := (Collector{Runner: runner}).ListPanes(context.Background())
	if err != nil || len(panes) != 0 {
		t.Fatalf("missing server panes=%v err=%v", panes, err)
	}
}

func TestTwoProjectInventoryIsReadOnlyAndExcludesPlainShells(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	rootOne, rootTwo := filepath.Join(t.TempDir(), "one"), filepath.Join(t.TempDir(), "two")
	for _, root := range []string{rootOne, rootTwo} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		projectState, err := projectdir.ResolveWithBase(stateBase, root)
		if err != nil {
			t.Fatal(err)
		}
		worktreeState, err := projectdir.WorktreeDirWithBase(stateBase, root, root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktreeState, "agent"), []byte("codex\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest := `{"version":1,"shells":[{"tmuxName":"agent-` + filepath.Base(root) + `","displayName":"Agent shell","namespace":"` + tmuxenv.Namespace() + `","agentType":"claude"},{"tmuxName":"plain-` + filepath.Base(root) + `","displayName":"Plain shell"}]}`
		if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotTree(t, stateBase)
	runner := &fakeRunner{git: map[string]string{
		rootOne: "worktree " + rootOne + "\nHEAD abc\nbranch refs/heads/main\n",
		rootTwo: "worktree " + rootTwo + "\nHEAD def\nbranch refs/heads/main\n",
	}, tmux: strings.Join([]string{
		"%1\tws-one\t" + rootOne + "\tcodex\tOpenAI Codex (v1)\t0",
		"%2\tagent-one\t" + rootOne + "\tclaude\tClaude\t0",
		"%3\tws-two\t" + rootTwo + "\tcodex\tOpenAI Codex (v1)\t0",
		"%4\tagent-two\t" + rootTwo + "\tclaude\tClaude\t0",
	}, "\n")}
	captures := 0
	collector := Collector{Runner: runner, Capture: func(string, int) (string, tty.PaneState, error) {
		captures++
		return "› Write tests for @filename", tty.PaneState{}, nil
	}}
	panes, err := collector.ListPanes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	results := []ProjectResult{
		collector.CollectProject(context.Background(), "one", rootOne, []string{rootOne, rootTwo}, panes),
		collector.CollectProject(context.Background(), "two", rootTwo, []string{rootOne, rootTwo}, panes),
	}
	if runner.list != 1 {
		t.Fatalf("tmux inventories = %d, want 1", runner.list)
	}
	if captures != 4 {
		t.Fatalf("captures = %d, want 4 matched agent panes", captures)
	}
	for _, result := range results {
		if result.Err != nil || len(result.Workspaces) != 2 {
			t.Fatalf("result = %#v", result)
		}
		for _, workspace := range result.Workspaces {
			if strings.HasPrefix(workspace.TmuxName, "plain-") {
				t.Fatal("plain shell included")
			}
		}
	}
	if after := snapshotTree(t, stateBase); !reflect.DeepEqual(before, after) {
		t.Fatalf("collector mutated Sidecar state\nbefore=%v\nafter=%v", before, after)
	}
}

func TestCollectorDiscoversAgentStartedInUntypedShell(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := t.TempDir()
	projectState, err := projectdir.ResolveWithBase(stateBase, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"shells":[` +
		`{"tmuxName":"dynamic-agent","displayName":"Terminal cutover","namespace":"` + tmuxenv.Namespace() + `"},` +
		`{"tmuxName":"plain-shell","displayName":"Shell 8","namespace":"` + tmuxenv.Namespace() + `"}]}`
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{git: map[string]string{root: "worktree " + root + "\nbranch refs/heads/main\n"}}
	collector := Collector{Runner: runner, Capture: func(target string, _ int) (string, tty.PaneState, error) {
		if target == "%1" {
			return "OpenAI Codex (v0.147.0)\n• Working (1s • esc to interrupt)", tty.PaneState{}, nil
		}
		return "$ ", tty.PaneState{}, nil
	}}
	panes := []Pane{
		{ID: "%1", Session: "dynamic-agent", Path: root, Command: "node"},
		{ID: "%2", Session: "plain-shell", Path: root, Command: "zsh"},
	}
	result := collector.CollectProject(context.Background(), "sidecar", root, []string{root}, panes)
	if result.Err != nil || len(result.Workspaces) != 1 {
		t.Fatalf("dynamic shell result = %#v", result)
	}
	got := result.Workspaces[0]
	if got.Kind != KindShell || got.Name != "Terminal cutover" || got.Provider != "codex" || got.PaneID != "%1" || got.Presentation.Lane != agentstatus.LaneWorking {
		t.Fatalf("dynamic agent = %#v", got)
	}
	got.ProjectRoot = root // Preserve the registered spelling used by projectdir metadata.
	if err := collector.ValidateWorkspace(context.Background(), got); err != nil {
		t.Fatalf("ValidateWorkspace(dynamic agent) = %v", err)
	}
}

func TestStatusPollDiscoversAgentStartedAfterUntypedShellWasPlain(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := t.TempDir()
	projectState, err := projectdir.ResolveWithBase(stateBase, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"shells":[{"tmuxName":"late-agent","displayName":"Overview bug","namespace":"` + tmuxenv.Namespace() + `"}]}`
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{git: map[string]string{root: "worktree " + root + "\nbranch refs/heads/main\n"}}
	output := "$ "
	base := Collector{Runner: runner, Capture: func(string, int) (string, tty.PaneState, error) { return output, tty.PaneState{}, nil }}.WithDefaults()
	inventory := base.CollectProjectInventory(context.Background(), "sidecar", root)
	collector := base.ForRefresh(1, BuildShellClaims([]ProjectResult{inventory}))

	first := collector.RefreshProjectStatus(context.Background(), inventory, []string{root}, []Pane{{ID: "%1", Session: "late-agent", Path: root, Command: "zsh"}})
	firstShell, ok := shellNamed(first, "late-agent")
	if !ok || firstShell.Provider != "" {
		t.Fatalf("plain candidate was not retained internally: %#v", first.Workspaces)
	}

	output = "OpenAI Codex (v0.147.0)\n• Working (1s • esc to interrupt)"
	second := collector.RefreshProjectStatus(context.Background(), first, []string{root}, []Pane{{ID: "%1", Session: "late-agent", Path: root, Command: "node"}})
	secondShell, ok := shellNamed(second, "late-agent")
	if !ok || secondShell.Provider != "codex" || secondShell.Presentation.Lane != agentstatus.LaneWorking {
		t.Fatalf("late agent was not discovered by status poll: %#v", second.Workspaces)
	}
}

func TestAmbiguousWorktreePanesAreUnavailableAndNotCaptured(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := projectdir.ResolveWithBase(stateBase, root); err != nil {
		t.Fatal(err)
	}
	wtState, err := projectdir.WorktreeDirWithBase(stateBase, root, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtState, "agent"), []byte("codex"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{git: map[string]string{root: "worktree " + root + "\nbranch refs/heads/main\n"}}
	captures := 0
	collector := Collector{Runner: runner, Capture: func(string, int) (string, tty.PaneState, error) { captures++; return "", tty.PaneState{}, nil }}
	result := collector.CollectProject(context.Background(), "repo", root, []string{root}, []Pane{{ID: "%1", Path: root}, {ID: "%2", Path: root}})
	if len(result.Workspaces) != 1 || result.Workspaces[0].Presentation.Freshness != agentstatus.FreshnessUnavailable || !result.Workspaces[0].IsMain {
		t.Fatalf("ambiguous result = %#v", result)
	}
	if captures != 0 {
		t.Fatalf("ambiguous panes captured %d times", captures)
	}
}

func TestResolveWorktreePanesIgnoresChromeAndPrefersWorkspaceSession(t *testing.T) {
	root := "/repo"
	workspace := Workspace{Path: root, Name: "repo"}
	cases := []struct {
		name    string
		panes   []Pane
		wantIDs []string
	}{
		{
			name: "worktree plus term panel prefers ws",
			panes: []Pane{
				{ID: "%1", Session: "sidecar-ws-repo", Path: root},
				{ID: "%2", Session: "sidecar-tp-repo", Path: root},
			},
			wantIDs: []string{"%1"},
		},
		{
			name: "worktree plus two editors and shells prefers ws",
			panes: []Pane{
				{ID: "%10", Session: "sidecar-edit-1", Path: root},
				{ID: "%11", Session: "sidecar-edit-2", Path: root},
				{ID: "%12", Session: "sidecar-sh-repo-1", Path: root},
				{ID: "%13", Session: "sidecar-sh-repo-2", Path: root},
				{ID: "%14", Session: "sidecar-ws-repo", Path: root},
			},
			wantIDs: []string{"%14"},
		},
		{
			name: "two unmanaged panes stay rivals",
			panes: []Pane{
				{ID: "%1", Session: "scratch-a", Path: root},
				{ID: "%2", Session: "scratch-b", Path: root},
			},
			wantIDs: []string{"%1", "%2"},
		},
		{
			name: "shells are never the worktree pane",
			panes: []Pane{
				{ID: "%1", Session: "sidecar-sh-repo-1", Path: root},
				{ID: "%2", Session: "sidecar-ws-repo", Path: root},
			},
			wantIDs: []string{"%2"},
		},
		{
			name: "two live worktree sessions stay ambiguous",
			panes: []Pane{
				{ID: "%1", Session: "sidecar-ws-one", Path: root},
				{ID: "%2", Session: "sidecar-ws-two", Path: root},
			},
			wantIDs: []string{"%1", "%2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveWorktreePanes(workspace, tc.panes)
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("panes = %#v, want ids %v", got, tc.wantIDs)
			}
			for i, id := range tc.wantIDs {
				if got[i].ID != id {
					t.Fatalf("pane %d = %#v, want %s", i, got[i], id)
				}
			}
		})
	}
}

func TestWorktreeChromeDoesNotMakeOutputAmbiguous(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	projectState, err := projectdir.ResolveWithBase(stateBase, root)
	if err != nil {
		t.Fatal(err)
	}
	wtState, err := projectdir.WorktreeDirWithBase(stateBase, root, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtState, "agent"), []byte("codex"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"shells":[{"tmuxName":"sidecar-sh-repo-1","displayName":"Shell 1","namespace":"` + tmuxenv.Namespace() + `","agentType":"claude"}]}`
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{git: map[string]string{root: "worktree " + root + "\nbranch refs/heads/main\n"}}
	var captured []string
	collector := Collector{Runner: runner, Capture: func(id string, _ int) (string, error) {
		captured = append(captured, id)
		return "• Working (1s • esc to interrupt)", nil
	}}
	panes := []Pane{
		{ID: "%ws", Session: "sidecar-ws-repo", Path: root, Command: "grok"},
		{ID: "%tp", Session: "sidecar-tp-repo", Path: root, Command: "zsh"},
		{ID: "%e1", Session: "sidecar-edit-1", Path: root, Command: "nvim"},
		{ID: "%e2", Session: "sidecar-edit-2", Path: root, Command: "nvim"},
		{ID: "%sh", Session: "sidecar-sh-repo-1", Path: root, Command: "claude"},
	}
	result := collector.CollectProject(context.Background(), "repo", root, []string{root}, panes)
	if result.Err != nil {
		t.Fatalf("collect: %v", result.Err)
	}
	worktree, shell, ok := worktreeAndShell(result)
	if !ok {
		t.Fatalf("workspaces = %#v, want one worktree and one shell", result.Workspaces)
	}
	if worktree.Ambiguous || !worktree.Live || worktree.PaneID != "%ws" || worktree.TmuxName != "sidecar-ws-repo" {
		t.Fatalf("worktree = %#v, want live pane %%ws", worktree)
	}
	if worktree.Presentation.Freshness == agentstatus.FreshnessUnavailable {
		t.Fatalf("worktree presentation unavailable: %#v", worktree.Presentation)
	}
	if shell.PaneID != "%sh" || shell.TmuxName != "sidecar-sh-repo-1" {
		t.Fatalf("shell = %#v, want its own session", shell)
	}
	if len(captured) != 2 || !containsString(captured, "%ws") || !containsString(captured, "%sh") {
		t.Fatalf("captured = %v, want worktree %%ws and shell %%sh", captured)
	}
}

func worktreeAndShell(result ProjectResult) (worktree, shell Workspace, ok bool) {
	for _, workspace := range result.Workspaces {
		switch workspace.Kind {
		case KindWorktree:
			worktree = workspace
		case KindShell:
			shell = workspace
		}
	}
	return worktree, shell, worktree.ID != "" && shell.ID != ""
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCollectorMatchesLiveSiblingWorktreeWithoutConfiguredOwner(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	base := t.TempDir()
	projectRoot := filepath.Join(base, "sidecar")
	worktree := filepath.Join(base, "sidecar-terminal-cutover")
	for _, path := range []string{projectRoot, worktree} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stateDir, err := projectdir.WorktreeDirWithBase(stateBase, projectRoot, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "agent"), []byte("codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{git: map[string]string{projectRoot: strings.Join([]string{
		"worktree " + projectRoot,
		"branch refs/heads/main",
		"",
		"worktree " + worktree,
		"branch refs/heads/terminal-cutover",
		"",
	}, "\n")}}
	collector := Collector{Runner: runner, Capture: func(target string, _ int) (string, tty.PaneState, error) {
		if target != "%40" {
			t.Fatalf("captured pane %q, want %%40", target)
		}
		return "• Working (1s • esc to interrupt)", tty.PaneState{}, nil
	}}
	result := collector.CollectProject(context.Background(), "sidecar", projectRoot, []string{projectRoot}, []Pane{{
		ID: "%40", Session: "sidecar-ws-sidecar-terminal-cutover", Path: worktree, Command: "node",
	}})
	if result.Err != nil || len(result.Workspaces) != 1 {
		t.Fatalf("sibling worktree result = %#v", result)
	}
	got := result.Workspaces[0]
	if got.PaneID != "%40" || got.Presentation.Lane != agentstatus.LaneWorking {
		t.Fatalf("sibling worktree = %#v, want pane %%40 in Working", got)
	}
}

func TestPanesForPathDoesNotClaimNestedConfiguredProject(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "projects")
	nested := filepath.Join(parent, "nested")
	panes := []Pane{{ID: "%7", Session: "nested-agent", Path: nested}}

	if got := panesForPath(parent, []string{parent, nested}, panes, nil); len(got) != 0 {
		t.Fatalf("parent claimed nested configured project panes: %#v", got)
	}
}

func TestCollectorPreservesTrackerTransitionsAcrossRefreshes(t *testing.T) {
	outputs := []string{"• Working (1s • esc to interrupt)", "› Write tests for @filename"}
	collector := Collector{Capture: func(string, int) (string, tty.PaneState, error) {
		out := outputs[0]
		outputs = outputs[1:]
		return out, tty.PaneState{}, nil
	}}.WithDefaults()
	pane := []Pane{{ID: "%1", Session: "agent", Path: "/tmp/repo", Command: "codex"}}
	first := Workspace{ID: "repo:worktree:one", Provider: "codex"}
	collector.observe(&first, pane, collector.Now())
	if first.Presentation.Lane != agentstatus.LaneWorking {
		t.Fatalf("first presentation = %#v", first.Presentation)
	}
	second := Workspace{ID: first.ID, Provider: "codex"}
	collector.observe(&second, pane, collector.Now())
	if second.Presentation.Lane != agentstatus.LaneDone {
		t.Fatalf("working -> idle presentation = %#v, want Done", second.Presentation)
	}
}

func TestRefreshCollectorBoundsMatchedPaneCaptures(t *testing.T) {
	collector := (Collector{Capture: func(string, int) (string, tty.PaneState, error) {
		time.Sleep(10 * time.Millisecond)
		return "› ready", tty.PaneState{}, nil
	}}).ForRefresh(2)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			workspace := Workspace{ID: fmt.Sprintf("agent-%d", i), Provider: "codex"}
			collector.observeContext(context.Background(), &workspace, []Pane{{ID: fmt.Sprintf("%%%d", i), Command: "codex"}}, time.Now())
		}(i)
	}
	wg.Wait()
	metrics := collector.Metrics()
	if metrics.Captures != 8 || metrics.MaxCaptures != 2 {
		t.Fatalf("capture metrics = %#v, want 8 captures bounded at 2", metrics)
	}
}

func TestLiveStatusRefreshReusesInventoryWithoutGitOrMetadataReads(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	collector := (Collector{Runner: runner, Capture: func(string, int) (string, tty.PaneState, error) {
		return "• Working (1s • esc to interrupt)", tty.PaneState{}, nil
	}}).ForRefresh(2)
	previous := ProjectResult{ProjectKey: canonical(root), ProjectRoot: root, Workspaces: []Workspace{{
		ID: "repo:worktree:agent", ProjectKey: canonical(root), ProjectRoot: root, Kind: KindWorktree, Path: root, Provider: "codex",
	}}}
	result := collector.RefreshProjectStatus(context.Background(), previous, []string{root}, []Pane{{ID: "%1", Path: root, Command: "codex"}})
	if runner.list != 0 || collector.Metrics().ProjectOps != 0 {
		t.Fatalf("live refresh performed inventory work: runner=%#v metrics=%#v", runner, collector.Metrics())
	}
	if len(result.Workspaces) != 1 || result.Workspaces[0].Presentation.Lane != agentstatus.LaneWorking {
		t.Fatalf("live refresh result = %#v", result)
	}
}

func TestCanonicalProjectPathDeduplicatesLinkedWorktreeIdentity(t *testing.T) {
	base := t.TempDir()
	mainRoot := filepath.Join(base, "main")
	linked := filepath.Join(base, "linked")
	gitDir := filepath.Join(mainRoot, ".git", "worktrees", "linked")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CanonicalProjectPath(linked); got != canonical(mainRoot) {
		t.Fatalf("linked identity = %q, want main %q", got, canonical(mainRoot))
	}
}

func TestShellSessionCollisionRequiresExactProjectPathOwnership(t *testing.T) {
	base := t.TempDir()
	one, two := filepath.Join(base, "one"), filepath.Join(base, "two")
	for _, root := range []string{one, two} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	panes := []Pane{{ID: "%1", Session: "shared", Path: one}}
	if got := panesForOwnedSession("shared", one, []string{one, two}, panes); len(got) != 1 {
		t.Fatalf("owner match = %#v", got)
	}
	if got := panesForOwnedSession("shared", two, []string{one, two}, panes); len(got) != 0 {
		t.Fatalf("colliding project claimed pane = %#v", got)
	}
	unique := map[string]string{"shared": canonical(one)}
	roaming := []Pane{{ID: "%2", Session: "shared", Path: t.TempDir()}}
	if got := panesForOwnedSession("shared", one, []string{one, two}, roaming, unique); len(got) != 1 {
		t.Fatalf("durably owned roaming shell = %#v", got)
	}
	ambiguous := map[string]string{"shared": ""}
	if got := panesForOwnedSession("shared", one, []string{one, two}, panes, ambiguous); len(got) != 0 {
		t.Fatalf("ambiguous shell claim = %#v", got)
	}
}

func TestAgentShellClaimsRefuseDuplicateDurableOwners(t *testing.T) {
	results := []ProjectResult{
		{ProjectKey: "/one", Workspaces: []Workspace{{Kind: KindShell, TmuxName: "shared", Provider: "codex"}}},
		{ProjectKey: "/two", Workspaces: []Workspace{{Kind: KindShell, TmuxName: "shared", Provider: "claude", Namespace: tmuxenv.Namespace()}}},
	}
	claims := BuildShellClaims(results)
	if !claims.Sessions["shared"] || claims.Owners["shared"] != "" {
		t.Fatalf("duplicate claims = %#v", claims)
	}
}

func TestShellMetadataRefusesBlockingFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "shells.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan []shellDefinition, 1)
	go func() { done <- readShells(fifo) }()
	select {
	case shells := <-done:
		if len(shells) != 0 {
			t.Fatalf("FIFO metadata parsed as shells: %#v", shells)
		}
	case <-time.After(time.Second):
		t.Fatal("shell metadata blocked on FIFO")
	}
}

func TestLegacyAgentShellSessionIsReservedFromWorktreeCapture(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := t.TempDir()
	projectState, err := projectdir.ResolveWithBase(stateBase, root)
	if err != nil {
		t.Fatal(err)
	}
	worktreeState, err := projectdir.WorktreeDirWithBase(stateBase, root, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeState, "agent"), []byte("codex"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"shells":[{"tmuxName":"legacy-agent","displayName":"Legacy","agentType":"claude"}]}`
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{git: map[string]string{root: "worktree " + root + "\nbranch refs/heads/main\n"}}
	captures := 0
	base := Collector{Runner: runner, Capture: func(string, int) (string, tty.PaneState, error) { captures++; return "working", tty.PaneState{}, nil }}.WithDefaults()
	inventory := base.CollectProjectInventory(context.Background(), "repo", root)
	claims := BuildShellClaims([]ProjectResult{inventory})
	result := base.ForRefresh(4, claims).RefreshProjectStatus(context.Background(), inventory, []string{root}, []Pane{{ID: "%1", Session: "legacy-agent", Path: root, Command: "codex"}})
	if captures != 0 || !claims.Sessions["legacy-agent"] {
		t.Fatalf("legacy session captures=%d claims=%#v", captures, claims)
	}
	for _, workspace := range result.Workspaces {
		if workspace.PaneID != "" || workspace.Presentation.Freshness != agentstatus.FreshnessUnavailable {
			t.Fatalf("legacy collision workspace = %#v", workspace)
		}
	}
}

func TestCanceledCaptureCannotMutateSharedTracker(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int64
	base := (Collector{Capture: func(string, int) (string, tty.PaneState, error) {
		call := atomic.AddInt64(&calls, 1)
		if call == 1 {
			close(started)
			<-release
			return "› Write tests for @filename", tty.PaneState{}, nil
		}
		return "• Working (1s • esc to interrupt)", tty.PaneState{}, nil
	}}).WithDefaults()
	oldCollector := base.ForRefresh(1)
	oldCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		workspace := Workspace{ID: "same-agent", Provider: "codex"}
		oldCollector.observeContext(oldCtx, &workspace, []Pane{{ID: "%1", Command: "codex"}}, time.Now())
		close(done)
	}()
	<-started
	cancel()
	currentCollector := base.ForRefresh(1)
	current := Workspace{ID: "same-agent", Provider: "codex"}
	currentCollector.observeContext(context.Background(), &current, []Pane{{ID: "%1", Command: "codex"}}, time.Now().Add(time.Second))
	currentCollector.CommitTrackers()
	close(release)
	<-done
	base.trackers.mu.Lock()
	tracker := base.trackers.values["same-agent"]
	base.trackers.mu.Unlock()
	if tracker.State != agentactivity.StateWorking {
		t.Fatalf("canceled capture contaminated tracker: %#v", tracker)
	}
}

func TestCanceledLocalTrackerApplyCannotReachCommittedState(t *testing.T) {
	applyLocked := make(chan struct{})
	releaseApply := make(chan struct{})
	base := (Collector{
		Capture: func(string, int) (string, tty.PaneState, error) {
			return "› Write tests for @filename", tty.PaneState{}, nil
		},
		beforeTrackerApply: func() {
			close(applyLocked)
			<-releaseApply
		},
	}).WithDefaults()
	refresh := base.ForRefresh(1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		workspace := Workspace{ID: "same-agent", Provider: "codex"}
		refresh.observeContext(ctx, &workspace, []Pane{{ID: "%1", Command: "codex"}}, time.Now())
		close(done)
	}()
	<-applyLocked
	cancel()
	close(releaseApply)
	<-done
	base.trackers.mu.Lock()
	_, committed := base.trackers.values["same-agent"]
	base.trackers.mu.Unlock()
	if committed || refresh.Metrics().TrackerCommits != 0 {
		t.Fatalf("canceled local apply reached base: committed=%v metrics=%#v", committed, refresh.Metrics())
	}
}

func TestSuccessfulRefreshCommitsTrackerContinuity(t *testing.T) {
	outputs := []string{"• Working (1s • esc to interrupt)", "› Write tests for @filename"}
	base := (Collector{Capture: func(string, int) (string, tty.PaneState, error) {
		output := outputs[0]
		outputs = outputs[1:]
		return output, tty.PaneState{}, nil
	}}).WithDefaults()
	first := base.ForRefresh(1)
	working := Workspace{ID: "same-agent", Provider: "codex"}
	first.observeContext(context.Background(), &working, []Pane{{ID: "%1", Command: "codex"}}, time.Now())
	first.CommitTrackers()
	second := base.ForRefresh(1)
	idle := Workspace{ID: "same-agent", Provider: "codex"}
	second.observeContext(context.Background(), &idle, []Pane{{ID: "%1", Command: "codex"}}, time.Now().Add(time.Second))
	if idle.Presentation.Lane != agentstatus.LaneDone {
		t.Fatalf("committed Working -> idle presentation = %#v", idle.Presentation)
	}
	second.CommitTrackers()
	if second.Metrics().TrackerCommits != 1 {
		t.Fatalf("successful commit metrics = %#v", second.Metrics())
	}
}

func TestValidateWorkspaceRechecksExactWorktreeWithoutMutatingState(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(t.TempDir(), "topic")
	for _, path := range []string{root, worktree} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stateDir, err := projectdir.WorktreeDirWithBase(stateBase, root, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "agent"), []byte("codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := canonical(worktree)
	workspace := Workspace{ProjectKey: canonical(root), ProjectRoot: root, Kind: KindWorktree, Key: key, Path: worktree}
	runner := &fakeRunner{git: map[string]string{root: "worktree " + worktree + "\nbranch refs/heads/topic\n"}}
	collector := Collector{Runner: runner}
	before := snapshotTree(t, stateBase)
	if err := collector.ValidateWorkspace(context.Background(), workspace); err != nil {
		t.Fatalf("valid worktree rejected: %v", err)
	}
	if after := snapshotTree(t, stateBase); !reflect.DeepEqual(before, after) {
		t.Fatalf("validation mutated Sidecar state\nbefore=%v\nafter=%v", before, after)
	}
	runner.git[root] = "worktree " + root + "\nbranch refs/heads/main\n"
	if err := collector.ValidateWorkspace(context.Background(), workspace); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("stale worktree validation = %v", err)
	}
}

func TestValidateWorkspaceRejectsRemovedShellIdentityWithoutMutatingState(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	projectState, err := projectdir.ResolveWithBase(stateBase, root)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(projectState, "shells.json")
	if err := os.WriteFile(manifestPath, []byte(`{"version":1,"shells":[{"tmuxName":"agent-shell","displayName":"Agent","agentType":"claude"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := Workspace{ProjectKey: canonical(root), ProjectRoot: root, Kind: KindShell, Key: "agent-shell", TmuxName: "agent-shell"}
	collector := Collector{Runner: &fakeRunner{}}
	if err := collector.ValidateWorkspace(context.Background(), workspace); err != nil {
		t.Fatalf("valid shell rejected: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"version":1,"shells":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, stateBase)
	if err := collector.ValidateWorkspace(context.Background(), workspace); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("stale shell validation = %v", err)
	}
	if after := snapshotTree(t, stateBase); !reflect.DeepEqual(before, after) {
		t.Fatalf("shell validation mutated Sidecar state\nbefore=%v\nafter=%v", before, after)
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// shellNamed finds one shell row in a result. The catalog now also carries
// plain worktrees, so a test about a shell has to name the shell.
func shellNamed(result ProjectResult, tmuxName string) (Workspace, bool) {
	for _, workspace := range result.Workspaces {
		if workspace.Kind == KindShell && workspace.Key == tmuxName {
			return workspace, true
		}
	}
	return Workspace{}, false
}

// TestCatalogIncludesPlainWorkspacesWithoutFabricatingAgentStatus covers slice 2
// item 1: the read-only inventory became an all-workspace catalog, and the
// Agents projection over it is exactly what the board collected before.
func TestCatalogIncludesPlainWorkspacesWithoutFabricatingAgentStatus(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := filepath.Join(t.TempDir(), "repo")
	linked := filepath.Join(t.TempDir(), "repo-topic")
	for _, dir := range []string{root, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projectState, err := projectdir.ResolveWithBase(stateBase, root)
	if err != nil {
		t.Fatal(err)
	}
	// The main worktree carries a recorded agent; the linked worktree carries
	// nothing at all and has no session.
	worktreeState, err := projectdir.WorktreeDirWithBase(stateBase, root, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeState, "agent"), []byte("codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"shells":[{"tmuxName":"plain-shell","displayName":"Shell 1","namespace":"` + tmuxenv.Namespace() + `"}]}`
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, stateBase)

	runner := &fakeRunner{git: map[string]string{root: "worktree " + root + "\nbranch refs/heads/main\nworktree " + linked + "\nbranch refs/heads/topic\n"}}
	captures := 0
	base := Collector{Runner: runner, Capture: func(string, int) (string, tty.PaneState, error) { captures++; return "$ ", tty.PaneState{}, nil }}.WithDefaults()
	inventory := base.CollectProjectInventory(context.Background(), "repo", root)
	collector := base.ForRefresh(2, BuildShellClaims([]ProjectResult{inventory}))
	result := collector.RefreshProjectStatus(context.Background(), inventory, []string{root}, []Pane{
		{ID: "%1", Session: "plain-shell", Path: root, Command: "zsh"},
	})

	catalog := Catalog(result)
	if len(catalog) != 3 {
		t.Fatalf("catalog = %#v, want main worktree, linked worktree, and the plain shell", catalog)
	}
	byName := map[string]Item{}
	for _, item := range catalog {
		byName[item.Name] = item
	}
	main, linkedItem, shell := byName[filepath.Base(root)], byName[filepath.Base(linked)], byName["Shell 1"]
	if main.Agent == nil || main.Provider != "codex" {
		t.Fatalf("agent worktree lost its status: %#v", main)
	}
	if linkedItem.Agent != nil || linkedItem.Provider != "" || linkedItem.Live || linkedItem.Branch != "topic" {
		t.Fatalf("plain worktree was given a fabricated agent state: %#v", linkedItem)
	}
	if shell.Agent != nil {
		t.Fatalf("an unidentified shell is not an agent: %#v", shell)
	}
	if !shell.Live || shell.PaneID != "%1" {
		t.Fatalf("plain shell lost its session health: %#v", shell)
	}

	// The plain worktree costs no pane capture: correlation is enough to know
	// whether it is live, and capturing it would be work for a row with no
	// agent semantics.
	if captures != 1 {
		t.Fatalf("captures = %d, want only the shell's discovery capture", captures)
	}

	// The Agents projection is the agent-only subset.
	agents := AgentWorkspaces(result.Workspaces)
	if len(agents) != 1 || agents[0].Provider != "codex" {
		t.Fatalf("agent projection = %#v", agents)
	}

	// Cataloguing is read-only.
	if after := snapshotTree(t, stateBase); !reflect.DeepEqual(before, after) {
		t.Fatalf("catalog collection mutated Sidecar state\nbefore=%v\nafter=%v", before, after)
	}
}

// Slice 4 item 1 of docs/plans/active/global-overview-workspaces.md: the global
// browser opens plain rows too, so validation must recheck what a plain
// workspace actually has — Git's own worktree list — instead of demanding the
// agent identity it was never collected with.
func TestValidateWorkspaceAcceptsAPlainWorktreeAndStillGuardsAgentIdentity(t *testing.T) {
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	root := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(t.TempDir(), "topic")
	for _, path := range []string{root, worktree} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fakeRunner{git: map[string]string{root: "worktree " + root + "\nbranch refs/heads/main\n\nworktree " + worktree + "\nbranch refs/heads/topic\n"}}
	collector := Collector{Runner: runner}

	// The main worktree and an agentless linked worktree both validate with no
	// recorded agent anywhere in state.
	for _, path := range []string{root, worktree} {
		plain := Workspace{ProjectKey: canonical(root), ProjectRoot: root, Kind: KindWorktree, Key: canonical(path), Path: path, Plain: true}
		if err := collector.ValidateWorkspace(context.Background(), plain); err != nil {
			t.Fatalf("plain worktree %q rejected: %v", path, err)
		}
	}
	if after := snapshotTree(t, stateBase); len(after) != 0 {
		t.Fatalf("validating a plain worktree wrote state: %v", after)
	}

	// A worktree Git no longer lists is refused whether or not it had an agent.
	missing := Workspace{ProjectKey: canonical(root), ProjectRoot: root, Kind: KindWorktree, Key: canonical(root) + "-gone", Path: root + "-gone", Plain: true}
	if err := collector.ValidateWorkspace(context.Background(), missing); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("removed plain worktree validation = %v", err)
	}

	// An agent-backed worktree keeps its stricter identity check.
	agent := Workspace{ProjectKey: canonical(root), ProjectRoot: root, Kind: KindWorktree, Key: canonical(worktree), Path: worktree}
	if err := collector.ValidateWorkspace(context.Background(), agent); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("agent worktree without a recorded agent = %v", err)
	}
}
