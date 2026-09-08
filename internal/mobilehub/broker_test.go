package mobilehub

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

type brokerTestOwner struct {
	mu      sync.Mutex
	raw     mobileproto.CatalogSnapshot
	caps    mobileproto.Capabilities
	valid   atomic.Bool
	streams []*brokerTestStream
	hold    string
}

func newBrokerTestOwner(name string) *brokerTestOwner {
	o := &brokerTestOwner{raw: rawOwnerCatalog(name, "same-project", "same-selector"), caps: mobileproto.DefaultCapabilities()}
	o.valid.Store(true)
	return o
}

func (o *brokerTestOwner) endpoint(host string) OwnerEndpoint {
	return OwnerEndpoint{Host: mobileproto.CatalogHost{ID: host, State: "online"}, Bind: func(context.Context) (BoundOwner, error) {
		return BoundOwner{Authority: CatalogAuthority{OwnerHostID: host, RegistrationFingerprint: host + "-registration"},
			Validate: func(context.Context) error {
				if !o.valid.Load() {
					return errors.New("route retargeted")
				}
				return nil
			},
			Start: func(context.Context) (LineStream, mobileproto.Response, error) {
				o.mu.Lock()
				defer o.mu.Unlock()
				s := &brokerTestStream{raw: o.raw, lines: make(chan []byte, 64), done: make(chan struct{}), hold: o.hold, held: make(chan struct{}, 1), release: make(chan struct{})}
				o.streams = append(o.streams, s)
				caps := o.caps
				return s, mobileproto.Response{Version: 0, Type: "hello", RequestID: "hub-owner-hello", APIInstance: "owner-api", Capabilities: &caps}, nil
			}}, nil
	}}
}

func (o *brokerTestOwner) latest(t *testing.T) *brokerTestStream {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.streams) == 0 {
		t.Fatal("owner was never started")
	}
	return o.streams[len(o.streams)-1]
}

type brokerTestStream struct {
	mu                        sync.Mutex
	raw                       mobileproto.CatalogSnapshot
	lines                     chan []byte
	done                      chan struct{}
	once                      sync.Once
	requests                  []mobileproto.Request
	hold                      string
	held                      chan struct{}
	release                   chan struct{}
	generation, reset, output uint64
	geometry                  mobileproto.Geometry
	control                   bool
}

