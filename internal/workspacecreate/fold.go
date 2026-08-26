package workspacecreate

import (
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/workspacelist"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The folds turn one loader sample into picker suggestions. They live here —
// not in either host — because a fold that drifts between surfaces is exactly
// how the overview once resolved diff rows against "hash  hash  title"
// strings. Both hosts call these; neither keeps its own copy.
//
// Value always carries what a target resolves from; Label is display-only.

// FoldDiffRefs folds recent commits and branches. Identity resolves
// host-side through uirequest.DiffTarget exactly as `sidecar open --diff`
// does; Label may repeat it for readability but never leaks into Value.
func FoldDiffRefs(refs []workspaceops.DiffRef) []Suggestion {
	out := make([]Suggestion, 0, len(refs))
	for _, ref := range refs {
		out = append(out, Suggestion{Value: ref.Identity, Label: ref.Label})
	}
	return out
}

// FoldIssues folds td issues, in-progress ones badged so "what am I doing"
// reads before "what exists". The row is the issue's title with its age in the
// right-hand column, on the same reasoning as the notes below: an id is what
// you copy to hand an issue to an agent, not what you scan a list by. Value
// keeps the id, so pasting one still matches the row it names.
func FoldIssues(issues []workspaceops.IssueRef) []Suggestion {
	return foldIssuesAt(issues, time.Now())
}

func foldIssuesAt(issues []workspaceops.IssueRef, now time.Time) []Suggestion {
	out := make([]Suggestion, 0, len(issues))
	for _, issue := range issues {
		badge := ""
		if issue.Status == "in_progress" {
			badge = "in progress"
		}
		label := strings.TrimSpace(issue.Title)
		if label == "" {
			// Nothing to read it by: the id is a worse name than none, but it
			// is the only one left.
			label = issue.ID
		}
		out = append(out, Suggestion{
			Value: issue.ID,
			Label: label,
			Badge: badge,
			Meta:  workspacelist.RelativeAge(issue.Updated, now),
		})
	}
	return out
}

// FoldNotes folds td notes. The row is the note's title with its age in the
// right-hand column, and the id is nowhere on it: a note id is a thing to copy
// for an agent, not a thing anyone recognises a note by, and it crowded out the
// one column that says which note this is. The id stays in Value, so pasting or
// typing one still matches the row it names.
func FoldNotes(notes []workspaceops.NoteRef) []Suggestion {
	return foldNotesAt(notes, time.Now())
}

func foldNotesAt(notes []workspaceops.NoteRef, now time.Time) []Suggestion {
	out := make([]Suggestion, 0, len(notes))
	for _, note := range notes {
		label := note.Title
		if label == "" {
			// Nothing to read it by: the id is a worse name than none, but it
			// is the only one left.
			label = note.ID
		}
		out = append(out, Suggestion{
			Value: note.ID,
			Label: label,
			Meta:  workspacelist.RelativeAge(note.Updated, now),
		})
	}
	return out
}
