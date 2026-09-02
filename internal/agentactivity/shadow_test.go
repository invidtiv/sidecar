package agentactivity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// shadowCapture collects records from the pump goroutine. Shadow writes are
// asynchronous by design — a slow sink must never delay a poll — so a test that
// wants to count them locks around the slice and flushes before reading it.
type shadowCapture struct {
	mu      sync.Mutex
	records []ShadowRecord
}

func (c *shadowCapture) add(record ShadowRecord) {
	c.mu.Lock()
	c.records = append(c.records, record)
	c.mu.Unlock()
}

func (c *shadowCapture) all(t *testing.T) []ShadowRecord {
	t.Helper()
	ShadowFlush()
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ShadowRecord(nil), c.records...)
}

func captureShadow(t *testing.T) *shadowCapture {
	t.Helper()
	capture := &shadowCapture{}
	SetShadowSink(capture.add)
	t.Cleanup(func() { SetShadowSink(nil) })
	return capture
}

func TestShadowModeIsOffUntilASinkIsInstalled(t *testing.T) {
	if ShadowSinkInstalled() {
		t.Fatal("a sink was left installed by another test")
	}
	// With no sink, Detect must not even run the manifest lane. The observable
	// proof is that the verdict is the Go tables' and nothing is recorded.
	ob := Observation{Agent: "antigravity", CurrentCommand: "agy", Screen: "Generating...\nesc to cancel\n"}
	if got := Detect(ob); got.Evidence != "antigravity.screen.working" {
		t.Fatalf("Detect() = %+v, want the Go rule table's verdict", got)
	}
}

func TestShadowModeRecordsAStateDisagreement(t *testing.T) {
	capture := captureShadow(t)
	// Antigravity's upstream manifest has no rule for "Generating..." or the
	// "esc to cancel" footer, so the manifest lane falls back to idle while the
	// Go table says working. That is exactly the class of disagreement Phase 2
	// has to decide, and exactly what shadow mode is for.
	ob := Observation{Agent: "antigravity", CurrentCommand: "agy", Screen: "Generating...\nesc to cancel · Gemini 3.6 Flash · high\n"}
	result := Detect(ob)
	if result.State != StateWorking {
		t.Fatalf("Detect() = %+v; shadow mode must not change the returned verdict", result)
	}
	records := capture.all(t)
	if len(records) != 1 {
		t.Fatalf("recorded %d disagreements, want 1", len(records))
	}
	record := records[0]
	if record.Go.State != StateWorking || record.Manifest.State != StateIdle {
		t.Fatalf("record = %+v", record)
	}
	if record.Agent != "antigravity" || record.Command != "agy" {
		t.Fatalf("record identity = %+v", record)
	}
	if record.Explain == nil || record.Explain.FallbackReason == "" {
		t.Fatalf("record carries no explain: %+v", record.Explain)
	}
}

func TestShadowModeIgnoresEvidenceSpellingAlone(t *testing.T) {
	capture := captureShadow(t)
	// Both lanes call this blocked; they name the finding `codex.title.blocked`
	// and `osc_title_blocked`. Logging every such pair would bury the
	// disagreements that matter under one line per matched rule per poll.
	ob := Observation{Agent: "codex", CurrentCommand: "codex", PaneTitle: "[ ! ] Action Required | project"}
	if got := Detect(ob); got.State != StateBlocked {
		t.Fatalf("Detect() = %+v", got)
	}
	if records := capture.all(t); len(records) != 0 {
		t.Fatalf("recorded %d disagreements for a same-state pair: %+v", len(records), records)
	}
}

// TestShadowModeLogsAPaneOnlyWhenItsVerdictChanges is the property that keeps
// shadow mode from being a load generator.
//
// A steady disagreement is the common case, not the rare one: an idle Codex pane
// has the Go table at `codex.screen.idle` with FallbackIdle false while the
// manifest lane falls back with FallbackIdle true, and the workspace polls every
// 2s idle and every 200ms active. Logging each poll would be one open, append,
// close and one multi-kilobyte explain record per pane per poll, forever.
func TestShadowModeLogsAPaneOnlyWhenItsVerdictChanges(t *testing.T) {
	capture := captureShadow(t)
	ob := Observation{Agent: "antigravity", CurrentCommand: "agy", Screen: "Generating...\nesc to cancel · Gemini 3.6 Flash · high\n"}
	for i := 0; i < 50; i++ {
		Detect(ob)
	}
	if records := capture.all(t); len(records) != 1 {
		t.Fatalf("50 identical polls recorded %d disagreements, want 1", len(records))
	}
}

// TestShadowModeDropsTheExplainPayloadOnARepeatedVerdict covers the pane that
// oscillates. Each change is worth a line; the second copy of the same explain
// record for the same pane is not, and it is by far the biggest part of a line.
func TestShadowModeDropsTheExplainPayloadOnARepeatedVerdict(t *testing.T) {
	capture := captureShadow(t)
	working := Observation{Agent: "antigravity", CurrentCommand: "agy", Screen: "Generating...\nesc to cancel · Gemini 3.6 Flash · high\n"}
	blocked := Observation{Agent: "antigravity", CurrentCommand: "agy", Screen: "Do you trust the contents of this project?\nAntigravity CLI requires permission to read, edit, and execute files here.\n> Yes, I trust this folder\n  No, exit\n↑/↓ Navigate · enter Confirm\n"}

	Detect(working)
	Detect(blocked)
	Detect(working)

	records := capture.all(t)
	if len(records) != 3 {
		t.Fatalf("recorded %d disagreements, want one per change: %+v", len(records), records)
	}
	if records[0].Explain == nil {
		t.Fatal("the first record for a verdict pair must carry its explain")
	}
	if records[1].Explain == nil {
		t.Fatal("a newly seen verdict pair must carry its explain")
	}
	if records[2].Explain != nil {
		t.Fatalf("a repeat of an already-explained verdict pair must drop the payload: %+v", records[2].Explain)
	}
	if records[2].Go.State != records[0].Go.State || records[2].Manifest.State != records[0].Manifest.State {
		t.Fatalf("record 2 = %+v, want the same verdict pair as record 0 = %+v", records[2], records[0])
	}
}

