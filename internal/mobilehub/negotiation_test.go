package mobilehub

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobileproto"
)

func TestBrokerNegotiatesAtFirstOwnerHelloAndForwardsV1(t *testing.T) {
	owner := newBrokerTestOwner("v1")
	owner.caps = mobileproto.SupportedCapabilities()
	caps := &mobileproto.ClientCapabilities{Presence: true, ResetFreeFrames: true, CoalescedFrames: true, ServerPaste: true, HolderLabels: true}
	viewer := &mobileproto.Viewer{Kind: "browser", Label: "Laptop"}
	r := newBrokerRig(t, brokerTestRouter(t, owner), mobileproto.Request{Capabilities: caps, Viewer: viewer})
	if *r.hello.Capabilities != mobileproto.SupportedCapabilities() {
		t.Fatal("v1 support not advertised")
	}
	_, opened := r.open(r.catalog().Sections[0].Rows[0])
	owner.mu.Lock()
	hellos := append([]mobileproto.Request(nil), owner.hellos...)
	owner.mu.Unlock()
	if len(hellos) != 2 || hellos[0].Capabilities != nil || !reflect.DeepEqual(hellos[1].Capabilities, caps) || !reflect.DeepEqual(hellos[1].Viewer, viewer) {
		t.Fatalf("owner hello negotiation: %+v", hellos)
	}
	presence := &mobileproto.Presence{Focused: true, Visible: true, Columns: 70, Rows: 20, IdleMS: 3}
	r.send(mobileproto.Request{Type: mobileproto.RequestPresence, RequestID: "presence", AttachmentHandle: opened.AttachmentHandle, OperationSequence: 1, Presence: presence})
	if r.next(mobileproto.ResponsePresence).OperationSequence != 1 {
		t.Fatal("presence sequence lost")
	}
	r.send(mobileproto.Request{Type: mobileproto.RequestPaste, RequestID: "paste", AttachmentHandle: opened.AttachmentHandle, OperationSequence: 2, DataBase64: "eAo="})
	r.next(mobileproto.ResponseAccepted)
	r.next(mobileproto.ResponseFrame)
	stream := owner.latest(t)
	stream.mu.Lock()
	holder := mobileproto.Response{Version: 0, Type: mobileproto.ResponseHolder, AttachmentHandle: "same-raw-attachment", AttachmentGeneration: stream.generation, Holder: &mobileproto.Holder{Kind: "browser", Label: "Laptop"}}
	frame := stream.frame("v1-repaint")
	frame.ResetFree, frame.Coalesced = true, true
	stream.mu.Unlock()
	stream.push(holder)
	if got := r.next(mobileproto.ResponseHolder); got.AttachmentHandle != opened.AttachmentHandle || *got.Holder != *holder.Holder {
		t.Fatalf("holder lost: %+v", got)
	}
	stream.push(mobileproto.Response{Version: 0, Type: mobileproto.ResponseHolder, AttachmentHandle: "same-raw-attachment", AttachmentGeneration: stream.generation, Holder: &mobileproto.Holder{}})
	if got := r.next(mobileproto.ResponseHolder); *got.Holder != (mobileproto.Holder{}) {
		t.Fatal("holder release was lost")
	}
	r.send(mobileproto.Request{Type: mobileproto.RequestHeartbeat, RequestID: "presence-heartbeat", AttachmentHandle: opened.AttachmentHandle, OperationSequence: 3, Presence: presence})
	r.next(mobileproto.ResponseHeartbeat)
	stream.push(frame)
	if got := r.next(mobileproto.ResponseFrame); !got.ResetFree || !got.Coalesced {
		t.Fatalf("frame flags lost: %+v", got)
	}
	records := stream.recorded()
	var found bool
	for _, q := range records {
		if q.Type == mobileproto.RequestPresence {
			found = true
			if q.AttachmentHandle != "same-raw-attachment" || !reflect.DeepEqual(q.Presence, presence) {
				t.Fatalf("presence changed: %+v", q)
			}
		}
	}
	if !found {
		t.Fatal("presence was not forwarded")
	}
}

