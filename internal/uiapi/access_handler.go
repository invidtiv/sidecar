package uiapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	notification "github.com/marcus/sidecar/internal/notify"
)

const (
	accessRequestsPath = "/api/v0/pairing/requests"
	accessRequestPath  = accessRequestsPath + "/{id}"
	accessApprovePath  = accessRequestsPath + "/approve"
	accessDenyPath     = accessRequestsPath + "/deny"
	devicesPath        = "/api/v0/pairing/sessions"
	devicePath         = devicesPath + "/{id}"
	maxPairingItemID   = 128
)

// pairingItemRoute maps /api/v0/pairing/requests/<id> and
// /api/v0/pairing/sessions/<id> to their route templates. The exact approve
// and deny routes are matched before this.
func pairingItemRoute(path string) (template, id string) {
	for _, prefix := range []string{accessRequestsPath + "/", devicesPath + "/"} {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok || !validPairingItemID(rest) {
			continue
		}
		return prefix + "{id}", rest
	}
	return "", ""
}

func validPairingItemID(id string) bool {
	if id == "" || len(id) > maxPairingItemID {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// accessEvents fans a new request out to approvers' events streams. Each
// subscriber holds at most the latest request: a burst coalesces into one
// event, which tells the client to re-list.
type accessEvents struct {
	mu   sync.Mutex
	subs map[chan AccessRequestInfo]struct{}
}

func (b *accessEvents) subscribe() (<-chan AccessRequestInfo, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[chan AccessRequestInfo]struct{}{}
	}
	ch := make(chan AccessRequestInfo, 1)
	b.subs[ch] = struct{}{}
	return ch, func() { b.mu.Lock(); delete(b.subs, ch); b.mu.Unlock() }
}

func (b *accessEvents) publish(info AccessRequestInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- info:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- info:
			default:
			}
		}
	}
}

func (s *Server) requireApprover(w http.ResponseWriter, c caller) bool {
	if mayApprove(c) {
		return true
	}
	writeError(w, http.StatusForbidden, CodeApproverRefused, "Only the Sidecar TUI or CLI on this machine, a signed-in browser or an allowed tailnet login may manage browser access; a paired origin cannot.")
	return false
}

// accessRequestOrigin applies the pairing exchange's origin rule to the
// unauthenticated request routes: only this listener's own origin may ask,
// never a paired origin.
func (s *Server) accessRequestOrigin(w http.ResponseWriter, c caller, required bool) bool {
	if c.origin == "" && !required {
		return true
	}
	if !s.browserOrigins[c.origin] {
		writeError(w, http.StatusForbidden, CodeOriginRefused, "Only this server's own UI may ask for access; open Sidecar's UI from this server's address.")
		return false
	}
	return true
}

func requestAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleCreateAccessRequest(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.accessRequestOrigin(w, c, true) {
		return
	}
	var body AccessRequestCreate
	if !decodeBody(w, r, &body) {
		return
	}
	if _, err := body.PublicKey.ecdsaKey(); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	created, info, err := s.auth.createAccessRequest(c.origin, requestAddress(r), body.Label, body.PublicKey)
	if errors.Is(err, errTooManyOutstanding) {
		writeError(w, http.StatusTooManyRequests, CodeTooMany, fmt.Sprintf("Too many browsers are waiting for approval (at most %d, and %d from one address); wait for one to be approved or to expire.", maxPendingAccessRequests, maxPendingAccessPerAddress))
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, CodeBackend, err.Error())
		return
	}
	s.accessSignals.publish(info)
	s.postAccessNotification(info)
	s.scheduleAccessSweep()
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) handlePollAccessRequest(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.accessRequestOrigin(w, c, false) {
		return
	}
	_, id := pairingItemRoute(r.URL.Path)
	secret := ""
	if scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, accessPollScheme) {
		secret = strings.TrimSpace(value)
	}
	status, ok := s.auth.pollAccessRequest(id, secret, c.origin)
	if !ok {
		writeError(w, http.StatusNotFound, CodeAccessNotFound, "This access request is unknown or has been forgotten; ask for access again.")
		return
	}
	s.afterAccessChange()
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleListAccessRequests(w http.ResponseWriter, _ *http.Request, c caller) {
	if !s.requireApprover(w, c) {
		return
	}
	list := s.auth.listAccessRequests()
	s.afterAccessChange()
	writeJSON(w, http.StatusOK, AccessRequestList{Requests: list})
}

func (s *Server) handleApproveAccess(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireApprover(w, c) {
		return
	}
	var body AccessApproveRequest
	if !decodeBody(w, r, &body) {
		return
	}
	via, err := approvedVia(c, body.Surface)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	code, ok := NormalizeAccessCode(body.Code)
	if !ok {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "An access code is six letters and digits, like K7Q-4MX; type the code the new browser shows.")
		return
	}
	s.credentialMu.Lock()
	approval, err := s.auth.approveAccess(c.client, code, via)
	s.credentialMu.Unlock()
	switch {
	case errors.Is(err, errAccessAttempts):
		writeError(w, http.StatusTooManyRequests, CodeTooManyAttempts, fmt.Sprintf("Too many wrong codes (%d in a minute); wait a minute, then type the code the new browser shows.", maxAccessCodeFailures))
		return
	case errors.Is(err, errAccessCodeInvalid):
		writeError(w, http.StatusNotFound, CodeAccessCodeInvalid, "No waiting browser shows that code; check the code on the new browser, which may have expired and shown a new one.")
		return
	case err != nil:
		s.auth.logStoreError(err)
		writeError(w, http.StatusServiceUnavailable, CodeBackend, err.Error())
		return
	}
	s.afterAccessChange()
	writeJSON(w, http.StatusOK, approval)
}

