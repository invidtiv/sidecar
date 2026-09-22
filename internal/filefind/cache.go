package filefind

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// DefaultMaxAge is how long a file list is trusted without a rescan when its
// owner has not said otherwise.
//
// A cache is told about changes by whoever owns it, and no owner sees every
// change: the Files plugin watches only the directories the tree has expanded,
// so a file an agent writes into a collapsed directory never marks the cache
// dirty, and a finder opened an hour later still answers from the list it
// walked before the file existed. That is how "use-cases" once found six
// near-misses and not USE-CASES.md. An age limit is the honest fallback, and
// it is cheap: an aged list is re-checked with one stat per directory the walk
// recorded (see DirStamp), and only a tree that actually moved pays the walk
// again. The old list stays on screen either way until the answer lands.
//
// A cache whose scanner cannot stamp directories — a remote catalog — walks in
// full when it ages out, so an owner of one may want a longer MaxAge.
const DefaultMaxAge = 10 * time.Second

// timeNow is the clock the cache ages itself by; tests replace it.
var timeNow = time.Now

// ScannedMsg carries the result of a background cache scan. Dirs distinguishes
// a directory scan (path auto-complete) from a file scan.
type ScannedMsg struct {
	Dirs    bool
	Files   []string // Paths relative to the scanned root, sorted
	ErrText string   // Non-empty when the scan failed or hit a limit
	Epoch   uint64

	// Stamps are the directories the walk descended into, with their
	// modification times, so the next Ensure can ask whether anything moved
	// without walking again. Nil when the scanner cannot provide them.
	Stamps []DirStamp

	// Unchanged is set when an aged cache was probed rather than walked and
	// nothing had moved: Files and Stamps are empty and the list already held
	// is still the answer. Only the clock moves.
	Unchanged bool
}

// GetEpoch implements plugin.EpochMessage, so a scan issued for a project the
// caller has since switched away from can be dropped on arrival.
func (m ScannedMsg) GetEpoch() uint64 { return m.Epoch }

// Cache holds a project's file (or directory) list plus the bookkeeping that
// keeps a rescan from being disruptive: the previous answer stays readable for
// the whole time the new scan runs.
//
// The zero value is ready to use. Whether a cache holds files or directories is
// decided by which Ensure method its owner calls, so a cache built by a struct
// literal cannot end up scanning the wrong thing.
type Cache struct {
	Files   []string // Paths relative to the root, sorted
	ErrText string   // Error message if the scan failed or hit a limit

	Scanning bool // A background scan is in flight
	OK       bool // A scan has completed at least once

	// Dirty is set when the disk moved under the cache, so its contents no
	// longer describe what is there. A scan clears it at its start, so a change
	// arriving while that scan is in flight re-sets it and the landing result
	// cannot pass itself off as current.
	Dirty bool

	// Scanned is when the list now held was last confirmed current, by a walk
	// landing or by a probe finding nothing moved. It is the zero time until a
	// scan has completed.
	Scanned time.Time

	// Stamps are the directories the last walk recorded (see DirStamp). Empty
	// when the scanner did not provide them, in which case an aged cache
	// walks in full.
	Stamps []DirStamp

	// MaxAge is how long the list is trusted after Scanned before Ensure walks
	// again. Zero means DefaultMaxAge; a negative value means the list never
	// ages out, for an owner that is told about every change.
	MaxAge time.Duration

	// Scan produces the path list. Nil walks this machine's filesystem, which
	// is every local caller. A surface bound to another machine binds its own
	// so the candidate list is that machine's files and this process never
	// walks a same-named path here.
	Scan func(root string, dirs bool) ([]string, string)
}

// Ensure starts a background scan of root's files when the cache is missing or
// the disk has moved under it, and returns the command that runs it. It returns
// nil when a scan is already in flight or the cache is current. The existing
// contents are left in place until the result lands, so a UI keeps showing
// something usable.
func (c *Cache) Ensure(root string, epoch uint64) tea.Cmd {
	return c.ensure(root, epoch, false)
}

// EnsureDirs is Ensure for a directory list (path auto-complete).
func (c *Cache) EnsureDirs(root string, epoch uint64) tea.Cmd {
	return c.ensure(root, epoch, true)
}

func (c *Cache) ensure(root string, epoch uint64, dirs bool) tea.Cmd {
	if root == "" || c.Scanning {
		return nil
	}
	if c.OK && !c.Dirty && !c.Expired() {
		return nil
	}
	// An aged list that nothing has reported a change to is probed before it
	// is walked. A list that was told the disk moved, or that has never been
	// walked, or that came from a scanner with no stamps to check, walks.
	probe := c.OK && !c.Dirty && c.Scan == nil && len(c.Stamps) > 0
	stamps := c.Stamps
	// Cleared at the start of the scan: a change arriving while it runs re-sets
	// the flag, so the result it is about to deliver is not mistaken for fresh.
	c.Dirty = false
	c.Scanning = true
	// Everything the walk touches is passed by value: it loads its own gitignore
	// rather than sharing a live tree's, whose match cache is not safe for
	// concurrent use.
	scan := c.Scan
	return func() tea.Msg {
		if probe && treeUnchanged(root, stamps) {
			return ScannedMsg{Dirs: dirs, Epoch: epoch, Unchanged: true}
		}
		if scan != nil {
			paths, errText := scan(root, dirs)
			return ScannedMsg{Dirs: dirs, Files: paths, ErrText: errText, Epoch: epoch}
		}
		paths, stamps, errText := scanTree(root, dirs)
		return ScannedMsg{Dirs: dirs, Files: paths, Stamps: stamps, ErrText: errText, Epoch: epoch}
	}
}

// Apply stores a landed scan result. Callers are responsible for dropping stale
// results (see ScannedMsg.GetEpoch) before calling this.
func (c *Cache) Apply(msg ScannedMsg) {
	c.Scanning = false
	c.Scanned = timeNow()
	if msg.Unchanged {
		return
	}
	c.OK = true
	c.Files = msg.Files
	c.ErrText = msg.ErrText
	c.Stamps = msg.Stamps
}

// MarkDirty records that the disk changed, so the next Ensure rescans.
func (c *Cache) MarkDirty() { c.Dirty = true }

// Expired reports whether the list has outlived MaxAge. A cache that has never
// scanned is not expired, merely empty; Ensure handles that on its own. Nor is
// a list nobody dated — one an owner filled in by hand rather than through
// Apply — because it has no age to compare.
func (c *Cache) Expired() bool {
	if !c.OK || c.Scanned.IsZero() {
		return false
	}
	maxAge := c.MaxAge
	if maxAge == 0 {
		maxAge = DefaultMaxAge
	}
	if maxAge < 0 {
		return false
	}
	return timeNow().Sub(c.Scanned) > maxAge
}

// Reset drops the cache contents and all bookkeeping. Use it when the root
// changes, e.g. on a project switch. The scanner goes with it: a cache that
// kept a remote scanner across a switch back to a local project would answer
// for the wrong machine.
func (c *Cache) Reset() { *c = Cache{} }
