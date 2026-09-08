package mobilehub

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

const (
	// A phone uses a separate catalog connection and one terminal connection.
	// This broker retains at most one owner, one target and one attachment.
	brokerRecentIDs        = 256
	brokerOutputBytes      = 16 << 20
	brokerOperationTimeout = 15 * time.Second
)

// ProtocolBroker projects v0 over the service that owns a terminal. It never
// owns terminal state, sequence advancement, geometry, presence or input retry.
type ProtocolBroker struct{ router *CatalogRouter }

func NewProtocolBroker(router *CatalogRouter) (*ProtocolBroker, error) {
	if router == nil {
		return nil, errors.New("mobile hub: catalog router is required")
	}
	return &ProtocolBroker{router: router}, nil
}

type brokerOwner struct {
	bound                           BoundOwner
	stream                          LineStream
	binding                         TargetBinding
	ctx                             context.Context
	cancel                          context.CancelFunc
	closed                          atomic.Bool
	publicTarget, rawTarget         string
	publicAttachment, rawAttachment string
	clientAttachmentID              string
	attachmentGeneration            uint64
}

func (o *brokerOwner) close() {
	if o != nil && o.closed.CompareAndSwap(false, true) {
		o.cancel()
		o.stream.Close()
	}
}

func (o *brokerOwner) validate(ctx context.Context) error {
	if o.closed.Load() {
		return ErrOwnerClosed
	}
	return o.bound.Validate(ctx)
}

type brokerEvent struct {
	owner *brokerOwner
	line  []byte
	err   error
}
type brokerPending struct {
	request mobileproto.Request
	rawID   string
	timeout *time.Timer
}
type brokerOutput struct {
	data  []byte
	owner *brokerOwner
	done  chan struct{}
}

type brokerRun struct {
	ctx           context.Context
	cancel        context.CancelCauseFunc
	router        *CatalogRouter
	instance      string
	owner         *brokerOwner
	pending       *brokerPending
	events        chan brokerEvent
	out           chan brokerOutput
	outputBytes   atomic.Int64
	requestNumber uint64
	seen          map[string]bool
	order         []string
	handshake     bool
}

// Run owns input/output for this protocol lifetime, closing them on shutdown
// when they implement io.Closer. A non-closeable Reader/Writer must itself
// return on cancellation; arbitrary blocking Go IO cannot be interrupted.
// Queues are bounded: one scanned request, one owner event, eight public output
// lines with a 16 MiB aggregate budget, plus the selected LineStream's own
// eight-line bound. No additional owner streams accumulate across selections.
func (b *ProtocolBroker) Run(ctx context.Context, input io.Reader, output io.Writer) error {
	if input == nil || output == nil {
		return errors.New("mobile hub: input and output are required")
	}
	instance, err := brokerHandle("hub_api")
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	r := &brokerRun{ctx: runCtx, cancel: cancel, router: b.router, instance: instance,
		events: make(chan brokerEvent, 1), out: make(chan brokerOutput, mobileproto.OutboundQueueDepth), seen: make(map[string]bool)}
	defer cancel(context.Canceled)
	defer func() { r.owner.close() }()
	defer func() {
		if r.pending != nil {
			r.pending.timeout.Stop()
		}
	}()
	var closeIO sync.Once
	stopIO := func() {
		closeIO.Do(func() {
			if c, ok := input.(io.Closer); ok {
				_ = c.Close()
			}
			if c, ok := output.(io.Closer); ok {
				_ = c.Close()
			}
		})
	}
	defer stopIO()
	go func() { <-runCtx.Done(); stopIO() }()
	go r.write(output)
	requests := make(chan []byte, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64<<10), mobileproto.MaxLineBytes+1)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case requests <- line:
			case <-runCtx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			cancel(fmt.Errorf("mobile hub: read request: %w", err))
		} else {
			cancel(io.EOF)
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		ready := requests
		if r.pending != nil {
			ready = nil
		}
		select {
		case <-runCtx.Done():
			if errors.Is(context.Cause(runCtx), io.EOF) {
				return nil
			}
			return context.Cause(runCtx)
		case <-ticker.C:
			if r.owner != nil {
				if err := r.owner.validate(runCtx); err != nil {
					return r.fatal("", mobileproto.ErrorIdentityChanged, err)
				}
			}
		case event := <-r.events:
			if event.owner != r.owner {
				continue
			}
			if event.err != nil {
				return r.fatal("", mobileproto.ErrorBackend, event.err)
			}
			if event.owner.closed.Load() {
				continue
			}
			if err := r.receive(event); err != nil {
				return r.fatal("", mobileproto.ErrorBackend, err)
			}
		case line := <-ready:
			request, err := decodeBrokerRequest(line)
			if err != nil {
				return r.fatal("", mobileproto.ErrorInvalidRequest, err)
			}
			if request.Version != mobileproto.Version {
				return r.fatal(request.RequestID, mobileproto.ErrorProtocolMismatch, errors.New("unsupported mobile protocol version"))
			}
			if err := r.rememberID(request.RequestID); err != nil {
				return r.fatal(request.RequestID, mobileproto.ErrorInvalidRequest, err)
			}
			if !r.handshake && request.Type != mobileproto.RequestHello {
				return r.fatal(request.RequestID, mobileproto.ErrorHandshake, errors.New("hello must be the first request"))
			}
			if r.handshake && request.Type == mobileproto.RequestHello {
				return r.fatal(request.RequestID, mobileproto.ErrorInvalidRequest, errors.New("hello was already accepted"))
			}
			if err := r.request(request); err != nil {
				return r.fatal(request.RequestID, mobileproto.ErrorBackend, err)
			}
		}
	}
}

