package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
)

// specPayload builds an ActionLayout request whose payload carries a full
// --spec layout's columns (the payload field is the array itself) instead of
// batch panes.
func specPayload(t *testing.T, columns string) uirequest.Request {
	t.Helper()
	payload := map[string]json.RawMessage{"mode": json.RawMessage(`"apply"`), "columns": json.RawMessage(columns)}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return uirequest.Request{
		ID: "layout-" + uirequest.NewRequestID(), Action: uirequest.ActionLayout,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{TmuxSession: "test-shell"},
		Payload: encoded,
	}
}

// gridKinds projects the live tree to a "col.row":kind map for assertions.
func gridKinds(t *testing.T, p *Plugin) map[string]panelayout.Kind {
	t.Helper()
	grid := panelayout.GridOf(p.paneRoot)
	if grid == nil {
		t.Fatalf("live tree escaped the grid vocabulary: %s", encodedTree(t, p))
	}
	out := make(map[string]panelayout.Kind)
	for c, column := range grid.Columns {
		for r, leaf := range column.Cells {
			out[panelayout.Cell{Col: c + 1, Row: r + 1}.String()] = leaf.Kind
		}
	}
	return out
}

// capturingHooks returns shell startup hooks that persist workspace state in
// memory, so an applied layout can be handed to a fresh plugin as a relaunch.
func capturingHooks(saved *state.WorkspaceState) shellStartupHooks {
	return shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return *saved },
		setWorkspaceState: func(_ string, next state.WorkspaceState) error { *saved = next; return nil },
	}
}

// showLiveShellLeaf opens a Shell leaf the way a live session has one: leaf,
// visible flag, and a session name all agreeing. showTermPanel alone stops
// short of the session, which only the panel lifecycle assigns; a test that
// needs the carry grammar needs the whole truth.
func showLiveShellLeaf(t *testing.T, p *Plugin) string {
	t.Helper()
	showTermPanel(t, p, SplitCols, 50)
	p.View(p.width, p.height)
	if p.termPanelSession == "" {
		p.termPanelSession = p.termPanelSessionName()
	}
	if p.shellLeaf() == nil || p.termPanelSession == "" {
		t.Fatal("fixture failed to open a shell leaf with a session")
	}
	return p.termPanelSession
}

