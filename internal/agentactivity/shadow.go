package agentactivity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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

// ShadowRecord is one disagreement between the two classifiers.
//
// It carries no screen text beyond the engine's own 240-character region
// previews, so a user can attach the log to an issue without reading every line
// first. That bound is the whole reason the previews are capped.
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

	Go       ShadowVerdict     `json:"go"`
	Manifest ShadowVerdict     `json:"manifest"`
	Explain  *manifest.Explain `json:"explain,omitempty"`
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

var (
	shadowMu   sync.RWMutex
	shadowSink func(ShadowRecord)
)

// SetShadowSink installs (or, with nil, removes) the shadow-mode sink. Detect
// runs the manifest lane only while a sink is installed, so the cost of shadow
// mode is exactly zero when it is off.
func SetShadowSink(sink func(ShadowRecord)) {
	shadowMu.Lock()
	shadowSink = sink
	shadowMu.Unlock()
}

// ShadowSinkInstalled reports whether shadow mode is running.
func ShadowSinkInstalled() bool {
	shadowMu.RLock()
	defer shadowMu.RUnlock()
	return shadowSink != nil
}

// NewShadowLog returns a sink that appends one JSON line per disagreement to
// stateDir/agent-detection-shadow.jsonl.
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
		mu.Lock()
		defer mu.Unlock()
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer func() { _ = file.Close() }()
		_, _ = file.Write(append(line, '\n'))
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
	sink(ShadowRecord{
		At:         at,
		Agent:      ob.Agent,
		Command:    ob.CurrentCommand,
		PaneHeight: ob.PaneHeight,
		Go:         shadowVerdict(goResult),
		Manifest:   shadowVerdict(manifestResult),
		Explain:    explain,
	})
}
