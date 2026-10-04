package uiapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/viewerlayout"
)

const ScopeUIControl = "ui:control"
const viewerPresencePath = "/api/v0/viewers/presence"
const viewerAckPath = "/api/v0/viewers/ack"
const viewerTTL = 15 * time.Second

type ViewerIdentity struct {
	ID         string `json:"id"`
	Capability string `json:"capability"`
}

// Viewport is CSS pixels, never terminal cells.
type Viewport = layoutreport.CSSViewport
type ViewerPresenceRequest struct {
	ViewerID    string   `json:"viewer_id"`
	Focused     bool     `json:"focused"`
	Visible     bool     `json:"visible"`
	Project     string   `json:"project"`
	Workspace   string   `json:"workspace,omitempty"`
	Session     string   `json:"session,omitempty"`
	FocusedPane int      `json:"focused_pane,omitempty"`
	Viewport    Viewport `json:"viewport"`
}
type ViewerPresenceResponse struct {
	Holder bool `json:"holder"`
}
type UIRequestEvent struct {
	ID        string            `json:"id"`
	Action    uirequest.Action  `json:"action"`
	Project   string            `json:"project"`
	Workspace string            `json:"workspace,omitempty"`
	Request   uirequest.Request `json:"request"`
	Document  LayoutDocument    `json:"document"`
	ETag      string            `json:"etag"`
	ExpiresAt time.Time         `json:"expires_at"`
}
type ViewerAckRequest struct {
	ViewerID string           `json:"viewer_id"`
	ID       string           `json:"id"`
	Status   uirequest.Status `json:"status"`
	Reason   string           `json:"reason,omitempty"`
}
type ViewerAckResponse struct {
	Document LayoutDocument `json:"document"`
	ETag     string         `json:"etag"`
}

type apiScreen struct {
	id        string
	caller    caller
	presence  ViewerPresenceRequest
	ws        contentservice.Workspace
	updated   time.Time
	focusedAt uint64
	out       chan EventMessage
	pending   map[string]*viewerPlan
}
type viewerRelay struct {
	mu      sync.Mutex
	once    sync.Once
	screens map[string]*apiScreen
	serial  uint64
	workers sync.WaitGroup
}
type viewerPlan struct {
	event  UIRequestEvent
	ack    uirequest.Ack
	root   string
	viewer string
}

