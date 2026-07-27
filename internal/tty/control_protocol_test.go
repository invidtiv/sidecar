package tty

import (
	"strings"
	"testing"
)

func TestControlParserFramesResponsesAndInterleavedOutput(t *testing.T) {
	var parser controlParser
	lines := []string{
		"%output %12 hello\\040world\\012",
		"%begin 100 7 0",
		"first",
		"%output %12 pane content, not a notification",
		"%begin pane text, not a nested frame",
		"%end 100 7 0",
	}
	var events []controlEvent
	for _, line := range lines {
		events = append(events, parser.FeedLine(line)...)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v, want output + response", events)
	}
	if events[0].Kind != controlEventOutput || events[0].Pane != "%12" ||
		string(events[0].Data) != "hello world\n" {
		t.Fatalf("output event = %#v", events[0])
	}
	if events[1].Kind != controlEventResponse ||
		strings.Join(events[1].Response.Lines, "\n") !=
			"first\n%output %12 pane content, not a notification\n%begin pane text, not a nested frame" {
		t.Fatalf("response event = %#v", events[1])
	}
}

func TestControlParserErrorFrameAndUnsolicitedAttachBlock(t *testing.T) {
	var parser controlParser
	// The initial attach response is a normal response even when no application
	// command is pending; correlation policy lives in the transport.
	parser.FeedLine("%begin 10 1 0")
	parser.FeedLine("attached")
	events := parser.FeedLine("%end 10 1 0")
	if len(events) != 1 || events[0].Response.Err != nil {
		t.Fatalf("attach response = %#v", events)
	}

	parser.FeedLine("%begin 11 2 0")
	parser.FeedLine("bad target")
	events = parser.FeedLine("%error 11 2 0")
	if len(events) != 1 || events[0].Response.Err == nil {
		t.Fatalf("error response = %#v", events)
	}
}

func TestControlParserExtendedOutputAndFlowEvents(t *testing.T) {
	var parser controlParser
	tests := []struct {
		line string
		kind controlEventKind
		pane string
		data string
	}{
		{"%extended-output %3 0 : a\\033b", controlEventOutput, "%3", "a\x1bb"},
		{"%layout-change @1 deadbeef", controlEventLayout, "", ""},
		{"%window-pane-changed @1 %9", controlEventLayout, "%9", ""},
		{"%pause %3", controlEventPause, "%3", ""},
		{"%continue %3", controlEventContinue, "%3", ""},
		{"%exit detached", controlEventExit, "", ""},
	}
	for _, tt := range tests {
		events := parser.FeedLine(tt.line)
		if len(events) != 1 || events[0].Kind != tt.kind ||
			events[0].Pane != tt.pane || string(events[0].Data) != tt.data {
			t.Errorf("FeedLine(%q) = %#v", tt.line, events)
		}
	}
}

func TestControlParserHandlesLongLines(t *testing.T) {
	var parser controlParser
	payload := strings.Repeat("x", 256*1024)
	events := parser.FeedLine("%output %1 " + payload)
	if len(events) != 1 || len(events[0].Data) != len(payload) {
		t.Fatalf("long output length = %d, want %d", len(events[0].Data), len(payload))
	}
}

func TestDecodeControlBytesPreservesUnknownEscapes(t *testing.T) {
	got := string(decodeControlBytes(`a\\b\040c\q`))
	if got != "a\\b c\\q" {
		t.Fatalf("decoded = %q", got)
	}
}
