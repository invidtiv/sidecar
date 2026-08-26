package workspacecreate

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// A note row says which note it is and how fresh it is, and says nothing else.
// The id used to lead the row, which pushed the title off narrow modals and
// left the untitled notes — the majority — reading as bare ids.
func TestNoteRowsShowTitleAndAgeWithoutID(t *testing.T) {
	now := time.Date(2026, 8, 25, 17, 0, 0, 0, time.UTC)
	rows := foldNotesAt([]workspaceops.NoteRef{
		{ID: "nt-7c82c9", Title: "Bugs found in the demo.", Updated: now.Add(-4 * 24 * time.Hour)},
		{ID: "nt-49fb79dd", Title: "td bugs", Updated: now.Add(-90 * time.Minute)},
	}, now)

	want := []Suggestion{
		{Value: "nt-7c82c9", Label: "Bugs found in the demo.", Meta: "4d"},
		{Value: "nt-49fb79dd", Label: "td bugs", Meta: "1h"},
	}
	if len(rows) != len(want) {
		t.Fatalf("folded %d rows, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

// The id is gone from the row but not from the picker: it stays in Value, so a
// pasted id still selects the note it names.
func TestNoteRowStillMatchesItsID(t *testing.T) {
	rows := foldNotesAt([]workspaceops.NoteRef{
		{ID: "nt-7c82c9", Title: "Bugs found in the demo."},
		{ID: "nt-49fb79dd", Title: "td bugs"},
	}, time.Now())

	got := filterSuggestions("nt-49fb79dd", rows)
	if len(got) != 1 || got[0].Value != "nt-49fb79dd" {
		t.Fatalf("filtering by id matched %+v, want the one note it names", got)
	}
}

// The age column is straight down the list rather than trailing each title,
// and a title too long to sit beside it gives way to it.
func TestNotePickerAgeColumnIsRightAligned(t *testing.T) {
	f := Open(OpenOpts{Kind: KindNote, ShowNotes: true})
	f.SetNotes([]Suggestion{
		{Value: "nt-1", Label: "short", Meta: "4d"},
		{Value: "nt-2", Label: strings.Repeat("very long note title ", 10), Meta: "12m"},
	})
	f.AdvanceToTarget()

	view := f.Build(52).Render(80, 40, mouse.NewHandler())
	rows := map[string]string{}
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		switch {
		case strings.Contains(line, "short"):
			rows["short"] = line
		case strings.Contains(line, "very long note title"):
			rows["long"] = line
		}
	}
	if len(rows) != 2 {
		t.Fatalf("expected both note rows on screen, got %d:\n%s", len(rows), view)
	}

	col := func(line, meta string) int {
		return strings.Index(strings.TrimRight(line, " "), meta)
	}
	shortEnd := col(rows["short"], "4d") + len("4d")
	longEnd := col(rows["long"], "12m") + len("12m")
	if shortEnd < 0 || longEnd < 0 {
		t.Fatalf("age missing from a row:\n%q\n%q", rows["short"], rows["long"])
	}
	if shortEnd != longEnd {
		t.Fatalf("ages end at columns %d and %d, want one right-aligned column:\n%q\n%q",
			shortEnd, longEnd, rows["short"], rows["long"])
	}
	if !strings.Contains(rows["long"], "… 12m") {
		t.Fatalf("long title should truncate into the gap before its age:\n%q", rows["long"])
	}
}

// Panes named by what they show do not ask for a name. Only the three kinds a
// user genuinely names — Shell, Worktree, Terminal split — draw the field, and
// focus never lands on one that is not drawn.
func TestNameFieldOnlyForKindsThatUseIt(t *testing.T) {
	cases := []struct {
		kind Kind
		name string
		want bool
	}{
		{KindShell, "shell", true},
		{KindWorktree, "worktree", true},
		{KindTerminalSplit, "terminal split", true},
		{KindFile, "file", false},
		{KindDiff, "diff", false},
		{KindIssue, "issue", false},
		{KindNote, "note", false},
		{KindResource, "resource", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Open(switcherOpts(tc.kind))
			view := renderForm(t, f)
			hasField := focusable(t, f, FieldName)
			if hasField != tc.want {
				t.Fatalf("Name field focusable = %v, want %v:\n%s", hasField, tc.want, view)
			}
			if got := f.Modal().FocusedID(); got == FieldName && !tc.want {
				t.Fatalf("focus opened on a Name field this kind does not draw")
			}
		})
	}
}

// Arrowing off Shell onto a pane kind takes the Name field away while it holds
// focus. Focus falls back to the list the arrow came from rather than to a
// field that is no longer rendered.
func TestArrowingOffNamedKindMovesFocusToTheList(t *testing.T) {
	f := Open(switcherOpts(KindShell))
	renderForm(t, f)
	if got := f.Modal().FocusedID(); got != FieldName {
		t.Fatalf("shell opened focused on %q, want %s", got, FieldName)
	}
	f.SetKind(KindNote)
	renderForm(t, f)
	if got := f.Modal().FocusedID(); got != FieldKind {
		t.Fatalf("focus after moving to Note = %q, want %s", got, FieldKind)
	}
}
