package tty

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestNewOutputBuffer(t *testing.T) {
	buf := NewOutputBuffer(100)
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty buffer, got %d lines", buf.Len())
	}
}

func TestOutputBuffer_Write(t *testing.T) {
	buf := NewOutputBuffer(100)
	buf.Write("line1\nline2\nline3")

	if buf.Len() != 3 {
		t.Errorf("expected 3 lines, got %d", buf.Len())
	}

	lines := buf.Lines()
	if lines[0] != "line1" || lines[1] != "line2" || lines[2] != "line3" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestOutputBuffer_Update(t *testing.T) {
	buf := NewOutputBuffer(100)

	// First write should return true
	changed := buf.Update("hello\nworld")
	if !changed {
		t.Error("expected changed=true for initial write")
	}

	// Same content should return false
	changed = buf.Update("hello\nworld")
	if changed {
		t.Error("expected changed=false for same content")
	}

	// Different content should return true
	changed = buf.Update("hello\nuniverse")
	if !changed {
		t.Error("expected changed=true for different content")
	}
}

func TestOutputBuffer_Capacity(t *testing.T) {
	buf := NewOutputBuffer(3)

	// Write more lines than capacity
	buf.Write("line1\nline2\nline3\nline4\nline5")

	if buf.Len() != 3 {
		t.Errorf("expected 3 lines (capacity), got %d", buf.Len())
	}

	lines := buf.Lines()
	// Should keep most recent lines
	if lines[0] != "line3" || lines[1] != "line4" || lines[2] != "line5" {
		t.Errorf("expected most recent lines, got: %v", lines)
	}
}

func TestOutputBuffer_StripMouseSequences(t *testing.T) {
	buf := NewOutputBuffer(100)

	// Content with mouse escape sequences
	content := "hello\x1b[<65;83;33Mworld"
	buf.Write(content)

	// Mouse sequences should be stripped
	result := buf.String()
	if strings.Contains(result, "\x1b[<") {
		t.Error("expected mouse sequences to be stripped")
	}
	if !strings.Contains(result, "hello") || !strings.Contains(result, "world") {
		t.Error("expected content to be preserved")
	}
}

func TestOutputBuffer_StripTerminalModeSequences(t *testing.T) {
	buf := NewOutputBuffer(100)

	// Content with terminal mode sequences
	content := "hello\x1b[?2004hworld"
	buf.Write(content)

	result := buf.String()
	if strings.Contains(result, "\x1b[?2004h") {
		t.Error("expected terminal mode sequences to be stripped")
	}
}

func TestOutputBuffer_LinesRange(t *testing.T) {
	buf := NewOutputBuffer(100)
	buf.Write("line0\nline1\nline2\nline3\nline4")

	lines := buf.LinesRange(1, 3)
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "line1" || lines[1] != "line2" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestOutputBuffer_Clear(t *testing.T) {
	buf := NewOutputBuffer(100)
	buf.Write("hello\nworld")

	if buf.Len() != 2 {
		t.Errorf("expected 2 lines before clear, got %d", buf.Len())
	}

	buf.Clear()

	if buf.Len() != 0 {
		t.Errorf("expected 0 lines after clear, got %d", buf.Len())
	}
}

func TestOutputBuffer_ClearAllowsSameContentAgain(t *testing.T) {
	buf := NewOutputBuffer(10)
	if !buf.Update("same") {
		t.Fatal("initial update should change the buffer")
	}
	buf.Clear()
	if !buf.Update("same") {
		t.Fatal("same raw content after Clear should update the buffer")
	}
	if got := buf.String(); got != "same" {
		t.Fatalf("buffer after Clear and update = %q, want same", got)
	}
}

func TestOutputBufferAbsoluteSnapshotPreservesPrependedHistory(t *testing.T) {
	b := NewOutputBuffer(20)
	if !b.UpdateSnapshot("live-8\nlive-9\n", 8) {
		t.Fatal("initial absolute snapshot was not applied")
	}
	if !b.PrependSnapshot("old-5\nold-6\nold-7\n", 5) {
		t.Fatal("older snapshot was not prepended")
	}
	if !b.UpdateSnapshot("live-8\nlive-9 changed\nlive-10\n", 8) {
		t.Fatal("changed live snapshot was not applied")
	}

	start, end, ok := b.AbsoluteRange()
	if !ok || start != 5 || end != 11 {
		t.Fatalf("absolute range = (%d,%d,%v), want (5,11,true)", start, end, ok)
	}
	got := b.LinesAbsoluteRange(5, 11)
	want := []string{"old-5", "old-6", "old-7", "live-8", "live-9 changed", "live-10"}
	if !slices.Equal(got, want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
}

func TestOutputBufferAbsoluteTrimAdvancesBase(t *testing.T) {
	b := NewOutputBuffer(4)
	b.UpdateSnapshot("six\nseven\neight\nnine\n", 6)
	b.PrependSnapshot("three\nfour\nfive\n", 3)

	start, end, ok := b.AbsoluteRange()
	if !ok || start != 6 || end != 10 {
		t.Fatalf("trimmed range = (%d,%d,%v), want (6,10,true)", start, end, ok)
	}
	if got := b.Lines(); !slices.Equal(got, []string{"six", "seven", "eight", "nine"}) {
		t.Fatalf("trimmed lines = %#v", got)
	}
}

func TestOutputBufferPrependRejectsGap(t *testing.T) {
	b := NewOutputBuffer(20)
	b.UpdateSnapshot("ten\neleven\n", 10)
	if b.PrependSnapshot("five\n", 5) {
		t.Fatal("gapped prepend unexpectedly succeeded")
	}
}

func TestOutputBufferLegacyUpdateClearsAbsoluteModeForIdenticalContent(t *testing.T) {
	b := NewOutputBuffer(20)
	if !b.UpdateSnapshot("same\ncontent", 42) {
		t.Fatal("absolute snapshot was not applied")
	}
	if !b.Update("same\ncontent") {
		t.Fatal("legacy update must apply when changing coordinate modes")
	}
	if start, end, ok := b.AbsoluteRange(); ok {
		t.Fatalf("legacy update left absolute range [%d,%d) active", start, end)
	}
}

func TestOutputBufferDelayedPrependCannotOverwriteNewerOverlap(t *testing.T) {
	b := NewOutputBuffer(20)
	b.UpdateSnapshot("live-2\nlive-3\nlive-4", 2)

	// Simulate an older capture requested before the live rows changed. Its
	// overlap at absolute lines 2-3 must not replace the current live tail.
	if !b.PrependSnapshot("old-0\nold-1\nstale-2\nstale-3", 0) {
		t.Fatal("older capture was not prepended")
	}
	got := b.Lines()
	want := []string{"old-0", "old-1", "live-2", "live-3", "live-4"}
	if !slices.Equal(got, want) {
		t.Fatalf("merged lines = %#v, want %#v", got, want)
	}
}

func TestOutputBufferUpdateUsesRawLengthHashGuard(t *testing.T) {
	b := NewOutputBuffer(10)
	content := "hello\x1b[?2004h"
	if !b.Update(content) {
		t.Fatal("initial update did not change")
	}
	if b.Update(content) {
		t.Fatal("identical raw content reprocessed after escape stripping")
	}
}

func TestOutputBuffer_LastNonEmptyLine(t *testing.T) {
	buf := NewOutputBuffer(10)
	buf.Write("first\nsecond\n \n\x1b[31m\x1b[0m")
	if got := buf.LastNonEmptyLine(); got != 1 {
		t.Fatalf("LastNonEmptyLine() = %d, want 1", got)
	}
	buf.Clear()
	if got := buf.LastNonEmptyLine(); got != -1 {
		t.Fatalf("LastNonEmptyLine() after Clear = %d, want -1", got)
	}
}

func TestPartialMouseSeqRegex(t *testing.T) {
	tests := []struct {
		input string
		match bool
	}{
		{"[<65;83;33M", true},     // scroll down
		{"[<64;10;5M", true},      // scroll up
		{"[<0;50;20m", true},      // release
		{"hello", false},          // normal text
		{"[notmouse]", false},     // not a mouse sequence
		{"[<abc;def;ghiM", false}, // invalid format
	}

	for _, tt := range tests {
		if got := PartialMouseSeqRegex.MatchString(tt.input); got != tt.match {
			t.Errorf("PartialMouseSeqRegex.MatchString(%q) = %v, want %v", tt.input, got, tt.match)
		}
	}
}

func TestOutputBuffer_StripTruncatedMouseSequence(t *testing.T) {
	buf := NewOutputBuffer(100)

	// Truncated mouse sequence missing trailing M (captured mid-transmission)
	content := "prompt> [<65;103;31"
	buf.Write(content)

	result := buf.String()
	if strings.Contains(result, "[<65;103;31") {
		t.Error("expected truncated mouse sequence to be stripped")
	}
	if !strings.Contains(result, "prompt> ") {
		t.Error("expected surrounding content to be preserved")
	}
}

func TestOutputBuffer_StripPartialMouseSequenceWithTerminator(t *testing.T) {
	buf := NewOutputBuffer(100)

	// Partial mouse sequence (no ESC) with terminator
	content := "prompt> [<65;103;31M"
	buf.Write(content)

	result := buf.String()
	if strings.Contains(result, "[<65;103;31M") {
		t.Error("expected partial mouse sequence to be stripped")
	}
	if !strings.Contains(result, "prompt> ") {
		t.Error("expected surrounding content to be preserved")
	}
}

// TestContainsMouseSequence tests the lenient mouse sequence detection (td-e2ce50)
func TestContainsMouseSequence(t *testing.T) {
	tests := []struct {
		input string
		want  bool
		desc  string
	}{
		{"[<65;143;8M", true, "complete sequence"},
		{"[<65;143;8M[<64;143;8M", true, "multiple complete sequences"},
		{"[<65;143;", true, "truncated (no M)"},
		{"8M[<65;143;8M", true, "starts mid-sequence"},
		{"[<65", true, "very truncated"},
		{"[<65;183;40M[<64;183;40M", true, "fast scroll sequence"},
		{"hello", false, "normal text"},
		{"[<notanumber", false, "not a sequence (non-numeric)"},
		{"ls -la", false, "command"},
		{"", false, "empty string"},
		{"[]", false, "empty brackets"},
		{"[test]", false, "normal brackets"},
		{"<65;143;8M", false, "missing opening bracket"},
	}

	for _, tt := range tests {
		if got := ContainsMouseSequence(tt.input); got != tt.want {
			t.Errorf("ContainsMouseSequence(%q) = %v, want %v (%s)", tt.input, got, tt.want, tt.desc)
		}
	}
}

// TestLooksLikeMouseFragment tests the very lenient fragment detection (td-e2ce50)
func TestLooksLikeMouseFragment(t *testing.T) {
	tests := []struct {
		input string
		want  bool
		desc  string
	}{
		// Very short fragments (the key improvement)
		{"[<", true, "just start marker"},
		{"[<6", true, "start + one digit"},
		{"[<64", true, "start + two digits"},
		{";1", true, "semicolon + digit (mid-sequence)"},
		{"3M", true, "digit + M (end of sequence)"},
		{"3m", true, "digit + m (end of sequence, release)"},

		// Longer sequences (should also match via ContainsMouseSequence)
		{"[<65;143;8M", true, "complete sequence"},
		{"[<65;143;8M[<64;143;8M", true, "multiple complete sequences"},
		{"[<65;143;", true, "truncated (no M)"},
		{"8M[<65;143;8M", true, "starts mid-sequence"},

		// Concatenated sequences (td-3b15ee: fast trackpad scroll pattern)
		{"[<64;107;16M[<64;107;16M[<64;107;16M", true, "many concatenated scroll events"},
		{"[<65;107;14M[<65;107;14M[<35;111;12M", true, "mixed scroll events"},
		{"M[<64;107;16M", true, "sequence starting with M (split boundary)"},
		{"m[<64;107;16M", true, "sequence starting with m (split boundary)"},

		// Split CSI sequences (just brackets arriving separately)
		{"[", false, "single bracket is a normal typeable character (callers use time-gating)"},
		{"[[", true, "double brackets"},
		{"[[[", true, "triple brackets"},
		{"[[[[[[[[[[", true, "many brackets (burst of split CSI)"},

		// Semicolon-heavy patterns (coordinate garbage)
		{"64;107;16", true, "raw coordinates (multi-semicolon)"},
		{"65;107;14;65;107;14", true, "multiple coordinate sets"},

		// Non-matches
		{"hello", false, "normal text"},
		{"a", false, "single letter"},
		{"12", false, "just digits (no markers)"},
		{"ls", false, "command"},
		{"", false, "empty string"},
		{"[test]", false, "normal brackets without <"},
		{"M", false, "just M (no digit)"},
		{";", false, "just semicolon (no digit)"},
		{"hello world", false, "text with space"},
		{"foo;bar", false, "semicolon but no digits"},
	}

	for _, tt := range tests {
		if got := LooksLikeMouseFragment(tt.input); got != tt.want {
			t.Errorf("LooksLikeMouseFragment(%q) = %v, want %v (%s)", tt.input, got, tt.want, tt.desc)
		}
	}
}

// The pane split is the answer to "how many buffer lines sit above pane row 0",
// and it has to survive the two things that move lines underneath it: the
// capacity trim, which drops rows off the front, and a lazily loaded older
// capture, which adds them.
func TestOutputBufferPaneSplitTracksTheContentItDescribes(t *testing.T) {
	rows := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s-%02d", prefix, i)
		}
		return out
	}
	history := rows("history", 4)
	pane := rows("pane", 3)
	content := strings.Join(append(append([]string{}, history...), pane...), "\n")

	b := NewOutputBuffer(100)
	if !b.ApplySnapshot(PaneSnapshot{
		Output: content, BaseLine: 96, Absolute: true, HistoryRows: 4, PaneRows: 3,
	}) {
		t.Fatal("snapshot was not applied")
	}
	lineCount, paneTop, ok := b.PaneWindow()
	if !ok || lineCount != 7 || paneTop != 4 {
		t.Fatalf("pane window = (%d lines, top %d, ok %v), want (7, 4, true)", lineCount, paneTop, ok)
	}
	if got := b.Lines()[paneTop]; got != "pane-00" {
		t.Fatalf("pane row 0 = %q, want %q", got, "pane-00")
	}

	// Older history merged in front moves pane row 0 down by exactly the rows
	// it added.
	if !b.PrependSnapshot(strings.Join(rows("older", 4), "\n")+"\n"+strings.Join(history, "\n"), 92) {
		t.Fatal("older history was not prepended")
	}
	_, paneTop, ok = b.PaneWindow()
	if !ok || paneTop != 8 {
		t.Fatalf("pane top after prepend = (%d, ok %v), want 8", paneTop, ok)
	}
	if got := b.Lines()[paneTop]; got != "pane-00" {
		t.Fatalf("pane row 0 after prepend = %q, want %q", got, "pane-00")
	}

	// And a relative buffer trimmed to capacity keeps pointing at the same row.
	small := NewOutputBuffer(5)
	small.ApplySnapshot(PaneSnapshot{Output: content, HistoryRows: 4, PaneRows: 3})
	lineCount, paneTop, ok = small.PaneWindow()
	if !ok || lineCount != 5 || paneTop != 2 {
		t.Fatalf("trimmed pane window = (%d lines, top %d, ok %v), want (5, 2, true)", lineCount, paneTop, ok)
	}
	if got := small.Lines()[paneTop]; got != "pane-00" {
		t.Fatalf("pane row 0 after trim = %q, want %q", got, "pane-00")
	}
}

// td-d29821: capture-shaped output is row separated, so a blank final pane row
// and a trailing terminator are the same bytes. Only the producer knows which,
// and the difference is the row the cursor is on at the bottom of a screen.
func TestOutputBufferKeepsABlankFinalPaneRow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		output    string
		snapshot  PaneSnapshot
		wantLines int
		wantTop   int
	}{
		{
			name:      "blank final grid row is a row",
			output:    "history\npane-0\n",
			snapshot:  PaneSnapshot{HistoryRows: 1, PaneRows: 2},
			wantLines: 3,
			wantTop:   1,
		},
		{
			name:      "trailing terminator is not",
			output:    "history\npane-0\npane-1\n",
			snapshot:  PaneSnapshot{HistoryRows: 1, PaneRows: 2},
			wantLines: 3,
			wantTop:   1,
		},
		{
			name:      "no stated split falls back to the terminator rule",
			output:    "history\npane-0\n",
			snapshot:  PaneSnapshot{},
			wantLines: 2,
			wantTop:   0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewOutputBuffer(100)
			snapshot := tc.snapshot
			snapshot.Output = tc.output
			b.ApplySnapshot(snapshot)
			lineCount, paneTop, ok := b.PaneWindow()
			if lineCount != tc.wantLines {
				t.Fatalf("line count = %d, want %d (%#v)", lineCount, tc.wantLines, b.Lines())
			}
			if ok != (tc.snapshot.PaneRows > 0) {
				t.Fatalf("split known = %v, want %v", ok, tc.snapshot.PaneRows > 0)
			}
			if ok && paneTop != tc.wantTop {
				t.Fatalf("pane top = %d, want %d", paneTop, tc.wantTop)
			}
		})
	}
}

