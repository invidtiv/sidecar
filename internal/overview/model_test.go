package overview

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

type stageRunner struct {
	tmuxCalls atomic.Int64
	gitCalls  atomic.Int64
}

func (r *stageRunner) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	if name == "tmux" {
		r.tmuxCalls.Add(1)
		return nil, nil
	}
	r.gitCalls.Add(1)
	return nil, nil
}

func TestOverviewIncrementalPartialErrorAndCompactStates(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "one", Path: "/tmp/one"}, {Name: "two", Path: "/tmp/two"}}
	m.roots = []string{"/tmp/one", "/tmp/two"}
	m.generation = 7
	m.loading = true
	m.syncBoard()
	if view := m.View(120, 24); !strings.Contains(view, "Loading") {
		t.Fatalf("loading view missing state: %q", view)
	}
	workspace := workspaceinventory.Workspace{ID: "one:worktree:a", ProjectKey: workspaceinventory.CanonicalPath("/tmp/one"), ProjectName: "one", ProjectRoot: "/tmp/one", Kind: workspaceinventory.KindWorktree, Key: "a", Name: "agent", Provider: "codex", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, Label: "working", Freshness: agentstatus.FreshnessCurrent}}
	m.Update(projectMsg{Generation: 7, Result: workspaceinventory.ProjectResult{ProjectKey: workspaceinventory.CanonicalPath("/tmp/one"), ProjectName: "one", ProjectRoot: "/tmp/one", Workspaces: []workspaceinventory.Workspace{workspace}}})
	m.Update(projectMsg{Generation: 7, Result: workspaceinventory.ProjectResult{ProjectKey: workspaceinventory.CanonicalPath("/tmp/two"), ProjectName: "two", ProjectRoot: "/tmp/two", Err: errors.New("missing repo")}})
	view := m.View(150, 24)
	if !strings.Contains(view, "one / agent") || !strings.Contains(view, "project unavailable") {
		t.Fatalf("partial/error view = %q", view)
	}
	compact := m.View(60, 12)
	if !strings.Contains(compact, "Agent Overview") || !strings.Contains(compact, "one / agent") {
		t.Fatalf("compact view = %q", compact)
	}
	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not activate selected live card")
	}
	if got, ok := cmd().(NavigateMsg); !ok || got.Workspace.ID != workspace.ID {
		t.Fatalf("activation = %#v", cmd())
	}
}

func TestOverviewRejectsExitedGeneration(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.generation = 2
	m.Stop()
	m.Update(projectMsg{Generation: 2, Result: workspaceinventory.ProjectResult{ProjectKey: "stale"}})
	if len(m.results) != 0 {
		t.Fatal("stale project result applied after Overview exit")
	}
}

func TestNormalizeProjectsPreservesFirstConfiguredIdentity(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	configured := []Project{{Name: "first", Path: real, Index: 0}, {Name: "duplicate", Path: alias, Index: 1}, {Name: "missing", Path: filepath.Join(real, "missing"), Index: 2}}
	m := New(workspaceinventory.Collector{})
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.phase, m.configured = phaseIdentity, len(configured)
	m.identityProjects = make(map[int]Project)
	for i, project := range configured {
		m.identityProjects[i] = normalizeProject(project)
	}
	m.refreshCollector = m.collector.ForRefresh(maxCaptures)
	_ = m.finishPhase()
	if len(m.inventoryOrder) != 2 || m.inventoryOrder[0].Name != "first" || m.inventoryOrder[0].Path != workspaceinventory.CanonicalPath(real) || m.inventoryOrder[1].Name != "missing" {
		t.Fatalf("normalized projects = %#v", m.inventoryOrder)
	}
}

