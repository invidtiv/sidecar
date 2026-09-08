// Package mobile implements Sidecar's bounded headless terminal service.
package mobile

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
)

const (
	maxResolvedTargets = 64
	maxAttachments     = 8
	recentRequestIDs   = 256
)

type ResolvedTarget struct {
	WorkspaceID, WorkspaceKind, ProjectRoot, Session, Pane, DisplayName string
	ServerPID                                                           int
	SessionID, SessionCreated, DurableSessionCreated                    string
	Width, Height                                                       int
}

type ResolveError struct{ Code, Message string }

func (e *ResolveError) Error() string { return e.Message }

type Resolver func(context.Context, string) (ResolvedTarget, error)
type HistoryCapturer func(target string, start, end, maxBytes int) (tty.CaptureRange, error)
type OwnerConfigGenerationProvider func(context.Context) (string, error)

type Config struct {
	Input                                     io.Reader
	Output                                    io.Writer
	Resolver                                  Resolver
	Catalog                                   CatalogProvider
	HistoryCapturer                           HistoryCapturer
	OwnerConfigGenerationProvider             OwnerConfigGenerationProvider
	HubID, OwnerHostID, OwnerConfigGeneration string
	Manager                                   *tty.ControlManager
}

type Service struct {
	in                                             io.Reader
	out                                            *safeEncoder
	resolve                                        Resolver
	catalog                                        CatalogProvider
	historyCapture                                 HistoryCapturer
	ownerConfigGeneration                          OwnerConfigGenerationProvider
	manager                                        *tty.ControlManager
	instance, hubID, ownerHostID, configGeneration string
	mu                                             sync.Mutex
	targets                                        map[string]targetState
	attachments                                    map[string]*attachment
	seenRequests                                   map[string]struct{}
	requestOrder                                   []string
	terminal                                       chan struct{}
	abortOnce                                      sync.Once
	closeOnce                                      sync.Once
	closeDone                                      chan struct{}
}

type targetState struct {
	wire     mobileproto.Target
	resolved ResolvedTarget
}

type safeEncoder struct {
	mu      sync.Mutex
	enc     *json.Encoder
	queue   chan mobileproto.Response
	err     error
	done    chan struct{}
	once    sync.Once
	onError func(error)
}

func (e *safeEncoder) write(response mobileproto.Response) error {
	if e.queue != nil {
		e.mu.Lock()
		err := e.err
		e.mu.Unlock()
		if err != nil {
			return err
		}
		select {
		case e.queue <- response:
			return nil
		default:
			return fmt.Errorf("mobile service: outbound queue overflow")
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enc.Encode(response)
}

func newSafeEncoder(output io.Writer) *safeEncoder {
	e := &safeEncoder{enc: json.NewEncoder(output), queue: make(chan mobileproto.Response, mobileproto.OutboundQueueDepth), done: make(chan struct{})}
	go func() {
		defer close(e.done)
		for response := range e.queue {
			if err := e.enc.Encode(response); err != nil {
				e.mu.Lock()
				e.err = err
				e.mu.Unlock()
				if e.onError != nil {
					e.onError(err)
				}
				return
			}
		}
	}()
	return e
}

func (e *safeEncoder) close() {
	if e.queue == nil {
		return
	}
	e.once.Do(func() { close(e.queue) })
	select {
	case <-e.done:
	case <-time.After(500 * time.Millisecond):
	}
}

func New(config Config) (*Service, error) {
	if config.Input == nil || config.Output == nil || config.Resolver == nil {
		return nil, fmt.Errorf("mobile service: input, output and resolver are required")
	}
	if config.Manager == nil {
		config.Manager = tty.NewControlManager()
	}
	instance, err := randomHandle("api")
	if err != nil {
		return nil, err
	}
	historyCapture := config.HistoryCapturer
	if historyCapture == nil {
		historyCapture = tty.CapturePaneRangeBounded
	}
	s := &Service{
		in: config.Input, out: newSafeEncoder(config.Output), resolve: config.Resolver, catalog: config.Catalog,
		historyCapture:        historyCapture,
		ownerConfigGeneration: config.OwnerConfigGenerationProvider,
		manager:               config.Manager, instance: instance, hubID: config.HubID,
		ownerHostID: config.OwnerHostID, configGeneration: config.OwnerConfigGeneration,
		targets: make(map[string]targetState), attachments: make(map[string]*attachment),
		seenRequests: make(map[string]struct{}),
		terminal:     make(chan struct{}),
		closeDone:    make(chan struct{}),
	}
	s.out.onError = s.abort
	return s, nil
}

func (s *Service) Run(ctx context.Context) error {
	defer func() {
		s.closeAll()
		s.out.close()
	}()
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 64<<10), mobileproto.MaxLineBytes)
	lines := make(chan []byte)
	scanDone := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-s.terminal:
				return
			}
		}
		scanDone <- scanner.Err()
	}()
	handshake := false
	for {
		var line []byte
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.terminal:
			return fmt.Errorf("mobile service: outbound transport unavailable")
		case err := <-scanDone:
			if err != nil {
				return fmt.Errorf("mobile service: read JSONL: %w", err)
			}
			return nil
		case line = <-lines:
		}
		request, err := decodeRequest(line)
		if err != nil {
			s.writeError("", mobileproto.ErrorInvalidRequest, err.Error(), false)
			continue
		}
		if request.Version != mobileproto.Version {
			s.writeError(request.RequestID, mobileproto.ErrorProtocolMismatch, fmt.Sprintf("protocol version %d is unsupported; expected %d", request.Version, mobileproto.Version), false)
			return nil
		}
		if err := s.validateRequestID(request.RequestID); err != nil {
			s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, err.Error(), false)
			continue
		}
		if !handshake && request.Type != mobileproto.RequestHello {
			s.writeError(request.RequestID, mobileproto.ErrorHandshake, "hello must be the first request", false)
			continue
		}
		if request.Type == mobileproto.RequestHello {
			if handshake {
				s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "hello was already accepted", false)
				continue
			}
			handshake = true
			caps := mobileproto.DefaultCapabilities()
			s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseHello, RequestID: request.RequestID, APIInstance: s.instance, Capabilities: &caps})
			continue
		}
		s.handle(ctx, request)
	}
}

