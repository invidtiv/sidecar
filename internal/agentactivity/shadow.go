package agentactivity

import (
	"encoding/json"
	"hash/fnv"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
)

// Shadow mode: run the vendored Herdr manifests alongside the Go rule tables on
// real panes, log where they disagree, and change nothing a user can see.
//
// It exists because the fixture corpus and the ported conformance suite prove
// the engine against captures somebody thought to save. A week of shadow
// running proves it against the screens that actually occur — the half-drawn
// frame, the resized pane, the overlay nobody minted a fixture for. Phase 2
// cuts providers over one at a time, and this is what says which ones are ready.
//
// The sink is a package-level variable rather than a config read inside Detect
// for two reasons. Detect is on the polling path and must not open a file or
// consult a feature manager per frame. And agentactivity is a leaf package that
// deliberately does not depend on internal/config; internal/app owns the state
// directory and wires the sink once at startup (never in a plugin Init, per the
// startup-latency rule).
//
// Two properties keep it from being a load generator on the poll path. A
// disagreement is logged only when a pane's verdict pair *changes*, because a
// steady disagreement — an idle Codex pane, where the Go table says
// `codex.screen.idle` with FallbackIdle false and the manifest lane falls back
// with FallbackIdle true — is otherwise one record per pane per poll, forever,
// at 200ms while a pane is active. And the write happens on a pump goroutine, so
// a slow or full disk delays no poll: the queue is bounded, over-full records
// are dropped, and the count of dropped records rides on the next one written.

// ShadowRecord is one disagreement between the two classifiers.
//
// It carries screen text only through the engine's own region previews: up to
// 240 characters per evaluated rule, which for a `whole_recent` rule is the top
// of the read window. That is real screen content, capped — enough to be worth a
// glance before a log is attached to an issue, and bounded enough that the file
// cannot grow without limit.
type ShadowRecord struct {
	At    time.Time `json:"at"`
	Agent string    `json:"agent"`
	// Command is the pane's foreground process name, because a disagreement
	// that only happens under a shared runtime is a different bug from one that
	// happens under the provider's own binary.
	Command string `json:"command"`
	// PaneHeight is the read window the manifest engine used. A disagreement
	// that only appears at one pane height is a windowing bug, not a rule bug,
	// and this is the field that says so.
	PaneHeight int `json:"paneHeight"`

	Go       ShadowVerdict `json:"go"`
	Manifest ShadowVerdict `json:"manifest"`
	// Explain is present the first time a pane produces a given verdict pair and
	// omitted when that same pair comes back, because the second copy of a
	// multi-kilobyte record says nothing the first did not.
	Explain *manifest.Explain `json:"explain,omitempty"`
	// Dropped counts the records the pump discarded since the last one it wrote,
	// so a log written while the disk was slow says so rather than silently
	// having holes in it.
	Dropped int `json:"dropped,omitempty"`
}

// ShadowVerdict is the comparable part of a Result.
type ShadowVerdict struct {
	State           State  `json:"state"`
	Evidence        string `json:"evidence"`
	SkipStateUpdate bool   `json:"skipStateUpdate"`
	FallbackIdle    bool   `json:"fallbackIdle"`
}

func shadowVerdict(r Result) ShadowVerdict {
	return ShadowVerdict{
		State:           r.State,
		Evidence:        r.Evidence,
		SkipStateUpdate: r.SkipStateUpdate,
		FallbackIdle:    r.FallbackIdle,
	}
}

// ShadowLogName is the file shadow disagreements are appended to, under the
// state directory.
const ShadowLogName = "agent-detection-shadow.jsonl"

const (
	// shadowQueueDepth is how many records may be waiting on the pump before
	// new ones are dropped. Records are written only when a pane's verdict pair
	// changes, so a backlog this deep means the sink itself has stalled, and the
	// right answer then is to drop and count rather than to hold up a poll.
	shadowQueueDepth = 256

	// shadowMaxPanes bounds the dedupe table. A machine does not have hundreds
	// of agent panes; if the key space ever grows past this the table is
	// cleared wholesale, which costs one extra record per pane and never grows.
	shadowMaxPanes = 256

	// shadowMaxTuplesPerPane bounds how many distinct verdict pairs one pane
	// remembers having explained. Past it, later records for that pane carry no
	// Explain, which is the same treatment a repeat gets.
	shadowMaxTuplesPerPane = 32
)

var (
	shadowMu   sync.RWMutex
	shadowSink func(ShadowRecord)

	shadowPumpRef atomic.Pointer[shadowPump]
)

