// Package terminalperf exposes privacy-safe counters for the visible terminal
// pipeline. The hot path records only fixed event kinds: terminal text, paths,
// targets, session names, and provider payloads cannot enter a probe.
package terminalperf

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event is one fixed terminal-pipeline operation.
type Event uint8

const (
	ModelFrameBuilt Event = iota + 1
	ModelFramePublished
	TerminalViewRendered
	RowCacheHit
	RowCacheMiss
	ContentLinkResolutionRequest
	ContentLinkResolutionCacheHit
	SynchronousResolverCall
	GlobalWorkspaceListRendered
	GlobalWorkspacePreviewRendered
	ApplicationViewRendered
	ProjectWorkspaceViewRendered
	ProjectSidebarRendered
	ProjectPreviewComposed
	ProjectPreviewCacheHit
	DocumentFrameBuilt
	DocumentFrameCacheHit
	DocumentLinkScan
	DocumentResolutionRequest
	DocumentResolutionCacheHit
	RowAnalyzerBypass
	eventMax
)

// Counters is an injectable recorder. It is intentionally data-free apart from
// counts so enabling it can never retain terminal content or user identity.
type Counters struct {
	modelFramesBuilt               atomic.Uint64
	modelFramesPublished           atomic.Uint64
	terminalViewsRendered          atomic.Uint64
	rowCacheHits                   atomic.Uint64
	rowCacheMisses                 atomic.Uint64
	contentLinkResolutionRequests  atomic.Uint64
	contentLinkResolutionCacheHits atomic.Uint64
	synchronousResolverCalls       atomic.Uint64
	globalWorkspaceListRendered    atomic.Uint64
	globalWorkspacePreviewRendered atomic.Uint64
	applicationViewsRendered       atomic.Uint64
	projectWorkspaceViewsRendered  atomic.Uint64
	projectSidebarRendered         atomic.Uint64
	projectPreviewComposes         atomic.Uint64
	projectPreviewCacheHits        atomic.Uint64
	documentFramesBuilt            atomic.Uint64
	documentFrameCacheHits         atomic.Uint64
	documentLinkScans              atomic.Uint64
	documentResolutionRequests     atomic.Uint64
	documentResolutionCacheHits    atomic.Uint64
	rowAnalyzerBypasses            atomic.Uint64
	outputToFrameMu                sync.Mutex
	outputToFrameSamples           uint64
	outputToFrameMaxUS             uint64
	outputToFrameBuckets           [outputToFrameBucketCount]uint64
}

// Snapshot is a point-in-time copy suitable for benchmark metrics or
// diagnostics. Field names are the complete diagnostic vocabulary.
type Snapshot struct {
	ModelFramesBuilt               uint64 `json:"model_frames_built"`
	ModelFramesPublished           uint64 `json:"model_frames_published"`
	TerminalViewsRendered          uint64 `json:"terminal_views_rendered"`
	RowCacheHits                   uint64 `json:"row_cache_hits"`
	RowCacheMisses                 uint64 `json:"row_cache_misses"`
	ContentLinkResolutionRequests  uint64 `json:"content_link_resolution_requests"`
	ContentLinkResolutionCacheHits uint64 `json:"content_link_resolution_cache_hits"`
	SynchronousResolverCalls       uint64 `json:"synchronous_resolver_calls"`
	GlobalWorkspaceListRendered    uint64 `json:"global_workspace_list_rendered"`
	GlobalWorkspacePreviewRendered uint64 `json:"global_workspace_preview_rendered"`
	ApplicationViewsRendered       uint64 `json:"application_views_rendered"`
	ProjectWorkspaceViewsRendered  uint64 `json:"project_workspace_views_rendered"`
	ProjectSidebarRendered         uint64 `json:"project_sidebar_rendered"`
	ProjectPreviewComposes         uint64 `json:"project_preview_composes"`
	ProjectPreviewCacheHits        uint64 `json:"project_preview_cache_hits"`
	DocumentFramesBuilt            uint64 `json:"document_frames_built"`
	DocumentFrameCacheHits         uint64 `json:"document_frame_cache_hits"`
	DocumentLinkScans              uint64 `json:"document_link_scans"`
	DocumentResolutionRequests     uint64 `json:"document_resolution_requests"`
	DocumentResolutionCacheHits    uint64 `json:"document_resolution_cache_hits"`
	RowAnalyzerBypasses            uint64 `json:"row_analyzer_bypasses"`
	OutputToFrameSamples           uint64 `json:"output_to_frame_samples"`
	OutputToFrameP95US             uint64 `json:"output_to_frame_p95_us"`
	OutputToFrameMaxUS             uint64 `json:"output_to_frame_max_us"`
}

