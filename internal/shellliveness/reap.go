package shellliveness

import (
	"time"

	"github.com/marcus/sidecar/internal/tmuxserver"
)

// The reap sequence, once, for every surface that has one.
//
// Deciding a shell record is dead and tombstoning it is the single most
// dangerous thing either Workspaces surface does: getting it wrong is not a
// stale row, it is a live user's shell entry deleted (td-8d18de destroyed six
// of them). The verdict rule already lived in this package; what did not was
// the *sequence* around it — which evidence may be acted on at all, which
// records are in scope, and what has to be true again at the moment of the
// write.
//
// That sequence now has two callers: the global Workspaces browser
// (internal/overview/shell_liveness.go) and the headless `sidecar host serve`
// loop (internal/hostserve), which reaps a remote machine's dead shells for a
// viewer that is not sitting at it. Two callers of one sequence, never two
// sequences — the remote-hosts plan's constraint was that reaping arrives
// "only by porting the overview's guards … never fresh logic", and the way to
// honour that is for there to be nothing left to port.
//
// The functions here are state-free in the sense internal/tty's
// DecideGeometryLease is: they hold no surface state, take their evidence as an
// argument, and return a decision rather than a command. A headless caller
// adopts them unchanged.
//
// The three guards, each of which exists because of a real incident:
//
//   - An empty pane listing is never acted on. `tmux kill-server` does not
//     unlink its socket, so a dead server and a live server with no sessions
//     are indistinguishable by socket identity; the collector reports both as
//     zero panes and no error. Acting on that listing suspects every shell at
//     once, which is the shape of td-8d18de. A failed listing is skipped for
//     the same reason: it is not evidence about anything.
//   - Incarnation fencing. A verdict is a statement about the moment it was
//     taken, under one tmux server and one life of a tmux name. Both
//     identities travel with the probe and are checked again at Confirm, so a
//     name that came back — or a server that was replaced — makes the verdict
//     unusable rather than merely late.
//   - Tombstones, never deletion. [ReapShell] calls a ForgetFunc, which in
//     production is workspaceops.ForgetManagedShell: shellstate's flock +
//     read-modify-write path, conditional on the record being unchanged since
//     it was observed. `sidecar shell restore` still works afterwards, on a
//     remote host exactly as locally.
//
// Two more rules survive the move because they are in the code below rather
// than in a caller: a shell in another tmux namespace is invisible to this
// listing so its absence means nothing, and a shell the tracker never saw
// running is left alone — that is what a manifest entry looks like after a
// reboot, and the offline-shell recreate path owns it, not auto-close.

// Shell is one manifest record a surface is asking about.
//
// It is deliberately not workspaceinventory.Workspace. This package has to stay
// importable from the headless serve loop and from both UI surfaces, and these
// five fields are the whole of what the decision needs.
type Shell struct {
	ProjectKey  string
	ProjectRoot string
	TmuxName    string
	Namespace   string
	// CreatedAt identifies the record, not the session. It fences the manifest
	// write: an entry rewritten since this observation is a different shell
	// wearing a reused name, and the conditional writer refuses it.
	CreatedAt time.Time
}

// ReapObservation is everything one refresh cycle knows, handed over whole so
// the guards can be applied here rather than re-derived per surface.
type ReapObservation struct {
	// Server is the tmux server identity this pass observed. It is recorded
	// even when the pass is about to be skipped: a vanished server still
	// changes incarnation, and the tracker's reset must fire on that
	// transition rather than on the next non-empty listing.
	Server tmuxserver.Incarnation

	// Namespace is the tmux namespace this surface can see. An empty namespace
	// means the surface cannot say which server it is looking at, so nothing
	// is judged.
	Namespace string

	// Panes is one entry per pane in this cycle's listing, holding its session
	// name. It is the listing itself and not a deduplicated set of live
	// sessions, because an EMPTY listing is what the first guard triggers on: a
	// caller that filtered blank names out first could turn a listing that
	// existed into one that did not.
	Panes []string

	// ListingFailed reports that the pane listing errored rather than
	// answering. A failed listing is no evidence about anything.
	ListingFailed bool

	// Shells are the shell records in view this cycle. Callers filter their own
	// inventory down to shells before building this; the kind check needs their
	// types, the rest does not.
	Shells []Shell

	// Now is the clock the probe throttle is measured against. Zero means
	// time.Now.
	Now time.Time
}

// ReapProbe names one shell whose absence is worth a second opinion, carrying
// the two identities the verdict will be fenced against.
type ReapProbe struct {
	Shell
	// Incarnation is the life of the tmux name at the moment of suspicion.
	Incarnation uint64
	// Server is the tmux server incarnation the suspicion was formed under.
	// Distinct from Incarnation, which is the life of the name.
	Server tmuxserver.Incarnation
}

