package resource

import (
	"strings"
	"testing"
)

// A reference is exactly one shape. Which one is not something the host may
// infer: a record naming both a matcher and a collection, or naming neither, is
// how a restored tab silently becomes a different tab.
func TestReferenceIsExactlyOneShape(t *testing.T) {
	cases := []struct {
		name  string
		ref   Reference
		shape Shape
		valid bool
	}{
		{
			name:  "matched document, the frozen protocol's only shape",
			ref:   Reference{Instance: "jira", Matcher: "issue-key", Locator: "CASH-1245"},
			shape: ShapeMatched, valid: true,
		},
		{
			name:  "collection tab",
			ref:   Reference{Instance: "recall", Collection: "results", Query: "dex"},
			shape: ShapeCollection, valid: true,
		},
		{
			name:  "one row of a collection",
			ref:   Reference{Instance: "recall", Collection: "results", Locator: "rc:notes:1"},
			shape: ShapeItem, valid: true,
		},
		{
			name:  "both a matcher and a collection is ambiguous",
			ref:   Reference{Instance: "recall", Matcher: "key", Locator: "X-1", Collection: "results"},
			shape: ShapeInvalid, valid: false,
		},
		{
			name:  "a matcher with no locator is neither shape",
			ref:   Reference{Instance: "jira", Matcher: "issue-key"},
			shape: ShapeInvalid, valid: false,
		},
		{
			name:  "a locator with no matcher and no collection is neither shape",
			ref:   Reference{Instance: "jira", Locator: "CASH-1245"},
			shape: ShapeInvalid, valid: false,
		},
		{
			name:  "an instance alone is nothing",
			ref:   Reference{Instance: "recall"},
			shape: ShapeInvalid, valid: false,
		},
		{
			name:  "a collection with no instance has no plugin to ask",
			ref:   Reference{Collection: "results"},
			shape: ShapeCollection, valid: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ref.Shape(); got != tc.shape {
				t.Errorf("Shape() = %v, want %v", got, tc.shape)
			}
			if got := tc.ref.Valid(); got != tc.valid {
				t.Errorf("Valid() = %v, want %v", got, tc.valid)
			}
		})
	}
}

// The view position is user-typed text that survives a relaunch, so it is
// bounded on the way out as well as on the way in.
func TestCollectionViewPositionIsBounded(t *testing.T) {
	base := Reference{Instance: "recall", Collection: "results"}
	if !base.Valid() {
		t.Fatal("the plain collection reference is not valid")
	}
	over := base
	over.Query = strings.Repeat("q", MaxQueryChars+1)
	if over.Valid() {
		t.Error("a query past the bound was accepted")
	}
	over = base
	over.Collection = strings.Repeat("c", MaxCollectionIDChars+1)
	if over.Valid() {
		t.Error("a collection id past the bound was accepted")
	}
	over = base
	over.View = strings.Repeat("v", MaxViewIDChars+1)
	if over.Valid() {
		t.Error("a view id past the bound was accepted")
	}
	over = base
	over.Sort = strings.Repeat("s", MaxSortIDChars+1)
	if over.Valid() {
		t.Error("a sort id past the bound was accepted")
	}
	over = base
	over.CursorID = strings.Repeat("i", MaxIdentityChars+1)
	if over.Valid() {
		t.Error("a cursor id past the bound was accepted")
	}
}

// IsPlugin is what decides whether a tab renders as the shared browser or as
// the resource card. A matched document must never answer yes to it.
func TestIsPluginNamesOnlyThePluginShapes(t *testing.T) {
	if (Reference{Instance: "jira", Matcher: "k", Locator: "X-1"}).IsPlugin() {
		t.Error("a matched document claimed to be a plugin shape")
	}
	if !(Reference{Instance: "r", Collection: "results"}).IsPlugin() {
		t.Error("a collection tab did not claim to be a plugin shape")
	}
	if !(Reference{Instance: "r", Collection: "results", Locator: "row"}).IsPlugin() {
		t.Error("a row tab did not claim to be a plugin shape")
	}
}
