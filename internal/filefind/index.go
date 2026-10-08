package filefind

import (
	"context"
	"io/fs"
	"os"
	"sync"
	"time"
)

// Index caches bounded file lists for API callers, sharing the TUI scan and
// freshness policy. Its zero value is ready to use. At most 32 roots are kept.
type Index struct {
	mu      sync.Mutex
	entries map[string]*indexEntry
}
type indexEntry struct {
	gate     chan struct{}
	cache    Cache
	identity os.FileInfo
	used     time.Time
}

// Paths returns an immutable snapshot. Concurrent requests for one root share
// one scan; waiting, walking, and freshness checks honor caller cancellation.
func (idx *Index) Paths(ctx context.Context, root string, allow func(string, fs.DirEntry) bool) ([]string, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	idx.mu.Lock()
	if idx.entries == nil {
		idx.entries = make(map[string]*indexEntry)
	}
	entry := idx.entries[root]
	if entry == nil {
		if len(idx.entries) >= 32 {
			var oldest string
			for key, e := range idx.entries {
				if oldest == "" || e.used.Before(idx.entries[oldest].used) {
					oldest = key
				}
			}
			delete(idx.entries, oldest)
		}
		entry = &indexEntry{gate: make(chan struct{}, 1)}
		idx.entries[root] = entry
	}
	entry.used = time.Now()
	idx.mu.Unlock()
	select {
	case entry.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	defer func() { <-entry.gate }()
	info, err := os.Stat(root)
	if err != nil {
		return nil, "", err
	}
	c := &entry.cache
	if entry.identity == nil || !os.SameFile(entry.identity, info) {
		c.Reset()
		entry.identity = info
	}
	if !c.OK || c.Expired() {
		if c.OK && unchangedContext(ctx, root, c.Stamps) {
			c.Scanned = time.Now()
		} else {
			files, stamps, warning := ScanTreeContext(ctx, root, false, allow)
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
			c.Apply(ScannedMsg{Files: files, Stamps: stamps, ErrText: warning})
		}
	}
	return c.Files, c.ErrText, nil
}

func unchangedContext(ctx context.Context, root string, stamps []DirStamp) bool {
	if len(stamps) == 0 {
		return false
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return false
	}
	defer func() { _ = dir.Close() }()
	probe, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()
	for _, stamp := range stamps {
		if probe.Err() != nil {
			return false
		}
		info, err := dir.Stat(stamp.Path)
		if err != nil || !info.ModTime().Equal(stamp.ModTime) {
			return false
		}
	}
	return true
}