// shadowKey identifies the pane a record came from.
//
// Detect is given an Observation and no pane identity — agentactivity is a leaf
// package and the polling surfaces do not pass one — so the key is the agent
// plus a hash of the pane title and foreground command. That is not a pane id:
// two identically-titled panes running the same agent share a key and so
// suppress each other's repeats. For shadow mode's purpose, which is finding
// disagreements the fixtures missed, that is the right trade: the first
// occurrence of every distinct verdict pair is still logged, and what collapses
// is only the duplicate.
type shadowKey struct {
	agent string
	pane  uint64
}

func shadowKeyFor(ob Observation) shadowKey {
	h := fnv.New64a()
	_, _ = h.Write([]byte(ob.PaneTitle))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(ob.CurrentCommand))
	return shadowKey{agent: ob.Agent, pane: h.Sum64()}
}

// shadowTuple is everything about a disagreement that decides whether it is the
// same disagreement as the last one. Comparable, so it is a map key.
type shadowTuple struct {
	goState          State
	goEvidence       string
	goSkip           bool
	goFallback       bool
	manifestState    State
	manifestRule     string
	manifestSkip     bool
	manifestFallback bool
}

// shadowPaneState is one pane's dedupe memory.
type shadowPaneState struct {
	last      shadowTuple
	hasLast   bool
	explained map[shadowTuple]bool
}

// shadowItem is what travels down the pump queue: a record to write, a flush
// marker to close, or a stop signal.
type shadowItem struct {
	record ShadowRecord
	done   chan struct{}
	stop   bool
}

// shadowPump owns one installed sink: its queue, its writer goroutine, and the
// per-pane dedupe table that decides what is worth queueing at all.
type shadowPump struct {
	sink  func(ShadowRecord)
	queue chan shadowItem
	done  chan struct{}

	mu    sync.Mutex
	panes map[shadowKey]*shadowPaneState

	drops atomic.Int64
}

func (p *shadowPump) run() {
	defer close(p.done)
	for item := range p.queue {
		switch {
		case item.stop:
			return
		case item.done != nil:
			close(item.done)
		default:
			p.sink(item.record)
		}
	}
}

// note records that this pane produced this verdict pair and answers two
// questions: is it different from the pane's last one (so worth writing at all),
// and is it the first time this pane has produced it (so worth an Explain).
func (p *shadowPump) note(key shadowKey, tuple shadowTuple) (changed, withExplain bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.panes == nil {
		p.panes = make(map[shadowKey]*shadowPaneState)
	}
	state := p.panes[key]
	if state == nil {
		if len(p.panes) >= shadowMaxPanes {
			p.panes = make(map[shadowKey]*shadowPaneState)
		}
		state = &shadowPaneState{explained: make(map[shadowTuple]bool)}
		p.panes[key] = state
	}
	if state.hasLast && state.last == tuple {
		return false, false
	}
	state.last, state.hasLast = tuple, true
	if state.explained[tuple] || len(state.explained) >= shadowMaxTuplesPerPane {
		return true, false
	}
	state.explained[tuple] = true
	return true, true
}

// enqueue hands a record to the pump without ever blocking the caller. A full
// queue drops the record and counts it; the count rides on the next record that
// does get through.
func (p *shadowPump) enqueue(record ShadowRecord) {
	dropped := p.drops.Load()
	record.Dropped = int(dropped)
	select {
	case p.queue <- shadowItem{record: record}:
		if dropped > 0 {
			p.drops.Add(-dropped)
		}
	default:
		p.drops.Add(1)
	}
}

// SetShadowSink installs (or, with nil, removes) the shadow-mode sink. Detect
// runs the manifest lane only while a sink is installed, so the cost of shadow
// mode is exactly zero when it is off.
//
// Installing a sink starts the one goroutine that writes to it, and replacing or
// removing one drains what is already queued before returning, so a caller that
// tears shadow mode down does not lose the last few records.
func SetShadowSink(sink func(ShadowRecord)) {
	shadowMu.Lock()
	prev := shadowPumpRef.Load()
	shadowSink = sink
	if sink == nil {
		shadowPumpRef.Store(nil)
	} else {
		next := &shadowPump{
			sink:  sink,
			queue: make(chan shadowItem, shadowQueueDepth),
			done:  make(chan struct{}),
			panes: make(map[shadowKey]*shadowPaneState),
		}
		go next.run()
		shadowPumpRef.Store(next)
	}
	shadowMu.Unlock()

	if prev == nil {
		return
	}
	// The queue is never closed: a poll goroutine that loaded the old pump a
	// moment ago may still send into it, and sending on a closed channel is a
	// panic in the middle of a diagnostic. A stop item makes the writer finish
	// the backlog ahead of it and exit; anything that arrives afterwards fills
	// the buffer and is collected.
	select {
	case prev.queue <- shadowItem{stop: true}:
		<-prev.done
	case <-prev.done:
	}
}

