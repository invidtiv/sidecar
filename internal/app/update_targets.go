package app

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/version"
)

// UpdateBatchReadyMsg signals that package-manager metadata was refreshed and
// the confirmed batch may start installing.
type UpdateBatchReadyMsg struct {
	PlanID int
}

// UpdateTargetResultMsg carries the settled result of one target in a batch.
type UpdateTargetResultMsg struct {
	PlanID int
	Index  int
	Result version.Result
}

// updateDescriptors returns the products to discover, in display order.
//
// Tasks takes part only when the tasks_plugin feature is effectively enabled,
// so config and CLI overrides behave exactly like plugin assembly. A disabled
// plugin adds no startup process or network work at all.
func updateDescriptors() []version.Descriptor {
	descs := []version.Descriptor{
		version.SidecarDescriptor(),
		version.TdDescriptor(),
	}
	if features.IsEnabled(features.TasksPlugin.Name) {
		descs = append(descs, version.TasksDescriptor())
	}
	return descs
}

// productCheckCmds returns the background release checks for every enabled
// product. force bypasses the per-product cache.
func (m *Model) productCheckCmds(force bool) []tea.Cmd {
	var cmds []tea.Cmd
	for _, d := range updateDescriptors() {
		current := ""
		if d.Product == version.ProductSidecar {
			current = m.currentVersion
		}
		cmds = append(cmds, version.CheckProductAsync(d, current, force))
	}
	return cmds
}

// setProductStatus records a discovered product, replacing any earlier result
// for the same product and keeping the list in display order.
func (m *Model) setProductStatus(msg version.ProductStatusMsg) {
	replaced := false
	for i := range m.products {
		if m.products[i].Product == msg.Target.Product {
			m.products[i] = msg.Target
			replaced = true
			break
		}
	}
	if !replaced {
		m.products = append(m.products, msg.Target)
	}
	m.sortProducts()

	if msg.Target.Product == version.ProductSidecar {
		if msg.ReleaseNotes != "" {
			m.updateNotes = msg.ReleaseNotes
		}
	}
}

func (m *Model) sortProducts() {
	order := map[version.ProductID]int{
		version.ProductSidecar: 0,
		version.ProductTd:      1,
		version.ProductTasks:   2,
	}
	sorted := make([]version.Target, 0, len(m.products))
	for rank := 0; rank < 3; rank++ {
		for _, t := range m.products {
			if order[t.Product] == rank {
				sorted = append(sorted, t)
			}
		}
	}
	m.products = sorted
}

// productTarget returns the discovered target for a product, if any.
func (m *Model) productTarget(id version.ProductID) *version.Target {
	for i := range m.products {
		if m.products[i].Product == id {
			return &m.products[i]
		}
	}
	return nil
}

// hasUpdatesAvailable reports whether any discovered product has a real
// available update.
func (m *Model) hasUpdatesAvailable() bool {
	return len(version.SelectPlan(m.products)) > 0
}

// availableUpdateCount is the number of products a confirmation would change.
func (m *Model) availableUpdateCount() int {
	return len(version.SelectPlan(m.products))
}

// startUpdateBatch confirms a plan and begins running it. The plan is captured
// immutably here; later discovery results cannot change what the user
// confirmed.
func (m *Model) startUpdateBatch(plan []version.Target) tea.Cmd {
	m.updatePlanID++
	m.updatePlan = plan
	m.updateResults = nil
	m.updateActiveIdx = 0
	m.updateError = ""
	m.updateInProgress = true
	m.updateStartTime = time.Now()
	m.updateModalState = UpdateModalProgress
	m.clearUpdateResultModals()

	if len(plan) == 0 {
		m.updateInProgress = false
		m.updateModalState = UpdateModalComplete
		return nil
	}

	planID := m.updatePlanID
	return tea.Batch(
		m.startElapsedTimer(),
		updateSpinnerTick(),
		func() tea.Msg {
			version.RefreshPackageMetadata(context.Background(), version.DefaultEnvironment(), plan)
			return UpdateBatchReadyMsg{PlanID: planID}
		},
	)
}

