package notify

import (
	"sort"
	"time"
)

// Retention is how long a dismissed notification stays in the store before it
// is compacted away. The centre's footer note ("Dismissed items clear after
// 24h") is a description of this constant, not a separate rule.
const Retention = 24 * time.Hour

// Active returns the notifications that still belong in the centre: everything
// not dismissed, newest first.
func Active(all []Notification) []Notification {
	out := make([]Notification, 0, len(all))
	for _, n := range all {
		if !n.Dismissed() {
			out = append(out, n)
		}
	}
	SortNewestFirst(out)
	return out
}

// Unread returns the undismissed, unread notifications, newest first.
func Unread(all []Notification) []Notification {
	out := make([]Notification, 0, len(all))
	for _, n := range all {
		if !n.Dismissed() && !n.Read() {
			out = append(out, n)
		}
	}
	SortNewestFirst(out)
	return out
}

// UnreadCount is what the header indicator counts.
func UnreadCount(all []Notification) int {
	count := 0
	for _, n := range all {
		if !n.Dismissed() && !n.Read() {
			count++
		}
	}
	return count
}

// SortNewestFirst orders in place by creation time, newest first, with the id
// as a stable tiebreak so two notifications posted in the same instant do not
// swap places between frames.
func SortNewestFirst(ns []Notification) {
	sort.SliceStable(ns, func(i, j int) bool {
		if !ns[i].CreatedAt.Equal(ns[j].CreatedAt) {
			return ns[i].CreatedAt.After(ns[j].CreatedAt)
		}
		return ns[i].ID > ns[j].ID
	})
}

// Loudest returns the unread notification that decides the indicator's colour:
// highest severity first, then the source's registry priority, then the newest.
// It reports false when there is nothing unread.
func Loudest(all []Notification) (Notification, bool) {
	var best Notification
	found := false
	for _, n := range all {
		if n.Dismissed() || n.Read() {
			continue
		}
		if !found || louder(n, best) {
			best, found = n, true
		}
	}
	return best, found
}

// LoudestHue is the colour the header indicator takes. It returns HueMuted and
// false when nothing is unread, which is the `·` empty state.
func LoudestHue(all []Notification) (Hue, bool) {
	n, ok := Loudest(all)
	if !ok {
		return HueMuted, false
	}
	if n.Severity == SeverityError {
		return HueError, true
	}
	return n.SourceInfo().Hue, true
}

func louder(a, b Notification) bool {
	if ar, br := a.Severity.Rank(), b.Severity.Rank(); ar != br {
		return ar > br
	}
	if ap, bp := SourceOf(a.Source).Priority, SourceOf(b.Source).Priority; ap != bp {
		return ap > bp
	}
	return a.CreatedAt.After(b.CreatedAt)
}

// MayToast reports whether a notification should currently be on screen as a
// toast: never seen, never dismissed, and not past its expiry. A sticky
// notification has no expiry and toasts until it is read or dismissed.
func MayToast(n Notification, now time.Time) bool {
	if n.Dismissed() || n.Read() {
		return false
	}
	return !ToastExpired(n, now)
}

// ToastExpired reports whether a toast's countdown has run out.
func ToastExpired(n Notification, now time.Time) bool {
	if n.Sticky || n.ExpiresAt == nil {
		return false
	}
	return !now.UTC().Before(n.ExpiresAt.UTC())
}

// Toastable filters a set to what may be on screen right now, newest first.
func Toastable(all []Notification, now time.Time) []Notification {
	out := make([]Notification, 0, len(all))
	for _, n := range all {
		if MayToast(n, now) {
			out = append(out, n)
		}
	}
	SortNewestFirst(out)
	return out
}

// MayDismiss reports whether caller is allowed to dismiss n from outside the
// TUI. Inside the TUI the user dismisses anything; over the CLI a caller may
// only dismiss what it posted.
func MayDismiss(n Notification, caller Origin) bool {
	return n.Origin.Matches(caller)
}

// Expired reports whether a dismissed notification is past the retention
// window and may be compacted away. Undismissed notifications never expire out
// of the store, however old they are.
func Expired(n Notification, now time.Time) bool {
	if n.DismissedAt == nil {
		return false
	}
	return now.UTC().Sub(n.DismissedAt.UTC()) > Retention
}

// GroupBySource splits a set into per-source groups in registry order, for the
// centre's section grammar. Groups with nothing in them are omitted.
func GroupBySource(all []Notification) []Group {
	byID := map[SourceID][]Notification{}
	for _, n := range all {
		id := SourceOf(n.Source).ID
		byID[id] = append(byID[id], n)
	}
	out := make([]Group, 0, len(byID))
	for _, s := range sources {
		items := byID[s.ID]
		if len(items) == 0 {
			continue
		}
		SortNewestFirst(items)
		out = append(out, Group{Source: s, Items: items})
	}
	return out
}

// Group is one section of the notification centre.
type Group struct {
	Source Source
	Items  []Notification
}

// Unread counts the unread items in the group.
func (g Group) Unread() int { return UnreadCount(g.Items) }