func TestBrokerRequestedCapabilitiesFailClosedOnOldOwner(t *testing.T) {
	owner := newBrokerTestOwner("v0")
	r := newBrokerRig(t, brokerTestRouter(t, owner), mobileproto.Request{Capabilities: &mobileproto.ClientCapabilities{Presence: true}})
	row := r.catalog().Sections[0].Rows[0]
	r.send(mobileproto.Request{Type: "resolve", RequestID: "select", Target: row.Target, ExpectedTarget: row.ExpectedTarget})
	if r.next("error").Error.Code != mobileproto.ErrorUnsupported {
		t.Fatal("old owner silently downgraded")
	}
	for _, q := range owner.latest(t).recorded() {
		if q.Type == mobileproto.RequestResolve {
			t.Fatal("resolve reached unsupported owner")
		}
	}
}

func TestBrokerV0RejectsUnnegotiatedPresenceAndPaste(t *testing.T) {
	r := newBrokerRig(t, brokerTestRouter(t, newBrokerTestOwner("v0")))
	if *r.hello.Capabilities != mobileproto.DefaultCapabilities() {
		t.Fatal("v0 hello changed")
	}
	for _, q := range []mobileproto.Request{
		{Type: mobileproto.RequestPresence, RequestID: "presence", Presence: &mobileproto.Presence{Columns: 80, Rows: 24}},
		{Type: mobileproto.RequestPaste, RequestID: "paste", DataBase64: "eA=="},
	} {
		r.send(q)
		if r.next("error").Error.Code != mobileproto.ErrorUnsupported {
			t.Fatal("operation was not refused")
		}
	}
}