// The M4 proof's first half: read the layout back, speak it as a spec with the
// primary moved to the right column, apply — the tree becomes exactly what the
// spec said, live leaves included, with every requested pane accounted in the
// ack items.
func TestLayoutApplySpec_RoundTripMovesPrimaryToRightColumn(t *testing.T) {
	p, _ := layoutRequestFixture(t)

	seed := layoutPayload(t, uirequest.LayoutModeApply, threePanes...)
	if cmd := p.handleUIRequest(seed); cmd == nil {
		t.Fatal("setup batch failed")
	}

	get := layoutPayload(t, uirequest.LayoutModeGet)
	_ = p.handleUIRequest(get)
	var report struct {
		Grid *struct {
			Columns []struct {
				Panes []struct {
					Kind    string   `json:"kind"`
					Session string   `json:"session,omitempty"`
					Tabs    []string `json:"tabs,omitempty"`
				} `json:"panes"`
			} `json:"columns"`
		} `json:"grid"`
	}
	if err := json.Unmarshal(readLayoutAck(t, get).Layout, &report); err != nil {
		t.Fatal(err)
	}
	if report.Grid == nil || len(report.Grid.Columns) != 2 {
		t.Fatalf("get reported %+v, want the seeded 2x2", report.Grid)
	}

	specPanes := func(cell struct {
		Kind    string   `json:"kind"`
		Session string   `json:"session,omitempty"`
		Tabs    []string `json:"tabs,omitempty"`
	}) map[string]any {
		pane := map[string]any{"kind": cell.Kind}
		switch cell.Kind {
		case "primary":
		case "shell":
			pane["session"] = cell.Session
		default:
			tabs := make([]any, len(cell.Tabs))
			for i, tab := range cell.Tabs {
				tabs[i] = tab
			}
			pane["targets"] = tabs
		}
		return pane
	}
	col1, col2 := report.Grid.Columns[0].Panes, report.Grid.Columns[1].Panes
	columns := []any{
		map[string]any{"panes": []any{specPanes(col1[1])}},                     // carried shell alone on the left
		map[string]any{"panes": []any{specPanes(col2[0]), specPanes(col2[1])}}, // file over issue, unchanged
		map[string]any{"panes": []any{specPanes(col1[0])}},                     // THE EDIT: primary now rightmost
	}
	raw, err := json.Marshal(columns)
	if err != nil {
		t.Fatal(err)
	}

	req := specPayload(t, string(raw))
	_ = p.handleUIRequest(req)
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened || ack.Reason != "" {
		t.Fatalf("ack = %s %q", ack.Status, ack.Reason)
	}
	if len(ack.Items) != 4 || ack.ItemsVersion != 1 {
		t.Fatalf("items = %+v, want one per spec pane", ack.Items)
	}
	wantCells := []string{"1.1", "2.1", "2.2", "3.1"}
	for _, item := range ack.Items {
		if item.Verdict != uirequest.ItemVerdictOpened {
			t.Errorf("item %d verdict = %s (%s)", item.Index, item.Verdict, item.Reason)
			continue
		}
		if item.Cell != wantCells[item.Index] {
			t.Errorf("item %d landed at %s, want %s", item.Index, item.Cell, wantCells[item.Index])
		}
	}

	kinds := gridKinds(t, p)
	want := map[string]panelayout.Kind{
		"1.1": panelayout.Shell,
		"2.1": panelayout.Document,
		"2.2": panelayout.Issue,
		"3.1": panelayout.Primary,
	}
	if len(kinds) != len(want) {
		t.Fatalf("final grid = %+v, want %v", kinds, want)
	}
	for cell, kind := range want {
		if kinds[cell] != kind {
			t.Errorf("cell %s = %v, want %v", cell, kinds[cell], kind)
		}
	}
	if leaf := panelayout.FirstOfKind(p.paneRoot, panelayout.Document); leaf == nil || len(p.docs[leaf.ContentID].tabs.Items) != 2 {
		t.Error("the file pane did not come back with both tabs")
	}
	if p.termPanelSession == "" || p.shellLeaf() == nil || !p.termPanelVisible {
		t.Error("the carried shell did not survive the apply")
	}
}

// Decision 5: a spec that omits a live leaf declines NAMING the session and
// leaves the tree byte-for-byte untouched — the terminal is never destroyed
// implicitly.
func TestLayoutApplySpec_OmittedLiveShellDeclinesNamingSession(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	session := showLiveShellLeaf(t, p)
	before := encodedTree(t, p)

	req := specPayload(t, `[
		{"panes":[{"kind":"primary"}]},
		{"panes":[{"kind":"file","targets":["README.md"]}]}
	]`)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("declined spec emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined {
		t.Fatalf("status = %s", ack.Status)
	}
	if !strings.Contains(ack.Reason, session) || !strings.Contains(ack.Reason, "omits the live terminal") {
		t.Fatalf("reason = %q, want the omitted-session rule naming %q", ack.Reason, session)
	}
	if len(ack.Items) != 2 || ack.ItemsVersion != 1 {
		t.Fatalf("items = %+v, want both spec panes accounted for", ack.Items)
	}
	for _, item := range ack.Items {
		if item.Verdict != uirequest.ItemVerdictDeclined || item.Cell != "" || item.Reason == "" {
			t.Errorf("item %d = %+v, want declined with no landing", item.Index, item)
		}
	}
	if after := encodedTree(t, p); after != before {
		t.Fatalf("decline mutated the tree:\nbefore %s\nafter  %s", before, after)
	}
	if p.shellLeaf() == nil || p.termPanelSession != session {
		t.Fatal("the live shell did not survive the decline")
	}
}

