package broadcastmodal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentbroadcast"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/notify"
)

func fixturePlan() agentbroadcast.Plan {
	return agentbroadcast.Plan{
		Scope:              agentbroadcast.Scope{Kind: agentbroadcast.ScopeProject, Project: "demo"},
		FromUser:           true,
		ShellsWithoutAgent: 2,
		Recipients: []agentbroadcast.Recipient{
			{
				Target:  agentcontrol.Target{Host: "local", Project: "demo", Session: "s1", Name: "tacoma-fable"},
				Agent:   agentcontrol.AgentState{Kind: "claude", Status: agentcontrol.StatusWorking, Freshness: "current"},
				Outcome: agentbroadcast.OutcomeWouldSend,
			},
			{
				Target:  agentcontrol.Target{Host: "local", Project: "demo", Session: "s2", Name: "inventory"},
				Agent:   agentcontrol.AgentState{Kind: "muse", Status: agentcontrol.StatusUnknown, Freshness: "stale"},
				Outcome: agentbroadcast.OutcomeSkipped,
				Reason:  &agentbroadcast.Reason{Code: string(agentcontrol.ErrNotReady), Message: "agent status is stale, not current; nothing is sent to a target Sidecar cannot vouch for"},
			},
			{
				Target:  agentcontrol.Target{Host: "other", Project: "other", Session: "s3", Name: "Shell 19"},
				Agent:   agentcontrol.AgentState{Kind: "claude", Status: agentcontrol.StatusIdle, Freshness: "current"},
				Outcome: agentbroadcast.OutcomeWouldSend,
			},
		},
	}
}

func TestChecklistAgreesWithDryRunJSONRowForRow(t *testing.T) {
	plan := fixturePlan()
	lines := recipientLines(Checklist(plan, false, nil))

	dryRun := agentbroadcast.Result{Scope: plan.Scope, Recipients: plan.Recipients, Summary: agentbroadcast.Summary{ShellsWithoutAgent: plan.ShellsWithoutAgent}}
	data, err := json.Marshal(dryRun)
	if err != nil {
		t.Fatal(err)
	}
	var decoded agentbroadcast.Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(lines) != len(decoded.Recipients) {
		t.Fatalf("checklist %d rows, dry-run JSON %d", len(lines), len(decoded.Recipients))
	}
	for i, line := range lines {
		rec := decoded.Recipients[i]
		if line.Name != recipientName(rec) {
			t.Fatalf("row %d name = %q, json %q", i, line.Name, recipientName(rec))
		}
		if line.Agent != string(rec.Agent.Kind) {
			t.Fatalf("row %d kind = %q, json %q", i, line.Agent, rec.Agent.Kind)
		}
		if line.Status != string(rec.Agent.Status) {
			t.Fatalf("row %d status = %q, json %q", i, line.Status, rec.Agent.Status)
		}
		wantChecked := rec.Outcome == agentbroadcast.OutcomeWouldSend
		if line.Checked != wantChecked {
			t.Fatalf("row %d checked = %v, outcome %s", i, line.Checked, rec.Outcome)
		}
		if rec.Outcome == agentbroadcast.OutcomeSkipped && line.Reason == "" {
			t.Fatalf("row %d skipped without a reason: %+v", i, rec)
		}
	}
}

func TestChecklistGroupsByProjectOnlyWhenAsked(t *testing.T) {
	plan := fixturePlan()
	ungrouped := Checklist(plan, false, nil)
	for _, line := range ungrouped {
		if line.Kind == lineSection {
			t.Fatalf("this-project grouping leaked a section: %+v", line)
		}
	}
	grouped := Checklist(plan, true, nil)
	var sections int
	for _, line := range grouped {
		if line.Kind == lineSection {
			sections++
		}
	}
	if sections != 2 {
		t.Fatalf("all-projects sections = %d, want 2", sections)
	}
	var sawCount bool
	for _, line := range grouped {
		if line.Kind == lineCount {
			sawCount = true
			if line.Label != "2 shells have no live agent and are not listed" {
				t.Fatalf("count line = %q", line.Label)
			}
		}
	}
	if !sawCount {
		t.Fatal("missing shells-without-agent count line")
	}
}