func TestBrokerRejectsV1Bounds(t *testing.T) {
	for _, q := range []mobileproto.Request{
		{Type: "hello", Capabilities: &mobileproto.ClientCapabilities{CoalescedFrames: true}},
		{Type: "hello", Viewer: &mobileproto.Viewer{Kind: "browser", Label: "bad\nlabel"}},
		{Type: "presence", Presence: &mobileproto.Presence{Columns: 80, Rows: 24, IdleMS: -1}},
		{Type: "presence", Presence: &mobileproto.Presence{Columns: 80, Rows: 24, IdleMS: 86400001}},
		{Type: "presence", Presence: &mobileproto.Presence{Columns: mobileproto.MaxColumns + 1, Rows: 24}},
		{Type: "paste", DataBase64: "not-base64"},
	} {
		data, _ := json.Marshal(q)
		if _, err := decodeBrokerRequest(data); err == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
}

func TestBrokerCoalescesOnlyAdjacentFramesAndRetainsBarriers(t *testing.T) {
	r := &brokerRun{outReady: make(chan struct{}, 1), capabilities: &mobileproto.ClientCapabilities{CoalescedFrames: true}}
	emit := func(kind string, seq, reset uint64) {
		t.Helper()
		if err := r.emit(mobileproto.Response{Version: 0, Type: kind, OutputSequence: seq, ResetGeneration: reset, Coalesced: kind == "frame"}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	for seq := uint64(1); seq <= 100; seq++ {
		emit("frame", seq, 1)
	}
	emit("accepted", 100, 1)
	emit("frame", 101, 1)
	emit("frame", 102, 1)
	emit("reset", 0, 2)
	emit("frame", 103, 2)
	for _, want := range []struct {
		kind string
		seq  uint64
	}{{"frame", 100}, {"accepted", 100}, {"frame", 102}, {"reset", 0}, {"frame", 103}} {
		item, ok := r.nextOutput()
		if !ok {
			t.Fatal("missing output")
		}
		var got mobileproto.Response
		_ = json.Unmarshal(item.data, &got)
		if got.Type != want.kind || got.OutputSequence != want.seq {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}
	if _, ok := r.nextOutput(); ok {
		t.Fatal("unexpected extra frame")
	}
	legacy := &brokerRun{outReady: make(chan struct{}, 1)}
	for i := 0; i < mobileproto.OutboundQueueDepth; i++ {
		if err := legacy.emit(mobileproto.Response{Type: "frame"}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.emit(mobileproto.Response{Type: "frame"}, nil, nil); err != ErrOwnerOutputOverflow {
		t.Fatalf("legacy overflow changed: %v", err)
	}
}

func TestOwnerLineQueueCoalescesOnlyNegotiatedAdjacentFrames(t *testing.T) {
	ctx := withOwnerHello(context.Background(), &mobileproto.ClientCapabilities{ResetFreeFrames: true, CoalescedFrames: true}, nil)
	q := newOwnerLineQueue(ctx)
	frame := func(seq, reset uint64) []byte {
		b, _ := json.Marshal(mobileproto.Response{Version: 0, Type: "frame", FrameKind: "full", Coalesced: true, AttachmentHandle: "a", AttachmentGeneration: 1, ResetGeneration: reset, OutputSequence: seq})
		return b
	}
	for seq := uint64(1); seq <= 100; seq++ {
		if !q.push(frame(seq, 1)) {
			t.Fatal("negotiated frame overflow")
		}
	}
	barrier := []byte(`{"version":0,"type":"reset","reset_generation":2}`)
	if !q.push(barrier) || !q.push(frame(101, 2)) || !q.push(frame(102, 2)) {
		t.Fatal("barrier queue overflow")
	}
	for _, want := range [][]byte{frame(100, 1), barrier, frame(102, 2)} {
		got, err := q.read(ctx)
		if err != nil || string(got) != string(want) {
			t.Fatalf("got %s err %v want %s", got, err, want)
		}
	}
	legacy := newOwnerLineQueue(context.Background())
	for i := 0; i < mobileproto.OutboundQueueDepth; i++ {
		if !legacy.push(frame(1, 1)) {
			t.Fatal("legacy queue too short")
		}
	}
	if legacy.push(frame(2, 1)) {
		t.Fatal("legacy coalescing was enabled")
	}
}

func TestBrokerReportsUnsupportedForStrictLegacyOwnerHello(t *testing.T) {
	owner := newBrokerTestOwner("strict-v0")
	router := brokerTestRouter(t, owner)
	directory := router.directory.(*fakeOwnerDirectory)
	bind := directory.snapshot.Endpoints[0].Bind
	registry := &fakeRouteRegistry{valid: true, mode: "strict-v0"}
	directory.snapshot.Endpoints[0].Bind = func(ctx context.Context) (BoundOwner, error) {
		bound, err := bind(ctx)
		if err != nil {
			return bound, err
		}
		start := bound.Start
		bound.Start = func(ctx context.Context) (LineStream, mobileproto.Response, error) {
			if ownerHelloRequest(ctx).Capabilities != nil {
				return StartOwner(ctx, registry, hosts.MobileRouteAuthority{})
			}
			return start(ctx)
		}
		return bound, nil
	}
	r := newBrokerRig(t, router, mobileproto.Request{Capabilities: &mobileproto.ClientCapabilities{Presence: true}})
	row := r.catalog().Sections[0].Rows[0]
	r.send(mobileproto.Request{Type: "resolve", RequestID: "strict-select", Target: row.Target, ExpectedTarget: row.ExpectedTarget})
	response := r.next("error")
	if response.Error.Code != mobileproto.ErrorUnsupported || !strings.Contains(response.Error.Message, `unknown field "capabilities"`) {
		t.Fatalf("strict hello refusal=%+v", response.Error)
	}
	// The unchanged v0 hello remains usable by the same strict owner.
	stream, _, err := StartOwner(context.Background(), registry, hosts.MobileRouteAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
}

func TestOwnerNegotiationErrorsDoNotReclassifyIdentityFailures(t *testing.T) {
	requested := mobileproto.Request{Type: "hello", RequestID: "hub-owner-hello", Capabilities: &mobileproto.ClientCapabilities{Presence: true}}
	for _, code := range []string{mobileproto.ErrorInvalidRequest, mobileproto.ErrorUnsupported, mobileproto.ErrorProtocolMismatch, mobileproto.ErrorIdentityChanged, mobileproto.ErrorBackend} {
		line, _ := json.Marshal(mobileproto.Response{Type: "error", Error: &mobileproto.Error{Code: code, Message: "refusal"}})
		err := ownerNegotiationError(requested, line)
		want := code == mobileproto.ErrorInvalidRequest || code == mobileproto.ErrorUnsupported || code == mobileproto.ErrorProtocolMismatch
		if errors.Is(err, ErrOwnerNegotiationUnsupported) != want {
			t.Fatalf("code=%s err=%v", code, err)
		}
		if err := ownerNegotiationError(mobileproto.Request{Type: "hello", RequestID: "hub-owner-hello"}, line); err != nil {
			t.Fatalf("legacy error reclassified: %v", err)
		}
	}
	line, _ := json.Marshal(mobileproto.Response{Version: 0, Type: "hello", RequestID: "hub-owner-hello", APIInstance: "old"})
	if err := ownerNegotiationError(requested, line); !errors.Is(err, ErrOwnerNegotiationUnsupported) {
		t.Fatalf("missing capabilities: %v", err)
	}
}
