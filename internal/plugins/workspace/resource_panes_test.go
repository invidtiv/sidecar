package workspace

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/terminallink"
)

// resourceStub is a provider that never runs: matching is pure and resolving
// is a command, so a test can prove the whole journey without a process, a
// network, or a credential.
type resourceStub struct {
	calls int
	// last is the command the newest resolve returned. A test runs THAT
	// rather than the click's own command, which is a batch that also carries
	// a tmux resize no test may execute.
	last tea.Cmd
}

func (s *resourceStub) matchers() []terminallink.ResourceMatcher {
	return []terminallink.ResourceMatcher{{
		Provider: "jira-work",
		ID:       "issue-key",
		Re:       regexp.MustCompile(`\bCASH-[1-9][0-9]*\b`),
	}}
}

func (s *resourceStub) resolve(modelID int, generation, epoch uint64, ref resource.Reference, _ bool) tea.Cmd {
	s.calls++
	s.last = func() tea.Msg {
		return resourceview.ResolvedMsg{
			ModelID:    modelID,
			Generation: generation,
			Epoch:      17,
			Ref:        ref,
			Document: resource.Document{
				Identity:  ref.Locator,
				Title:     "Refund totals differ after partial capture",
				Body:      &resource.Body{Format: resource.FormatMarkdown, Text: "Ticket description."},
				SourceURL: "https://jira.example.test/browse/" + ref.Locator,
			},
		}
	}
	return s.last
}

// result runs the newest resolve and types its answer.
func (s *resourceStub) result(t *testing.T) resourceview.ResolvedMsg {
	t.Helper()
	if s.last == nil {
		t.Fatal("no resolve was started")
	}
	msg, ok := s.last().(resourceview.ResolvedMsg)
	if !ok {
		t.Fatalf("resolve answered %T, want a resolve result", s.last())
	}
	return msg
}

// resourceTestPlugin is the steel thread's starting point: a shell selected, a
// terminal leaf, and one ready provider.
func resourceTestPlugin(t *testing.T) (*Plugin, *resourceStub, string) {
	t.Helper()
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	stub := &resourceStub{}
	p.SetResourceMatchers(stub.matchers())
	p.SetResourceResolver(stub.resolve)
	return p, stub, filepath.Clean(resolved)
}

// clickResourceKey is the click journey end to end: scan the line the terminal
// is showing, then activate the span the scan produced. Nothing is hand-built,
// so a span that would never be underlined cannot be clicked here either.
func clickResourceKey(t *testing.T, p *Plugin, line string) tea.Cmd {
	t.Helper()
	context := p.terminalLinkSurfaceContext(false)
	if !context.ok {
		t.Fatal("the selected terminal surface has no link context")
	}
	for _, link := range p.resolvedTerminalLinks(context, p.terminalOutputBuffer(false), line) {
		if link.Kind != terminallink.KindResource {
			continue
		}
		cmd, ok := p.activateResolvedTerminalLink(link, context, false)
		if !ok {
			t.Fatalf("the resource link in %q refused activation", line)
		}
		return cmd
	}
	t.Fatalf("no resource link in %q", line)
	return nil
}

func resourceLocators(t *testing.T, res *resourcePane) []string {
	t.Helper()
	var out []string
	for _, ref := range res.tabs.References() {
		out = append(out, ref.Locator)
	}
	return out
}

func TestResourceKeyClickOpensAResourceLeaf(t *testing.T) {
	p, stub, _ := resourceTestPlugin(t)

	cmd := clickResourceKey(t, p, "agent: see CASH-1245 for the failing capture")
	if cmd == nil {
		t.Fatal("the click scheduled no resolve")
	}
	res, leaf := p.activeResourcePane()
	if res == nil || leaf == nil {
		t.Fatal("the click opened no Resource leaf")
	}
	if got := resourceLocators(t, res); len(got) != 1 || got[0] != "CASH-1245" {
		t.Fatalf("tabs = %v, want one CASH-1245", got)
	}
	if p.paneFocus != leaf.ID || p.activePane != PanePreview {
		t.Fatalf("focus = pane %d/%v, want the new Resource leaf", p.paneFocus, p.activePane)
	}
	if stub.calls != 1 {
		t.Fatalf("resolver called %d times, want exactly one for one click", stub.calls)
	}
	if ref := res.tabs.Active().Reference(); ref.Instance != "jira-work" || ref.Matcher != "issue-key" {
		t.Fatalf("reference = %#v, want the matcher's provider and ID", ref)
	}
}