func TestWithRequestedPromotesSkippedAndDropsUnchecked(t *testing.T) {
	plan := fixturePlan()
	keep := map[string]bool{
		agentbroadcast.RecipientID(plan.Recipients[0]): false, // was would_send
		agentbroadcast.RecipientID(plan.Recipients[1]): true,  // was skipped
		agentbroadcast.RecipientID(plan.Recipients[2]): true,
	}
	got := plan.WithRequested(func(row agentbroadcast.Recipient) bool {
		return keep[agentbroadcast.RecipientID(row)]
	})
	if got.Recipients[0].Outcome != agentbroadcast.OutcomeSkipped || got.Recipients[0].Reason == nil || got.Recipients[0].Reason.Code != "excluded" {
		t.Fatalf("unchecked would_send = %+v", got.Recipients[0])
	}
	if got.Recipients[1].Outcome != agentbroadcast.OutcomeWouldSend || got.Recipients[1].Reason != nil {
		t.Fatalf("checked skipped = %+v", got.Recipients[1])
	}
	if got.Recipients[2].Outcome != agentbroadcast.OutcomeWouldSend {
		t.Fatalf("checked would_send = %+v", got.Recipients[2])
	}
}

func TestNotificationsRegisterBroadcastSource(t *testing.T) {
	plan := fixturePlan()
	result := agentbroadcast.Result{
		Scope:      plan.Scope,
		Recipients: plan.Recipients,
		Summary:    agentbroadcast.Summary{Submitted: 1, Skipped: 2, ShellsWithoutAgent: 2},
	}
	notes := Notifications(result)
	if len(notes) != 1+len(plan.Recipients) {
		t.Fatalf("notifications = %d, want 1+%d", len(notes), len(plan.Recipients))
	}
	if notes[0].Source != notify.SourceBroadcast {
		t.Fatalf("summary source = %s", notes[0].Source)
	}
	if notes[0].Title != "Broadcast sent to 1 agents (2 skipped)" {
		t.Fatalf("toast = %q", notes[0].Title)
	}
	if notes[0].ExpiresAt == nil {
		t.Fatal("summary toast should expire so it does not stick as a toast")
	}
	for i, n := range notes[1:] {
		if n.Source != notify.SourceBroadcast {
			t.Fatalf("row %d source = %s", i, n.Source)
		}
		if n.ExpiresAt != nil {
			t.Fatalf("per-target row %d should stay sticky in the centre", i)
		}
	}
}

func TestHostInitialSelectionFollowsPlan(t *testing.T) {
	h := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	h.ApplyPlan(PlannedMsg{Gen: h.gen, Plan: fixturePlan()})
	lines := recipientLines(h.lines())
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	if !lines[0].Checked || lines[1].Checked || !lines[2].Checked {
		t.Fatalf("initial checks = %+v", lines)
	}
}

func TestReplanUnblocksWhenPlanHangs(t *testing.T) {
	prev := planTimeout
	planTimeout = 40 * time.Millisecond
	t.Cleanup(func() { planTimeout = prev })

	release := make(chan struct{})
	h := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	h.Service = agentbroadcast.Service{
		Candidates: func(context.Context, agentbroadcast.PlanRequest) ([]managedtarget.Target, error) {
			<-release
			return nil, nil
		},
	}
	start := time.Now()
	msg := h.Replan()()
	if time.Since(start) > 300*time.Millisecond {
		t.Fatal("Replan waited on a hung Plan")
	}
	close(release)
	planned, ok := msg.(PlannedMsg)
	if !ok || planned.Err == nil || !strings.Contains(planned.Err.Error(), "timed out") {
		t.Fatalf("msg = %#v", msg)
	}
}

func TestRenderEmptyPlanExplainsWhy(t *testing.T) {
	h := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	h.ApplyPlan(PlannedMsg{Gen: h.gen, Plan: agentbroadcast.Plan{ShellsWithoutAgent: 31}})
	view := ansi.Strip(h.Render(80, 24, mouse.NewHandler()))
	if !strings.Contains(view, "No live agents to send to.") {
		t.Fatalf("empty plan did not say why:\n%s", view)
	}
	if !strings.Contains(view, "31 shells have no live agent and are not listed") {
		t.Fatalf("empty plan omitted the shells-without-agent count:\n%s", view)
	}
}

func TestRenderThisProjectWithoutKeyExplainsScope(t *testing.T) {
	h := New(SurfaceProject, "", t.TempDir(), ScopeThisProject, false)
	h.ApplyPlan(PlannedMsg{Gen: h.gen, Plan: agentbroadcast.Plan{}})
	view := ansi.Strip(h.Render(80, 24, mouse.NewHandler()))
	if !strings.Contains(view, "Select a workspace so this project has a scope") {
		t.Fatalf("empty this-project scope was silent:\n%s", view)
	}
}

