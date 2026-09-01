package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestLayoutPaneFlag_AcceptsTheDocumentedShapes(t *testing.T) {
	for name, raw := range map[string]string{
		"file with two targets": `{"kind":"file","targets":["a.go:12","b.md"]}`,
		"issue":                 `{"kind":"issue","targets":["td-756c34"]}`,
		"diff working tree":     `{"kind":"diff"}`,
		"resource":              `{"kind":"resource","provider":"jira-work","targets":["CASH-1245"]}`,
		"shell":                 `{"kind":"shell","run":"make dev","name":"dev server"}`,
		"file at a cell":        `{"kind":"file","targets":["a.go"],"at":"2.1"}`,
	} {
		pane, code, msg := layoutPaneFlag(raw)
		if code != 0 {
			t.Errorf("%s: exit %d msg %q", name, code, msg)
			continue
		}
		if pane.Kind == "" {
			t.Errorf("%s: kind lost", name)
		}
	}
}

func TestLayoutPaneFlag_Refuses(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want string
	}{
		"not json":      {`{"kind":"file"`, "not valid JSON"},
		"unknown kind":  {`{"kind":"browser","targets":["x"]}`, "not one of"},
		"primary":       {`{"kind":"primary"}`, "cannot be opened"},
		"bad cell":      {`{"kind":"file","targets":["a.go"],"at":"2.x"}`, "grid cell"},
		"cell zero":     {`{"kind":"file","targets":["a.go"],"at":"0.1"}`, "grid cell"},
		"shell targets": {`{"kind":"shell","targets":["a.go"]}`, "run/type/name"},
		"no provider":   {`{"kind":"resource","targets":["CASH-1245"]}`, "provider"},
		"no target":     {`{"kind":"file"}`, "needs at least one target"},
		"empty kind":    {`{"targets":["a.go"]}`, "not one of"},
		// Carrying a live terminal by session is --spec grammar. A batch closes
		// nothing, so there is no leaf to carry: accepting the field silently
		// would open a SECOND shell where the caller asked for none.
		"shell session": {`{"kind":"shell","session":"sidecar-tp-x"}`, "session"},
	} {
		_, code, msg := layoutPaneFlag(tc.raw)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (msg %q)", name, code, msg)
			continue
		}
		if !strings.Contains(msg, tc.want) {
			t.Errorf("%s: msg %q does not mention %q", name, msg, tc.want)
		}
	}
}