func (s *Server) registerViewer(c caller) (*apiScreen, error) {
	if !s.hasScope(c, ScopeUIControl) {
		return nil, fmt.Errorf("this credential needs ui:control to receive pane requests")
	}
	s.viewer.once.Do(func() {
		watcher, err := uirequest.NewWatcher(s.opts.StateDir)
		if err != nil {
			s.viewerErr = err
			return
		}
		messages := watcher.Start()
		s.viewer.workers.Add(1)
		go func() {
			defer watcher.Stop()
			defer s.viewer.workers.Done()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case msg, ok := <-messages:
					if !ok {
						return
					}
					if m, ok := msg.(uirequest.RequestMsg); ok {
						s.relayUIRequest(m.Request)
					}
				case <-tick.C:
					s.viewer.mu.Lock()
					for _, v := range s.viewer.screens {
						for id, plan := range v.pending {
							_, err := os.Stat(uirequest.RequestPath(s.opts.StateDir, id, plan.event.Action))
							if err != nil || !plan.event.ExpiresAt.After(time.Now()) || s.holderLocked() != v {
								if err == nil {
									s.declineViewer(plan.event.Request, "the pane request expired or its viewer lost focus; retry it")
								}
								delete(v.pending, id)
							}
						}
					}
					s.publishViewerLocked()
					s.viewer.mu.Unlock()
				}
			}
		}()
	})
	if s.viewerErr != nil {
		return nil, s.viewerErr
	}
	id, err := randomToken(18)
	if err != nil {
		return nil, err
	}
	v := &apiScreen{id: "api-viewer-" + id, caller: c, out: make(chan EventMessage, 1), pending: map[string]*viewerPlan{}}
	s.viewer.mu.Lock()
	defer s.viewer.mu.Unlock()
	if s.viewer.screens == nil {
		s.viewer.screens = map[string]*apiScreen{}
	}
	s.viewer.screens[v.id] = v
	return v, nil
}
func (s *Server) removeViewer(v *apiScreen) {
	s.viewer.mu.Lock()
	defer s.viewer.mu.Unlock()
	for _, plan := range v.pending {
		s.declineViewer(plan.event.Request, "the API viewer disconnected; pane requests are never queued")
	}
	delete(s.viewer.screens, v.id)
	s.publishViewerLocked()
}
func (s *Server) holderLocked() *apiScreen {
	var winner *apiScreen
	for _, v := range s.viewer.screens {
		if v.updated.IsZero() || time.Since(v.updated) >= viewerTTL || !s.hasScope(v.caller, ScopeUIControl) {
			continue
		}
		if !v.presence.Focused || !v.presence.Visible {
			continue
		}
		if winner == nil || v.focusedAt > winner.focusedAt {
			winner = v
		}
	}
	return winner
}
func (s *Server) publishViewerLocked() {
	winner := s.holderLocked()
	focused := winner != nil
	if winner == nil {
		for _, v := range s.viewer.screens {
			if !v.updated.IsZero() && time.Since(v.updated) < viewerTTL && s.hasScope(v.caller, ScopeUIControl) && (winner == nil || v.updated.After(winner.updated)) {
				winner = v
			}
		}
	}
	record := uirequest.APIViewer{PID: os.Getpid(), Focused: focused, ExpiresAt: time.Now().Add(2 * time.Second), Capabilities: []string{uirequest.APIViewerRelay}}
	if winner != nil {
		record.Instance = winner.id
	}
	if err := uirequest.WriteAPIViewer(s.opts.StateDir, record); err != nil {
		s.opts.Logf("API viewer presence: %v", err)
	}
}
func (s *Server) handleViewerPresence(w http.ResponseWriter, r *http.Request, c caller) {
	var p ViewerPresenceRequest
	if !decodeBody(w, r, &p) {
		return
	}
	if p.Viewport.Width < 1 || p.Viewport.Width > 32768 || p.Viewport.Height < 1 || p.Viewport.Height > 32768 || p.FocusedPane < 0 {
		writeError(w, 400, CodeInvalidRequest, "Send a viewport of 1..32768 CSS pixels and a nonnegative focused_pane.")
		return
	}
	ws, err := s.contentBackend().LookupProject(r.Context(), p.Project, p.Workspace)
	if err != nil {
		writeContentError(w, err)
		return
	}
	s.viewer.mu.Lock()
	defer s.viewer.mu.Unlock()
	v := s.viewer.screens[p.ViewerID]
	if v == nil || v.caller.client != c.client {
		writeError(w, 409, "viewer_unavailable", "Open a capable events stream before reporting presence.")
		return
	}
	if p.Focused && p.Visible && (!v.presence.Focused || !v.presence.Visible || v.updated.IsZero() || time.Since(v.updated) >= viewerTTL) {
		s.viewer.serial++
		v.focusedAt = s.viewer.serial
	}
	v.presence = p
	v.ws = ws
	v.updated = time.Now()
	for id, plan := range v.pending {
		if s.holderLocked() != v || plan.root != ws.Root || plan.event.Workspace != p.Workspace || plan.event.Request.Origin.TmuxSession != "" && plan.event.Request.Origin.TmuxSession != p.Session {
			s.declineViewer(plan.event.Request, "the origin shell is not on screen, and relayed open/layout requests are never queued")
			delete(v.pending, id)
		}
	}
	s.publishViewerLocked()
	writeJSON(w, 200, ViewerPresenceResponse{Holder: s.holderLocked() == v})
}
func (s *Server) declineViewer(req uirequest.Request, reason string) {
	_ = uirequest.WriteAck(s.opts.StateDir, req.ID, req.Action, uirequest.Ack{Instance: req.Viewer, Host: uirequest.HostName(), PID: os.Getpid(), Status: uirequest.StatusDeclined, Reason: reason, Surface: "browser"})
}
func (s *Server) relayUIRequest(req uirequest.Request) {
	if req.Viewer == "" || (req.Action != uirequest.ActionOpen && req.Action != uirequest.ActionLayout) {
		return
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.viewer.mu.Lock()
	defer s.viewer.mu.Unlock()
	v := s.viewer.screens[req.Viewer]
	if v == nil || s.holderLocked() != v || !s.callerLive(v.caller) {
		s.declineViewer(req, "the API viewer is not focused and visible; pane requests are never queued")
		return
	}
	originRoot, _ := filepath.EvalSymlinks(req.Origin.WorkDir)
	if req.Origin.HostID != "" || originRoot != v.ws.Root || req.Origin.TmuxSession != "" && req.Origin.TmuxSession != v.presence.Session {
		s.declineViewer(req, "the origin shell is not on screen, and relayed open/layout requests are never queued")
		return
	}
	if len(v.pending) != 0 {
		s.declineViewer(req, "the API viewer has an unacknowledged pane request; retry after it finishes")
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	plan, err := s.planViewerRequest(ctx, v, req)
	if err != nil {
		s.declineViewer(req, err.Error())
		return
	}
	if !plan.event.ExpiresAt.After(time.Now()) {
		s.declineViewer(req, "the pane request expired; retry it")
		return
	}
	v.pending[req.ID] = plan
	select {
	case v.out <- EventMessage{Type: "ui_request", UIRequest: &plan.event}:
	default:
		delete(v.pending, req.ID)
		s.declineViewer(req, "the API viewer cannot receive pane requests while its events stream is busy")
	}
}
func (s *Server) handleViewerAck(w http.ResponseWriter, r *http.Request, c caller) {
	var a ViewerAckRequest
	if !decodeBody(w, r, &a) {
		return
	}
	if a.Status != uirequest.StatusOpened && a.Status != uirequest.StatusDeclined {
		writeError(w, 400, CodeInvalidRequest, "Acknowledge with opened or declined.")
		return
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.viewer.mu.Lock()
	defer s.viewer.mu.Unlock()
	v := s.viewer.screens[a.ViewerID]
	if v == nil || v.caller.client != c.client || !s.callerLive(v.caller) {
		writeError(w, 409, "viewer_unavailable", "The viewer disconnected; reconnect without replaying the request.")
		return
	}
	plan := v.pending[a.ID]
	if plan == nil {
		writeError(w, 409, "request_unavailable", "The request is unknown or already acknowledged; do not replay it.")
		return
	}
	delete(v.pending, a.ID)
	if _, err := os.Stat(uirequest.RequestPath(s.opts.StateDir, a.ID, plan.event.Action)); err != nil || !plan.event.ExpiresAt.After(time.Now()) || s.holderLocked() != v {
		s.declineViewer(plan.event.Request, "the pane request expired or its viewer lost focus; retry it")
		writeError(w, 409, "request_unavailable", "The request expired or the viewer lost focus; do not replay it.")
		return
	}
	if a.Status == uirequest.StatusDeclined {
		s.declineViewer(plan.event.Request, a.Reason)
		writeJSON(w, 200, ViewerAckResponse{Document: plan.event.Document, ETag: plan.event.ETag})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	host := &viewerLayoutHost{s: s, ctx: ctx, v: v}
	if err := host.validateSaved(plan.event.Document.Layout); err != nil {
		s.declineViewer(plan.event.Request, err.Error())
		writeError(w, 409, "content_changed", err.Error())
		return
	}
	store := viewerlayout.FileStore{Dir: filepath.Join(s.dir, "layouts")}
	doc, etag, err := store.Put(c.client, plan.root, plan.event.ETag, plan.event.Document)
	if err != nil {
		s.declineViewer(plan.event.Request, "the viewer layout changed; read it again before retrying")
		writeError(w, 409, "layout_changed", err.Error())
		return
	}
	if err := uirequest.WriteAck(s.opts.StateDir, a.ID, plan.event.Action, plan.ack); err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, 200, ViewerAckResponse{Document: doc, ETag: etag})
}
