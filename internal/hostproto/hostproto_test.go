package hostproto

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestEncodeStampsEnvelope(t *testing.T) {
	var buffer bytes.Buffer
	encoder := NewEncoder(&buffer)
	fixed := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	encoder.SetClock(func() time.Time { return fixed })

	for i := 0; i < 3; i++ {
		if err := encoder.Encode(Message{Kind: KindEvent, Event: &Event{Kind: EventStatus}}); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}

	decoder := NewDecoder(&buffer)
	for want := uint64(1); want <= 3; want++ {
		msg, err := decoder.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if msg.Seq != want {
			t.Errorf("seq = %d, want %d", msg.Seq, want)
		}
		if msg.Proto != Version {
			t.Errorf("proto = %d, want %d", msg.Proto, Version)
		}
		if !msg.At.Equal(fixed) {
			t.Errorf("at = %v, want %v", msg.At, fixed)
		}
	}
	if _, err := decoder.Next(); err != io.EOF {
		t.Errorf("trailing read = %v, want EOF", err)
	}
}

// TestEncodeDoesNotEscapeHTML matters because pane captures are full of angle
// brackets and ampersands and there is no browser at the far end. Escaping
// them would bloat every preview and make a raw transcript unreadable.
func TestEncodeDoesNotEscapeHTML(t *testing.T) {
	var buffer bytes.Buffer
	encoder := NewEncoder(&buffer)
	if err := encoder.Encode(Message{Kind: KindSnapshot, Snapshot: &Snapshot{
		Projects: []Project{{Items: []Item{{ID: "x", Preview: `<a href="b"> & 'c'`}}}},
	}}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Go's encoder escapes < > & as \u003c \u003e \u0026 when HTML escaping
	// is on. Their absence, not the absence of a literal "<", is the property.
	for _, escape := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(buffer.String(), escape) {
			t.Errorf("preview was HTML-escaped (%s): %s", escape, buffer.String())
		}
	}
}

// TestDecoderSkipsBlankLines: a remote login shell that emits a stray newline
// before exec'ing sidecar is a real and common condition. It must not look
// like a protocol violation.
func TestDecoderSkipsBlankLines(t *testing.T) {
	stream := "\n\n" + `{"proto":1,"kind":"hello","seq":1,"hello":{"proto":1}}` + "\n\n"
	decoder := NewDecoder(strings.NewReader(stream))
	msg, err := decoder.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if msg.Kind != KindHello {
		t.Errorf("kind = %q, want hello", msg.Kind)
	}
	if _, err := decoder.Next(); err != io.EOF {
		t.Errorf("after blank trailer: %v, want EOF", err)
	}
}

// TestDecoderNamesShellContamination is the failure a first-time user hits.
// A login shell with an interactive preexec hook writes OSC sequences to the
// same stdout the protocol travels on, and the error must say so rather than
// surfacing a bare JSON syntax complaint.
func TestDecoderNamesShellContamination(t *testing.T) {
	contaminated := "\x1b]697;PreExec\x07" + `{"proto":1,"kind":"hello"}` + "\n"
	decoder := NewDecoder(strings.NewReader(contaminated))
	_, err := decoder.Next()
	if err == nil {
		t.Fatal("contaminated line decoded without error")
	}
	if !strings.Contains(err.Error(), "not the protocol") {
		t.Errorf("error %q does not explain what went wrong", err)
	}
}

func TestDecoderRejectsOversizedLine(t *testing.T) {
	huge := `{"proto":1,"kind":"snapshot","x":"` + strings.Repeat("a", MaxLineBytes+16) + `"}`
	decoder := NewDecoder(strings.NewReader(huge))
	_, err := decoder.Next()
	if err == nil {
		t.Fatal("oversized line accepted")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error %q does not name the size limit", err)
	}
}

