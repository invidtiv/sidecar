package contentservice

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/filefind"
)

const (
	// KindCatalog is the machine-contract kind for `sidecar content catalog`.
	KindCatalog = "catalog"

	catalogDiffLimit  = 15
	catalogIssueLimit = 30
	catalogNoteLimit  = 20
)

// CatalogResult is the machine contract for `sidecar content catalog --json`.
//
// Kind is always "catalog". Workspace is the durable id that was listed.
// KindFilter is the --kind that was requested, empty when every picker kind
// was listed. Arrays may be empty; a login log line is not this object.
type CatalogResult struct {
	Kind       string         `json:"kind"`
	Workspace  string         `json:"workspace"`
	KindFilter string         `json:"kindFilter,omitempty"`
	Files      []string       `json:"files,omitempty"`
	Diffs      []CatalogDiff  `json:"diffs,omitempty"`
	Issues     []CatalogIssue `json:"issues,omitempty"`
	Notes      []CatalogNote  `json:"notes,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
}

// CatalogDiff is one recent commit or branch the diff picker can offer.
type CatalogDiff struct {
	Identity string `json:"identity"`
	Label    string `json:"label"`
}

// CatalogIssue is one td issue the issue picker can offer.
type CatalogIssue struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Status  string `json:"status,omitempty"`
	Updated string `json:"updated,omitempty"`
}

// CatalogNote is one td note the note picker can offer.
type CatalogNote struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Updated string `json:"updated,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
// A log line has neither kind catalog nor a workspace id.
func (r CatalogResult) ValidRemoteResult() bool {
	return r.Kind == KindCatalog && strings.TrimSpace(r.Workspace) != ""
}

func requireCatalogKind(kind string) error {
	switch kind {
	case "", KindFile, KindIssue, KindNote, KindDiff:
		return nil
	default:
		return UnknownKind(kind)
	}
}

// Catalog lists picker candidates for a workspace. kind filters to one picker
// kind; empty lists files, diffs, issues, and notes together.
func (s *Service) Catalog(ctx context.Context, workspaceID, kind string) (CatalogResult, error) {
	if err := ctx.Err(); err != nil {
		return CatalogResult{}, err
	}
	if err := requireCatalogKind(kind); err != nil {
		return CatalogResult{}, err
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return CatalogResult{}, err
	}
	result := CatalogResult{Kind: KindCatalog, Workspace: ws.ID, KindFilter: kind}
	want := func(k string) bool { return kind == "" || kind == k }

	if want(KindFile) {
		result.Files = s.catalogFiles(ws.Root)
	}
	if want(KindDiff) {
		diffs, err := s.catalogDiffs(ctx, ws.Root)
		if err != nil {
			return CatalogResult{}, err
		}
		result.Diffs = diffs
	}
	if want(KindIssue) {
		issues, err := s.catalogIssues(ctx, ws.Root)
		if err != nil {
			return CatalogResult{}, err
		}
		result.Issues = issues
	}
	if want(KindNote) {
		notes, err := s.catalogNotes(ctx, ws.Root)
		if err != nil {
			return CatalogResult{}, err
		}
		result.Notes = notes
	}
	return result, nil
}

func (s *Service) catalogFiles(root string) []string {
	paths, _ := filefind.ScanPaths(root, false)
	if paths == nil {
		return []string{}
	}
	return paths
}

func (s *Service) catalogDiffs(ctx context.Context, root string) ([]CatalogDiff, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := s.gitOutput(ctx, root, "log", "--max-count="+strconv.Itoa(catalogDiffLimit), "--pretty=%h%x09%s")
	if err != nil {
		return []CatalogDiff{}, nil
	}
	var diffs []CatalogDiff
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		hash, subject, found := strings.Cut(line, "\t")
		if !found || hash == "" {
			continue
		}
		label := subject
		if label == "" {
			label = hash
		}
		diffs = append(diffs, CatalogDiff{Identity: hash, Label: hash + "  " + label})
	}
	branches, err := s.gitOutput(ctx, root, "branch", "--format=%(refname:short)")
	if err == nil {
		for _, branch := range strings.Split(strings.TrimSpace(string(branches)), "\n") {
			if branch == "" {
				continue
			}
			diffs = append(diffs, CatalogDiff{Identity: branch, Label: branch + "  (branch)"})
		}
	}
	if diffs == nil {
		return []CatalogDiff{}, nil
	}
	return diffs, nil
}

func (s *Service) catalogIssues(ctx context.Context, root string) ([]CatalogIssue, error) {
	list := s.ListIssues
	if list == nil {
		list = defaultListIssues
	}
	issues, err := list(ctx, root, catalogIssueLimit)
	if err != nil {
		return []CatalogIssue{}, nil
	}
	if issues == nil {
		return []CatalogIssue{}, nil
	}
	return issues, nil
}

func (s *Service) catalogNotes(ctx context.Context, root string) ([]CatalogNote, error) {
	list := s.ListNotes
	if list == nil {
		list = defaultListNotes
	}
	notes, err := list(ctx, root, catalogNoteLimit)
	if err != nil {
		return []CatalogNote{}, nil
	}
	if notes == nil {
		return []CatalogNote{}, nil
	}
	return notes, nil
}

func catalogNoteTitle(title, content string) string {
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

func catalogTime(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return raw
}

func parseTDListIssues(output []byte, limit int) []CatalogIssue {
	if len(output) == 0 {
		return nil
	}
	type rawIssue struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Status  string `json:"status"`
		Updated string `json:"updated_at"`
	}
	var issues []rawIssue
	if err := json.Unmarshal(output, &issues); err != nil {
		return nil
	}
	out := make([]CatalogIssue, 0, len(issues))
	for _, i := range issues {
		if i.ID == "" {
			continue
		}
		out = append(out, CatalogIssue{
			ID: i.ID, Title: strings.TrimSpace(i.Title), Status: i.Status, Updated: catalogTime(i.Updated),
		})
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func parseTDListNotes(output []byte, limit int) []CatalogNote {
	if len(output) == 0 {
		return nil
	}
	type rawNote struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Content string `json:"content"`
		Updated string `json:"updated_at"`
		Deleted any    `json:"deleted_at"`
	}
	var notes []rawNote
	if err := json.Unmarshal(output, &notes); err != nil {
		return nil
	}
	out := make([]CatalogNote, 0, len(notes))
	for _, n := range notes {
		if n.ID == "" || n.Deleted != nil {
			continue
		}
		out = append(out, CatalogNote{
			ID: n.ID, Title: catalogNoteTitle(n.Title, n.Content), Updated: catalogTime(n.Updated),
		})
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