func TestLayoutCellParsing(t *testing.T) {
	for raw, want := range map[string]panelayout.Cell{
		"2.1": {Col: 2, Row: 1},
		"3":   {Col: 3, Row: 1},
		"4.4": {Col: 4, Row: 4},
	} {
		got, ok := panelayout.ParseCell(raw)
		if !ok || got != want {
			t.Errorf("ParseCell(%q) = %+v %v, want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "0.1", "2.0", "a.b", "2.", ".1", "-1.2", "99999999999999999999"} {
		if got, ok := panelayout.ParseCell(raw); ok {
			t.Errorf("ParseCell(%q) accepted as %+v", raw, got)
		}
	}
}

func TestLayoutCommandsDeclareAgentDoc(t *testing.T) {
	layout := RootCommand().FindSubcommand("layout")
	if layout == nil {
		t.Fatal("no layout command registered")
	}
	for _, name := range []string{"get", "apply"} {
		sub := layout.FindSubcommand(name)
		if sub == nil {
			t.Fatalf("no layout %s subcommand", name)
		}
		if sub.Agent.Invocation == "" || sub.Agent.Summary == "" {
			t.Errorf("layout %s missing AgentDoc: %+v", name, sub.Agent)
		}
	}
}

// A get request carries no panes and an apply carries only descriptors; the
// payload the CLI writes is exactly the documented shape.
func TestLayoutPayloadWireShape(t *testing.T) {
	payload, err := json.Marshal(uirequest.LayoutPayload{
		Mode: uirequest.LayoutModeApply,
		Panes: []uirequest.LayoutPane{
			{Kind: "file", Targets: []string{"a.go"}, At: "2.1"},
			{Kind: "shell", Run: "make dev"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"mode":"apply"`, `"panes":[`, `"at":"2.1"`, `"run":"make dev"`} {
		if !strings.Contains(string(payload), want) {
			t.Errorf("payload missing %s: %s", want, payload)
		}
	}
}

func TestLayoutSpecFlag_AcceptsTheDocumentedGrammar(t *testing.T) {
	for name, raw := range map[string]string{
		"primary alone":       `{"columns":[{"panes":[{"kind":"primary"}]}]}`,
		"primary beside file": `{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"file","targets":["a.go:12"]}]}]}`,
		"stacked column":      `{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"issue","targets":["td-756c34"]},{"kind":"diff"}]}]}`,
		"carried shell":       `{"columns":[{"panes":[{"kind":"primary"},{"kind":"shell","session":"sidecar-sh-x-1"}]}]}`,
		"new shell":           `{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"shell","run":"make dev","name":"dev"}]}]}`,
		"resource":            `{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"resource","provider":"jira","targets":["CASH-1"]}]}]}`,
	} {
		if _, code, msg := layoutSpecFlag(raw); code != 0 {
			t.Errorf("%s: exit %d msg %q", name, code, msg)
		}
	}
}

func TestLayoutSpecFlag_Refuses(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want string
	}{
		"not json":        {`{"columns":`, "not a valid layout"},
		"no columns":      {`{}`, "at least one column"},
		"five columns":    {`{"columns":[{},{},{},{},{}]}`, "cap is 4"},
		"empty column":    {`{"columns":[{"panes":[]}]}`, "carries no panes"},
		"five rows":       {`{"columns":[{"panes":[{"kind":"primary"},{},{},{},{"kind":"diff"}]}]}`, "cap is 4"},
		"unknown kind":    {`{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"browser"}]}]}`, "unknown pane kind"},
		"no primary":      {`{"columns":[{"panes":[{"kind":"file","targets":["a.go"]}]}]}`, "exactly one"},
		"two primaries":   {`{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"primary"}]}]}`, "found 2"},
		"at in spec":      {`{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"file","targets":["a.go"],"at":"2.1"}]}]}`, "positions panes"},
		"primary fields":  {`{"columns":[{"panes":[{"kind":"primary","session":"x"}]}]}`, "takes no other fields"},
		"carry with run":  {`{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"shell","session":"s","run":"x"}]}]}`, "takes only"},
		"shell targets":   {`{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"shell","targets":["a.go"]}]}]}`, "not targets"},
		"resource no pro": {`{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"resource","targets":["CASH-1"]}]}]}`, "provider"},
		"file no target":  {`{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"file"}]}]}`, "needs at least one target"},
	} {
		_, code, msg := layoutSpecFlag(tc.raw)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (msg %q)", name, code, msg)
			continue
		}
		if !strings.Contains(msg, tc.want) {
			t.Errorf("%s: msg %q does not mention %q", name, msg, tc.want)
		}
	}
}

// --spec and --pane are different modes; passing both is a usage error before
// any request is written.
func TestLayoutApply_SpecAndPaneAreMutuallyExclusive(t *testing.T) {
	spec := `{"columns":[{"panes":[{"kind":"primary"}]}]}`
	for _, args := range [][]string{
		{"layout", "apply", "--spec", spec, "--pane", `{"kind":"file","targets":["a.go"]}`},
		{"layout", "apply", "--pane", `{"kind":"file","targets":["a.go"]}`, "--spec", spec},
		{"layout", "apply", "--spec", spec, "--spec", spec},
		{"layout", "apply"},
	} {
		var out, errOut bytes.Buffer
		handled, code := Run(args, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("Run(%v) = handled %v code %d, want usage error 2", args, handled, code)
		}
		if combined := out.String() + errOut.String(); !strings.Contains(combined, "--spec") {
			t.Fatalf("Run(%v) output %q does not explain the modes", args, combined)
		}
	}
}

// The spec rides to the host verbatim in the payload's columns field.
func TestLayoutApply_SpecRidesInColumnsField(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "active task")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	workDir := t.TempDir()
	projectDir := filepath.Join(stateHome, "sidecar", "projects", "sidecar")
	if err := os.WriteFile(filepath.Join(projectDir, "meta.json"), []byte(`{"path":`+quoteJSON(t, workDir)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}

	type captured struct {
		payload uirequest.LayoutPayload
		err     error
	}
	capture := make(chan captured, 1)
	go func() {
		reqsDir := filepath.Join(stateHome, "sidecar", "requests")
		for i := 0; i < 400; i++ {
			time.Sleep(25 * time.Millisecond)
			entries, err := os.ReadDir(reqsDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".json") || strings.Contains(e.Name(), ".tmp.") {
					continue
				}
				req, err := uirequest.ReadRequest(filepath.Join(reqsDir, e.Name()))
				if err != nil || req.Action != uirequest.ActionLayout {
					continue
				}
				payload, err := uirequest.DecodeLayoutPayload(req.Payload)
				capture <- captured{payload: payload, err: err}
				_ = uirequest.WriteAck(filepath.Join(stateHome, "sidecar"), req.ID, req.Action, uirequest.Ack{
					Instance: "test-instance", Status: uirequest.StatusOpened, At: time.Now().UTC(),
				})
				return
			}
		}
		close(capture)
	}()

	spec := `{"columns":[{"panes":[{"kind":"primary"}]}]}`
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"layout", "apply", "--spec", spec, "--wait", "10s"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("apply = handled %v code %d stderr %q", handled, code, errOut.String())
	}

	got, ok := <-capture
	if !ok {
		t.Fatal("no layout request was written")
	}
	if got.err != nil {
		t.Fatalf("written payload does not decode: %v", got.err)
	}
	var compact bytes.Buffer
	_ = json.Compact(&compact, got.payload.Columns)
	if len(got.payload.Panes) != 0 || compact.String() != `[{"panes":[{"kind":"primary"}]}]` {
		t.Fatalf("payload = panes %v columns %s, want the spec's columns verbatim", got.payload.Panes, got.payload.Columns)
	}
	columns, err := uirequest.DecodeLayoutColumns(got.payload.Columns)
	if err != nil {
		t.Fatal(err)
	}
	if err := uirequest.ValidateLayoutSpec(uirequest.LayoutSpec{Columns: columns}); err != nil {
		t.Fatalf("host-side grammar rejects what the CLI accepted: %v", err)
	}
	if strings.Contains(out.String(), `"action"`) {
		t.Errorf("apply without --json wrote its structured result to stdout:\n%s", out.String())
	}
}

// --json is what asks for the structured object, on apply exactly as on open.
// The two projections are alternatives: without it a human gets human lines and
// nothing else, with it stdout is the object and nothing else.
func TestLayoutApply_StructuredResultIsGatedOnJSON(t *testing.T) {
	acks := []uirequest.Ack{{
		Instance: "test-instance",
		Status:   uirequest.StatusOpened,
		Items: []uirequest.AckItem{
			{Index: 0, Verdict: uirequest.ItemVerdictCarried, Cell: "1.1"},
			{Index: 1, Verdict: uirequest.ItemVerdictOpened, Cell: "2.1"},
			{Index: 2, Verdict: uirequest.ItemVerdictDeclined, Reason: "no room"},
		},
	}}
	dest := openDestination{DisplayName: "active task", Resolved: uirequest.ResolvedCurrentShell}

	var plain bytes.Buffer
	printLayoutResult(Env{Stdout: &plain}, uirequest.LayoutModeApply, dest, acks, false)
	if strings.Contains(plain.String(), `"action"`) {
		t.Errorf("plain apply leaked the structured object:\n%s", plain.String())
	}
	for _, want := range []string{"carried the live pane already at 1.1", "opened pane at 2.1", "declined index 2: no room"} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("plain apply missing %q:\n%s", want, plain.String())
		}
	}

	var structured bytes.Buffer
	printLayoutResult(Env{Stdout: &structured}, uirequest.LayoutModeApply, dest, acks, true)
	first, _, _ := strings.Cut(structured.String(), "\n")
	var result struct {
		Action string              `json:"action"`
		Mode   string              `json:"mode"`
		Items  []uirequest.AckItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(first), &result); err != nil {
		t.Fatalf("--json first line does not decode: %v\n%s", err, structured.String())
	}
	if result.Action != "layout" || result.Mode != uirequest.LayoutModeApply || len(result.Items) != 3 {
		t.Errorf("--json result = %+v, want the layout apply items", result)
	}
	if result.Items[0].Verdict != uirequest.ItemVerdictCarried {
		t.Errorf("carried verdict did not survive the wire: %+v", result.Items[0])
	}
}

func TestLayoutGetHelpMentionsSessions(t *testing.T) {
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"layout", "get", "--help"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("help = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "--sessions") {
		t.Fatalf("layout get help missing --sessions:\n%s", got)
	}
	if !strings.Contains(got, "[ROW]") {
		t.Fatalf("layout get help missing optional ROW:\n%s", got)
	}
}

func TestLayoutGetFastRefusesMissingViewerPresence(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	writeProjectShell(t, stateDir, "sidecar", shellstate.Definition{
		TmuxName: "sidecar-sh-sidecar-1", DisplayName: "active task", Namespace: "/tmp/sock", WorkDir: workDir,
	})
	original := sessionLeaseOwner
	t.Cleanup(func() { sessionLeaseOwner = original })
	sessionLeaseOwner = func(string) string { return "laptop-99" }

	started := time.Now()
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"layout", "get", "--shell", "active task", "--wait", "5s", "--json"}, &out, &errOut)
	elapsed := time.Since(started)
	if !handled || code != 4 {
		t.Fatalf("missing presence = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if elapsed > time.Second {
		t.Fatalf("fast-refuse waited %s", elapsed)
	}
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "laptop-99") || !strings.Contains(combined, "cannot receive pane requests") {
		t.Fatalf("refusal = %q", combined)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "requests")); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(filepath.Join(stateDir, "requests"))
		if len(entries) != 0 {
			t.Fatalf("fast-refuse still wrote a request: %v", entries)
		}
	}
}

func TestLayoutSessionsFlagExclusiveWithShellAndProject(t *testing.T) {
	for _, args := range [][]string{
		{"layout", "get", "--sessions", "--shell", "x"},
		{"layout", "get", "--sessions", "row", "--project", "sidecar"},
		{"layout", "get", "--sessions=row", "--shell", "x"},
		{"layout", "apply", "--sessions", "--project", "sidecar", "--pane", `{"kind":"diff"}`},
	} {
		var out, errOut bytes.Buffer
		handled, code := Run(args, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("Run(%v) = handled %v code %d, want usage 2", args, handled, code)
		}
		combined := out.String() + errOut.String()
		if !strings.Contains(combined, "--sessions") {
			t.Fatalf("Run(%v) output %q does not mention --sessions", args, combined)
		}
		if !strings.Contains(combined, "--shell") && !strings.Contains(combined, "--project") {
			t.Fatalf("Run(%v) output %q does not mention --shell/--project", args, combined)
		}
	}
}

func TestLayoutSessionsParsesBareEqualsAndValue(t *testing.T) {
	cases := []struct {
		name string
		args []string
		row  string
	}{
		{"bare", []string{"layout", "get", "--sessions", "--wait", "0"}, ""},
		{"value", []string{"layout", "get", "--sessions", "sidecar:shell:sidecar-sh-sidecar-1", "--wait", "0"}, "sidecar:shell:sidecar-sh-sidecar-1"},
		{"equals", []string{"layout", "get", "--sessions=sidecar:shell:sidecar-sh-sidecar-1", "--wait", "0"}, "sidecar:shell:sidecar-sh-sidecar-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateHome, stateDir := setupIsolatedCLI(t)
			workDir := t.TempDir()
			writeProjectMeta(t, stateDir, "sidecar", workDir)
			writeProjectShell(t, stateDir, "sidecar", shellstate.Definition{
				TmuxName: "sidecar-sh-sidecar-1", DisplayName: "active task",
			})
			if err := uirequest.Announce(stateDir, uirequest.Instance{
				PID: os.Getpid(), ProjectKey: "sidecar", Project: "sidecar", WorkDir: workDir,
			}); err != nil {
				t.Fatal(err)
			}

			capture := make(chan uirequest.Request, 1)
			go func() {
				reqsDir := filepath.Join(stateHome, "sidecar", "requests")
				for i := 0; i < 400; i++ {
					time.Sleep(25 * time.Millisecond)
					entries, err := os.ReadDir(reqsDir)
					if err != nil {
						continue
					}
					for _, e := range entries {
						if !strings.HasSuffix(e.Name(), ".json") || strings.Contains(e.Name(), ".tmp.") {
							continue
						}
						req, err := uirequest.ReadRequest(filepath.Join(reqsDir, e.Name()))
						if err != nil || req.Action != uirequest.ActionLayout {
							continue
						}
						capture <- req
						_ = uirequest.WriteAck(filepath.Join(stateHome, "sidecar"), req.ID, req.Action, uirequest.Ack{
							Instance: "test-instance", Status: uirequest.StatusOpened, At: time.Now().UTC(),
							Layout: json.RawMessage(`{"version":1,"grid":null,"caps":{"maxColumns":4,"maxRows":4,"liveLeaves":2},"floors":{}}`),
						})
						return
					}
				}
				close(capture)
			}()

			var out, errOut bytes.Buffer
			args := append([]string{}, tc.args...)
			// Give the capture loop time to see the request.
			for i, a := range args {
				if a == "0" && i > 0 && args[i-1] == "--wait" {
					args[i] = "10s"
				}
			}
			handled, code := Run(args, &out, &errOut)
			if !handled || code != 0 {
				t.Fatalf("Run(%v) = handled %v code %d stderr %q", args, handled, code, errOut.String())
			}
			req, ok := <-capture
			if !ok {
				t.Fatal("no layout request was written")
			}
			if !req.Origin.Sessions {
				t.Fatalf("Origin.Sessions = false, want true: %+v", req.Origin)
			}
			if req.Origin.TmuxSession != "" {
				t.Fatalf("TmuxSession = %q, want empty so the project surface ignores this", req.Origin.TmuxSession)
			}
			if tc.row == "" {
				if req.Origin.SessionsRow != "" {
					t.Fatalf("SessionsRow = %q, want empty for the selected row", req.Origin.SessionsRow)
				}
			} else if !strings.Contains(req.Origin.SessionsRow, "sidecar-sh-sidecar-1") {
				t.Fatalf("SessionsRow = %q, want a durable id for sidecar-sh-sidecar-1", req.Origin.SessionsRow)
			}
			if req.Origin.SessionsRow == "" && req.Origin.ProjectKey != "sidecar" {
				t.Fatalf("ProjectKey = %q, want the instance's project", req.Origin.ProjectKey)
			}
		})
	}
}

func TestLayoutSessionsRowAmbiguityMatchesShell(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	a := t.TempDir()
	b := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", a)
	writeProjectMeta(t, stateDir, "braid", b)
	writeProjectShell(t, stateDir, "sidecar", shellstate.Definition{
		TmuxName: "sidecar-sh-sidecar-1", DisplayName: "dev",
	})
	writeProjectShell(t, stateDir, "braid", shellstate.Definition{
		TmuxName: "sidecar-sh-braid-1", DisplayName: "dev",
	})

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"layout", "get", "--sessions", "dev", "--wait", "0"}, &out, &errOut)
	if !handled || code != 3 {
		t.Fatalf("ambiguous row = handled %v code %d, want 3 (stderr %q)", handled, code, errOut.String())
	}
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "more than one") || !strings.Contains(combined, "durable id") {
		t.Fatalf("ambiguity missing choices: %q", combined)
	}
	if !strings.Contains(combined, ":shell:sidecar-sh-sidecar-1") || !strings.Contains(combined, ":shell:sidecar-sh-braid-1") {
		t.Fatalf("ambiguity missing durable ids: %q", combined)
	}
}

func TestLayoutSessionsUnknownRowIsUsage(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	writeProjectMeta(t, stateDir, "sidecar", t.TempDir())
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"layout", "get", "--sessions", "no-such-row", "--wait", "0"}, &out, &errOut)
	if !handled || code != 2 {
		t.Fatalf("unknown row = handled %v code %d, want 2 (stderr %q)", handled, code, errOut.String())
	}
	if combined := out.String() + errOut.String(); !strings.Contains(combined, "unknown Sessions row") {
		t.Fatalf("unknown row message = %q", combined)
	}
}

// The main checkout is a Sessions catalog row even when Sidecar has registered
// no extra worktrees. layout get prints canonical(root):worktree:canonical(root)
// as surface; apply --sessions $surface must not die as "unknown Sessions row".
func TestLayoutSessionsMainCheckoutWorktreeID(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	canon := canonicalOpenPath(workDir)
	ids := []string{
		canon + ":worktree:" + canon,
		workDir + ":worktree:" + workDir,
		"sidecar:worktree:" + canon,
	}
	for _, id := range ids {
		var out, errOut bytes.Buffer
		handled, code := Run([]string{"layout", "get", "--sessions", id, "--wait", "0"}, &out, &errOut)
		combined := out.String() + errOut.String()
		if strings.Contains(combined, "unknown Sessions row") {
			t.Fatalf("main-checkout id %q refused as unknown: %q", id, combined)
		}
		if !handled || code != 3 {
			t.Fatalf("id %q = handled %v code %d, want 3 no-instance (stderr %q)", id, handled, code, combined)
		}
	}
}

func TestLayoutSessionsIDShapedUnknownPassesThrough(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	writeProjectMeta(t, stateDir, "sidecar", t.TempDir())
	id := "/tmp/not-a-registered-project:worktree:/tmp/not-a-registered-project"
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"layout", "get", "--sessions", id, "--wait", "0"}, &out, &errOut)
	combined := out.String() + errOut.String()
	if strings.Contains(combined, "unknown Sessions row") {
		t.Fatalf("ID-shaped row usage-failed; host should decline: %q", combined)
	}
	if !handled || code != 3 {
		t.Fatalf("pass-through = handled %v code %d, want 3 (stderr %q)", handled, code, combined)
	}
}
