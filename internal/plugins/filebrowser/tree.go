package filebrowser

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SortMode represents how files are sorted in the tree.
type SortMode int

const (
	SortByName SortMode = iota
	SortBySize
	SortByTime
	SortByType
)

// SortModeLabel returns a short label for display.
func (s SortMode) Label() string {
	switch s {
	case SortByName:
		return "name"
	case SortBySize:
		return "size"
	case SortByTime:
		return "time"
	case SortByType:
		return "type"
	default:
		return "name"
	}
}

// NextSortMode cycles to the next sort mode.
func (s SortMode) Next() SortMode {
	return (s + 1) % 4
}

// FileNode represents a file or directory in the tree.
type FileNode struct {
	Name       string
	Path       string // Relative path from root
	IsDir      bool
	IsExpanded bool
	IsIgnored  bool // Set by gitignore
	Children   []*FileNode
	Parent     *FileNode
	Depth      int
	Size       int64
	ModTime    time.Time
}

// FileTree manages the hierarchical file structure.
type FileTree struct {
	Root     *FileNode
	RootDir  string
	FlatList []*FileNode // Flattened visible nodes for cursor navigation
	// source lists directories. Local and remote trees differ here and only
	// here; see TreeSource.
	source      TreeSource
	listings    map[string]DirListing // batched answers not yet consumed
	SortMode    SortMode              // Current sort mode
	ShowIgnored bool                  // Whether to include ignored files in FlatList
}

// NewFileTree creates a new file tree rooted at the given directory on this
// machine.
func NewFileTree(rootDir string) *FileTree {
	return NewFileTreeWithSource(rootDir, newLocalTreeSource(rootDir))
}

// NewFileTreeWithSource creates a tree that lists through source. RootDir is
// still the identity the rest of the plugin uses for a path; for a remote tree
// it is the host's root and must never be handed to a local os or git call.
func NewFileTreeWithSource(rootDir string, source TreeSource) *FileTree {
	return &FileTree{
		RootDir:     rootDir,
		FlatList:    make([]*FileNode, 0),
		source:      source,
		ShowIgnored: true, // Show ignored files by default
	}
}

// Build initializes the tree by loading the root directory's children.
func (t *FileTree) Build() error {
	if t.source == nil {
		t.source = newLocalTreeSource(t.RootDir)
	}
	t.Root = &FileNode{
		Name:       filepath.Base(t.RootDir),
		Path:       "",
		IsDir:      true,
		IsExpanded: true,
		Depth:      -1, // Root is hidden, children start at depth 0
	}

	if err := t.loadChildren(t.Root); err != nil {
		return err
	}

	t.Flatten()
	return nil
}

// BuildSpec fully describes a tree build. Every field is a value the caller owns
// outright, so a build can run on a background goroutine without touching the
// tree the UI is currently rendering.
type BuildSpec struct {
	RootDir       string
	SortMode      SortMode
	ShowIgnored   bool
	ExpandedPaths map[string]bool // Directories to re-expand after loading
	// Source lists the directories. Nil means this machine's filesystem, which
	// keeps every existing caller and test valid.
	Source TreeSource
}

// BuildTree constructs a brand-new tree from spec. It shares no state with any
// existing FileTree, which is what makes it safe to call off the UI goroutine:
// the caller swaps the result in when the resulting message is handled.
func BuildTree(spec BuildSpec) (*FileTree, error) {
	source := spec.Source
	if source == nil {
		source = newLocalTreeSource(spec.RootDir)
	}
	t := NewFileTreeWithSource(spec.RootDir, source)
	t.SortMode = spec.SortMode
	t.ShowIgnored = spec.ShowIgnored

	// One batched listing for the root plus everything that was expanded. A
	// per-level walk is a handful of ReadDirs locally and a round trip per
	// level over ssh, which is the whole reason ListDirs takes a batch.
	t.prefetch(spec.ExpandedPaths)

	if err := t.Build(); err != nil {
		return nil, err
	}

	t.RestoreExpandedPaths(spec.ExpandedPaths)
	return t, nil
}

// prefetch asks the source for the root and every remembered expanded path at
// once. Paths that turn out to be unreachable cost nothing: the answers are
// consumed by path as loadChildren reaches them, and whatever is left over is
// dropped with the map.
func (t *FileTree) prefetch(expanded map[string]bool) {
	rels := make([]string, 0, len(expanded)+1)
	rels = append(rels, "")
	for path := range expanded {
		if path != "" {
			rels = append(rels, filepath.ToSlash(path))
		}
	}
	sort.Strings(rels)
	t.listings = t.source.ListDirs(rels)
}

