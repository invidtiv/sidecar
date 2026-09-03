package resourceview

import "testing"

func persists(host *fakeHost) int {
	n := 0
	for _, e := range host.events {
		if e == "persist" {
			n++
		}
	}
	return n
}

// A save costs a synchronous state.json write on the project surface, and every
// consumed key asks for one. Typing into a collection pane's query line must
// therefore not be one write per character: the pane saves when what would be
// written has changed, and not otherwise.
func TestAKeyThatChangesNothingCostsNoSave(t *testing.T) {
	pane, host, _ := newPane()
	pane.Activate(ref("CASH-1"))
	if persists(host) != 1 {
		t.Fatalf("opening a tab saved %d times, want once", persists(host))
	}

	// A scroll at the top of a one-line card moves nothing, and a key the pane
	// answers without changing the record is the same case.
	for i := 0; i < 5; i++ {
		pane.persist()
	}
	if got := persists(host); got != 1 {
		t.Fatalf("%d saves after five no-op persists, want the original one", got)
	}
}

// Anything that moves what is written still writes, exactly once.
func TestAChangedTabStillSaves(t *testing.T) {
	pane, host, _ := newPane()
	pane.Activate(ref("CASH-1"))
	before := persists(host)

	pane.Activate(ref("CASH-2"))
	if got := persists(host); got != before+1 {
		t.Fatalf("opening a second tab produced %d saves, want one more than %d", got, before)
	}

	pane.CycleTab(-1)
	if got := persists(host); got != before+2 {
		t.Fatalf("moving the active tab produced %d saves, want one more", got)
	}

	empty, _ := pane.CloseActiveTab()
	if empty {
		t.Fatal("closing one of two tabs emptied the leaf")
	}
	if got := persists(host); got != before+3 {
		t.Fatalf("closing a tab produced %d saves, want one more", got)
	}
}
