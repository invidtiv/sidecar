package workspaceops

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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

// NoteRef is one td note the note picker can offer.
type NoteRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// RecentNotes lists a project's non-deleted notes from td, pinned first as td
// orders them. Only identity and title are decoded; bodies stay on disk.
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
		refs = append(refs, NoteRef{ID: n.ID, Title: strings.TrimSpace(n.Title)})
	}
	if len(refs) > limit {
		refs = refs[:limit]
	}
	return refs, nil
}
