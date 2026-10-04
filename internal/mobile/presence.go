package mobile

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
)

func (s *Service) advertisedCapabilities() mobileproto.Capabilities {
	if s.capabilitiesRequested {
		return mobileproto.SupportedCapabilities()
	}
	return mobileproto.DefaultCapabilities()
}

// Presence and input run under the same attachment operation lock as v0.
func (a *attachment) ensurePresenceGeometry() (LeaseGeometry, error) {
	if a.presenceGeometry != nil {
		return a.presenceGeometry, nil
	}
	t := a.target.resolved
	g, err := a.service.terminalBackend.Geometry(tty.HeadlessTargetIdentity{ServerPID: t.ServerPID, SessionID: t.SessionID, SessionCreated: t.SessionCreated, Session: t.Session, Pane: t.Pane, Width: t.Width, Height: t.Height, PaneCount: t.PaneCount}, a.ownerID)
	if err != nil {
		return nil, err
	}
	if err = g.SetHolderLabel(a.service.viewer.Kind, a.service.viewer.Label); err != nil {
		return nil, err
	}
	a.presenceGeometry = g
	return g, nil
}

func (s *Service) presence(ctx context.Context, r mobileproto.Request) {
	if !s.clientCaps.Presence {
		s.writeError(r.RequestID, mobileproto.ErrorUnsupported, "negotiate presence in hello", false)
		return
	}
	a, ok := s.operationAttachment(ctx, r, false)
	if !ok {
		return
	}
	defer a.opMu.Unlock()
	p := r.Presence
	if r.Type == mobileproto.RequestHeartbeat && p == nil && a.presenceState != nil {
		copy := *a.presenceState
		copy.IdleMS = min(int64(86400000), copy.IdleMS+time.Since(a.presenceAt).Milliseconds())
		p = &copy
	}
	if err := mobileproto.ValidatePresence(p); err != nil {
		s.writeError(r.RequestID, mobileproto.ErrorInvalidRequest, err.Error(), false)
		return
	}
	a.mu.Lock()
	inputKnown := a.latest.InputModesKnown
	a.mu.Unlock()
	if p.Focused && p.Visible && !inputKnown {
		s.writeError(r.RequestID, mobileproto.ErrorUnsupportedMode, "tmux did not expose required input modes", false)
		return
	}
	g, err := a.ensurePresenceGeometry()
	if err != nil {
		s.writeError(r.RequestID, mobileproto.ErrorBackend, err.Error(), true)
		return
	}
	owned, err := g.Presence(p.Focused, p.Visible, time.Duration(p.IdleMS)*time.Millisecond, p.Columns, p.Rows, false)
	if err != nil {
		a.loseControlLocked()
		s.writeError(r.RequestID, mobileproto.ErrorLease, err.Error(), true)
		return
	}
	copy := *p
	a.presenceState = &copy
	a.presenceAt = time.Now()
	a.mu.Lock()
	a.control = owned
	if owned {
		a.geometry = g
	} else {
		a.geometry = nil
	}
	a.operationSequence = r.OperationSequence
	resized := owned && (a.latest.PaneWidth != p.Columns || a.latest.PaneHeight != p.Rows)
	if resized {
		a.resetGeneration++
		a.firstOutputForReset = 0
		a.awaitGeometryLocked(p.Columns, p.Rows)
	}
	reset, output := a.resetGeneration, a.outputSequence
	a.mu.Unlock()
	typeName := mobileproto.ResponsePresence
	if r.Type == mobileproto.RequestHeartbeat {
		typeName = mobileproto.ResponseHeartbeat
	}
	if !s.emit(mobileproto.Response{Version: mobileproto.Version, Type: typeName, RequestID: r.RequestID, AttachmentHandle: a.handle, AttachmentGeneration: a.generation, OperationSequence: r.OperationSequence, Control: owned, ResetGeneration: reset, OutputSequence: output}) {
		a.loseControlLocked()
		return
	}
	if resized {
		s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseReset, AttachmentHandle: a.handle, AttachmentGeneration: a.generation, ResetGeneration: reset, Reason: mobileproto.ResetResize})
		a.requestSnapshot()
	}
	a.updateHolderLocked()
}