func (s *brokerTestStream) Close() { s.once.Do(func() { close(s.done) }) }
func (s *brokerTestStream) ReadLine(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, io.EOF
	case line := <-s.lines:
		return line, nil
	}
}
func (s *brokerTestStream) push(response mobileproto.Response) {
	data, _ := json.Marshal(response)
	select {
	case s.lines <- data:
	case <-s.done:
	}
}
func (s *brokerTestStream) WriteLine(line []byte) error {
	var q mobileproto.Request
	if err := json.Unmarshal(line, &q); err != nil {
		return err
	}
	s.mu.Lock()
	s.requests = append(s.requests, q)
	s.mu.Unlock()
	if q.Type == s.hold {
		s.held <- struct{}{}
		select {
		case <-s.release:
		case <-s.done:
			return io.ErrClosedPipe
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	response := mobileproto.Response{Version: 0, RequestID: q.RequestID, Type: brokerResponseType(q.Type)}
	if q.Type == mobileproto.RequestSessions {
		response.Type = "sessions"
		response.Catalog = &s.raw
		s.push(response)
		return nil
	}
	identity := *s.raw.Sections[0].Rows[0].ExpectedTarget
	target := mobileproto.Target{Handle: "same-raw-target", HubInstance: "owner-api", HubID: identity.HubID, OwnerHostID: identity.OwnerHostID,
		OwnerConfigGeneration: identity.OwnerConfigGeneration, WorkspaceID: identity.WorkspaceID, WorkspaceKind: identity.WorkspaceKind,
		Session: identity.Session, Pane: identity.Pane, ServerIncarnation: identity.ServerIncarnation, TargetGeneration: identity.TargetGeneration,
		Geometry: mobileproto.Geometry{Columns: 80, Rows: 24}}
	if q.Type == mobileproto.RequestResolve {
		if q.Target != s.raw.Sections[0].Rows[0].Target || q.ExpectedTarget == nil || *q.ExpectedTarget != identity {
			return errors.New("broker lost raw resolve authority")
		}
		response.Target = &target
		s.push(response)
		return nil
	}
	if q.Type == mobileproto.RequestOpen || q.Type == mobileproto.RequestReconnect {
		if q.Type == mobileproto.RequestOpen && q.TargetHandle != "same-raw-target" {
			return errors.New("broker lost raw target handle")
		}
		if q.Type == mobileproto.RequestReconnect && (q.Target != s.raw.Sections[0].Rows[0].Target || q.ExpectedTarget == nil || *q.ExpectedTarget != identity) {
			return errors.New("broker lost reconnect authority")
		}
		s.generation = 1
		if q.Type == mobileproto.RequestReconnect {
			s.generation = q.PreviousAttachmentGeneration + 1
			response.Target = &target
		}
		s.reset, s.output, s.geometry, s.control = 1, 1, target.Geometry, false
		response.AttachmentHandle = "same-raw-attachment"
		response.AttachmentGeneration = s.generation
		response.ResetGeneration = s.reset
		s.push(response)
		s.push(s.frame("owner-initial"))
		return nil
	}
	if q.AttachmentHandle != "same-raw-attachment" {
		return errors.New("broker lost raw attachment handle")
	}
	response.AttachmentHandle = q.AttachmentHandle
	response.AttachmentGeneration = s.generation
	response.OperationSequence = q.OperationSequence
	response.OutputSequence = s.output
	response.ResetGeneration = s.reset
	switch q.Type {
	case mobileproto.RequestControl, mobileproto.RequestResize:
		s.control = true
		s.geometry = mobileproto.Geometry{Columns: q.Columns, Rows: q.Rows}
		s.reset++
		response.Control = true
		response.Geometry = &s.geometry
		response.ResetGeneration = s.reset
		s.push(response)
		s.push(mobileproto.Response{Version: 0, Type: "reset", AttachmentHandle: "same-raw-attachment", AttachmentGeneration: s.generation, ResetGeneration: s.reset, Reason: "resize"})
		s.output++
		s.push(s.frame("owner-resized"))
		return nil
	case mobileproto.RequestInput:
		response.Control = s.control
		s.push(response)
		s.output++
		s.push(s.frame(q.DataBase64))
		return nil
	case mobileproto.RequestHeartbeat:
		response.Control = s.control
	case mobileproto.RequestRelease:
		s.control = false
	case mobileproto.RequestHistory:
		response.OperationSequence = 0
		response.OutputSequence = q.LastOutputSequence
		response.ResetGeneration = q.LastResetGeneration
		response.Geometry = &s.geometry
		response.History = &mobileproto.HistorySnapshot{HistorySize: 1, HistoryRows: 1, StartLine: 0, EndLine: 1, AtOldest: true, RenderVTBase64: base64.StdEncoding.EncodeToString([]byte("old\r\ncurrent"))}
	case mobileproto.RequestClose:
		response.OperationSequence = 0
		response.OutputSequence = 0
		response.ResetGeneration = 0
	default:
		return errors.New("unexpected forwarded request")
	}
	s.push(response)
	return nil
}
func (s *brokerTestStream) frame(text string) mobileproto.Response {
	geometry := s.geometry
	modes := mobileproto.Modes{InputKnown: true, Autowrap: true}
	return mobileproto.Response{Version: 0, Type: "frame", AttachmentHandle: "same-raw-attachment", AttachmentGeneration: s.generation, OutputSequence: s.output, ResetGeneration: s.reset, FrameKind: "full", Geometry: &geometry, Modes: &modes, Control: s.control, RenderVTBase64: base64.StdEncoding.EncodeToString([]byte(text))}
}
func (s *brokerTestStream) recorded() []mobileproto.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mobileproto.Request(nil), s.requests...)
}

type brokerRig struct {
	t         *testing.T
	in        *io.PipeWriter
	responses chan mobileproto.Response
	done      chan error
	cancel    context.CancelFunc
	hello     mobileproto.Response
}