func (r *brokerRun) rememberID(id string) error {
	if id == "" || len(id) > mobileproto.MaxRequestIDBytes || r.seen[id] {
		return errors.New("request_id is empty, oversized or recently used")
	}
	if len(r.order) == brokerRecentIDs {
		delete(r.seen, r.order[0])
		r.order = r.order[1:]
	}
	r.seen[id] = true
	r.order = append(r.order, id)
	return nil
}

func (r *brokerRun) request(request mobileproto.Request) error {
	switch request.Type {
	case mobileproto.RequestHello, mobileproto.RequestStatus:
		if request.Type == mobileproto.RequestHello {
			if r.handshake {
				return errors.New("hello was already accepted")
			}
			r.handshake = true
		}
		caps := mobileproto.DefaultCapabilities()
		return r.emit(mobileproto.Response{Version: mobileproto.Version, Type: request.Type, RequestID: request.RequestID, APIInstance: r.instance, Capabilities: &caps}, nil, nil)
	case mobileproto.RequestSessions:
		if r.owner != nil && r.owner.rawAttachment != "" {
			return r.refuse(request.RequestID, mobileproto.ErrorUnsupported, "catalog queries require a stream without a terminal attachment", true)
		}
		// A catalog scan needs its own temporary owner. Drop an unopened
		// selection so even this mixed-use connection retains only one stream.
		r.owner.close()
		r.owner = nil
		query := mobileproto.CatalogQuery{}
		if request.CatalogQuery != nil {
			query = *request.CatalogQuery
		}
		catalog, err := r.router.Query(r.ctx, query)
		if err != nil {
			return r.refuse(request.RequestID, mobileproto.ErrorBackend, err.Error(), true)
		}
		return r.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseSessions, RequestID: request.RequestID, APIInstance: r.instance, Catalog: &catalog}, nil, nil)
	case mobileproto.RequestResolve, mobileproto.RequestReconnect:
		if request.Type == mobileproto.RequestResolve && r.owner != nil && r.owner.rawAttachment != "" {
			return r.refuse(request.RequestID, mobileproto.ErrorUnsupported, "close the active attachment before resolving another target", false)
		}
		if request.Type == mobileproto.RequestReconnect && r.owner != nil && r.owner.rawAttachment != "" {
			o := r.owner
			if request.Target != o.binding.PublicSelector || *request.ExpectedTarget != o.binding.PublicExpected || request.PreviousAttachmentGeneration != o.attachmentGeneration || request.AttachmentID != o.clientAttachmentID {
				return r.refuse(request.RequestID, mobileproto.ErrorAttachment, "reconnect does not name this active attachment; it was not replaced", false)
			}
		}
		r.owner.close()
		r.owner = nil
		ownerCtx, ownerCancel := context.WithCancel(r.ctx)
		bound, stream, binding, err := r.router.Lookup(ownerCtx, request.Target, *request.ExpectedTarget)
		if err != nil {
			ownerCancel()
			return r.refuse(request.RequestID, mobileproto.ErrorIdentityChanged, err.Error(), false)
		}
		o := &brokerOwner{bound: bound, stream: stream, binding: binding, ctx: ownerCtx, cancel: ownerCancel}
		r.owner = o
		go func() { <-o.ctx.Done(); o.close() }()
		if !brokerCompatibleCapabilities(bound.Capabilities) {
			o.close()
			r.owner = nil
			return r.refuse(request.RequestID, mobileproto.ErrorUnsupported, "owning service capabilities do not match the public mobile v0 contract", false)
		}
		o.publicTarget, err = brokerHandle("hub_target")
		if err != nil {
			return err
		}
		go r.readOwner(o)
		request.Target = binding.OwnerSelector
		expected := binding.OwnerExpected
		request.ExpectedTarget = &expected
	case mobileproto.RequestOpen:
		if r.owner == nil || request.TargetHandle != r.owner.publicTarget || r.owner.rawTarget == "" {
			return r.refuse(request.RequestID, mobileproto.ErrorNotFound, "unknown target_handle", false)
		}
		if r.owner.rawAttachment != "" {
			return r.refuse(request.RequestID, mobileproto.ErrorOverflow, "one attachment is permitted per terminal stream", false)
		}
		request.TargetHandle = r.owner.rawTarget
	default:
		if r.owner == nil || r.owner.rawAttachment == "" || request.AttachmentHandle != r.owner.publicAttachment {
			return r.refuse(request.RequestID, mobileproto.ErrorAttachment, "unknown attachment_handle", false)
		}
		request.AttachmentHandle = r.owner.rawAttachment
	}
	// This actor serializes every owner write. Validate inside this boundary,
	// after any earlier operation has completed, never only at catalog lookup.
	if err := r.owner.validate(r.ctx); err != nil {
		return err
	}
	r.requestNumber++
	if r.requestNumber == 0 {
		return errors.New("internal request sequence exhausted")
	}
	rawID := fmt.Sprintf("hub-forward-%d", r.requestNumber)
	o := r.owner
	timeout := time.AfterFunc(brokerOperationTimeout, func() {
		o.close()
		r.cancel(errors.New("owning service operation timed out; no request was retried"))
	})
	r.pending = &brokerPending{request: request, rawID: rawID, timeout: timeout}
	request.RequestID = rawID // never expose arbitrary client IDs to owner housekeeping
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return r.owner.stream.WriteLine(data)
}

