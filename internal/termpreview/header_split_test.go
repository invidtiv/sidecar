package termpreview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// col reports the first column of substring s in row, in cells.
func col(t *testing.T, row, s string) int {
	t.Helper()
	idx := strings.Index(row, s)
	if idx < 0 {
		t.Fatalf("row %q does not contain %q", row, s)
	}
	return ansi.StringWidth(row[:idx])
}

func TestHeaderRowSplitRightAlignsActionChips(t *testing.T) {
	row, placements := HeaderRowSplit([]string{"shell name"}, []string{"Diff"}, "", 40, 0, nil)
	if ansi.StringWidth(row) != 40 {
		t.Fatalf("row width = %d, want 40 with a right-aligned chip", ansi.StringWidth(row))
	}
	if got := col(t, row, "shell name"); got != 0 {
		t.Fatalf("name column = %d, want 0", got)
	}
	if got := col(t, row, "Diff"); got != 36 {
		t.Fatalf("Diff column = %d, want 36 (flush right)", got)
	}
	if !placements[0].Drawn || placements[0].Col != 36 || placements[0].Width != 4 {
		t.Fatalf("placement %+v does not match the drawn row", placements[0])
	}
}

func TestHeaderRowSplitPutsChipsLeftOfTheHints(t *testing.T) {
	const hints = "INTERACTIVE"
	row, placements := HeaderRowSplit([]string{"shell"}, []string{"Diff", "Task"}, hints, 60, len(hints), nil)
	diffCol, taskCol, hintCol := col(t, row, "Diff"), col(t, row, "Task"), col(t, row, hints)
	if diffCol >= taskCol || taskCol >= hintCol {
		t.Fatalf("row %q: want Diff < Task < INTERACTIVE, got %d %d %d", row, diffCol, taskCol, hintCol)
	}
	if hintCol+ansi.StringWidth(hints) != 60 {
		t.Fatalf("hints do not end at the right edge: %q", row)
	}
	if placements[0].Col != diffCol || placements[1].Col != taskCol {
		t.Fatalf("placements %+v do not match the drawn columns %d/%d", placements, diffCol, taskCol)
	}
	if ansi.StringWidth(row) > 60 {
		t.Fatalf("row width = %d, want <= 60", ansi.StringWidth(row))
	}
}

func TestHeaderRowSplitTruncatesTheNameNotTheChips(t *testing.T) {
	name := strings.Repeat("n", 40)
	row, placements := HeaderRowSplit([]string{name}, []string{"Diff", "Task"}, "", 24, 0, nil)
	if !placements[0].Drawn || !placements[1].Drawn {
		t.Fatalf("a narrow row dropped its action chips: %+v", placements)
	}
	if strings.Contains(row, name) {
		t.Fatalf("row %q kept the full name instead of truncating it", row)
	}
	if !strings.Contains(row, "n") {
		t.Fatalf("row %q dropped the name entirely", row)
	}
	if ansi.StringWidth(row) > 24 {
		t.Fatalf("row width = %d, want <= 24", ansi.StringWidth(row))
	}
}

func TestHeaderRowSplitDropsChipsOnlyWhenTheRowCannotHoldThem(t *testing.T) {
	_, placements := HeaderRowSplit([]string{"x"}, []string{"Diff", "Task"}, "", 6, 0, nil)
	if placements[1].Drawn {
		t.Fatalf("Task should be dropped at width 6: %+v", placements)
	}
	if !placements[0].Drawn {
		t.Fatalf("Diff should survive at width 6: %+v", placements)
	}
}

func TestHeaderRowSplitWithoutActionsMatchesHeaderRow(t *testing.T) {
	chips := []string{"name", "Agent"}
	want := HeaderRow(chips, "hints", 40, 0, nil)
	got, placements := HeaderRowSplit(chips, nil, "hints", 40, 0, nil)
	if got != want {
		t.Fatalf("split row %q != header row %q", got, want)
	}
	if len(placements) != 0 {
		t.Fatalf("placements = %+v, want none", placements)
	}
}
