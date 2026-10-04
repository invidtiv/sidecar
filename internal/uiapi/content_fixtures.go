package uiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/marcus/sidecar/internal/contentservice"
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
	if project != "fixture-project" {
		return contentservice.Workspace{}, contentservice.Rejected("no fixture project %q", project)
	}
	if workspace != "" && workspace != "fixture-project" {
		return contentservice.Workspace{}, contentservice.Rejected("no fixture workspace %q", workspace)
	}
	return contentservice.Workspace{ID: "fixture-project", Root: "/workspace/fixture"}, nil
}
func (b *FixtureBackend) ReadProject(ctx context.Context, project, workspace string, p contentservice.ReadParams) (contentservice.ReadResult, error) {
	if _, err := b.LookupProject(ctx, project, workspace); err != nil {
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
	return doc, nil
}
func (b *FixtureBackend) TreeProject(ctx context.Context, project, workspace string, paths []string) (contentservice.TreeResult, error) {
	if _, err := b.LookupProject(ctx, project, workspace); err != nil {
		return contentservice.TreeResult{}, err
	}
	if len(paths) > contentservice.MaxTreePaths {
		return contentservice.TreeResult{}, contentservice.Rejected("too many fixture directories")
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	result := contentservice.TreeResult{Kind: "tree", Workspace: "fixture-project", Dirs: []contentservice.TreeDir{}}
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