func brokerCompatibleCapabilities(c mobileproto.Capabilities) bool {
	return c == mobileproto.DefaultCapabilities()
}

func (r *brokerRun) readOwner(o *brokerOwner) {
	for {
		line, err := o.stream.ReadLine(o.ctx)
		if err != nil {
			if o.ctx.Err() != nil {
				return
			}
			// Invalidate queued output as soon as EOF/failure is observed, even
			// if delivery of the terminal event is behind a full event queue.
			o.close()
			select {
			case r.events <- brokerEvent{owner: o, err: err}:
			case <-r.ctx.Done():
			}
			return
		}
		select {
		case r.events <- brokerEvent{owner: o, line: line, err: err}:
		case <-o.ctx.Done():
			return
		}
	}
}

func (r *brokerRun) receive(event brokerEvent) error {
	o := event.owner
	if err := o.validate(r.ctx); err != nil {
		return err
	}
	var response mobileproto.Response
	if err := decodeBrokerEnvelope(event.line, &response); err != nil {
		return err
	}
	if response.Version != mobileproto.Version || response.APIInstance != "" || response.Capabilities != nil || response.Catalog != nil {
		return errors.New("invalid owning service response envelope")
	}
	async := response.RequestID == ""
	var request mobileproto.Request
	if async {
		if response.Type != mobileproto.ResponseFrame && response.Type != mobileproto.ResponseReset && response.Type != mobileproto.ResponseError {
			return errors.New("unexpected asynchronous owner response")
		}
	} else {
		if r.pending == nil || response.RequestID != r.pending.rawID {
			return errors.New("foreign or duplicate owner response correlation")
		}
		request = r.pending.request
		if response.Type != mobileproto.ResponseError && response.Type != brokerResponseType(request.Type) {
			return errors.New("owner response type disagrees with request")
		}
		response.RequestID = request.RequestID
	}
	if response.Type == mobileproto.ResponseError {
		if response.Error == nil || response.Error.Code == "" || response.Target != nil || response.History != nil || response.AttachmentHandle != "" {
			return errors.New("invalid owner error response")
		}
		if async {
			return errors.New("owning service terminated: " + response.Error.Message)
		}
		r.pending.timeout.Stop()
		r.pending = nil
		return r.emit(response, o, nil)
	}
	if response.Error != nil {
		return errors.New("owner response combines success and error")
	}
	if response.Type == mobileproto.ResponseResolved && (response.AttachmentHandle != "" || response.AttachmentGeneration != 0 || response.Control || response.OperationSequence != 0 || response.OutputSequence != 0 || response.ResetGeneration != 0 || response.Geometry != nil) {
		return errors.New("resolved owner target unexpectedly carries attachment state")
	}
	if response.Target != nil {
		if response.Type != mobileproto.ResponseResolved && response.Type != mobileproto.ResponseReconnected {
			return errors.New("unexpected owner target")
		}
		if response.Target.Identity() != o.binding.OwnerExpected || !brokerBoundedHandle(response.Target.Handle) || !brokerGeometry(response.Target.Geometry) {
			return errors.New("owner returned another target identity")
		}
		o.rawTarget = response.Target.Handle
		identity := o.binding.PublicExpected
		target := *response.Target
		target.Handle, target.HubInstance = o.publicTarget, r.instance
		target.HubID, target.OwnerHostID, target.OwnerConfigGeneration = identity.HubID, identity.OwnerHostID, identity.OwnerConfigGeneration
		target.WorkspaceID, target.WorkspaceKind = identity.WorkspaceID, identity.WorkspaceKind
		target.Session, target.Pane, target.ServerIncarnation, target.TargetGeneration = identity.Session, identity.Pane, identity.ServerIncarnation, identity.TargetGeneration
		response.Target = &target
	} else if response.Type == mobileproto.ResponseResolved || response.Type == mobileproto.ResponseReconnected {
		return errors.New("owner omitted the resolved target")
	}
	if response.Type == mobileproto.ResponseOpened || response.Type == mobileproto.ResponseReconnected {
		if o.rawAttachment != "" || !brokerBoundedHandle(response.AttachmentHandle) || response.AttachmentGeneration == 0 || response.ResetGeneration == 0 {
			return errors.New("invalid owner attachment opening")
		}
		if response.Control || response.OperationSequence != 0 || response.OutputSequence != 0 {
			return errors.New("owner opening unexpectedly restores control or output")
		}
		if request.Type == mobileproto.RequestReconnect && response.AttachmentGeneration != request.PreviousAttachmentGeneration+1 {
			return errors.New("owner reconnect generation did not advance")
		}
		handle, err := brokerHandle("hub_attachment")
		if err != nil {
			return err
		}
		o.rawAttachment, o.publicAttachment, o.attachmentGeneration = response.AttachmentHandle, handle, response.AttachmentGeneration
		o.clientAttachmentID = request.AttachmentID
	}
	if response.Type != mobileproto.ResponseResolved {
		if o.rawAttachment == "" || response.AttachmentHandle != o.rawAttachment || response.AttachmentGeneration != o.attachmentGeneration {
			return errors.New("owner output belongs to another attachment")
		}
		response.AttachmentHandle = o.publicAttachment
	}
	if response.Geometry != nil && !brokerGeometry(*response.Geometry) {
		return errors.New("invalid owner geometry")
	}
	if response.Type == mobileproto.ResponseFrame {
		if response.Geometry == nil || response.Modes == nil || response.FrameKind != "full" || response.OutputSequence == 0 || response.ResetGeneration == 0 {
			return errors.New("incomplete owner full frame")
		}
		if _, err := base64.StdEncoding.DecodeString(response.RenderVTBase64); err != nil {
			return errors.New("invalid owner frame bytes")
		}
	} else if response.RenderVTBase64 != "" || response.Modes != nil {
		return errors.New("terminal bytes outside an owner frame")
	}
	if response.Type == mobileproto.ResponseReset && response.ResetGeneration == 0 {
		return errors.New("invalid owner reset")
	}
	if response.Type == mobileproto.ResponseHistory {
		if err := validateBrokerHistory(response, request); err != nil {
			return err
		}
	} else if response.History != nil {
		return errors.New("history outside a history response")
	}
	if request.OperationSequence != 0 && response.OperationSequence != request.OperationSequence {
		return errors.New("owner acknowledgement changed the operation sequence")
	}
	if !async {
		r.pending.timeout.Stop()
		r.pending = nil
	}
	if err := r.emit(response, o, nil); err != nil {
		return err
	}
	if response.Type == mobileproto.ResponseClosed {
		o.rawAttachment, o.publicAttachment = "", ""
		o.clientAttachmentID = ""
		o.attachmentGeneration = 0
	}
	return nil
}