func brokerTestRouter(t *testing.T, owners ...*brokerTestOwner) *CatalogRouter {
	t.Helper()
	directory := &fakeOwnerDirectory{snapshot: DirectorySnapshot{Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "hub-cfg"}, Hosts: []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}}, Validate: func(context.Context) error { return nil }}}
	for i, o := range owners {
		host := fmt.Sprintf("host-%d", i)
		ep := o.endpoint(host)
		directory.snapshot.Hosts = append(directory.snapshot.Hosts, ep.Host)
		directory.snapshot.Endpoints = append(directory.snapshot.Endpoints, ep)
	}
	router, err := NewCatalogRouter(directory)
	if err != nil {
		t.Fatal(err)
	}
	return router
}
func newBrokerRig(t *testing.T, router *CatalogRouter) *brokerRig {
	t.Helper()
	input, in := io.Pipe()
	out, output := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	r := &brokerRig{t: t, in: in, responses: make(chan mobileproto.Response, 128), done: make(chan error, 1), cancel: cancel}
	b, err := NewProtocolBroker(router)
	if err != nil {
		t.Fatal(err)
	}
	go func() { r.done <- b.Run(ctx, input, output) }()
	go func() {
		defer close(r.responses)
		defer func() { _ = out.Close() }()
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 64<<10), mobileproto.MaxLineBytes+1)
		for scanner.Scan() {
			var response mobileproto.Response
			if json.Unmarshal(scanner.Bytes(), &response) != nil {
				return
			}
			select {
			case r.responses <- response:
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() { cancel(); _ = in.Close() })
	r.send(mobileproto.Request{Type: "hello", RequestID: "hub-owner-hello"})
	r.hello = r.next("hello")
	return r
}
func (r *brokerRig) send(q mobileproto.Request) {
	r.t.Helper()
	data, err := json.Marshal(q)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err = r.in.Write(append(data, '\n')); err != nil {
		r.t.Fatal(err)
	}
}
func (r *brokerRig) next(kind string) mobileproto.Response {
	r.t.Helper()
	select {
	case response, ok := <-r.responses:
		if !ok {
			r.t.Fatal("broker output closed")
		}
		if response.Type != kind {
			r.t.Fatalf("got %s, wanted %s: %+v error=%+v", response.Type, kind, response, response.Error)
		}
		return response
	case <-time.After(3 * time.Second):
		r.t.Fatalf("timed out waiting for %s", kind)
	}
	return mobileproto.Response{}
}
func (r *brokerRig) catalog() mobileproto.CatalogSnapshot {
	r.send(mobileproto.Request{Type: "sessions", RequestID: "catalog"})
	return *r.next("sessions").Catalog
}
func (r *brokerRig) open(row mobileproto.CatalogRow) (mobileproto.Target, mobileproto.Response) {
	r.send(mobileproto.Request{Type: "resolve", RequestID: "hub-target-lookup", Target: row.Target, ExpectedTarget: row.ExpectedTarget})
	resolved := r.next("resolved")
	r.send(mobileproto.Request{Type: "open", RequestID: "hub-forward-1", TargetHandle: resolved.Target.Handle, AttachmentID: "caller-id"})
	opened := r.next("opened")
	r.next("frame")
	return *resolved.Target, opened
}