func decodeRequest(line []byte) (mobileproto.Request, error) {
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	var request mobileproto.Request
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("invalid JSON request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return request, fmt.Errorf("request contains trailing JSON")
		}
		return request, fmt.Errorf("invalid trailing JSON: %w", err)
	}
	var presence struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(line, &presence); err != nil || presence.Version == nil {
		return request, fmt.Errorf("version is required")
	}
	return request, nil
}

func (s *Service) validateRequestID(id string) error {
	if id == "" || len(id) > mobileproto.MaxRequestIDBytes {
		return fmt.Errorf("request_id must be 1..%d bytes", mobileproto.MaxRequestIDBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.seenRequests[id]; exists {
		return fmt.Errorf("request_id %q was already used", id)
	}
	if len(s.requestOrder) == recentRequestIDs {
		delete(s.seenRequests, s.requestOrder[0])
		s.requestOrder = s.requestOrder[1:]
	}
	s.seenRequests[id] = struct{}{}
	s.requestOrder = append(s.requestOrder, id)
	return nil
}

func (s *Service) handle(ctx context.Context, request mobileproto.Request) {
	if s.isTerminal() {
		return
	}
	switch request.Type {
	case mobileproto.RequestStatus:
		caps := mobileproto.DefaultCapabilities()
		s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseStatus, RequestID: request.RequestID, APIInstance: s.instance, Capabilities: &caps})
	case mobileproto.RequestSessions:
		s.sessions(ctx, request)
	case mobileproto.RequestHistory:
		s.history(ctx, request)
	case mobileproto.RequestResolve:
		s.resolveTarget(ctx, request)
	case mobileproto.RequestOpen:
		s.open(ctx, request, false)
	case mobileproto.RequestReconnect:
		s.open(ctx, request, true)
	case mobileproto.RequestControl:
		s.control(ctx, request)
	case mobileproto.RequestInput:
		s.inputBytes(ctx, request)
	case mobileproto.RequestResize:
		s.resize(ctx, request)
	case mobileproto.RequestHeartbeat:
		s.heartbeat(ctx, request)
	case mobileproto.RequestRelease:
		s.release(ctx, request)
	case mobileproto.RequestClose:
		s.close(request)
	default:
		s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "unknown request type", false)
	}
}

func (s *Service) sessions(ctx context.Context, request mobileproto.Request) {
	s.mu.Lock()
	activeAttachments := len(s.attachments) > 0
	s.mu.Unlock()
	if activeAttachments {
		s.writeError(request.RequestID, mobileproto.ErrorUnsupported, "catalog queries require a stream without a terminal attachment", true)
		return
	}
	query := mobileproto.CatalogQuery{}
	if request.CatalogQuery != nil {
		query = *request.CatalogQuery
	}
	configGeneration, err := s.currentOwnerConfigGeneration(ctx)
	if err != nil {
		s.resolveFailure(request.RequestID, err)
		return
	}
	snapshot, err := QueryCatalog(ctx, s.catalog, s.resolve, query, CatalogIdentity{HubID: s.hubID, OwnerHostID: s.ownerHostID, OwnerConfigGeneration: configGeneration})
	if err != nil {
		s.resolveFailure(request.RequestID, err)
		return
	}
	if err := s.requireOwnerConfigGeneration(ctx, configGeneration); err != nil {
		s.resolveFailure(request.RequestID, err)
		return
	}
	s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseSessions, RequestID: request.RequestID, APIInstance: s.instance, Catalog: &snapshot})
}

func (s *Service) resolveTarget(ctx context.Context, request mobileproto.Request) {
	if request.Target == "" || len(request.Target) > mobileproto.MaxTargetBytes {
		s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "target is required and bounded", false)
		return
	}
	s.mu.Lock()
	full := len(s.targets) >= maxResolvedTargets
	s.mu.Unlock()
	if full {
		s.writeError(request.RequestID, mobileproto.ErrorOverflow, "too many resolved target handles", true)
		return
	}
	handle, err := randomHandle("target")
	if err != nil {
		s.writeError(request.RequestID, mobileproto.ErrorBackend, err.Error(), true)
		return
	}
	resolved, wire, err := s.resolveWireTarget(ctx, request.Target, handle)
	if err != nil {
		s.resolveFailure(request.RequestID, err)
		return
	}
	if request.ExpectedTarget != nil && wire.Identity() != *request.ExpectedTarget {
		s.writeError(request.RequestID, mobileproto.ErrorIdentityChanged, "managed terminal identity changed after catalog observation", false)
		return
	}
	s.mu.Lock()
	s.targets[handle] = targetState{wire: wire, resolved: resolved}
	s.mu.Unlock()
	s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseResolved, RequestID: request.RequestID, Target: &wire})
}

func (s *Service) wireTarget(handle string, resolved ResolvedTarget, configGeneration string) mobileproto.Target {
	identity := targetIdentity(CatalogIdentity{HubID: s.hubID, OwnerHostID: s.ownerHostID, OwnerConfigGeneration: configGeneration}, resolved)
	return mobileproto.Target{
		Handle: handle, HubID: identity.HubID, HubInstance: s.instance, OwnerHostID: identity.OwnerHostID,
		OwnerConfigGeneration: identity.OwnerConfigGeneration, WorkspaceID: identity.WorkspaceID,
		WorkspaceKind: identity.WorkspaceKind, Session: identity.Session, Pane: identity.Pane,
		ServerIncarnation: identity.ServerIncarnation, TargetGeneration: identity.TargetGeneration,
		DisplayName: resolved.DisplayName, Geometry: mobileproto.Geometry{Columns: resolved.Width, Rows: resolved.Height},
	}
}

