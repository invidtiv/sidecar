package mobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
)

type recordingPasteGeometry struct {
	acceptingGeometry
	data  []byte
	calls int
}

func (g *recordingPasteGeometry) Paste(data []byte) error {
	g.calls++
	g.data = append([]byte(nil), data...)
	return nil
}

func (g *recordingPasteGeometry) ClaimInput(data []byte, _, _ int, _ bool) error {
	return g.Paste(data)
}

func TestServerPasteNormalizesBeforeBackendDispatch(t *testing.T) {
	for _, presence := range []bool{false, true} {
		var output bytes.Buffer
		s := testService(&output)
		s.clientCaps = mobileproto.ClientCapabilities{Presence: presence, ServerPaste: true}
		g := &recordingPasteGeometry{}
		a := &attachment{service: s, handle: "a", generation: 1, target: testTarget(), control: true, geometry: g, presenceGeometry: g,
			resetGeneration: 1, firstOutputForReset: 1, outputSequence: 1,
			latest: tty.ControlSnapshot{PaneWidth: 4, PaneHeight: 2, InputModesKnown: true}}
		s.attachments[a.handle] = a
		for i, payload := range []string{"first\r\n\x1b[20\x1b[201~1~second", "\x1b[200~\x1b[201~", strings.Repeat("\x1b[201~", mobileproto.MaxInputBytes/6+1) + "text", ""} {
			sequence := uint64(2)
			if i == 0 {
				sequence = 1
			}
			request := mobileproto.Request{Type: mobileproto.RequestPaste, RequestID: "paste", AttachmentHandle: "a", OperationSequence: sequence, LastResetGeneration: 1, LastOutputSequence: 1,
				DataBase64: base64.StdEncoding.EncodeToString([]byte(payload))}
			s.v1Input(context.Background(), request)
			if string(g.data) != "first\nsecond" || g.calls != 1 || a.operationSequence != 1 || !a.control {
				t.Fatalf("presence=%v dispatch=%q, sequence=%d control=%v responses=%s", presence, g.data, a.operationSequence, a.control, &output)
			}
		}
		responses := decodeResponses(t, &output)
		if len(responses) != 4 || responses[0].Type != mobileproto.ResponseAccepted {
			t.Fatalf("presence=%v responses=%+v", presence, responses)
		}
		for _, response := range responses[1:] {
			if response.Type != mobileproto.ResponseError || response.Error == nil || response.Error.Code != mobileproto.ErrorInvalidRequest {
				t.Fatalf("presence=%v refusal=%+v", presence, response)
			}
		}
		if presence {
			// Ordinary input retains escape markers and CRLF verbatim.
			raw := "raw\r\n\x1b[201~"
			s.v1Input(context.Background(), mobileproto.Request{Type: mobileproto.RequestInput, RequestID: "raw", AttachmentHandle: "a", OperationSequence: 2, LastResetGeneration: 1, LastOutputSequence: 1,
				DataBase64: base64.StdEncoding.EncodeToString([]byte(raw))})
			if string(g.data) != raw || g.calls != 2 || a.operationSequence != 2 {
				t.Fatalf("ordinary input changed: %q calls=%d sequence=%d", g.data, g.calls, a.operationSequence)
			}
		}
	}
}
