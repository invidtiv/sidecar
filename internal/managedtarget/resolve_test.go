package managedtarget

import "testing"

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