func TestProtocolBrokerForwardsExactOwnerJourneyAndFreshReconnect(t *testing.T) {
	owner := newBrokerTestOwner("raw-owner")
	router := brokerTestRouter(t, owner)
	r := newBrokerRig(t, router)
	row := r.catalog().Sections[0].Rows[0]
	target, opened := r.open(row)
	if target.Identity() != *row.ExpectedTarget || target.HubInstance != r.hello.APIInstance || target.Handle == "same-raw-target" || opened.AttachmentHandle == "same-raw-attachment" {
		t.Fatal("raw identities leaked or public identity changed")
	}
	operation := func(kind, id string, seq uint64) mobileproto.Request {
		return mobileproto.Request{Type: kind, RequestID: id, AttachmentHandle: opened.AttachmentHandle, OperationSequence: seq, LastOutputSequence: 1, LastResetGeneration: 1}
	}
	control := operation("control", "control", 1)
	control.Columns, control.Rows = 70, 20
	r.send(control)
	ack := r.next("control")
	reset := r.next("reset")
	frame := r.next("frame")
	if ack.OperationSequence != 1 || reset.ResetGeneration != 2 || frame.Geometry.Columns != 70 {
		t.Fatal("owner geometry/sequence changed")
	}
	input := operation("input", "input", 2)
	input.LastOutputSequence, input.LastResetGeneration = frame.OutputSequence, frame.ResetGeneration
	input.DataBase64 = base64.StdEncoding.EncodeToString([]byte("exact\x00input\n"))
	r.send(input)
	r.next("accepted")
	r.next("frame")
	resize := operation("resize", "resize", 3)
	resize.Columns, resize.Rows = 60, 18
	r.send(resize)
	r.next("resized")
	r.next("reset")
	frame = r.next("frame")
	r.send(mobileproto.Request{Type: "history", RequestID: "history", AttachmentHandle: opened.AttachmentHandle, Columns: 60, Rows: 18, HistoryRows: 1, LastOutputSequence: frame.OutputSequence, LastResetGeneration: frame.ResetGeneration})
	history := r.next("history")
	if history.OutputSequence != frame.OutputSequence || history.ResetGeneration != frame.ResetGeneration || history.History.RenderVTBase64 != base64.StdEncoding.EncodeToString([]byte("old\r\ncurrent")) {
		t.Fatal("history checkpoint or bytes were changed")
	}
	r.send(operation("heartbeat", "heartbeat", 4))
	r.next("heartbeat")
	r.send(operation("release", "release", 5))
	r.next("released")
	stream := owner.latest(t)
	records := stream.recorded()
	var inputs int
	for _, q := range records {
		if q.Type != "sessions" && (q.RequestID == "hub-owner-hello" || q.RequestID == "hub-target-lookup") {
			t.Fatal("caller id entered internal namespace")
		}
		if q.Type == "input" {
			inputs++
			if q.DataBase64 != input.DataBase64 {
				t.Fatal("input bytes changed")
			}
		}
	}
	if inputs != 1 {
		t.Fatalf("input replay count %d", inputs)
	}
	r.cancel()
	select {
	case <-stream.done:
	case <-time.After(time.Second):
		t.Fatal("cancel retained owner")
	}
	fresh := newBrokerRig(t, router)
	if fresh.hello.APIInstance == r.hello.APIInstance {
		t.Fatal("API identity reused")
	}
	fresh.send(mobileproto.Request{Type: "reconnect", RequestID: "fresh", Target: row.Target, ExpectedTarget: row.ExpectedTarget, PreviousAttachmentGeneration: opened.AttachmentGeneration, LastOutputSequence: frame.OutputSequence, LastResetGeneration: frame.ResetGeneration})
	reconnected := fresh.next("reconnected")
	fresh.next("frame")
	if reconnected.Target.Identity() != target.Identity() || reconnected.AttachmentGeneration != 2 || reconnected.Control || reconnected.AttachmentHandle == opened.AttachmentHandle {
		t.Fatal("fresh reconnect lost identity or restored control")
	}
	for _, q := range owner.latest(t).recorded() {
		if q.Type == "input" || q.Type == "heartbeat" || q.Type == "release" {
			t.Fatalf("reconnect invented %s", q.Type)
		}
	}
}

func TestProtocolBrokerSameRawPaneAcrossOwnersAndActiveResolveRefusal(t *testing.T) {
	one, two := newBrokerTestOwner("same-raw-owner"), newBrokerTestOwner("same-raw-owner")
	r := newBrokerRig(t, brokerTestRouter(t, one, two))
	catalog := r.catalog()
	var rows []mobileproto.CatalogRow
	for _, section := range catalog.Sections {
		rows = append(rows, section.Rows...)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	_, opened := r.open(rows[0])
	active := one.latest(t)
	r.send(mobileproto.Request{Type: "resolve", RequestID: "other", Target: rows[1].Target, ExpectedTarget: rows[1].ExpectedTarget})
	if r.next("error").Error.Code != mobileproto.ErrorUnsupported {
		t.Fatal("active resolve not refused")
	}
	r.send(mobileproto.Request{Type: "sessions", RequestID: "attached-catalog"})
	if r.next("error").Error.Code != mobileproto.ErrorUnsupported {
		t.Fatal("attached catalog not refused")
	}
	select {
	case <-active.done:
		t.Fatal("read-only refusal closed active terminal")
	default:
	}
	r.send(mobileproto.Request{Type: "close", RequestID: "close", AttachmentHandle: opened.AttachmentHandle})
	r.next("closed")
	r.send(mobileproto.Request{Type: "resolve", RequestID: "switch", Target: rows[1].Target, ExpectedTarget: rows[1].ExpectedTarget})
	switched := r.next("resolved")
	if switched.Target.OwnerHostID != rows[1].OwnerHostID {
		t.Fatal("fell back to first owner")
	}
	select {
	case <-active.done:
	case <-time.After(time.Second):
		t.Fatal("unused old owner retained")
	}
	r.send(mobileproto.Request{Type: "release", RequestID: "old-release", AttachmentHandle: opened.AttachmentHandle, OperationSequence: 1})
	if r.next("error").Error.Code != mobileproto.ErrorAttachment {
		t.Fatal("old attachment accepted by new owner")
	}
	for _, q := range two.latest(t).recorded() {
		if q.Type == "release" {
			t.Fatal("stale release crossed owner boundary")
		}
	}
}

func TestProtocolBrokerRevalidationDropsDelayedResponseAndQueuedRelease(t *testing.T) {
	owner := newBrokerTestOwner("raw")
	owner.hold = "input"
	r := newBrokerRig(t, brokerTestRouter(t, owner))
	_, opened := r.open(r.catalog().Sections[0].Rows[0])
	s := owner.latest(t)
	r.send(mobileproto.Request{Type: "input", RequestID: "delayed", AttachmentHandle: opened.AttachmentHandle, OperationSequence: 1, DataBase64: "eA=="})
	select {
	case <-s.held:
	case <-time.After(time.Second):
		t.Fatal("input not held")
	}
	r.send(mobileproto.Request{Type: "release", RequestID: "stale-release", AttachmentHandle: opened.AttachmentHandle, OperationSequence: 2})
	owner.valid.Store(false)
	close(s.release)
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("retarget did not close owner")
	}
	for _, q := range s.recorded() {
		if q.Type == "release" {
			t.Fatal("queued release forwarded after retarget")
		}
	}
	select {
	case err := <-r.done:
		if err == nil {
			t.Fatal("retarget not reported")
		}
	case <-time.After(time.Second):
		t.Fatal("retarget did not end broker")
	}
}

