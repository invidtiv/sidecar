// Package terminalperf exposes privacy-safe counters for the visible terminal
// pipeline. The hot path records only fixed event kinds: terminal text, paths,
// targets, session names, and provider payloads cannot enter a probe.
package terminalperf

import "sync/atomic"

// Event is one fixed terminal-pipeline operation.
type Event uint8

const (
	ModelFrameBuilt Event = iota + 1
	ModelFramePublished
	TerminalViewRendered
	RowCacheHit
	RowCacheMiss
	CanvasInference
	ContentLinkResolutionRequest
	ContentLinkResolutionCacheHit
	SynchronousResolverCall
)

// Counters is an injectable recorder. It is intentionally data-free apart from
// counts so enabling it can never retain terminal content or user identity.
type Counters struct {
	modelFramesBuilt               atomic.Uint64
	modelFramesPublished           atomic.Uint64
	terminalViewsRendered          atomic.Uint64
	rowCacheHits                   atomic.Uint64
	rowCacheMisses                 atomic.Uint64
	canvasInferences               atomic.Uint64
	contentLinkResolutionRequests  atomic.Uint64
	contentLinkResolutionCacheHits atomic.Uint64
	synchronousResolverCalls       atomic.Uint64
}

// Snapshot is a point-in-time copy suitable for benchmark metrics or
// diagnostics. Field names are the complete diagnostic vocabulary.
type Snapshot struct {
	ModelFramesBuilt               uint64
	ModelFramesPublished           uint64
	TerminalViewsRendered          uint64
	RowCacheHits                   uint64
	RowCacheMisses                 uint64
	CanvasInferences               uint64
	ContentLinkResolutionRequests  uint64
	ContentLinkResolutionCacheHits uint64
	SynchronousResolverCalls       uint64
}

var active atomic.Pointer[Counters]

// Install routes subsequent events to counters until the returned restore
// function is called. It is intended for a benchmark, isolated proof, or a
// production diagnostic owner that can scope the installation.
func Install(counters *Counters) (restore func()) {
	previous := active.Swap(counters)
	return func() { active.Store(previous) }
}

// Record increments a fixed event counter when a probe is installed.
func Record(event Event) {
	Add(event, 1)
}

// Add records count occurrences without exposing dynamic labels.
func Add(event Event, count int) {
	if count <= 0 {
		return
	}
	counters := active.Load()
	if counters == nil {
		return
	}
	n := uint64(count)
	switch event {
	case ModelFrameBuilt:
		counters.modelFramesBuilt.Add(n)
	case ModelFramePublished:
		counters.modelFramesPublished.Add(n)
	case TerminalViewRendered:
		counters.terminalViewsRendered.Add(n)
	case RowCacheHit:
		counters.rowCacheHits.Add(n)
	case RowCacheMiss:
		counters.rowCacheMisses.Add(n)
	case CanvasInference:
		counters.canvasInferences.Add(n)
	case ContentLinkResolutionRequest:
		counters.contentLinkResolutionRequests.Add(n)
	case ContentLinkResolutionCacheHit:
		counters.contentLinkResolutionCacheHits.Add(n)
	case SynchronousResolverCall:
		counters.synchronousResolverCalls.Add(n)
	}
}

// Snapshot returns the current fixed counters.
func (c *Counters) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	return Snapshot{
		ModelFramesBuilt:               c.modelFramesBuilt.Load(),
		ModelFramesPublished:           c.modelFramesPublished.Load(),
		TerminalViewsRendered:          c.terminalViewsRendered.Load(),
		RowCacheHits:                   c.rowCacheHits.Load(),
		RowCacheMisses:                 c.rowCacheMisses.Load(),
		CanvasInferences:               c.canvasInferences.Load(),
		ContentLinkResolutionRequests:  c.contentLinkResolutionRequests.Load(),
		ContentLinkResolutionCacheHits: c.contentLinkResolutionCacheHits.Load(),
		SynchronousResolverCalls:       c.synchronousResolverCalls.Load(),
	}
}
