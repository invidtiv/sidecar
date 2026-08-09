package workspaceinventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

type fakeRunner struct {
	git  map[string]string
	tmux string
	list int
}

func (r *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" {
		r.list++
		return []byte(r.tmux), nil
	}
	for i, arg := range args {
		if arg == "-C" && i+1 < len(args) {
			return []byte(r.git[args[i+1]]), nil
		}
	}
	return nil, fmt.Errorf("unexpected command %s %v", name, args)
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
	collector := Collector{Runner: runner, Capture: func(string, int) (string, error) { captures++; return "› Write tests for @filename", nil }}
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
	collector := Collector{Runner: runner, Capture: func(string, int) (string, error) { captures++; return "", nil }}
	result := collector.CollectProject(context.Background(), "repo", root, []string{root}, []Pane{{ID: "%1", Path: root}, {ID: "%2", Path: root}})
	if len(result.Workspaces) != 1 || result.Workspaces[0].Presentation.Freshness != agentstatus.FreshnessUnavailable {
		t.Fatalf("ambiguous result = %#v", result)
	}
	if captures != 0 {
		t.Fatalf("ambiguous panes captured %d times", captures)
	}
}

func TestCollectorPreservesTrackerTransitionsAcrossRefreshes(t *testing.T) {
	outputs := []string{"• Working (1s • esc to interrupt)", "› Write tests for @filename"}
	collector := Collector{Capture: func(string, int) (string, error) {
		out := outputs[0]
		outputs = outputs[1:]
		return out, nil
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
