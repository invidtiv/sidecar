package uiapi

import (
	"context"
	"sync"
	"time"
)

const (
	legacyHolderTTL        = time.Second
	legacyHolderTimeout    = 2 * time.Second
	maxLegacyHolderEntries = 1024
	maxLegacyHolderReads   = 4
)

type holderSession struct {
	host, session string
}

type holderObservation struct {
	holder  *GeometryHolder
	expires time.Time
	pending chan struct{}
}

// legacyHolderCache shares read-only lease observations between credentials,
// attachments and events streams. It caches null observations too. Negotiated
// holder messages never pass through this fallback.
type legacyHolderCache struct {
	mu      sync.Mutex
	entries map[holderSession]*holderObservation
	reads   int
}

func copyGeometryHolder(holder *GeometryHolder) *GeometryHolder {
	if holder == nil {
		return nil
	}
	copy := *holder
	return &copy
}

func (c *legacyHolderCache) get(ctx, lifetime context.Context, now func() time.Time, source GeometryHolderSource, term TerminalInfo) *GeometryHolder {
	key := holderSession{term.OwnerHostID, term.Session}
	c.mu.Lock()
	entry := c.entries[key]
	if entry != nil && entry.pending == nil && now().Before(entry.expires) {
		holder := copyGeometryHolder(entry.holder)
		c.mu.Unlock()
		return holder
	}
	if entry != nil && entry.pending != nil {
		done := entry.pending
		c.mu.Unlock()
		return c.wait(ctx, entry, done)
	}
	// Saturation must not bypass the cache and start an unbounded read. Keep
	// the last observation, or null on first observation, until the next tick.
	if c.reads >= maxLegacyHolderReads {
		var holder *GeometryHolder
		if entry != nil {
			holder = copyGeometryHolder(entry.holder)
		}
		c.mu.Unlock()
		return holder
	}
	if entry == nil {
		if c.entries == nil {
			c.entries = make(map[holderSession]*holderObservation)
		}
		if len(c.entries) >= maxLegacyHolderEntries {
			// Only expired completed observations can be evicted. Fresh or
			// in-flight reads cannot multiply when another client connects.
			var oldest holderSession
			var expires time.Time
			for candidate, observation := range c.entries {
				if observation.pending == nil && !now().Before(observation.expires) && (expires.IsZero() || observation.expires.Before(expires)) {
					oldest, expires = candidate, observation.expires
				}
			}
			if expires.IsZero() {
				c.mu.Unlock()
				return nil
			}
			delete(c.entries, oldest)
		}
		entry = &holderObservation{}
		c.entries[key] = entry
	}
	done := make(chan struct{})
	entry.pending = done
	c.reads++
	c.mu.Unlock()
	// The shared read belongs to the API process, not whichever events
	// client happened to ask first. Disconnecting it only cancels its wait.
	go func() {
		readCtx, cancel := context.WithTimeout(lifetime, legacyHolderTimeout)
		defer cancel()
		holder := source.GeometryHolder(readCtx, term)
		c.mu.Lock()
		entry.holder = copyGeometryHolder(holder)
		entry.expires = now().Add(legacyHolderTTL)
		entry.pending = nil
		c.reads--
		close(done)
		c.mu.Unlock()
	}()
	return c.wait(ctx, entry, done)
}

func (c *legacyHolderCache) wait(ctx context.Context, entry *holderObservation, done <-chan struct{}) *GeometryHolder {
	select {
	case <-ctx.Done():
		return nil
	case <-done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return copyGeometryHolder(entry.holder)
	}
}

func (s *Server) legacyHolder(ctx context.Context, source GeometryHolderSource, term TerminalInfo) *GeometryHolder {
	lifetime := s.ctx
	if lifetime == nil {
		lifetime = context.Background()
	}
	now := s.opts.Now
	if now == nil {
		now = time.Now
	}
	return s.holderCache.get(ctx, lifetime, now, source, term)
}
