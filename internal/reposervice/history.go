package reposervice

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/contentservice"
)

const (
	// DefaultHistoryLimit is one page for a viewer that did not say.
	DefaultHistoryLimit = 100

	// MaxHistoryLimit is what one call may return regardless of what was asked.
	// A viewer scrolling a long history asks for more; it never asks a host to
	// serialize an entire log, and the host is where that is enforced because
	// the host is the machine that would pay for it.
	MaxHistoryLimit = 500
)

// HistoryQuery is one page request. Cursor is the full hash of the last row of
// the previous page.
type HistoryQuery struct {
	Limit  int
	Cursor string
	Author string
	Path   string
}

// HistoryResult is the machine contract for `sidecar repo history --json`.
type HistoryResult struct {
	Kind         string `json:"kind"`
	Workspace    string `json:"workspace"`
	NoRepository bool   `json:"noRepository,omitempty"`

	Commits []CommitRow `json:"commits,omitempty"`
	// Limit is what the host actually applied, which is not necessarily what
	// was asked for.
	Limit int `json:"limit,omitempty"`
	// NextCursor is empty when this page reached the end of the log.
	NextCursor string `json:"nextCursor,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// CommitRow is one row of the commit list.
type CommitRow struct {
	Hash        string    `json:"hash"`
	ShortHash   string    `json:"shortHash,omitempty"`
	Subject     string    `json:"subject,omitempty"`
	Author      string    `json:"author,omitempty"`
	AuthorEmail string    `json:"authorEmail,omitempty"`
	Date        time.Time `json:"date,omitempty"`
	Parents     []string  `json:"parents,omitempty"`
	Merge       bool      `json:"merge,omitempty"`
	Pushed      bool      `json:"pushed,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
func (r HistoryResult) ValidRemoteResult() bool {
	return r.Kind == KindHistory && strings.TrimSpace(r.Workspace) != ""
}

// historyFormat is NUL-separated fields, one commit per line. %s never contains
// a newline, so the two separators cannot collide.
const historyFormat = "%H%x00%h%x00%an%x00%ae%x00%at%x00%P%x00%s"

// History returns one page of the commit log, newest first.
func (s *Service) History(ctx context.Context, workspaceID string, q HistoryQuery) (HistoryResult, error) {
	r, ok, err := s.open(ctx, workspaceID)
	if err != nil {
		return HistoryResult{}, err
	}
	result := HistoryResult{Kind: KindHistory, Workspace: r.ID}
	if !ok {
		result.NoRepository = true
		return result, nil
	}

	limit := q.Limit
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	if limit > MaxHistoryLimit {
		limit = MaxHistoryLimit
		result.Truncated = true
	}
	result.Limit = limit

	args := []string{"log", "--format=" + historyFormat, "-n", strconv.Itoa(limit)}
	if q.Cursor != "" {
		cursor, err := requireHash(q.Cursor)
		if err != nil {
			return HistoryResult{}, err
		}
		// The cursor is the previous page's last row, not an offset. An offset
		// silently repeats or skips a commit when the host commits between two
		// pages, and a viewer cannot tell that it happened.
		args = append(args, cursor, "--skip=1")
	}
	if author := strings.TrimSpace(q.Author); author != "" {
		if err := requireFilter(author, "author"); err != nil {
			return HistoryResult{}, err
		}
		args = append(args, "--author="+author)
	}
	if strings.TrimSpace(q.Path) != "" {
		rel, err := requirePath(r.Root, q.Path)
		if err != nil {
			return HistoryResult{}, err
		}
		args = append(args, "--", rel)
	}

	out, err := s.git(ctx, r.Root, args...)
	if err != nil {
		if !s.hasHead(ctx, r) {
			// A repository with no commits has an empty history, which is an
			// answer. Only a real failure is an error.
			return result, nil
		}
		return HistoryResult{}, contentservice.Internal("read commit history", err)
	}
	result.Commits = parseHistory(out)
	if len(result.Commits) == limit {
		result.NextCursor = result.Commits[len(result.Commits)-1].Hash
	}
	s.markPushed(ctx, r, result.Commits)
	return result, nil
}

func parseHistory(out []byte) []CommitRow {
	lines := splitLines(out)
	rows := make([]CommitRow, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\x00")
		if len(fields) < 7 {
			continue
		}
		row := CommitRow{
			Hash:        fields[0],
			ShortHash:   fields[1],
			Author:      fields[2],
			AuthorEmail: fields[3],
			Subject:     fields[6],
			Parents:     strings.Fields(fields[5]),
		}
		row.Merge = len(row.Parents) > 1
		if seconds, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
			row.Date = time.Unix(seconds, 0).UTC()
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *Service) hasHead(ctx context.Context, r repo) bool {
	_, err := s.git(ctx, r.Root, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// markPushed asks git which of this page's commits the upstream does not have.
//
// Bounded by the page rather than by the whole unpushed set: a branch a long
// way ahead of its upstream would otherwise make the answer depend on a cap,
// and a commit wrongly labelled pushed is the label that matters.
func (s *Service) markPushed(ctx context.Context, r repo, rows []CommitRow) {
	if len(rows) == 0 {
		return
	}
	if _, err := s.git(ctx, r.Root, "rev-parse", "--verify", "--quiet", "@{upstream}"); err != nil {
		// No upstream means nothing has been pushed anywhere.
		return
	}
	args := []string{"rev-list", "--no-walk"}
	for _, row := range rows {
		args = append(args, row.Hash)
	}
	args = append(args, "--not", "@{upstream}")
	out, err := s.git(ctx, r.Root, args...)
	if err != nil {
		return
	}
	unpushed := map[string]bool{}
	for _, hash := range splitLines(out) {
		unpushed[strings.TrimSpace(hash)] = true
	}
	for i := range rows {
		rows[i].Pushed = !unpushed[rows[i].Hash]
	}
}

// requireFilter refuses a filter value that git would read as an option or that
// carries control characters.
func requireFilter(raw, what string) error {
	if strings.HasPrefix(raw, "-") {
		return contentservice.Rejected("%s filter %q may not begin with a dash", what, raw)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return contentservice.Rejected("%s filter contains control characters", what)
		}
	}
	if len(raw) > contentservice.MaxLocatorBytes {
		return contentservice.Rejected("%s filter exceeds %d bytes", what, contentservice.MaxLocatorBytes)
	}
	return nil
}
