package sessionrestore

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceops"
)

func TestSupplementalCandidatesFiltersNamespaceAndOwnedKinds(t *testing.T) {
	stateDir := t.TempDir()
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetSessionsPaneLayout("local", &state.PaneLayoutJSON{Root: "/work/topic", Open: true, Split: &state.PaneSplitJSON{
		Axis: "cols", A: &state.PaneLayoutJSON{Kind: "terminal"}, B: &state.PaneLayoutJSON{Kind: "shell", Session: "sidecar-tp-topic"},
	}}); err != nil {
		t.Fatal(err)
	}
	path := workspaceops.RecoverySessionsPath(stateDir)
	now := time.Now().UTC()
	for _, def := range []shellstate.Definition{
		{TmuxName: "sidecar-ws-topic", DisplayName: "topic", Namespace: "/tmp/ours", WorkDir: "/work/topic", CreatedAt: now, Restore: &shellstate.RestoreState{Eligible: true, LastSeenServer: "pid=1"}},
		{TmuxName: "sidecar-tp-topic", DisplayName: "terminal", Namespace: "/tmp/ours", WorkDir: "/work/topic", CreatedAt: now, Restore: &shellstate.RestoreState{Eligible: true, LastSeenServer: "pid=1"}},
		{TmuxName: "sidecar-tp-hidden", DisplayName: "hidden", Namespace: "/tmp/ours", WorkDir: "/work/topic", CreatedAt: now, Restore: &shellstate.RestoreState{Eligible: true, LastSeenServer: "pid=1"}},
		{TmuxName: "sidecar-ws-remote", DisplayName: "remote", Namespace: "/tmp/theirs", WorkDir: "/remote/topic", CreatedAt: now},
		{TmuxName: "sidecar-ws-legacy", DisplayName: "legacy", WorkDir: "/work/legacy", CreatedAt: now},
		{TmuxName: "third-party", DisplayName: "other", Namespace: "/tmp/ours", WorkDir: "/work/nope", CreatedAt: now},
	} {
		if err := shellstate.AddAtPath(path, def); err != nil {
			t.Fatal(err)
		}
	}
	got, err := (Collector{StateDir: stateDir, Namespace: "/tmp/ours"}).SupplementalCandidates(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Def.TmuxName != "sidecar-tp-topic" || got[1].Def.TmuxName != "sidecar-ws-topic" {
		t.Fatalf("candidates = %+v", got)
	}
	for _, candidate := range got {
		if candidate.ManifestPath != path || candidate.ProjectRoot != candidate.Def.WorkDir || candidate.Project != filepath.Base(candidate.Def.WorkDir) {
			t.Fatalf("candidate = %+v", candidate)
		}
	}
}

type oneProviderCandidateFinder struct{}

func (oneProviderCandidateFinder) Find(q agentsession.CandidateQuery) (agentsession.Candidate, bool, error) {
	if q.AgentKind != "claude" {
		return agentsession.Candidate{}, false, nil
	}
	value := "conversation-1"
	if len(q.Claimed) > 0 {
		value = "conversation-2"
	}
	return agentsession.Candidate{Ref: agentsession.Ref{Kind: agentsession.RefID, Value: value}, Confidence: agentsession.CandidateLikely, Reason: "fixture"}, true, nil
}

func TestSupplementalCandidatesRecoversLegacyOpenLayoutsWithLossEvidence(t *testing.T) {
	stateDir := t.TempDir()
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	project := filepath.Dir(worktree)
	layout := &state.PaneLayoutJSON{Root: worktree, ProjectRoot: project, WorkspaceKind: "worktree", Open: true,
		Split: &state.PaneSplitJSON{Axis: "cols", A: &state.PaneLayoutJSON{Kind: "terminal"}, B: &state.PaneLayoutJSON{Kind: "shell", Session: "sidecar-tp-topic", Name: "logs"}}}
	if err := state.SetSessionsPaneLayout("local-worktree", layout); err != nil {
		t.Fatal(err)
	}
	lostAt := time.Now().UTC()
	createdAt := lostAt.Add(-2 * time.Hour)
	prior := []Shell{{ProjectRoot: project, Def: shellstate.Definition{CreatedAt: createdAt, Restore: &shellstate.RestoreState{
		Eligible: true, LastSeenServer: "pid=41", ServerLostAt: lostAt,
	}}}}
	got, err := (Collector{StateDir: stateDir, Namespace: "/tmp/ours"}).SupplementalCandidates(prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("legacy candidates = %+v, want worktree and split", got)
	}
	names := map[string]Shell{}
	for _, candidate := range got {
		names[candidate.Def.TmuxName] = candidate
	}
	worktreeName := workspaceops.WorktreeSessionName(worktree, filepath.Base(worktree))
	if names[worktreeName].Def.WorkDir != worktree || names["sidecar-tp-topic"].Def.WorkDir != worktree {
		t.Fatalf("legacy candidates = %+v", got)
	}
	if names[worktreeName].Def.Restore.LastSeenServer != "pid=41" {
		t.Fatalf("loss evidence not carried: %+v", names[worktreeName])
	}
	if !names[worktreeName].Def.CreatedAt.Equal(createdAt) {
		t.Fatalf("parent lifetime lower bound not carried: %+v", names[worktreeName].Def)
	}
	collector := Collector{CandidateFinder: oneProviderCandidateFinder{}}
	if err := collector.discoverCandidates(got, false); err != nil {
		t.Fatal(err)
	}
	plan := Build(Input{Config: DefaultConfig(), CurrentServer: "pid=42", Live: map[string]LiveState{}, Shells: got,
		DirExists: func(string) bool { return true }, ProviderAvailable: func(string) bool { return true },
		Request: Request{Agents: true, Prefill: true}})
	actions := map[string]Action{}
	for _, step := range plan.Steps {
		actions[step.Session] = step.Action
	}
	if actions[worktreeName] != ActionPrefillResume || actions["sidecar-tp-topic"] != ActionPrefillResume {
		t.Fatalf("candidate actions = %+v, want worktree and split prefill", actions)
	}
	auto := Build(Input{Config: Config{RecreateShells: true, ResumeAgents: ResumeAuto}, CurrentServer: "pid=42", Live: map[string]LiveState{}, Shells: got,
		DirExists: func(string) bool { return true }, ProviderAvailable: func(string) bool { return true },
		Request: Request{Agents: true, Prefill: true}})
	for _, step := range auto.Steps {
		if step.Action == ActionResumeAgent || step.ExternalExecution {
			t.Fatalf("inferred candidate executed under auto: %+v", step)
		}
	}
}
