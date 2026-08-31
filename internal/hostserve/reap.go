package hostserve

import (
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Serve's reap: a remote host that forgets its dead shells.
//
// Until this landed, a remote shell that exited went dead within one poll —
// liveness flips as soon as its pane leaves the listing — and then stayed on
// the viewer's board forever, because nothing on the host removed the record.
// The only thing that ever did was the human's own Sidecar on that machine, if
// one happened to be running. A remote host the user is not sitting at is
// precisely the case where "someone else's Sidecar will tidy up" is not true.
//
// The whole of the decision is shellliveness.PlanReap / ConfirmReap /
// ReapShell, which is also what internal/overview/shell_liveness.go calls.
// There is one implementation of the sequence and this file is not it: what is
// here is the binding — turning serve's cycle into an observation, running the
// probes inline because this loop has no command runtime, and writing the
// tombstone through the same guarded writer the browser uses.
//
// Reaching a state-tree writer from this package is a real change to what serve
// is, and the package doc says so. What has not changed is the tmux call graph:
// a reap adds `tmux list-sessions` and nothing else, and TestServeIsReadOnly
// still holds.
//
// Concurrency is the already-normal multi-instance case. Two viewers each spawn
// their own serve, and the host's own Sidecar may be running beside them; all
// of them write through shellstate's flock plus read-modify-write, conditional
// on the record being unchanged since it was observed. The loser of a race
// finds the record already tombstoned and reports KindNotFound, which is traced
// and otherwise ignored.

// reapPass runs one cycle's reap and returns anything the viewer should be told
// about. Errors are reported, never fatal: a host that cannot tidy its manifest
// is still a host worth watching.
func reapPass(
	tracker *shellliveness.Tracker,
	opts Options,
	namespace string,
	incarnation tmuxserver.Incarnation,
	panes []workspaceinventory.Pane,
	paneErr error,
	results []workspaceinventory.ProjectResult,
) []hostproto.Error {
	obs := shellliveness.ReapObservation{
		Server:        incarnation,
		Namespace:     namespace,
		ListingFailed: paneErr != nil,
		Now:           opts.Now(),
	}
	// One entry per pane, blank session names included: the empty-listing guard
	// is about whether tmux listed anything at all, so filtering here would
	// change what it sees.
	obs.Panes = make([]string, 0, len(panes))
	for _, pane := range panes {
		obs.Panes = append(obs.Panes, pane.Session)
	}
	for _, result := range results {
		if result.Err != nil {
			continue
		}
		for _, workspace := range result.Workspaces {
			if workspace.Kind != workspaceinventory.KindShell {
				continue
			}
			obs.Shells = append(obs.Shells, shellliveness.Shell{
				ProjectKey:  result.ProjectKey,
				ProjectRoot: result.ProjectRoot,
				TmuxName:    workspace.TmuxName,
				Namespace:   workspace.Namespace,
				CreatedAt:   workspace.CreatedAt,
			})
		}
	}

	plan := shellliveness.PlanReap(tracker, obs)
	if plan.Skipped != "" || len(plan.Probes) == 0 {
		return nil
	}

	var errs []hostproto.Error
	for _, probe := range plan.Probes {
		// Inline rather than fanned out. A probe is one bounded `list-sessions`
		// against a healthy local server, the throttle in ShouldProbe means one
		// per suspected shell per 15s, and the alternative — a goroutine per
		// probe — would put concurrent writes into a loop whose whole
		// single-flight discipline is that it has none.
		verdict := opts.ProbeShell(probe.TmuxName)
		// The incarnation is re-read here, not reused from the top of the
		// cycle: the server can have been replaced while the probe ran, and
		// that is exactly what the fence is for.
		if !shellliveness.ConfirmReap(tracker, opts.ServerIncarnation(), probe, verdict) {
			continue
		}
		resurrected, err := shellliveness.ReapShell(opts.ProbeShell, opts.ForgetShell, probe, opts.ServerIncarnation())
		if resurrected || err == nil {
			continue
		}
		errs = append(errs, hostproto.Error{
			Code:    hostproto.ErrInternal,
			Message: "could not forget dead shell " + probe.TmuxName + " on " + opts.HostID + ": " + err.Error(),
		})
	}
	return errs
}