func TestProtocolBrokerEOFClosesBlockedOwnerWrite(t *testing.T) {
	owner := newBrokerTestOwner("raw")
	owner.hold = "input"
	r := newBrokerRig(t, brokerTestRouter(t, owner))
	_, opened := r.open(r.catalog().Sections[0].Rows[0])
	s := owner.latest(t)
	r.send(mobileproto.Request{Type: "input", RequestID: "blocked", AttachmentHandle: opened.AttachmentHandle, OperationSequence: 1, DataBase64: "eA=="})
	select {
	case <-s.held:
	case <-time.After(time.Second):
		t.Fatal("owner not blocked")
	}
	_ = r.in.Close()
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("EOF retained blocked owner")
	}
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("EOF did not end broker")
	}
}

func TestProtocolBrokerRejectsIncompatibleObservedOwnerCapabilities(t *testing.T) {
	owner := newBrokerTestOwner("raw")
	owner.caps.HeartbeatIntervalMS = 100
	owner.caps.PresenceTimeoutMS = 500
	r := newBrokerRig(t, brokerTestRouter(t, owner))
	row := r.catalog().Sections[0].Rows[0]
	r.send(mobileproto.Request{Type: "resolve", RequestID: "resolve", Target: row.Target, ExpectedTarget: row.ExpectedTarget})
	if r.next("error").Error.Code != mobileproto.ErrorUnsupported {
		t.Fatal("incompatible capabilities accepted")
	}
	for _, q := range owner.latest(t).recorded() {
		if q.Type == "resolve" {
			t.Fatal("resolve reached incompatible owner")
		}
	}
}