func (s *Server) handleDenyAccess(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireApprover(w, c) {
		return
	}
	var body AccessDenyRequest
	if !decodeBody(w, r, &body) {
		return
	}
	denial, err := s.auth.denyAccess(body.RequestID)
	if err != nil {
		writeError(w, http.StatusNotFound, CodeAccessNotFound, "No pending access request has that id; list them with `sidecar api requests`.")
		return
	}
	s.afterAccessChange()
	writeJSON(w, http.StatusOK, denial)
}

func (s *Server) handleListDevices(w http.ResponseWriter, _ *http.Request, c caller) {
	if !s.requireApprover(w, c) {
		return
	}
	devices, err := s.auth.listDevices(c.client)
	if err != nil {
		s.auth.logStoreError(err)
		writeError(w, http.StatusServiceUnavailable, CodeBackend, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, DeviceList{Devices: devices})
}

// handleRevokeDevice signs out one browser registration as the bulk
// revocation does: its bearers get 401, its unused tickets stop working and
// its terminal and events streams close with 4401.
func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireApprover(w, c) {
		return
	}
	_, id := pairingItemRoute(r.URL.Path)
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	found, err := s.auth.revokeDevice(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeBackend, fmt.Sprintf("Could not save the device revocation: %v.", err))
		return
	}
	closed := s.clients.revoke(map[string]bool{sessionClient(id): true})
	if !found && closed == 0 {
		writeError(w, http.StatusNotFound, CodeDeviceNotFound, "No browser registration has that id; list them with `sidecar api devices`.")
		return
	}
	writeJSON(w, http.StatusOK, DeviceRevocation{ID: id, Revoked: true, TerminalsClosed: closed})
}

// The access notification. One live notification stands for every pending
// request: a burst coalesces into it, and it is withdrawn once none remain.

func (s *Server) postAccessNotification(info AccessRequestInfo) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	store, err := notification.Open(s.opts.StateDir)
	if err != nil {
		s.opts.Logf("access request notification: %v", err)
		return
	}
	defer func() { _ = store.Close() }()
	if s.accessNoteID != "" {
		if n, ok := store.Get(s.accessNoteID); ok && !n.Dismissed() {
			return
		}
	}
	who := "A browser"
	if info.Label != "" {
		who = fmt.Sprintf("A browser calling itself %q", info.Label)
	}
	n := notification.Notification{ID: notification.NewID(), Source: notification.SourceAccessRequest, Severity: notification.SeverityWarning,
		Title: "A browser is asking for access",
		Body:  fmt.Sprintf("%s at %s wants to sign in through %s. Open this notification and type the code it shows, or run sidecar api approve CODE.", who, info.Address, info.Origin)}
	result, err := store.Post(n)
	if err != nil {
		s.opts.Logf("access request notification: %v", err)
		return
	}
	s.accessNoteID = result.ID
}

// afterAccessChange withdraws the access notification once nothing is
// pending.
func (s *Server) afterAccessChange() {
	if s.auth.pendingAccessCount() == 0 {
		s.withdrawAccessNotification()
	}
}

func (s *Server) withdrawAccessNotification() {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if s.accessNoteID == "" {
		return
	}
	store, err := notification.Open(s.opts.StateDir)
	if err != nil {
		s.opts.Logf("access request notification: %v", err)
		return
	}
	defer func() { _ = store.Close() }()
	if err := store.Dismiss(s.accessNoteID); err != nil && !errors.Is(err, notification.ErrNotFound) {
		s.opts.Logf("access request notification: %v", err)
		return
	}
	s.accessNoteID = ""
}

// withdrawStaleAccessNotifications dismisses access notifications a previous
// server left behind. Requests do not survive a restart, so none of them can
// still be approved.
func (s *Server) withdrawStaleAccessNotifications() {
	if _, err := os.Stat(notification.Path(s.opts.StateDir)); err != nil {
		return
	}
	store, err := notification.Open(s.opts.StateDir)
	if err != nil {
		s.opts.Logf("access request notification: %v", err)
		return
	}
	defer func() { _ = store.Close() }()
	all, err := store.List()
	if err != nil {
		return
	}
	stale := []string{}
	for _, n := range all {
		if n.Source == notification.SourceAccessRequest && !n.Dismissed() {
			stale = append(stale, n.ID)
		}
	}
	if len(stale) > 0 {
		if _, err := store.DismissMany(stale); err != nil {
			s.opts.Logf("access request notification: %v", err)
		}
	}
}

// scheduleAccessSweep expires requests on time even when nobody polls or
// lists them, so the notification is withdrawn when the last one lapses.
func (s *Server) scheduleAccessSweep() {
	time.AfterFunc(accessRequestTTL+time.Second, s.sweepAccess)
}

func (s *Server) sweepAccess() {
	if s.ctx.Err() != nil {
		return
	}
	if s.auth.sweepAccess() == 0 {
		s.withdrawAccessNotification()
	}
}
