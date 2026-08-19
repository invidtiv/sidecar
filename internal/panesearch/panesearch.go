// Package panesearch is the pair of search surfaces a document pane can open
// on itself: the fuzzy file finder (ctrl+p) and the project-wide ripgrep search
// (f). Both are rooted at the pane's own directory, which is what makes them
// work unchanged in the project workspace and in the global Workspaces browser
// — a pane carries the workspace or shell directory it was opened against, and
// neither surface asks anything else about the host.
//
// It lives here rather than in one host for the reason the pane frame does: a
// capability that exists on one pane surface and not the other is the bug the
// parity rule forbids. The host owns only where the surface is drawn and what
// happens to the file it picks.
package panesearch

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/projectsearch"
)

// Kind names which surface a pane is showing.
type Kind int

const (
	KindFinder Kind = iota + 1
	KindProject
)

// Mode is one pane's live search surface. Exactly one of finder and search is
// set; the host talks to it through the small vocabulary below so neither the
// key path nor the render path asks which surface it is driving.
type Mode struct {
	kind   Kind
	finder *filefind.Finder
	search *projectsearch.Search
}

// Outcome is the two surfaces' Result narrowed to what a pane host can act on:
// drop the mode, or load a file into the pane.
type Outcome struct {
	Cancelled bool
	Open      bool
	Path      string
	Line      int
	NewTab    bool
}

// NewFinder opens the fuzzy file finder rooted at root, reusing (and dating)
// the caller's per-root file-list cache. The command it returns is the scan, if
// one was needed; hosts wrap it so its reply comes back to the right pane.
func NewFinder(caches *Caches, root string, epoch uint64) (*Mode, tea.Cmd) {
	finder := filefind.NewFinder(caches.For(root), root, epoch)
	scan := finder.Open()
	caches.NoteScan(root, scan != nil)
	return &Mode{kind: KindFinder, finder: finder}, scan
}

// NewProject opens the ripgrep project search rooted at root.
func NewProject(root string, epoch uint64) *Mode {
	return &Mode{kind: KindProject, search: projectsearch.New(root, epoch)}
}

func (m *Mode) Kind() Kind {
	if m == nil {
		return 0
	}
	return m.kind
}

func (m *Mode) Name() string {
	if m != nil && m.kind == KindProject {
		return "Search"
	}
	return "Find"
}

// Query is what the user has typed, for the pane header.
func (m *Mode) Query() string {
	if m == nil {
		return ""
	}
	if m.kind == KindProject {
		if m.search != nil && m.search.State != nil {
			return m.search.State.Query
		}
		return ""
	}
	if m.finder != nil {
		return m.finder.Query()
	}
	return ""
}

// HeaderLabel is the mode's identity in the pane header: the surface's name and
// the query so far, so a pane in search mode never reads as a pane showing a
// file.
func (m *Mode) HeaderLabel() string {
	if m == nil {
		return ""
	}
	label := "⌕ " + m.Name()
	if q := m.Query(); q != "" {
		label += " " + q
	}
	return label
}

// Close releases whatever the surface still owns. The finder owns nothing (its
// file list belongs to the caller's per-root cache); the project search owns a
// running ripgrep process, which would otherwise keep going to its timeout.
func (m *Mode) Close() {
	if m == nil {
		return
	}
	if m.kind == KindProject {
		m.search.Close()
	}
}

func (m *Mode) SetSize(width, height int) {
	if m == nil || m.kind != KindProject || m.search == nil {
		return
	}
	if width > 0 && height > 0 {
		m.search.SetSize(width, height)
	}
}

// View draws the surface at the given size. fill is panemodal's answer to
// whether the box has room to show the pane around the modal; passing it
// through is what makes the tight case a modal that owns the pane rather than a
// small box floating on an empty field.
func (m *Mode) View(width, height int, fill bool, handler *mouse.Handler) string {
	if m == nil {
		return ""
	}
	if m.kind == KindProject {
		if m.search == nil {
			return ""
		}
		m.search.SetFill(fill)
		return m.search.View(width, height, handler)
	}
	if m.finder == nil {
		return ""
	}
	m.finder.SetFill(fill)
	return m.finder.View(width, height, handler)
}

func (m *Mode) HandleKey(msg tea.KeyPressMsg) (Outcome, tea.Cmd) {
	if m == nil {
		return Outcome{Cancelled: true}, nil
	}
	if m.kind == KindProject {
		if m.search == nil {
			return Outcome{Cancelled: true}, nil
		}
		res, cmd := m.search.HandleKey(msg)
		return projectSearchOutcome(res), cmd
	}
	if m.finder == nil {
		return Outcome{Cancelled: true}, nil
	}
	// The finder has no "beside what I am looking at" key of its own: it reports
	// NewTab only because hosts that have tabs need somewhere to put the answer.
	// shift+enter is that key here, matching the project search's.
	if msg.String() == "shift+enter" {
		matches := m.finder.Matches()
		if cursor := m.finder.Cursor(); cursor >= 0 && cursor < len(matches) {
			path := matches[cursor].Path
			m.finder.Reset()
			return Outcome{Open: true, Path: path, NewTab: true}, nil
		}
		return Outcome{}, nil
	}
	res, cmd := m.finder.HandleKey(msg)
	return finderOutcome(res), cmd
}

