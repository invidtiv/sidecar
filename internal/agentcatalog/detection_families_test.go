package agentcatalog

import (
	"strings"
	"testing"
)

// The load-bearing property of a detection-only family is that no surface which
// offers a user a choice can reach it. Getting this wrong shows up as an agent
// in a creation picker that cannot be launched, so every accessor that answers
// "what can Sidecar start" is asserted here rather than assumed from the fact
// that the two lists are separate variables.
func TestDetectionOnlyFamiliesAreNeverOfferedAsAChoice(t *testing.T) {
	for _, family := range DetectionFamilies() {
		t.Run(family.ID, func(t *testing.T) {
			for _, offered := range Families() {
				if offered.ID == family.ID {
					t.Fatalf("%s is in Families(), which is the creation picker's order", family.ID)
				}
			}
			if _, ok := Find(family.ID); ok {
				t.Fatalf("Find(%q) resolved; configuration pages would list it as selectable", family.ID)
			}
			if Known(family.ID) {
				t.Fatalf("Known(%q) is true; it is not a family Sidecar can start", family.ID)
			}
			if _, ok := FindLaunch(family.ID); ok {
				t.Fatalf("FindLaunch(%q) resolved; an execution boundary would try to start it", family.ID)
			}
			if _, ok := Lookup(family.ID); ok {
				t.Fatalf("Lookup(%q) resolved; resume and session binding would treat it as launchable", family.ID)
			}
			if _, err := BuildLaunch(family.ID, nil, false); err == nil {
				t.Fatalf("BuildLaunch(%q) built a command for a family with no Command", family.ID)
			}
			if family.CanResume() {
				t.Fatalf("%s claims a native resume", family.ID)
			}
			if _, err := BuildResume(family.ID, "id", "abc", nil); err == nil {
				t.Fatalf("BuildResume(%q) built a resume", family.ID)
			}
			// Resolve honours an allowlist, and a user can write anything into
			// one. An unknown entry is dropped, and a detection-only id must be
			// dropped the same way rather than smuggled into the picker through
			// configuration.
			if got := Resolve([]string{family.ID}); len(got) != len(Families()) {
				t.Fatalf("Resolve([%q]) = %d families; an allowlist naming nothing selectable must fall back to everything", family.ID, len(got))
			}
			for _, id := range ResolvePicker([]string{family.ID, "claude"}, true) {
				if id == family.ID {
					t.Fatalf("ResolvePicker offered %q", family.ID)
				}
			}
		})
	}
}

// The fields a detection-only family does carry, and the ones it must not.
// Every field left empty here is a cost the family is not paying: a command it
// cannot be launched with, a resume nothing implements, an adapter that would
// have to exist, and a skip-permissions flag that would be a guess.
func TestDetectionOnlyFamiliesCarryOnlyIdentity(t *testing.T) {
	seen := map[string]bool{}
	for _, family := range DetectionFamilies() {
		if strings.TrimSpace(family.ID) == "" || strings.TrimSpace(family.Name) == "" || strings.TrimSpace(family.Short) == "" {
			t.Errorf("%+v is missing an id, a name, or a short label", family)
		}
		if seen[family.ID] {
			t.Errorf("duplicate detection-only family %q", family.ID)
		}
		seen[family.ID] = true
		if family.Command != "" || family.SkipPermissionsArg != "" || family.AdapterID != "" ||
			len(family.ResumeArgs) != 0 || len(family.ResumeKinds) != 0 {
			t.Errorf("%s carries launch, resume or adapter fields: %+v", family.ID, family)
		}
		if _, ok := Find(family.ID); ok {
			t.Errorf("%s is in both halves of the catalog", family.ID)
		}
		for _, alias := range family.Aliases {
			if alias == family.ID {
				t.Errorf("%s repeats its own id in Aliases", family.ID)
			}
		}
	}
	// ConversationAdapterID falls back to ID, which is correct for a family with
	// no adapter only because nothing looks one up for these. Stated so the
	// fallback is not mistaken for a claim that an adapter exists.
	for _, family := range DetectionFamilies() {
		if got := family.ConversationAdapterID(); got != family.ID {
			t.Errorf("%s ConversationAdapterID = %q", family.ID, got)
		}
	}
}

func TestDetectionOnlyReportsExactlyTheSecondList(t *testing.T) {
	for _, family := range DetectionFamilies() {
		if !DetectionOnly(family.ID) {
			t.Errorf("DetectionOnly(%q) = false", family.ID)
		}
		if _, ok := FindDetection(family.ID); !ok {
			t.Errorf("FindDetection(%q) = false", family.ID)
		}
	}
	for _, family := range Families() {
		if DetectionOnly(family.ID) {
			t.Errorf("DetectionOnly(%q) = true for a launchable family", family.ID)
		}
	}
	for _, id := range []string{"", "  ", "aider", "gemini", "omp", "qoder", "unknown"} {
		if DetectionOnly(id) {
			t.Errorf("DetectionOnly(%q) = true", id)
		}
	}
	// The id is Herdr's label, not the prettier product name: "qoder" is one of
	// that agent's process spellings and must not resolve as the family.
	if _, ok := FindDetection("qoder"); ok {
		t.Fatal("FindDetection(\"qoder\") resolved; the family id is qodercli")
	}
}

func TestDetectionFamiliesReturnsACopy(t *testing.T) {
	first := DetectionFamilies()
	if len(first) == 0 {
		t.Fatal("no detection-only families")
	}
	first[0].Name = "mutated"
	if DetectionFamilies()[0].Name == "mutated" {
		t.Fatal("DetectionFamilies aliased catalog storage")
	}
}
