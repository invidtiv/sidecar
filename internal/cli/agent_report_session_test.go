package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/shellstate"
)

// TestReportSessionNoOpsOutsideAManagedShell is the fail-open rule. A provider
// hook fires wherever the provider runs, and a user who has never opened
// Sidecar must not see its complaints in their own agent terminal.
func TestReportSessionNoOpsOutsideAManagedShell(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")

	code, out, errOut := runLifecycleCLI(t, "agent", "report-session", "--kind", "codex", "--id", "019f2c8a")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	if out != "" || errOut != "" {
		t.Fatalf("a no-op was not silent: stdout=%q stderr=%q", out, errOut)
	}
}

func TestReportSessionNoOpIsStillStructuredForJSONCallers(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")

	code, out, errOut := runLifecycleCLI(t, "agent", "report-session", "--kind", "codex", "--id", "019f2c8a", "--json")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr not empty with --json: %q", errOut)
	}
	var res struct {
		SchemaVersion int    `json:"schemaVersion"`
		Managed       bool   `json:"managed"`
		Bound         bool   `json:"bound"`
		Note          string `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout is not one JSON document: %q (%v)", out, err)
	}
	if res.Managed || res.Bound {
		t.Fatalf("a no-op reported itself as managed/bound: %+v", res)
	}
	if res.Note == "" {
		t.Fatal("the no-op carried no explanation")
	}
	if res.SchemaVersion != reportSessionSchemaVersion {
		t.Fatalf("schemaVersion = %d", res.SchemaVersion)
	}
}

// TestReportSessionUsageErrorsComeBeforeAnyContextWork pins the same ordering
// the rest of the agent family uses: a mistyped command line is exit 2, and it
// is answered before anything looks at tmux or the manifest.
func TestReportSessionUsageErrorsComeBeforeAnyContextWork(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no kind", []string{"agent", "report-session", "--id", "x"}, "--kind is required"},
		{"no way of naming a conversation", []string{"agent", "report-session", "--kind", "codex"},
			"one of --id, --path, --clear, or --hook-stdin is required"},
		{"id and path together", []string{"agent", "report-session", "--kind", "codex", "--id", "x", "--path", "/tmp/a"},
			"mutually exclusive"},
		{"id and clear together", []string{"agent", "report-session", "--kind", "codex", "--id", "x", "--clear"},
			"mutually exclusive"},
		{"clear and hook-stdin together", []string{"agent", "report-session", "--kind", "codex", "--clear", "--hook-stdin"},
			"mutually exclusive"},
		{"a flag with no value", []string{"agent", "report-session", "--kind"}, "--kind requires a value"},
		{"an unknown flag", []string{"agent", "report-session", "--kind", "codex", "--id", "x", "--nope"}, "unknown argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runLifecycleCLI(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit %d, wanted 2 (stdout=%q stderr=%q)", code, out, errOut)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Fatalf("stderr = %q, wanted it to mention %q", errOut, tc.want)
			}
		})
	}
}

// TestAProviderWithNoOfficialIntegrationSaysSo names the cause rather than the
// symptom. Falling through to the validator produced "the source is empty",
// which describes what the defaulting failed to fill in instead of why.
func TestAProviderWithNoOfficialIntegrationSaysSo(t *testing.T) {
	for _, kind := range []string{"codex", "claude", "opencode", "pi"} {
		if agentsession.OfficialSourceFor(kind) == "" {
			t.Fatalf("%s is expected to have an official source", kind)
		}
	}
	// grok is a real catalog family with no Sidecar integration, so the
	// defaulting genuinely has nothing to fill in.
	if agentsession.OfficialSourceFor("grok") != "" {
		t.Skip("grok gained an official integration; pick another provider for this case")
	}
}

// TestReportSessionIsNotInTheHappyPathList keeps the plan's decision that this
// is an integration surface: discoverable in help, absent from the short
// `sidecar agents` list an agent reads to learn the coordination sequence.
func TestReportSessionIsNotInTheHappyPathList(t *testing.T) {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("report-session")
	if cmd == nil {
		t.Fatal("report-session is not registered")
	}
	if cmd.Agent.Invocation != "" || cmd.Agent.Summary != "" {
		t.Fatal("report-session carries AgentDoc metadata, which would put it in the agents happy-path list")
	}
	if !cmd.Mutates {
		t.Fatal("report-session writes a binding and must be marked as mutating")
	}
	// The help has to state the trust rules and the current-shell requirement,
	// because that is the only place a hook author reads them.
	for _, want := range []string{"official Sidecar integration", "managed shell", "occupies the pane"} {
		if !strings.Contains(cmd.Long, want) {
			t.Fatalf("help does not mention %q", want)
		}
	}
}

func TestHookPayloadReaderIsBoundedAndStrict(t *testing.T) {
	t.Run("a codex session-start payload", func(t *testing.T) {
		p, err := readHookPayload(strings.NewReader(
			`{"session_id":"019f2c8a","transcript_path":"/home/u/.codex/sessions/a.jsonl","hook_event_name":"SessionStart","source":"startup"}`))
		if err != nil {
			t.Fatal(err)
		}
		if p.SessionID != "019f2c8a" || p.HookEventName != "SessionStart" {
			t.Fatalf("payload = %+v", p)
		}
	})

	t.Run("a sub-agent payload is recognisable", func(t *testing.T) {
		p, err := readHookPayload(strings.NewReader(`{"session_id":"x","agent_id":"sub-1"}`))
		if err != nil {
			t.Fatal(err)
		}
		if p.AgentID == "" {
			t.Fatal("the sub-agent marker was not decoded, so a nested conversation could bind the pane")
		}
	})

	t.Run("fields Sidecar does not name cannot reach a record", func(t *testing.T) {
		// The struct decodes four fields. Everything else in a provider payload
		// -- prompt text, tool arguments, model names -- is dropped by the
		// decoder rather than filtered later.
		p, err := readHookPayload(strings.NewReader(
			`{"session_id":"x","prompt":"secret prompt text","tool_input":{"password":"hunter2"},"cwd":"/repo"}`))
		if err != nil {
			t.Fatal(err)
		}
		if p.SessionID != "x" {
			t.Fatalf("payload = %+v", p)
		}
		blob, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, leaked := range []string{"secret prompt text", "hunter2", "/repo"} {
			if strings.Contains(string(blob), leaked) {
				t.Fatalf("the decoded payload carried %q: %s", leaked, blob)
			}
		}
	})

	t.Run("refusals", func(t *testing.T) {
		cases := []struct {
			name, body, want string
		}{
			{"empty", "", "empty"},
			{"whitespace only", "   \n", "empty"},
			{"not JSON", "session_id=x", "not valid JSON"},
			{"a JSON array", "[1,2,3]", "not valid JSON"},
			{"over the cap", `{"session_id":"` + strings.Repeat("a", maxHookStdinBytes) + `"}`, "cap"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := readHookPayload(strings.NewReader(tc.body))
				if err == nil {
					t.Fatalf("%s was accepted", tc.name)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, wanted it to mention %q", err, tc.want)
				}
			})
		}
	})

	t.Run("a nil reader is a refusal, not a panic", func(t *testing.T) {
		if _, err := readHookPayload(nil); err == nil {
			t.Fatal("a nil stdin was accepted")
		}
	})
}

// TestReportSessionErrorCodesMapToTheRefusalTheyDescribe keeps the JSON error
// vocabulary tied to the validator's own error values rather than to message
// text, so an integration can branch on the code.
func TestReportSessionErrorCodesMapToTheRefusalTheyDescribe(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{agentsession.ErrStaleGeneration, "stale_generation"},
		{agentsession.ErrUntrustedSource, "untrusted_source"},
		{agentsession.ErrOutsideStoreRoot, "outside_store_root"},
		{agentsession.ErrUnsupportedKind, "unsupported_kind"},
		{agentsession.ErrInvalidRef, "invalid_reference"},
		{errors.New("disk on fire"), "store_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := reportSessionCode(tc.err); got != tc.want {
				t.Fatalf("reportSessionCode(%v) = %q, wanted %q", tc.err, got, tc.want)
			}
			// Wrapped errors must map the same way; the CLI never sees a bare
			// sentinel in practice.
			wrapped := errors.Join(errors.New("while binding"), tc.err)
			if got := reportSessionCode(wrapped); got != tc.want {
				t.Fatalf("a wrapped %v mapped to %q, wanted %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestOnlyStoreFailuresExitOne separates "the machine broke" from "the report
// lost", which is the difference between a bug worth investigating and normal
// operation.
func TestOnlyStoreFailuresExitOne(t *testing.T) {
	var out, errOut strings.Builder
	env := Env{Stdout: &out, Stderr: &errOut}

	if code := emitReportSessionError(env, true, "store_failed", errors.New("boom")); code != 1 {
		t.Fatalf("a store failure exited %d, wanted 1", code)
	}
	for _, code := range []string{"stale_generation", "invalid_reference", "untrusted_source", "outside_store_root"} {
		if got := emitReportSessionError(env, true, code, errors.New("x")); got != exitInputRejected {
			t.Fatalf("%s exited %d, wanted %d", code, got, exitInputRejected)
		}
	}
}

// TestTheJSONResultNeverCarriesTheConversationValue is the redaction rule at
// the reporting surface. The hook already knows what it reported; anything
// capturing its output should not learn it too.
func TestTheJSONResultNeverCarriesTheConversationValue(t *testing.T) {
	blob, err := json.Marshal(reportSessionResult{
		SchemaVersion: reportSessionSchemaVersion,
		Managed:       true,
		Decision:      agentsession.DecisionRecorded,
		Kind:          "codex",
		RefKind:       agentsession.RefID,
		Reported:      true,
		Bound:         true,
		Shell:         "sidecar-sh-p-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), `"value"`) {
		t.Fatalf("the result contract has a value field: %s", blob)
	}
	for _, want := range []string{`"refKind":"id"`, `"reported":true`, `"bound":true`} {
		if !strings.Contains(string(blob), want) {
			t.Fatalf("the result lost %s: %s", want, blob)
		}
	}
}