func validateBrokerHistory(response mobileproto.Response, request mobileproto.Request) error {
	h := response.History
	if h == nil || response.Geometry == nil || response.Geometry.Columns != request.Columns || response.Geometry.Rows != request.Rows || response.ResetGeneration != request.LastResetGeneration || response.OutputSequence != request.LastOutputSequence || h.HistoryRows < 0 || h.HistoryRows > request.HistoryRows || h.HistorySize < h.HistoryRows || h.StartLine < 0 || h.EndLine != h.HistorySize || h.EndLine-h.StartLine != h.HistoryRows || h.AtOldest != (h.StartLine == 0) {
		return errors.New("invalid owner history bounds or checkpoint")
	}
	if len(h.RenderVTBase64) > base64.StdEncoding.EncodedLen(mobileproto.MaxHistoryBytes) {
		return errors.New("owner history exceeds byte bound")
	}
	if _, err := base64.StdEncoding.DecodeString(h.RenderVTBase64); err != nil {
		return errors.New("invalid owner history bytes")
	}
	return nil
}

func (r *brokerRun) refuse(id, code, message string, retry bool) error {
	return r.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseError, RequestID: id, Error: &mobileproto.Error{Code: code, Message: message, Retry: retry}}, nil, nil)
}

func (r *brokerRun) fatal(id, code string, err error) error {
	r.owner.close() // no queued old-owner output may follow the terminal error
	done := make(chan struct{})
	_ = r.emit(mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseError, RequestID: id, Error: &mobileproto.Error{Code: code, Message: err.Error()}}, nil, done)
	select {
	case <-done:
	case <-r.ctx.Done():
	case <-time.After(250 * time.Millisecond):
	}
	return err
}

