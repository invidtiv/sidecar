package terminalfixture

import (
	"strings"
	"testing"
)

func TestOpenCodeFixtureIsDeterministicPrivacySafeAndRepresentative(t *testing.T) {
	first := NewOpenCode(160, 44)
	second := NewOpenCode(160, 44)
	if first.Frame(3) != second.Frame(3) || string(first.Burst(7)) != string(second.Burst(7)) {
		t.Fatal("fixture generation is not deterministic")
	}
	joined := first.Frame(3) + string(first.Burst(7))
	for _, forbidden := range []string{"/Users/", "marcus", ".local/state", ".config/sidecar", "sidecar-sh-"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("fixture contains forbidden user-shaped token %q", forbidden)
		}
	}
	for _, want := range []string{"\x1b[48;2;18;18;22m", "https://docs.example.test", ExistingGoPath, ExistingDocPath, MissingPath, "history moved"} {
		if !strings.Contains(joined, want) {
			t.Errorf("fixture lacks representative token %q", want)
		}
	}
	if got := strings.Count(first.Frame(3), "\n") + 1; got != first.Height+3 {
		t.Fatalf("history-bearing frame rows = %d, want %d", got, first.Height+3)
	}
}
