package uiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mobileproto"
	notification "github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
	"github.com/marcus/sidecar/internal/uirequest"
)

const notificationsPath = "/api/v0/notifications"
const notificationSettingsPath = notificationsPath + "/settings"
const notificationOptionsPath = notificationSettingsPath + "/options"
const notificationReadPath = notificationsPath + "/read"
const notificationDismissPath = notificationsPath + "/dismiss"
const notificationClaimPath = notificationsPath + "/claim"
const notificationReceiptPath = notificationsPath + "/receipt"

type NotificationSnapshot struct {
	Notifications []notification.Notification              `json:"notifications"`
	Unread        int                                      `json:"unread"`
	ToastIDs      []string                                 `json:"toast_ids"`
	DeliveryIDs   []string                                 `json:"delivery_ids"`
	Delivery      map[string]notification.DeliveryDecision `json:"delivery"`
}

// NotificationMutation names the records to mark read or dismiss. Send id for
// one record (an unknown id is 404) or ids, never both, for a batch applied as one change:
// a batch skips unknown and already-settled ids, because the goal state holds
// for them. Servers advertising the notifications_batch capability accept ids.
type NotificationMutation struct {
	ID  string   `json:"id,omitempty"`
	IDs []string `json:"ids,omitempty"`
}

// maxNotificationBatch bounds one mutation; the request body cap bounds it too.
const maxNotificationBatch = 1000

type NotificationReceiptRequest struct {
	Channel   string `json:"channel,omitempty"`
	Succeeded *bool  `json:"succeeded,omitempty"`
	Error     string `json:"error,omitempty"`
	ID        string `json:"id"`
	ViewerID  string `json:"viewer_id,omitempty"`
}
type NotificationClaimResponse struct {
	Claimed bool `json:"claimed"`
}
type NotificationReceiptResponse struct {
	Delivered bool `json:"delivered"`
}

// NotificationSettingsOptions is what a settings form needs beside the stored
// NotificationsConfig: the registered sources a client may override, with
// their built-in rules, and whether this credential may save.
type NotificationSettingsOptions struct {
	// Writable reports whether this caller may PUT the settings (ui:control or
	// full). A read-only credential can still show them.
	Writable bool `json:"writable"`
	// Sources lists the registered sources, loudest first.
	Sources []NotificationSourceOption `json:"sources"`
}

// NotificationSourceOption is one registered source as a form presents it.
type NotificationSourceOption struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Defaults is the rule this build applies when the source has no override.
	Defaults NotificationSourceRule `json:"defaults"`
}

// NotificationSourceRule is a resolved per-source rule. Expiry is a Go
// duration (`10s`) or `sticky` for a toast that stays until dismissed.
type NotificationSourceRule struct {
	Toast  bool   `json:"toast"`
	Native bool   `json:"native"`
	Sound  string `json:"sound"`
	Expiry string `json:"expiry"`
}

func notificationSettingsOptions(writable bool) NotificationSettingsOptions {
	builtIn := notification.ResolveConfig(config.NotificationsConfig{})
	out := NotificationSettingsOptions{Writable: writable, Sources: []NotificationSourceOption{}}
	for _, source := range notification.Sources() {
		rule := builtIn.SourceRule(source.ID)
		expiry := "sticky"
		if rule.Expiry != config.StickyExpiry {
			expiry = rule.Expiry.String()
		}
		out.Sources = append(out.Sources, NotificationSourceOption{
			ID: string(source.ID), Title: source.Title, Description: source.Description,
			Defaults: NotificationSourceRule{Toast: rule.Toast, Native: rule.Native, Sound: string(rule.Sound), Expiry: expiry},
		})
	}
	return out
}

func (s *Server) handleNotificationSettingsOptions(w http.ResponseWriter, r *http.Request, c caller) {
	writeJSON(w, 200, notificationSettingsOptions(s.hasScope(c, ScopeUIControl)))
}

