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

// TestFixtureCensus classifies every real fixture and prints the table: what
// each screen resolves to, and which rule id said so.
//
//	go test ./internal/agentactivity/ -run TestFixtureCensus -v
//
// Through Phase 2 this ran *two* classifiers — the Go rule tables beside the
// manifest engine — and a disagreement was the input to the next provider's
// cutover decision. All ten providers are cut over, so there is one lane left
// and nothing to compare it against inside this package; the differential
// harness (scripts/herdr-diff.sh) is what compares it against Herdr now.
//
// What is left is worth keeping for two reasons. It is the only place every
// fixture is classified, including the ones no expectation table names, so a
// fixture added without a test still shows up here. And it is a gate for the
// `state:` header a fixture declares: a fixture that says what it is and then
// reads as something else is either a broken rule or a lying fixture, and both
// are worth failing over.
func TestFixtureCensus(t *testing.T) {
	rows := censusRows(t)
	if len(rows) == 0 {
		t.Fatal("no fixtures found; the census is measuring nothing")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n%-12s %-38s %-9s %-40s %s\n",
		"AGENT", "FIXTURE", "STATE", "RULE", "DECLARED")
	declared := 0
	for _, row := range rows {
		note := row.declared
		switch {
		case note == "":
			note = "-"
		case row.declaredMatches():
			declared++
		default:
			note += " MISMATCH"
			t.Errorf("%s/%s declares state %q and classifies as %q via %s",
				row.agent, row.fixture, row.declared, row.state, row.rule)
		}
		fmt.Fprintf(&b, "%-12s %-38s %-9s %-40s %s\n",
			row.agent, row.fixture, row.state, row.rule, note)
	}
	fmt.Fprintf(&b, "\n%d fixtures, %d of them declaring the state they expect\n",
		len(rows), declared)
	t.Log(b.String())
}

type censusRow struct {
	agent, fixture string
	state, rule    string
	skip           bool
	// declared is the fixture header's own `state:` line, empty when it has
	// none. Its vocabulary is the fixture author's, not State's: "retain" means
	// a skip_state_update rule matched, and a value naming neither a state nor
	// "retain" (such as codex/exit.txt's "exited-outside-detector") is prose and
	// is not checked.
	declared string
}

func (r censusRow) declaredMatches() bool {
	switch r.declared {
	case "idle", "working", "blocked", "unknown":
		return r.state == r.declared
	case "retain":
		return r.skip
	default:
		return true
	}
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
			declaredState := ""
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
				case "state":
					declaredState = strings.TrimSpace(value)
				}
			}

			result, explain := DetectManifest(ob)
			rule := result.Evidence
			if explain != nil && explain.MatchedRule == nil {
				rule = "(" + explain.FallbackReason + ")"
			}
			rows = append(rows, censusRow{
				agent: agent, fixture: name,
				state: string(result.State), rule: rule,
				skip: result.SkipStateUpdate, declared: declaredState,
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
