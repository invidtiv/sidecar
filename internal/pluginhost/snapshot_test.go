package pluginhost

import (
	"fmt"
	"slices"
	"testing"

	"github.com/marcus/sidecar/internal/resource"
)

func TestSnapshotOrdering(t *testing.T) {
	store := NewSnapshotStore()
	err := store.Replace([]DescribedSet{
		{Instance: "second", Order: 1, Matchers: []Matcher{
			{ID: "b", Pattern: "B-[0-9]+", Priority: 10},
			{ID: "a", Pattern: "A-[0-9]+", Priority: 10},
		}},
		{Instance: "first", Order: 0, Matchers: []Matcher{
			{ID: "low", Pattern: "L-[0-9]+", Priority: 1},
			{ID: "high", Pattern: "H-[0-9]+", Priority: 100},
		}},
	})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got := ids(store.Current().Matchers())
	// Ascending configured order, then descending priority, then matcher ID.
	want := []string{"first/high", "first/low", "second/a", "second/b"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func ids(ms []CompiledMatcher) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Instance + "/" + m.ID
	}
	return out
}

func TestSnapshotIsImmutableAndGenerational(t *testing.T) {
	store := NewSnapshotStore()
	if store.Current().Generation() != 0 {
		t.Fatal("a fresh store should be generation 0")
	}
	if err := store.Replace([]DescribedSet{{Instance: "a", Matchers: []Matcher{{ID: "m", Pattern: "X-[0-9]+"}}}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	first := store.Current()
	if first.Generation() != 1 || first.Len() != 1 {
		t.Fatalf("first snapshot = gen %d len %d", first.Generation(), first.Len())
	}

	if err := store.Replace([]DescribedSet{{Instance: "a", Matchers: []Matcher{{ID: "m", Pattern: "Y-[0-9]+"}, {ID: "n", Pattern: "Z-[0-9]+"}}}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	second := store.Current()
	if second.Generation() != 2 || second.Len() != 2 {
		t.Fatalf("second snapshot = gen %d len %d", second.Generation(), second.Len())
	}
	// The handle a scan was already holding is untouched.
	if first.Len() != 1 || first.Matchers()[0].Pattern != "X-[0-9]+" {
		t.Fatal("replacing the snapshot mutated the previous one")
	}
	// The returned slice is a copy.
	ms := second.Matchers()
	ms[0] = CompiledMatcher{}
	if second.Matchers()[0].ID == "" {
		t.Fatal("Matchers() handed out the internal slice")
	}
}

func TestSnapshotFailedReplacementKeepsThePreviousOne(t *testing.T) {
	store := NewSnapshotStore()
	if err := store.Replace([]DescribedSet{{Instance: "a", Matchers: []Matcher{{ID: "good", Pattern: "X-[0-9]+"}}}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	good := store.Current()

	cases := []struct {
		name string
		sets []DescribedSet
	}{
		{"invalid re2", []DescribedSet{{Instance: "a", Matchers: []Matcher{{ID: "bad", Pattern: "([a-z"}}}}},
		{"duplicate id", []DescribedSet{{Instance: "a", Matchers: []Matcher{{ID: "d", Pattern: "A-1"}, {ID: "d", Pattern: "B-1"}}}}},
		{"empty id", []DescribedSet{{Instance: "a", Matchers: []Matcher{{ID: "", Pattern: "A-1"}}}}},
		{"too many matchers", []DescribedSet{{Instance: "a", Matchers: manyMatchers(resource.MaxMatchersPerProvider + 1)}}},
		{"too many providers", manySets(resource.MaxProviders + 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Replace(tc.sets); err == nil {
				t.Fatal("expected the replacement to be refused")
			}
			if store.LastError() == nil {
				t.Fatal("the failure was not reported")
			}
			if store.Current() != good {
				t.Fatal("a failed replacement replaced the live snapshot")
			}
		})
	}

	// Recovery clears the recorded failure.
	if err := store.Replace([]DescribedSet{{Instance: "a", Matchers: []Matcher{{ID: "good", Pattern: "X-[0-9]+"}}}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if store.LastError() != nil {
		t.Fatalf("LastError survived a successful replacement: %v", store.LastError())
	}
}

func manyMatchers(n int) []Matcher {
	out := make([]Matcher, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Matcher{ID: fmt.Sprintf("m%d", i), Pattern: fmt.Sprintf("X%d-[0-9]+", i)})
	}
	return out
}

func manySets(n int) []DescribedSet {
	out := make([]DescribedSet, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, DescribedSet{Instance: fmt.Sprintf("p%d", i), Order: i, Matchers: []Matcher{{ID: "m", Pattern: "X-[0-9]+"}}})
	}
	return out
}

func TestSnapshotLookup(t *testing.T) {
	store := NewSnapshotStore()
	_ = store.Replace([]DescribedSet{{Instance: "a", Matchers: []Matcher{{ID: "m", Pattern: `\bCASH-[0-9]+\b`}}}})
	m, ok := store.Current().Lookup("a", "m")
	if !ok {
		t.Fatal("Lookup missed")
	}
	if got := m.Regexp().FindString("see CASH-1245 today"); got != "CASH-1245" {
		t.Fatalf("whole match = %q", got)
	}
	if _, ok := store.Current().Lookup("a", "nope"); ok {
		t.Fatal("Lookup found a matcher that does not exist")
	}
}

func TestValidateDescriptionSanitizesInfo(t *testing.T) {
	desc, err := ValidateDescription("inst", &Info{
		Kind:    "jira\x1b]8;;https://evil.test\x1b\\x\x1b]8;;\x1b\\",
		Name:    "Jira",
		DocsURL: "javascript:alert(1)",
	}, []Matcher{{ID: "m", Pattern: "X-[0-9]+"}})
	if err != nil {
		t.Fatalf("ValidateDescription: %v", err)
	}
	if desc.Info.DocsURL != "" {
		t.Fatalf("javascript docsUrl survived: %q", desc.Info.DocsURL)
	}
	if desc.Info.Kind == "" || desc.Info.Kind[0] == 0x1b {
		t.Fatalf("kind = %q", desc.Info.Kind)
	}
}

func TestValidateDescriptionRefusesUnstorableMatcherID(t *testing.T) {
	// An ID that only survives sanitization in rewritten form would orphan
	// saved references, so it is refused rather than accepted.
	_, err := ValidateDescription("inst", nil, []Matcher{{ID: "iss\x01ue", Pattern: "X-1"}})
	if err == nil {
		t.Fatal("expected a rewritten matcher id to be refused")
	}
}

func TestSnapshotTerminalMatchersPreserveOrderAndCompiledExpressions(t *testing.T) {
	store := NewSnapshotStore()
	err := store.Replace([]DescribedSet{
		{Instance: "second", Order: 1, Matchers: []Matcher{{ID: "b", Pattern: `B-[0-9]+`}}},
		{Instance: "first", Order: 0, Matchers: []Matcher{
			{ID: "low", Pattern: `L-[0-9]+`, Priority: 1},
			{ID: "high", Pattern: `H-[0-9]+`, Priority: 100},
		}},
	})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got := store.Current().TerminalMatchers()
	want := []struct{ provider, id string }{
		{"first", "high"}, {"first", "low"}, {"second", "b"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d matchers, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Provider != w.provider || got[i].ID != w.id {
			t.Errorf("matcher %d = {%q,%q}, want {%q,%q}", i, got[i].Provider, got[i].ID, w.provider, w.id)
		}
		if got[i].Re == nil {
			t.Errorf("matcher %d has no compiled expression", i)
		}
	}
	if !got[0].Re.MatchString("H-12") {
		t.Error("compiled expression does not match what it declared")
	}
}

func TestNilSnapshotYieldsNoTerminalMatchers(t *testing.T) {
	var s *Snapshot
	if got := s.TerminalMatchers(); got != nil {
		t.Fatalf("a nil snapshot must contribute nothing, got %+v", got)
	}
}

// Claims ride on the matchers so the scanner can reclassify a built-in URL
// span only under an instance's own patterns. An instance without claims
// contributes none, and entries are normalized to the lowercase form URL host
// extraction produces.
func TestSnapshotCarriesClaimHostsOnTerminalMatchers(t *testing.T) {
	store := NewSnapshotStore()
	err := store.Replace([]DescribedSet{
		{Instance: "github", Order: 0, ClaimHosts: []string{"GitHub.com", "  ", "https://evil.test"},
			Matchers: []Matcher{{ID: "url", Pattern: `x`}}},
		{Instance: "plain", Order: 1, Matchers: []Matcher{{ID: "m", Pattern: `y`}}},
	})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	snap := store.Current()
	if got := snap.ClaimHosts("github"); !slices.Equal(got, []string{"github.com"}) {
		t.Fatalf("github claims = %v, want [github.com]", got)
	}
	if got := snap.ClaimHosts("plain"); got != nil {
		t.Fatalf("plain claims = %v, want none", got)
	}
	if got := snap.ClaimHosts("absent"); got != nil {
		t.Fatalf("absent claims = %v, want none", got)
	}
	for _, m := range snap.TerminalMatchers() {
		switch m.Provider {
		case "github":
			if !slices.Equal(m.ClaimHosts, []string{"github.com"}) {
				t.Errorf("%s/%s claimHosts = %v", m.Provider, m.ID, m.ClaimHosts)
			}
		case "plain":
			if m.ClaimHosts != nil {
				t.Errorf("%s/%s claimHosts = %v, want none", m.Provider, m.ID, m.ClaimHosts)
			}
		}
	}
}

// A replacement that drops an instance's claim must drop it everywhere at once:
// claims and matchers are one snapshot.
func TestSnapshotReplacementReplacesClaimHostsAtomically(t *testing.T) {
	store := NewSnapshotStore()
	set := func(claims []string) {
		t.Helper()
		err := store.Replace([]DescribedSet{{
			Instance: "a", Order: 0, ClaimHosts: claims,
			Matchers: []Matcher{{ID: "m", Pattern: `A-[0-9]+`}},
		}})
		if err != nil {
			t.Fatalf("Replace: %v", err)
		}
	}
	set([]string{"old.example.com"})
	set(nil)
	if got := store.Current().ClaimHosts("a"); got != nil {
		t.Fatalf("claims survived a replacement that dropped them: %v", got)
	}
}
