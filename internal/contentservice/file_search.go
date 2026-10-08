package contentservice

import (
	"context"
	"io/fs"
	"strings"

	"github.com/marcus/sidecar/internal/filefind"
)

const MaxFileSearchResults = 100
const MaxFileSearchRecent = 100
const MaxFileSearchQueryBytes = 512

type FileSearchParams struct {
	Query  string
	Limit  int
	Recent []string
}
type FileSearchMatch struct {
	Path string `json:"path"`
	// Positions are zero-based Unicode code point indices, not UTF-8 byte offsets.
	Positions []int `json:"positions"`
	Score     int   `json:"score"`
}
type FileSearchResult struct {
	Root      string            `json:"root"`
	Query     string            `json:"query"`
	Results   []FileSearchMatch `json:"results"`
	Truncated bool              `json:"truncated"`
	Warning   string            `json:"warning,omitempty"`
}

func ValidateFileSearch(p FileSearchParams) error {
	if len(p.Query) > MaxFileSearchQueryBytes || strings.ContainsRune(p.Query, 0) {
		return Rejected("file search query is too long or invalid")
	}
	if p.Limit < 0 || p.Limit > MaxFileSearchResults {
		return Rejected("file search limit must be between 1 and 100")
	}
	if len(p.Recent) > MaxFileSearchRecent {
		return Rejected("at most 100 recent file hints are accepted")
	}
	for _, path := range p.Recent {
		if len(path) > MaxLocatorBytes || strings.ContainsRune(path, 0) {
			return Rejected("recent file hint is too long or invalid")
		}
		if err := StrictRelative(path); err != nil {
			return err
		}
	}
	return nil
}

// SearchFiles searches an already-authorized project/workspace root. Both the
// CLI and HTTP resolve through LookupProject before calling this shared core.
func (s *Service) SearchFiles(ctx context.Context, index *filefind.Index, root string, p FileSearchParams) (FileSearchResult, error) {
	result := FileSearchResult{Root: root, Query: p.Query, Results: []FileSearchMatch{}}
	if err := ValidateFileSearch(p); err != nil {
		return result, err
	}
	if p.Limit == 0 {
		p.Limit = filefind.MaxMatches
	}
	policy, err := s.openProjectPolicy(ctx, root)
	if err != nil {
		return result, err
	}
	defer policy.close()
	// Do not descend into Git administrative directories (including custom
	// git-dir layouts), and refuse escaping symlinks before indexing them.
	allow := func(path string) bool { _, err := policy.path(path); return err == nil }
	files, warning, err := index.Paths(ctx, root, func(path string, entry fs.DirEntry) bool {
		if policy.metadata(path) {
			return false
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return allow(path)
		}
		return true
	})
	if err != nil {
		return result, err
	}
	result.Warning = warning
	// Cached paths can disappear or become denied links between index refreshes.
	// Expand the ranked prefix only when refusals remove results, so lower valid
	// matches still fill the requested list without rescanning the filesystem.
	count := p.Limit + 1
	for {
		matches, err := filefind.FilterContext(ctx, files, p.Query, count, filefind.FilterOptions{Recent: p.Recent})
		if err != nil {
			return result, err
		}
		result.Results = result.Results[:0]
		for _, match := range matches {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			rel, err := policy.path(match.Path)
			if err != nil {
				continue
			}
			info, err := policy.root.Stat(rel)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if len(result.Results) == p.Limit {
				result.Truncated = true
				break
			}
			positions := []int{}
			n := 0
			for off := range match.Path {
				for _, r := range match.MatchRanges {
					if off >= r.Start && off < r.End {
						positions = append(positions, n)
						break
					}
				}
				n++
			}
			result.Results = append(result.Results, FileSearchMatch{Path: match.Path, Positions: positions, Score: match.Score})
		}
		if result.Truncated || len(matches) < count || count >= len(files) {
			break
		}
		count = min(count*2, len(files))
	}
	return result, nil
}

// SearchProject resolves explicit ownership before invoking the shared finder.
func (s *Service) SearchProject(ctx context.Context, project, workspace string, index *filefind.Index, p FileSearchParams) (FileSearchResult, error) {
	ws, err := s.LookupProject(ctx, project, workspace)
	if err != nil {
		return FileSearchResult{}, err
	}
	return s.SearchFiles(ctx, index, ws.Root, p)
}
