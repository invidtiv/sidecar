package hostproto

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func sampleNotify() NotifyEvent {
	at := time.Date(2026, 8, 30, 9, 0, 4, 0, time.UTC)
	origin := NotifyOrigin{ItemID: "proj:shell:agent-1", ProjectKey: "sidecar", Session: "sidecar-agent-1", Path: "/home/marcus/code/sidecar"}
	return NotifyEvent{
		Key: NotifyKey(origin, NotifyWaiting, at), OccurredAt: at,
		Class: NotifyWaiting, Source: "waiting", Severity: "warning",
		Title: "agent-1 needs input", Body: "claude · sidecar/main", Sticky: true,
		Origin: origin,
	}
}

// The server fixture and the client fixture are the same bytes read from both
// ends: a host encodes, a viewer decodes, and every field the local viewer
// needs to file one notification survives.
func TestNotifyRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	encoder := NewEncoder(&buffer)
	original := sampleNotify()
	if err := encoder.Encode(Message{Kind: KindNotify, Notify: &original}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	msg, err := NewDecoder(&buffer).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if msg.Kind != KindNotify || msg.Notify == nil {
		t.Fatalf("kind = %q, notify = %v", msg.Kind, msg.Notify)
	}
	got := *msg.Notify
	if got.Key != original.Key || got.Class != NotifyWaiting || got.Title != original.Title ||
		got.Body != original.Body || got.Severity != "warning" || got.Source != "waiting" || !got.Sticky {
		t.Errorf("notify = %+v", got)
	}
	if !got.OccurredAt.Equal(original.OccurredAt) {
		t.Errorf("occurredAt = %v, want %v", got.OccurredAt, original.OccurredAt)
	}
	if got.Origin != original.Origin {
		t.Errorf("origin = %+v, want %+v", got.Origin, original.Origin)
	}
}