func (r *brokerRun) emit(response mobileproto.Response, owner *brokerOwner, done chan struct{}) error {
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if len(data) > mobileproto.MaxLineBytes {
		return errors.New("mobile hub: response exceeds line bound")
	}
	data = append(data, '\n')
	if r.outputBytes.Add(int64(len(data))) > brokerOutputBytes {
		r.outputBytes.Add(-int64(len(data)))
		return ErrOwnerOutputOverflow
	}
	select {
	case r.out <- brokerOutput{data: data, owner: owner, done: done}:
		return nil
	default:
		r.outputBytes.Add(-int64(len(data)))
		return ErrOwnerOutputOverflow
	}
}

func (r *brokerRun) write(output io.Writer) {
	for {
		select {
		case <-r.ctx.Done():
			return
		case item := <-r.out:
			if r.ctx.Err() != nil {
				return
			}
			if item.owner != nil {
				if item.owner.closed.Load() {
					r.outputBytes.Add(-int64(len(item.data)))
					continue
				}
				if err := item.owner.validate(r.ctx); err != nil {
					item.owner.close()
					r.cancel(err)
					return
				}
			}
			n, err := output.Write(item.data)
			r.outputBytes.Add(-int64(len(item.data)))
			if err == nil && n != len(item.data) {
				err = io.ErrShortWrite
			}
			if err != nil {
				r.cancel(err)
				return
			}
			if item.done != nil {
				close(item.done)
			}
		}
	}
}

func brokerHandle(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(data[:]), nil
}

func brokerBoundedHandle(value string) bool {
	return value != "" && len(value) <= mobileproto.MaxTargetBytes
}
func brokerGeometry(g mobileproto.Geometry) bool {
	return g.Columns >= 2 && g.Columns <= mobileproto.MaxColumns && g.Rows >= 1 && g.Rows <= mobileproto.MaxRows
}

func brokerResponseType(request string) string {
	return map[string]string{mobileproto.RequestResolve: mobileproto.ResponseResolved, mobileproto.RequestOpen: mobileproto.ResponseOpened,
		mobileproto.RequestReconnect: mobileproto.ResponseReconnected, mobileproto.RequestControl: mobileproto.ResponseControl,
		mobileproto.RequestInput: mobileproto.ResponseAccepted, mobileproto.RequestResize: mobileproto.ResponseResized,
		mobileproto.RequestHeartbeat: mobileproto.ResponseHeartbeat, mobileproto.RequestRelease: mobileproto.ResponseReleased,
		mobileproto.RequestClose: mobileproto.ResponseClosed, mobileproto.RequestHistory: mobileproto.ResponseHistory}[request]
}