func (s *Service) currentOwnerConfigGeneration(ctx context.Context) (string, error) {
	if s.ownerConfigGeneration == nil {
		if s.configGeneration == "" {
			return "", fmt.Errorf("owner configuration generation is unavailable")
		}
		return s.configGeneration, nil
	}
	generation, err := s.ownerConfigGeneration(ctx)
	if err != nil {
		return "", fmt.Errorf("read current owner configuration generation: %w", err)
	}
	if generation == "" {
		return "", fmt.Errorf("current owner configuration generation is empty")
	}
	return generation, nil
}

func (s *Service) requireOwnerConfigGeneration(ctx context.Context, expected string) error {
	current, err := s.currentOwnerConfigGeneration(ctx)
	if err != nil {
		return err
	}
	if current != expected {
		return &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "owner configuration changed"}
	}
	return nil
}

func (s *Service) resolveWireTarget(ctx context.Context, selector, handle string) (ResolvedTarget, mobileproto.Target, error) {
	configGeneration, err := s.currentOwnerConfigGeneration(ctx)
	if err != nil {
		return ResolvedTarget{}, mobileproto.Target{}, err
	}
	resolved, err := s.resolve(ctx, selector)
	if err != nil {
		return ResolvedTarget{}, mobileproto.Target{}, err
	}
	if err := s.requireOwnerConfigGeneration(ctx, configGeneration); err != nil {
		return ResolvedTarget{}, mobileproto.Target{}, err
	}
	return resolved, s.wireTarget(handle, resolved, configGeneration), nil
}

func targetIdentity(identity CatalogIdentity, resolved ResolvedTarget) mobileproto.TargetIdentity {
	incarnation := fmt.Sprintf("pid=%d", resolved.ServerPID)
	generationBytes := sha256.Sum256([]byte(strings.Join([]string{resolved.WorkspaceID, resolved.WorkspaceKind, resolved.Session, resolved.Pane, resolved.SessionID, resolved.SessionCreated, resolved.DurableSessionCreated, incarnation}, "\x00")))
	return mobileproto.TargetIdentity{
		HubID: identity.HubID, OwnerHostID: identity.OwnerHostID, OwnerConfigGeneration: identity.OwnerConfigGeneration,
		WorkspaceID: resolved.WorkspaceID, WorkspaceKind: resolved.WorkspaceKind, Session: resolved.Session, Pane: resolved.Pane,
		ServerIncarnation: incarnation, TargetGeneration: hex.EncodeToString(generationBytes[:16]),
	}
}

func (s *Service) target(handle string) (targetState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.targets[handle]
	return t, ok
}

func (s *Service) revalidate(ctx context.Context, target targetState) error {
	_, err := s.revalidatedTarget(ctx, target)
	return err
}

func (s *Service) revalidatedTarget(ctx context.Context, target targetState) (ResolvedTarget, error) {
	expectedConfig := target.wire.OwnerConfigGeneration
	if expectedConfig == "" {
		expectedConfig = s.configGeneration
	}
	if err := s.requireOwnerConfigGeneration(ctx, expectedConfig); err != nil {
		return ResolvedTarget{}, err
	}
	current, err := s.resolve(ctx, target.resolved.Session)
	if err != nil {
		return ResolvedTarget{}, err
	}
	want, got := target.resolved, current
	if want.WorkspaceID != got.WorkspaceID || want.WorkspaceKind != got.WorkspaceKind || want.ProjectRoot != got.ProjectRoot || want.Session != got.Session ||
		want.Pane != got.Pane || want.ServerPID != got.ServerPID || want.SessionID != got.SessionID || want.SessionCreated != got.SessionCreated {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "managed terminal identity changed"}
	}
	if want.DurableSessionCreated == "" || want.DurableSessionCreated != got.DurableSessionCreated {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "managed terminal identity changed"}
	}
	if err := s.requireOwnerConfigGeneration(ctx, expectedConfig); err != nil {
		return ResolvedTarget{}, err
	}
	return current, nil
}