// The payload is a bounded semantic event. Anything that could turn into an
// instruction on the receiving machine has no field to travel in, and this
// test is what keeps someone from adding one without noticing.
func TestNotifyCarriesNoExecutableOrTerminalContent(t *testing.T) {
	var buffer bytes.Buffer
	event := sampleNotify()
	if err := NewEncoder(&buffer).Encode(Message{Kind: KindNotify, Notify: &event}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	line := buffer.String()
	for _, forbidden := range []string{"preview", "command", "exec", "bundle", "tty", "env", "paneId", "escape"} {
		if strings.Contains(strings.ToLower(line), strings.ToLower(`"`+forbidden)) {
			t.Errorf("notify line carries a %q field: %s", forbidden, line)
		}
	}
}

// Two `sidecar host serve` processes watching one machine poll on their own
// clocks. If the key moved with the observing process, two viewers of the same
// host would each store their own copy of one transition.
func TestNotifyKeyIsStableAcrossObservers(t *testing.T) {
	origin := sampleNotify().Origin
	base := time.Date(2026, 8, 30, 9, 0, 1, 0, time.UTC)
	first := NotifyKey(origin, NotifyWaiting, base)
	second := NotifyKey(origin, NotifyWaiting, base.Add(4*time.Second))
	if first != second {
		t.Errorf("two observers %s apart produced %q and %q", 4*time.Second, first, second)
	}
	if same := NotifyKey(origin, NotifyDone, base); same == first {
		t.Error("a different transition class produced the same key")
	}
	other := origin
	other.Session = "sidecar-agent-2"
	if same := NotifyKey(other, NotifyWaiting, base); same == first {
		t.Error("a different workspace produced the same key")
	}
	// A later turn is a different event. The key must not be an identity for
	// the workspace, or an agent could only ever notify once.
	if later := NotifyKey(origin, NotifyWaiting, base.Add(time.Hour)); later == first {
		t.Error("a transition an hour later produced the same key")
	}
}

// Seq and Generation are facts about one connection, and the key must not be
// derived from either — a key that moved with the connection would give two
// viewers of one host two records for one transition.
//
// The guarantee is structural: NotifyKey takes no such parameter. What is
// worth asserting is that the encoder's own sequence numbering, which does
// advance per message, leaves the key alone.
func TestNotifyKeyIgnoresConnectionFacts(t *testing.T) {
	origin := sampleNotify().Origin
	at := time.Date(2026, 8, 30, 9, 0, 4, 0, time.UTC)
	want := NotifyKey(origin, NotifyFailure, at)

	var buf bytes.Buffer
	encoder := NewEncoder(&buf)
	for i := 0; i < 3; i++ {
		event := sampleNotify()
		event.Key = NotifyKey(origin, NotifyFailure, at)
		event.Class, event.OccurredAt, event.Origin = NotifyFailure, at, origin
		if err := encoder.Encode(Message{Kind: KindNotify, Notify: &event}); err != nil {
			t.Fatal(err)
		}
	}
	decoder := NewDecoder(bytes.NewReader(buf.Bytes()))
	seen := map[uint64]string{}
	for i := 0; i < 3; i++ {
		msg, err := decoder.Next()
		if err != nil {
			t.Fatal(err)
		}
		seen[msg.Seq] = msg.Notify.Key
	}
	if len(seen) != 3 {
		t.Fatalf("expected three distinct sequence numbers, got %v", seen)
	}
	for seq, key := range seen {
		if key != want {
			t.Errorf("message %d carried key %q, want %q — the key moved with the connection", seq, key, want)
		}
	}
}

// Two observers whose clocks disagree by a hair can land either side of a
// bucket boundary and produce two keys for one transition. The store's logical
// dedupe window is the second rule that collapses them; that the two keys
// really are distinct is this package's half of the claim, and that the window
// is wide enough is asserted in internal/hostserve, which sees both packages.
func TestNotifyKeyStraddlingABucketBoundaryProducesDistinctKeys(t *testing.T) {
	origin := sampleNotify().Origin
	boundary := time.Date(2026, 8, 30, 9, 0, 15, 0, time.UTC)
	if before, after := NotifyKey(origin, NotifyWaiting, boundary.Add(-time.Millisecond)), NotifyKey(origin, NotifyWaiting, boundary); before == after {
		t.Fatal("the boundary case the logical-dedupe fallback exists for cannot occur; the reasoning behind it is wrong")
	}
}

func TestNotifyValidationFailsClosed(t *testing.T) {
	valid := sampleNotify()
	cases := []struct {
		name   string
		mutate func(*NotifyEvent)
		want   string
	}{
		{"no key", func(e *NotifyEvent) { e.Key = "" }, "no key"},
		{"long key", func(e *NotifyEvent) { e.Key = strings.Repeat("k", MaxNotifyKeyBytes+1) }, "key exceeds"},
		{"no title", func(e *NotifyEvent) { e.Title = "" }, "no title"},
		{"long title", func(e *NotifyEvent) { e.Title = strings.Repeat("t", MaxNotifyTitleBytes+1) }, "title exceeds"},
		{"long body", func(e *NotifyEvent) { e.Body = strings.Repeat("b", MaxNotifyBodyBytes+1) }, "body exceeds"},
		{"no time", func(e *NotifyEvent) { e.OccurredAt = time.Time{} }, "occurrence time"},
		{"no origin", func(e *NotifyEvent) { e.Origin = NotifyOrigin{} }, "no origin"},
		{"long origin", func(e *NotifyEvent) { e.Origin.Path = strings.Repeat("p", MaxNotifyOriginBytes+1) }, "origin field exceeds"},
		{"unknown class", func(e *NotifyEvent) { e.Class = "restarted" }, "unknown notify class"},
		{"withdrawal with content", func(e *NotifyEvent) { e.Withdraws = "abc" }, "withdrawal carries no notification content"},
		{"transition withdrawal with content", func(e *NotifyEvent) { e.WithdrawsTransition = true }, "withdrawal carries no notification content"},
		{"withdrawal naming both forms", func(e *NotifyEvent) {
			*e = NotifyEvent{Withdraws: "abc", WithdrawsTransition: true, Class: NotifyWaiting, Origin: valid.Origin}
		}, "not both"},
		{"transition withdrawal without an origin", func(e *NotifyEvent) {
			*e = NotifyEvent{WithdrawsTransition: true, Class: NotifyWaiting}
		}, "no origin"},
		{"transition withdrawal of something other than a wait", func(e *NotifyEvent) {
			*e = NotifyEvent{WithdrawsTransition: true, Class: NotifyDone, Origin: valid.Origin}
		}, "only a wait"},
		{"transition withdrawal with an oversized origin", func(e *NotifyEvent) {
			origin := valid.Origin
			origin.Path = strings.Repeat("p", MaxNotifyOriginBytes+1)
			*e = NotifyEvent{WithdrawsTransition: true, Class: NotifyWaiting, Origin: origin}
		}, "origin field exceeds"},
		{"withdrawal by key carrying a transition identity", func(e *NotifyEvent) {
			*e = NotifyEvent{Withdraws: "abc", Class: NotifyWaiting, Origin: valid.Origin}
		}, "no transition identity"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			event := valid
			testCase.mutate(&event)
			err := Message{Kind: KindNotify, Notify: &event}.Validate()
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

// A bounded field violation must not reach a caller as data. The decoder is
// where a viewer's trust in a remote payload begins, so it refuses there.
func TestDecoderRefusesAnOutOfBoundsNotify(t *testing.T) {
	line := `{"proto":2,"kind":"notify","seq":1,"at":"2026-08-30T09:00:00Z","notify":{"key":"k","occurredAt":"2026-08-30T09:00:00Z","class":"waiting","title":"` +
		strings.Repeat("x", MaxNotifyTitleBytes+1) + `","origin":{"itemId":"a"}}}` + "\n"
	_, err := NewDecoder(strings.NewReader(line)).Next()
	if err == nil {
		t.Fatal("an over-long title decoded")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error %v is not ErrInvalid", err)
	}
}

func TestEncoderRefusesAnInvalidNotify(t *testing.T) {
	var buffer bytes.Buffer
	event := sampleNotify()
	event.Class = ""
	if err := NewEncoder(&buffer).Encode(Message{Kind: KindNotify, Notify: &event}); err == nil {
		t.Fatal("an invalid notify was encoded")
	}
	if buffer.Len() != 0 {
		t.Errorf("a refused message still wrote %d bytes", buffer.Len())
	}
}

func TestNotifyMessageWithoutPayloadIsRefused(t *testing.T) {
	if err := (Message{Kind: KindNotify}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("an empty notify message validated: %v", err)
	}
}

func TestWithdrawalRoundTrips(t *testing.T) {
	var buffer bytes.Buffer
	event := NotifyEvent{Withdraws: sampleNotify().Key}
	if err := NewEncoder(&buffer).Encode(Message{Kind: KindNotify, Notify: &event}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	msg, err := NewDecoder(&buffer).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !msg.Notify.IsWithdrawal() || msg.Notify.Withdraws != event.Withdraws {
		t.Errorf("withdrawal = %+v", msg.Notify)
	}
}

// The protocol integer moved for KindNotify, so both directions of the
// mismatch must still name the machine to update. A viewer that silently
// dropped an unknown kind is the failure this bump prevents.
func TestNotifyBumpKeepsTheMismatchActionable(t *testing.T) {
	if Version < 2 {
		t.Fatalf("Version = %d; KindNotify requires a bump past 1", Version)
	}
	older := IncompatibleMessage("mac-mini", 1)
	if !strings.Contains(older, "too old on mac-mini") || !strings.Contains(older, "proto 1") ||
		!strings.Contains(older, "update sidecar there") {
		t.Errorf("v1 host message is not actionable: %q", older)
	}
	newer := IncompatibleMessage("mac-mini", Version+1)
	if !strings.Contains(newer, "too old here") || !strings.Contains(newer, "update sidecar locally") {
		t.Errorf("newer host message is not actionable: %q", newer)
	}
	if Compatible(1) {
		t.Error("a v1 host is compatible with this viewer")
	}
}

func TestBoundNotifyTextSanitizesAndTruncates(t *testing.T) {
	got := BoundNotifyText("agent\x1b]9;pwned\x07 needs\tinput\nnow", MaxNotifyTitleBytes)
	if strings.ContainsAny(got, "\x1b\x07\n\t") {
		t.Errorf("control characters survived: %q", got)
	}
	if got != "agent]9;pwned needs input now" {
		t.Errorf("text = %q", got)
	}
	long := BoundNotifyText(strings.Repeat("é", 200), 21)
	if len(long) > 21 {
		t.Errorf("length = %d, want <= 21", len(long))
	}
	if !isValidUTF8(long) {
		t.Errorf("truncation split a rune: %q", long)
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