func TestProtocolBrokerStrictRequestDecode(t *testing.T) {
	for _, line := range []string{
		`{"type":"hello","request_id":"a"}`, `{"version":0,"type":"hello","request_id":"a","unknown":1}`,
		`{"version":0,"type":"hello","request_id":"a"} {}`, `{"version":0,"type":"status","request_id":"a","attachment_handle":"x"}`,
		`{"version":0,"type":"resolve","request_id":"a","target":"x"}`, `{"version":0,"type":"input","request_id":"a","data_base64":"??"}`,
		`{"version":0,"type":"reconnect","request_id":"a","target":"x","expected_target":{},"previous_attachment_generation":18446744073709551615}`,
		strings.Repeat("x", mobileproto.MaxLineBytes+1),
	} {
		if _, err := decodeBrokerRequest([]byte(line)); err == nil {
			t.Fatalf("malformed request accepted: %.100s", line)
		}
	}
	r := &brokerRun{seen: make(map[string]bool)}
	for i := 0; i < brokerRecentIDs+10; i++ {
		if err := r.rememberID(fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.seen) != brokerRecentIDs || len(r.order) != brokerRecentIDs {
		t.Fatal("request IDs grew without bound")
	}
	if r.rememberID("0") != nil {
		t.Fatal("evicted ID not reusable")
	}
	if r.rememberID("0") == nil {
		t.Fatal("recent duplicate accepted")
	}
}

func TestProtocolBrokerRefusesForeignOwnerResponses(t *testing.T) {
	for _, kind := range []string{"attachment", "correlation", "target", "history"} {
		t.Run(kind, func(t *testing.T) {
			owner := newBrokerTestOwner("raw")
			r := newBrokerRig(t, brokerTestRouter(t, owner))
			_, _ = r.open(r.catalog().Sections[0].Rows[0])
			s := owner.latest(t)
			s.mu.Lock()
			bad := s.frame("must-not-cross")
			s.mu.Unlock()
			switch kind {
			case "attachment":
				bad.AttachmentHandle = "foreign"
			case "correlation":
				bad.RequestID = "hub-forward-999"
			case "target":
				bad.Target = &mobileproto.Target{Handle: "foreign"}
			case "history":
				bad.History = &mobileproto.HistorySnapshot{}
			}
			s.push(bad)
			if response := r.next("error"); response.Error == nil {
				t.Fatal("invalid owner output accepted")
			}
			select {
			case <-s.done:
			case <-time.After(time.Second):
				t.Fatal("invalid owner stream retained")
			}
		})
	}
}

type brokerGatedWriter struct {
	target  io.Writer
	block   atomic.Bool
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
	enter   sync.Once
}

func (w *brokerGatedWriter) Write(data []byte) (int, error) {
	if w.block.Load() {
		w.enter.Do(func() { close(w.entered) })
		<-w.closed
		return 0, io.ErrClosedPipe
	}
	return w.target.Write(data)
}
func (w *brokerGatedWriter) Close() error {
	w.once.Do(func() { close(w.closed) })
	if c, ok := w.target.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func TestProtocolBrokerOutputBackpressureClosesOwnerAndDiscardsQueuedFrames(t *testing.T) {
	owner := newBrokerTestOwner("raw")
	router := brokerTestRouter(t, owner)
	input, in := io.Pipe()
	out, output := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &brokerGatedWriter{target: output, entered: make(chan struct{}), closed: make(chan struct{})}
	broker, _ := NewProtocolBroker(router)
	r := &brokerRig{t: t, in: in, responses: make(chan mobileproto.Response, 32), done: make(chan error, 1), cancel: cancel}
	go func() { r.done <- broker.Run(ctx, input, writer) }()
	go func() {
		defer func() { _ = out.Close() }()
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			var response mobileproto.Response
			_ = json.Unmarshal(scanner.Bytes(), &response)
			r.responses <- response
		}
	}()
	t.Cleanup(func() { cancel(); _ = in.Close() })
	r.send(mobileproto.Request{Type: "hello", RequestID: "hello"})
	r.next("hello")
	_, _ = r.open(r.catalog().Sections[0].Rows[0])
	s := owner.latest(t)
	writer.block.Store(true)
	for i := 0; i < 32; i++ {
		s.mu.Lock()
		s.output++
		frame := s.frame("output")
		s.mu.Unlock()
		s.push(frame)
	}
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("writer never blocked")
	}
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("backpressure did not close owner")
	}
	select {
	case err := <-r.done:
		if err == nil {
			t.Fatal("overflow not reported")
		}
	case <-time.After(time.Second):
		t.Fatal("overflow did not terminate broker")
	}
}

