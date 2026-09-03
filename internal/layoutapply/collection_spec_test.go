package layoutapply

import (
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/uirequest"
)

// A resource pane in a spec may name a collection instead of a locator. No
// matcher is consulted for it: there is no span a matcher could have claimed,
// and a row is addressed by its collection and ID.
func TestResolveTargetsAcceptsACollectionPane(t *testing.T) {
	targets, refusal := ResolveTargets(panelayout.Resource, uirequest.LayoutPane{
		Kind: "resource", Provider: "recall", Collection: "results", Query: "dex",
	}, "", nil)
	if refusal != "" {
		t.Fatalf("a collection pane was refused: %s", refusal)
	}
	if len(targets) != 1 {
		t.Fatalf("resolved %d targets, want 1", len(targets))
	}
	got := targets[0]
	if got.Provider != "recall" || got.Collection != "results" || got.Query != "dex" {
		t.Fatalf("target = %+v, want the collection and its query", got)
	}
	if got.Matcher != "" || got.Value != "" {
		t.Fatalf("a collection target carried document fields: %+v", got)
	}
}

// One target beside a collection is the row inside it.
func TestResolveTargetsAcceptsARowOfACollection(t *testing.T) {
	targets, refusal := ResolveTargets(panelayout.Resource, uirequest.LayoutPane{
		Kind: "resource", Provider: "ongoing", Collection: "projects", Targets: []string{"recall"},
	}, "", nil)
	if refusal != "" {
		t.Fatalf("a row pane was refused: %s", refusal)
	}
	if targets[0].Value != "recall" || targets[0].Collection != "projects" {
		t.Fatalf("target = %+v, want the row of that collection", targets[0])
	}
}

// The spec grammar refuses what it cannot mean, before anything opens.
func TestSpecValidationForCollectionPanes(t *testing.T) {
	cases := []struct {
		name     string
		pane     uirequest.LayoutPane
		contains string
	}{
		{
			name:     "a query with no collection",
			pane:     uirequest.LayoutPane{Kind: "resource", Provider: "recall", Query: "dex"},
			contains: "needs a \"collection\"",
		},
		{
			name: "more rows than one",
			pane: uirequest.LayoutPane{Kind: "resource", Provider: "recall", Collection: "results",
				Targets: []string{"a", "b"}},
			contains: "at most one target",
		},
		{
			name:     "a resource pane with neither",
			pane:     uirequest.LayoutPane{Kind: "resource", Provider: "recall"},
			contains: "at least one target",
		},
		{
			name:     "no instance",
			pane:     uirequest.LayoutPane{Kind: "resource", Collection: "results"},
			contains: "configured \"provider\" instance",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := uirequest.LayoutSpec{Columns: []uirequest.LayoutSpecColumn{
				{Panes: []uirequest.LayoutPane{{Kind: "primary"}}},
				{Panes: []uirequest.LayoutPane{tc.pane}},
			}}
			err := uirequest.ValidateLayoutSpec(spec)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if got := err.Error(); !contains(got, tc.contains) {
				t.Fatalf("refusal %q does not say %q", got, tc.contains)
			}
		})
	}
}

// A collection pane is a valid spec, so the grammar's acceptance is asserted as
// well as its refusals.
func TestSpecAcceptsACollectionPane(t *testing.T) {
	spec := uirequest.LayoutSpec{Columns: []uirequest.LayoutSpecColumn{
		{Panes: []uirequest.LayoutPane{{Kind: "primary"}}},
		{Panes: []uirequest.LayoutPane{{
			Kind: "resource", Provider: "recall", Collection: "results", Query: "dex",
		}}},
	}}
	if err := uirequest.ValidateLayoutSpec(spec); err != nil {
		t.Fatalf("a collection pane was refused: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