const (
	outputToFrameBucketWidthUS = uint64(1000)
	// Buckets 0..99 cover <=1ms through <=100ms. The final bucket retains
	// larger observations; its reported upper bound is the observed maximum.
	outputToFrameBucketCount = 101
)

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
	case ContentLinkResolutionRequest:
		counters.contentLinkResolutionRequests.Add(n)
	case ContentLinkResolutionCacheHit:
		counters.contentLinkResolutionCacheHits.Add(n)
	case SynchronousResolverCall:
		counters.synchronousResolverCalls.Add(n)
	case GlobalWorkspaceListRendered:
		counters.globalWorkspaceListRendered.Add(n)
	case GlobalWorkspacePreviewRendered:
		counters.globalWorkspacePreviewRendered.Add(n)
	case ApplicationViewRendered:
		counters.applicationViewsRendered.Add(n)
	case ProjectWorkspaceViewRendered:
		counters.projectWorkspaceViewsRendered.Add(n)
	case ProjectSidebarRendered:
		counters.projectSidebarRendered.Add(n)
	case ProjectPreviewComposed:
		counters.projectPreviewComposes.Add(n)
	case ProjectPreviewCacheHit:
		counters.projectPreviewCacheHits.Add(n)
	case DocumentFrameBuilt:
		counters.documentFramesBuilt.Add(n)
	case DocumentFrameCacheHit:
		counters.documentFrameCacheHits.Add(n)
	case DocumentLinkScan:
		counters.documentLinkScans.Add(n)
	case DocumentResolutionRequest:
		counters.documentResolutionRequests.Add(n)
	case DocumentResolutionCacheHit:
		counters.documentResolutionCacheHits.Add(n)
	case RowAnalyzerBypass:
		counters.rowAnalyzerBypasses.Add(n)
	}
}

// RecordOutputToFrame records a fixed numeric duration only. Millisecond
// buckets are enough to decide the 50ms presentation gate without retaining
// terminal content, targets, or a per-frame timestamp trail.
func RecordOutputToFrame(duration time.Duration) {
	counters := active.Load()
	if counters == nil {
		return
	}
	microseconds := uint64(0)
	if duration > 0 {
		microseconds = uint64(duration.Microseconds())
	}
	bucket := uint64(0)
	if microseconds > 0 {
		bucket = (microseconds - 1) / outputToFrameBucketWidthUS
	}
	if bucket >= outputToFrameBucketCount-1 {
		bucket = outputToFrameBucketCount - 1
	}
	counters.outputToFrameMu.Lock()
	if microseconds > counters.outputToFrameMaxUS {
		counters.outputToFrameMaxUS = microseconds
	}
	counters.outputToFrameBuckets[bucket]++
	counters.outputToFrameSamples++
	counters.outputToFrameMu.Unlock()
}

func (c *Counters) outputToFrameSnapshot() (samples, p95, maximum uint64) {
	c.outputToFrameMu.Lock()
	defer c.outputToFrameMu.Unlock()
	samples = c.outputToFrameSamples
	maximum = c.outputToFrameMaxUS
	if samples == 0 {
		return samples, 0, maximum
	}
	rank := (samples*95 + 99) / 100
	var seen uint64
	for index := range outputToFrameBucketCount {
		seen += c.outputToFrameBuckets[index]
		if seen < rank {
			continue
		}
		if index == outputToFrameBucketCount-1 {
			return samples, maximum, maximum
		}
		upperBound := uint64(index+1) * outputToFrameBucketWidthUS
		if maximum < upperBound {
			return samples, maximum, maximum
		}
		return samples, upperBound, maximum
	}
	return samples, maximum, maximum
}

// Snapshot returns the current fixed counters.
func (c *Counters) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	latencySamples, latencyP95, latencyMax := c.outputToFrameSnapshot()
	return Snapshot{
		ModelFramesBuilt:               c.modelFramesBuilt.Load(),
		ModelFramesPublished:           c.modelFramesPublished.Load(),
		TerminalViewsRendered:          c.terminalViewsRendered.Load(),
		RowCacheHits:                   c.rowCacheHits.Load(),
		RowCacheMisses:                 c.rowCacheMisses.Load(),
		ContentLinkResolutionRequests:  c.contentLinkResolutionRequests.Load(),
		ContentLinkResolutionCacheHits: c.contentLinkResolutionCacheHits.Load(),
		SynchronousResolverCalls:       c.synchronousResolverCalls.Load(),
		GlobalWorkspaceListRendered:    c.globalWorkspaceListRendered.Load(),
		GlobalWorkspacePreviewRendered: c.globalWorkspacePreviewRendered.Load(),
		ApplicationViewsRendered:       c.applicationViewsRendered.Load(),
		ProjectWorkspaceViewsRendered:  c.projectWorkspaceViewsRendered.Load(),
		ProjectSidebarRendered:         c.projectSidebarRendered.Load(),
		ProjectPreviewComposes:         c.projectPreviewComposes.Load(),
		ProjectPreviewCacheHits:        c.projectPreviewCacheHits.Load(),
		DocumentFramesBuilt:            c.documentFramesBuilt.Load(),
		DocumentFrameCacheHits:         c.documentFrameCacheHits.Load(),
		DocumentLinkScans:              c.documentLinkScans.Load(),
		DocumentResolutionRequests:     c.documentResolutionRequests.Load(),
		DocumentResolutionCacheHits:    c.documentResolutionCacheHits.Load(),
		RowAnalyzerBypasses:            c.rowAnalyzerBypasses.Load(),
		OutputToFrameSamples:           latencySamples,
		OutputToFrameP95US:             latencyP95,
		OutputToFrameMaxUS:             latencyMax,
	}
}
