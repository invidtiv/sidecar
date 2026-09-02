package contentservice

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/filefind"
)

// KindTree is the machine-contract kind for `sidecar content tree`.
const KindTree = "tree"

const (
	// MaxTreePaths caps how many directories one call may list. A viewer asks
	// for the root plus the directories it has expanded, so this is generous
	// for the real shape and still a bound on a hostile argument list.
	MaxTreePaths = 256

	// MaxTreeEntries caps one directory's listing. A directory this large is
	// not navigable in a tree pane, and the honest answer there is a truncated
	// listing the viewer can say is truncated — not a payload that pushes the
	// whole call past MaxEncodedBytes and returns nothing at all.
	MaxTreeEntries = 5000
)

// TreeResult is the machine contract for `sidecar content tree --json`.
//
// Kind is always "tree" and Workspace is the durable id that was listed. Dirs
// holds one entry per requested path, in the order they were requested, so a
// viewer can pair answers with requests without matching on strings.
type TreeResult struct {
	Kind      string    `json:"kind"`
	Workspace string    `json:"workspace"`
	Dirs      []TreeDir `json:"dirs"`
}

// TreeDir is one directory's listing.
//
// Err is per directory rather than per call on purpose: a viewer asks for the
// root plus everything it had expanded, and one of those may have been deleted
// or made unreadable since. Failing the whole call would blank a tree because
// one remembered subdirectory went away.
type TreeDir struct {
	// Path is relative to the workspace root, slash-separated. "" is the root.
	Path      string      `json:"path"`
	Entries   []TreeEntry `json:"entries,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
	Err       string      `json:"err,omitempty"`
}

// TreeEntry is one child of a listed directory.
//
// Dir follows os.ReadDir: a symlink pointing at a directory is not a Dir, it
// is a Symlink. The listing does not follow links, so a viewer cannot be led
// out of the workspace by one.
type TreeEntry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir,omitempty"`
	Symlink  bool      `json:"symlink,omitempty"`
	Ignored  bool      `json:"ignored,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
// A login banner has neither kind tree nor a workspace id.
func (r TreeResult) ValidRemoteResult() bool {
	return r.Kind == KindTree && strings.TrimSpace(r.Workspace) != ""
}

// Tree lists directories under a workspace root for a viewing Sidecar's file
// tree. An empty paths list means the root alone.
//
// Presentation is not decided here. The listing reports what is on disk and
// whether git ignores it; which entries a tree hides, how they sort, and what
// an ignored entry looks like are the viewer's rules, and they must stay in
// one place rather than being half-applied on each side of the wire.
func (s *Service) Tree(ctx context.Context, workspaceID string, paths []string) (TreeResult, error) {
	if err := ctx.Err(); err != nil {
		return TreeResult{}, err
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return TreeResult{}, err
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	if len(paths) > MaxTreePaths {
		return TreeResult{}, Rejected("a tree request may list at most %d directories", MaxTreePaths)
	}

	ignore := filefind.NewGitIgnore()
	_ = ignore.LoadFile(filepath.Join(ws.Root, ".gitignore"))

	result := TreeResult{Kind: KindTree, Workspace: ws.ID, Dirs: make([]TreeDir, 0, len(paths))}
	for _, raw := range paths {
		if err := ctx.Err(); err != nil {
			return TreeResult{}, err
		}
		rel, abs, err := resolveTreeDir(ws.Root, raw)
		if err != nil {
			// A path that escapes the root is a rejected request, not a
			// directory that happens to be missing. Answering it per-directory
			// would let a caller probe the filesystem one refusal at a time.
			return TreeResult{}, err
		}
		result.Dirs = append(result.Dirs, listTreeDir(rel, abs, ignore))
	}
	return result, nil
}

// resolveTreeDir maps a requested path onto a directory inside the workspace.
// Only relative paths are accepted: unlike a file target, there is no case for
// browsing a directory outside the workspace a viewer asked about.
func resolveTreeDir(root, raw string) (rel, abs string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "." {
		return "", root, nil
	}
	if err := validateLocator(raw, "path"); err != nil {
		return "", "", err
	}
	if filepath.IsAbs(raw) || isHomeToken(raw) {
		return "", "", Rejected("path %q must be relative to the workspace root", raw)
	}
	rel = filepath.Clean(filepath.FromSlash(raw))
	abs = filepath.Join(root, rel)
	if !contained(root, abs) {
		return "", "", Rejected("path %q escapes workspace root", raw)
	}
	return filepath.ToSlash(rel), abs, nil
}

func listTreeDir(rel, abs string, ignore *filefind.GitIgnore) TreeDir {
	dir := TreeDir{Path: rel}
	entries, err := os.ReadDir(abs)
	if err != nil {
		dir.Err = readDirReason(rel, err)
		return dir
	}
	if len(entries) > MaxTreeEntries {
		entries = entries[:MaxTreeEntries]
		dir.Truncated = true
	}
	dir.Entries = make([]TreeEntry, 0, len(entries))
	for _, entry := range entries {
		child := TreeEntry{Name: entry.Name(), Dir: entry.IsDir()}
		child.Symlink = entry.Type()&fs.ModeSymlink != 0
		childRel := entry.Name()
		if rel != "" {
			childRel = rel + "/" + entry.Name()
		}
		child.Ignored = ignore.IsIgnored(childRel, entry.IsDir())
		if info, err := entry.Info(); err == nil {
			child.Size = info.Size()
			child.Modified = info.ModTime().UTC()
		}
		dir.Entries = append(dir.Entries, child)
	}
	// Sorted by name so the wire order is stable across filesystems. The
	// viewer re-sorts into its own mode; a stable order here is what makes two
	// calls for the same directory comparable.
	sort.Slice(dir.Entries, func(i, j int) bool { return dir.Entries[i].Name < dir.Entries[j].Name })
	return dir
}

func readDirReason(rel string, err error) string {
	name := rel
	if name == "" {
		name = "."
	}
	if os.IsNotExist(err) {
		return "directory " + name + " no longer exists"
	}
	if os.IsPermission(err) {
		return "directory " + name + " is not readable"
	}
	return "directory " + name + " could not be listed: " + err.Error()
}
