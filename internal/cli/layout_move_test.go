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
	"github.com/marcus/sidecar/internal/uirequest"
)

// Usage errors are answered before a request is ever written: nothing on the
// bus, nothing for a host to decline, exit 2.
func TestLayoutMove_UsageErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no source and no destination": {[]string{"layout", "move"}, "--to"},
		"no destination":               {[]string{"layout", "move", "2.1"}, "--to"},
		"no source":                    {[]string{"layout", "move", "--to", "left"}, "name the pane to move"},
		"both source forms":            {[]string{"layout", "move", "2.1", "--focused", "--to", "left"}, "not both"},
		"two source cells":             {[]string{"layout", "move", "2.1", "1.1", "--to", "left"}, "a second"},
		"source is not a cell":         {[]string{"layout", "move", "two", "--to", "left"}, "grid cell"},
		"source outside the grid":      {[]string{"layout", "move", "5.1", "--to", "left"}, "outside the 4x4"},
		"destination is a word":        {[]string{"layout", "move", "2.1", "--to", "sideways"}, "is not a cell"},
		"destination column too big":   {[]string{"layout", "move", "2.1", "--to", "9"}, "outside the 4-column"},
		"destination cell too big":     {[]string{"layout", "move", "2.1", "--to", "1.9"}, "outside the 4x4"},
		"empty destination":            {[]string{"layout", "move", "2.1", "--to", ""}, "--to requires"},
		"sessions with project":        {[]string{"layout", "move", "2.1", "--to", "left", "--sessions", "--project", "sidecar"}, "--sessions"},
		"unknown option":               {[]string{"layout", "move", "2.1", "--to", "left", "--nope"}, "unknown option"},
	} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tc.args, &out, &errOut)
			if !handled || code != 2 {
				t.Fatalf("Run(%v) = handled %v code %d, want usage 2", tc.args, handled, code)
			}
			if combined := out.String() + errOut.String(); !strings.Contains(combined, tc.want) {
				t.Fatalf("Run(%v) output %q does not explain %q", tc.args, combined, tc.want)
			}
		})
	}
}

// Everything the CLI accepts, the host's own grammar accepts too — they call
// the same validator, which is what keeps a usage error and a decline from
// disagreeing about what a move is.
func TestLayoutMove_AcceptedFormsAreTheHostsFormsToo(t *testing.T) {
	for name, move := range map[string]uirequest.LayoutMove{
		"cell to cell":        {From: "2.1", To: "1.2"},
		"cell to column":      {From: "2.1", To: "3"},
		"focused to left":     {Focused: true, To: "left"},
		"focused to right":    {Focused: true, To: "right"},
		"focused to up":       {Focused: true, To: "up"},
		"focused to down":     {Focused: true, To: "down"},
		"direction uppercase": {From: "1.1", To: "RIGHT"},
		"padded destination":  {From: "1.1", To: " 2.2 "},
	} {
		t.Run(name, func(t *testing.T) {
			if err := uirequest.ValidateLayoutMove(move); err != nil {
				t.Fatalf("%+v rejected: %v", move, err)
			}
			payload, err := json.Marshal(uirequest.LayoutPayload{Mode: uirequest.LayoutModeMove, Move: &move})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := uirequest.DecodeLayoutPayload(payload)
			if err != nil {
				t.Fatalf("host rejects the payload the CLI writes: %v", err)
			}
			if decoded.Move == nil || *decoded.Move != move {
				t.Fatalf("move did not survive the wire: %+v", decoded.Move)
			}
			if len(decoded.Panes) != 0 || len(decoded.Columns) != 0 {
				t.Fatalf("a move payload carried panes or a spec: %+v", decoded)
			}
		})
	}
}

// The column form and the cell form are different destinations; "3" must not
// quietly become 3.1, which is what a plain cell parse would do with it.
func TestLayoutMove_BareNumberIsAColumnNotACell(t *testing.T) {
	target, err := uirequest.ParseLayoutMoveTo("3")
	if err != nil || target.Form != uirequest.LayoutMoveColumn || target.Column != 3 {
		t.Fatalf(`ParseLayoutMoveTo("3") = %+v, %v; want the column form`, target, err)
	}
	cell, err := uirequest.ParseLayoutMoveTo("3.1")
	if err != nil || cell.Form != uirequest.LayoutMoveCell || cell.Cell != (panelayout.Cell{Col: 3, Row: 1}) {
		t.Fatalf(`ParseLayoutMoveTo("3.1") = %+v, %v; want cell 3.1`, cell, err)
	}
	for word, want := range map[string]panelayout.Direction{
		"left": panelayout.DirectionLeft, "right": panelayout.DirectionRight,
		"up": panelayout.DirectionUp, "down": panelayout.DirectionDown,
	} {
		got, err := uirequest.ParseLayoutMoveTo(word)
		if err != nil || got.Form != uirequest.LayoutMoveDirection || got.Direction != want {
			t.Fatalf("ParseLayoutMoveTo(%q) = %+v, %v", word, got, err)
		}
	}
}

// moveAckRun writes a layout move on an isolated state tree, answers the
// request with the given ack, and returns the CLI's exit code and output. It is
// the whole round trip minus a running app.
func moveAckRun(t *testing.T, args []string, answer func(uirequest.Request) uirequest.Ack) (int, string, string, uirequest.LayoutPayload) {
	t.Helper()
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

	captured := make(chan uirequest.LayoutPayload, 1)
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
				payload, _ := uirequest.DecodeLayoutPayload(req.Payload)
				captured <- payload
				_ = uirequest.WriteAck(filepath.Join(stateHome, "sidecar"), req.ID, req.Action, answer(req))
				return
			}
		}
		close(captured)
	}()

	var out, errOut bytes.Buffer
	_, code := Run(append(args, "--wait", "10s"), &out, &errOut)
	payload := <-captured
	return code, out.String(), errOut.String(), payload
}

