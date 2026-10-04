package contentservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/rootfile"
	"github.com/marcus/sidecar/internal/workspacediff"
)

var errGitMetadata = errors.New("git administrative content is refused over the API")

// projectPolicy is scoped to an API call. Git's own resolver identifies both
// linked-worktree metadata and its common directory without guessing names.
// os.Root resolves caller paths without probing past an escaping symlink.
type projectPolicy struct {
	root    *os.Root
	gitDirs []string
}

func (s *Service) openProjectPolicy(ctx context.Context, root string) (*projectPolicy, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, Rejected("project root is not readable: %v", err)
	}
	p := &projectPolicy{root: dir}
	out, err := s.gitOutput(ctx, root, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		// Non-Git projects have ordinary content too. If metadata is present, a
		// failed discovery cannot safely grant access to an unknown separate dir.
		if _, statErr := dir.Lstat(".git"); !os.IsNotExist(statErr) {
			_ = dir.Close()
			return nil, Internal("resolve Git content policy", err)
		}
		if err = ctx.Err(); err != nil {
			_ = dir.Close()
			return nil, err
		}
		return p, nil
	}
	for _, path := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		rel, relErr := filepath.Rel(canonical(root), canonical(path))
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			p.gitDirs = append(p.gitDirs, filepath.ToSlash(rel))
		}
	}
	return p, nil
}

func (p *projectPolicy) close() { _ = p.root.Close() }

func (p *projectPolicy) metadata(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	for _, dir := range p.gitDirs {
		// Git metadata remains protected on case-insensitive filesystems.
		path, metadata := strings.ToLower(rel), strings.ToLower(dir)
		if metadata == "." || path == metadata || strings.HasPrefix(path, metadata+"/") {
			return true
		}
	}
	return false
}

func refuseGitMetadata() error {
	return &Error{Code: CodeRejected, Message: errGitMetadata.Error(), Err: errGitMetadata}
}

func (p *projectPolicy) path(raw string) (string, error) {
	rel, err := projectRelative(raw)
	if err != nil {
		return "", err
	}
	if p.metadata(rel) {
		return "", refuseGitMetadata()
	}
	resolved, err := p.resolve(rel)
	if err != nil {
		return "", err
	}
	if p.metadata(resolved) {
		return "", refuseGitMetadata()
	}
	return resolved, nil
}

// resolve follows only in-root relative links, including the existing parent
// of a missing file. Reject an escaping link before examining its target so
// present and absent out-of-root files produce identical refusals.
func (p *projectPolicy) resolve(rel string) (string, error) {
	pending := strings.Split(filepath.FromSlash(rel), string(filepath.Separator))
	resolved := "."
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if resolved == "." {
				return "", Rejected("path %q is not accessible within the project", rel)
			}
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, part)
		info, err := p.root.Lstat(next)
		if os.IsNotExist(err) {
			missing := filepath.Join(append([]string{next}, pending...)...)
			if missing == ".." || strings.HasPrefix(missing, ".."+string(filepath.Separator)) {
				return "", Rejected("path %q is not accessible within the project", rel)
			}
			return filepath.ToSlash(missing), nil
		}
		if err != nil {
			return "", Rejected("path %q is not accessible within the project", rel)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			resolved = next
			continue
		}
		links++
		if links > 40 {
			return "", Rejected("path %q has too many symbolic links", rel)
		}
		target, err := p.root.Readlink(next)
		if err != nil {
			return "", Rejected("path %q is not accessible within the project", rel)
		}
		if filepath.IsAbs(target) {
			return "", Rejected("path %q is not accessible within the project", rel)
		}
		// Keep target components in order: cleaning a/../b before resolving a
		// changes the result when a is itself a symlink.
		pending = append(strings.Split(target, string(filepath.Separator)), pending...)

	}
	return filepath.ToSlash(resolved), nil
}

func (p *projectPolicy) filterTree(tree TreeResult) TreeResult {
	for i := range tree.Dirs {
		entries := tree.Dirs[i].Entries
		kept := entries[:0]
		for _, entry := range entries {
			_, err := p.path(filepath.ToSlash(filepath.Join(tree.Dirs[i].Path, entry.Name)))
			if !errors.Is(err, errGitMetadata) {
				kept = append(kept, entry)
			}
		}
		tree.Dirs[i].Entries = kept
	}
	return tree
}

func (p *projectPolicy) open(raw string) (*os.File, error) {
	rel, err := p.path(raw)
	if err != nil {
		return nil, err
	}
	return rootfile.OpenNoFollow(p.root, rel)
}

// diffFilter excludes administrative paths before Git emits patches, and before
// the synthetic untracked-file reader inspects or opens any candidate path.
func (p *projectPolicy) diffFilter() *workspacediff.ReadFilter {
	excludes := []string{":(top,icase,exclude,glob)**/.git", ":(top,icase,exclude,glob)**/.git/**"}
	for _, dir := range p.gitDirs {
		excludes = append(excludes, ":(top,icase,exclude,literal)"+dir)
	}
	return &workspacediff.ReadFilter{ExcludePaths: excludes, AllowPath: func(path string) bool {
		_, err := p.path(path)
		return err == nil
	}}
}
