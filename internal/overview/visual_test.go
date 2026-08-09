package overview

import (
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestRelativeAgeBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		changedAt time.Time
		want      string
	}{
		{"zero", time.Time{}, ""},
		{"just now", now.Add(-2 * time.Second), "now"},
		{"just under now boundary", now.Add(-4999 * time.Millisecond), "now"},
		{"seconds", now.Add(-12 * time.Second), "12s"},
		{"just under a minute", now.Add(-59 * time.Second), "59s"},
		{"minutes", now.Add(-3 * time.Minute), "3m"},
		{"just under an hour", now.Add(-59 * time.Minute), "59m"},
		{"hours", now.Add(-1 * time.Hour), "1h"},
		{"just under a day", now.Add(-23 * time.Hour), "23h"},
		{"days", now.Add(-48 * time.Hour), "2d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativeAge(tc.changedAt, now); got != tc.want {
				t.Fatalf("relativeAge(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestSpineAndKindGlyphDifferByWorkspaceKind(t *testing.T) {
	worktree := workspaceinventory.Workspace{Kind: workspaceinventory.KindWorktree, ProjectKey: "p", ProjectName: "one", Name: "agent", Provider: "codex"}
	shell := workspaceinventory.Workspace{Kind: workspaceinventory.KindShell, ProjectKey: "p", ProjectName: "one", Name: "agent", Provider: "codex", TmuxName: "sess"}

	worktreeLines := cardLines(worktree, false, false, time.Now())
	shellLines := cardLines(shell, false, false, time.Now())

	if worktreeLines[0].Spans[0].Text != "▌" {
		t.Fatalf("worktree spine = %q, want ▌", worktreeLines[0].Spans[0].Text)
	}
	if shellLines[0].Spans[0].Text != "▏" {
		t.Fatalf("shell spine = %q, want ▏", shellLines[0].Spans[0].Text)
	}
	if worktreeLines[0].Spans[2].Text != " ⑂" {
		t.Fatalf("worktree kind glyph = %q, want ' ⑂'", worktreeLines[0].Spans[2].Text)
	}
	if shellLines[0].Spans[2].Text != " ❯" {
		t.Fatalf("shell kind glyph = %q, want ' ❯'", shellLines[0].Spans[2].Text)
	}
	// Every line, not just the first, carries the project-hue spine.
	for i, line := range worktreeLines {
		if line.Spans[0].Text != "▌" {
			t.Fatalf("worktree line %d spine = %q, want ▌", i, line.Spans[0].Text)
		}
	}
	for i, line := range shellLines {
		if line.Spans[0].Text != "▏" {
			t.Fatalf("shell line %d spine = %q, want ▏", i, line.Spans[0].Text)
		}
	}
}

func TestCardLinesRefreshingAndStaleLandOnLineThree(t *testing.T) {
	base := workspaceinventory.Workspace{
		Kind: workspaceinventory.KindWorktree, ProjectKey: "p", ProjectName: "one", Name: "agent", Branch: "main", Provider: "codex",
		Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, Label: "working", Freshness: agentstatus.FreshnessCurrent},
	}
	cases := []struct {
		name             string
		refreshing       bool
		stale            bool
		wantLine3Suffix  string
		wantNotLine3Text string
	}{
		{"refreshing", true, false, "· refreshing…", "· stale"},
		{"stale", false, true, "· stale", "· refreshing…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := cardLines(base, tc.refreshing, tc.stale, time.Now())
			line3 := lines[2].Spans[len(lines[2].Spans)-1].Text
			if !strings.Contains(line3, tc.wantLine3Suffix) {
				t.Fatalf("line 3 = %q, want to contain %q", line3, tc.wantLine3Suffix)
			}
			if strings.Contains(line3, tc.wantNotLine3Text) {
				t.Fatalf("line 3 = %q, unexpectedly contains %q", line3, tc.wantNotLine3Text)
			}
			// Freshness abnormalities never leak onto line 1 or line 2.
			for i, line := range lines[:2] {
				for _, span := range line.Spans {
					if strings.Contains(span.Text, "refreshing") || strings.Contains(span.Text, "stale") {
						t.Fatalf("freshness word leaked onto line %d: %q", i, span.Text)
					}
				}
			}
		})
	}
}