func decodeBrokerEnvelope(line []byte, destination any) error {
	if len(line) == 0 || len(line) > mobileproto.MaxLineBytes || hasNewline(line) {
		return errors.New("empty, multiline or oversized protocol envelope")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid protocol JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("protocol envelope contains trailing JSON")
	}
	var presence struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(line, &presence); err != nil || presence.Version == nil {
		return errors.New("protocol version is required")
	}
	return nil
}

func decodeBrokerRequest(line []byte) (mobileproto.Request, error) {
	var request mobileproto.Request
	if err := decodeBrokerEnvelope(line, &request); err != nil {
		return request, err
	}
	allowed := ""
	switch request.Type {
	case mobileproto.RequestHello, mobileproto.RequestStatus:
	case mobileproto.RequestSessions:
		allowed = "catalog_query"
	case mobileproto.RequestResolve:
		allowed = "target expected_target"
	case mobileproto.RequestOpen:
		allowed = "target_handle attachment_id"
	case mobileproto.RequestReconnect:
		allowed = "target expected_target attachment_id previous_attachment_generation last_output_sequence last_reset_generation"
	case mobileproto.RequestControl, mobileproto.RequestResize:
		allowed = "attachment_handle operation_sequence columns rows last_output_sequence last_reset_generation"
	case mobileproto.RequestInput:
		allowed = "attachment_handle operation_sequence data_base64 last_output_sequence last_reset_generation"
	case mobileproto.RequestHeartbeat, mobileproto.RequestRelease:
		allowed = "attachment_handle operation_sequence last_output_sequence last_reset_generation"
	case mobileproto.RequestClose:
		allowed = "attachment_handle"
	case mobileproto.RequestHistory:
		allowed = "attachment_handle columns rows history_rows last_output_sequence last_reset_generation"
	default:
		return request, errors.New("unknown request type")
	}
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(line, &fields)
	for key := range fields {
		if key != "version" && key != "type" && key != "request_id" && !strings.Contains(" "+allowed+" ", " "+key+" ") {
			return request, fmt.Errorf("field %s is not allowed for %s", key, request.Type)
		}
	}
	if len(request.Target) > mobileproto.MaxTargetBytes || len(request.TargetHandle) > mobileproto.MaxTargetBytes || len(request.AttachmentHandle) > mobileproto.MaxTargetBytes || len(request.AttachmentID) > mobileproto.MaxAttachmentIDBytes {
		return request, errors.New("target or attachment exceeds protocol bound")
	}
	if query := request.CatalogQuery; query != nil {
		if len(query.Search) > mobileproto.MaxCatalogQueryBytes || len(query.Sort) > mobileproto.MaxCatalogQueryBytes {
			return request, errors.New("catalog query exceeds byte bound")
		}
		for _, filters := range [][]string{query.Hosts, query.Providers, query.States} {
			if len(filters) > mobileproto.MaxCatalogFilters {
				return request, errors.New("catalog filter count exceeds bound")
			}
			for _, value := range filters {
				if len(value) > mobileproto.MaxCatalogQueryBytes {
					return request, errors.New("catalog filter exceeds byte bound")
				}
			}
		}
	}
	if request.Type == mobileproto.RequestResolve || request.Type == mobileproto.RequestReconnect {
		if request.Target == "" || request.ExpectedTarget == nil {
			return request, errors.New("exact target and expected_target are required")
		}
		identity, _ := json.Marshal(request.ExpectedTarget)
		if len(identity) > 8*mobileproto.MaxTargetBytes {
			return request, errors.New("expected target exceeds protocol bound")
		}
	}
	if request.Type == mobileproto.RequestReconnect && (request.PreviousAttachmentGeneration == 0 || request.PreviousAttachmentGeneration == ^uint64(0)) {
		return request, errors.New("invalid previous attachment generation")
	}
	if request.Type == mobileproto.RequestInput {
		if len(request.DataBase64) > base64.StdEncoding.EncodedLen(mobileproto.MaxInputBytes) {
			return request, errors.New("input exceeds protocol bound")
		}
		data, err := base64.StdEncoding.DecodeString(request.DataBase64)
		if err != nil || len(data) == 0 || len(data) > mobileproto.MaxInputBytes {
			return request, errors.New("invalid input bytes")
		}
	}
	return request, nil
}