func TestProtocolBrokerUsesRealLocalServiceCatalogResolveAndConfigFence(t *testing.T) {
	now := time.Date(2026, 9, 8, 19, 0, 0, 0, time.UTC)
	resolved := mobile.ResolvedTarget{WorkspaceID: "workspace", WorkspaceKind: "shell", ProjectRoot: "/isolated-model-only", Session: "same-session", Pane: "%1", ServerPID: 42,
		SessionID: "$1", SessionCreated: "1700000000", DurableSessionCreated: now.Add(-time.Hour).Format(time.RFC3339Nano), Width: 80, Height: 24}
	workspace := workspaceinventory.Workspace{ID: resolved.WorkspaceID, ProjectKey: resolved.ProjectRoot, ProjectName: "Isolated", ProjectRoot: resolved.ProjectRoot,
		Kind: workspaceinventory.KindShell, Name: "Terminal", TmuxName: resolved.Session, PaneID: resolved.Pane, Live: true, CreatedAt: now.Add(-time.Hour), ObservedAt: now}
	var config atomic.Value
	config.Store("owner-config")
	factory := func(input io.Reader, output io.Writer) (*mobile.Service, error) {
		return mobile.New(mobile.Config{Input: input, Output: output, HubID: "raw-hub", OwnerHostID: "raw-owner", OwnerConfigGeneration: "owner-config",
			OwnerConfigGenerationProvider: func(context.Context) (string, error) { return config.Load().(string), nil },
			Resolver:                      func(context.Context, string) (mobile.ResolvedTarget, error) { return resolved, nil },
			Catalog: func(context.Context) (mobile.CatalogInput, error) {
				return mobile.CatalogInput{ObservedAt: now, Hosts: []mobileproto.CatalogHost{{ID: "raw-owner", State: "online", Local: true}}, Projects: []mobile.CatalogProject{{Label: "Isolated", Result: workspaceinventory.ProjectResult{ProjectKey: resolved.ProjectRoot, ProjectName: "Isolated", Workspaces: []workspaceinventory.Workspace{workspace}}}}}, nil
			}})
	}
	directory := &fakeOwnerDirectory{snapshot: DirectorySnapshot{Identity: mobile.CatalogIdentity{HubID: "public-hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "public-config"}, Hosts: []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}}, Validate: func(context.Context) error { return nil }}}
	directory.snapshot.Endpoints = []OwnerEndpoint{{Host: directory.snapshot.Hosts[0], Bind: func(context.Context) (BoundOwner, error) {
		return BoundOwner{Authority: CatalogAuthority{OwnerHostID: "local:hub", RegistrationFingerprint: "stable"}, Validate: func(context.Context) error { return nil },
			Start: func(ctx context.Context) (LineStream, mobileproto.Response, error) { return StartLocal(ctx, factory) }}, nil
	}}}
	router, err := NewCatalogRouter(directory)
	if err != nil {
		t.Fatal(err)
	}
	r := newBrokerRig(t, router)
	row := r.catalog().Sections[0].Rows[0]
	r.send(mobileproto.Request{Type: "resolve", RequestID: "local-resolve", Target: row.Target, ExpectedTarget: row.ExpectedTarget})
	response := r.next("resolved")
	if response.Target.Identity() != *row.ExpectedTarget || response.Target.HubID != "public-hub" || response.Target.HubInstance != r.hello.APIInstance {
		t.Fatal("real owner response was not remapped exactly")
	}
	// No Subscribe/open is invoked here: the real service's pure resolver/catalog
	// path is exercised without any tmux process or user state. The full terminal
	// forwarding journey above uses injected owner frames; CLI proof owns tmux.
	config.Store("owner-config-replaced")
	r.send(mobileproto.Request{Type: "resolve", RequestID: "old-selection", Target: row.Target, ExpectedTarget: row.ExpectedTarget})
	if failure := r.next("error"); failure.Error.Code != mobileproto.ErrorIdentityChanged {
		t.Fatalf("old selection survived owner config replacement: %+v", failure.Error)
	}
}

func TestProtocolBrokerMalformedPublicRequestClosesActiveOwner(t *testing.T) {
	owner := newBrokerTestOwner("raw")
	r := newBrokerRig(t, brokerTestRouter(t, owner))
	_, _ = r.open(r.catalog().Sections[0].Rows[0])
	s := owner.latest(t)
	if _, err := r.in.Write([]byte("{\"version\":0,\"type\":\"status\",\"request_id\":\"bad\",\"data_base64\":\"eA==\"}\n")); err != nil {
		t.Fatal(err)
	}
	if r.next("error").Error.Code != mobileproto.ErrorInvalidRequest {
		t.Fatal("malformed request not refused")
	}
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("malformed request retained owner")
	}
}

func TestProtocolBrokerCatalogDropsOnlyAnUnopenedSelection(t *testing.T) {
	owner := newBrokerTestOwner("raw")
	r := newBrokerRig(t, brokerTestRouter(t, owner))
	row := r.catalog().Sections[0].Rows[0]
	r.send(mobileproto.Request{Type: "resolve", RequestID: "resolve-unopened", Target: row.Target, ExpectedTarget: row.ExpectedTarget})
	resolved := r.next("resolved")
	unopened := owner.latest(t)
	r.send(mobileproto.Request{Type: "sessions", RequestID: "scan-again"})
	r.next("sessions")
	select {
	case <-unopened.done:
	default:
		t.Fatal("catalog retained an unnecessary owner stream")
	}
	r.send(mobileproto.Request{Type: "open", RequestID: "obsolete-selection", TargetHandle: resolved.Target.Handle})
	if r.next("error").Error.Code != mobileproto.ErrorNotFound {
		t.Fatal("obsolete unopened handle remained live")
	}
	for _, q := range unopened.recorded() {
		if q.Type == "release" || q.Type == "close" {
			t.Fatal("catalog invented a terminal mutation")
		}
	}
}

func TestProtocolBrokerActiveReconnectReplacesOnlyExactCurrentAttachment(t *testing.T) {
	one, two := newBrokerTestOwner("same-raw-owner"), newBrokerTestOwner("same-raw-owner")
	r := newBrokerRig(t, brokerTestRouter(t, one, two))
	catalog := r.catalog()
	var rows []mobileproto.CatalogRow
	for _, section := range catalog.Sections {
		rows = append(rows, section.Rows...)
	}
	_, opened := r.open(rows[0])
	claim := func(handle, id string) {
		r.send(mobileproto.Request{Type: "control", RequestID: id, AttachmentHandle: handle, OperationSequence: 1, LastOutputSequence: 1, LastResetGeneration: 1, Columns: 70, Rows: 20})
		r.next("control")
		r.next("reset")
		r.next("frame")
	}
	claim(opened.AttachmentHandle, "initial-control")
	active := one.latest(t)
	refuse := func(request mobileproto.Request, stream *brokerTestStream) {
		t.Helper()
		before := len(stream.recorded())
		r.send(request)
		if response := r.next("error"); response.Error.Code != mobileproto.ErrorAttachment {
			t.Fatalf("unexpected reconnect refusal: %+v", response.Error)
		}
		select {
		case <-stream.done:
			t.Fatal("rejected reconnect closed the active owner")
		default:
		}
		stream.mu.Lock()
		controlling := stream.control
		stream.mu.Unlock()
		if !controlling || len(stream.recorded()) != before {
			t.Fatal("rejected reconnect mutated the controlling owner")
		}
	}
	refuse(mobileproto.Request{Type: "reconnect", RequestID: "other-owner", Target: rows[1].Target, ExpectedTarget: rows[1].ExpectedTarget, PreviousAttachmentGeneration: 1, AttachmentID: "caller-id"}, active)
	refuse(mobileproto.Request{Type: "reconnect", RequestID: "other-client", Target: rows[0].Target, ExpectedTarget: rows[0].ExpectedTarget, PreviousAttachmentGeneration: 1, AttachmentID: "different-client-id"}, active)
	r.send(mobileproto.Request{Type: "reconnect", RequestID: "exact-reconnect", Target: rows[0].Target, ExpectedTarget: rows[0].ExpectedTarget, PreviousAttachmentGeneration: 1, AttachmentID: "caller-id", LastOutputSequence: 2, LastResetGeneration: 2})
	reconnected := r.next("reconnected")
	r.next("frame")
	if reconnected.AttachmentGeneration != 2 || reconnected.Control || reconnected.AttachmentHandle == opened.AttachmentHandle {
		t.Fatal("exact reconnect did not replace with a fresh viewing attachment")
	}
	select {
	case <-active.done:
	default:
		t.Fatal("exact reconnect retained the prior owner")
	}
	claim(reconnected.AttachmentHandle, "new-control")
	refuse(mobileproto.Request{Type: "reconnect", RequestID: "stale-generation", Target: rows[0].Target, ExpectedTarget: rows[0].ExpectedTarget, PreviousAttachmentGeneration: 1, AttachmentID: "caller-id"}, one.latest(t))
}

func TestProtocolBrokerOwnerEOFInvalidatesBeforeBlockedEventDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ownerCtx, ownerCancel := context.WithCancel(ctx)
	stream := &brokerTestStream{lines: make(chan []byte), done: make(chan struct{})}
	o := &brokerOwner{stream: stream, ctx: ownerCtx, cancel: ownerCancel}
	r := &brokerRun{ctx: ctx, events: make(chan brokerEvent, 1)}
	// An older event already occupies the actor's bounded queue. Learning EOF
	// must still mark every public writer item from this owner obsolete now.
	r.events <- brokerEvent{owner: o, line: []byte("old queued frame")}
	stream.Close()
	finished := make(chan struct{})
	go func() { r.readOwner(o); close(finished) }()
	select {
	case <-ownerCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("owner EOF waited behind queued data before invalidating output")
	}
	if !o.closed.Load() {
		t.Fatal("owner output remained eligible after EOF")
	}
	<-r.events
	select {
	case event := <-r.events:
		if !errors.Is(event.err, io.EOF) {
			t.Fatalf("terminal event: %v", event.err)
		}
	case <-time.After(time.Second):
		t.Fatal("missing owner EOF event")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("owner reader did not terminate")
	}
}