func TestASecondLocatorAppendsATabAndADuplicateFocusesOne(t *testing.T) {
	p, stub, _ := resourceTestPlugin(t)

	clickResourceKey(t, p, "CASH-1245 is blocked by CASH-1246")
	res, _ := p.activeResourcePane()
	if got := resourceLocators(t, res); len(got) != 1 {
		t.Fatalf("tabs after the first click = %v, want one", got)
	}
	clickResourceKey(t, p, "CASH-1246 landed")
	if got := resourceLocators(t, res); len(got) != 2 || got[1] != "CASH-1246" {
		t.Fatalf("tabs = %v, want CASH-1245 then CASH-1246", got)
	}
	if res.tabs.ActiveIndex() != 1 {
		t.Fatalf("active tab = %d, want the one just clicked", res.tabs.ActiveIndex())
	}

	before := stub.calls
	clickResourceKey(t, p, "back to CASH-1245")
	if got := resourceLocators(t, res); len(got) != 2 {
		t.Fatalf("tabs = %v, want the duplicate focused rather than appended", got)
	}
	if res.tabs.ActiveIndex() != 0 {
		t.Fatalf("active tab = %d, want the existing CASH-1245", res.tabs.ActiveIndex())
	}
	if stub.calls != before {
		t.Fatalf("resolver called again on a resolved tab (%d → %d)", before, stub.calls)
	}
	if _, second := p.activeResourcePane(); second == nil {
		t.Fatal("the second click split the tree again")
	}
	if count := len(p.resources); count != 1 {
		t.Fatalf("%d Resource leaves, want one for every provider and locator", count)
	}
}

// A provider that has not described itself contributes no matcher, and a key
// with no matcher is ordinary text: no span, no underline, no click target.
func TestAnUnreadyProviderLeavesTheKeyAsPlainText(t *testing.T) {
	p, _, _ := resourceTestPlugin(t)
	p.SetResourceMatchers(nil)

	const line = "agent: see CASH-1245 for the failing capture"
	context := p.terminalLinkSurfaceContext(false)
	if !context.ok {
		t.Fatal("the selected terminal surface has no link context")
	}
	for _, link := range p.resolvedTerminalLinks(context, p.terminalOutputBuffer(false), line) {
		if link.Kind == terminallink.KindResource {
			t.Fatalf("an unready provider still produced %#v", link)
		}
	}
	if got := decorateTerminalLinks(line, p.terminalLinkResolver(false, p.terminalOutputBuffer(false))); got != line {
		t.Fatalf("decorated = %q, want the line unchanged", got)
	}
}

// The one thing that may leave the process is the reference. A resolved
// document's title, body and URL are in memory and must stay there.
func TestPersistenceRoundTripsReferencesOnly(t *testing.T) {
	p, stub, resolved := resourceTestPlugin(t)

	clickResourceKey(t, p, "CASH-1245 and CASH-1246 both regressed")
	p.applyResourceResolved(stub.result(t))
	res, _ := p.activeResourcePane()
	res.tabs.SetSize(40, 3)
	if !res.pane.Scroll(2) {
		t.Fatal("the card did not scroll, so there is no offset to persist")
	}
	clickResourceKey(t, p, "CASH-1246 landed")

	layout := p.persistedPaneLayout()
	saved := firstLayoutLeafOfKind(layout, contentKindResource)
	if saved == nil {
		t.Fatalf("no resource leaf persisted: %#v", layout)
	}
	if len(saved.ResourceTabs) != 2 {
		t.Fatalf("persisted tabs = %#v, want both references", saved.ResourceTabs)
	}
	first := saved.ResourceTabs[0]
	if first.Provider != "jira-work" || first.Matcher != "issue-key" || first.Locator != "CASH-1245" || first.Scroll != 2 {
		t.Fatalf("persisted reference = %#v, want provider/matcher/locator", first)
	}
	if saved.Active != 1 {
		t.Fatalf("persisted active = %d, want the second tab", saved.Active)
	}
	encoded, err := json.Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"Refund totals", "Ticket description", "jira.example.test"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("resolved document content reached disk: %s", encoded)
		}
	}

	// The reference alone has to be enough to put the pane back.
	restored := docPaneTestPlugin(t, resolved, true)
	restored.SetResourceResolver((&resourceStub{}).resolve)
	restored.restorePaneLayout(layout)
	back, _ := restored.activeResourcePane()
	if back == nil {
		t.Fatal("the Resource leaf did not come back")
	}
	if got := resourceLocators(t, back); len(got) != 2 || got[0] != "CASH-1245" || got[1] != "CASH-1246" {
		t.Fatalf("restored tabs = %v, want both references in order", got)
	}
	if back.root != resolved || back.surface != "shell:test-shell" {
		t.Fatalf("restored leaf surface = %q %q, want the selected terminal's", back.root, back.surface)
	}
}

