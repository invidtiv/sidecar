package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/version"
)

// renderUpdatePhase renders the single update modal for the model's current
// phase — one frame of the real surface.
func renderUpdatePhase(m *Model) string {
	m.ensureUpdateModal()
	return m.updateModal.Render(m.width, m.height, m.updateMouseHandler)
}

// One modal object carries every phase: confirming a plan and settling its
// results must never rebuild the modal, so focus and hit regions survive the
// journey without any re-priming.
func TestUpdateModal_SingleInstanceAcrossPhases(t *testing.T) {
	m := &Model{width: 100, height: 40}
	m.products = []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}

	if !m.openUpdateModal() || m.updateModalState != UpdateModalPreview {
		t.Fatalf("expected the confirmation to open, got %v", m.updateModalState)
	}
	out := renderUpdatePhase(m)
	if !strings.Contains(out, "Update available") {
		t.Errorf("overview phase should carry its own title:\n%s", out)
	}

	built := m.updateModal
	m.handleUpdateModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.updateModalState != UpdateModalProgress {
		t.Fatalf("confirming should start the batch, state is %v", m.updateModalState)
	}
	renderUpdatePhase(m)
	if m.updateModal != built {
		t.Fatal("starting a batch must not rebuild the modal")
	}
	out = renderUpdatePhase(m)
	if !strings.Contains(out, "Updating") || !strings.Contains(out, "td") {
		t.Errorf("installing phase should show progress for the running product:\n%s", out)
	}

	if cmd := m.handleUpdateTargetResult(UpdateTargetResultMsg{PlanID: m.updatePlanID, Index: 0,
		Result: version.Result{Target: m.updatePlan[0], Status: version.StatusUpdated, Version: "1.1.0"}}); cmd != nil {
		t.Fatalf("a settled last target finishes the batch without commands, got %v", cmd)
	}
	if m.updateModalState != UpdateModalComplete {
		t.Fatalf("expected completion, got %v", m.updateModalState)
	}
	renderUpdatePhase(m)
	if m.updateModal != built {
		t.Fatal("settling must not rebuild the modal")
	}
	out = renderUpdatePhase(m)
	if !strings.Contains(out, "Update complete") {
		t.Errorf("done phase should present completion:\n%s", out)
	}
	if m.updateModal.FocusedID() == "" {
		t.Error("the done phase must have a focus list without any priming")
	}
}

// Enter must confirm even before the first paint. openUpdateModal runs on the
// Update side, so the modal already exists in program state; a frame has not
// necessarily rendered when the user's next key arrives.
func TestUpdateModal_EnterBeforeFirstPaintConfirms(t *testing.T) {
	m := &Model{width: 100, height: 40}
	m.products = []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}

	if !m.openUpdateModal() {
		t.Fatal("the confirmation should open")
	}
	// Deliberately no render: this is the keypress that lands in the same
	// event batch as the open.
	m.handleUpdateModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.updateModalState != UpdateModalProgress {
		t.Fatalf("Enter should confirm before any paint, state is %v", m.updateModalState)
	}
	if len(m.updatePlan) != 1 || m.updatePlan[0].Product != version.ProductTd {
		t.Errorf("confirmed plan = %+v", m.updatePlan)
	}
}

// A modal hidden mid-install reopens as Installing; one whose batch settled
// while hidden lands on Done or Failed; an acknowledged outcome yields to a
// fresh confirmation instead of replaying stale results.
func TestUpdateModal_ReopenConvergesToCurrentPhase(t *testing.T) {
	plan := []version.Target{target(version.ProductTasks, "Tasks", "1.5.0", "1.6.0", true)}
	m := modelWithBatch(plan)

	m.closeUpdateModal()
	if !m.updateInProgress {
		t.Fatal("hiding the modal must not settle the batch")
	}
	if !m.openUpdateModal() || m.updateModalState != UpdateModalProgress {
		t.Errorf("reopening mid-batch must converge on Installing, got %v", m.updateModalState)
	}

	// Hide again before the batch settles: a failure that lands while the user
	// is away must be waiting for them when they come back.
	m.closeUpdateModal()
	m.handleUpdateTargetResult(UpdateTargetResultMsg{PlanID: 7, Index: 0,
		Result: version.Result{Target: plan[0], Status: version.StatusFailed, Err: errors.New("brew exited 1")}})
	if m.updateModalState != UpdateModalClosed {
		t.Fatalf("a batch that settles while hidden stays hidden, got %v", m.updateModalState)
	}
	if !m.openUpdateModal() || m.updateModalState != UpdateModalError {
		t.Errorf("unseen failures must reopen onto Failed, got %v", m.updateModalState)
	}
	out := renderUpdatePhase(m)
	if !strings.Contains(out, "Retry") {
		t.Errorf("failed phase must offer Retry:\n%s", out)
	}

	// The failure left nothing pending (the dismissed target was this model's
	// only product), so the acked outcome yields refusal rather than a stale
	// replay of results the user has already seen.
	m.closeUpdateModal()
	if m.openUpdateModal() {
		t.Error("with nothing pending, an acknowledged outcome must not reopen stale results")
	}
}