func (s *Service) history(ctx context.Context, request mobileproto.Request) {
	if request.HistoryRows < 1 || request.HistoryRows > mobileproto.MaxHistoryRows {
		s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, fmt.Sprintf("history_rows must be 1..%d", mobileproto.MaxHistoryRows), false)
		return
	}
	if request.Columns < 2 || request.Columns > mobileproto.MaxColumns || request.Rows < 1 || request.Rows > mobileproto.MaxRows {
		s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "history geometry is invalid", false)
		return
	}
	a, ok := s.attachment(request.AttachmentHandle)
	if !ok {
		s.writeError(request.RequestID, mobileproto.ErrorAttachment, "unknown attachment", false)
		return
	}
	current, err := s.revalidatedTarget(ctx, a.target)
	if err != nil {
		s.resolveFailure(request.RequestID, err)
		return
	}
	if current.Width != request.Columns || current.Height != request.Rows {
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, "history geometry does not match the current target", true)
		return
	}
	a.mu.Lock()
	reset, output, first := a.resetGeneration, a.outputSequence, a.firstOutputForReset
	latest := a.latest
	a.mu.Unlock()
	if request.LastResetGeneration != reset || first == 0 || request.LastOutputSequence < first || request.LastOutputSequence > output {
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, "history checkpoint has not applied an authoritative frame for the current reset", false)
		return
	}
	if latest.PaneWidth != request.Columns || latest.PaneHeight != request.Rows {
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, "history geometry does not match the applied frame", true)
		return
	}
	if latest.AltScreen {
		s.writeError(request.RequestID, mobileproto.ErrorUnsupported, "normal-buffer history is unavailable while the alternate screen is active", true)
		return
	}
	capture, err := s.historyCapture(a.target.resolved.Pane, -request.HistoryRows, request.Rows-1, mobileproto.MaxHistoryBytes)
	if err != nil {
		if errors.Is(err, tty.ErrCaptureRangeTooLarge) {
			s.writeError(request.RequestID, mobileproto.ErrorOverflow, err.Error(), false)
			return
		}
		s.writeError(request.RequestID, mobileproto.ErrorBackend, err.Error(), true)
		return
	}
	if s.isTerminal() {
		return
	}
	if currentAttachment, attached := s.attachment(a.handle); !attached || currentAttachment != a {
		s.writeError(request.RequestID, mobileproto.ErrorAttachment, "attachment closed during history capture", false)
		return
	}
	current, err = s.revalidatedTarget(ctx, a.target)
	if err != nil {
		s.resolveFailure(request.RequestID, err)
		return
	}
	if current.Width != request.Columns || current.Height != request.Rows {
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, "target geometry changed during history capture", true)
		return
	}
	a.mu.Lock()
	stable := a.resetGeneration == reset && a.latest.PaneWidth == request.Columns && a.latest.PaneHeight == request.Rows && !a.latest.AltScreen
	a.mu.Unlock()
	if !stable {
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, "terminal state changed during history capture", true)
		return
	}
	want := a.target.resolved
	if capture.Pane != want.Pane || capture.Session != want.Session || capture.ServerPID != want.ServerPID ||
		capture.SessionID != want.SessionID || capture.SessionCreated != want.SessionCreated {
		s.writeError(request.RequestID, mobileproto.ErrorIdentityChanged, "history capture target identity changed", false)
		return
	}
	if capture.PaneWidth != request.Columns || capture.PaneHeight != request.Rows || capture.AltScreen {
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, "history capture geometry or screen changed", true)
		return
	}
	totalRows := capture.EndLine - capture.StartLine
	historyRows := totalRows - capture.PaneHeight
	if historyRows < 0 || historyRows > request.HistoryRows || capture.StartLine+historyRows != capture.HistorySize {
		s.writeError(request.RequestID, mobileproto.ErrorBackend, "history capture row bounds are inconsistent", true)
		return
	}
	snapshot := tty.ControlSnapshot{Output: capture.Output, HistorySize: capture.HistorySize, CaptureBase: capture.StartLine,
		HistoryRows: historyRows, PaneRows: capture.PaneHeight, HasHistory: true, PaneWidth: capture.PaneWidth, PaneHeight: capture.PaneHeight}
	vt, err := normalizedHistorySnapshot(snapshot)
	if err != nil {
		s.writeError(request.RequestID, mobileproto.ErrorBackend, err.Error(), true)
		return
	}
	if len(vt) > mobileproto.MaxHistoryBytes {
		s.writeError(request.RequestID, mobileproto.ErrorOverflow, "normalized history snapshot exceeds the advertised byte bound", false)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(vt)
	if len(encoded) > mobileproto.MaxLineBytes-(64<<10) {
		s.writeError(request.RequestID, mobileproto.ErrorOverflow, "encoded history snapshot exceeds the JSONL line bound", false)
		return
	}
	geometry := mobileproto.Geometry{Columns: capture.PaneWidth, Rows: capture.PaneHeight}
	snapshotWire := mobileproto.HistorySnapshot{HistorySize: capture.HistorySize, HistoryRows: historyRows,
		StartLine: capture.StartLine, EndLine: capture.HistorySize, AtOldest: capture.StartLine == 0, RenderVTBase64: encoded}
	s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseHistory, RequestID: request.RequestID,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, OutputSequence: request.LastOutputSequence,
		ResetGeneration: reset, Geometry: &geometry, History: &snapshotWire})
}

func (s *Service) open(ctx context.Context, request mobileproto.Request, reconnect bool) {
	if request.AttachmentID == "" || len(request.AttachmentID) > mobileproto.MaxAttachmentIDBytes {
		s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "attachment_id is required and bounded", false)
		return
	}
	var target targetState
	generation := uint64(1)
	s.mu.Lock()
	attachmentsFull := len(s.attachments) >= maxAttachments
	targetsFull := len(s.targets) >= maxResolvedTargets
	s.mu.Unlock()
	if attachmentsFull {
		s.writeError(request.RequestID, mobileproto.ErrorOverflow, "too many live attachments", true)
		return
	}
	if reconnect {
		if request.Target == "" || request.ExpectedTarget == nil || request.PreviousAttachmentGeneration == 0 {
			s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "reconnect requires target, expected_target and previous_attachment_generation", false)
			return
		}
		if request.PreviousAttachmentGeneration == ^uint64(0) {
			s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "previous_attachment_generation cannot advance", false)
			return
		}
		if targetsFull {
			s.writeError(request.RequestID, mobileproto.ErrorOverflow, "too many resolved target handles", true)
			return
		}
		targetHandle, err := randomHandle("target")
		if err != nil {
			s.writeError(request.RequestID, mobileproto.ErrorBackend, err.Error(), true)
			return
		}
		resolved, wire, err := s.resolveWireTarget(ctx, request.Target, targetHandle)
		if err != nil {
			s.resolveFailure(request.RequestID, err)
			return
		}
		if wire.Identity() != *request.ExpectedTarget {
			s.writeError(request.RequestID, mobileproto.ErrorIdentityChanged, "reconnect target identity changed", false)
			return
		}
		target = targetState{wire: wire, resolved: resolved}
		s.mu.Lock()
		s.targets[targetHandle] = target
		s.mu.Unlock()
		generation = request.PreviousAttachmentGeneration + 1
	} else {
		var ok bool
		target, ok = s.target(request.TargetHandle)
		if !ok {
			s.writeError(request.RequestID, mobileproto.ErrorNotFound, "unknown target_handle", false)
			return
		}
		if err := s.revalidate(ctx, target); err != nil {
			s.resolveFailure(request.RequestID, err)
			return
		}
	}
	handle, err := randomHandle("attachment")
	if err != nil {
		s.writeError(request.RequestID, mobileproto.ErrorBackend, err.Error(), true)
		return
	}
	a := newAttachment(s, handle, request.AttachmentID, generation, target)
	sub, err := s.manager.Subscribe(tty.ControlRequest{Session: target.resolved.Session, Pane: target.resolved.Pane, Visible: true, Focused: true, Scrollback: 1,
		FullMetadata: true, OnSnapshot: a.offerSnapshot, OnFallback: a.offerFailure})
	if err != nil {
		a.stopAttachment()
		s.writeError(request.RequestID, mobileproto.ErrorBackend, err.Error(), true)
		return
	}
	a.subscription = sub
	s.mu.Lock()
	s.attachments[handle] = a
	s.mu.Unlock()
	typeName := mobileproto.ResponseOpened
	if reconnect {
		typeName = mobileproto.ResponseReconnected
	}
	response := mobileproto.Response{Version: mobileproto.Version, Type: typeName, RequestID: request.RequestID,
		AttachmentHandle: handle, AttachmentGeneration: generation, ResetGeneration: 1}
	if reconnect {
		response.Target = &target.wire
	}
	if err := s.out.write(response); err != nil {
		s.abort(err)
		s.removeAttachment(handle)
		a.stopAttachment()
		return
	}
	close(a.ready)
}

