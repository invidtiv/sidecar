package contentservice

import (
	"context"
	"github.com/marcus/sidecar/internal/config"
	"os"
	"path/filepath"
	"strings"
)

// LookupProject resolves an explicitly configured project and an optional
// durable workspace belonging to it. No ambient current-project fallback.
func (s *Service) LookupProject(ctx context.Context, project, workspace string) (Workspace, error) {
	projects, err := s.projects()
	if err != nil {
		return Workspace{}, err
	}
	var root string
	for _, p := range projects {
		if p.Name == project || canonical(config.ExpandPath(p.Path)) == project {
			if root != "" {
				return Workspace{}, Rejected("project %q is ambiguous", project)
			}
			root = canonical(config.ExpandPath(p.Path))
		}
	}
	if root == "" {
		return Workspace{}, Rejected("project %q is not configured", project)
	}
	if workspace == "" {
		return Workspace{ID: root + ":worktree:" + root, Kind: kindWorktree, Root: root, Key: root}, nil
	}
	_, projectKey, _, ok := parseWorkspaceID(workspace)
	if !ok || canonical(projectKey) != root {
		return Workspace{}, Rejected("workspace does not belong to project %q", project)
	}
	return s.LookupWorkspace(ctx, workspace)
}

// ReadProject applies the HTTP project's path boundary before the ordinary
// typed content read. Markdown is a file document, rendered by the client.
func (s *Service) ReadProject(ctx context.Context, project, workspace string, params ReadParams) (ReadResult, error) {
	ws, err := s.LookupProject(ctx, project, workspace)
	if err != nil {
		return ReadResult{}, err
	}
	if err := requireKind(params.Kind); err != nil {
		return ReadResult{}, err
	}
	if err := requireOperation(params.Kind, params.Operation); err != nil {
		return ReadResult{}, err
	}
	if params.Kind == KindResource {
		return ReadResult{}, UnknownKind(params.Kind)
	}
	if params.Kind == KindFile {
		rel, err := projectRelative(params.Target)
		if err != nil {
			return ReadResult{}, err
		}
		doc, err := readContainedFile(ctx, ws.Root, rel, params.IfRevision)
		if err != nil {
			return ReadResult{}, err
		}
		return readResultFrom(ws.ID, doc), nil
	}
	if params.Kind == KindDiff && params.Path != "" {
		rel, err := projectRelative(params.Path)
		if err != nil {
			return ReadResult{}, err
		}
		if err := checkRooted(ws.Root, rel); err != nil {
			return ReadResult{}, err
		}
	}
	return s.readWorkspace(ctx, ws, params)
}

// TreeProject lists only explicitly requested directories of this project.
func (s *Service) TreeProject(ctx context.Context, project, workspace string, paths []string) (TreeResult, error) {
	ws, err := s.LookupProject(ctx, project, workspace)
	if err != nil {
		return TreeResult{}, err
	}
	for _, path := range paths {
		if err := StrictRelative(path); err != nil {
			return TreeResult{}, err
		}
	}
	dir, err := os.OpenRoot(ws.Root)
	if err != nil {
		return TreeResult{}, Rejected("project root is not readable: %v", err)
	}
	defer func() { _ = dir.Close() }()
	return s.treeWorkspaceRead(ctx, ws, paths, dir)
}

// StrictRelative rejects traversal components even when cleaning would leave
// the final path inside the root. It is shared by API reads and subscriptions.
func StrictRelative(raw string) error {
	if filepath.IsAbs(raw) || isHomeToken(raw) {
		return Rejected("path must be relative to the project root")
	}
	for _, part := range strings.Split(filepath.ToSlash(raw), "/") {
		if part == ".." {
			return Rejected("path traversal is refused")
		}
	}
	return nil
}

// projectRelative cleans an API locator lexically and nothing more.
// Containment, symlinks included, is decided only through os.Root, which
// refuses at an escaping link without looking beyond it. Resolving the path
// first with EvalSymlinks answered differently for an existing and a missing
// out-of-root target, an oracle for what exists outside the project.
func projectRelative(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if err := validateLocator(raw, "path"); err != nil {
		return "", err
	}
	if err := StrictRelative(raw); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw))), nil
}

// checkRooted refuses a path that is unreachable within root, through an
// escaping symlink at any component included. A missing in-root path passes:
// a deleted file still has a diff and a file not yet written can be watched.
func checkRooted(root, rel string) error {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return Rejected("project root is not readable: %v", err)
	}
	defer func() { _ = dir.Close() }()
	_, err = dir.Stat(filepath.FromSlash(rel))
	if err != nil && !os.IsNotExist(err) {
		return Rejected("path %q is not accessible within the project: %v", rel, err)
	}
	return nil
}
