package workspacecreate

import (
	"strings"

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
// reads before "what exists".
func FoldIssues(issues []workspaceops.IssueRef) []Suggestion {
	out := make([]Suggestion, 0, len(issues))
	for _, issue := range issues {
		badge := ""
		if issue.Status == "in_progress" {
			badge = "in progress"
		}
		label := strings.TrimSpace(issue.ID + "  " + issue.Title)
		out = append(out, Suggestion{Value: issue.ID, Label: label, Badge: badge})
	}
	return out
}

// FoldNotes folds td notes.
func FoldNotes(notes []workspaceops.NoteRef) []Suggestion {
	out := make([]Suggestion, 0, len(notes))
	for _, note := range notes {
		label := note.ID
		if note.Title != "" {
			label = note.ID + "  " + note.Title
		}
		out = append(out, Suggestion{Value: note.ID, Label: label})
	}
	return out
}