// Relaunch must not fan out one provider process per remembered tab. Every
// restored tab is armed; selecting one is what turns it into a request.
func TestRestoredResourceTabsAreArmedNotResolved(t *testing.T) {
	p, stub, resolved := resourceTestPlugin(t)
	layout := &state.PaneLayoutJSON{
		Root: resolved, Surface: "shell:test-shell", Open: true,
		Split: &state.PaneSplitJSON{Axis: "cols", Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
			B: &state.PaneLayoutJSON{Kind: contentKindResource, Active: 1, ResourceTabs: []state.PaneResourceTabJSON{
				{Provider: "jira-work", Matcher: "issue-key", Locator: "CASH-1245", Scroll: 3},
				{Provider: "jira-work", Matcher: "issue-key", Locator: "CASH-1246"},
			}},
		},
	}
	if cmd := p.restorePaneLayout(layout); cmd != nil {
		t.Fatal("restore scheduled work; a remembered reference must wait to be selected")
	}
	if stub.calls != 0 {
		t.Fatalf("restore called the resolver %d times, want none", stub.calls)
	}
	res, _ := p.activeResourcePane()
	if res == nil {
		t.Fatal("the Resource leaf was not restored")
	}
	for i, m := range res.tabs.All() {
		if m.State() != resourceview.StateArmed {
			t.Fatalf("tab %d state = %v, want armed", i, m.State())
		}
	}
	if res.tabs.ActiveIndex() != 1 {
		t.Fatalf("active tab = %d, want the persisted one", res.tabs.ActiveIndex())
	}

	// Selecting is the request, and only for the tab selected.
	if cmd := p.selectResourceTab(res, 0); cmd == nil {
		t.Fatal("selecting an armed tab did not resolve it")
	}
	if stub.calls != 1 {
		t.Fatalf("resolver called %d times, want one for the selected tab", stub.calls)
	}
	if res.tabs.At(1).State() != resourceview.StateArmed {
		t.Fatal("selecting one tab resolved another")
	}
}

// A provider that is not wired up at all must produce a card that says so
// rather than a tab that spins forever.
func TestAMissingResolverBecomesATypedError(t *testing.T) {
	p, _, _ := resourceTestPlugin(t)
	p.SetResourceResolver(nil)

	clickResourceKey(t, p, "CASH-1245 is blocked")
	res, _ := p.activeResourcePane()
	if res == nil {
		t.Fatal("the click opened no Resource leaf")
	}
	if got := res.tabs.Active().State(); got != resourceview.StateError {
		t.Fatalf("state = %v, want a typed error", got)
	}
}

// Closing the last tab is the only thing that may drop a reference, and it
// takes the leaf with it.
func TestClosingTheLastResourceTabCollapsesTheLeaf(t *testing.T) {
	p, _, _ := resourceTestPlugin(t)
	clickResourceKey(t, p, "CASH-1245 is blocked")
	if p.closeActiveResourceTab() == nil {
		t.Fatal("closing the last tab did not resize the terminal beside it")
	}
	if res, _ := p.activeResourcePane(); res != nil {
		t.Fatal("the Resource leaf outlived its last tab")
	}
	if len(p.resources) != 0 {
		t.Fatalf("%d Resource panes left behind", len(p.resources))
	}
}

// The underline is the whole visible half of the feature: without it there is
// nothing to click. The negative case above was tested and the positive one
// was not, which is how a decoration path that never sees the matchers got
// this far.
func TestAReadyProviderUnderlinesTheKey(t *testing.T) {
	p, _, _ := resourceTestPlugin(t)

	const line = "agent: see CASH-1245 for the failing capture"
	context := p.terminalLinkSurfaceContext(false)
	if !context.ok {
		t.Fatal("the selected terminal surface has no link context")
	}

	found := false
	for _, link := range p.resolvedTerminalLinks(context, p.terminalOutputBuffer(false), line) {
		if link.Kind == terminallink.KindResource {
			found = true
		}
	}
	if !found {
		t.Error("a ready provider produced no resource link")
	}

	got := decorateTerminalLinks(line, p.terminalLinkResolver(false, p.terminalOutputBuffer(false)))
	if got == line {
		t.Fatalf("decorated line is unchanged, so the key was never underlined: %q", got)
	}
	if !strings.Contains(got, "\x1b[4m") {
		t.Errorf("decorated = %q, want an underline around the key", got)
	}
}
