package hostproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func sampleUIRequest() UIRequest {
	return UIRequest{
		ID:        "0000000000000001-abcdef",
		Action:    UIRequestActionOpen,
		CreatedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
		TTLMs:     1200,
		Origin: UIRequestOrigin{
			TmuxSession: "proj-agent-1",
			ProjectKey:  "sidecar",
			WorkDir:     "/home/marcus/code/sidecar",
			HostID:      "mac-mini",
		},
		Target: UIRequestTarget{Kind: "file", Value: "README.md", Line: 12},
	}
}

func TestUIRequestRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	original := sampleUIRequest()
	if err := NewEncoder(&buffer).Encode(Message{Kind: KindUIRequest, UIRequest: &original}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	msg, err := NewDecoder(&buffer).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if msg.Kind != KindUIRequest || msg.UIRequest == nil {
		t.Fatalf("kind = %q, uiRequest = %v", msg.Kind, msg.UIRequest)
	}
	got := *msg.UIRequest
	if got.ID != original.ID || got.Action != UIRequestActionOpen || got.TTLMs != original.TTLMs {
		t.Errorf("ui request = %+v", got)
	}
	if got.Origin != original.Origin {
		t.Errorf("origin = %+v, want %+v", got.Origin, original.Origin)
	}
	if got.Target != original.Target {
		t.Errorf("target = %+v, want %+v", got.Target, original.Target)
	}
}

func TestUIRequestValidationFailsClosed(t *testing.T) {
	valid := sampleUIRequest()
	cases := []struct {
		name   string
		mutate func(*UIRequest)
		want   string
	}{
		{"no id", func(r *UIRequest) { r.ID = "" }, "no id"},
		{"long id", func(r *UIRequest) { r.ID = strings.Repeat("i", MaxUIRequestIDBytes+1) }, "id exceeds"},
		{"no ttl", func(r *UIRequest) { r.TTLMs = 0 }, "no ttl"},
		{"no session", func(r *UIRequest) { r.Origin.TmuxSession = "" }, "no origin session"},
		{"no host id", func(r *UIRequest) { r.Origin.HostID = "" }, "no host id"},
		{"unknown action", func(r *UIRequest) { r.Action = "rename-shell" }, "unknown ui request action"},
		{"open without target", func(r *UIRequest) { r.Target = UIRequestTarget{} }, "no target"},
		{"layout without payload", func(r *UIRequest) {
			r.Action = UIRequestActionLayout
			r.Target = UIRequestTarget{}
			r.Payload = nil
		}, "no payload"},
		{"long target", func(r *UIRequest) { r.Target.Value = strings.Repeat("p", MaxUIRequestTargetBytes+1) }, "target exceeds"},
		{"long origin", func(r *UIRequest) { r.Origin.WorkDir = strings.Repeat("p", MaxUIRequestFieldBytes+1) }, "field exceeds"},
		{"huge payload", func(r *UIRequest) {
			r.Action = UIRequestActionLayout
			r.Target = UIRequestTarget{}
			r.Payload = json.RawMessage(`{"mode":"apply","spec":"` + strings.Repeat("x", MaxUIRequestPayloadBytes) + `"}`)
		}, "exceeds"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			event := valid
			testCase.mutate(&event)
			err := Message{Kind: KindUIRequest, UIRequest: &event}.Validate()
			if err == nil {
				t.Fatalf("%+v validated", event)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error %v is not ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("error %q does not name %q", err, testCase.want)
			}
		})
	}
}

func TestUIRequestMessageWithoutPayloadIsRefused(t *testing.T) {
	if err := (Message{Kind: KindUIRequest}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("an empty ui request message validated: %v", err)
	}
}

func TestEncoderRefusesAnInvalidUIRequest(t *testing.T) {
	var buffer bytes.Buffer
	event := sampleUIRequest()
	event.Action = ""
	if err := NewEncoder(&buffer).Encode(Message{Kind: KindUIRequest, UIRequest: &event}); err == nil {
		t.Fatal("an invalid ui request was encoded")
	}
	if buffer.Len() != 0 {
		t.Errorf("a refused message still wrote %d bytes", buffer.Len())
	}
}

func TestDecoderRefusesAnInvalidUIRequest(t *testing.T) {
	line := `{"proto":2,"kind":"uirequest","seq":1,"uiRequest":{"id":"x","action":"open","ttlMs":1200,"origin":{"tmuxSession":"s"}}}` + "\n"
	_, err := NewDecoder(strings.NewReader(line)).Next()
	if err == nil {
		t.Fatal("a ui request without host id decoded")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error %v is not ErrInvalid", err)
	}
}

