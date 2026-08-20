package msg

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/notify"
)

// Alert returns a command filing a notification from a named source, for the
// producers whose event is not generic app chrome: a blocked action the user
// must act on, a merge that unwound itself, an agent that fell back. It is the
// source-aware sibling of ShowToast, which always speaks as `system`, and it
// goes down the same road — notify.PostMsg, answered by the app shell.
func Alert(source notify.SourceID, severity notify.Severity, title string) tea.Cmd {
	return func() tea.Msg { return notify.Alert(source, severity, title) }
}

// Blocked reports an action the app refused, with the reason. It is a warning
// from the `waiting` source because the user is the one who has to do something
// about it; the lease is explicit so a transient refusal does not sit sticky in
// the centre the way a genuinely waiting agent should.
func Blocked(reason string) tea.Cmd {
	return func() tea.Msg {
		post := notify.Alert(notify.SourceWaiting, notify.SeverityWarning, reason)
		expires := time.Now().UTC().Add(blockedActionExpiry)
		post.Notification.ExpiresAt = &expires
		return post
	}
}

// blockedActionExpiry is how long a refusal stays on screen: long enough to
// read a sentence, short enough that it never becomes the centre's furniture.
const blockedActionExpiry = 6 * time.Second