// isSystemFile returns true for OS-generated files that clutter file browsers.
func isSystemFile(name string) bool {
	// Exact matches
	switch name {
	case ".DS_Store", ".Spotlight-V100", ".Trashes", ".fseventsd",
		".TemporaryItems", ".DocumentRevisions-V100",
		"Thumbs.db", "desktop.ini", "$RECYCLE.BIN":
		return true
	}
	// macOS resource fork files (._*)
	if strings.HasPrefix(name, "._") {
		return true
	}
	return false
}

// loadChildren populates a node's children from the tree's source.
//
// Which entries a tree hides is decided here and nowhere else. A remote
// listing reports what is on the host's disk; skipping OS clutter is this
// viewer's presentation rule, and applying it half on each side of the wire is
// how two surfaces come to disagree about what a directory contains.
func (t *FileTree) loadChildren(node *FileNode) error {
	listing, err := t.listingFor(node.Path)
	if err != nil {
		return err
	}

	node.Children = make([]*FileNode, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		if isSystemFile(entry.Name) {
			continue
		}
		node.Children = append(node.Children, &FileNode{
			Name:      entry.Name,
			Path:      filepath.Join(node.Path, entry.Name),
			IsDir:     entry.IsDir,
			IsIgnored: entry.IsIgnored,
			Parent:    node,
			Depth:     node.Depth + 1,
			Size:      entry.Size,
			ModTime:   entry.ModTime,
		})
	}

	sortChildren(node.Children, t.SortMode)
	return nil
}

// listingFor consumes a prefetched answer when there is one, and otherwise
// asks the source for this directory alone — the shape a user expanding an
// unvisited directory produces.
func (t *FileTree) listingFor(rel string) (DirListing, error) {
	key := filepath.ToSlash(rel)
	if listing, ok := t.listings[key]; ok {
		delete(t.listings, key)
		return listing, listing.Err
	}
	if t.source == nil {
		t.source = newLocalTreeSource(t.RootDir)
	}
	listing := t.source.ListDirs([]string{key})[key]
	return listing, listing.Err
}

// sortChildren sorts nodes according to the given mode.
func sortChildren(children []*FileNode, mode SortMode) {
	sort.Slice(children, func(i, j int) bool {
		// Directories always come before files
		if children[i].IsDir != children[j].IsDir {
			return children[i].IsDir
		}

		switch mode {
		case SortBySize:
			// Larger files first
			if children[i].Size != children[j].Size {
				return children[i].Size > children[j].Size
			}
			// Fall back to name
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)

		case SortByTime:
			// Newer files first
			if !children[i].ModTime.Equal(children[j].ModTime) {
				return children[i].ModTime.After(children[j].ModTime)
			}
			// Fall back to name
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)

		case SortByType:
			// Sort by extension, then by name
			exti := strings.ToLower(filepath.Ext(children[i].Name))
			extj := strings.ToLower(filepath.Ext(children[j].Name))
			if exti != extj {
				return exti < extj
			}
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)

		default: // SortByName
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
		}
	})
}

// Expand opens a directory node, reloading its children from disk. Children
// loaded by an earlier expand are re-read rather than reused, so a directory
// that changed while it was collapsed opens with what is actually there. The
// expanded directories inside the subtree stay expanded.
//
// On a read error the previously loaded children are kept, so a directory that
// briefly becomes unreadable does not blank out.
func (t *FileTree) Expand(node *FileNode) error {
	if !node.IsDir {
		return nil
	}

	expanded := make(map[string]bool)
	t.collectExpanded(node, expanded)

	if err := t.loadChildren(node); err != nil {
		return err
	}

	node.IsExpanded = true
	if len(expanded) > 0 {
		t.restoreExpanded(node, expanded)
	}
	t.Flatten()
	return nil
}

// Collapse closes a directory node.
func (t *FileTree) Collapse(node *FileNode) {
	node.IsExpanded = false
	t.Flatten()
}

// Toggle expands or collapses a directory node.
func (t *FileTree) Toggle(node *FileNode) error {
	if !node.IsDir {
		return nil
	}

	if node.IsExpanded {
		t.Collapse(node)
		return nil
	}
	return t.Expand(node)
}

// Flatten rebuilds the FlatList from visible nodes.
func (t *FileTree) Flatten() []*FileNode {
	t.FlatList = t.FlatList[:0] // Reuse slice
	if t.Root != nil {
		t.flattenNode(t.Root)
	}
	return t.FlatList
}