func TestRenderDrawsProjectSectionLabelsWhenGrouped(t *testing.T) {
	h := New(SurfaceProject, "", t.TempDir(), ScopeAllProjects, false)
	h.ApplyPlan(PlannedMsg{Gen: h.gen, Plan: fixturePlan()})
	view := ansi.Strip(h.Render(80, 24, mouse.NewHandler()))
	if !strings.Contains(view, "demo") || !strings.Contains(view, "other") {
		t.Fatalf("grouped modal omitted project sections:\n%s", view)
	}
	if !strings.Contains(view, "tacoma-fable") || !strings.Contains(view, "inventory") {
		t.Fatalf("grouped modal omitted recipient rows:\n%s", view)
	}
}

// longPlan is n recipients whose names are longer than the fixed-width column
// the checklist used to have, in one project.
func longPlan(n int) agentbroadcast.Plan {
	plan := agentbroadcast.Plan{Scope: agentbroadcast.Scope{Kind: agentbroadcast.ScopeProject, Project: "demo"}}
	for i := range n {
		plan.Recipients = append(plan.Recipients, agentbroadcast.Recipient{
			Target:  agentcontrol.Target{Host: "local", Project: "demo", Session: fmt.Sprintf("s%02d", i), Name: fmt.Sprintf("refactor the notification centre %02d", i)},
			Agent:   agentcontrol.AgentState{Kind: "claude", Status: agentcontrol.StatusIdle, Freshness: "current"},
			Outcome: agentbroadcast.OutcomeWouldSend,
		})
	}
	return plan
}

func TestRowsUseTheWidthTheModalHasBeforeTruncating(t *testing.T) {
	h := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	h.ApplyPlan(PlannedMsg{Host: h, Gen: h.gen, Plan: longPlan(2)})
	view := ansi.Strip(h.Render(120, 30, mouse.NewHandler()))
	if !strings.Contains(view, "refactor the notification centre 00") {
		t.Fatalf("a name the modal had room for was truncated:\n%s", view)
	}
}