// Caps hold in spec mode too, and a grammar violation never reaches the tree.
func TestLayoutApplySpec_OutOfCapGridDeclinesByteIdentical(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	before := encodedTree(t, p)

	fiveColumns := `[` +
		`{"panes":[{"kind":"primary"}]},` +
		`{"panes":[{"kind":"file","targets":["README.md"]}]},` +
		`{"panes":[{"kind":"issue","targets":["td-1a2b3c"]}]},` +
		`{"panes":[{"kind":"note","targets":["nt-1a2b3c"]}]},` +
		`{"panes":[{"kind":"diff"}]}]`
	req := specPayload(t, fiveColumns)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("over-cap spec emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "cap is 4") {
		t.Fatalf("ack = %s %q, want the column-cap refusal", ack.Status, ack.Reason)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatal("decline mutated the tree")
	}

	fiveRows := `[
		{"panes":[{"kind":"primary"},{"kind":"file","targets":["README.md"]},{"kind":"issue","targets":["td-1a2b3c"]},{"kind":"note","targets":["nt-1a2b3c"]},{"kind":"diff"}]}
	]`
	req = specPayload(t, fiveRows)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("over-cap spec emitted a command")
	}
	ack = readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "cap is 4") {
		t.Fatalf("ack = %s %q, want the row-cap refusal", ack.Status, ack.Reason)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatal("decline mutated the tree")
	}
}

// A second live terminal cannot be asked for while one is carried: the live
// leaf cap stands, and a carried session must actually be on screen.
func TestLayoutApplySpec_LiveLeafAccountingRefusals(t *testing.T) {
	p, _ := layoutRequestFixture(t)
	session := showLiveShellLeaf(t, p)
	before := encodedTree(t, p)

	req := specPayload(t, `[
		{"panes":[{"kind":"primary"}]},
		{"panes":[{"kind":"shell","session":"`+session+`","run":"echo hi"}]}
	]`)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("carry-with-run spec emitted a command")
	}
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "takes only") {
		t.Fatalf("ack = %s %q, want the carry-form refusal", ack.Status, ack.Reason)
	}

	req = specPayload(t, `[
		{"panes":[{"kind":"primary"}]},
		{"panes":[{"kind":"shell","session":"sidecar-tp-nowhere"}]}
	]`)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("phantom-session spec emitted a command")
	}
	ack = readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "sidecar-tp-nowhere") {
		t.Fatalf("ack = %s %q, want the unknown-session refusal", ack.Status, ack.Reason)
	}

	req = specPayload(t, `[
		{"panes":[{"kind":"primary"}]},
		{"panes":[{"kind":"shell","session":"`+session+`"}]},
		{"panes":[{"kind":"shell","run":"echo hi"}]}
	]`)
	if cmd := p.handleUIRequest(req); cmd != nil {
		t.Fatal("double-terminal spec emitted a command")
	}
	ack = readLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || !strings.Contains(ack.Reason, "two live terminals") {
		t.Fatalf("ack = %s %q, want the live-cap refusal", ack.Status, ack.Reason)
	}
	if after := encodedTree(t, p); after != before {
		t.Fatal("refusal mutated the tree")
	}
}

// Passive panes absent from the spec close freely; the ones named come back;
// the applied shape is exactly the spec's.
func TestLayoutApplySpec_ReplacesPassivesAndKeepsLiveLeaves(t *testing.T) {
	p, _ := layoutRequestFixture(t)

	seed := layoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md", "docs/notes.md"}},
		uirequest.LayoutPane{Kind: "issue", Targets: []string{"td-1a2b3c"}},
	)
	_ = p.handleUIRequest(seed)
	session := showLiveShellLeaf(t, p)

	req := specPayload(t, `[
		{"panes":[{"kind":"primary"},{"kind":"shell","session":"`+session+`"}]},
		{"panes":[{"kind":"issue","targets":["td-1a2b3c"]}]}
	]`)
	_ = p.handleUIRequest(req)
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("ack = %s %q", ack.Status, ack.Reason)
	}

	kinds := gridKinds(t, p)
	want := map[string]panelayout.Kind{
		"1.1": panelayout.Primary,
		"1.2": panelayout.Shell,
		"2.1": panelayout.Issue,
	}
	if len(kinds) != len(want) {
		t.Fatalf("final grid = %+v, want %v", kinds, want)
	}
	for cell, kind := range want {
		if kinds[cell] != kind {
			t.Errorf("cell %s = %v, want %v", cell, kinds[cell], kind)
		}
	}
	// The file pane closed with its content; nothing else claims its tabs.
	if leaf := panelayout.FirstOfKind(p.paneRoot, panelayout.Document); leaf != nil {
		t.Error("the unnamed file pane survived a spec that omitted it")
	}
	if p.contentDeck != nil {
		t.Error("the stale deck was kept; the next open would reconcile the old tree")
	}
	if p.shellLeaf() == nil || p.termPanelSession != session {
		t.Error("the carried shell lost its session")
	}
}

