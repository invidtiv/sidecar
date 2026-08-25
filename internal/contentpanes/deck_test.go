package contentpanes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func testPlacement() Placement {
	return Placement{
		Box: panelayout.Box{W: 200, H: 60},
		Floors: panelayout.Floors{
			Primary:  panelayout.Floor{Width: 20, Height: 5},
			Doc:      panelayout.Floor{Width: 20, Height: 5},
			Issue:    panelayout.Floor{Width: 20, Height: 5},
			Note:     panelayout.Floor{Width: 20, Height: 5},
			Diff:     panelayout.Floor{Width: 20, Height: 5},
			Resource: panelayout.Floor{Width: 20, Height: 5},
		},
	}
}

func testContext(root string) SurfaceContext {
	return SurfaceContext{Root: root, Surface: "files", BaseRef: "main", Epoch: 7}
}

func fileRef(path string) contentlink.Ref {
	return contentlink.Ref{Kind: contentlink.KindFile, Value: path}
}

func issueRef(id string) contentlink.Ref {
	return contentlink.Ref{Kind: contentlink.KindIssue, Value: id}
}

func noteRef(id string) contentlink.Ref {
	return contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: id}
}

func diffRef(spec string) contentlink.Ref {
	return contentlink.Ref{Kind: contentlink.KindDiff, Value: spec}
}

func resourceRefForTest(locator string) contentlink.Ref {
	return contentlink.Ref{Kind: contentlink.KindResource, Provider: "linear", Matcher: "issue", Value: locator}
}

func paneForKind(d *Deck, kind panelayout.Kind) *pane {
	leaf := panelayout.FirstOfKind(d.root, kind)
	if leaf == nil {
		return nil
	}
	return d.panes[leaf.ID]
}