// ReapPlan is what this cycle's evidence supports.
type ReapPlan struct {
	// Probes are the shells to take an independent second opinion on. The
	// caller runs them however its surface runs work — a tea.Cmd in the
	// browser, an inline call in the serve loop — and feeds each verdict back
	// through [ConfirmReap].
	Probes []ReapProbe

	// Skipped names the guard that stopped the pass, for tracing. Empty when
	// the pass ran. A surface that silently does nothing is indistinguishable
	// from one that decided nothing was dead, and those are very different
	// facts when a user asks why a row is still there.
	Skipped string
}

// PlanReap records this cycle's liveness and answers which shells to probe.
//
// It mutates the tracker — that is what "record this cycle's liveness" means —
// but holds no state of its own, so the tracker is the caller's and the
// decision is identical on every surface.
func PlanReap(tracker *Tracker, obs ReapObservation) ReapPlan {
	if tracker == nil {
		return ReapPlan{Skipped: "no tracker"}
	}
	// Observe before every early return. Socket-stat is how a surface running
	// outside tmux notices a restart on this pass, and the reset must fire on
	// the transition, not only on the next listing that happens to be usable.
	tracker.ObserveServer(obs.Server)

	switch {
	case obs.ListingFailed:
		return ReapPlan{Skipped: "tmux listing failed"}
	case len(obs.Panes) == 0:
		// A server that is not running and a server with no sessions look
		// identical here. This is the td-8d18de guard; incarnation fencing is
		// the real fence, but a guard that only works because the last line of
		// defence holds is not a guard.
		return ReapPlan{Skipped: "empty pane listing"}
	case obs.Namespace == "":
		return ReapPlan{Skipped: "no tmux namespace"}
	}

	live := make(map[string]bool, len(obs.Panes))
	for _, session := range obs.Panes {
		live[session] = true
	}
	now := obs.Now
	if now.IsZero() {
		now = time.Now()
	}

	var plan ReapPlan
	for _, shell := range obs.Shells {
		if shell.TmuxName == "" || shell.Namespace != obs.Namespace {
			continue
		}
		if live[shell.TmuxName] {
			tracker.Observe(shell.TmuxName)
			continue
		}
		// ShouldProbe carries the "never seen alive" gate as well as the
		// throttle, which is why the reboot case needs no check here.
		if !tracker.ShouldProbe(shell.TmuxName, now) {
			continue
		}
		plan.Probes = append(plan.Probes, ReapProbe{
			Shell:       shell,
			Incarnation: tracker.Incarnation(shell.TmuxName),
			Server:      tracker.Server(),
		})
	}
	return plan
}

// ConfirmReap folds one probe verdict in and reports whether this shell should
// now leave the surface and have its manifest entry tombstoned.
//
// server is the tmux server identity observed at the moment the verdict is
// being applied, which can be a different one from the identity the suspicion
// was formed under; recording it here is what makes the fence in Confirm fire.
func ConfirmReap(tracker *Tracker, server tmuxserver.Incarnation, probe ReapProbe, verdict Verdict) bool {
	if tracker == nil {
		return false
	}
	tracker.ObserveServer(server)
	return tracker.Confirm(probe.TmuxName, verdict, probe.Incarnation, probe.Server)
}

// ForgetFunc tombstones one shell record. workspaceops.ForgetManagedShell is
// the production implementation; its signature is restated here rather than
// imported so this package stays free of the writer's dependencies and both
// callers can substitute it in tests.
type ForgetFunc func(projectRoot, tmuxName, namespace string, observedAt time.Time) error

// ReapShell performs the write half: one fresh probe, then the conditional
// tombstone.
//
// The re-probe is not belt and braces. A verdict is a statement about the
// moment it was taken, and between that moment and this write the user can have
// brought the same tmux name back — pressing Enter on an offline row recreates
// the session under its old name — and deleting the identity of a shell that is
// running right now is the one outcome this feature must never produce. Two
// independent things prevent it: [ConfirmReap]'s incarnation fence, and this
// probe, which makes the evidence fresh at the instant of the deletion.
//
// resurrected reports that the session was back and no write was attempted.
func ReapShell(probe ProbeFunc, forget ForgetFunc, target ReapProbe) (resurrected bool, err error) {
	if probe == nil || forget == nil {
		return false, nil
	}
	if probe(target.TmuxName) != Gone {
		return true, nil
	}
	return false, forget(target.ProjectRoot, target.TmuxName, target.Namespace, target.CreatedAt)
}
