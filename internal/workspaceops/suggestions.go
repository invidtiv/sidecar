package workspaceops

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DiffRef is one recent commit or branch the diff picker can offer. Identity
// is what a target resolves from (`sidecar open --diff <identity>` shape);
// Label is what the row displays.
type DiffRef struct {
	Identity string
	Label    string
}

// RecentDiffRefs lists recent commits and local branches in workDir, commits
// first, newest first. These are offers, not resolutions: opening re-resolves
// host-side through uirequest.DiffTarget exactly as the CLI does.
func RecentDiffRefs(ctx context.Context, workDir string, commitLimit int) ([]DiffRef, error) {
	if commitLimit <= 0 {
		commitLimit = 15
	}
	cmd := exec.CommandContext(ctx, "git", "log", fmt.Sprintf("--max-count=%d", commitLimit), "--pretty=%h%x09%s")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	var refs []DiffRef
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		hash, subject, found := strings.Cut(line, "\t")
		if !found || hash == "" {
			continue
		}
		label := subject
		if label == "" {
			label = hash
		}
		refs = append(refs, DiffRef{Identity: hash, Label: hash + "  " + label})
	}
	branches, err := ListLocalBranches(ctx, workDir)
	if err == nil {
		for _, branch := range branches {
			refs = append(refs, DiffRef{Identity: branch, Label: branch + "  (branch)"})
		}
	}
	return refs, nil
}

// IssueRef is one td issue the issue picker can offer.
type IssueRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// RecentIssues lists a project's in-progress then open issues from td,
// in-progress first so "what am I doing" is the top of the list.
func RecentIssues(ctx context.Context, workDir string, limit int) ([]IssueRef, error) {
	if limit <= 0 {
		limit = 30
	}
	inProgress, err := runTDList(ctx, workDir, "in_progress", limit)
	if err != nil {
		return nil, err
	}
	open, err := runTDList(ctx, workDir, "open", limit)
	if err != nil {
		return nil, err
	}
	return append(inProgress, open...), nil
}

func runTDList(ctx context.Context, workDir, status string, limit int) ([]IssueRef, error) {
	cmd := exec.CommandContext(ctx, "td", "list", "--json", "--status", status, "--limit", fmt.Sprint(limit))
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("td list: %w", err)
	}
	var issues []IssueRef
	if len(output) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(output, &issues); err != nil {
		return nil, fmt.Errorf("parse td json: %w", err)
	}
	return issues, nil
}

// NoteRef is one td note the note picker can offer. Title is what the note
// reads as, not the raw title column: most notes carry an empty title and are
// known by their first line, so the picker would otherwise offer rows a user
// cannot tell apart. Updated is when the note last changed, which is what a
// picker row uses to say how fresh it is.
type NoteRef struct {
	ID      string
	Title   string
	Updated time.Time
}

// RecentNotes lists a project's non-deleted notes from td, pinned first as td
// orders them.
func RecentNotes(ctx context.Context, workDir string, limit int) ([]NoteRef, error) {
	if limit <= 0 {
		limit = 20
	}
	cmd := exec.CommandContext(ctx, "td", "-w", workDir, "--json", "note", "list")
	cmd.Env = append(cmd.Environ(), "TD_SYNC_AUTO_START=0", "TD_ANALYTICS=false")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("td note list: %w", err)
	}
	if len(output) == 0 {
		return nil, nil
	}
	type rawNote struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
		Updated string `json:"updated_at"`
		Pinned  bool   `json:"pinned"`
		Deleted any    `json:"deleted_at"`
	}
	var notes []rawNote
	if err := json.Unmarshal(output, &notes); err != nil {
		return nil, fmt.Errorf("parse td note list output: %w", err)
	}
	var refs []NoteRef
	for _, n := range notes {
		if n.ID == "" || n.Deleted != nil {
			continue
		}
		refs = append(refs, NoteRef{
			ID:      n.ID,
			Title:   NoteTitle(n.Title, n.Content),
			Updated: parseNoteTime(n.Updated),
		})
	}
	if len(refs) > limit {
		refs = refs[:limit]
	}
	return refs, nil
}

// NoteTitle is what a note is called on screen: its own title, or the first
// non-blank line of its body when it has none. td leaves the title column
// empty for notes captured body-first, and the notes plugin has always shown
// that first line — this is the same rule, shared so a note does not read as
// "untitled" in one surface and by name in another.
func NoteTitle(title, content string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	for _, line := range strings.Split(content, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// parseNoteTime reads td's timestamps, which arrive both as zone offsets and
// as fractional UTC. A stamp that parses as neither yields the zero time, and
// the picker simply shows no age for that row.
func parseNoteTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}