// startElapsedTimer starts the elapsed time ticker.
func (m *Model) startElapsedTimer() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return UpdateElapsedTickMsg{}
	})
}

// runUpdateTarget installs and verifies one target of the confirmed plan.
// Targets run sequentially to avoid concurrent package-manager locks and
// confusing PATH changes mid-batch.
func (m *Model) runUpdateTarget(planID, idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.updatePlan) {
		return nil
	}
	target := m.updatePlan[idx]
	return func() tea.Msg {
		result := version.Apply(context.Background(), version.DefaultEnvironment(), target)
		return UpdateTargetResultMsg{PlanID: planID, Index: idx, Result: result}
	}
}

// handleUpdateBatchReady starts the first target once metadata is refreshed.
func (m *Model) handleUpdateBatchReady(msg UpdateBatchReadyMsg) tea.Cmd {
	if msg.PlanID != m.updatePlanID || !m.updateInProgress {
		return nil // stale batch
	}
	return m.runUpdateTarget(m.updatePlanID, 0)
}

// handleUpdateTargetResult records a settled target and advances the batch.
// It continues after a failure so a later target still gets its chance, and a
// failure never erases an earlier success.
func (m *Model) handleUpdateTargetResult(msg UpdateTargetResultMsg) tea.Cmd {
	if msg.PlanID != m.updatePlanID || !m.updateInProgress {
		return nil // stale result from an abandoned or superseded batch
	}
	if msg.Index != len(m.updateResults) {
		return nil // out-of-order result; the batch is strictly sequential
	}

	m.updateResults = append(m.updateResults, msg.Result)

	// Reflect a verified upgrade in the discovered product list so diagnostics
	// stop offering it.
	if msg.Result.Status == version.StatusUpdated {
		if t := m.productTarget(msg.Result.Target.Product); t != nil {
			t.CurrentVersion = msg.Result.Version
			t.HasUpdate = false
		}
		m.clearDiagnosticsModal()
	}

	m.updateActiveIdx = len(m.updateResults)
	if m.updateActiveIdx < len(m.updatePlan) {
		return m.runUpdateTarget(m.updatePlanID, m.updateActiveIdx)
	}
	return m.finishUpdateBatch()
}

// finishUpdateBatch settles the batch and picks the completion surface.
func (m *Model) finishUpdateBatch() tea.Cmd {
	m.updateInProgress = false
	m.needsRestart = version.RestartRequired(m.updateResults)
	m.clearUpdateResultModals()

	failed := len(version.RetryTargets(m.updateResults)) > 0
	if failed {
		m.updateError = version.Summarize(m.updateResults)
		m.statusIsError = true
		if m.updateModalState == UpdateModalProgress {
			m.updateModalState = UpdateModalError
		}
	} else if m.updateModalState == UpdateModalProgress {
		m.updateModalState = UpdateModalComplete
	}

	if m.updateModalState == UpdateModalClosed {
		m.ShowToast(version.Summarize(m.updateResults), 10*time.Second)
	}
	return nil
}

// openUpdatePreview moves the user from diagnostics into the update preview.
func (m *Model) openUpdatePreview() {
	m.updateModalState = UpdateModalPreview
	m.showDiagnostics = false
	m.clearUpdatePreviewModal()
}

// updateToastSummary describes the discovered updates in one line, so async
// per-product checks arriving one after another do not overwrite each other
// with partial claims.
func (m *Model) updateToastSummary() string {
	plan := version.SelectPlan(m.products)
	switch len(plan) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("%s %s available! Press ! for details",
			plan[0].DisplayName, plan[0].LatestVersion)
	default:
		return fmt.Sprintf("%d updates available! Press ! for details", len(plan))
	}
}
