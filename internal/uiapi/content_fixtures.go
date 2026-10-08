package uiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/livewatch"
)

func (b *FixtureBackend) loadContent(dir string) error {
	b.content = make(map[string]contentservice.ReadResult)
	for _, kind := range []string{"file", "issue", "note", "diff"} {
		path := filepath.Join(dir, "content-"+kind+".json")
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Size() > contentservice.MaxEncodedBytes {
			return fmt.Errorf("fixture %s is oversized", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc contentservice.ReadResult
		if err = json.Unmarshal(data, &doc); err != nil {
			return err
		}
		if doc.Kind != kind || !doc.ValidRemoteResult() {
			return fmt.Errorf("invalid content fixture %s", path)
		}
		b.content[kind] = doc
	}
	data, err := os.ReadFile(filepath.Join(dir, "content-tree.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) > contentservice.MaxEncodedBytes {
		return fmt.Errorf("tree fixture is oversized")
	}
	if err = json.Unmarshal(data, &b.tree); err != nil {
		return err
	}
	if !b.tree.ValidRemoteResult() {
		return fmt.Errorf("invalid content tree fixture")
	}
	return nil
}

func (b *FixtureBackend) LookupProject(_ context.Context, project, workspace string) (contentservice.Workspace, error) {
	if project != "fixture-project" && (b.workspace == nil || project != b.workspace.Project.Name) {
		return contentservice.Workspace{}, contentservice.Rejected("no fixture project %q", project)
	}
	project = "fixture-project"
	root := "/workspace/fixture"
	if b.workspace != nil {
		root = b.workspace.Project.Path
	}
	if workspace == "" || workspace == "fixture-project" {
		return contentservice.Workspace{ID: "fixture-project", Root: root}, nil
	}
	for _, section := range b.catalog.Sections {
		for _, row := range section.Rows {
			if row.ProjectID != project || (workspace != row.ContentWorkspaceID && workspace != row.ID && workspace != row.WorkspaceID) {
				continue
			}
			if row.ContentWorkspaceID == "" {
				// The legacy fixture shell belongs to the configured root,
				// independently of its synthetic terminal workspace identity.
				return contentservice.Workspace{ID: "fixture-project", Root: root}, nil
			}
			return contentservice.Workspace{ID: row.ContentWorkspaceID, Root: row.Path}, nil
		}
	}
	return contentservice.Workspace{}, contentservice.Rejected("no fixture workspace %q", workspace)
}
func (b *FixtureBackend) ReadProject(ctx context.Context, project, workspace string, p contentservice.ReadParams) (contentservice.ReadResult, error) {
	ws, err := b.LookupProject(ctx, project, workspace)
	if err != nil {
		return contentservice.ReadResult{}, err
	}
	doc, ok := b.content[p.Kind]
	if !ok {
		return contentservice.ReadResult{}, contentservice.Rejected("fixture content kind %q is unavailable", p.Kind)
	}
	target := doc.Target
	if p.Kind == "file" {
		target = doc.Display
	}
	if p.Target != "" && p.Target != target || p.Operation != doc.Operation || p.Path != "" || p.Parent != "" || p.Offset != 0 || p.Limit != 0 {
		return contentservice.ReadResult{}, contentservice.Rejected("no fixture content with that reference")
	}
	if p.IfRevision == doc.Revision {
		return contentservice.ReadResult{Kind: doc.Kind, Revision: doc.Revision, NotModified: true}, nil
	}
	doc.Workspace = ws.ID
	if doc.Path != "" {
		doc.Path = filepath.Join(ws.Root, doc.Display)
	}
	return doc, nil
}
func (b *FixtureBackend) TreeProject(ctx context.Context, project, workspace string, paths []string) (contentservice.TreeResult, error) {
	ws, err := b.LookupProject(ctx, project, workspace)
	if err != nil {
		return contentservice.TreeResult{}, err
	}
	if len(paths) > contentservice.MaxTreePaths {
		return contentservice.TreeResult{}, contentservice.Rejected("too many fixture directories")
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	result := contentservice.TreeResult{Kind: "tree", Workspace: ws.ID, Dirs: []contentservice.TreeDir{}}
	for _, path := range paths {
		if err := contentservice.StrictRelative(path); err != nil {
			return contentservice.TreeResult{}, err
		}
		found := false
		for _, dir := range b.tree.Dirs {
			if dir.Path == path || path == "." && dir.Path == "" {
				result.Dirs = append(result.Dirs, dir)
				found = true
				break
			}
		}
		if !found {
			result.Dirs = append(result.Dirs, contentservice.TreeDir{Path: path, Err: "directory is not in the fixture"})
		}
	}
	return result, nil
}
func (b *FixtureBackend) WatchProject(ctx context.Context, project, workspace string, p contentservice.ReadParams) ([]livewatch.Target, error) {
	if _, err := b.LookupProject(ctx, project, workspace); err != nil {
		return nil, err
	}
	if p.Kind == "tree" {
		_, err := b.TreeProject(ctx, project, workspace, []string{p.Target})
		return nil, err
	}
	_, err := b.ReadProject(ctx, project, workspace, p)
	return nil, err
}

func (b *FixtureBackend) SearchProject(ctx context.Context, project, workspace string, _ *filefind.Index, p contentservice.FileSearchParams) (contentservice.FileSearchResult, error) {
	ws, err := b.LookupProject(ctx, project, workspace)
	if err != nil {
		return contentservice.FileSearchResult{}, err
	}
	if err := contentservice.ValidateFileSearch(p); err != nil {
		return contentservice.FileSearchResult{}, err
	}
	files := []string{}
	if doc, ok := b.content["file"]; ok {
		files = append(files, doc.Display)
	}
	if p.Limit == 0 {
		p.Limit = filefind.MaxMatches
	}
	matches, err := filefind.FilterContext(ctx, files, p.Query, p.Limit+1, filefind.FilterOptions{Recent: p.Recent})
	if err != nil {
		return contentservice.FileSearchResult{}, err
	}
	result := contentservice.FileSearchResult{Root: ws.Root, Query: p.Query, Results: []contentservice.FileSearchMatch{}, Truncated: len(matches) > p.Limit}
	for _, match := range matches[:min(len(matches), p.Limit)] {
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
		result.Results = append(result.Results, contentservice.FileSearchMatch{Path: match.Path, Positions: positions, Score: match.Score})
	}
	return result, nil
}
