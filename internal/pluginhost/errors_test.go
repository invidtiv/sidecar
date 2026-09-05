package pluginhost

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/resource"
)

// The three reasons a wrong executable produces must not share one message.
//
// This is the failure a second, older build of the right tool causes: a bare
// command name resolves against whatever PATH Sidecar was started with, and a
// build that predates the plugin subcommand refuses it, exits non-zero, and
// writes nothing. Reporting that as "returned something Sidecar could not use"
// describes a protocol bug the provider does not have, and sends the reader to
// the plugin's source instead of to `which`.
func TestTransportErrorsThatAWrongExecutableCausesEachSpeakForThemselves(t *testing.T) {
	message := func(reason TransportReason) string {
		err := (&TransportError{Instance: "recall", Method: "describe", Reason: reason}).ResourceError()
		return err.Message
	}

	shared := message(ReasonShape)
	for _, reason := range []TransportReason{ReasonSpawn, ReasonExit, ReasonMalformed, ReasonProtocol} {
		if got := message(reason); got == shared {
			t.Fatalf("%s still shares the catch-all message %q", reason, got)
		}
	}

	// Each of them says what actually happened, in its own words.
	if got := message(ReasonExit); !strings.Contains(got, "exited") {
		t.Fatalf("exit message = %q, want it to say the command exited", got)
	}
	if got := message(ReasonMalformed); !strings.Contains(got, "response") {
		t.Fatalf("malformed message = %q, want it to say no readable response", got)
	}

	// The catch-all keeps its own wording for the reasons it is true of: a
	// response that arrived and was the wrong shape or size.
	for _, reason := range []TransportReason{ReasonOversize, ReasonExtraOutput, ReasonInvalidResource} {
		if got := message(reason); got != shared {
			t.Fatalf("%s = %q, want the catch-all %q", reason, got, shared)
		}
	}
}

// Every reason a wrong executable causes carries the one command that prints
// the file the name resolved to, because that is the next thing to run.
func TestWrongExecutableErrorsPointAtPluginCheck(t *testing.T) {
	for _, reason := range []TransportReason{ReasonSpawn, ReasonExit, ReasonMalformed, ReasonProtocol} {
		err := (&TransportError{Instance: "recall", Method: "describe", Reason: reason}).ResourceError()
		if want := "sidecar plugin check recall"; err.SetupHint != want {
			t.Fatalf("%s hint = %q, want %q", reason, err.SetupHint, want)
		}
	}

	// A reason that says nothing about which binary answered gets no hint.
	if err := (&TransportError{Instance: "recall", Reason: ReasonTimeout}).ResourceError(); err.SetupHint != "" {
		t.Fatalf("a timeout offered %q", err.SetupHint)
	}

	// The hint names an instance, so an error without one has nothing to say.
	if err := (&TransportError{Reason: ReasonExit}).ResourceError(); err.SetupHint != "" {
		t.Fatalf("an instance-less error offered %q", err.SetupHint)
	}
}

// The codes each reason maps onto are the contract the cards are built from,
// and splitting the messages must not have moved one.
func TestTransportErrorCodesAreUnchangedByTheSplit(t *testing.T) {
	want := map[TransportReason]resource.Code{
		ReasonSpawn:     resource.CodeInvalidConfig,
		ReasonProtocol:  resource.CodeInvalidConfig,
		ReasonTimeout:   resource.CodeUnavailable,
		ReasonCanceled:  resource.CodeUnavailable,
		ReasonExit:      resource.CodeInternal,
		ReasonMalformed: resource.CodeInternal,
		ReasonShape:     resource.CodeInternal,
	}
	for reason, code := range want {
		if got := (&TransportError{Reason: reason}).ResourceError().Code; got != code {
			t.Fatalf("%s code = %s, want %s", reason, got, code)
		}
	}
}