// TestShadowModeDropsRatherThanBlockingAPoll proves the queue is bounded and
// that the loss is reported rather than silent. A stalled sink must cost the
// poll goroutine nothing but the records it could not take.
func TestShadowModeDropsRatherThanBlockingAPoll(t *testing.T) {
	capture := &shadowCapture{}
	release := make(chan struct{})
	first := true
	SetShadowSink(func(record ShadowRecord) {
		if first {
			first = false
			<-release
		}
		capture.add(record)
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		SetShadowSink(nil)
	})

	// Each pane is distinct, so each one is a change worth logging. One record
	// is in the stalled sink and shadowQueueDepth fit in the queue; the excess
	// has nowhere to go.
	const excess = 10
	total := 1 + shadowQueueDepth + excess
	for i := 0; i < total; i++ {
		Detect(Observation{
			Agent:          "antigravity",
			CurrentCommand: "agy",
			PaneTitle:      fmt.Sprintf("pane-%d", i),
			Screen:         "Generating...\nesc to cancel · Gemini 3.6 Flash · high\n",
		})
	}

	close(release)
	ShadowFlush()

	// One more poll from a pane nobody has seen, to carry the drop count.
	Detect(Observation{
		Agent:          "antigravity",
		CurrentCommand: "agy",
		PaneTitle:      "pane-carrier",
		Screen:         "Generating...\nesc to cancel · Gemini 3.6 Flash · high\n",
	})

	records := capture.all(t)
	if len(records) != total-excess+1 {
		t.Fatalf("sink saw %d records, want %d", len(records), total-excess+1)
	}
	last := records[len(records)-1]
	if last.Dropped != excess {
		t.Fatalf("the record after a stall reports %d drops, want %d", last.Dropped, excess)
	}
	for _, record := range records[:len(records)-1] {
		if record.Dropped != 0 {
			t.Fatalf("a record written before the stall reports %d drops", record.Dropped)
		}
	}
}

func TestShadowLogAppendsOneJSONLineAndCarriesNoRawScreen(t *testing.T) {
	dir := t.TempDir()
	SetShadowSink(NewShadowLog(dir))
	t.Cleanup(func() { SetShadowSink(nil) })

	screen := "Generating...\nesc to cancel\n"
	// The same pane twice, then a second pane: the repeat is suppressed, so the
	// log has one line per pane rather than one per poll.
	Detect(Observation{Agent: "antigravity", CurrentCommand: "agy", PaneTitle: "one", Screen: screen})
	Detect(Observation{Agent: "antigravity", CurrentCommand: "agy", PaneTitle: "one", Screen: screen})
	Detect(Observation{Agent: "antigravity", CurrentCommand: "agy", PaneTitle: "two", Screen: screen})
	ShadowFlush()

	file, err := os.Open(filepath.Join(dir, ShadowLogName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	lines := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for scanner.Scan() {
		lines++
		var record ShadowRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("line %d is not a ShadowRecord: %v", lines, err)
		}
		if record.Explain == nil {
			continue
		}
		// Screen text reaches the log only through the engine's own region
		// previews, capped at 240 characters per evaluated rule.
		for _, rule := range record.Explain.EvaluatedRules {
			if runes := []rune(rule.Evidence.RegionPreview); len(runes) > 243 {
				t.Fatalf("rule %s carries a %d-character preview", rule.ID, len(runes))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != 2 {
		t.Fatalf("log has %d lines, want one per pane whose verdict changed", lines)
	}
}

// TestShadowLogRotatesOnceAtItsCap bounds shadow mode's footprint on disk. A
// week of shadow running is the point of the exercise, and an unbounded append
// is how a diagnostic becomes the problem it was meant to find.
func TestShadowLogRotatesOnceAtItsCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ShadowLogName)
	if err := os.WriteFile(path, make([]byte, shadowLogMaxBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	sink := NewShadowLog(dir)
	sink(ShadowRecord{Agent: "antigravity", Command: "agy"})

	rotated, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("the oversized log was not rotated: %v", err)
	}
	if rotated.Size() != int64(shadowLogMaxBytes) {
		t.Fatalf("rotation kept %d bytes, want the whole previous log", rotated.Size())
	}
	current, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if current.Size() == 0 || current.Size() > 4096 {
		t.Fatalf("the fresh log holds %d bytes, want just the new record", current.Size())
	}

	// A second rotation overwrites the one generation kept rather than
	// accumulating .2, .3, and so on.
	if err := os.WriteFile(path, make([]byte, shadowLogMaxBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	sink(ShadowRecord{Agent: "antigravity", Command: "agy"})
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("a second rotation generation was created: %v", err)
	}
}
