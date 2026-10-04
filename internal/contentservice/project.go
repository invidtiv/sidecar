package contentservice

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/config"
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
	ws, err := s.LookupWorkspace(ctx, workspace)
	if err != nil {
		return Workspace{}, err
	}
	if ws.Kind == kindWorktree || ws.Root != root {
		// Git keeps registrations for directories deleted outside Git. Resolve
		// the checkout itself too: a recreated directory or substituted symlink
		// must not borrow that registration (or redirect a layout store key).
		expectedPath := ws.Root
		if ws.Kind == kindWorktree {
			expectedPath = ws.Key
		}
		expected, err := filepath.Abs(expectedPath)
		if err != nil {
			return Workspace{}, Rejected("workspace no longer owns this worktree")
		}
		out, err := s.gitOutput(ctx, expected, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
		parts := strings.Split(strings.TrimSpace(string(out)), "\n")
		if err != nil || len(parts) != 2 || filepath.Clean(parts[0]) != filepath.Clean(expected) || ws.Root != filepath.Clean(expected) {
			return Workspace{}, Rejected("workspace no longer owns this worktree")
		}
		common, err := s.gitOutput(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || canonical(parts[1]) != canonical(strings.TrimSpace(string(common))) {
			return Workspace{}, Rejected("workspace no longer owns this worktree")
		}
	}
	return ws, nil
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
		policy, err := s.openProjectPolicy(ctx, ws.Root)
		if err != nil {
			return ReadResult{}, err
		}
		defer policy.close()
		rel, err := policy.path(params.Target)
		if err != nil {
			return ReadResult{}, err
		}
		file, err := policy.open(rel)
		if err != nil {
			return ReadResult{}, Rejected("file %q is not readable within the project: %v", rel, err)
		}
		defer func() { _ = file.Close() }()
		display, _ := projectRelative(params.Target)
		doc, err := readOpened(ctx, ResolvedFile{Display: display, Absolute: file.Name()}, file, params.IfRevision)
		if err != nil {
			return ReadResult{}, err
		}
		return readResultFrom(ws.ID, doc), nil
	}
	if params.Kind == KindDiff {
		policy, err := s.openProjectPolicy(ctx, ws.Root)
		if err != nil {
			return ReadResult{}, err
		}
		defer policy.close()
		if policy.metadata(".") {
			return ReadResult{}, refuseGitMetadata()
		}
		params.diffFilter = policy.diffFilter()
		if params.Path != "" {
			if _, err := policy.path(params.Path); err != nil {
				return ReadResult{}, err
			}
			// The Git path identifies the tree entry, not a symlink's file target.
			params.Path, err = projectRelative(params.Path)
			if err != nil {
				return ReadResult{}, err
			}
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
	policy, err := s.openProjectPolicy(ctx, ws.Root)
	if err != nil {
		return TreeResult{}, err
	}
	defer policy.close()
	for _, path := range paths {
		if path == "" {
			path = "."
		}
		if _, err := policy.path(path); err != nil {
			return TreeResult{}, err
		}
	}
	tree, err := s.treeWorkspaceOpen(ctx, ws, paths, policy.root, policy.open)
	return policy.filterTree(tree), err
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