// A move that landed: exit 0, the landed cell and the surface it changed both
// reported, and the payload on the bus is the documented move record.
func TestLayoutMove_MovedIsExitZeroAndNamesTheLandedCellAndSurface(t *testing.T) {
	code, out, errOut, payload := moveAckRun(t, []string{"layout", "move", "2.1", "--to", "left"},
		func(req uirequest.Request) uirequest.Ack {
			return uirequest.Ack{
				Instance: "test-instance", Status: uirequest.StatusMoved, At: time.Now().UTC(),
				ItemsVersion: 1,
				Items: []uirequest.AckItem{{
					Verdict: uirequest.ItemVerdictMoved, Cell: "1.2", Pane: 7, Surface: "shell:sidecar-sh-x",
				}},
			}
		})
	if code != 0 {
		t.Fatalf("move = exit %d, stderr %q", code, errOut)
	}
	if payload.Mode != uirequest.LayoutModeMove || payload.Move == nil ||
		payload.Move.From != "2.1" || payload.Move.To != "left" || payload.Move.Focused {
		t.Fatalf("payload = %+v, want the move record the arguments describe", payload)
	}
	if !strings.Contains(out, "moved the pane to 1.2") || !strings.Contains(out, "shell:sidecar-sh-x") {
		t.Fatalf("move output does not name the landed cell and surface:\n%s", out)
	}
	if strings.Contains(out, `"action"`) {
		t.Fatalf("move without --json wrote its structured result to stdout:\n%s", out)
	}
}

// An accepted no-op is exit 0 and says so in its own word. Reporting it as a
// move would tell an agent the layout changed when it did not; reporting it as
// a decline would tell it the request failed when the pane is exactly where it
// asked for it to be.
func TestLayoutMove_UnchangedIsASuccessWithItsOwnWord(t *testing.T) {
	code, out, errOut, _ := moveAckRun(t, []string{"layout", "move", "--focused", "--to", "up", "--json"},
		func(req uirequest.Request) uirequest.Ack {
			return uirequest.Ack{
				Instance: "test-instance", Status: uirequest.StatusUnchanged, Reason: "already at the top",
				At: time.Now().UTC(), ItemsVersion: 1,
				Items: []uirequest.AckItem{{
					Verdict: uirequest.ItemVerdictUnchanged, Cell: "2.1", Pane: 7,
					Surface: "shell:sidecar-sh-x", Reason: "already at the top",
				}},
			}
		})
	if code != 0 {
		t.Fatalf("unchanged = exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "unchanged: the pane is still at 2.1") || !strings.Contains(out, "already at the top") {
		t.Fatalf("unchanged output does not say what happened:\n%s", out)
	}
	first, _, _ := strings.Cut(out, "\n")
	var result struct {
		Mode   string              `json:"mode"`
		Status string              `json:"status"`
		Items  []uirequest.AckItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(first), &result); err != nil {
		t.Fatalf("--json first line does not decode: %v\n%s", err, out)
	}
	if result.Mode != uirequest.LayoutModeMove || result.Status != string(uirequest.StatusUnchanged) {
		t.Fatalf("structured result = %+v, want an explicit unchanged status", result)
	}
	if len(result.Items) != 1 || result.Items[0].Verdict != uirequest.ItemVerdictUnchanged {
		t.Fatalf("structured items = %+v, want an unchanged verdict", result.Items)
	}
}

// A host-side refusal is exit 4 with the reason verbatim, exactly as get and
// apply refuse. Layout never queues, so there is no third outcome.
func TestLayoutMove_DeclineIsExitFourWithTheReasonVerbatim(t *testing.T) {
	const reason = "the window is too small to split"
	code, _, errOut, _ := moveAckRun(t, []string{"layout", "move", "2.1", "--to", "3"},
		func(req uirequest.Request) uirequest.Ack {
			return uirequest.Ack{
				Instance: "test-instance", Status: uirequest.StatusDeclined, Reason: reason,
				At: time.Now().UTC(), ItemsVersion: 1,
				Items: []uirequest.AckItem{{Verdict: uirequest.ItemVerdictDeclined, Reason: reason}},
			}
		})
	if code != 4 {
		t.Fatalf("declined move = exit %d, want 4", code)
	}
	if !strings.Contains(errOut, reason) {
		t.Fatalf("decline did not carry the reason verbatim:\n%s", errOut)
	}
}

func TestLayoutMove_HelpAndAgentDocDescribeEveryForm(t *testing.T) {
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"layout", "move", "--help"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("help = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	help := out.String()
	for _, want := range []string{"--focused", "--to", "--sessions", "left", "right", "up", "down", "unchanged", "never queue"} {
		if !strings.Contains(help, want) {
			t.Errorf("layout move help does not mention %q:\n%s", want, help)
		}
	}

	cmd := RootCommand().FindSubcommand("layout").FindSubcommand("move")
	if cmd == nil || cmd.Agent.Invocation == "" || cmd.Agent.Summary == "" {
		t.Fatalf("layout move declares no AgentDoc: %+v", cmd)
	}
	if !cmd.Mutates {
		t.Fatal("layout move is a mutating verb and must be declared as one")
	}
	agents := RenderAgents(RootCommand())
	if !strings.Contains(agents, "sidecar layout move") {
		t.Fatalf("sidecar agents does not list layout move:\n%s", agents)
	}
	if !strings.Contains(agents, "layout move repositions one pane that is already open") {
		t.Fatalf("sidecar agents does not describe the pane layout surface:\n%s", agents)
	}
}