func TestSyncBoardOrdersByProjectGroupThenRecency(t *testing.T) {
	now := time.Now()
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "first", Path: "/tmp/first", Key: "first"}, {Name: "second", Path: "/tmp/second", Key: "second"}}

	older := workspaceinventory.Workspace{ID: "first-old", ProjectKey: "first", ProjectName: "first", Name: "old", Kind: workspaceinventory.KindWorktree, Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, ChangedAt: now.Add(-time.Hour)}}
	newer := workspaceinventory.Workspace{ID: "first-new", ProjectKey: "first", ProjectName: "first", Name: "new", Kind: workspaceinventory.KindWorktree, Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, ChangedAt: now}}
	// second sorts after first regardless of recency, since project order wins.
	secondNewest := workspaceinventory.Workspace{ID: "second-newest", ProjectKey: "second", ProjectName: "second", Name: "newest", Kind: workspaceinventory.KindWorktree, Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking, ChangedAt: now.Add(time.Hour)}}

	m.results["first"] = workspaceinventory.ProjectResult{ProjectKey: "first", Workspaces: []workspaceinventory.Workspace{older, newer}}
	m.results["second"] = workspaceinventory.ProjectResult{ProjectKey: "second", Workspaces: []workspaceinventory.Workspace{secondNewest}}
	m.syncBoard()

	board := m.board.Board()
	var working kanban.Lane
	for _, lane := range board.Lanes {
		if lane.ID == "working" {
			working = lane
		}
	}
	if len(working.Cards) != 3 {
		t.Fatalf("working lane cards = %d, want 3", len(working.Cards))
	}
	got := []string{working.Cards[0].ID, working.Cards[1].ID, working.Cards[2].ID}
	want := []string{"first-new", "first-old", "second-newest"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("card order = %v, want %v", got, want)
		}
	}
}

func TestSyncBoardEmptyLanesCarryNoMessage(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "one", Path: "/tmp/one", Key: "one"}}
	m.results["one"] = workspaceinventory.ProjectResult{ProjectKey: "one", Workspaces: []workspaceinventory.Workspace{
		{ID: "one-a", ProjectKey: "one", ProjectName: "one", Name: "a", Kind: workspaceinventory.KindWorktree, Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking}},
	}}
	m.syncBoard()

	board := m.board.Board()
	for _, lane := range board.Lanes {
		if lane.ID == "working" {
			continue
		}
		if len(lane.Cards) != 0 {
			t.Fatalf("lane %s unexpectedly has cards: %#v", lane.ID, lane.Cards)
		}
		if lane.State != kanban.CellEmpty {
			t.Fatalf("lane %s state = %s, want %s", lane.ID, lane.State, kanban.CellEmpty)
		}
		if lane.Message != "" {
			t.Fatalf("lane %s message = %q, want empty (component supplies the dim ·)", lane.ID, lane.Message)
		}
	}
}

func TestSummaryCountsProjectsAndAgents(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{
		{Name: "one", Path: "/tmp/one", Key: "one"},
		{Name: "two", Path: "/tmp/two", Key: "two"},
	}
	m.results["one"] = workspaceinventory.ProjectResult{ProjectKey: "one", Workspaces: []workspaceinventory.Workspace{
		{ID: "one-a", ProjectKey: "one", ProjectName: "one", Name: "a", Kind: workspaceinventory.KindWorktree, Provider: "claude", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking}},
		{ID: "one-b", ProjectKey: "one", ProjectName: "one", Name: "b", Kind: workspaceinventory.KindShell, Provider: "codex", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneIdle}},
	}}
	m.results["two"] = workspaceinventory.ProjectResult{ProjectKey: "two", Workspaces: []workspaceinventory.Workspace{
		{ID: "two-a", ProjectKey: "two", ProjectName: "two", Name: "a", Kind: workspaceinventory.KindWorktree, Provider: "grok", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneDone}},
	}}
	m.syncBoard()

	if got, want := m.summary(), "2 projects · 3 agents"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}