func (s *Service) control(ctx context.Context, request mobileproto.Request) {
	a, ok := s.operationAttachment(ctx, request, true)
	if !ok {
		return
	}
	defer a.opMu.Unlock()
	a.mu.Lock()
	snapshot := a.latest
	a.mu.Unlock()
	if !snapshot.InputModesKnown {
		s.writeError(request.RequestID, mobileproto.ErrorUnsupportedMode, "tmux did not expose required bracketed-paste and application key modes", false)
		return
	}
	owner := mobileOwnerID(a.handle, a.generation, request.OperationSequence)
	expected := tty.HeadlessTargetIdentity{ServerPID: a.target.resolved.ServerPID, SessionID: a.target.resolved.SessionID,
		SessionCreated: a.target.resolved.SessionCreated, Session: a.target.resolved.Session, Pane: a.target.resolved.Pane,
		Width: a.target.resolved.Width, Height: a.target.resolved.Height}
	geometry, err := tty.NewHeadlessGeometry(s.manager, expected, owner)
	if err == nil {
		err = geometry.ClaimResize(request.Columns, request.Rows)
	}
	if err != nil {
		s.writeError(request.RequestID, mobileproto.ErrorLease, err.Error(), true)
		return
	}
	a.mu.Lock()
	a.geometry = geometry
	a.control = true
	a.operationSequence = request.OperationSequence
	reset := a.resetGeneration
	resized := snapshot.PaneWidth != request.Columns || snapshot.PaneHeight != request.Rows
	if resized {
		a.resetGeneration++
		reset = a.resetGeneration
		a.firstOutputForReset = 0
		a.expectedColumns, a.expectedRows = request.Columns, request.Rows
	}
	a.mu.Unlock()
	if err := s.out.write(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseControl, RequestID: request.RequestID,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, Control: true, OperationSequence: request.OperationSequence,
		OutputSequence: a.outputSequence, ResetGeneration: reset, Geometry: &mobileproto.Geometry{Columns: request.Columns, Rows: request.Rows}}); err != nil {
		s.abort(err)
		a.loseControlLocked()
		return
	}
	if resized {
		s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseReset,
			AttachmentHandle: a.handle, AttachmentGeneration: a.generation, ResetGeneration: reset, Reason: "resize"})
		a.requestSnapshot()
	}
}

func (s *Service) inputBytes(ctx context.Context, request mobileproto.Request) {
	a, ok := s.operationAttachment(ctx, request, false)
	if !ok {
		return
	}
	defer a.opMu.Unlock()
	data, err := base64.StdEncoding.DecodeString(request.DataBase64)
	if err != nil || len(data) == 0 || len(data) > mobileproto.MaxInputBytes {
		s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "input must be valid non-empty base64 within the advertised bound", false)
		return
	}
	a.mu.Lock()
	geometry := a.geometry
	a.mu.Unlock()
	if err := geometry.SendLiteral(data); err != nil {
		a.loseControlLocked()
		s.writeError(request.RequestID, mobileproto.ErrorLease, err.Error(), true)
		return
	}
	a.mu.Lock()
	a.operationSequence = request.OperationSequence
	a.mu.Unlock()
	if err := s.out.write(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseAccepted, RequestID: request.RequestID,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, Control: true, OperationSequence: request.OperationSequence,
		OutputSequence: a.outputSequence, ResetGeneration: a.resetGeneration}); err != nil {
		s.abort(err)
		a.loseControlLocked()
	}
}