// A NEW shell pane (run/type form) grafts exactly where the spec put it — the
// two-stage commit keeps the fit-tested shape cell for cell.
func TestLayoutApplySpec_NewShellGraftsIntoItsSpecCell(t *testing.T) {
	p, _ := layoutRequestFixture(t)

	req := specPayload(t, `[
		{"panes":[{"kind":"file","targets":["README.md"]}]},
		{"panes":[{"kind":"primary"}]},
		{"panes":[{"kind":"shell","run":"echo hi","name":"dev server"}]}
	]`)
	cmd := p.handleUIRequest(req)
	_ = cmd
	ack := readLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("ack = %s %q", ack.Status, ack.Reason)
	}
	for _, item := range ack.Items {
		if item.Verdict != uirequest.ItemVerdictOpened {
			t.Errorf("item %d = %s (%s)", item.Index, item.Verdict, item.Reason)
		}
	}

	kinds := gridKinds(t, p)
	want := map[string]panelayout.Kind{
		"1.1": panelayout.Document,
		"2.1": panelayout.Primary,
		"3.1": panelayout.Shell,
	}
	if len(kinds) != len(want) {
		t.Fatalf("final grid = %+v, want %v", kinds, want)
	}
	for cell, kind := range want {
		if kinds[cell] != kind {
			t.Errorf("cell %s = %v, want %v", cell, kinds[cell], kind)
		}
	}
	if name := p.shellLeafTitle(); name != "dev server" {
		t.Errorf("shell title = %q, want the spec's name", name)
	}
	if !p.termPanelVisible || p.shellLeaf() == nil {
		t.Error("the new shell did not become the visible terminal split")
	}
}

// The applied tree is ordinary PaneLayoutJSON state: what the ordinary
// selection save persists decodes back through the ordinary relaunch path.
func TestLayoutApplySpec_RelaunchRestoresAppliedTree(t *testing.T) {
	p, root := layoutRequestFixture(t)
	p.ctx.ProjectRoot = root
	var saved state.WorkspaceState
	p.shellStartupHooks = capturingHooks(&saved)

	req := specPayload(t, `[
		{"panes":[{"kind":"primary"}]},
		{"panes":[{"kind":"file","targets":["README.md","docs/notes.md"]},{"kind":"issue","targets":["td-1a2b3c"]}]}
	]`)
	_ = p.handleUIRequest(req)
	layout, ok := saved.PaneLayouts["shell:test-shell"]
	if !ok || layout == nil || layout.Split == nil {
		t.Fatalf("apply persisted %+v, want the applied tree under shell:test-shell", saved.PaneLayouts)
	}

	relaunch := docPaneTestPlugin(t, root, true)
	relaunch.ctx.ProjectRoot = root
	relaunch.shells = p.shells
	relaunch.shellStartupHooks = capturingHooks(&saved)
	if !relaunch.restoreSelectionState() {
		t.Fatal("relaunch did not restore the selected shell")
	}

	kinds := gridKinds(t, relaunch)
	want := map[string]panelayout.Kind{
		"1.1": panelayout.Primary,
		"2.1": panelayout.Document,
		"2.2": panelayout.Issue,
	}
	if len(kinds) != len(want) {
		t.Fatalf("relaunched grid = %+v, want %v", kinds, want)
	}
	for cell, kind := range want {
		if kinds[cell] != kind {
			t.Errorf("relaunch cell %s = %v, want %v", cell, kinds[cell], kind)
		}
	}
	if leaf := panelayout.FirstOfKind(relaunch.paneRoot, panelayout.Document); leaf == nil ||
		len(relaunch.docs[leaf.ContentID].tabs.Items) != 2 {
		t.Error("relaunch did not bring back both file tabs")
	}
}
