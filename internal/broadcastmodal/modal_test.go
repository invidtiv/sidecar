package broadcastmodal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	h := New("demo", t.TempDir(), ScopeThisProject, false)
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
	h := New("demo", t.TempDir(), ScopeThisProject, false)
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
	h := New("demo", t.TempDir(), ScopeThisProject, false)
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
	h := New("", t.TempDir(), ScopeThisProject, false)
	h.ApplyPlan(PlannedMsg{Gen: h.gen, Plan: agentbroadcast.Plan{}})
	view := ansi.Strip(h.Render(80, 24, mouse.NewHandler()))
	if !strings.Contains(view, "Select a workspace so this project has a scope") {
		t.Fatalf("empty this-project scope was silent:\n%s", view)
	}
}

func TestRenderDrawsProjectSectionLabelsWhenGrouped(t *testing.T) {
	h := New("", t.TempDir(), ScopeAllProjects, false)
	h.ApplyPlan(PlannedMsg{Gen: h.gen, Plan: fixturePlan()})
	view := ansi.Strip(h.Render(80, 24, mouse.NewHandler()))
	if !strings.Contains(view, "demo") || !strings.Contains(view, "other") {
		t.Fatalf("grouped modal omitted project sections:\n%s", view)
	}
	if !strings.Contains(view, "tacoma-fable") || !strings.Contains(view, "inventory") {
		t.Fatalf("grouped modal omitted recipient rows:\n%s", view)
	}
}