func (s *Service) resize(ctx context.Context, request mobileproto.Request) {
	a, ok := s.operationAttachment(ctx, request, false)
	if !ok {
		return
	}
	defer a.opMu.Unlock()
	a.mu.Lock()
	geometry := a.geometry
	a.mu.Unlock()
	if err := geometry.Resize(request.Columns, request.Rows); err != nil {
		a.loseControlLocked()
		s.writeError(request.RequestID, mobileproto.ErrorLease, err.Error(), true)
		return
	}
	a.mu.Lock()
	a.operationSequence = request.OperationSequence
	a.resetGeneration++
	reset := a.resetGeneration
	a.firstOutputForReset = 0
	a.expectedColumns, a.expectedRows = request.Columns, request.Rows
	a.mu.Unlock()
	if err := s.out.write(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseResized, RequestID: request.RequestID,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, Control: true, OperationSequence: request.OperationSequence,
		OutputSequence: a.outputSequence, ResetGeneration: reset, Geometry: &mobileproto.Geometry{Columns: request.Columns, Rows: request.Rows}}); err != nil {
		s.abort(err)
		a.loseControlLocked()
		return
	}
	s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseReset,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, ResetGeneration: reset, Reason: "resize"})
	a.requestSnapshot()
}

func (s *Service) heartbeat(ctx context.Context, request mobileproto.Request) {
	a, ok := s.operationAttachment(ctx, request, false)
	if !ok {
		return
	}
	defer a.opMu.Unlock()
	a.mu.Lock()
	geometry := a.geometry
	a.mu.Unlock()
	if err := geometry.Heartbeat(); err != nil {
		a.loseControlLocked()
		s.writeError(request.RequestID, mobileproto.ErrorLease, err.Error(), true)
		return
	}
	a.mu.Lock()
	a.operationSequence = request.OperationSequence
	a.mu.Unlock()
	if err := s.out.write(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseHeartbeat, RequestID: request.RequestID,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, Control: true, OperationSequence: request.OperationSequence,
		OutputSequence: a.outputSequence, ResetGeneration: a.resetGeneration}); err != nil {
		s.abort(err)
		a.loseControlLocked()
	}
}

func (s *Service) release(ctx context.Context, request mobileproto.Request) {
	a, ok := s.operationAttachment(ctx, request, false)
	if !ok {
		return
	}
	defer a.opMu.Unlock()
	a.mu.Lock()
	geometry := a.geometry
	a.geometry = nil
	a.control = false
	a.operationSequence = request.OperationSequence
	a.mu.Unlock()
	if err := geometry.Release(); err != nil {
		s.writeError(request.RequestID, mobileproto.ErrorBackend, err.Error(), true)
		return
	}
	s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseReleased, RequestID: request.RequestID,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, OperationSequence: request.OperationSequence,
		OutputSequence: a.outputSequence, ResetGeneration: a.resetGeneration})
}

func (s *Service) close(request mobileproto.Request) {
	a, ok := s.attachment(request.AttachmentHandle)
	if !ok {
		s.writeError(request.RequestID, mobileproto.ErrorAttachment, "unknown attachment", false)
		return
	}
	s.removeAttachment(a.handle)
	a.stopAttachment()
	s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseClosed, RequestID: request.RequestID,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation})
}

func (s *Service) operationAttachment(ctx context.Context, request mobileproto.Request, entering bool) (*attachment, bool) {
	if s.isTerminal() {
		return nil, false
	}
	a, ok := s.attachment(request.AttachmentHandle)
	if !ok {
		s.writeError(request.RequestID, mobileproto.ErrorAttachment, "unknown attachment", false)
		return nil, false
	}
	a.opMu.Lock()
	if err := s.revalidate(ctx, a.target); err != nil {
		a.loseControlLocked()
		a.opMu.Unlock()
		s.resolveFailure(request.RequestID, err)
		return nil, false
	}
	a.mu.Lock()
	expected := a.operationSequence + 1
	controlled := a.control
	reset := a.resetGeneration
	output := a.outputSequence
	firstForReset := a.firstOutputForReset
	a.mu.Unlock()
	if request.OperationSequence != expected || request.OperationSequence == 0 {
		a.loseControlLocked()
		a.opMu.Unlock()
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, fmt.Sprintf("operation_sequence must be %d", expected), false)
		return nil, false
	}
	if request.LastResetGeneration != reset {
		a.loseControlLocked()
		a.opMu.Unlock()
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, fmt.Sprintf("last_reset_generation must be %d", reset), false)
		return nil, false
	}
	if firstForReset == 0 || request.LastOutputSequence < firstForReset || request.LastOutputSequence > output {
		a.loseControlLocked()
		a.opMu.Unlock()
		s.writeError(request.RequestID, mobileproto.ErrorOperationOrder, "last_output_sequence has not applied an authoritative frame for this reset generation", false)
		return nil, false
	}
	if entering {
		if controlled {
			a.opMu.Unlock()
			s.writeError(request.RequestID, mobileproto.ErrorInvalidRequest, "attachment already controls input", false)
			return nil, false
		}
	} else if !controlled {
		a.opMu.Unlock()
		s.writeError(request.RequestID, mobileproto.ErrorControlRequired, "attachment does not control input", false)
		return nil, false
	}
	return a, true
}

func (s *Service) attachment(handle string) (*attachment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.attachments[handle]
	return a, ok
}
func (s *Service) removeAttachment(handle string) {
	s.mu.Lock()
	delete(s.attachments, handle)
	s.mu.Unlock()
}

func (s *Service) closeAll() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		list := make([]*attachment, 0, len(s.attachments))
		for _, a := range s.attachments {
			list = append(list, a)
		}
		s.attachments = make(map[string]*attachment)
		s.mu.Unlock()
		for _, a := range list {
			a.stopAttachment()
		}
		if s.manager != nil {
			s.manager.Stop()
		}
		if s.closeDone != nil {
			close(s.closeDone)
		}
	})
	if s.closeDone != nil {
		<-s.closeDone
	}
}

func (s *Service) resolveFailure(requestID string, err error) {
	var resolveErr *ResolveError
	if errors.As(err, &resolveErr) {
		s.writeError(requestID, resolveErr.Code, resolveErr.Message, false)
		return
	}
	s.writeError(requestID, mobileproto.ErrorBackend, err.Error(), true)
}