func TestOverviewBoundsProjectFanout(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.generation = 3
	for i := 0; i < 10; i++ {
		path := filepath.Join(t.TempDir(), "repo")
		m.projects = append(m.projects, Project{Name: string(rune('a' + i)), Path: path})
		m.roots = append(m.roots, path)
	}
	m.pending = append([]Project(nil), m.projects...)
	m.completed = make(map[int]bool)
	m.refreshCollector = m.collector.ForRefresh(maxCaptures)
	msg, ok := m.dispatchProjects()().(tea.BatchMsg)
	if !ok || len(msg) != maxProjects || m.active != maxProjects || m.maxActive != maxProjects {
		t.Fatalf("initial fanout msg=%T len=%d active=%d max=%d", msg, len(msg), m.active, m.maxActive)
	}
	first := m.projects[0]
	cmd := m.Update(projectMsg{Generation: 3, Result: workspaceinventory.ProjectResult{ProjectKey: clean(first.Path), ProjectRoot: first.Path}})
	if cmd == nil || m.active != maxProjects {
		t.Fatalf("replacement dispatch active=%d cmd=%v", m.active, cmd)
	}
}

func TestOverviewDispatchesBoundedInventoryWithoutAllProjectMetadataBarrier(t *testing.T) {
	runner := &stageRunner{}
	m := New(workspaceinventory.Collector{Runner: runner})
	projects := make([]Project, 30)
	for i := range projects {
		projects[i] = Project{Name: "project", Path: filepath.Join("/unread-projects", string(rune('a'+i)))}
	}
	msg, ok := m.Start(projects)().(panesMsg)
	if !ok || runner.tmuxCalls.Load() != 1 || runner.gitCalls.Load() != 0 {
		t.Fatalf("initial stage msg=%T tmux=%d git=%d", msg, runner.tmuxCalls.Load(), runner.gitCalls.Load())
	}
	cmd := m.Update(msg)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != maxProjects || runner.gitCalls.Load() != 0 {
		t.Fatalf("first dispatch batch=%T len=%d git=%d", batch, len(batch), runner.gitCalls.Load())
	}
	first := batch[0]().(projectMsg)
	_ = m.Update(first)
	if len(m.identityProjects) != 1 || runner.gitCalls.Load() != 0 || !m.loading {
		t.Fatalf("first bounded identity results=%d git=%d loading=%v", len(m.identityProjects), runner.gitCalls.Load(), m.loading)
	}
}

func TestOverviewCanonicalAliasesRunOneFullInventory(t *testing.T) {
	runner := &stageRunner{}
	m := New(workspaceinventory.Collector{Runner: runner})
	real := t.TempDir()
	projects := []Project{{Name: "first", Path: real}}
	for i := 1; i < 30; i++ {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(real, alias); err != nil {
			t.Fatal(err)
		}
		projects = append(projects, Project{Name: "duplicate", Path: alias})
	}
	queue := []tea.Cmd{m.Start(projects)}
	for len(queue) > 0 && m.loading {
		cmd := queue[0]
		queue = queue[1:]
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if next := m.Update(msg); next != nil && m.loading {
			queue = append(queue, next)
		}
	}
	if m.loading || runner.gitCalls.Load() != 1 || len(m.projects) != 1 || m.projects[0].Name != "first" || m.refreshCollector.Metrics().ProjectOps != 1 {
		t.Fatalf("alias refresh loading=%v git=%d projects=%#v metrics=%#v", m.loading, runner.gitCalls.Load(), m.projects, m.refreshCollector.Metrics())
	}
}

