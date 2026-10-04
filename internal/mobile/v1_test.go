package mobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
)

func TestHelloNegotiatesV1AndLegacyStaysExact(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		v1          bool
	}{
		{"legacy", `{"version":0,"type":"hello","request_id":"h"}`, false},
		{"v1", `{"version":0,"type":"hello","request_id":"h","capabilities":{"presence":true,"reset_free_frames":true,"coalesced_frames":true,"server_paste":true,"holder_labels":true},"viewer":{"kind":"ios","label":"iPhone"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			s := testService(&out)
			s.in = strings.NewReader(tc.input + "\n")
			if err := s.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			r := decodeResponses(t, &out)
			want := mobileproto.DefaultCapabilities()
			if tc.v1 {
				want = mobileproto.SupportedCapabilities()
			}
			if len(r) != 1 || r[0].Capabilities == nil || *r[0].Capabilities != want {
				t.Fatalf("hello=%+v", r)
			}
			if s.clientCaps.Presence != tc.v1 {
				t.Fatal("presence negotiation lost")
			}
		})
	}
}

func TestPresenceAndPasteRequireNegotiation(t *testing.T) {
	for _, kind := range []string{mobileproto.RequestPresence, mobileproto.RequestPaste} {
		var out bytes.Buffer
		s := testService(&out)
		s.handle(context.Background(), mobileproto.Request{Type: kind, RequestID: "op"})
		r := decodeResponses(t, &out)
		if len(r) != 1 || r[0].Error == nil || r[0].Error.Code != mobileproto.ErrorUnsupported {
			t.Fatalf("%s response=%+v", kind, r)
		}
	}
}

func TestResetFreeFramesIgnoreCarriedParserState(t *testing.T) {
	snapshot := tty.ControlSnapshot{Output: "abcd\nefgh", PaneWidth: 4, PaneHeight: 2, PaneRows: 2, Autowrap: true, AltScreen: true, OriginMode: true, InsertMode: true, CursorVisible: true}
	legacy, _, err := normalizedFullFrame(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	vt, _, err := normalizedFrame(snapshot, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(legacy, []byte("\x1bc")) {
		t.Fatal("legacy RIS changed")
	}
	if bytes.Contains(vt, []byte("\x1bc")) || bytes.Contains(vt, []byte("\x1b[?1049")) {
		t.Fatalf("reset-free frame resets buffer: %q", vt)
	}
	if !bytes.HasPrefix(vt, []byte("\x1b]8;;\x1b\\\x1b[0m\x1b[?6l\x1b[4l\x1b[r\x1b[?7l\x1b[H")) {
		t.Fatalf("carried modes not cleared: %q", vt)
	}
	if !bytes.Contains(vt, []byte("abcd")) || !bytes.Contains(vt, []byte("efgh")) {
		t.Fatalf("incomplete repaint: %q", vt)
	}
}

func TestNegotiatedFrameFlagsAndLegacyOmission(t *testing.T) {
	for _, v1 := range []bool{false, true} {
		var out bytes.Buffer
		s := testService(&out)
		s.clientCaps = mobileproto.ClientCapabilities{ResetFreeFrames: v1, CoalescedFrames: v1}
		target := testTarget()
		a := &attachment{service: s, handle: "a", generation: 1, target: target, resetGeneration: 1}
		a.publish(tty.ControlSnapshot{Output: "abcd\nefgh", Pane: target.resolved.Pane, Session: target.resolved.Session, ServerPID: target.resolved.ServerPID, SessionID: target.resolved.SessionID, SessionCreated: target.resolved.SessionCreated, PaneWidth: 4, PaneHeight: 2, PaneRows: 2})
		r := decodeResponses(t, &out)
		if len(r) != 1 || r[0].ResetFree != v1 || r[0].Coalesced != v1 {
			t.Fatalf("frame=%+v", r)
		}
		vt, _ := base64.StdEncoding.DecodeString(r[0].RenderVTBase64)
		if bytes.HasPrefix(vt, []byte("\x1bc")) == v1 {
			t.Fatal("RIS flag disagrees with payload")
		}
	}
}

func TestEncoderCoalescesFramesBeforeQueuePreservingBarriers(t *testing.T) {
	e := &safeEncoder{wake: make(chan struct{}, 1)}
	frame := func(n uint64) mobileproto.Response {
		return mobileproto.Response{Type: mobileproto.ResponseFrame, AttachmentHandle: "a", ResetGeneration: 1, OutputSequence: n, Coalesced: true}
	}
	for n := uint64(1); n <= 100; n++ {
		if err := e.write(frame(n)); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.pending) != 1 || e.pending[0].OutputSequence != 100 {
		t.Fatalf("pending=%+v", e.pending)
	}
	if err := e.write(mobileproto.Response{Type: mobileproto.ResponseAccepted, OperationSequence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := e.write(frame(101)); err != nil {
		t.Fatal(err)
	}
	if len(e.pending) != 3 || e.pending[0].OutputSequence != 100 || e.pending[1].Type != mobileproto.ResponseAccepted || e.pending[2].OutputSequence != 101 {
		t.Fatalf("barrier crossed: %+v", e.pending)
	}
}

func TestEncoderLegacyFramesStillOverflow(t *testing.T) {
	e := &safeEncoder{wake: make(chan struct{}, 1)}
	for i := 0; i < mobileproto.OutboundQueueDepth; i++ {
		if err := e.write(mobileproto.Response{Type: mobileproto.ResponseFrame}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.write(mobileproto.Response{Type: mobileproto.ResponseFrame}); err == nil {
		t.Fatal("legacy overflow changed")
	}
}

func TestEncoderSlowWriterGetsLatestFrameAndControl(t *testing.T) {
	input, output := io.Pipe()
	e := newSafeEncoder(output)
	defer func() { _ = input.Close() }()
	defer func() { _ = output.Close() }()
	defer e.close()
	if err := e.write(mobileproto.Response{Type: mobileproto.ResponseHello}); err != nil {
		t.Fatal(err)
	}
	// The writer is blocked on hello; all full frames remain replaceable.
	time.Sleep(10 * time.Millisecond)
	for n := uint64(1); n <= 100; n++ {
		if err := e.write(mobileproto.Response{Type: mobileproto.ResponseFrame, AttachmentHandle: "a", ResetGeneration: 1, OutputSequence: n, Coalesced: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.write(mobileproto.Response{Type: mobileproto.ResponseAccepted, RequestID: "input"}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(input)
	for _, want := range []string{mobileproto.ResponseHello, mobileproto.ResponseFrame, mobileproto.ResponseAccepted} {
		var r mobileproto.Response
		if err := decoder.Decode(&r); err != nil {
			t.Fatal(err)
		}
		if r.Type != want || (want == mobileproto.ResponseFrame && r.OutputSequence != 100) {
			t.Fatalf("response=%+v", r)
		}
	}
}

func TestPresenceDoesNotBypassLegacyControlGuards(t *testing.T) {
	for _, kind := range []string{mobileproto.RequestResize, mobileproto.RequestRelease} {
		var out bytes.Buffer
		s := testService(&out)
		s.clientCaps.Presence = true
		a := &attachment{service: s, handle: "a", generation: 1, target: testTarget(), resetGeneration: 1, firstOutputForReset: 1, outputSequence: 1}
		s.attachments[a.handle] = a
		s.handle(context.Background(), mobileproto.Request{Type: kind, RequestID: "op", AttachmentHandle: a.handle, OperationSequence: 1, LastOutputSequence: 1, LastResetGeneration: 1, Columns: 4, Rows: 2})
		r := decodeResponses(t, &out)
		if len(r) != 1 || r[0].Error == nil || r[0].Error.Code != mobileproto.ErrorControlRequired {
			t.Fatalf("%s response=%+v", kind, r)
		}
	}
}