func (s *Service) writeError(requestID, code, message string, retry bool) {
	s.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseError, RequestID: requestID,
		Error: &mobileproto.Error{Code: code, Message: message, Retry: retry}})
}

func (s *Service) emit(response mobileproto.Response) bool {
	if err := s.out.write(response); err != nil {
		s.abort(err)
		return false
	}
	return true
}

func (s *Service) abort(error) {
	s.abortOnce.Do(func() {
		if s.terminal != nil {
			close(s.terminal)
		}
		go s.closeAll()
	})
}

func (s *Service) isTerminal() bool {
	if s.terminal == nil {
		return false
	}
	select {
	case <-s.terminal:
		return true
	default:
		return false
	}
}

func randomHandle(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mobile service: random identity: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
func mobileOwnerID(handle string, generation, controlEpoch uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", handle, generation, controlEpoch)))
	host, _ := os.Hostname()
	if host == "" {
		host = "sidecar"
	}
	return fmt.Sprintf("%s-mobile-%x-%d", host, sum[:6], os.Getpid())
}

type attachment struct {
	service                                            *Service
	handle, clientID                                   string
	generation                                         uint64
	target                                             targetState
	subscription                                       *tty.ControlSubscription
	ready                                              chan struct{}
	snapshots                                          chan queuedSnapshot
	failures                                           chan error
	stop                                               chan struct{}
	stopped                                            chan struct{}
	stopOnce                                           sync.Once
	mu                                                 sync.Mutex
	opMu                                               sync.Mutex
	latest                                             tty.ControlSnapshot
	geometry                                           *tty.HeadlessGeometry
	control                                            bool
	operationSequence, outputSequence, resetGeneration uint64
	firstOutputForReset                                uint64
	expectedColumns, expectedRows                      int
}

func newAttachment(service *Service, handle, clientID string, generation uint64, target targetState) *attachment {
	a := &attachment{service: service, handle: handle, clientID: clientID, generation: generation, target: target,
		ready: make(chan struct{}), snapshots: make(chan queuedSnapshot, 1), failures: make(chan error, 1), stop: make(chan struct{}), stopped: make(chan struct{}), resetGeneration: 1}
	go a.run()
	return a
}

type queuedSnapshot struct {
	snapshot        tty.ControlSnapshot
	resetGeneration uint64
	discontinuity   string
}

func (a *attachment) offerSnapshot(snapshot tty.ControlSnapshot) {
	a.mu.Lock()
	queued := queuedSnapshot{snapshot: snapshot, resetGeneration: a.resetGeneration}
	a.mu.Unlock()
	select {
	case a.snapshots <- queued:
		return
	default:
	}
	var dropped queuedSnapshot
	select {
	case dropped = <-a.snapshots:
	default:
	}
	if dropped.resetGeneration == queued.resetGeneration {
		queued.discontinuity = a.skippedDiscontinuity(dropped, queued)
	}
	select {
	case a.snapshots <- queued:
	default:
	}
}

func (a *attachment) skippedDiscontinuity(dropped, replacement queuedSnapshot) string {
	reason := dropped.discontinuity
	a.mu.Lock()
	latest := a.latest
	expectedColumns, expectedRows := a.expectedColumns, a.expectedRows
	a.mu.Unlock()
	if captureIdentityChanged(dropped.snapshot, a.target.resolved) {
		reason = strongerDiscontinuity(reason, "identity_changed")
	}
	hadLatest := latest.Pane != ""
	if hadLatest && (dropped.snapshot.AltScreen != latest.AltScreen || dropped.snapshot.AltScreen != replacement.snapshot.AltScreen) {
		reason = strongerDiscontinuity(reason, "alternate_screen")
	}
	if expectedColumns > 0 && expectedRows > 0 {
		droppedWasExpected := dropped.snapshot.PaneWidth == expectedColumns && dropped.snapshot.PaneHeight == expectedRows
		replacementIsExpected := replacement.snapshot.PaneWidth == expectedColumns && replacement.snapshot.PaneHeight == expectedRows
		if droppedWasExpected && !replacementIsExpected {
			reason = strongerDiscontinuity(reason, "geometry_changed")
		}
		return reason
	}
	if hadLatest && (snapshotGeometryChanged(latest, dropped.snapshot) || snapshotGeometryChanged(dropped.snapshot, replacement.snapshot)) {
		reason = strongerDiscontinuity(reason, "geometry_changed")
	}
	return reason
}

func strongerDiscontinuity(current, candidate string) string {
	priority := func(reason string) int {
		switch reason {
		case "identity_changed":
			return 3
		case "alternate_screen":
			return 2
		case "geometry_changed":
			return 1
		default:
			return 0
		}
	}
	if priority(candidate) > priority(current) {
		return candidate
	}
	return current
}

func (a *attachment) offerFailure(err error) {
	select {
	case a.failures <- err:
	default:
	}
}

func (a *attachment) run() {
	defer close(a.stopped)
	select {
	case <-a.ready:
	case <-a.stop:
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			return
		case err := <-a.failures:
			a.fail("capture_failed", err)
		case snapshot := <-a.snapshots:
			a.publishQueued(snapshot)
		case <-ticker.C:
			a.expirePresence()
		}
	}
}

func (a *attachment) publish(snapshot tty.ControlSnapshot) {
	a.mu.Lock()
	reset := a.resetGeneration
	a.mu.Unlock()
	a.publishObserved(snapshot, reset)
}

func (a *attachment) publishObserved(snapshot tty.ControlSnapshot, observedReset uint64) {
	a.publishQueued(queuedSnapshot{snapshot: snapshot, resetGeneration: observedReset})
}