func TestOverviewTmuxFailurePreservesLastGoodButEmptyInventoryDoesNot(t *testing.T) {
	root := t.TempDir()
	key := clean(root)
	old := workspaceinventory.Workspace{ID: "old", ProjectKey: key, ProjectName: "repo", ProjectRoot: root, Name: "agent", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneBlocked, Label: "blocked", Attention: true, Evidence: "permission prompt", Freshness: agentstatus.FreshnessCurrent}}
	m := New(workspaceinventory.Collector{})
	m.generation, m.loading = 4, true
	m.projects = []Project{{Name: "repo", Path: root, Key: key}}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, ProjectRoot: root, Workspaces: []workspaceinventory.Workspace{old}}
	m.roots = []string{root}
	m.Update(panesMsg{Generation: 4, Projects: []Project{{Name: "repo", Path: root, Key: key}}, LiveOnly: true, Err: errors.New("tmux permission denied")})
	newWorkspace := old
	newWorkspace.ID = "new"
	m.Update(projectMsg{Generation: 4, Project: Project{Index: 0}, Phase: phaseStatus, Result: workspaceinventory.ProjectResult{ProjectKey: key, ProjectRoot: root, Workspaces: []workspaceinventory.Workspace{newWorkspace}}})
	if got := m.results[key].Workspaces; len(got) != 1 || got[0].ID != "old" || got[0].Presentation.Freshness != agentstatus.FreshnessStale {
		t.Fatalf("tmux failure replaced last good: %#v", got)
	}
	if view := m.View(150, 24); !strings.Contains(view, "stale") || !strings.Contains(view, "tmux unavailable") {
		t.Fatalf("stale tmux view = %q", view)
	}
	position, ok := m.board.Board().PositionOf("old")
	if !ok || m.board.Board().Lanes[position.Column].ID != kanban.LaneID(agentstatus.LanePaused) || m.results[key].Workspaces[0].Presentation.Evidence != "permission prompt" {
		t.Fatalf("stale blocked projection position=%#v result=%#v", position, m.results[key])
	}

	m.generation, m.loading = 5, true
	m.Update(panesMsg{Generation: 5, Projects: []Project{{Name: "repo", Path: root, Key: key}}, Panes: []workspaceinventory.Pane{}, LiveOnly: true})
	m.Update(projectMsg{Generation: 5, Project: Project{Index: 0}, Phase: phaseStatus, Result: workspaceinventory.ProjectResult{ProjectKey: key, ProjectRoot: root, Workspaces: []workspaceinventory.Workspace{newWorkspace}}})
	if got := m.results[key].Workspaces; len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("successful empty tmux inventory did not replace snapshot: %#v", got)
	}

	firstLoad := New(workspaceinventory.Collector{})
	firstLoad.generation, firstLoad.loading = 1, true
	firstLoad.projects = []Project{{Name: "repo", Path: root, Key: key}}
	firstLoad.roots = []string{root}
	firstLoad.Update(panesMsg{Generation: 1, Projects: []Project{{Name: "repo", Path: root, Key: key}}, LiveOnly: true, Err: errors.New("tmux unavailable")})
	unavailable := newWorkspace
	unavailable.Presentation.Freshness = agentstatus.FreshnessUnavailable
	firstLoad.Update(projectMsg{Generation: 1, Project: Project{Index: 0}, Phase: phaseStatus, Result: workspaceinventory.ProjectResult{ProjectKey: key, ProjectRoot: root, Workspaces: []workspaceinventory.Workspace{unavailable}}})
	if view := firstLoad.View(150, 24); strings.Contains(view, "stale · refresh failed") || !strings.Contains(view, "unavailable") {
		t.Fatalf("first-load tmux failure view = %q", view)
	}
}

func TestOverviewExplicitRefreshImmediatelyProjectsLastGoodAsRefreshing(t *testing.T) {
	root := t.TempDir()
	key := clean(root)
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "repo", Path: root, Key: key}}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, Workspaces: []workspaceinventory.Workspace{{
		ID: "agent", ProjectKey: key, ProjectName: "repo", Name: "agent", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneIdle, Freshness: agentstatus.FreshnessCurrent},
	}}}
	m.syncBoard()
	cmd := m.Start(m.projects)
	if cmd == nil || !strings.Contains(m.View(150, 24), "refreshing") || len(m.completed) != 0 {
		t.Fatalf("immediate refreshing view = %q completed=%v", m.View(150, 24), m.completed)
	}
}

