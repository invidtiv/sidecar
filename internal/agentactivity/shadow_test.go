package agentactivity

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func captureShadow(t *testing.T) *[]ShadowRecord {
	t.Helper()
	var records []ShadowRecord
	SetShadowSink(func(record ShadowRecord) { records = append(records, record) })
	t.Cleanup(func() { SetShadowSink(nil) })
	return &records
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
	records := captureShadow(t)
	// Antigravity's upstream manifest has no rule for "Generating..." or the
	// "esc to cancel" footer, so the manifest lane falls back to idle while the
	// Go table says working. That is exactly the class of disagreement Phase 2
	// has to decide, and exactly what shadow mode is for.
	ob := Observation{Agent: "antigravity", CurrentCommand: "agy", Screen: "Generating...\nesc to cancel · Gemini 3.6 Flash · high\n"}
	result := Detect(ob)
	if result.State != StateWorking {
		t.Fatalf("Detect() = %+v; shadow mode must not change the returned verdict", result)
	}
	if len(*records) != 1 {
		t.Fatalf("recorded %d disagreements, want 1", len(*records))
	}
	record := (*records)[0]
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
	records := captureShadow(t)
	// Both lanes call this blocked; they name the finding `codex.title.blocked`
	// and `osc_title_blocked`. Logging every such pair would bury the
	// disagreements that matter under one line per matched rule per poll.
	ob := Observation{Agent: "codex", CurrentCommand: "codex", PaneTitle: "[ ! ] Action Required | project"}
	if got := Detect(ob); got.State != StateBlocked {
		t.Fatalf("Detect() = %+v", got)
	}
	if len(*records) != 0 {
		t.Fatalf("recorded %d disagreements for a same-state pair: %+v", len(*records), *records)
	}
}

func TestShadowLogAppendsOneJSONLineAndCarriesNoScreenText(t *testing.T) {
	dir := t.TempDir()
	SetShadowSink(NewShadowLog(dir))
	t.Cleanup(func() { SetShadowSink(nil) })

	Detect(Observation{Agent: "antigravity", CurrentCommand: "agy", Screen: "Generating...\nesc to cancel\n"})
	Detect(Observation{Agent: "antigravity", CurrentCommand: "agy", Screen: "Generating...\nesc to cancel\n"})

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
		// The only screen text a record may carry is the engine's own capped
		// region previews, so a user can attach the log to an issue without
		// reading every line first.
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
		t.Fatalf("log has %d lines, want one per disagreement", lines)
	}
}