func (a *attachment) publishQueued(observed queuedSnapshot) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	currentReset := a.resetGeneration
	a.mu.Unlock()
	if observed.resetGeneration != currentReset {
		// The capture callback ran before a deliberate reset, but delivery was
		// waiting behind that operation. It cannot establish post-reset state.
		a.requestSnapshot()
		return
	}
	snapshot := observed.snapshot
	want := a.target.resolved
	if captureIdentityChanged(snapshot, want) || observed.discontinuity == "identity_changed" {
		a.advanceResetLocked("identity_changed", true)
		a.service.writeError("", mobileproto.ErrorIdentityChanged, "capture target identity changed", false)
		a.requestSnapshot()
		return
	}
	vt, modes, err := normalizedFullFrame(snapshot)
	if err != nil {
		a.failLocked("capture_invalid", err)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(vt)
	if len(encoded) > mobileproto.MaxLineBytes-(64<<10) {
		a.failLocked("frame_too_large", fmt.Errorf("normalized frame exceeds line bound"))
		return
	}
	a.mu.Lock()
	hadLatest := a.latest.Pane != ""
	geometryChanged := hadLatest && snapshotGeometryChanged(a.latest, snapshot)
	awaitingGeometry := a.expectedColumns > 0 && a.expectedRows > 0
	expectedGeometry := awaitingGeometry && snapshot.PaneWidth == a.expectedColumns && snapshot.PaneHeight == a.expectedRows
	if expectedGeometry {
		a.expectedColumns, a.expectedRows = 0, 0
	}
	altChanged := hadLatest && a.latest.AltScreen != snapshot.AltScreen
	a.mu.Unlock()
	if awaitingGeometry && !expectedGeometry {
		// A capture already in flight when tmux acknowledged the resize still
		// describes the old grid. Never relabel it as the first frame of the new
		// reset generation; ask the ordered actor for a post-resize capture.
		if observed.discontinuity != "" {
			a.mu.Lock()
			a.expectedColumns, a.expectedRows = 0, 0
			a.mu.Unlock()
			a.advanceResetLocked(observed.discontinuity, true)
		}
		a.requestSnapshot()
		return
	}
	reason := observed.discontinuity
	if altChanged {
		reason = strongerDiscontinuity(reason, "alternate_screen")
	}
	if geometryChanged && !expectedGeometry {
		reason = strongerDiscontinuity(reason, "geometry_changed")
	}
	if reason != "" {
		a.advanceResetLocked(reason, true)
	}
	a.mu.Lock()
	a.latest = snapshot
	a.outputSequence++
	sequence, reset := a.outputSequence, a.resetGeneration
	first := a.firstOutputForReset
	a.mu.Unlock()
	geometry := mobileproto.Geometry{Columns: snapshot.PaneWidth, Rows: snapshot.PaneHeight}
	historySize := snapshot.HistorySize
	if err := a.service.out.write(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseFrame,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, OutputSequence: sequence,
		ResetGeneration: reset, FrameKind: "full", Geometry: &geometry, Modes: &modes, RenderVTBase64: encoded,
		HistorySize: &historySize}); err == nil && first == 0 {
		a.mu.Lock()
		if a.resetGeneration == reset && a.firstOutputForReset == 0 {
			a.firstOutputForReset = sequence
		}
		a.mu.Unlock()
	} else if err != nil {
		a.service.abort(err)
		a.advanceResetLocked("output_unavailable", true)
	}
}

func captureIdentityChanged(snapshot tty.ControlSnapshot, want ResolvedTarget) bool {
	return snapshot.Pane != want.Pane || snapshot.Session != want.Session || snapshot.ServerPID != want.ServerPID || snapshot.SessionID != want.SessionID || snapshot.SessionCreated != want.SessionCreated
}

func snapshotGeometryChanged(first, second tty.ControlSnapshot) bool {
	return first.PaneWidth != second.PaneWidth || first.PaneHeight != second.PaneHeight
}

func (a *attachment) fail(reason string, err error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.failLocked(reason, err)
}

func (a *attachment) failLocked(reason string, err error) {
	a.advanceResetLocked(reason, true)
	a.service.writeError("", mobileproto.ErrorBackend, err.Error(), true)
}

func (a *attachment) advanceResetLocked(reason string, revokeControl bool) {
	if revokeControl {
		a.loseControlLocked()
	}
	a.mu.Lock()
	a.resetGeneration++
	reset := a.resetGeneration
	a.firstOutputForReset = 0
	a.mu.Unlock()
	a.service.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseReset,
		AttachmentHandle: a.handle, AttachmentGeneration: a.generation, ResetGeneration: reset, Reason: reason})
}

func (a *attachment) expirePresence() {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	geometry, controlled := a.geometry, a.control
	a.mu.Unlock()
	if !controlled || geometry == nil {
		return
	}
	expired, err := geometry.ExpirePresence()
	if err != nil {
		a.failLocked("presence_release_failed", err)
		return
	}
	if !expired {
		return
	}
	a.mu.Lock()
	a.geometry = nil
	a.control = false
	a.mu.Unlock()
	a.advanceResetLocked("presence_timeout", false)
	// The pane may be completely idle. Explicitly request the replacement
	// frame that lets the client acknowledge this reset and take control again.
	a.requestSnapshot()
}

func (a *attachment) requestSnapshot() {
	if a.subscription != nil {
		a.subscription.RequestSnapshot()
	}
}

func (a *attachment) loseControl() { a.opMu.Lock(); defer a.opMu.Unlock(); a.loseControlLocked() }
func (a *attachment) loseControlLocked() {
	a.mu.Lock()
	geometry := a.geometry
	a.geometry = nil
	a.control = false
	a.mu.Unlock()
	if geometry != nil {
		_ = geometry.Release()
	}
}
func (a *attachment) stopAttachment() {
	a.stopOnce.Do(func() {
		a.loseControl()
		close(a.stop)
		if a.subscription != nil {
			a.subscription.Close()
		}
		<-a.stopped
	})
}
