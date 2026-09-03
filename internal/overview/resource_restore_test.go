package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/resourceview"
)

// The Sessions twin of TestRelaunchRestoresACollectionTab: this surface saves a
// pane layout per row and reads it back on the next process, so a collection tab
// left open here has to come back here too. A shape that survives on one
// projection and is dropped on the other is the parity bug this phase exists to
// prevent.
func TestSessionsRelaunchRestoresACollectionTab(t *testing.T) {
	m := resourcePreviewModel(t)
	calls := &globalCollectionCalls{}
	run(t, m, m.SetPluginCalls(calls.callsFor()))
	run(t, m, m.OpenPreviewResource(resourceview.Ref{
		Instance: "recall", Collection: "results", Query: "dex",
	}))
	if m.preview.resource == nil {
		t.Fatal("no Resource pane opened for a collection")
	}

	layout := m.sessionsPaneLayoutJSON()
	if layout == nil {
		t.Fatal("a composed preview with a collection tab encoded to nil")
	}

	// The next process: a fresh model reading that record back.
	next := resourcePreviewModel(t)
	run(t, next, next.SetPluginCalls((&globalCollectionCalls{}).callsFor()))
	run(t, next, next.warmPreviewFromLayout("a", layout))

	res := next.preview.resource
	if res == nil || res.tabs == nil {
		t.Fatal("the Resource pane did not come back after a relaunch")
	}
	refs := res.tabs.References()
	if len(refs) != 1 {
		t.Fatalf("restored tabs = %#v, want the one collection tab", refs)
	}
	if refs[0].Provider != "recall" || refs[0].Collection != "results" || refs[0].Query != "dex" {
		t.Fatalf("restored reference = %#v, want the collection and the typed query", refs[0])
	}
	if refs[0].Matcher != "" || refs[0].Locator != "" {
		t.Fatalf("the collection came back wearing the matched shape: %#v", refs[0])
	}
}
