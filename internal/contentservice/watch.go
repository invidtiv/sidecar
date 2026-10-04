package contentservice

import (
	"context"
	"errors"
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
	policy, err := s.openProjectPolicy(ctx, ws.Root)
	if err != nil {
		return nil, err
	}
	defer policy.close()
	switch params.Kind {
	case KindFile:
		rel, err := policy.path(params.Target)
		if err != nil {
			return nil, err
		}
		original, _ := projectRelative(params.Target)
		abs := filepath.Join(ws.Root, filepath.FromSlash(original))
		resolved := filepath.Join(ws.Root, filepath.FromSlash(rel))
		return []livewatch.Target{livewatch.File(abs), livewatch.File(resolved)}, nil
	case KindTree:
		path := params.Target
		if path == "" || path == "." {
			if _, err := policy.path("."); err != nil {
				return nil, err
			}
			return []livewatch.Target{livewatch.Dir(ws.Root)}, nil
		}
		rel, err := policy.path(path)
		if err != nil {
			return nil, err
		}
		original, _ := projectRelative(path)
		abs := filepath.Join(ws.Root, filepath.FromSlash(original))
		resolved := filepath.Join(ws.Root, filepath.FromSlash(rel))
		return []livewatch.Target{livewatch.Dir(abs), livewatch.Dir(resolved)}, nil
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
			rel, err := policy.path(path)
			if err != nil {
				return err
			}
			targets = append(targets, livewatch.File(filepath.Join(ws.Root, filepath.FromSlash(rel))))
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
					// A tracked symlink's patch contains its target string, never
					// the administrative contents. Keep aggregate Git watches but
					// do not register that target as ordinary project content.
					if errors.Is(err, errGitMetadata) {
						continue
					}
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
