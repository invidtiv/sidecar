package contentservice

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/noteview"
)

// WatchProject resolves only an open pane's filesystem dependencies. Like the
// desktop diff, it watches Git admin files and paths already under review; it
// never recursively walks the repository or polls git status.
func (s *Service) WatchProject(ctx context.Context, project, workspace string, params ReadParams) ([]livewatch.Target, error) {
	ws, err := s.LookupProject(ctx, project, workspace)
	if err != nil {
		return nil, err
	}
	if params.Kind != KindTree {
		if err := requireKind(params.Kind); err != nil {
			return nil, err
		}
		if err := requireOperation(params.Kind, params.Operation); err != nil {
			return nil, err
		}
	}
	switch params.Kind {
	case KindFile:
		if err = StrictRelative(params.Target); err != nil {
			return nil, err
		}
		_, abs, err := ContainedRelative(ws.Root, params.Target)
		if err != nil {
			return nil, err
		}
		return []livewatch.Target{livewatch.File(abs), livewatch.File(canonical(abs))}, nil
	case KindTree:
		path := params.Target
		if path == "" || path == "." {
			return []livewatch.Target{livewatch.Dir(ws.Root)}, nil
		}
		if err = StrictRelative(path); err != nil {
			return nil, err
		}
		_, abs, err := ContainedRelative(ws.Root, path)
		if err != nil {
			return nil, err
		}
		return []livewatch.Target{livewatch.Dir(abs), livewatch.Dir(canonical(abs))}, nil
	case KindIssue:
		doc, err := s.ReadProject(ctx, project, workspace, params)
		if err != nil {
			return nil, err
		}
		root := ws.Root
		if doc.Issue != nil && doc.Issue.Owner != nil {
			root = doc.Issue.Owner.Root
		}
		return issueview.StoreTargets(root), nil
	case KindNote:
		return noteview.StoreTargets(ws.Root), nil
	case KindDiff:
		doc, err := s.ReadProject(ctx, project, workspace, params)
		if err != nil {
			return nil, err
		}
		git := s.Git
		if git == nil {
			git = defaultGit
		}
		var targets []livewatch.Target
		for _, name := range []string{"index", "HEAD", "packed-refs", "FETCH_HEAD", "refs", "refs/heads"} {
			out, err := git(ctx, ws.Root, "rev-parse", "--path-format=absolute", "--git-path", name)
			if err != nil {
				return nil, Internal("resolve Git watch paths", err)
			}
			path := strings.TrimSpace(string(out))
			if name == "refs" || name == "refs/heads" {
				targets = append(targets, livewatch.Dir(path))
			} else {
				targets = append(targets, livewatch.File(path))
			}
		}
		add := func(path string) error {
			if err := StrictRelative(path); err != nil {
				return err
			}
			_, abs, err := ContainedRelative(ws.Root, path)
			if err != nil {
				return err
			}
			targets = append(targets, livewatch.File(abs))
			return nil
		}
		if params.Path != "" {
			if err = add(params.Path); err != nil {
				return nil, err
			}
		}
		if doc.Diff != nil && doc.Diff.Snapshot != nil {
			for _, file := range doc.Diff.Snapshot.Files {
				if err = add(file.Path); err != nil {
					return nil, err
				}
			}
		}
		dirs := map[string]bool{}
		for _, t := range targets {
			path := t.Path
			if !t.Dir {
				path = filepath.Dir(path)
			}
			dirs[path] = true
		}
		if len(dirs) > 64 {
			return nil, Rejected("diff watch exceeds 64 directories; open individual file panes")
		}
		return targets, nil
	}
	return nil, fmt.Errorf("unknown content watch kind %q", params.Kind)
}