func (t *FileTree) flattenNode(node *FileNode) {
	for _, child := range node.Children {
		// Skip ignored files/folders when ShowIgnored is false
		if !t.ShowIgnored && child.IsIgnored {
			continue
		}
		t.FlatList = append(t.FlatList, child)
		if child.IsDir && child.IsExpanded {
			t.flattenNode(child)
		}
	}
}

// GetNode returns the node at the given index, or nil if out of bounds.
func (t *FileTree) GetNode(index int) *FileNode {
	if index < 0 || index >= len(t.FlatList) {
		return nil
	}
	return t.FlatList[index]
}

// Len returns the number of visible nodes.
func (t *FileTree) Len() int {
	return len(t.FlatList)
}

// FindParentDir returns the parent directory node, or nil if at root.
func (t *FileTree) FindParentDir(node *FileNode) *FileNode {
	if node == nil || node.Parent == nil || node.Parent == t.Root {
		return nil
	}
	return node.Parent
}

// IndexOf returns the index of a node in the flat list, or -1 if not found.
func (t *FileTree) IndexOf(node *FileNode) int {
	for i, n := range t.FlatList {
		if n == node {
			return i
		}
	}
	return -1
}

// FindByPath returns the node with the given relative path, or nil if not found.
func (t *FileTree) FindByPath(path string) *FileNode {
	idx := t.IndexOfPath(path)
	if idx < 0 {
		return nil
	}
	return t.FlatList[idx]
}

// IndexOfPath returns the flat-list index of the node with the given relative
// path, or -1 if no visible node has that path.
func (t *FileTree) IndexOfPath(path string) int {
	for i, n := range t.FlatList {
		if n.Path == path {
			return i
		}
	}
	return -1
}

// GetExpandedPaths returns the paths of all expanded directories.
func (t *FileTree) GetExpandedPaths() map[string]bool {
	expanded := make(map[string]bool)
	if t.Root != nil {
		t.collectExpanded(t.Root, expanded)
	}
	return expanded
}

func (t *FileTree) collectExpanded(node *FileNode, expanded map[string]bool) {
	for _, child := range node.Children {
		if child.IsDir && child.IsExpanded {
			expanded[child.Path] = true
			t.collectExpanded(child, expanded)
		}
	}
}

// RestoreExpandedPaths expands directories that were previously expanded.
func (t *FileTree) RestoreExpandedPaths(paths map[string]bool) {
	if t.Root == nil || len(paths) == 0 {
		return
	}
	t.restoreExpanded(t.Root, paths)
	t.Flatten()
}

// SetExpandedPaths makes the tree's expansion match paths exactly: directories
// named in the set are expanded (loading their children if needed) and every
// other visible directory is collapsed. Unlike RestoreExpandedPaths it can undo
// an expansion, which is what a rebuilt tree needs to pick up a collapse the
// user made while the build was running.
func (t *FileTree) SetExpandedPaths(paths map[string]bool) {
	if t.Root == nil {
		return
	}
	t.setExpanded(t.Root, paths)
	t.Flatten()
}

func (t *FileTree) setExpanded(node *FileNode, paths map[string]bool) {
	for _, child := range node.Children {
		if !child.IsDir {
			continue
		}
		if !paths[child.Path] {
			// Nested state is left alone, exactly as Collapse leaves it.
			child.IsExpanded = false
			continue
		}
		if len(child.Children) == 0 {
			_ = t.loadChildren(child)
		}
		child.IsExpanded = true
		t.setExpanded(child, paths)
	}
}

func (t *FileTree) restoreExpanded(node *FileNode, paths map[string]bool) {
	for _, child := range node.Children {
		if child.IsDir && paths[child.Path] {
			// Load children if needed and expand
			if len(child.Children) == 0 {
				_ = t.loadChildren(child)
			}
			child.IsExpanded = true
			t.restoreExpanded(child, paths)
		}
	}
}

// SetSortMode changes the sort mode and re-sorts the tree.
func (t *FileTree) SetSortMode(mode SortMode) {
	t.SortMode = mode
	if t.Root != nil {
		t.resortNode(t.Root)
		t.Flatten()
	}
}

// resortNode recursively re-sorts a node and its children.
func (t *FileTree) resortNode(node *FileNode) {
	if len(node.Children) > 0 {
		sortChildren(node.Children, t.SortMode)
		for _, child := range node.Children {
			if child.IsDir {
				t.resortNode(child)
			}
		}
	}
}
