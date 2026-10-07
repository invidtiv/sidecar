// Package hostnotify adapts authenticated host transitions to the shared notification core.
package hostnotify

import (
	"errors"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/notify"
	"path/filepath"
	"strings"
	"time"
)

type Events struct {
	Post               []notify.Notification
	Dismiss            []string
	DismissTransitions []string
}

// Adapt keeps SSH opt-in, freshness, sanitization and host identity identical across consumers.
func Adapt(update hosts.Update, enabled bool, now time.Time) Events {
	var out Events
	if !enabled {
		return out
	}
	for _, event := range update.Notify {
		if event.IsWithdrawal() {
			if event.WithdrawsTransition {
				if key, ok := DedupeKey(update.HostID, event); ok {
					out.DismissTransitions = append(out.DismissTransitions, key)
				}
			} else if update.HostID != "" {
				out.Dismiss = append(out.Dismiss, notify.RemoteID(update.HostID, event.Withdraws))
			}
		} else if n, ok := Notification(update.HostID, event, now); ok {
			out.Post = append(out.Post, n)
		}
	}
	return out
}

// Apply stores adapted outcomes; aliases retain the canonical ID when concurrent observers
// report one logical wait with different event keys. Only outstanding waits need aliases.
func Apply(store notify.Store, events Events, aliases map[string]string) error {
	for _, n := range events.Post {
		result, err := store.Post(n)
		if err != nil {
			return err
		}
		if n.Transition != nil && n.Transition.Class == notify.TransitionWaiting && result.ID != n.ID {
			aliases[n.ID] = result.ID
		}
	}
	for _, id := range events.Dismiss {
		canonical := id
		if aliases[id] != "" {
			canonical = aliases[id]
		}
		if err := store.Dismiss(canonical); err != nil && !errors.Is(err, notify.ErrNotFound) {
			return err
		}
		delete(aliases, id)
		for alias, value := range aliases {
			if value == canonical {
				delete(aliases, alias)
			}
		}
	}
	if len(events.DismissTransitions) > 0 {
		all, err := store.List()
		if err != nil {
			return err
		}
		for _, key := range events.DismissTransitions {
			for _, n := range all {
				if n.Transition != nil && n.Transition.DedupeKey == key && !n.Dismissed() {
					if err := store.Dismiss(n.ID); err != nil {
						return err
					}
					for id, canonical := range aliases {
						if canonical == n.ID {
							delete(aliases, id)
						}
					}
				}
			}
		}
	}
	return nil
}

// Notification adapts one wire event into the local model.
//
// Text is sanitized and bounded again on arrival even though the host did it
// before sending. The host is across an authenticated trust boundary, not
// inside it, and this is the last point before the text becomes a stored
// record that a local desktop service may later be handed.
func Notification(hostID string, event hostproto.NotifyEvent, now time.Time) (notify.Notification, bool) {
	class := transitionClass(event.Class)
	if class == "" || hostID == "" {
		return notify.Notification{}, false
	}
	occurred := event.OccurredAt.UTC()
	if occurred.IsZero() || now.Sub(occurred) > notify.LiveEventGrace {
		return notify.Notification{}, false
	}
	if occurred.After(now) {
		// A host whose clock runs ahead must not pin a toast to a countdown
		// that has not started. The event is as fresh as it can be: now.
		occurred = now
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
	origin := remoteOrigin(hostID, event.Origin)
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
			DedupeKey:      dedupeKeyFor(hostID, stable, class),
			ReplacementKey: stable,
		},
	}, true
}

// remoteOrigin builds the local origin for a remote event.
//
// A post and a transition withdrawal both derive identity through here, and
// that is the point: a withdrawal computing the key even slightly differently
// would match no record and fail silently, which is indistinguishable from the
// bug it exists to fix.
func remoteOrigin(hostID string, o hostproto.NotifyOrigin) notify.Origin {
	return notify.Origin{
		HostID:      hostID,
		TmuxSession: o.Session,
		ProjectKey:  filepath.Base(filepath.Clean(strings.TrimSpace(hosts.ScopedKey(hostID, o.ProjectKey)))),
		WorkDir:     o.Path,
	}
}

func dedupeKeyFor(hostID, stableOrigin string, class notify.TransitionClass) string {
	return hostID + ":" + stableOrigin + ":" + string(class)
}

// DedupeKey names the transition a withdrawal retires.
func DedupeKey(hostID string, event hostproto.NotifyEvent) (string, bool) {
	class := transitionClass(event.Class)
	if class == "" || hostID == "" {
		return "", false
	}
	return dedupeKeyFor(hostID, remoteOrigin(hostID, event.Origin).StableKey(), class), true
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