func TestOverviewRefreshPrunesRemovedProjectAndPreservesSelectedCardMovement(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	oneKey, twoKey := clean(one), clean(two)
	selected := workspaceinventory.Workspace{ID: "selected", ProjectKey: oneKey, ProjectName: "one", Name: "agent", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, ChangedAt: time.Now()}}
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "one", Path: one}, {Name: "two", Path: two}}
	m.results[oneKey] = workspaceinventory.ProjectResult{ProjectKey: oneKey, Workspaces: []workspaceinventory.Workspace{selected}}
	m.results[twoKey] = workspaceinventory.ProjectResult{ProjectKey: twoKey}
	m.syncBoard()
	pos, ok := m.board.Board().PositionOf(selected.ID)
	if !ok {
		t.Fatal("selected card missing")
	}
	m.board.Select(kanban.Selection(pos))
	selected.Presentation.Lane = agentstatus.LaneBlocked
	m.results[oneKey] = workspaceinventory.ProjectResult{ProjectKey: oneKey, Workspaces: []workspaceinventory.Workspace{selected}}
	m.syncBoard()
	card, ok := m.board.Board().CardAt(m.board.Selection())
	if !ok || card.ID != selected.ID {
		t.Fatalf("selection after lane move = %#v", card)
	}
	m.generation, m.loading = 8, true
	m.Update(panesMsg{Generation: 8, Projects: []Project{{Name: "one", Path: one}}})
	m.Update(projectMsg{Generation: 8, Project: Project{Name: "one", Path: one, Key: oneKey, Index: 0}, Phase: phaseInventory, Result: workspaceinventory.ProjectResult{ProjectKey: oneKey, ProjectRoot: one}})
	if _, exists := m.results[twoKey]; exists {
		t.Fatal("removed configured project survived refresh")
	}
}

func TestOverviewCancellationStopsPollAndTraceIsPrivacySafe(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	var trace bytes.Buffer
	m.traceWriter = &trace
	first := m.Start([]Project{{Name: "secret-name", Path: "/private/secret-path"}})
	firstContext := m.ctx
	_ = m.Start(nil)
	select {
	case <-firstContext.Done():
	default:
		t.Fatal("new refresh did not cancel prior generation")
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.generation = 10
	poll := m.pollCmd()
	done := make(chan tea.Msg, 1)
	go func() { done <- poll() }()
	m.Stop()
	select {
	case msg := <-done:
		if m.Update(msg) != nil {
			t.Fatal("canceled poll restarted Overview")
		}
	case <-time.After(time.Second):
		t.Fatal("poll command did not drain after Stop")
	}
	if first == nil || strings.Contains(trace.String(), "secret-name") || strings.Contains(trace.String(), "secret-path") || !strings.Contains(trace.String(), "configured=1") || !strings.Contains(trace.String(), "poll_cancel_requested") || !strings.Contains(trace.String(), "poll_drained") {
		t.Fatalf("privacy-safe trace = %q", trace.String())
	}
}

func TestOverviewStopDiscardsGenerationLocalTrackers(t *testing.T) {
	outputs := []string{"• Working (1s • esc to interrupt)", "› Write tests for @filename"}
	collector := workspaceinventory.Collector{Capture: func(string, int) (string, error) {
		output := outputs[0]
		outputs = outputs[1:]
		return output, nil
	}}
	m := New(collector)
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.refreshCollector = m.collector.ForRefresh(1)
	root := t.TempDir()
	previous := workspaceinventory.ProjectResult{ProjectKey: root, ProjectRoot: root, Workspaces: []workspaceinventory.Workspace{{ID: "agent", ProjectKey: root, ProjectRoot: root, Kind: workspaceinventory.KindWorktree, Path: root, Provider: "codex"}}}
	_ = m.refreshCollector.RefreshProjectStatus(m.ctx, previous, []string{root}, []workspaceinventory.Pane{{ID: "%1", Path: root, Command: "codex"}})
	m.Stop()
	if m.refreshCollector.Metrics().TrackerCommits != 0 {
		t.Fatalf("Stop committed tracker state: %#v", m.refreshCollector.Metrics())
	}
	next := m.collector.ForRefresh(1)
	result := next.RefreshProjectStatus(context.Background(), previous, []string{root}, []workspaceinventory.Pane{{ID: "%1", Path: root, Command: "codex"}})
	if got := result.Workspaces[0].Presentation.Lane; got != agentstatus.LaneIdle {
		t.Fatalf("stopped Working contaminated next idle state: %s", got)
	}
}

func TestOverviewAdaptivePollCadence(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	if got := m.pollInterval(); got != idlePollEvery {
		t.Fatalf("empty cadence = %s", got)
	}
	m.results["one"] = workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{{Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking}}}}
	if got := m.pollInterval(); got != livePollEvery {
		t.Fatalf("live cadence = %s", got)
	}
}

