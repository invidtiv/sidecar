package managedtarget

import (
	"strings"
	"testing"
)

func TestResolvePrefersSessionAndRefusesAmbiguousDisplayName(t *testing.T) {
	candidates := []Target{{Host: "local", Project: "a", Session: "s1", Name: "reviewer"}, {Host: "local", Project: "b", Session: "reviewer", Name: "other"}, {Host: "local", Project: "b", Session: "s2", Name: "reviewer"}}
	got, err := Resolve(candidates, Query{Host: "local", Value: "reviewer"})
	if err != nil || got.Session != "reviewer" {
		t.Fatalf("exact = %+v, %v", got, err)
	}
	_, err = Resolve(candidates, Query{Host: "local", Value: "reviewer", Project: "a"})
	if err != nil {
		t.Fatalf("scoped display: %v", err)
	}
	_, err = Resolve([]Target{candidates[0], candidates[2]}, Query{Host: "local", Value: "reviewer"})
	if e, ok := err.(*Error); !ok || e.Kind != Ambiguous {
		t.Fatalf("err = %T %v", err, err)
	}
}

func TestResolvePreservesRegisteredWorktreeTierAndHostScope(t *testing.T) {
	candidates := []Target{
		{Host: "local", Project: "p", Session: "sidecar-ws-feature", WorktreeRoot: "/registered/feature", Priority: 1},
		{Host: "local", Project: "p", Session: "sidecar-ws-feature", WorktreeRoot: "/discovered/feature", Priority: 2},
		{Host: "remote", Project: "p", Session: "sidecar-ws-feature", WorktreeRoot: "/remote/feature", Priority: 1},
	}
	got, err := Resolve(candidates, Query{Host: "local", Project: "p", Value: "sidecar-ws-feature"})
	if err != nil || got.WorktreeRoot != "/registered/feature" {
		t.Fatalf("registered tier = %+v, %v", got, err)
	}
}

func TestResolveRefusesEqualTierWorktreeSessionCollision(t *testing.T) {
	candidates := []Target{
		{Host: "local", Project: "p", Kind: "worktree", Session: "sidecar-ws-feature", WorktreeRoot: "/registered/one/feature", Priority: 1},
		{Host: "local", Project: "p", Kind: "worktree", Session: "sidecar-ws-feature", WorktreeRoot: "/registered/two/feature", Priority: 1},
	}
	_, err := Resolve(candidates, Query{Host: "local", Project: "p", Value: "sidecar-ws-feature"})
	if e, ok := err.(*Error); !ok || e.Kind != Ambiguous {
		t.Fatalf("equal-tier collision err = %T %v, want ambiguity", err, err)
	}
}

func TestResolveGlobalExplicitRequiresUniqueTarget(t *testing.T) {
	candidates := []Target{{Host: "local", Project: "a", Session: "s1", Name: "reviewer"}, {Host: "local", Project: "b", Session: "s2", Name: "reviewer"}}
	_, err := Resolve(candidates, Query{Host: "local", Value: "reviewer"})
	if e, ok := err.(*Error); !ok || e.Kind != Ambiguous {
		t.Fatalf("global ambiguity = %T %v", err, err)
	}
	got, err := Resolve(candidates, Query{Host: "local", Project: "b", Value: "reviewer"})
	if err != nil || got.Session != "s2" {
		t.Fatalf("project scope = %+v, %v", got, err)
	}
}

func TestResolvePriorityNeverHidesCrossTargetAmbiguity(t *testing.T) {
	tests := map[string][]Target{
		"same display name across mixed project priorities": {
			{Host: "local", Project: "a", Kind: "shell", Session: "s1", Name: "reviewer", Priority: 0},
			{Host: "local", Project: "b", Kind: "worktree", Session: "s2", Name: "reviewer", Priority: 2},
		},
		"shell and worktree display collision in one project": {
			{Host: "local", Project: "a", Kind: "shell", Session: "s1", Name: "reviewer", Priority: 0},
			{Host: "local", Project: "a", Kind: "worktree", Session: "s2", Name: "reviewer", Priority: 1},
		},
		"exact session collision across kinds": {
			{Host: "local", Project: "a", Kind: "shell", Session: "same", Priority: 0},
			{Host: "local", Project: "a", Kind: "worktree", Session: "same", Priority: 1},
		},
	}
	for name, candidates := range tests {
		t.Run(name, func(t *testing.T) {
			value := "reviewer"
			if name == "exact session collision across kinds" {
				value = "same"
			}
			_, err := Resolve(candidates, Query{Host: "local", Value: value})
			if e, ok := err.(*Error); !ok || e.Kind != Ambiguous {
				t.Fatalf("Resolve() err = %T %v, want ambiguity", err, err)
			}
		})
	}
}

func TestAmbiguityNamesTheProjectsAndTheSelector(t *testing.T) {
	candidates := []Target{
		{Host: "local", Project: "sidecar", Session: "sidecar-ws-topic"},
		{Host: "local", Project: "sidecar-2", Session: "sidecar-ws-topic"},
		{Host: "local", Project: ".claude", Session: "sidecar-ws-topic"},
	}
	_, err := Resolve(candidates, Query{Host: "local", Value: "sidecar-ws-topic"})
	e, ok := err.(*Error)
	if !ok || e.Kind != Ambiguous {
		t.Fatalf("err = %T %v", err, err)
	}
	for _, want := range []string{"sidecar, sidecar-2, .claude", "--project", "--shell"} {
		if !strings.Contains(e.Message, want) {
			t.Fatalf("message %q does not name %q", e.Message, want)
		}
	}
	// Two records in ONE project cannot be narrowed by --project, and saying
	// so would send the caller after a flag that changes nothing.
	_, err = Resolve(candidates[:1], Query{Host: "local", Value: "sidecar-ws-topic", Project: "missing"})
	if e, ok := err.(*Error); !ok || e.Kind != NotFound {
		t.Fatalf("scoped miss = %T %v", err, err)
	}
	same := []Target{
		{Host: "local", Project: "p", Kind: "worktree", Session: "sidecar-ws-feature", WorktreeRoot: "/a/feature", Priority: 1},
		{Host: "local", Project: "p", Kind: "worktree", Session: "sidecar-ws-feature", WorktreeRoot: "/b/feature", Priority: 1},
	}
	_, err = Resolve(same, Query{Host: "local", Value: "sidecar-ws-feature"})
	if e, ok := err.(*Error); !ok || strings.Contains(e.Message, "--project") || !strings.Contains(e.Message, `project "p"`) {
		t.Fatalf("same-project message = %v", err)
	}
}
