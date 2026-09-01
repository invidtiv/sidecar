package agentactivity

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestManifestCensus runs both screen-lane classifiers over every real fixture
// and prints the table. It is a *report*, not a gate: in this phase the Go rule
// tables still author every user-visible verdict, and a disagreement here is
// the input to Phase 2's per-provider cutover decision rather than a failure.
//
//	go test ./internal/agentactivity/ -run TestManifestCensus -v
//
// It fails only when a fixture cannot be read or classified at all, because
// that means the census itself is not measuring what it claims to.
func TestManifestCensus(t *testing.T) {
	rows := censusRows(t)
	if len(rows) == 0 {
		t.Fatal("no fixtures found; the census is measuring nothing")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n%-12s %-36s %-9s %-38s %-9s %-30s %s\n",
		"AGENT", "FIXTURE", "GO", "GO EVIDENCE", "MANIFEST", "MANIFEST RULE", "AGREE")
	agreed := 0
	for _, row := range rows {
		agree := "yes"
		if !row.agrees() {
			agree = "NO"
		} else {
			agreed++
		}
		fmt.Fprintf(&b, "%-12s %-36s %-9s %-38s %-9s %-30s %s\n",
			row.agent, row.fixture, row.goState, row.goEvidence,
			row.manifestState, row.manifestRule, agree)
	}
	fmt.Fprintf(&b, "\n%d fixtures, %d agree, %d disagree\n", len(rows), agreed, len(rows)-agreed)
	t.Log(b.String())
}

type censusRow struct {
	agent, fixture             string
	goState, goEvidence        string
	manifestState              string
	manifestRule               string
	goSkip, manifestSkip       bool
	goFallback, manifestFallbk bool
}

func (r censusRow) agrees() bool {
	return r.goState == r.manifestState && r.goSkip == r.manifestSkip
}

func censusRows(t *testing.T) []censusRow {
	t.Helper()
	agents, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var rows []censusRow
	for _, dir := range agents {
		// proof/ holds run transcripts, not captures.
		if !dir.IsDir() || dir.Name() == "proof" {
			continue
		}
		agent := dir.Name()
		files, err := os.ReadDir(filepath.Join("testdata", agent))
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(files))
		for _, file := range files {
			names = append(names, file.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			if filepath.Ext(name) != ".txt" {
				continue
			}
			// process_identity.txt and availability.txt record environment
			// facts, not screens.
			if name == "process_identity.txt" || name == "availability.txt" {
				continue
			}
			data, err := os.ReadFile(filepath.Join("testdata", agent, name))
			if err != nil {
				t.Fatal(err)
			}
			head, screen, found := strings.Cut(string(data), "screen:\n")
			if !found {
				continue
			}
			ob := Observation{Agent: agent, Screen: screen}
			for _, line := range strings.Split(head, "\n") {
				key, value, ok := strings.Cut(line, ": ")
				if !ok {
					continue
				}
				switch strings.TrimSpace(key) {
				case "pane_title":
					ob.PaneTitle = value
				case "pane_current_command":
					ob.CurrentCommand = value
				case "pane_height":
					ob.PaneHeight, _ = strconv.Atoi(strings.TrimSpace(value))
				}
			}

			goResult := Detect(ob)
			manifestResult, explain := DetectManifest(ob)
			rule := ""
			if explain != nil && explain.MatchedRule != nil {
				rule = explain.MatchedRule.ID
			} else if explain != nil {
				rule = "(" + explain.FallbackReason + ")"
			} else {
				rule = "(no manifest evaluated: " + manifestResult.Evidence + ")"
			}
			rows = append(rows, censusRow{
				agent: agent, fixture: name,
				goState: string(goResult.State), goEvidence: goResult.Evidence,
				manifestState: string(manifestResult.State), manifestRule: rule,
				goSkip: goResult.SkipStateUpdate, manifestSkip: manifestResult.SkipStateUpdate,
				goFallback: goResult.FallbackIdle, manifestFallbk: manifestResult.FallbackIdle,
			})
		}
	}
	return rows
}

// TestEveryClaimedProviderHasAVendoredManifest is the census's precondition: a
// provider Sidecar claims but has no manifest for would silently produce a
// column of "no manifest evaluated" rows instead of a disagreement.
func TestEveryClaimedProviderHasAVendoredManifest(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "grok", "antigravity", "pi", "copilot", "cursor", "opencode", "amp", "muse"} {
		ob := Observation{Agent: agent, Screen: "", CurrentCommand: agent}
		if agent == "copilot" {
			ob.CurrentCommand = "copilot"
		}
		_, explain := DetectManifest(ob)
		if explain == nil {
			t.Fatalf("%s: no manifest evaluated; ManifestAgentID(%q) = %q", agent, agent, ManifestAgentID(agent))
		}
	}
}