func TestCompatibilityIsExactAndDirectional(t *testing.T) {
	if !Compatible(Version) {
		t.Fatal("current version is not compatible with itself")
	}
	if Compatible(Version+1) || Compatible(Version-1) {
		t.Error("compatibility is not exact")
	}
	older := IncompatibleMessage("mac-mini", Version-1)
	if !strings.Contains(older, "too old on mac-mini") {
		t.Errorf("older-host message does not point at the host: %q", older)
	}
	newer := IncompatibleMessage("mac-mini", Version+1)
	if !strings.Contains(newer, "too old here") {
		t.Errorf("newer-host message does not point at the local end: %q", newer)
	}
	if IncompatibleMessage("mac-mini", Version) != "" {
		t.Error("a matching version produced an incompatibility message")
	}
}

// TestRoundTripPreservesPresentation guards the field the whole feature exists
// to deliver: the resolved lane a viewer renders.
func TestRoundTripPreservesPresentation(t *testing.T) {
	var buffer bytes.Buffer
	encoder := NewEncoder(&buffer)
	original := Item{
		ID: "p:shell:s", HostID: "mac-mini", Kind: "shell", Name: "Claude pane",
		Provider: "claude", Session: "proj-claude", PaneID: "%3", Live: true,
		Agent: &Presentation{Lane: "blocked", Icon: "◆", Label: "blocked", Attention: true, Evidence: "claude.screen.blocked"},
	}
	if err := encoder.Encode(Message{Kind: KindSnapshot, Snapshot: &Snapshot{Projects: []Project{{Items: []Item{original}}}}}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	msg, err := NewDecoder(&buffer).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	got := msg.Snapshot.Projects[0].Items[0]
	if got.Agent == nil {
		t.Fatal("presentation lost in transit")
	}
	if got.Agent.Lane != "blocked" || !got.Agent.Attention || got.Agent.Evidence != "claude.screen.blocked" {
		t.Errorf("presentation = %+v", got.Agent)
	}
	if got.Session != "proj-claude" {
		t.Errorf("session = %q; a viewer cannot open a control channel without it", got.Session)
	}
}

// TestOlderHostHelloReadsAsNoVerbCapabilities is why the capability set could be
// added without moving Version. An older host's hello has no `verbs` object in
// it at all, and the decoder answers "that host cannot do it" rather than
// failing — which is exactly what a viewer needs before it chooses an argument
// list. A protocol bump would instead have made every un-updated host
// unreachable to fix a flag one verb accepts.
func TestOlderHostHelloReadsAsNoVerbCapabilities(t *testing.T) {
	line := `{"proto":1,"kind":"hello","seq":1,"hello":{"proto":1,"version":"0.9.0","host":"mac-mini","tmuxPresent":true,"capabilities":{"processIdentity":true}}}`
	msg, err := NewDecoder(strings.NewReader(line + "\n")).Next()
	if err != nil {
		t.Fatalf("an older host's hello no longer decodes: %v", err)
	}
	if msg.Hello == nil {
		t.Fatal("no hello decoded")
	}
	if msg.Hello.Capabilities.Verbs.CreateShellAgent {
		t.Error("a host that never wrote the field was read as supporting --agent")
	}
	if msg.Hello.Capabilities.Verbs.ContentReadV1 {
		t.Error("a host that never wrote the field was read as supporting content read")
	}
	if !msg.Hello.Capabilities.ProcessIdentity {
		t.Error("the capabilities that were present stopped decoding")
	}
}

// TestVerbCapabilitiesSurviveTheWire. The field is only useful if it arrives.
func TestVerbCapabilitiesSurviveTheWire(t *testing.T) {
	var buffer bytes.Buffer
	encoder := NewEncoder(&buffer)
	if err := encoder.Encode(Message{Kind: KindHello, Hello: &Hello{
		Proto:        Version,
		Capabilities: Capabilities{Verbs: VerbCapabilities{CreateShellAgent: true, ContentReadV1: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	msg, err := NewDecoder(&buffer).Next()
	if err != nil {
		t.Fatal(err)
	}
	if !msg.Hello.Capabilities.Verbs.CreateShellAgent {
		t.Fatalf("the advertised verb capability did not survive the wire: %s", buffer.String())
	}
	if !msg.Hello.Capabilities.Verbs.ContentReadV1 {
		t.Fatalf("ContentReadV1 did not survive the wire: %s", buffer.String())
	}
}