// A v2 decoder that predates KindUIRequest still parses the rest of the stream:
// unknown kinds are not a protocol bump, and dropping one line must not lose
// the snapshot that follows.
func TestV2DecoderIgnoresUnknownKindAndKeepsReading(t *testing.T) {
	var buffer bytes.Buffer
	encoder := NewEncoder(&buffer)
	if err := encoder.Encode(Message{Kind: KindHello, Hello: &Hello{Proto: Version}}); err != nil {
		t.Fatal(err)
	}
	event := sampleUIRequest()
	if err := encoder.Encode(Message{Kind: KindUIRequest, UIRequest: &event}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(Message{Kind: KindSnapshot, Snapshot: &Snapshot{Generation: 7}}); err != nil {
		t.Fatal(err)
	}

	type v2Message struct {
		Proto    int          `json:"proto"`
		Kind     string       `json:"kind"`
		Seq      uint64       `json:"seq"`
		Hello    *Hello       `json:"hello,omitempty"`
		Snapshot *Snapshot    `json:"snapshot,omitempty"`
		Event    *Event       `json:"event,omitempty"`
		Error    *Error       `json:"error,omitempty"`
		Notify   *NotifyEvent `json:"notify,omitempty"`
	}
	decoder := json.NewDecoder(&buffer)
	var kinds []string
	var snapshot *Snapshot
	for {
		var msg v2Message
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("v2 decoder lost the stream: %v", err)
		}
		kinds = append(kinds, msg.Kind)
		if msg.Snapshot != nil {
			snapshot = msg.Snapshot
		}
	}
	if len(kinds) != 3 || kinds[0] != string(KindHello) || kinds[1] != string(KindUIRequest) || kinds[2] != string(KindSnapshot) {
		t.Fatalf("kinds = %v", kinds)
	}
	if snapshot == nil || snapshot.Generation != 7 {
		t.Fatalf("snapshot after unknown kind = %+v", snapshot)
	}
}

func TestCurrentDecoderReadsPastUIRequest(t *testing.T) {
	var buffer bytes.Buffer
	encoder := NewEncoder(&buffer)
	event := sampleUIRequest()
	if err := encoder.Encode(Message{Kind: KindHello, Hello: &Hello{Proto: Version}}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(Message{Kind: KindUIRequest, UIRequest: &event}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(Message{Kind: KindSnapshot, Snapshot: &Snapshot{Generation: 3}}); err != nil {
		t.Fatal(err)
	}
	decoder := NewDecoder(&buffer)
	if msg, err := decoder.Next(); err != nil || msg.Kind != KindHello {
		t.Fatalf("hello: %v %+v", err, msg)
	}
	if msg, err := decoder.Next(); err != nil || msg.Kind != KindUIRequest {
		t.Fatalf("uirequest: %v %+v", err, msg)
	}
	msg, err := decoder.Next()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if msg.Kind != KindSnapshot || msg.Snapshot == nil || msg.Snapshot.Generation != 3 {
		t.Fatalf("snapshot = %+v", msg)
	}
}

func TestUnknownKindIsNotAProtocolViolation(t *testing.T) {
	line := `{"proto":2,"kind":"future-kind","seq":4,"at":"2026-08-31T12:00:00Z","future":{"x":1}}` + "\n"
	msg, err := NewDecoder(strings.NewReader(line)).Next()
	if err != nil {
		t.Fatalf("unknown kind must still parse: %v", err)
	}
	if msg.Kind != "future-kind" {
		t.Errorf("kind = %q", msg.Kind)
	}
}

func TestUIRequestRelayV1IsAdditiveOnHello(t *testing.T) {
	line := `{"proto":2,"kind":"hello","seq":1,"hello":{"proto":2,"capabilities":{"verbs":{"contentReadV1":true}}}}` + "\n"
	msg, err := NewDecoder(strings.NewReader(line)).Next()
	if err != nil {
		t.Fatalf("hello without the new verb no longer decodes: %v", err)
	}
	if msg.Hello.Capabilities.Verbs.UIRequestRelayV1 {
		t.Error("a host that never wrote the field was read as supporting ui request relay")
	}
	if !msg.Hello.Capabilities.Verbs.ContentReadV1 {
		t.Error("the capabilities that were present stopped decoding")
	}
}