func TestOverviewPollReusesFailedInventoryWithoutStatOrGit(t *testing.T) {
	runner := &stageRunner{}
	m := New(workspaceinventory.Collector{Runner: runner})
	root := filepath.Join(t.TempDir(), "still-missing")
	key := workspaceinventory.CanonicalPath(root)
	m.projects = []Project{{Name: "missing", Path: root, Key: key}}
	m.roots = []string{root}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, ProjectName: "missing", ProjectRoot: root, Err: errors.New("configured project missing")}
	panes := m.start(m.projects, "poll")().(panesMsg)
	cmd := m.Update(panes)
	result := cmd().(projectMsg)
	if result.Result.Err == nil || runner.tmuxCalls.Load() != 1 || runner.gitCalls.Load() != 0 || m.refreshCollector.Metrics().ProjectOps != 0 {
		t.Fatalf("failed poll result=%#v tmux=%d git=%d metrics=%#v", result.Result, runner.tmuxCalls.Load(), runner.gitCalls.Load(), m.refreshCollector.Metrics())
	}
	_ = m.Update(result)
}

func TestOverviewExplicitRefreshCancelsAndStartsNewGeneration(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "one", Path: t.TempDir()}}
	_ = m.Start(m.projects)
	oldContext, oldGeneration := m.ctx, m.generation
	cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil || m.generation != oldGeneration+1 {
		t.Fatalf("explicit refresh cmd=%v generation=%d", cmd, m.generation)
	}
	select {
	case <-oldContext.Done():
	default:
		t.Fatal("explicit refresh did not cancel prior generation")
	}
}

func TestOverviewDoubleClickActivatesExactCard(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	workspace := workspaceinventory.Workspace{
		ID:          "two:worktree:agent",
		ProjectKey:  workspaceinventory.CanonicalPath("/tmp/two"),
		ProjectName: "two",
		ProjectRoot: "/tmp/two",
		Kind:        workspaceinventory.KindWorktree,
		Key:         workspaceinventory.CanonicalPath("/tmp/two-agent"),
		Name:        "agent",
		Path:        "/tmp/two-agent",
		Provider:    "codex",
		Presentation: agentstatus.Presentation{
			Lane:      agentstatus.LaneWorking,
			Label:     "working",
			Freshness: agentstatus.FreshnessCurrent,
		},
	}
	m.projects = []Project{{Name: "two", Path: "/tmp/two"}}
	m.results[workspace.ProjectKey] = workspaceinventory.ProjectResult{
		ProjectKey:  workspace.ProjectKey,
		ProjectName: workspace.ProjectName,
		ProjectRoot: workspace.ProjectRoot,
		Workspaces:  []workspaceinventory.Workspace{workspace},
	}
	m.syncBoard()
	m.View(150, 24)
	regions := m.mouse.HitMap.Regions()
	var cardX, cardY int
	found := false
	for _, region := range regions {
		if hit, ok := region.Data.(kanban.HitRegion); ok && hit.CardID == workspace.ID {
			cardX, cardY, found = region.Rect.X, region.Rect.Y, true
			break
		}
	}
	if !found {
		t.Fatalf("card hit region missing: %#v", regions)
	}
	click := tea.MouseClickMsg{X: cardX, Y: cardY, Button: tea.MouseLeft}
	if cmd := m.Update(click); cmd != nil {
		t.Fatal("single click unexpectedly activated card")
	}
	cmd := m.Update(click)
	if cmd == nil {
		t.Fatal("double click did not activate card")
	}
	got, ok := cmd().(NavigateMsg)
	if !ok || got.Workspace.ID != workspace.ID || got.Workspace.Path != workspace.Path {
		t.Fatalf("double-click navigation = %#v", cmd())
	}
}
