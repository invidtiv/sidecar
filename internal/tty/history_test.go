package tty

import "testing"

// The reach is the order of a lazy history read, and every host adopts it rather
// than restating it. What is proved here is that order: one request per
// bound-hit, a scroll that arrives while one is running coalesced onto it, a
// result from a superseded request refused, and a hard stop at tmux's oldest
// line.

// A bound-hit opens exactly one read, of the chunk immediately older than the
// buffer's first line, addressed in the coordinates capture-pane counts in.
func TestOneReadPerBoundHit(t *testing.T) {
	reach := HistoryReach{HistorySize: 1200}

	request, outcome := reach.Request(600, true, 3)
	if outcome != HistoryRequested {
		t.Fatalf("first bound-hit = %v, want a read", outcome)
	}
	if request.Start != 0-1200 || request.End != 599-1200 {
		t.Fatalf("read range = [%d,%d], want the 600 lines below absolute 600",
			request.Start, request.End)
	}
	if request.Generation != 1 {
		t.Fatalf("generation = %d, want the first", request.Generation)
	}

	if _, outcome := reach.Request(600, true, 4); outcome != HistoryInFlight {
		t.Fatalf("second bound-hit = %v, want it to ride on the read already running", outcome)
	}
	if reach.PendingScroll != 7 {
		t.Fatalf("pending scroll = %d, want both hits coalesced", reach.PendingScroll)
	}
}

// The scroll waiting on a read is replayed when it lands, not lost: the reader
// pushed against the bound while the rows were in flight.
func TestPendingScrollIsCoalescedRatherThanLost(t *testing.T) {
	reach := HistoryReach{HistorySize: 1200}
	request, _ := reach.Request(600, true, 5)
	reach.Request(600, true, 6)

	scrollLines, ok := reach.Accept(request.Generation)
	if !ok || scrollLines != 11 {
		t.Fatalf("accepted read = %d lines ok=%v, want the 11 lines the reader is owed", scrollLines, ok)
	}
	if reach.Loading || reach.PendingScroll != 0 {
		t.Fatalf("reach after the read = %#v, want idle with nothing owed", reach)
	}

	// A prepend shorter than the movement owed walks back another chunk.
	reach.Settle(300, 1200)
	if remainder, more := reach.Remainder(scrollLines, 4); !more || remainder != 7 {
		t.Fatalf("remainder = %d more=%v, want the 7 rows the prepend did not cover", remainder, more)
	}
}

// A result from a request the reach has cancelled or replaced is refused. It
// would otherwise land on a window the reader has since moved somewhere else.
func TestAStaleGenerationIsIgnored(t *testing.T) {
	reach := HistoryReach{HistorySize: 1200}
	stale, _ := reach.Request(600, true, 5)
	reach.Cancel()

	if _, ok := reach.Accept(stale.Generation); ok {
		t.Fatal("a cancelled read was admitted")
	}
	if _, ok := reach.Accept(0); ok {
		t.Fatal("an untagged read was admitted")
	}

	fresh, outcome := reach.Request(600, true, 5)
	if outcome != HistoryRequested || fresh.Generation == stale.Generation {
		t.Fatalf("read after a cancel = %v gen=%d, want a new generation", outcome, fresh.Generation)
	}
	if _, ok := reach.Accept(fresh.Generation); !ok {
		t.Fatal("the current read was refused")
	}
}

// The reach ends where tmux's history ends and nowhere earlier: at absolute base
// 0, and for a pane that reports no history at all.
func TestTheReachStopsAtTheOldestLine(t *testing.T) {
	reach := HistoryReach{HistorySize: 1200}
	request, _ := reach.Request(600, true, 5)
	reach.Accept(request.Generation)
	reach.Settle(0, 1200)

	if _, outcome := reach.Request(0, true, 5); outcome != HistoryEnded {
		t.Fatalf("a reach at absolute 0 = %v, want the end of history", outcome)
	}
	if !reach.NoteEnd() {
		t.Fatal("the reader was not told the pane has no more history")
	}
	if reach.NoteEnd() {
		t.Fatal("the reader was told twice")
	}

	empty := HistoryReach{}
	empty.Record(0)
	if _, outcome := empty.Request(0, true, 5); outcome != HistoryEnded {
		t.Fatalf("a pane with no history = %v, want the end of history", outcome)
	}

	// Neither is the same as not knowing: a buffer whose lines are not tmux's
	// coordinates cannot say where history ends, so it says nothing.
	unknown := HistoryReach{HistorySize: 1200}
	if _, outcome := unknown.Request(600, false, 5); outcome != HistoryUnavailable {
		t.Fatalf("a relative buffer = %v, want no claim either way", outcome)
	}
}

// A read that proves there was more history re-arms the notice: the next end is
// a different fact from the one already told.
func TestSettlingAboveZeroReArmsTheNotice(t *testing.T) {
	reach := HistoryReach{HistorySize: 1200}
	reach.NoteEnd()
	reach.Settle(300, 1200)
	if !reach.NoteEnd() {
		t.Fatal("a reader who found more history was never told about the next end")
	}
}
