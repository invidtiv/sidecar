package filebrowser

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/hostproto"
)

// DirEntry is one child of a listed directory, from whichever machine owns the
// files. It is deliberately smaller than fs.DirEntry: everything a FileNode
// needs, and nothing that would tempt a caller into a second stat.
type DirEntry struct {
	Name      string
	IsDir     bool
	IsSymlink bool
	IsIgnored bool
	Size      int64
	ModTime   time.Time
}

// DirListing is one directory's answer. Err belongs to that directory alone: a
// remembered subdirectory that has gone away must not blank the tree around it.
type DirListing struct {
	Entries   []DirEntry
	Truncated bool
	Err       error
}

// TreeSource lists directories for a FileTree.
//
// This is the plugin's only structural read, and it is the whole of the local
// and remote difference. Everything above it — sorting, flattening, expansion
// memory, hiding system files, the cursor, the view — runs on the answer and
// cannot tell which machine produced it. There is no remote FileTree type, and
// there must not be one.
//
// ListDirs takes a batch because opening a tree means the root plus every
// directory the user had expanded. Locally that is a handful of ReadDirs;
// remotely it is the difference between one round trip and one per level.
type TreeSource interface {
	ListDirs(rels []string) map[string]DirListing
}

// localTreeSource lists this machine's filesystem.
type localTreeSource struct {
	root   string
	ignore *filefind.GitIgnore
}

func newLocalTreeSource(root string) *localTreeSource {
	ignore := filefind.NewGitIgnore()
	_ = ignore.LoadFile(filepath.Join(root, ".gitignore"))
	return &localTreeSource{root: root, ignore: ignore}
}

func (s *localTreeSource) ListDirs(rels []string) map[string]DirListing {
	out := make(map[string]DirListing, len(rels))
	for _, rel := range rels {
		out[rel] = s.listDir(rel)
	}
	return out
}

func (s *localTreeSource) listDir(rel string) DirListing {
	entries, err := os.ReadDir(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return DirListing{Err: err}
	}
	listing := DirListing{Entries: make([]DirEntry, 0, len(entries))}
	for _, entry := range entries {
		childRel := entry.Name()
		if rel != "" {
			childRel = rel + "/" + entry.Name()
		}
		child := DirEntry{
			Name:      entry.Name(),
			IsDir:     entry.IsDir(),
			IsSymlink: entry.Type()&fs.ModeSymlink != 0,
			IsIgnored: s.ignore.IsIgnored(filepath.FromSlash(childRel), entry.IsDir()),
		}
		info, err := entry.Info()
		if err != nil {
			// Skip files we cannot stat, exactly as this read always has.
			continue
		}
		child.Size = info.Size()
		child.ModTime = info.ModTime()
		listing.Entries = append(listing.Entries, child)
	}
	return listing
}

// remoteTreeSource lists a registered host's filesystem through
// `sidecar content tree`, over the ssh connection Sessions already holds open.
type remoteTreeSource struct {
	hostID      string
	workspaceID string
	run         func(ctx context.Context, hostID string, args []string, out any) error
	timeout     time.Duration
}

// remoteTreeTimeout bounds one listing call. A tree read that outlives the
// keypress that asked for it is how a quit comes to take a minute (td-052329),
// so this is short and the refusal is honest rather than a hang.
const remoteTreeTimeout = 20 * time.Second

func (s *remoteTreeSource) ListDirs(rels []string) map[string]DirListing {
	out := make(map[string]DirListing, len(rels))
	if len(rels) == 0 {
		return out
	}
	args := []string{"content", "tree", "--workspace", s.workspaceID}
	for _, rel := range rels {
		path := rel
		if path == "" {
			path = "."
		}
		args = append(args, "--path", path)
	}
	args = append(args, "--json")

	timeout := s.timeout
	if timeout <= 0 {
		timeout = remoteTreeTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var result contentservice.TreeResult
	if err := s.run(ctx, s.hostID, args, &result); err != nil {
		// One transport failure is every requested directory's failure. Saying
		// so per directory keeps the tree's existing behaviour: the branch
		// that could not be read keeps what it had.
		for _, rel := range rels {
			out[rel] = DirListing{Err: err}
		}
		return out
	}
	if !result.ValidRemoteResult() {
		err := fmt.Errorf("%s did not answer content tree", s.hostID)
		for _, rel := range rels {
			out[rel] = DirListing{Err: err}
		}
		return out
	}
	for _, dir := range result.Dirs {
		out[dir.Path] = remoteListing(dir)
	}
	for _, rel := range rels {
		if _, ok := out[rel]; !ok {
			out[rel] = DirListing{Err: fmt.Errorf("%s did not list %q", s.hostID, rel)}
		}
	}
	return out
}

func remoteListing(dir contentservice.TreeDir) DirListing {
	if dir.Err != "" {
		return DirListing{Err: fmt.Errorf("%s", dir.Err)}
	}
	listing := DirListing{Truncated: dir.Truncated, Entries: make([]DirEntry, 0, len(dir.Entries))}
	for _, entry := range dir.Entries {
		listing.Entries = append(listing.Entries, DirEntry{
			Name:      entry.Name,
			IsDir:     entry.Dir,
			IsSymlink: entry.Symlink,
			IsIgnored: entry.Ignored,
			Size:      entry.Size,
			ModTime:   entry.Modified,
		})
	}
	return listing
}

// remoteTreeUnavailable is why a bound Files tab cannot list a host, or "" when
// it can. The three reasons are distinct on purpose: "offline" is temporary and
// the user waits, "too old" is a host that needs updating, and "no workspace"
// is this viewer having nothing to browse yet. One combined sentence would send
// the user looking in the wrong place for two of the three.
func remoteTreeUnavailable(hostID, workspaceID string, verbs hostproto.VerbCapabilities, connected bool) string {
	switch {
	case hostID == "":
		return ""
	case !connected:
		return fmt.Sprintf("[%s] is not connected", hostID)
	case !verbs.ContentTreeV1:
		return fmt.Sprintf("[%s] runs a Sidecar that predates the file tree contract (sidecar content tree)", hostID)
	case strings.TrimSpace(workspaceID) == "":
		return fmt.Sprintf("no worktree on [%s] is bound yet", hostID)
	}
	return ""
}

// remoteCatalogFiles is the host's find-by-name index, from the picker catalog
// verb Sessions already uses. It is deliberately not derived from the tree: a
// tree is only as complete as the directories the user happened to expand,
// while the catalog is the host's own bounded, gitignore-filtered list.
func remoteCatalogFiles(ctx context.Context, run func(context.Context, string, []string, any) error, hostID, workspaceID string) ([]string, error) {
	if run == nil || hostID == "" || workspaceID == "" {
		return nil, nil
	}
	args := []string{"content", "catalog", "--workspace", workspaceID, "--kind", contentservice.KindFile, "--json"}
	var result contentservice.CatalogResult
	if err := run(ctx, hostID, args, &result); err != nil {
		return nil, err
	}
	if !result.ValidRemoteResult() {
		return nil, fmt.Errorf("%s did not answer content catalog", hostID)
	}
	return result.Files, nil
}
