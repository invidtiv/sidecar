package cli

import (
	"errors"
	"testing"

	"github.com/marcus/sidecar/internal/agentsession"
)

// TestAMistypedKindIsUnsupportedRatherThanAMismatch keeps the refusal honest
// about which thing was wrong.
//
// The kind gate compares --kind to the provider occupying the pane. Before the
// catalog lookup, a value that named no provider at all reached that comparison
// and came back as kind_mismatch, blaming the pane for a claim that could never
// have matched anything. The lookup also folds in the alias vocabulary: the
// conversation adapters call Claude "claude-code" and Cursor "cursor-cli", and
// those name the same providers, not different ones.
func TestAMistypedKindIsUnsupportedRatherThanAMismatch(t *testing.T) {
	t.Run("canonical ids pass through unchanged", func(t *testing.T) {
		for _, id := range []string{"claude", "codex", "opencode", "pi", "grok", "cursor", "antigravity", "amp", "copilot"} {
			got, err := resolveReportedKind(id)
			if err != nil {
				t.Fatalf("resolveReportedKind(%q) = %v", id, err)
			}
			if got != id {
				t.Fatalf("resolveReportedKind(%q) = %q, want it unchanged", id, got)
			}
		}
	})

	t.Run("aliases resolve to their family", func(t *testing.T) {
		cases := map[string]string{
			"claude-code": "claude",
			"cursor-cli":  "cursor",
			"pi-agent":    "pi",
			"agy":         "antigravity",
			// Whitespace a hook picked up from its own configuration must not
			// decide the provider either.
			"  codex ": "codex",
		}
		for claim, want := range cases {
			got, err := resolveReportedKind(claim)
			if err != nil {
				t.Fatalf("resolveReportedKind(%q) = %v", claim, err)
			}
			if got != want {
				t.Fatalf("resolveReportedKind(%q) = %q, want %q", claim, got, want)
			}
		}
	})

	t.Run("an unknown kind names its own problem", func(t *testing.T) {
		for _, claim := range []string{"", "   ", "claude-cli", "clyde", "Claude Code"} {
			got, err := resolveReportedKind(claim)
			if err == nil {
				t.Fatalf("resolveReportedKind(%q) = %q, wanted a refusal", claim, got)
			}
			if !errors.Is(err, agentsession.ErrUnsupportedKind) {
				t.Fatalf("resolveReportedKind(%q) error = %v, want ErrUnsupportedKind", claim, err)
			}
			// The sentinel is what the JSON code is derived from, so pin the
			// code the caller actually sees, not only the sentinel.
			if code := reportSessionCode(err); code != "unsupported_kind" {
				t.Fatalf("resolveReportedKind(%q) reported code %q, want unsupported_kind", claim, code)
			}
		}
	})
}
