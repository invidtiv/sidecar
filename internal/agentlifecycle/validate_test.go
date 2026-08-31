package agentlifecycle

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/agentactivity"
)

var validateNow = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func validReport() Report {
	return Report{
		SchemaVersion: SchemaVersion,
		ID:            "rpt-1",
		Kind:          KindState,
		Identity: Identity{
			Host:              "host-a",
			ServerIncarnation: "inc-1",
			PaneID:            "%7",
			Provider:          "opencode",
			RunID:             "run-1",
			ProcessGeneration: "gen-1",
		},
		Source:        "sidecar.opencode.plugin",
		SourceVersion: "1",
		Sequence:      7,
		State:         agentactivity.StateWorking,
		ObservedAt:    validateNow.Add(-time.Second),
		Reason:        ReasonTurnStart,
	}
}

func TestValidateAcceptsAHealthyReport(t *testing.T) {
	if err := Validate(validReport(), validateNow); err != nil {
		t.Fatal(err)
	}
}

// TestValidateRejects is the bounds and vocabulary gate. Provider hook input is
// untrusted local data and this is the seam it enters through, so every rule
// that keeps a malformed or oversized record out of the store is stated here
// rather than left to the CLI's flag parsing — which the next caller would not
// go through.
func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Report)
	}{
		{"a future schema version", func(r *Report) { r.SchemaVersion = SchemaVersion + 1 }},
		{"a missing id", func(r *Report) { r.ID = "" }},
		{"an unknown kind", func(r *Report) { r.Kind = "invented" }},
		{"a state report asserting unknown", func(r *Report) { r.State = agentactivity.StateUnknown }},
		{"a state report with no state", func(r *Report) { r.State = "" }},
		{"a state report carrying an outcome", func(r *Report) { r.Outcome = OutcomeCompleted }},
		{"an end report with no outcome", func(r *Report) { r.Kind = KindEnd; r.State = "" }},
		{"an end report carrying a state", func(r *Report) { r.Kind = KindEnd; r.Outcome = OutcomeCompleted }},
		{"a release carrying a state", func(r *Report) { r.Kind = KindRelease }},
		{"a session report carrying an outcome", func(r *Report) {
			r.Kind = KindSession
			r.State = ""
			r.Outcome = OutcomeUnknown
		}},
		{"a reason outside the allowlist", func(r *Report) { r.Reason = "something_the_vendor_made_up" }},
		{"a missing source", func(r *Report) { r.Source = "" }},
		{"an oversized source", func(r *Report) { r.Source = strings.Repeat("s", MaxSourceBytes+1) }},
		{"a sequence beyond the bound", func(r *Report) { r.Sequence = MaxSequence + 1 }},
		{"a missing host", func(r *Report) { r.Identity.Host = "" }},
		{"a missing pane", func(r *Report) { r.Identity.PaneID = "" }},
		{"a missing server incarnation", func(r *Report) { r.Identity.ServerIncarnation = "" }},
		{"a missing run", func(r *Report) { r.Identity.RunID = "" }},
		{"a missing process generation", func(r *Report) { r.Identity.ProcessGeneration = "" }},
		{"an oversized identity field", func(r *Report) {
			r.Identity.RunID = strings.Repeat("r", MaxIdentityFieldBytes+1)
		}},
		{"a control character in an identity field", func(r *Report) { r.Identity.PaneID = "%7\x1b[31m" }},
		{"invalid utf-8 in an identity field", func(r *Report) { r.Identity.Provider = "open\xffcode" }},
		{"a zero observation time", func(r *Report) { r.ObservedAt = time.Time{} }},
		{"a report from too far in the future", func(r *Report) {
			r.ObservedAt = validateNow.Add(MaxClockSkew + time.Minute)
		}},
		{"a report from too far in the past", func(r *Report) {
			r.ObservedAt = validateNow.Add(-MaxClockSkew - time.Minute)
		}},
		{"an unsanitized detail", func(r *Report) { r.Detail = "one\ntwo" }},
		{"an oversized detail", func(r *Report) { r.Detail = strings.Repeat("d", MaxDetailBytes+1) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := validReport()
			tc.edit(&r)
			err := Validate(r, validateNow)
			if err == nil {
				t.Fatal("accepted")
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestValidateAcceptsEveryKindInItsProperShape(t *testing.T) {
	for _, k := range Kinds() {
		t.Run(string(k), func(t *testing.T) {
			r := validReport()
			r.Kind = k
			switch k {
			case KindState:
			case KindEnd:
				r.State = ""
				r.Outcome = OutcomeCancelled
			default:
				r.State = ""
			}
			if err := Validate(r, validateNow); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateAcceptsEveryFrozenReason(t *testing.T) {
	for _, code := range Reasons() {
		r := validReport()
		r.Reason = code
		if err := Validate(r, validateNow); err != nil {
			t.Fatalf("reason %q rejected: %v", code, err)
		}
	}
}

// TestSanitizeDetail covers the one free-form field in the record. It cannot
// detect a secret — nothing can tell a password from a word — so what it
// guarantees is narrower and worth being precise about: whatever ends up
// stored is bounded, is valid UTF-8, cannot break the JSONL line framing, and
// cannot render as something other than itself in a terminal.
func TestSanitizeDetail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"a plain code is untouched", "permission_request", "permission_request"},
		{"newlines become separators", "one\ntwo", "one two"},
		{"tabs and returns become separators", "one\t\r\ntwo", "one two"},
		{"runs of whitespace collapse", "one   \n\n  two", "one two"},
		{"ansi escapes are removed", "\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"leading and trailing space is trimmed", "  spaced  ", "spaced"},
		{"bidi overrides are removed", "safe\u202etxet", "safetxet"},
		{"a lone null is removed", "a\x00b", "ab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeDetail(tc.in); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("output is always within the bound and valid utf-8", func(t *testing.T) {
		// Multi-byte runes so truncation lands mid-rune unless it is handled.
		got := SanitizeDetail(strings.Repeat("é", MaxDetailBytes))
		if len(got) > MaxDetailBytes {
			t.Fatalf("len = %d, limit %d", len(got), MaxDetailBytes)
		}
		if !utf8.ValidString(got) {
			t.Fatal("truncation split a rune")
		}
	})

	t.Run("sanitizing is idempotent", func(t *testing.T) {
		// Validate refuses anything that is not already sanitized, so a value
		// that changed on a second pass would make a legitimately-sanitized
		// record unstorable.
		for _, in := range []string{"one\ntwo", "\x1b[31mred", strings.Repeat("é", 300), "  x  "} {
			once := SanitizeDetail(in)
			if twice := SanitizeDetail(once); twice != once {
				t.Fatalf("not idempotent: %q -> %q -> %q", in, once, twice)
			}
			if err := Validate(withDetail(validReport(), once), validateNow); err != nil {
				t.Fatalf("a sanitized detail was rejected: %v", err)
			}
		}
	})
}

func withDetail(r Report, detail string) Report {
	r.Detail = detail
	return r
}