// Dismissing Done/Failed must never lock the updater: while an update is
// still pending — a failed target keeps its availability — the next entry
// point offers a fresh confirmation instead of refusing.
func TestUpdateModal_AckedYieldsToFreshConfirmation(t *testing.T) {
	m := &Model{width: 100, height: 40}
	failed := target(version.ProductTasks, "Tasks", "1.5.0", "1.6.0", true)
	pending := target(version.ProductTd, "td", "1.0.0", "1.1.0", true)
	m.products = []version.Target{pending, failed}

	// A settled failure the user has not seen yet reopens onto Failed…
	m.updateCarried = []version.Result{{Target: failed, Status: version.StatusFailed, Err: errors.New("brew exited 1")}}
	if !m.openUpdateModal() || m.updateModalState != UpdateModalError {
		t.Fatalf("unseen failure should reopen onto Failed, got %v", m.updateModalState)
	}
	out := renderUpdatePhase(m)
	if !strings.Contains(out, "Retry") {
		t.Errorf("failed phase must offer Retry:\n%s", out)
	}

	// …and once acknowledged, the still-pending updates open a fresh
	// confirmation — the pre-fix behavior refused here permanently.
	m.closeUpdateModal()
	if !m.openUpdateModal() || m.updateModalState != UpdateModalPreview {
		t.Fatalf("acked outcome with a pending update must open a fresh confirmation, got %v", m.updateModalState)
	}
	if out := renderUpdatePhase(m); !strings.Contains(out, "td") {
		t.Errorf("fresh confirmation should show the pending update:\n%s", out)
	}

	// From there the user can still run the failed product again: a failed
	// target keeps its availability, so the fresh plan still includes it.
	m.startUpdateBatch(version.SelectPlan(m.products))
	found := false
	for _, t := range m.updatePlan {
		if t.Product == version.ProductTasks {
			found = true
		}
	}
	if !found {
		t.Errorf("the previously failed product must be re-runnable via the fresh plan: %+v", m.updatePlan)
	}
}

// No entry point may orphan an in-flight batch by starting another over it.
func TestStartUpdateBatch_RefusesConcurrentStart(t *testing.T) {
	plan := []version.Target{
		target(version.ProductTd, "td", "1.0.0", "1.1.0", true),
		target(version.ProductTasks, "Tasks", "1.5.0", "1.6.0", true),
	}
	m := modelWithBatch(plan)

	before := m.updatePlanID
	results := len(m.updateResults)
	if cmd := m.startUpdateBatch(version.SelectPlan(m.products)); cmd != nil {
		t.Error("a concurrent start must schedule nothing")
	}
	if m.updatePlanID != before {
		t.Errorf("plan id moved from %d to %d: the in-flight batch would be orphaned", before, m.updatePlanID)
	}
	if len(m.updateResults) != results || len(m.updatePlan) != len(plan) {
		t.Error("a refused start must leave the running batch untouched")
	}
	if !m.updateInProgress {
		t.Error("the original batch must still be running")
	}
}

// The elapsed clock belongs to the batch, not the overlay: it keeps ticking
// while the modal is hidden, and stops only when the batch settles.
func TestElapsedTick_ContinuesWhileBatchInFlight(t *testing.T) {
	plan := []version.Target{target(version.ProductTasks, "Tasks", "1.5.0", "1.6.0", true)}
	m := modelWithBatch(plan)
	m.updateModalState = UpdateModalClosed

	updated, cmd := m.Update(UpdateElapsedTickMsg{})
	if cmd == nil {
		t.Fatal("the tick must continue while a batch is in flight even with the modal hidden")
	}
	if _, ok := updated.(Model); !ok {
		t.Fatalf("Update returned %T", updated)
	}

	m.updateInProgress = false
	if _, cmd := m.Update(UpdateElapsedTickMsg{}); cmd != nil {
		t.Error("the tick must stop once no batch is in flight")
	}
}
