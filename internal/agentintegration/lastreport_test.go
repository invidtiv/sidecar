package agentintegration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/agentlifecycle/lifecyclestore"
)

func TestTheLastReportIsWhatSeparatesInstalledFromWorking(t *testing.T) {
	// "The integration is installed" and "the integration is working" are
	// different claims, and telling them apart is most of why anyone opens this
	// surface. A status with no last report is an integration that has never
	// actually reported anything, which an installed-and-current status alone
	// cannot say.
	svc, env, _ := fixture(t)
	mustApply(t, svc, ActionInstall)

	if st := mustStatus(t, svc); st.LastReport != nil {
		t.Fatalf("an integration that has never run reported a last record: %+v", st.LastReport)
	}

	stateDir := t.TempDir()
	logPath := filepath.Join(stateDir, lifecyclestore.FileName)
	id := agentlifecycle.Identity{
		Host: "h", ServerIncarnation: "pid=1", PaneID: "%3",
		Provider: OpenCodeProvider, RunID: "r", ProcessGeneration: "pid=2",
	}
	records := []agentlifecycle.Report{
		{
			SchemaVersion: agentlifecycle.SchemaVersion, ID: "a", Kind: agentlifecycle.KindState,
			Source: OpenCodeSource, SourceVersion: "1", Sequence: 1,
			State: agentactivity.StateWorking, Reason: agentlifecycle.ReasonTurnStart,
			ObservedAt: time.Now().Add(-2 * time.Minute), Identity: id,
		},
		{
			SchemaVersion: agentlifecycle.SchemaVersion, ID: "b", Kind: agentlifecycle.KindEnd,
			Source: OpenCodeSource, SourceVersion: "1", Sequence: 2,
			Outcome: agentlifecycle.OutcomeCancelled, Reason: agentlifecycle.ReasonCancelled,
			ObservedAt: time.Now().Add(-1 * time.Minute), Identity: id,
		},
		{
			// Another source's record, which must not be attributed here.
			SchemaVersion: agentlifecycle.SchemaVersion, ID: "c", Kind: agentlifecycle.KindState,
			Source: "sidecar.codex.hooks", SourceVersion: "1", Sequence: 9,
			State: agentactivity.StateIdle, Reason: agentlifecycle.ReasonTurnComplete,
			ObservedAt: time.Now(),
			Identity: agentlifecycle.Identity{
				Host: "h", ServerIncarnation: "pid=1", PaneID: "%9",
				Provider: "codex", RunID: "r2", ProcessGeneration: "pid=3",
			},
		},
	}
	var lines []byte
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(append(lines, b...), '\n')
	}
	if err := os.WriteFile(logPath, lines, 0o644); err != nil {
		t.Fatal(err)
	}

	env.StateDir = stateDir
	withStore := Service{Env: env, Adapters: DefaultAdapters()}
	st := mustStatus(t, withStore)
	if st.LastReport == nil {
		t.Fatal("the last report was not attached")
	}
	if st.LastReport.Kind != agentlifecycle.KindEnd || st.LastReport.Sequence != 2 {
		t.Fatalf("the newest record is not the one reported: %+v", st.LastReport)
	}
	if st.LastReport.Outcome != agentlifecycle.OutcomeCancelled || st.LastReport.PaneID != "%3" {
		t.Fatalf("the summary lost the facts worth showing: %+v", st.LastReport)
	}
	if st.LastReport.Age == "" {
		t.Fatal("the summary has no rendered age")
	}

	// The summary is a summary. Identity beyond the pane — the salted session
	// fingerprint above all — answers no question asked here, and the standing
	// rule for this data is to carry the minimum that serves the purpose.
	encoded, err := json.Marshal(st.LastReport)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"runId", "sessionFingerprint", "processGeneration"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("the summary carries %s, which nothing here needs: %s", leaked, encoded)
		}
	}

	// An unreadable log is not an error. "There is no last report" and "the log
	// could not be read" mean the same thing to a surface that only reports it,
	// and a status command must not fail because a hook wrote a bad line.
	if err := os.WriteFile(logPath, []byte("{not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := mustStatus(t, withStore); st.LastReport != nil {
		t.Fatalf("a corrupt log produced a report: %+v", st.LastReport)
	}
}
