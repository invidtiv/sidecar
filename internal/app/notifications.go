package app

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uirequest"
)

// The app shell owns the notification store. It is the single writer inside
// this process, and the only thing that answers a `notify` UI request, so
// every surface — toasts, the header indicator, the centre panel — reads one
// snapshot rather than each opening the log for itself.

// PostNotification returns a command that files a notification.
func PostNotification(n notify.Notification) tea.Cmd {
	return func() tea.Msg { return notify.PostMsg{Notification: n} }
}

// DismissNotification returns a command that dismisses one notification.
func DismissNotification(id string) tea.Cmd {
	return func() tea.Msg { return notify.DismissMsg{ID: id} }
}

// ReadNotification returns a command that marks one notification read.
func ReadNotification(id string) tea.Cmd {
	return func() tea.Msg { return notify.ReadMsg{ID: id} }
}

// openNotificationStore opens the JSONL store, falling back to an in-memory
// store when the state tree cannot be written. A state dir that refuses to
// open costs the user persistence, never the alert itself.
func openNotificationStore() notify.Store {
	store, err := notify.Open(config.StateDir())
	if err != nil {
		slog.Debug("notify: falling back to an in-memory store", "err", err)
		return notify.NewMemStore()
	}
	return store
}

// Notifications returns the current snapshot, newest first.
func (m *Model) Notifications() []notify.Notification {
	if m.notifications == nil {
		return nil
	}
	return m.notificationCache
}

// UnreadNotifications is what the header indicator counts.
func (m *Model) UnreadNotifications() int {
	return notify.UnreadCount(m.notificationCache)
}

// refreshNotifications re-reads the store into the render-side cache. Views
// never touch the store directly: they read a slice that only changes on an
// update, so a frame cannot be built mid-write.
func (m *Model) refreshNotifications() {
	if m.notifications == nil {
		m.notificationCache = nil
		return
	}
	all, err := m.notifications.List()
	if err != nil {
		slog.Debug("notify: list failed", "err", err)
		return
	}
	m.notificationCache = all
}

// postNotification stores a notification and returns the broadcast announcing
// it, so a toast host can start its countdown from the stored record.
func (m *Model) postNotification(n notify.Notification) tea.Cmd {
	if m.notifications == nil {
		return nil
	}
	stored, err := m.notifications.Post(n)
	if err != nil {
		slog.Debug("notify: post failed", "err", err)
		return nil
	}
	m.refreshNotifications()
	return func() tea.Msg { return notify.PostedMsg{Notification: stored} }
}

// dismissNotification dismisses by id, ignoring an id the store never saw.
func (m *Model) dismissNotification(id string) {
	if m.notifications == nil || id == "" {
		return
	}
	if err := m.notifications.Dismiss(id); err != nil {
		slog.Debug("notify: dismiss failed", "id", id, "err", err)
		return
	}
	m.refreshNotifications()
}

// readNotification marks one notification read.
func (m *Model) readNotification(id string) {
	if m.notifications == nil || id == "" {
		return
	}
	if err := m.notifications.MarkRead(id); err != nil {
		slog.Debug("notify: mark read failed", "id", id, "err", err)
		return
	}
	m.refreshNotifications()
}

// sweepNotifications runs on the 1s heartbeat. It retires toasts whose
// countdown has run out (they stay in the centre — suppressed is not dropped)
// and compacts records past the 24h retention window.
func (m *Model) sweepNotifications(now time.Time) {
	if m.notifications == nil {
		return
	}
	removed, err := m.notifications.Sweep(now)
	if err != nil {
		slog.Debug("notify: sweep failed", "err", err)
		return
	}
	if removed > 0 {
		m.refreshNotifications()
	}
}

// handleNotifyRequest answers a `notify` request from the file-RPC bus: the
// out-of-process posting API, landing in the same store as an in-process post.
//
// An instance only answers for its own project. Otherwise a second Sidecar on
// another repo would file another copy, and the CLI's no-ack fallback (which
// writes the log directly) would never run for the project that is not open.
func (m *Model) handleNotifyRequest(req uirequest.Request) tea.Cmd {
	if req.Action != uirequest.ActionNotify {
		return nil
	}
	if !m.ownsNotifyRequest(req) {
		m.ackNotify(req, uirequest.StatusDeclined, "this instance is not showing that project")
		return nil
	}

	if req.Target.Kind == uirequest.TargetKindNotification && req.Target.Value != "" {
		// Dismissal. The CLI has already applied the origin check against the
		// log; it is re-applied here because this process must never dismiss
		// something on a caller's word alone.
		id := req.Target.Value
		found, ok := m.findNotification(id)
		if !ok {
			m.ackNotify(req, uirequest.StatusDeclined, "no such notification")
			return nil
		}
		if !notify.MayDismiss(found, notifyOriginFrom(req.Origin)) {
			m.ackNotify(req, uirequest.StatusDeclined, "a caller may only dismiss notifications it posted")
			return nil
		}
		m.dismissNotification(id)
		m.ackNotify(req, uirequest.StatusOpened, "")
		return nil
	}

	var n notify.Notification
	if len(req.Payload) == 0 {
		m.ackNotify(req, uirequest.StatusError, "notify request carried no notification")
		return nil
	}
	if err := json.Unmarshal(req.Payload, &n); err != nil {
		m.ackNotify(req, uirequest.StatusError, "notify payload could not be read")
		return nil
	}
	if n.Origin.Zero() {
		n.Origin = notifyOriginFrom(req.Origin)
	}
	cmd := m.postNotification(n)
	m.ackNotify(req, uirequest.StatusOpened, "")
	return cmd
}

// ownsNotifyRequest reports whether this instance is the one the request is
// addressed to. A request with no project or working directory is unaddressed
// and every instance takes it.
func (m *Model) ownsNotifyRequest(req uirequest.Request) bool {
	origin := req.Origin
	if origin.WorkDir == "" && origin.ProjectKey == "" {
		return true
	}
	for _, mine := range []string{m.ui.WorkDir, m.ui.ProjectRoot} {
		if mine == "" {
			continue
		}
		if origin.WorkDir != "" && pathsEqual(mine, origin.WorkDir) {
			return true
		}
		if origin.ProjectKey != "" && filepath.Base(mine) == origin.ProjectKey {
			return true
		}
	}
	return false
}

func pathsEqual(a, b string) bool {
	an, _ := normalizePath(a)
	bn, _ := normalizePath(b)
	return an == bn
}

func (m *Model) findNotification(id string) (notify.Notification, bool) {
	for _, n := range m.notificationCache {
		if n.ID == id {
			return n, true
		}
	}
	return notify.Notification{}, false
}

func notifyOriginFrom(o uirequest.Origin) notify.Origin {
	return notify.Origin{
		TmuxSession: o.TmuxSession,
		ProjectKey:  o.ProjectKey,
		WorkDir:     o.WorkDir,
		PID:         o.PID,
	}
}

func (m *Model) ackNotify(req uirequest.Request, status uirequest.Status, reason string) {
	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: uirequest.InstanceID("app"),
		Host:     uirequest.HostName(),
		PID:      os.Getpid(),
		Status:   status,
		Reason:   reason,
		Surface:  "notifications",
	})
}

// ToastableNotifications is the set a toast host should currently be drawing.
func (m *Model) ToastableNotifications(now time.Time) []notify.Notification {
	return notify.Toastable(m.notificationCache, now)
}
