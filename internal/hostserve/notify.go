package hostserve

import (
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Notification forwarding is the second consumer of notify.LaneTracker; the
// first is internal/plugins/workspace/agent_triggers.go, and the rules they
// share live in the tracker so the two cannot drift. What this file adds is
// the same adapter shape that file adds locally: observations in, wire events
// out.
//
// Three properties are load-bearing, and each of them is a failure this
// package must not reproduce.
//
//   - Only a settled transition produces an event. The tracker's first sight
//     of a workspace is a baseline, never a notification, so a serve process
//     that starts next to an already-blocked agent says nothing. That is also
//     what makes a reconnect silent: a viewer's reconnect is a NEW serve
//     process, whose tracker starts empty.
//   - Nothing here reads a snapshot, a generation, or a sequence number. A
//     notification derived from state would be replayed every time state was
//     re-sent, which is precisely how a stale prompt would arrive on someone's
//     desktop an hour after it was answered.
//   - The host does not deliver, claim, or record a receipt. It says what
//     happened; the viewer decides whether its user needs to hear about it.
type notifier struct {
	tracker notify.LaneTracker
	// keys maps the tracker's own notification ID to the wire key that was
	// sent for it, so leaving the blocked lane can withdraw the right event.
	// The viewer never sees a tracker ID: it derives its local record ID from
	// the wire key, and a withdrawal has to name that same key.
	keys map[string]string
}

func newNotifier(debounce time.Duration) *notifier {
	return &notifier{tracker: notify.LaneTracker{Debounce: debounce}, keys: make(map[string]string)}
}

// observe folds one complete cycle's observations into the tracker and returns
// the messages to send. The set must be complete: a workspace missing from it
// is how the tracker learns a shell is gone.
func (n *notifier) observe(obs []notify.LaneObservation, now time.Time) []hostproto.NotifyEvent {
	events := n.tracker.Observe(obs, now)
	if events.Empty() {
		return nil
	}
	out := make([]hostproto.NotifyEvent, 0, len(events.Post)+len(events.Dismiss))
	for _, posted := range events.Post {
		event, ok := notifyEvent(posted)
		if !ok {
			continue
		}
		n.keys[posted.ID] = event.Key
		out = append(out, event)
	}
	for _, id := range events.Dismiss {
		key, ok := n.keys[id]
		if !ok {
			// A wait this process never announced — it belongs to a tracker
			// state seeded before the connection existed, or to a post the
			// payload rules refused. Withdrawing a key the viewer does not
			// have would be a message about nothing.
			continue
		}
		delete(n.keys, id)
		out = append(out, hostproto.NotifyEvent{Withdraws: key})
	}
	return out
}

// notifyEvent projects one tracker notification onto the wire.
//
// Only the already-resolved semantic fields cross: the source, the severity,
// the transition class, the bounded title and body, and the stable identity of
// the workspace. The pane's text is not here, and neither is anything the
// receiving machine could act on.
func notifyEvent(n notify.Notification) (hostproto.NotifyEvent, bool) {
	meta := n.Transition
	if meta == nil || meta.LaneKey == "" {
		return hostproto.NotifyEvent{}, false
	}
	class := hostproto.NotifyClass(meta.Class)
	switch class {
	case hostproto.NotifyWaiting, hostproto.NotifyDone, hostproto.NotifyFailure:
	default:
		return hostproto.NotifyEvent{}, false
	}
	origin := hostproto.NotifyOrigin{
		ItemID:     meta.LaneKey,
		ProjectKey: n.Origin.ProjectKey,
		Session:    n.Origin.TmuxSession,
		Path:       n.Origin.WorkDir,
	}
	occurred := n.CreatedAt.UTC()
	event := hostproto.NotifyEvent{
		Key:        hostproto.NotifyKey(origin, class, occurred),
		OccurredAt: occurred,
		Class:      class,
		Source:     string(n.Source),
		Severity:   string(n.Severity),
		Title:      hostproto.BoundNotifyText(n.Title, hostproto.MaxNotifyTitleBytes),
		Body:       hostproto.BoundNotifyText(n.Body, hostproto.MaxNotifyBodyBytes),
		Sticky:     n.Sticky,
		Origin:     origin,
	}
	// Refuse here rather than let the encoder refuse. A message that cannot
	// satisfy the contract is one notification lost; an encode error in the
	// serve loop ends the connection and loses the host.
	if err := (hostproto.Message{Kind: hostproto.KindNotify, Notify: &event}).Validate(); err != nil {
		return hostproto.NotifyEvent{}, false
	}
	return event, true
}

// laneObservations projects one project's refreshed workspaces into the
// tracker's vocabulary. Only rows with a resolved agent are included: a plain
// worktree's lane is a projection of legacy status and would announce
// transitions nobody made.
func laneObservations(result workspaceinventory.ProjectResult) []notify.LaneObservation {
	out := make([]notify.LaneObservation, 0, len(result.Workspaces))
	for _, w := range result.Workspaces {
		if !w.HasAgent() {
			continue
		}
		out = append(out, notify.LaneObservation{
			Key:          w.ID,
			Label:        w.Name,
			Context:      laneContext(result.ProjectName, w.Branch),
			Provider:     w.Provider,
			Presentation: w.Presentation,
			Origin: notify.Origin{
				TmuxSession: w.TmuxName,
				ProjectKey:  result.ProjectKey,
				WorkDir:     w.Path,
			},
			ProjectRoot: result.ProjectRoot,
		})
	}
	return out
}

// laneContext is the "where" half of a notification body, in the same shape
// the local workspace adapter builds it: with five agents running, the name
// alone does not say which one is talking.
func laneContext(project, branch string) string {
	project = strings.TrimSpace(project)
	branch = strings.TrimSpace(branch)
	switch {
	case project != "" && branch != "" && branch != project:
		return project + "/" + branch
	case project != "":
		return project
	default:
		return branch
	}
}