func TestDeckPlacementAndHomogeneousTabs(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	place := testPlacement()

	first := d.Open(ctx, fileRef("README.md"), place)
	if first.Status != StatusOpened || !first.CreatedLeaf || !first.CreatedTab {
		t.Fatalf("first Open = %#v", first)
	}
	docLeaf := panelayout.FirstOfKind(d.root, panelayout.Document)
	if docLeaf == nil || d.focus != docLeaf.ID {
		t.Fatalf("document leaf/focus = %#v/%d", docLeaf, d.focus)
	}
	root := d.root
	if root.Split == nil || root.Split.Axis != panelayout.Columns || root.Split.A.Kind != panelayout.Primary {
		t.Fatalf("first placement = %#v", root)
	}

	same := d.Open(ctx, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md", Line: 42}, place)
	if same.Status != StatusFocused || same.CreatedLeaf || same.CreatedTab || same.LeafID != first.LeafID {
		t.Fatalf("same document = %#v", same)
	}
	if got := paneForKind(d, panelayout.Document); got == nil || len(got.tabs) != 1 || got.tabs[0].ref.Line != 42 {
		t.Fatalf("same-kind retarget tabs = %#v", got)
	}

	second := d.Open(ctx, fileRef("docs/guide.md"), place)
	if second.Status != StatusOpened || second.CreatedLeaf || !second.CreatedTab || second.LeafID != first.LeafID {
		t.Fatalf("second document = %#v", second)
	}
	if got := paneForKind(d, panelayout.Document); len(got.tabs) != 2 || got.active != 1 {
		t.Fatalf("document tabs = %#v", got)
	}

	issue := d.Open(ctx, issueRef("td-1a2b3c"), place)
	if issue.Status != StatusOpened || !issue.CreatedLeaf {
		t.Fatalf("issue Open = %#v", issue)
	}
	if panelayout.FirstOfKind(d.root, panelayout.Issue) == nil || panelayout.FirstOfKind(d.root, panelayout.Document) == nil {
		t.Fatalf("different kinds did not keep distinct leaves: %#v", d.root)
	}

	note := d.Open(ctx, noteRef("nt-abc123"), place)
	if note.Status != StatusOpened || !note.CreatedLeaf || note.Kind != panelayout.Note {
		t.Fatalf("note Open = %#v", note)
	}
	sameNote := d.Open(ctx, noteRef("nt-abc123"), place)
	if sameNote.Status != StatusFocused || sameNote.CreatedLeaf || sameNote.CreatedTab {
		t.Fatalf("same note = %#v", sameNote)
	}
	secondNote := d.Open(ctx, noteRef("nt-def456"), place)
	if secondNote.Status != StatusOpened || secondNote.CreatedLeaf || !secondNote.CreatedTab || secondNote.LeafID != note.LeafID {
		t.Fatalf("second note = %#v", secondNote)
	}
	if got := paneForKind(d, panelayout.Note); got == nil || len(got.tabs) != 2 || got.active != 1 {
		t.Fatalf("note tabs = %#v", got)
	}
}

func TestDeckNotePersistRoundTrip(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	place := testPlacement()
	if out := d.Open(ctx, noteRef("nt-abc123"), place); !out.Accepted() {
		t.Fatalf("open note = %#v", out)
	}
	if out := d.Open(ctx, noteRef("nt-def456"), place); !out.Accepted() {
		t.Fatalf("open second note = %#v", out)
	}
	encoded := d.Encode()
	raw, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"kind":"note"`) || !strings.Contains(string(raw), "nt-abc123") {
		t.Fatalf("encoded note state missing identity: %s", raw)
	}
	restored := Decode(ctx, Config{}, encoded)
	if restored.Leaf(panelayout.Note) == 0 {
		t.Fatal("restored deck lost the note leaf")
	}
	items, active := restored.Tabs(restored.Leaf(panelayout.Note))
	if len(items) != 2 || active != 1 || items[0].Ref.Value != "nt-abc123" || items[1].Ref.Value != "nt-def456" {
		t.Fatalf("restored note tabs = %#v active=%d", items, active)
	}
}

func TestDeckSameIdentityReloadsWhenSurfaceContextChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(root)
	d := New(ctx, Config{})
	first := d.Open(ctx, fileRef("README.md"), testPlacement())
	if first.Command == nil {
		t.Fatal("first open did not load")
	}
	firstResult := first.Command().(Result)
	if _, ok := d.Apply(firstResult); !ok {
		t.Fatal("first result was not applied")
	}

	other := ctx
	other.Root = t.TempDir()
	other.Surface = "other"
	other.Epoch++
	if err := os.WriteFile(filepath.Join(other.Root, "README.md"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := d.Open(other, fileRef("README.md"), testPlacement())
	if got.Status != StatusFocused || got.Command == nil {
		t.Fatalf("cross-surface focus = %#v, want reload command", got)
	}
	result := got.Command().(Result)
	if result.ID.Surface != other.Surface || result.ID.Epoch != other.Epoch {
		t.Fatalf("reload identity = %#v, want surface %q epoch %d", result.ID, other.Surface, other.Epoch)
	}
}

func TestDeckDocumentPathsPreserveAbsoluteAndRootRelativeMeaning(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "relative.txt"), []byte("relative\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	absDir := t.TempDir()
	absPath := filepath.Join(absDir, "outside.txt")
	if err := os.WriteFile(absPath, []byte("absolute\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(root)
	d := New(ctx, Config{})

	for _, tc := range []struct {
		name     string
		ref      contentlink.Ref
		wantRoot string
		wantBody string
	}{
		{name: "relative", ref: contentlink.Ref{Kind: contentlink.KindFile, Value: "relative.txt", Line: 2}, wantRoot: root, wantBody: "relative\nsecond\n"},
		{name: "absolute", ref: contentlink.Ref{Kind: contentlink.KindFile, Value: absPath, Line: 2}, wantRoot: "", wantBody: "absolute\nsecond\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := d.Open(ctx, tc.ref, testPlacement())
			if out.Command == nil {
				t.Fatal("document open returned no load command")
			}
			result := out.Command().(Result)
			loaded := result.Payload.(docview.LoadedMsg)
			if loaded.Path != filepath.ToSlash(filepath.Clean(tc.ref.Value)) {
				t.Fatalf("loaded path = %q, want %q", loaded.Path, tc.ref.Value)
			}
			if loaded.Result.Error != nil || loaded.Result.Content != tc.wantBody {
				t.Fatalf("load result = error %v body %q", loaded.Result.Error, loaded.Result.Content)
			}
			view := d.Viewer(out.LeafID).(*docview.Model)
			if view.Root() != tc.wantRoot {
				t.Fatalf("document root = %q, want %q", view.Root(), tc.wantRoot)
			}
			if _, applied := d.Apply(result); !applied {
				t.Fatal("current document result was rejected")
			}
			if view.Rendered() {
				t.Fatal("a line-qualified text target must remain raw")
			}
		})
	}
}

func TestDeckAbsoluteDocumentPersistsAndReopensWithoutDoubleRooting(t *testing.T) {
	root := t.TempDir()
	absPath := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(absPath, []byte("# outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(root)
	d := New(ctx, Config{})
	opened := d.Open(ctx, fileRef(absPath), testPlacement())
	if opened.Command == nil {
		t.Fatal("absolute document open returned no command")
	}
	_ = opened.Command().(Result)
	state := d.Encode()
	d.CloseActive()

	restored := Decode(ctx, Config{}, state)
	p := paneForKind(restored, panelayout.Document)
	if p == nil || len(p.tabs) != 1 || p.tabs[0].ref.Value != filepath.ToSlash(absPath) {
		t.Fatalf("restored absolute tab = %#v", p)
	}
	cmd := restored.SelectTab(p.leafID, 0)
	if cmd == nil {
		t.Fatal("restored absolute tab did not load")
	}
	current := cmd().(Result)
	loaded := current.Payload.(docview.LoadedMsg)
	if loaded.Result.Error != nil || loaded.Result.Content != "# outside\n" {
		t.Fatalf("restored load = error %v body %q", loaded.Result.Error, loaded.Result.Content)
	}
	if got := restored.Viewer(p.leafID).(*docview.Model).Root(); got != "" {
		t.Fatalf("restored absolute document root = %q, want empty", got)
	}
	if _, applied := restored.Apply(current); !applied {
		t.Fatal("restored tab rejected its current async result")
	}
}

func TestDeckMissingAbsoluteDocumentReportsTheExactTarget(t *testing.T) {
	ctx := testContext(t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing.txt")
	d := New(ctx, Config{})
	out := d.Open(ctx, fileRef(missing), testPlacement())
	if out.Status != StatusOpened || out.Command == nil {
		t.Fatalf("missing absolute Open = %#v", out)
	}
	result := out.Command().(Result)
	loaded := result.Payload.(docview.LoadedMsg)
	if loaded.Result.Error == nil {
		t.Fatal("missing absolute target reported success")
	}
	if loaded.Path != filepath.ToSlash(missing) || strings.Contains(loaded.Result.Error.Error(), ctx.Root) {
		t.Fatalf("missing result path/error = %q / %v", loaded.Path, loaded.Result.Error)
	}
}

func TestDeckDiffUsesDedicatedDiffRoot(t *testing.T) {
	ctx := testContext(t.TempDir())
	ctx.DiffRoot = t.TempDir()
	d := New(ctx, Config{})
	out := d.Open(ctx, diffRef("wt"), testPlacement())
	view := d.Viewer(out.LeafID).(*workspacediff.View)
	if view.WorkDir != ctx.DiffRoot {
		t.Fatalf("diff WorkDir = %q, want dedicated DiffRoot %q", view.WorkDir, ctx.DiffRoot)
	}
}

func TestDeckDiffSnapshotContinuationKeepsDedicatedDiffRoot(t *testing.T) {
	ctx := testContext(t.TempDir())
	ctx.DiffRoot = t.TempDir()
	d := New(ctx, Config{})
	out := d.Open(ctx, diffRef("wt"), testPlacement())
	view := d.Viewer(out.LeafID).(*workspacediff.View)
	view.Scope = workspacediff.ScopeCommits
	cmd := d.ApplyBroadcast(workspacediff.SnapshotMsg{
		Epoch: ctx.Epoch, Binding: view.Binding, WorkspaceID: ctx.Surface,
		Identity: workspacediff.IdentityWorkingTree,
		Snapshot: &workspacediff.Snapshot{
			State:   workspacediff.LoadStateReady,
			Commits: []workspacediff.CommitInfo{{Hash: "deadbeef", Subject: "fix"}},
		},
	})
	if cmd == nil {
		t.Fatal("commit snapshot did not start its selected-commit continuation")
	}
	if got := view.WorkDir; got != ctx.DiffRoot {
		t.Fatalf("snapshot continuation rebound WorkDir = %q, want DiffRoot %q", got, ctx.DiffRoot)
	}
}

func TestDeckLateResourceResolverConfiguresFutureViewer(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	calls := 0
	d.SetResourceResolver(func(_ int, _, _ uint64, _ resource.Reference, _ bool) tea.Cmd {
		calls++
		return func() tea.Msg { return nil }
	})
	out := d.Open(ctx, resourceRefForTest("ENG-42"), testPlacement())
	if out.Command == nil {
		t.Fatal("resource opened after late resolver setup returned no load command")
	}
	_ = out.Command()
	if calls != 1 {
		t.Fatalf("late resolver calls = %d, want 1", calls)
	}
}

func TestDeckSetContextRearmsEveryKindAndSelectRestartsLoad(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(root)
	d := New(ctx, Config{ResourceResolver: func(_ int, _, _ uint64, _ resource.Reference, _ bool) tea.Cmd {
		return func() tea.Msg { return nil }
	}})
	place := testPlacement()
	place.Box = panelayout.Box{W: 400, H: 120}
	refs := []contentlink.Ref{
		fileRef("README.md"), issueRef("td-1a2b3c"), diffRef("wt"), resourceRefForTest("ENG-42"),
	}
	for _, ref := range refs {
		if out := d.Open(ctx, ref, place); out.Command == nil {
			t.Fatalf("initial Open(%s) did not start work", ref.Kind)
		}
	}

	// Reusing Surface and Epoch is intentional: a root/base change alone must
	// invalidate results and viewers from the old context.
	other := ctx
	other.Root = t.TempDir()
	other.BaseRef = "release"
	cmds := d.SetContext(other)
	if len(cmds) == 0 {
		t.Fatal("SetContext did not restart loads for visible armed tabs")
	}
	started := map[panelayout.Kind]bool{}
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		result, ok := cmd().(Result)
		if !ok {
			t.Fatalf("visible reload = %T, want Result", cmd())
		}
		tab := d.tabByID(result.ID.TabID)
		if tab == nil {
			t.Fatalf("reload targeted missing tab %d", result.ID.TabID)
		}
		for _, kind := range []panelayout.Kind{panelayout.Document, panelayout.Issue, panelayout.Diff, panelayout.Resource} {
			if paneForKind(d, kind) != nil && paneForKind(d, kind).tabs[0] == tab {
				started[kind] = true
			}
		}
	}
	for _, kind := range []panelayout.Kind{panelayout.Document, panelayout.Issue, panelayout.Diff, panelayout.Resource} {
		leaf := panelayout.FirstOfKind(d.root, kind)
		if leaf == nil {
			t.Fatalf("kind %d leaf disappeared", kind)
		}
		p := d.panes[leaf.ID]
		if p == nil || len(p.tabs) != 1 || !sameContext(p.tabs[0].ctx, other) {
			t.Fatalf("kind %d tab was not rebound: %#v", kind, p)
		}
		if !started[kind] {
			t.Fatalf("kind %d visible tab was not asked to load after SetContext", kind)
		}
	}
}

func TestSetContextReloadsVisibleDocumentWithoutWaitingForSelect(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(root)
	d := New(ctx, Config{})
	opened := d.Open(ctx, fileRef("README.md"), testPlacement())
	if opened.Command == nil {
		t.Fatal("open did not load")
	}
	if _, ok := d.Apply(opened.Command().(Result)); !ok {
		t.Fatal("first result was not applied")
	}
	view := d.Viewer(opened.LeafID).(*docview.Model)
	view.SetSize(40, 6)
	if strings.Contains(view.View(), "Loading document") {
		t.Fatal("loaded document still shows loading")
	}

	other := ctx
	other.BaseRef = "release"
	cmds := d.SetContext(other)
	if len(cmds) != 1 {
		t.Fatalf("SetContext cmds = %d, want 1 visible document load", len(cmds))
	}
	view = d.Viewer(opened.LeafID).(*docview.Model)
	view.SetSize(40, 6)
	if !strings.Contains(view.View(), "Loading document") {
		t.Fatal("rebind did not arm a loading placeholder")
	}
	if _, ok := d.Apply(cmds[0]().(Result)); !ok {
		t.Fatal("SetContext load was not applied")
	}
	if strings.Contains(view.View(), "Loading document") {
		t.Fatal("visible document stayed on loading after SetContext")
	}
	if !strings.Contains(view.View(), "hello") {
		t.Fatalf("reloaded view = %q", view.View())
	}
}

func TestDecodeLoadVisibleStartsArmedActiveTabs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(root)
	d := New(ctx, Config{})
	place := testPlacement()
	place.Box = panelayout.Box{W: 400, H: 120}
	if out := d.Open(ctx, fileRef("README.md"), place); out.Command == nil {
		t.Fatal("document open did not load")
	}
	if out := d.Open(ctx, issueRef("td-1a2b3c"), place); out.Command == nil {
		t.Fatal("issue open did not load")
	}
	restored := Decode(ctx, Config{}, d.Encode())
	doc := paneForKind(restored, panelayout.Document)
	if doc == nil || !doc.tabs[0].view.(*documentViewer).view.NeedsLoad() {
		t.Fatal("restored document was not armed")
	}
	if cmds := restored.LoadVisible(); len(cmds) < 2 {
		t.Fatalf("LoadVisible = %d cmds, want the visible document and issue", len(cmds))
	}
}

func TestDeckDiffRejectsRawBroadcastFromReusedSurfaceEpochAfterRebind(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	out := d.Open(ctx, diffRef("wt"), testPlacement())
	before := d.Viewer(out.LeafID).(*workspacediff.View)
	stale := workspacediff.SnapshotMsg{
		Epoch: ctx.Epoch, Binding: before.Binding, WorkspaceID: ctx.Surface,
		Identity: workspacediff.IdentityWorkingTree,
	}
	other := ctx
	other.Root = t.TempDir()
	other.BaseRef = "release"
	d.SetContext(other)
	after := d.Viewer(out.LeafID).(*workspacediff.View)
	if before.Binding == 0 || after.Binding == before.Binding {
		t.Fatalf("diff binding was reused across context: before=%d after=%d", before.Binding, after.Binding)
	}
	if after.State != workspacediff.LoadStateLoading {
		t.Fatalf("rebound view state = %v, want loading from SetContext", after.State)
	}
	after.ApplySnapshotMsg(stale, other.Root, other.Surface)
	if after.State != workspacediff.LoadStateLoading {
		t.Fatalf("stale raw snapshot changed rebound view state to %v", after.State)
	}
}

func TestDeckDiffDirectOpenRebindRejectsOldAndZeroBindingBroadcasts(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	first := d.Open(ctx, diffRef("wt"), testPlacement())
	before := d.Viewer(first.LeafID).(*workspacediff.View)
	stale := workspacediff.SnapshotMsg{
		Epoch: ctx.Epoch, Binding: before.Binding, WorkspaceID: ctx.Surface,
		Identity: workspacediff.IdentityWorkingTree,
	}
	other := ctx
	other.Root = t.TempDir()
	other.BaseRef = "release"
	reopened := d.Open(other, diffRef("wt"), testPlacement())
	after := d.Viewer(first.LeafID).(*workspacediff.View)
	if reopened.Command == nil || after.Binding == before.Binding {
		t.Fatalf("direct Open did not rebind/reload: outcome=%#v before=%d after=%d", reopened, before.Binding, after.Binding)
	}
	for _, msg := range []workspacediff.SnapshotMsg{stale, {
		Epoch: other.Epoch, WorkspaceID: other.Surface, Identity: workspacediff.IdentityWorkingTree,
	}} {
		after.ApplySnapshotMsg(msg, other.Root, other.Surface)
		if after.State != workspacediff.LoadStateLoading {
			t.Fatalf("stale/zero-binding raw snapshot changed rebound state to %v: %#v", after.State, msg)
		}
	}
}

func TestDeckThirdPaneFollowsTheGridAndFitRefusalIsNonMutating(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	place := testPlacement()
	doc := d.Open(ctx, fileRef("README.md"), place)
	issue := d.Open(ctx, issueRef("td-1a2b3c"), place)
	place.Boxes = map[int]panelayout.Box{
		doc.LeafID:   {W: 40, H: 10},
		issue.LeafID: {W: 80, H: 30},
	}
	// With the right column holding two content panes, the grid rule splits
	// the primary column — the boxes no longer choose.
	plan, ok := d.PlanOpen(diffRef("wt"), place.Boxes)
	if !ok || plan.Split != 1 || plan.Axis != panelayout.Rows {
		t.Fatalf("PlanOpen diff = %#v ok=%v, want a split of the primary leaf", plan, ok)
	}

	before := d.Encode()
	focus := d.focus
	place.Box = panelayout.Box{W: 30, H: 8}
	out := d.Open(ctx, diffRef("wt"), place)
	if out.Status != StatusRefused || out.Refusal != RefusalFit {
		t.Fatalf("narrow Open = %#v", out)
	}
	if got := d.Encode(); !reflect.DeepEqual(got, before) || d.focus != focus {
		t.Fatalf("fit refusal mutated deck\nbefore=%#v\nafter=%#v\nfocus=%d want %d", before, got, d.focus, focus)
	}
}

func TestDeckFocusHideCloseAndReopen(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	place := testPlacement()
	d.Open(ctx, fileRef("one.md"), place)
	d.Open(ctx, fileRef("two.md"), place)
	issue := d.Open(ctx, issueRef("td-1a2b3c"), place)

	ids := leafIDs(d.root)
	if len(ids) != 3 || d.CycleFocus(1) != ids[0] || d.CycleFocus(-1) != ids[2] {
		t.Fatalf("focus wrap ids=%v focus=%d", ids, d.focus)
	}
	docLeaf := panelayout.FirstOfKind(d.root, panelayout.Document)
	if !d.FocusLeaf(docLeaf.ID) || !d.HideFocused() || !d.Hidden(panelayout.Document) {
		t.Fatal("document hide failed")
	}
	if panelayout.FirstOfKind(d.root, panelayout.Document) != nil || d.focus == docLeaf.ID {
		t.Fatalf("hidden document remains visible: root=%#v focus=%d", d.root, d.focus)
	}
	narrow := place
	narrow.Box = panelayout.Box{W: 30, H: 8}
	if refused := d.Open(ctx, fileRef("three.md"), narrow); refused.Refusal != RefusalFit {
		t.Fatalf("narrow hidden reopen = %#v", refused)
	}
	if !d.Hidden(panelayout.Document) || panelayout.FirstOfKind(d.root, panelayout.Document) != nil {
		t.Fatal("refused reopen consumed the hidden pane")
	}

	reopen := d.Open(ctx, fileRef("three.md"), place)
	if reopen.Status != StatusOpened || !reopen.CreatedLeaf || d.Hidden(panelayout.Document) {
		t.Fatalf("reopen = %#v hidden=%v", reopen, d.Hidden(panelayout.Document))
	}
	if got := paneForKind(d, panelayout.Document); got == nil || len(got.tabs) != 3 || got.tabs[0].ref.Value != "one.md" {
		t.Fatalf("reopened tabs = %#v", got)
	}

	if !d.CloseActive() {
		t.Fatal("close active document failed")
	}
	if got := paneForKind(d, panelayout.Document); got == nil || len(got.tabs) != 2 {
		t.Fatalf("close one tab = %#v", got)
	}
	d.CloseActive()
	d.CloseActive()
	if panelayout.FirstOfKind(d.root, panelayout.Document) != nil || d.Hidden(panelayout.Document) {
		t.Fatalf("last close retained document: root=%#v hidden=%v", d.root, d.Hidden(panelayout.Document))
	}
	if panelayout.FirstOfKind(d.root, panelayout.Issue) == nil || d.focus == issue.LeafID && d.panes[issue.LeafID] == nil {
		t.Fatal("closing document damaged issue leaf")
	}
}

func TestDeckForgetLeafDropsAllTabsAndCollapsesLeaf(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	place := testPlacement()
	doc := d.Open(ctx, fileRef("one.md"), place)
	d.Open(ctx, fileRef("two.md"), place)
	issue := d.Open(ctx, issueRef("td-1111aa"), place)
	if !d.ForgetLeaf(doc.LeafID) {
		t.Fatal("ForgetLeaf failed")
	}
	if panelayout.FirstOfKind(d.root, panelayout.Document) != nil || d.Hidden(panelayout.Document) {
		t.Fatalf("ForgetLeaf retained document: root=%#v hidden=%v", d.root, d.Hidden(panelayout.Document))
	}
	if panelayout.FirstOfKind(d.root, panelayout.Issue) == nil || d.panes[issue.LeafID] == nil {
		t.Fatal("ForgetLeaf damaged companion issue leaf")
	}
}

func TestDeckEncodeDecodeIsReferenceOnlyAndArmsRestoredTabs(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	place := testPlacement()
	d.Open(ctx, fileRef("README.md"), place)
	d.Open(ctx, issueRef("td-1a2b3c"), place)
	d.Open(ctx, diffRef("abcdef1"), place)
	resource := d.Open(ctx, resourceRefForTest("ENG-42"), place)
	d.FocusLeaf(resource.LeafID)
	d.HideFocused()

	state := d.Encode()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"loaded body", "rendered frame", "provider secret", "oauth"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("encoded state contains loaded content %q: %s", forbidden, raw)
		}
	}

	restored := Decode(ctx, Config{}, state)
	if !reflect.DeepEqual(restored.Encode(), state) {
		t.Fatalf("round trip\n got: %#v\nwant: %#v", restored.Encode(), state)
	}
	if !restored.Hidden(panelayout.Resource) {
		t.Fatal("hidden resource was not restored")
	}
	doc := paneForKind(restored, panelayout.Document)
	if doc == nil || !doc.tabs[0].view.(*documentViewer).view.NeedsLoad() {
		t.Fatal("restored document was not armed")
	}
	if cmd := restored.SelectTab(doc.leafID, 0); cmd == nil {
		t.Fatal("selecting restored document did not start its deferred load")
	}
}

func TestDecodeCollapsesUnknownKindsAndInvalidTabs(t *testing.T) {
	ctx := testContext(t.TempDir())
	state := State{Version: 1, Root: &NodeState{
		Axis: "columns", Ratio: 50,
		A: &NodeState{Kind: "primary"},
		B: &NodeState{
			Axis: "rows", Ratio: 50,
			A: &NodeState{
				Kind: "hologram",
				Pane: &PaneState{Kind: "hologram", Tabs: []TabState{{Ref: fileRef("no.md")}}},
			},
			B: &NodeState{
				Kind: "document",
				Pane: &PaneState{Kind: "document", Tabs: []TabState{
					{Ref: fileRef("../outside.md")},
					{Ref: fileRef("README.md")},
					{Ref: fileRef("README.md")},
				}},
			},
		},
	}}
	d := Decode(ctx, Config{}, state)
	if got := leafIDs(d.root); len(got) != 2 {
		t.Fatalf("decoded leaves = %v root=%#v", got, d.root)
	}
	p := paneForKind(d, panelayout.Document)
	if p == nil || len(p.tabs) != 1 || p.tabs[0].ref.Value != "README.md" {
		t.Fatalf("decoded document tabs = %#v", p)
	}
}

func TestDeckDropsClosedAndForeignAsyncResults(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(root)
	d := New(ctx, Config{})
	opened := d.Open(ctx, fileRef("README.md"), testPlacement())
	if opened.Command == nil {
		t.Fatal("document Open returned no load")
	}
	result, ok := opened.Command().(Result)
	if !ok {
		t.Fatalf("load returned %T", opened.Command())
	}
	if _, ok := result.Payload.(docview.LoadedMsg); !ok {
		t.Fatalf("payload = %T", result.Payload)
	}

	foreign := *d
	foreign.SetContext(SurfaceContext{Root: root, Surface: "files", Epoch: ctx.Epoch + 1})
	if _, applied := foreign.Apply(result); applied {
		t.Fatal("foreign epoch accepted async result")
	}

	if !d.CloseActive() {
		t.Fatal("close failed")
	}
	reopened := d.Open(ctx, fileRef("README.md"), testPlacement())
	if reopened.TabID == opened.TabID {
		t.Fatal("closed tab identity was reused")
	}
	if _, applied := d.Apply(result); applied {
		t.Fatal("closed tab accepted stale async result")
	}
	current, ok := reopened.Command().(Result)
	if !ok {
		t.Fatalf("reopened load returned %T", reopened.Command())
	}
	if _, applied := d.Apply(current); !applied {
		t.Fatal("current async result was rejected")
	}
	encoded, err := json.Marshal(d.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "hello") {
		t.Fatalf("loaded document body crossed persistence boundary: %s", encoded)
	}
}

func TestDeckRejectsStaleViewerIdentityInsideCurrentEnvelope(t *testing.T) {
	ctx := testContext(t.TempDir())
	d := New(ctx, Config{})
	opened := d.Open(ctx, diffRef("wt"), testPlacement())
	p := paneForKind(d, panelayout.Diff)
	if p == nil || len(p.tabs) != 1 {
		t.Fatal("diff tab did not open")
	}
	tab := p.tabs[0]
	result := Result{
		ID: AsyncID{TabID: tab.id, Generation: tab.generation, Epoch: ctx.Epoch, Surface: ctx.Surface},
		Payload: workspacediff.SnapshotMsg{
			Epoch: ctx.Epoch, WorkspaceID: "another-surface", Identity: "wt",
		},
	}
	if opened.Command == nil || tab.generation == 0 {
		t.Fatal("diff tab did not establish an async generation")
	}
	if _, applied := d.Apply(result); applied {
		t.Fatal("current deck envelope accepted a stale viewer surface")
	}
}

func TestDeckReturnsActionsAndRefusesInvalidRefs(t *testing.T) {
	d := New(testContext(t.TempDir()), Config{})
	ctx := d.Context()
	place := testPlacement()
	before := d.Encode()
	for _, ref := range []contentlink.Ref{
		{Kind: contentlink.KindURL, Value: "https://example.com/path"},
		{Kind: contentlink.KindInternal, Namespace: "session", Value: "sidecar-sh-1"},
	} {
		if out := d.Open(ctx, ref, place); out.Status != StatusAction || !out.Accepted() {
			t.Fatalf("action Open(%#v) = %#v", ref, out)
		}
	}
	for _, ref := range []contentlink.Ref{
		{Kind: contentlink.Kind("future"), Value: "x"},
		fileRef("../outside"),
		issueRef("not-an-issue"),
		{Kind: contentlink.KindURL, Value: "file:///tmp/no"},
	} {
		if out := d.Open(ctx, ref, place); out.Status != StatusRefused || out.Accepted() {
			t.Fatalf("invalid Open(%#v) = %#v", ref, out)
		}
	}
	if got := d.Encode(); !reflect.DeepEqual(got, before) {
		t.Fatalf("actions/refusals mutated deck\n got=%#v\nwant=%#v", got, before)
	}
}