// CaptureSnapshot is the one place capture-shaped producers derive their split,
// so it has to agree with what tmux actually delivers: history followed by every
// pane row, trailing blanks included.
func TestCaptureSnapshotSplitsHistoryFromPane(t *testing.T) {
	for _, tc := range []struct {
		name        string
		output      string
		paneHeight  int
		rowsJoined  bool
		wantHistory int
		wantPane    int
	}{
		{"history and pane", "h0\nh1\np0\np1\n", 2, false, 2, 2},
		{"pane only", "p0\np1\n", 2, false, 0, 2},
		{"capture shorter than the pane", "p0\n", 4, false, 0, 1},
		{"no geometry observed", "p0\np1\n", 0, false, 0, 0},
		// -J collapses wrapped lines, so the capture's rows are not the grid's
		// and no split may be stated at all.
		{"joined rows", "h0\nh1\np0\np1\n", 2, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CaptureSnapshot(CaptureInput{
				Output:     tc.output,
				BaseLine:   7,
				Absolute:   true,
				PaneHeight: tc.paneHeight,
				RowsJoined: tc.rowsJoined,
			})
			if got.HistoryRows != tc.wantHistory || got.PaneRows != tc.wantPane {
				t.Fatalf("split = %d history + %d pane rows, want %d + %d",
					got.HistoryRows, got.PaneRows, tc.wantHistory, tc.wantPane)
			}
		})
	}
}
