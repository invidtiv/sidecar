package filefind

import (
	tea "charm.land/bubbletea/v2"
)

// ScannedMsg carries the result of a background cache scan. Dirs distinguishes
// a directory scan (path auto-complete) from a file scan.
type ScannedMsg struct {
	Dirs    bool
	Files   []string // Paths relative to the scanned root, sorted
	ErrText string   // Non-empty when the scan failed or hit a limit
	Epoch   uint64
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
	if c.OK && !c.Dirty {
		return nil
	}
	// Cleared at the start of the scan: a change arriving while it runs re-sets
	// the flag, so the result it is about to deliver is not mistaken for fresh.
	c.Dirty = false
	c.Scanning = true
	// Everything the walk touches is passed by value: it loads its own gitignore
	// rather than sharing a live tree's, whose match cache is not safe for
	// concurrent use.
	scan := c.Scan
	if scan == nil {
		scan = ScanPaths
	}
	return func() tea.Msg {
		paths, errText := scan(root, dirs)
		return ScannedMsg{Dirs: dirs, Files: paths, ErrText: errText, Epoch: epoch}
	}
}

// Apply stores a landed scan result. Callers are responsible for dropping stale
// results (see ScannedMsg.GetEpoch) before calling this.
func (c *Cache) Apply(msg ScannedMsg) {
	c.Scanning = false
	c.OK = true
	c.Files = msg.Files
	c.ErrText = msg.ErrText
}

// MarkDirty records that the disk changed, so the next Ensure rescans.
func (c *Cache) MarkDirty() { c.Dirty = true }

// Reset drops the cache contents and all bookkeeping. Use it when the root
// changes, e.g. on a project switch. The scanner goes with it: a cache that
// kept a remote scanner across a switch back to a local project would answer
// for the wrong machine.
func (c *Cache) Reset() { *c = Cache{} }