// ShadowFlush blocks until every record queued before the call has been handed
// to the sink. Shadow writes are asynchronous, so this is how a test — or a
// shutdown path — reads a complete log rather than a partial one.
func ShadowFlush() {
	pump := shadowPumpRef.Load()
	if pump == nil {
		return
	}
	done := make(chan struct{})
	select {
	case pump.queue <- shadowItem{done: done}:
	case <-pump.done:
		return
	}
	select {
	case <-done:
	case <-pump.done:
	}
}

// ShadowSinkInstalled reports whether shadow mode is running.
func ShadowSinkInstalled() bool {
	shadowMu.RLock()
	defer shadowMu.RUnlock()
	return shadowSink != nil
}

// shadowLogMaxBytes caps the log. Past it the file is rotated to a single `.1`
// generation, so shadow mode's whole footprint on disk is bounded at twice this
// and an old rotation is overwritten rather than accumulating.
const shadowLogMaxBytes = 5 << 20

// NewShadowLog returns a sink that appends one JSON line per disagreement to
// stateDir/agent-detection-shadow.jsonl, rotating to
// agent-detection-shadow.jsonl.1 once the file would pass 5 MiB.
//
// Every failure is swallowed. A diagnostic that cannot be written must never
// break the poll it is observing, and there is no user-facing promise here to
// keep: shadow mode is a maintainer's instrument.
func NewShadowLog(stateDir string) func(ShadowRecord) {
	path := filepath.Join(stateDir, ShadowLogName)
	var mu sync.Mutex
	return func(record ShadowRecord) {
		line, err := json.Marshal(record)
		if err != nil {
			return
		}
		line = append(line, '\n')
		mu.Lock()
		defer mu.Unlock()
		if info, err := os.Stat(path); err == nil && info.Size()+int64(len(line)) > shadowLogMaxBytes {
			if err := os.Rename(path, path+".1"); err != nil {
				return
			}
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer func() { _ = file.Close() }()
		_, _ = file.Write(line)
	}
}

// compareInShadow runs the manifest lane and reports any disagreement. It is
// called only when a sink is installed.
func compareInShadow(ob Observation, goResult Result, sink func(ShadowRecord)) {
	manifestResult, explain := DetectManifest(ob)
	if goResult.State == manifestResult.State &&
		goResult.Evidence == manifestResult.Evidence &&
		goResult.SkipStateUpdate == manifestResult.SkipStateUpdate &&
		goResult.FallbackIdle == manifestResult.FallbackIdle {
		return
	}
	// The evidence strings are a rule id on one side and a rule id on the
	// other, and they are *expected* to differ for every matched rule until the
	// cutover: `claude.title.working` and `osc_title_working` name the same
	// finding in two vocabularies. Logging every one of those would bury the
	// disagreements that matter, so a difference in evidence alone is recorded
	// only when the two lanes also disagree about something a surface acts on.
	if goResult.State == manifestResult.State &&
		goResult.SkipStateUpdate == manifestResult.SkipStateUpdate &&
		goResult.FallbackIdle == manifestResult.FallbackIdle {
		return
	}
	at := ob.CapturedAt
	if at.IsZero() {
		at = time.Now()
	}
	record := ShadowRecord{
		At:         at,
		Agent:      ob.Agent,
		Command:    ob.CurrentCommand,
		PaneHeight: ob.PaneHeight,
		Go:         shadowVerdict(goResult),
		Manifest:   shadowVerdict(manifestResult),
		Explain:    explain,
	}

	pump := shadowPumpRef.Load()
	if pump == nil {
		// A sink installed by something other than SetShadowSink, which today
		// nothing does. Writing it inline is still better than losing it.
		sink(record)
		return
	}
	changed, withExplain := pump.note(shadowKeyFor(ob), shadowTupleFor(goResult, manifestResult, explain))
	if !changed {
		return
	}
	if !withExplain {
		record.Explain = nil
	}
	pump.enqueue(record)
}

func shadowTupleFor(goResult, manifestResult Result, explain *manifest.Explain) shadowTuple {
	rule := manifestResult.Evidence
	if explain != nil {
		if explain.MatchedRule != nil {
			rule = explain.MatchedRule.ID
		} else {
			rule = ""
		}
	}
	return shadowTuple{
		goState:          goResult.State,
		goEvidence:       goResult.Evidence,
		goSkip:           goResult.SkipStateUpdate,
		goFallback:       goResult.FallbackIdle,
		manifestState:    manifestResult.State,
		manifestRule:     rule,
		manifestSkip:     manifestResult.SkipStateUpdate,
		manifestFallback: manifestResult.FallbackIdle,
	}
}
