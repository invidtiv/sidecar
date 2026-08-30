package agentcontrol

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReadSource names one passive view of a managed agent. Every terminal source
// is a capture: nothing here scrolls, resizes, enters copy mode, or otherwise
// touches the agent's own alternate-screen UI, because a read that changes what
// the agent is showing is a read that changed the thing it measured.
type ReadSource string

const (
	// SourceVisible is the pane's current screen, exactly as it stands.
	SourceVisible ReadSource = "visible"
	// SourceRecent is the visible screen plus recent scrollback.
	SourceRecent ReadSource = "recent"
	// SourceRecentUnwrapped is SourceRecent with soft wraps joined, which is
	// what makes logs and long agent answers usable as text.
	SourceRecentUnwrapped ReadSource = "recent-unwrapped"
	// SourceDetection is the exact slice the lifecycle detector reads, so a
	// caller arguing with a status verdict can see the evidence it saw.
	SourceDetection ReadSource = "detection"
	// SourceTranscript is the provider's own conversation, available only once
	// an exact provider-reported session binding exists.
	SourceTranscript ReadSource = "transcript"
)

// ReadSources lists the sources in the order the CLI documents them.
func ReadSources() []ReadSource {
	return []ReadSource{SourceVisible, SourceRecent, SourceRecentUnwrapped, SourceDetection, SourceTranscript}
}

func (r ReadSource) valid() bool {
	for _, known := range ReadSources() {
		if r == known {
			return true
		}
	}
	return false
}

// ParseReadSource accepts a source name from a caller.
func ParseReadSource(value string) (ReadSource, error) {
	source := ReadSource(strings.ToLower(strings.TrimSpace(value)))
	if !source.valid() {
		names := make([]string, 0, len(ReadSources()))
		for _, known := range ReadSources() {
			names = append(names, string(known))
		}
		return "", fmt.Errorf("unknown source %q; use one of: %s", value, strings.Join(names, ", "))
	}
	return source, nil
}

// DetectionScrollback is how much history the lifecycle detector reads. It is
// stated once so `--source detection` returns the same slice the status verdict
// was computed from rather than a lookalike.
const DetectionScrollback = 80

// ReadRequest is one passive read.
type ReadRequest struct {
	Target Target
	Source ReadSource
	// Lines bounds the result. Zero means the source's own default.
	Lines int
	// ANSI preserves styling where the source has it. Terminal sources without
	// styling and the transcript ignore it.
	ANSI bool
}

// TranscriptMessage is the small stable projection of a provider conversation
// turn. It is deliberately not the conversation adapter's own Message type:
// that shape belongs to the history plugin and would make this JSON contract
// hostage to a UI's needs.
type TranscriptMessage struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at,omitzero"`
}

// ReadResult is the stable read contract.
type ReadResult struct {
	Target     Target              `json:"target"`
	Source     ReadSource          `json:"source"`
	Kind       string              `json:"kind,omitempty"`
	Status     Status              `json:"status,omitempty"`
	Lines      int                 `json:"lines"`
	CapturedAt time.Time           `json:"capturedAt"`
	Text       string              `json:"text,omitempty"`
	Messages   []TranscriptMessage `json:"messages,omitempty"`
}

// TranscriptReader is the injected exact-transcript seam.
//
// It is separate from the terminal adapter on purpose: conversation stores and
// terminal control are independent, and a missing or disabled Conversations
// plugin must never disable start, prompt, wait, or a terminal read. M3 adds
// the binding that supplies an implementation; until then a Service leaves this
// nil and `--source transcript` answers transcript_unavailable rather than
// guessing the newest session with the same working directory.
type TranscriptReader interface {
	SessionMessages(ctx context.Context, target Target, limit int) ([]TranscriptMessage, error)
}