func notificationSettings() (config.NotificationsConfig, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.NotificationsConfig{}, err
	}
	return cfg.Notifications, nil
}
func (s *Server) notificationSnapshot(c caller) (NotificationSnapshot, error) {
	all, err := notification.ReadAll(notification.Path(s.opts.StateDir))
	if err != nil {
		return NotificationSnapshot{}, err
	}
	cfg, err := notificationSettings()
	if err != nil {
		return NotificationSnapshot{}, err
	}
	s.projectNotificationTargets(all)
	rules := notification.ResolveConfig(cfg)
	out := NotificationSnapshot{Notifications: notification.Active(all), Unread: notification.UnreadCount(all), ToastIDs: []string{}, DeliveryIDs: []string{}, Delivery: map[string]notification.DeliveryDecision{}}
	s.viewer.mu.Lock()
	holder := s.holderLocked()
	foreground := holder != nil && holder.caller.client == c.client
	s.viewer.mu.Unlock()
	now := time.Now()
	for _, n := range out.Notifications {
		if notification.MayToast(n, now) && rules.SourceRule(n.Source).Toast {
			out.ToastIDs = append(out.ToastIDs, n.ID)
		}
		out.Delivery[n.ID] = notification.ResolveDelivery(n, rules, notification.RuntimeContext{Now: now, Foreground: foreground, Discovered: true, Capabilities: notification.CapabilitySet{Native: true, Sound: true}})
		if decision := out.Delivery[n.ID]; decision.Native.Deliver || decision.Sound.Deliver {
			out.DeliveryIDs = append(out.DeliveryIDs, n.ID)
		}
	}
	return out, nil
}
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request, c caller) {
	out, err := s.notificationSnapshot(c)
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleNotificationSettings(w http.ResponseWriter, r *http.Request, c caller) {
	if r.Method == http.MethodPut {
		var input config.NotificationsConfig
		if !decodeBody(w, r, &input) {
			return
		}
		if err := config.ValidateNotifications(input, config.ConfigPath()); err != nil {
			detail := ErrorDetail{Code: CodeInvalidRequest, Message: err.Error()}
			var field *config.NotificationFieldError
			if errors.As(err, &field) {
				detail.Field = field.Field
			}
			writeJSON(w, 400, ErrorBody{Error: detail})
			return
		}
		if err := config.SaveNotifications(func(cfg *config.NotificationsConfig) { *cfg = input }); err != nil {
			writeContentError(w, err)
			return
		}
		notification.ApplyConfig(input)
		// Existing TUI consumers reload the same config through the public request bus.
		_, _ = uirequest.WriteRequest(s.opts.StateDir, uirequest.Request{ID: uirequest.NewRequestID(), Action: uirequest.ActionConfigReload})
	}
	cfg, err := notificationSettings()
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, 200, cfg)
}
func (s *Server) handleNotificationMutation(w http.ResponseWriter, r *http.Request, c caller) {
	var input NotificationMutation
	if !decodeBody(w, r, &input) {
		return
	}
	batch := input.IDs != nil
	if batch && input.ID != "" {
		writeError(w, 400, CodeInvalidRequest, "Send id or ids, not both.")
		return
	}
	ids := input.IDs
	if !batch {
		ids = []string{input.ID}
	}
	if len(ids) == 0 || !batch && input.ID == "" {
		writeError(w, 400, CodeInvalidRequest, "id or ids is required")
		return
	}
	if len(ids) > maxNotificationBatch {
		writeError(w, 400, CodeInvalidRequest, fmt.Sprintf("Send at most %d ids per request.", maxNotificationBatch))
		return
	}
	for _, id := range ids {
		if id == "" {
			writeError(w, 400, CodeInvalidRequest, "ids must not be empty")
			return
		}
	}
	store, err := notification.Open(s.opts.StateDir)
	if err != nil {
		writeContentError(w, err)
		return
	}
	defer func() { _ = store.Close() }()
	switch {
	case batch && r.URL.Path == notificationReadPath:
		_, err = store.MarkReadMany(ids)
	case batch:
		_, err = store.DismissMany(ids)
	case r.URL.Path == notificationReadPath:
		err = store.MarkRead(input.ID)
	default:
		err = store.Dismiss(input.ID)
	}
	if errors.Is(err, notification.ErrNotFound) {
		writeError(w, 404, CodeNotFound, "Notification not found.")
		return
	}
	if err != nil {
		writeContentError(w, err)
		return
	}
	s.handleNotifications(w, r, c)
}
func (s *Server) handleNotificationReceipt(w http.ResponseWriter, r *http.Request, c caller) {
	var input NotificationReceiptRequest
	if !decodeBody(w, r, &input) {
		return
	}
	if input.Channel == "" {
		input.Channel = "toast"
	}
	if input.Channel != "toast" && input.Channel != "native" && input.Channel != "sound" {
		writeError(w, 400, CodeInvalidRequest, "channel must be toast, native or sound")
		return
	}
	snapshot, err := s.notificationSnapshot(c)
	if err != nil {
		writeContentError(w, err)
		return
	}
	found := false
	for _, n := range snapshot.Notifications {
		if n.ID == input.ID {
			found = true
		}
	}
	if !found {
		writeError(w, 404, CodeNotFound, "Notification not found.")
		return
	}
	owner := "browser:" + c.client
	ledger, err := notifydelivery.Open(s.opts.StateDir)
	if err != nil {
		writeContentError(w, err)
		return
	}
	defer func() { _ = ledger.Close() }()
	if r.URL.Path == notificationClaimPath {
		eligible := false
		if input.Channel == "toast" {
			for _, id := range snapshot.ToastIDs {
				if id == input.ID {
					eligible = true
				}
			}
		} else {
			d := snapshot.Delivery[input.ID]
			eligible = input.Channel == "native" && d.Native.Deliver || input.Channel == "sound" && d.Sound.Deliver
		}
		if !eligible {
			writeJSON(w, 200, NotificationClaimResponse{})
			return
		}
		won, _, err := ledger.Claim(input.ID, input.Channel, owner, time.Now(), 15*time.Second)
		if err != nil {
			writeContentError(w, err)
			return
		}
		writeJSON(w, 200, NotificationClaimResponse{Claimed: won})
		return
	}
	if input.Error != "" {
		input.Error = cleanClientText(input.Error)
	}
	succeeded := input.Succeeded == nil || *input.Succeeded
	if err = ledger.Complete(input.ID, input.Channel, notifydelivery.Receipt{Owner: owner, Provider: "browser", Succeeded: succeeded, Error: input.Error}); err != nil {
		writeError(w, 409, "claim_unavailable", "Claim this delivery before reporting its result.")
		return
	}
	if input.Channel == "toast" && succeeded {
		s.viewer.mu.Lock()
		defer s.viewer.mu.Unlock()
		v := s.holderLocked()
		if v != nil && v.caller.client == c.client && (input.ViewerID == "" || input.ViewerID == v.id) {
			for id, req := range v.notifications {
				var n notification.Notification
				_ = json.Unmarshal(req.Payload, &n)
				if n.ID == input.ID {
					_ = uirequest.WithRequestLock(s.opts.StateDir, id, req.Action, func() error {
						if _, err := os.Stat(uirequest.RequestPath(s.opts.StateDir, id, req.Action)); err != nil {
							return nil
						}
						return uirequest.WriteAck(s.opts.StateDir, id, req.Action, uirequest.Ack{Instance: v.id, PID: s.endpoint.PID, Host: uirequest.HostName(), Surface: "browser", Status: uirequest.StatusOpened})
					})
					delete(v.notifications, id)
				}
			}
		}
	}
	writeJSON(w, 200, NotificationReceiptResponse{Delivered: succeeded})
}