func TestRowsTruncateOnlyWhenTheModalRunsOut(t *testing.T) {
	h := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	h.ApplyPlan(PlannedMsg{Host: h, Gen: h.gen, Plan: longPlan(2)})
	view := ansi.Strip(h.Render(44, 30, mouse.NewHandler()))
	if strings.Contains(view, "refactor the notification centre 00") {
		t.Fatalf("a name wider than the modal was not truncated:\n%s", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("truncation left no ellipsis:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > 44 {
			t.Fatalf("row overflowed the modal frame (%d cols):\n%s", ansi.StringWidth(line), line)
		}
	}
}

// The cursor and the pointer colour a row where it sits. Padding them like a
// button shifted the whole row two columns right under the pointer, so the
// list appeared to jump as the mouse moved down it.
func TestCursorAndHoverDoNotIndentTheRow(t *testing.T) {
	h := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	h.ApplyPlan(PlannedMsg{Host: h, Gen: h.gen, Plan: longPlan(3)})
	rows := recipientLines(h.lines())
	cols := columnWidths(rows, 60)
	plain := ansi.Strip(h.renderRow(rows[0], cols, 60, false, false))
	cursor := ansi.Strip(h.renderRow(rows[0], cols, 60, true, false))
	hover := ansi.Strip(h.renderRow(rows[0], cols, 60, false, true))
	if cursor != plain || hover != plain {
		t.Fatalf("pointer state changed the row's text:\nplain  %q\ncursor %q\nhover  %q", plain, cursor, hover)
	}
	if !strings.HasPrefix(plain, "[") {
		t.Fatalf("row does not start at the checkbox: %q", plain)
	}
}

func TestLongRecipientListScrolls(t *testing.T) {
	h := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	h.ApplyPlan(PlannedMsg{Host: h, Gen: h.gen, Plan: longPlan(maxList + 6)})
	view := ansi.Strip(h.Render(120, 40, mouse.NewHandler()))
	if strings.Contains(view, "centre 09") {
		t.Fatalf("a row past the window was drawn:\n%s", view)
	}
	if !h.canScrollList(1) {
		t.Fatal("a list longer than the window reports it cannot scroll")
	}
	h.scrollList(3)
	if h.listOffset != 3 {
		t.Fatalf("listOffset = %d, want 3", h.listOffset)
	}
	view = ansi.Strip(h.Render(120, 40, mouse.NewHandler()))
	if !strings.Contains(view, "centre 09") {
		t.Fatalf("scrolling did not reach a later row:\n%s", view)
	}
	if strings.Contains(view, "centre 00") {
		t.Fatalf("scrolling left the first row on screen:\n%s", view)
	}
	// The bar is drawn beside the rows, and stops at both ends.
	h.scrollList(99)
	if h.listOffset != maxList+6-maxList {
		t.Fatalf("listOffset past the end = %d", h.listOffset)
	}
	if h.canScrollList(1) {
		t.Fatal("the bottom of the list still reports room below")
	}
}

// A plan addressed to another host is another surface's answer. Both
// Workspaces surfaces receive the message, and a modal left open on the one
// the user is not looking at used to adopt it (td-cd1706).
func TestApplyPlanIgnoresAnotherHostsPlan(t *testing.T) {
	mine := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	theirs := New(SurfaceProject, "other", t.TempDir(), ScopeAllProjects, false)
	mine.gen, theirs.gen = 1, 1
	mine.ApplyPlan(PlannedMsg{Host: theirs, Gen: 1, Plan: fixturePlan()})
	if len(mine.plan.Recipients) != 0 {
		t.Fatalf("host adopted another host's plan: %+v", mine.plan.Recipients)
	}
	mine.ApplyPlan(PlannedMsg{Host: mine, Gen: 1, Plan: fixturePlan()})
	if len(mine.plan.Recipients) != 3 {
		t.Fatalf("host ignored its own plan: %+v", mine.plan.Recipients)
	}
}

func TestDeselectedRowsAreNotReported(t *testing.T) {
	plan := fixturePlan()
	requested := plan.WithRequested(func(row agentbroadcast.Recipient) bool { return false })
	result := agentbroadcast.Result{
		Scope:      requested.Scope,
		Recipients: requested.Recipients,
		Summary:    agentbroadcast.Summary{Skipped: 3},
	}
	notes := Notifications(result)
	if len(notes) != 2 {
		// One toast plus the one row that was skipped for a reason of its own.
		t.Fatalf("notifications = %d, want the toast and the genuinely skipped row: %+v", len(notes), notes)
	}
	if notes[0].Title != "Broadcast sent to 0 agents (1 skipped)" {
		t.Fatalf("toast counted deselections as skips: %q", notes[0].Title)
	}
	for _, n := range notes[1:] {
		if strings.Contains(n.Body, agentbroadcast.ReasonExcluded) {
			t.Fatalf("a deselected recipient was reported: %+v", n)
		}
	}
}

// Clicking a scope segment has to replan, exactly as the arrow keys do. It did
// not, so "all projects" in the project workspace relabelled the this-project
// answer instead of widening it (td-cd1706).
func TestClickingAllProjectsReplans(t *testing.T) {
	h := New(SurfaceProject, "demo", t.TempDir(), ScopeThisProject, false)
	planned := make(chan agentbroadcast.PlanRequest, 4)
	h.Service = agentbroadcast.Service{
		Candidates: func(_ context.Context, req agentbroadcast.PlanRequest) ([]managedtarget.Target, error) {
			planned <- req
			return nil, nil
		},
	}
	handler := mouse.NewHandler()
	h.Render(120, 40, handler)

	var region *mouse.Region
	for _, r := range handler.HitMap.Regions() {
		if r.ID == "all-projects" {
			r := r
			region = &r
			break
		}
	}
	if region == nil {
		t.Fatal("no hit region for the all-projects segment")
	}
	close, cmd := h.HandleMouse(tea.MouseClickMsg{X: region.Rect.X + 1, Y: region.Rect.Y, Button: tea.MouseLeft}, handler)
	if close {
		t.Fatal("a scope click closed the modal")
	}
	if h.scopeIdx != int(ScopeAllProjects) {
		t.Fatalf("scope after the click = %d", h.scopeIdx)
	}
	if cmd == nil {
		t.Fatal("a scope click returned no replan command")
	}
	cmd()
	select {
	case req := <-planned:
		if req.ScopeKind != agentbroadcast.ScopeAll {
			t.Fatalf("replan asked for %+v, want the all-projects scope", req)
		}
	default:
		t.Fatal("the replan never reached the candidate source")
	}
}