func (s *Service) v1Input(ctx context.Context, r mobileproto.Request) {
	a, ok := s.operationAttachment(ctx, r, false)
	if !ok {
		return
	}
	defer a.opMu.Unlock()
	data, err := base64.StdEncoding.Strict().DecodeString(r.DataBase64)
	if err != nil || len(data) == 0 || len(data) > mobileproto.MaxInputBytes {
		s.writeError(r.RequestID, mobileproto.ErrorInvalidRequest, "input must be valid non-empty base64 within the advertised bound", false)
		return
	}
	if r.Type == mobileproto.RequestPaste {
		data = tty.NormalizeHeadlessPaste(data)
		if len(data) == 0 {
			s.writeError(r.RequestID, mobileproto.ErrorInvalidRequest, "paste contains no bytes after removing bracketed-paste markers", false)
			return
		}
	}
	a.mu.Lock()
	snapshot := a.latest
	geometry := a.geometry
	a.mu.Unlock()
	if !snapshot.InputModesKnown {
		s.writeError(r.RequestID, mobileproto.ErrorUnsupportedMode, "tmux did not expose required input modes", false)
		return
	}
	columns, rows := snapshot.PaneWidth, snapshot.PaneHeight
	if s.clientCaps.Presence {
		g, e := a.ensurePresenceGeometry()
		if e != nil {
			s.writeError(r.RequestID, mobileproto.ErrorBackend, e.Error(), true)
			return
		}
		if a.presenceState != nil {
			columns, rows = a.presenceState.Columns, a.presenceState.Rows
		}
		err = g.ClaimInput(data, columns, rows, r.Type == mobileproto.RequestPaste)
		geometry = g
	} else {
		err = geometry.Paste(data)
	}
	if err != nil {
		a.loseControlLocked()
		s.writeError(r.RequestID, mobileproto.ErrorLease, err.Error(), true)
		return
	}
	if a.presenceState != nil {
		a.presenceState.IdleMS = 0
		a.presenceAt = time.Now()
	}
	a.mu.Lock()
	a.geometry = geometry
	a.control = true
	a.operationSequence = r.OperationSequence
	resized := snapshot.PaneWidth != columns || snapshot.PaneHeight != rows
	if resized {
		a.resetGeneration++
		a.firstOutputForReset = 0
		a.awaitGeometryLocked(columns, rows)
	}
	reset, output := a.resetGeneration, a.outputSequence
	a.mu.Unlock()
	if !s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseAccepted, RequestID: r.RequestID, AttachmentHandle: a.handle, AttachmentGeneration: a.generation, OperationSequence: r.OperationSequence, Control: true, OutputSequence: output, ResetGeneration: reset}) {
		a.loseControlLocked()
		return
	}
	if resized {
		s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseReset, AttachmentHandle: a.handle, AttachmentGeneration: a.generation, ResetGeneration: reset, Reason: mobileproto.ResetResize})
		a.requestSnapshot()
	}
	a.updateHolderLocked()
}

// Holder observations are advisory and never give mutation authority. Polling
// the small owner option on the existing actor also covers otherwise idle panes.
func (a *attachment) updateHolderLocked() {
	if !a.service.clientCaps.HolderLabels {
		return
	}
	g, err := a.ensurePresenceGeometry()
	if err != nil {
		return
	}
	kind, label, err := g.Holder()
	if err != nil {
		return
	}
	next := mobileproto.Holder{Kind: kind, Label: label}
	if next == a.holder {
		return
	}
	a.holder = next
	a.service.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseHolder, AttachmentHandle: a.handle, AttachmentGeneration: a.generation, Holder: &next})
}