// Watch filesystem invalidations and send coalesced authoritative snapshots.
// Browser clients neither poll nor interpret the append-only log.
func (s *Server) watchNotifications(ctx context.Context, c caller, pending *eventPending) (func(), error) {
	if !s.hasScope(c, ScopeContentRead) && !s.hasScope(c, ScopeUIControl) {
		return func() {}, nil
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{s.opts.StateDir, filepath.Dir(config.ConfigPath())} {
		if err = watcher.Add(dir); err != nil {
			_ = watcher.Close()
			return nil, err
		}
	}
	refresh := func() {
		out, err := s.notificationSnapshot(c)
		if err != nil {
			pending.put(EventMessage{Type: "error", Error: &ErrorDetail{Code: CodeBackend, Message: err.Error()}})
			return
		}
		pending.put(EventMessage{Type: "notifications", Notifications: &out})
	}
	refresh()
	done := make(chan struct{})
	watchCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(done)
		defer func() { _ = watcher.Close() }()
		for {
			select {
			case <-watchCtx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Name == notification.Path(s.opts.StateDir) || event.Name == config.ConfigPath() {
					refresh()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				pending.put(EventMessage{Type: "error", Error: &ErrorDetail{Code: CodeBackend, Message: err.Error()}})
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}

func (s *Server) relayNotification(req uirequest.Request) {
	s.viewer.mu.Lock()
	defer s.viewer.mu.Unlock()
	v := s.viewer.screens[req.Viewer]
	if v == nil || s.holderLocked() != v || s.viewer.notificationConsumers[v.caller.client] == 0 {
		return
	}
	// Notification posting is global to this machine; delivery is addressed to its active screen.
	store, err := notification.Open(s.opts.StateDir)
	if err != nil {
		return
	}
	defer func() { _ = store.Close() }()
	if req.Target.Value != "" {
		all, _ := store.List()
		for _, n := range all {
			if n.ID == req.Target.Value && notification.MayDismiss(n, notification.Origin{TmuxSession: req.Origin.TmuxSession, WorkDir: req.Origin.WorkDir, ProjectKey: req.Origin.ProjectKey}) {
				_ = store.Dismiss(n.ID)
				_ = uirequest.WriteAck(s.opts.StateDir, req.ID, req.Action, uirequest.Ack{Instance: v.id, Host: uirequest.HostName(), PID: s.endpoint.PID, Status: uirequest.StatusOpened})
				return
			}
		}
		return
	}
	var n notification.Notification
	if json.Unmarshal(req.Payload, &n) != nil || n.ID == "" {
		return
	}
	_, err = store.Post(n)
	if err != nil {
		return
	}
	if v.notifications == nil {
		v.notifications = map[string]uirequest.Request{}
	}
	if len(v.notifications) < 128 {
		v.notifications[req.ID] = req
	}
}

// Project notification locators from their producing workspace. Public catalog
// selectors are copied unchanged; origin paths never select the reader's project.
func (s *Server) projectNotificationTargets(all []notification.Notification) {
	needed := false
	for _, n := range all {
		if len(n.Targets) > 0 || n.Transition != nil && n.Origin.TmuxSession != "" {
			needed = true
			break
		}
	}
	if !needed {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		for i := range all {
			all[i].Targets = append([]notification.Target(nil), all[i].Targets...)
			for j := range all[i].Targets {
				target := &all[i].Targets[j]
				if target.Kind == notification.TargetFile || target.Kind == notification.TargetIssue || target.Kind == notification.TargetCommit {
					target.RoutingError = "The notification owning workspace configuration is unavailable."
				}
			}
		}
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	showIdle := true
	catalog, catalogErr := s.opts.Backend.Sessions(ctx, mobileproto.CatalogQuery{Sort: "project", ShowIdleSessions: &showIdle})
	var rows []mobileproto.CatalogRow
	for _, section := range catalog.Sections {
		rows = append(rows, section.Rows...)
	}
	for i := range all {
		n := &all[i]
		n.Targets = append([]notification.Target(nil), n.Targets...)
		if len(n.Targets) == 0 && n.Transition != nil && n.Origin.TmuxSession != "" {
			target := notification.Target{Kind: notification.TargetSession, Value: n.Origin.TmuxSession, RoutingError: "The notification owning session is unavailable or ambiguous."}
			var chosen *mobileproto.CatalogRow
			best := -1
			ambiguous := false
			if catalogErr == nil {
				for _, row := range rows {
					if row.Session != n.Origin.TmuxSession || n.Origin.HostID != "" && row.OwnerHostID != n.Origin.HostID || n.Origin.HostID == "" && !catalogOwnerLocal(catalog, row.OwnerHostID) {
						continue
					}
					rel, err := filepath.Rel(row.Path, n.Origin.WorkDir)
					if row.Path == "" || n.Origin.WorkDir == "" || err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
						continue
					}
					if len(row.Path) > best {
						copy := row
						chosen = &copy
						best = len(row.Path)
						ambiguous = false
					} else if len(row.Path) == best && chosen != nil && chosen.ID != row.ID {
						ambiguous = true
					}
				}
			}
			if chosen != nil && !ambiguous {
				target.Value = chosen.ID
				target.Project = chosen.ProjectName
				target.Workspace = chosen.ContentWorkspaceID
				target.RoutingError = ""
			}
			n.Targets = append(n.Targets, target)
		}
		for j := range n.Targets {
			target := &n.Targets[j]
			if target.Kind != notification.TargetFile && target.Kind != notification.TargetIssue && target.Kind != notification.TargetCommit {
				continue
			}
			project, workspace, root := "", "", ""
			if target.Project != "" {
				for _, p := range cfg.Projects.List {
					if p.Name == target.Project || filepath.Base(p.Path) == target.Project || filepath.IsAbs(target.Project) && filepath.Clean(p.Path) == filepath.Clean(target.Project) {
						if project != "" {
							project = ""
							break
						}
						project = p.Name
						root = p.Path
					}
				}
			} else if catalogErr == nil {
				best := -1
				ambiguous := false
				for _, row := range rows {
					if n.Origin.HostID != "" && n.Origin.HostID != row.OwnerHostID {
						continue
					}
					if n.Origin.HostID == "" && !catalogOwnerLocal(catalog, row.OwnerHostID) {
						continue
					}
					if n.Origin.TmuxSession != "" && n.Origin.TmuxSession != row.Session {
						continue
					}
					if row.Path == "" || n.Origin.WorkDir == "" {
						continue
					}
					rel, err := filepath.Rel(filepath.Clean(row.Path), filepath.Clean(n.Origin.WorkDir))
					if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
						continue
					}
					if len(row.Path) == best && (project != row.ProjectName || workspace != row.ContentWorkspaceID) {
						ambiguous = true
					}
					if len(row.Path) > best {
						ambiguous = false
						best = len(row.Path)
						project = row.ProjectName
						workspace = row.ContentWorkspaceID
						root = row.Path
					}
				}
				if ambiguous {
					target.RoutingError = "The notification origin matches multiple workspaces; choose an explicit project target."
					continue
				}
				if project == "" && n.Origin.HostID == "" && n.Origin.TmuxSession == "" {
					for _, p := range cfg.Projects.List {
						rel, err := filepath.Rel(p.Path, n.Origin.WorkDir)
						if err == nil && n.Origin.WorkDir != "" && rel != ".." && !strings.HasPrefix(rel, "../") && len(p.Path) > len(root) {
							project = p.Name
							root = p.Path
						}
					}
				}
			}
			if project == "" {
				target.RoutingError = "The notification's owning workspace is unavailable; choose an explicit project target."
				continue
			}
			ws, err := s.contentBackend().LookupProject(ctx, project, workspace)
			if err != nil {
				target.RoutingError = "The notification's owning workspace is no longer available."
				continue
			}
			if target.Project == "" && filepath.Clean(ws.Root) != filepath.Clean(root) {
				target.RoutingError = "The notification owning workspace no longer matches its origin."
				continue
			}
			if target.Project == "" && target.Kind == notification.TargetFile && n.Origin.WorkDir != "" {
				path := target.Value
				if !filepath.IsAbs(path) {
					path = filepath.Join(n.Origin.WorkDir, path)
				}
				rel, err := filepath.Rel(ws.Root, path)
				if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
					target.RoutingError = "The file target leaves its owning workspace."
					continue
				}
				target.Value = filepath.ToSlash(rel)
			}
			target.Project = project
			target.Workspace = workspace
			target.RoutingError = ""
		}
	}
}
func catalogOwnerLocal(catalog mobileproto.CatalogSnapshot, id string) bool {
	for _, host := range catalog.Hosts {
		if host.ID == id {
			return host.Local
		}
	}
	return id == ""
}