func (m *Mode) HandleMouse(msg tea.MouseMsg, handler *mouse.Handler) (Outcome, tea.Cmd) {
	if m == nil {
		return Outcome{}, nil
	}
	if m.kind == KindProject {
		if m.search == nil {
			return Outcome{}, nil
		}
		res, cmd := m.search.HandleMouse(msg, handler)
		return projectSearchOutcome(res), cmd
	}
	if m.finder == nil {
		return Outcome{}, nil
	}
	res, cmd := m.finder.HandleMouse(msg, handler)
	return finderOutcome(res), cmd
}

// Update feeds the surface its own async traffic. Both surfaces drop messages
// stamped with an epoch other than the one they were opened at.
func (m *Mode) Update(msg tea.Msg) tea.Cmd {
	if m == nil {
		return nil
	}
	if m.kind == KindProject {
		if m.search == nil {
			return nil
		}
		return m.search.Update(msg)
	}
	if m.finder == nil {
		return nil
	}
	return m.finder.Update(msg)
}

func finderOutcome(res filefind.Result) Outcome {
	switch res.Outcome {
	case filefind.OutcomeCancelled:
		return Outcome{Cancelled: true}
	case filefind.OutcomeOpen:
		return Outcome{Open: true, Path: res.Path, Line: res.Line, NewTab: res.NewTab}
	}
	return Outcome{}
}

func projectSearchOutcome(res projectsearch.Result) Outcome {
	switch res.Outcome {
	case projectsearch.OutcomeCancelled:
		return Outcome{Cancelled: true}
	// A pane has no external editor to hand a hit to, and the gestures that
	// produce it — ctrl+e, a double-click on a row — are the user asking for the
	// hit rather than for an editor. They open it in the pane.
	case projectsearch.OutcomeOpen, projectsearch.OutcomeOpenExternal:
		return Outcome{Open: true, Path: res.Path, Line: res.Line, NewTab: res.NewTab}
	}
	return Outcome{}
}

// CacheTTL is how long a root's file list is trusted without a rescan.
//
// The Files plugin has a filesystem watcher and marks its cache dirty the moment
// the tree moves; the pane surfaces have no such signal, so there is nothing
// here to invalidate the list precisely. A short lifetime is the honest
// substitute: the second ctrl+p in a working session costs nothing, and a
// finder opened minutes later still sees files created since.
const CacheTTL = 30 * time.Second

type cacheEntry struct {
	cache   *filefind.Cache
	scanned time.Time
}

// Caches holds one file list per root, shared by every pane rooted there. Panes
// on one root are looking at one directory tree, so they walk it once between
// them rather than once per ctrl+p: a fresh cache per open meant every open
// paid a full ScanPaths walk (up to 50k files), and open/esc/open spawned walks
// whose results were then thrown away.
//
// The zero value is ready to use.
type Caches struct {
	entries map[string]*cacheEntry
}

// For returns the file list for root, rescanning it if the last walk has aged
// out.
func (c *Caches) For(root string) *filefind.Cache {
	if c.entries == nil {
		c.entries = make(map[string]*cacheEntry)
	}
	entry := c.entries[root]
	if entry == nil {
		entry = &cacheEntry{cache: &filefind.Cache{}}
		c.entries[root] = entry
	}
	if !entry.scanned.IsZero() && time.Since(entry.scanned) > CacheTTL {
		entry.cache.MarkDirty()
	}
	return entry.cache
}

// NoteScan records that a scan of root has just been issued. Only a scan that
// actually started moves the clock, so a cache that answered from memory keeps
// the age of the walk it is still showing.
func (c *Caches) NoteScan(root string, started bool) {
	if !started {
		return
	}
	if entry := c.entries[root]; entry != nil {
		entry.scanned = time.Now()
	}
}

// Finder is the live file finder, or nil when this is a project search. Hosts
// use it to ask the surface what it is showing; the routing above needs no such
// question.
func (m *Mode) Finder() *filefind.Finder {
	if m == nil {
		return nil
	}
	return m.finder
}

// Search is the live project search, or nil when this is a finder.
func (m *Mode) Search() *projectsearch.Search {
	if m == nil {
		return nil
	}
	return m.search
}

// Len is how many roots have a cached file list.
func (c *Caches) Len() int { return len(c.entries) }

// Scanned is when root's list was last walked, or the zero time if it has never
// been walked.
func (c *Caches) Scanned(root string) time.Time {
	if entry := c.entries[root]; entry != nil {
		return entry.scanned
	}
	return time.Time{}
}

// SetScanned backdates (or forwards) root's last walk. It exists so a caller
// can age a list out deliberately — the only lever there is, given that nothing
// watches these trees.
func (c *Caches) SetScanned(root string, at time.Time) {
	if entry := c.entries[root]; entry != nil {
		entry.scanned = at
	}
}
