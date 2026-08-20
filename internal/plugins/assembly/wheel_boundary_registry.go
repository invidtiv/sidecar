package assembly

import (
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/conversations"
	"github.com/marcus/sidecar/internal/plugins/filebrowser"
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
	"github.com/marcus/sidecar/internal/plugins/notes"
	"github.com/marcus/sidecar/internal/plugins/tasks"
	"github.com/marcus/sidecar/internal/plugins/tdmonitor"
	"github.com/marcus/sidecar/internal/plugins/workspace"
)

// WheelBoundaryPolicy classifies how a wheel-handling surface answers the
// pre-update boundary question asked by app.FilterInput:
//
//	"Would this exact wheel event change the surface under the pointer?"
//
// See docs/plans/active/scroll-inertia-complete-coverage.md.
type WheelBoundaryPolicy string

const (
	// WheelCovered means the surface implements plugin.WheelBoundaryConsumer
	// and answers exactly for every region it owns, including its modals.
	WheelCovered WheelBoundaryPolicy = "covered"

	// WheelExternallyOwned means the scroll state belongs to an embedded model
	// from another repository. Sidecar must not approximate its bounds, so the
	// surface stays unfiltered until that upstream exposes a read-only boundary
	// contract (plan Phase 4).
	WheelExternallyOwned WheelBoundaryPolicy = "externally-owned"

	// WheelDeprecatedExclusion means the surface is deliberately out of scope: a
	// deprecated, default-off plugin that is expected to be removed rather than
	// covered. Recorded by name so it is an audited decision, not an oversight.
	WheelDeprecatedExclusion WheelBoundaryPolicy = "deprecated-exclusion"
)

// WheelBoundarySurface is one declared row of the coverage ledger.
type WheelBoundarySurface struct {
	// ID is the plugin ID for project plugins, or a descriptive identifier for
	// a host-owned global surface.
	ID string

	// Surface names the wheel-handling surface in human terms.
	Surface string

	// Policy is the declared boundary policy.
	Policy WheelBoundaryPolicy

	// Probe is a typed nil plugin pointer used for the compile/runtime check
	// that the declared policy matches reality. Nil for surfaces that are not
	// plugins (globals owned by the app shell).
	Probe plugin.Plugin

	// HostsModals records whether the surface owns declarative modals whose
	// wheel input it must answer for as well as its panes.
	HostsModals bool

	// Note explains the policy, especially what evidence would change it.
	Note string
}

// WheelBoundaryRegistry is the explicit, auditable list of every Sidecar
// surface that receives wheel input, with its declared boundary policy.
//
// A new wheel-handling plugin or modal host must be added here; the tests in
// wheel_boundary_registry_test.go fail when a registered plugin has no row,
// when a "covered" row does not implement plugin.WheelBoundaryConsumer, or when
// an excluded row silently starts implementing it without being reclassified.
//
// App-level overlays are not listed here: each tea ModalKind has its own row in
// internal/app/modal_wheel_boundary_test.go, enforced by
// TestEveryModalKindHasALedgerRow.
var WheelBoundaryRegistry = []WheelBoundarySurface{
	{
		ID:          IDWorkspace,
		Surface:     "Workspaces sidebar, docs/issues, diffs, commit files, terminal scrollback, plugin modals",
		Policy:      WheelCovered,
		Probe:       (*workspace.Plugin)(nil),
		HostsModals: true,
		Note:        "Panes answer exactly; tmux panes with mouse reporting and unexhausted scrollback stay unknown by design.",
	},
	{
		ID:          IDGitStatus,
		Surface:     "Git status tree, diff pane, commit preview, full diff, plugin modals",
		Policy:      WheelCovered,
		Probe:       (*gitstatus.Plugin)(nil),
		HostsModals: true,
	},
	{
		ID:          IDFileBrowser,
		Surface:     "File tree, file preview, plugin modals",
		Policy:      WheelCovered,
		Probe:       (*filebrowser.Plugin)(nil),
		HostsModals: true,
		Note:        "Inline editor overlays forward: the embedded tmux editor owns that wheel stream.",
	},
	{
		ID:          IDNotes,
		Surface:     "Notes list, markdown preview, plugin modals",
		Policy:      WheelCovered,
		Probe:       (*notes.Plugin)(nil),
		HostsModals: true,
		Note:        "Textarea edit mode and the inline tmux editor stay unknown until their owners expose exact bounds.",
	},
	{
		ID:          "global-workspaces",
		Surface:     "Global Workspaces overview: sidebar, doc/issue tabs, diff/task tabs, terminal preview, activity board",
		Policy:      WheelCovered,
		Probe:       nil, // owned by internal/overview via the app shell, not a plugin
		HostsModals: false,
		Note:        "Answered by overview.WorkspacesWheelAtBoundary and BoardWheelAtBoundary; rename/view flyouts forward.",
	},
	{
		ID:          "notification-centre",
		Surface:     "App-shell notification centre panel",
		Policy:      WheelCovered,
		Probe:       nil, // owned by internal/app, not a plugin
		HostsModals: false,
		Note:        "Answered by app.notificationCentreWheelAtBoundary from FilterInput before the plugin underneath.",
	},
	{
		ID:          IDTDMonitor,
		Surface:     "Embedded td monitor panels, detail scroll, and td-owned modals",
		Policy:      WheelExternallyOwned,
		Probe:       (*tdmonitor.Plugin)(nil),
		HostsModals: true,
		Note:        "Awaiting the upstream td read-only boundary contract (plan Phase 4). Sidecar must not inspect td's private state; the Sidecar setup modal is on Sidecar's modal contract.",
	},
	{
		ID:          "tasks-project",
		Surface:     "Project Tasks plugin, embedded Tasks model",
		Policy:      WheelExternallyOwned,
		Probe:       (*tasks.Plugin)(nil),
		HostsModals: true,
		Note:        "Awaiting the upstream Tasks embedding boundary contract (plan Phase 4).",
	},
	{
		ID:          "tasks-global",
		Surface:     "Global Tasks tab, same Tasks plugin instance selected through globalTasksPlugin()",
		Policy:      WheelExternallyOwned,
		Probe:       (*tasks.Plugin)(nil),
		HostsModals: true,
		Note:        "app.FilterInput takes a distinct global branch; it already delegates whenever the Tasks plugin implements the contract.",
	},
	{
		ID:          IDConversations,
		Surface:     "Conversations sidebar, turn/message panes, detail view, resume-session modal",
		Policy:      WheelDeprecatedExclusion,
		Probe:       (*conversations.Plugin)(nil),
		HostsModals: true,
		Note:        "Deprecated, default-off, expected to be removed. Named exclusion, not an oversight: if it is undeprecated, update the plan inventory first, then cover its cursor sidebar, detailScroll bounds, hasMoreSessions lazy exception, and resume modal.",
	},
}

// WheelBoundaryPolicyFor returns the declared policy for an ID.
func WheelBoundaryPolicyFor(id string) (WheelBoundarySurface, bool) {
	for _, s := range WheelBoundaryRegistry {
		if s.ID == id {
			return s, true
		}
	}
	return WheelBoundarySurface{}, false
}
