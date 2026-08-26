package workspaceops

import "testing"

// Most td notes are captured body-first and carry an empty title column, so a
// surface that reads the column alone offers rows nobody can tell apart. The
// notes list has always fallen back to the first line; NoteTitle is that rule,
// shared, so the create modal's picker names a note the same way.
func TestNoteTitleFallsBackToFirstLine(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		content string
		want    string
	}{
		{"own title wins", "before relaunch", "before relaunch\n\nupdate the site", "before relaunch"},
		{"first line when untitled", "", "Bugs found in the demo.\n\nOne is that…", "Bugs found in the demo."},
		{"skips leading blank lines", "", "\n\n  experimental note  \nline one", "experimental note"},
		{"trims a padded title", "  td bugs  ", "whatever", "td bugs"},
		{"nothing to read it by", "", "\n   \n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NoteTitle(tc.title, tc.content); got != tc.want {
				t.Fatalf("NoteTitle(%q, %q) = %q, want %q", tc.title, tc.content, got, tc.want)
			}
		})
	}
}
