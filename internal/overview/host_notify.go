package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/notify"
)

// Forwarded remote notifications land here, where host updates already land.
//
// This file is an adapter and nothing more. It turns a bounded wire event into
// an ordinary notify.PostMsg and hands it to the app's existing post seam —
// the same road a local lane transition takes. Everything that decides whether
// the user hears anything stays where it already is: the store owns dedupe and
// retention, notify.ResolveDelivery owns source rules, channel modes, quiet
// hours and foreground, the delivery ledger owns the claim, and the platform
// adapters own the providers. None of them learn that SSH exists.
//
// Two gates run before that, and both are here rather than downstream:
//
//   - notifications.ssh.managedHosts. Off by default, and it governs local
//     consumption only: the read-only status stream is unaffected, so a user
//     who has not opted in still sees remote rows and simply hears nothing.
//   - the live-event window. A forwarded event crossed a network and was
//     stamped by another machine's clock. One that queued behind a slow link,
//     or arrived from a host whose clock is adrift, is not news; refusing it
//     here means no record at all rather than a stale record that policy would
//     have to keep quiet.

// forwardHostNotifications turns one update's forwarded events into local
// posts and withdrawals.
func (m *Model) forwardHostNotifications(update hosts.Update) tea.Cmd {
	if len(update.Notify) == 0 || !m.managedHostNotificationsEnabled() {
		return nil
	}
	now := time.Now().UTC()
	cmds := make([]tea.Cmd, 0, len(update.Notify))
	for _, event := range update.Notify {
		if event.IsWithdrawal() {
			// The wait was answered on the host. Withdrawing by derived ID
			// works from any local process, including one that never saw the
			// post: the ID is a function of the host and the event key.
			id := notify.RemoteID(update.HostID, event.Withdraws)
			cmds = append(cmds, func() tea.Msg { return notify.DismissMsg{ID: id} })
			continue
		}
		n, ok := remoteNotification(update.HostID, event, now)
		if !ok {
			continue
		}
		cmds = append(cmds, func() tea.Msg { return notify.PostMsg{Notification: n} })
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) managedHostNotificationsEnabled() bool {
	return m.config != nil && m.config.Notifications.SSH.ManagedHosts
}

// remoteNotification adapts one wire event into the local model.
//
// Text is sanitized and bounded again on arrival even though the host did it
// before sending. The host is across an authenticated trust boundary, not
// inside it, and this is the last point before the text becomes a stored
// record that a local desktop service may later be handed.
func remoteNotification(hostID string, event hostproto.NotifyEvent, now time.Time) (notify.Notification, bool) {
	class := transitionClass(event.Class)
	if class == "" || hostID == "" {
		return notify.Notification{}, false
	}
	occurred := event.OccurredAt.UTC()
	if occurred.IsZero() || now.Sub(occurred) > notify.LiveEventGrace {
		return notify.Notification{}, false
	}
	title := hostproto.BoundNotifyText(event.Title, hostproto.MaxNotifyTitleBytes)
	if title == "" {
		return notify.Notification{}, false
	}
	source := notify.SourceID(event.Source)
	if !notify.ValidSource(source) {
		// A source this build does not know about is filed as the transition's
		// own source rather than dropped or invented. The user's rules are
		// written against the sources they can see.
		source = defaultSourceFor(class)
	}
	origin := notify.Origin{
		HostID:      hostID,
		TmuxSession: event.Origin.Session,
		ProjectKey:  attentionProjectKey(hosts.ScopedKey(hostID, event.Origin.ProjectKey)),
		WorkDir:     event.Origin.Path,
	}
	stable := origin.StableKey()
	return notify.Notification{
		ID:        notify.RemoteID(hostID, event.Key),
		Source:    source,
		Severity:  severityFor(event.Severity, class),
		Title:     title,
		Body:      hostproto.BoundNotifyText(event.Body, hostproto.MaxNotifyBodyBytes),
		CreatedAt: occurred,
		Sticky:    event.Sticky,
		Origin:    origin,
		Transition: &notify.TransitionMetadata{
			Class:   class,
			LaneKey: hosts.ScopedKey(hostID, event.Origin.ItemID),
			// The dedupe key is the second duplicate rule, and it covers what
			// the derived ID cannot: two serve processes whose observations
			// fell either side of the event key's time bucket produce two IDs
			// for one transition, and the store's logical window collapses
			// them here.
			DedupeKey:      hostID + ":" + stable + ":" + string(class),
			ReplacementKey: stable,
		},
	}, true
}

func transitionClass(class hostproto.NotifyClass) notify.TransitionClass {
	switch class {
	case hostproto.NotifyWaiting:
		return notify.TransitionWaiting
	case hostproto.NotifyDone:
		return notify.TransitionDone
	case hostproto.NotifyFailure:
		return notify.TransitionFailure
	default:
		return ""
	}
}

func defaultSourceFor(class notify.TransitionClass) notify.SourceID {
	if class == notify.TransitionWaiting {
		return notify.SourceWaiting
	}
	return notify.SourceSession
}

// severityFor keeps the wire's severity only when it is one this build knows.
// Anything else takes the class's own severity, so an unfamiliar value cannot
// quietly promote a finished turn into an error cue.
func severityFor(severity string, class notify.TransitionClass) notify.Severity {
	switch notify.Severity(severity) {
	case notify.SeverityInfo:
		return notify.SeverityInfo
	case notify.SeverityWarning:
		return notify.SeverityWarning
	case notify.SeverityError:
		return notify.SeverityError
	}
	switch class {
	case notify.TransitionWaiting:
		return notify.SeverityWarning
	case notify.TransitionFailure:
		return notify.SeverityError
	default:
		return notify.SeverityInfo
	}
}
