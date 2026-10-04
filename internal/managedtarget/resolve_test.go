package managedtarget

import (
	"strings"
	"testing"
)

func TestResolveMissingSessionNeverFallsBackToDisplayName(t *testing.T) {
	candidates := []Target{{Host: "local", Project: "p", Session: "live", Name: "sidecar-sh-deleted"}}
	_, err := Resolve(candidates, Query{Host: "local", Value: "sidecar-sh-deleted"})
	if e, ok := err.(*Error); !ok || e.Kind != NotFound {
		t.Fatalf("stale session resolved to collision: %v", err)
	}
}

func TestResolveBareTargetCompatibility(t *testing.T) {
	for _, name := range []string{"rev U3-c", "Shell 3"} {
		candidates := []Target{{Session: "sidecar-sh-demo-3", Name: name}}
		got, err := Resolve(candidates, Query{Value: name})
		if err != nil || got.Session != candidates[0].Session {
			t.Fatalf("unique bare display name %q: %+v %v", name, got, err)
		}
		_, err = Resolve(append(candidates, Target{Session: "sidecar-sh-demo-4", Name: name}), Query{Value: name})
		if e, ok := err.(*Error); !ok || e.Kind != Ambiguous {
			t.Fatalf("ambiguous bare display name %q: %v", name, err)
		}
		got, err = Resolve(append(candidates, Target{Session: name, Name: "Other"}), Query{Value: name})
		if err != nil || got.Session != name {
			t.Fatalf("exact session must win for %q: %+v %v", name, got, err)
		}
		_, err = Resolve(candidates, Query{Value: SessionSelector(name)})
		if e, ok := err.(*Error); !ok || e.Kind != NotFound {
			t.Fatalf("explicit session fell back for %q: %v", name, err)
		}
	}
	for _, name := range []string{"sidecar-sh-deleted", "sidecar-ws-deleted", "sidecar-tp-deleted"} {
		candidates := []Target{{Session: "live", Name: name}}
		_, err := Resolve(candidates, Query{Value: name})
		if e, ok := err.(*Error); !ok || e.Kind != NotFound || !strings.Contains(e.Message, "session") {
			t.Fatalf("missing managed session %q must clearly refuse: %v", name, err)
		}
		got, err := Resolve(candidates, Query{Value: "name:" + name})
		if err != nil || got.Session != "live" {
			t.Fatalf("explicit display name %q: %+v %v", name, got, err)
		}
	}
}

func TestResolvePrefersSessionAndRefusesAmbiguousDisplayName(t *testing.T) {
	candidates := []Target{{Host: "local", Project: "a", Session: "s1", Name: "reviewer"}, {Host: "local", Project: "b", Session: "reviewer", Name: "other"}, {Host: "local", Project: "b", Session: "s2", Name: "reviewer"}}
	got, err := Resolve(candidates, Query{Host: "local", Value: "reviewer"})
	if err != nil || got.Session != "reviewer" {
		t.Fatalf("exact = %+v, %v", got, err)
	}
	_, err = Resolve(candidates, Query{Host: "local", Value: "name:reviewer", Project: "a"})
	if err != nil {
		t.Fatalf("scoped display: %v", err)
	}
	_, err = Resolve([]Target{candidates[0], candidates[2]}, Query{Host: "local", Value: "name:reviewer"})
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
	_, err := Resolve(candidates, Query{Host: "local", Value: "name:reviewer"})
	if e, ok := err.(*Error); !ok || e.Kind != Ambiguous {
		t.Fatalf("global ambiguity = %T %v", err, err)
	}
	got, err := Resolve(candidates, Query{Host: "local", Project: "b", Value: "name:reviewer"})
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
			value := "name:reviewer"
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

func TestResolveExplicitNamesAndLiteralSessionEscapes(t *testing.T) {
	candidates := []Target{{Session: "name:deleted", Name: "Other"}, {Session: "live", Name: "deleted"}}
	got, err := Resolve(candidates, Query{Value: "session:name:deleted"})
	if err != nil || got.Session != "name:deleted" {
		t.Fatalf("literal session: %+v %v", got, err)
	}
	got, err = Resolve(candidates, Query{Value: "name:deleted"})
	if err != nil || got.Session != "live" {
		t.Fatalf("human name: %+v %v", got, err)
	}
	_, err = Resolve(append(candidates, Target{Session: "another", Name: "deleted"}), Query{Value: "name:deleted"})
	if e, ok := err.(*Error); !ok || e.Kind != Ambiguous {
		t.Fatalf("named collision did not refuse: %v", err)
	}
}
