package resourceview

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/resource"
)

func ref(locator string) resource.Reference {
	return resource.Reference{Instance: "jira-work", Matcher: "issue-key", Locator: locator}
}

// recorder captures what the view asked the host to resolve, and lets a test
// answer on its own schedule so stale-result ordering can be exercised.
type recorder struct {
	calls []struct {
		modelID    int
		generation uint64
		ref        resource.Reference
		refresh    bool
	}
}

func (r *recorder) resolver() Resolver {
	return func(modelID int, generation, epoch uint64, rf resource.Reference, refresh bool) tea.Cmd {
		r.calls = append(r.calls, struct {
			modelID    int
			generation uint64
			ref        resource.Reference
			refresh    bool
		}{modelID, generation, rf, refresh})
		// A resolver hands back the work that will answer. This one answers on
		// the test's own schedule through Apply, but it still returns a command,
		// because a resolver that starts nothing means "no provider is wired up
		// yet" and the view deliberately treats it as such.
		return func() tea.Msg { return nil }
	}
}

func (r *recorder) last() (modelID int, generation uint64, refresh bool) {
	c := r.calls[len(r.calls)-1]
	return c.modelID, c.generation, c.refresh
}

func doc(identity, title string) resource.Document {
	return resource.Document{Identity: identity, Title: title}
}

func TestLoadRequestsAndShowsLoading(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(40, 10)
	m.Load(1, ref("CASH-1"), 0)

	if m.State() != StateLoading {
		t.Fatalf("state = %v, want loading", m.State())
	}
	if len(rec.calls) != 1 {
		t.Fatalf("want exactly 1 resolve request, got %d", len(rec.calls))
	}
	if _, _, refresh := rec.last(); refresh {
		t.Error("a first load must not ask for a freshness bypass")
	}
}

func TestApplyLandsDocument(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(40, 10)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()

	if !m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: doc("CASH-1", "A title")}) {
		t.Fatal("Apply rejected the answer to its own request")
	}
	if m.State() != StateReady {
		t.Fatalf("state = %v, want ready", m.State())
	}
	got, ok := m.Document()
	if !ok || got.Title != "A title" {
		t.Errorf("document = %+v, ok=%v", got, ok)
	}
}

func TestStaleResultIsDiscarded(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(40, 10)
	m.Load(1, ref("CASH-1"), 0)
	staleID, staleGen, _ := rec.last()

	// The user clicks a different key into the same tab before the first
	// answer arrives.
	m.Load(1, ref("CASH-2"), 0)

	if m.Apply(ResolvedMsg{ModelID: staleID, Generation: staleGen, Document: doc("CASH-1", "Old")}) {
		t.Fatal("a superseded answer must not be applied")
	}
	if _, ok := m.Document(); ok {
		t.Error("the retargeted tab must still be empty")
	}
	if m.Reference().Locator != "CASH-2" {
		t.Errorf("locator = %q, want CASH-2", m.Reference().Locator)
	}
}

func TestResultForAnotherModelIsDiscarded(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.Load(7, ref("CASH-1"), 0)
	_, gen, _ := rec.last()

	if m.Apply(ResolvedMsg{ModelID: 999, Generation: gen, Document: doc("CASH-1", "x")}) {
		t.Fatal("an answer addressed to another tab must not be applied")
	}
}

func TestTypedErrorBecomesErrorCard(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(60, 20)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()

	m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Err: &resource.Error{
		Code: resource.CodeUnauthorized, Message: "credentials expired", SetupHint: "run doctor",
	}})

	if m.State() != StateError {
		t.Fatalf("state = %v, want error", m.State())
	}
	view := m.View()
	for _, want := range []string{"Not authorized", "credentials expired", "run doctor"} {
		if !strings.Contains(view, want) {
			t.Errorf("error card missing %q:\n%s", want, view)
		}
	}
}

func TestUntypedErrorBecomesInternal(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(40, 10)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()

	m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Err: errors.New("pipe broke")})
	if got := m.Err(); got == nil || got.Code != resource.CodeInternal {
		t.Fatalf("error = %+v, want an internal typed error", got)
	}
}

func TestFailedRefreshKeepsTheLastGoodDocument(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(60, 20)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()
	m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: doc("CASH-1", "Still here")})

	m.Refresh()
	id, gen, refresh := rec.last()
	if !refresh {
		t.Error("Refresh must ask for a freshness bypass")
	}
	m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Refresh: true,
		Err: &resource.Error{Code: resource.CodeUnavailable, Message: "down"}})

	if m.State() != StateReady {
		t.Fatalf("state = %v, want the document to survive a failed refresh", m.State())
	}
	view := m.View()
	if !strings.Contains(view, "Still here") {
		t.Errorf("the last good document should still be visible:\n%s", view)
	}
	if !strings.Contains(view, "Refresh failed") {
		t.Errorf("the failure should still be reported:\n%s", view)
	}
}

func TestInitialFailureAfterNoDocumentShowsErrorCard(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(60, 20)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()
	m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Refresh: true,
		Err: &resource.Error{Code: resource.CodeNotFound}})
	if m.State() != StateError {
		t.Fatalf("state = %v, want error when there is no document to keep", m.State())
	}
}

func TestArmedTabDoesNotResolveUntilAsked(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(40, 10)
	m.Arm(1, ref("CASH-1"), 0)

	if len(rec.calls) != 0 {
		t.Fatalf("arming must start no resolve, got %d", len(rec.calls))
	}
	if m.State() != StateArmed {
		t.Fatalf("state = %v, want armed", m.State())
	}
	m.Resolve()
	if len(rec.calls) != 1 {
		t.Fatalf("Resolve should start exactly one request, got %d", len(rec.calls))
	}
	// A second Resolve on a now-loading tab must not double-request.
	m.Resolve()
	if len(rec.calls) != 1 {
		t.Fatalf("Resolve on a loading tab must not re-request, got %d", len(rec.calls))
	}
}

// A request nobody can answer must leave the tab where the user can get an
// answer later. Before the app publishes a resolver there is no provider to
// ask, and that is the state the armed card already describes — so asking early
// costs nothing and the tab is still armed when readiness arrives.
func TestAskingBeforeAProviderExistsLeavesTheTabArmed(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(40, 10)
	m.Arm(1, ref("CASH-1"), 0)

	if cmd := m.Resolve(); cmd != nil {
		t.Fatal("a tab with no resolver started work anyway")
	}
	if m.State() != StateArmed {
		t.Fatalf("state = %v, want armed so readiness can still resolve it", m.State())
	}
	if !strings.Contains(m.View(), "Waiting for jira-work") {
		t.Errorf("card should say what it is waiting for:\n%s", m.View())
	}

	// Readiness arrives: the same tab resolves without the user touching it.
	rec := &recorder{}
	m.SetResolver(rec.resolver())
	if cmd := m.Resolve(); cmd == nil {
		t.Fatal("a resolver arriving did not let the armed tab resolve")
	}
	if len(rec.calls) != 1 {
		t.Fatalf("resolver called %d times, want one", len(rec.calls))
	}
	if m.State() != StateLoading {
		t.Fatalf("state = %v, want loading", m.State())
	}
}

// The same rule for a resolver that exists but starts nothing, which is how a
// host says "not yet" without inventing a failure. The tab must not spin.
func TestAResolverThatStartsNoWorkDoesNotLeaveTheTabLoading(t *testing.T) {
	m := New(nil, func(int, uint64, uint64, resource.Reference, bool) tea.Cmd { return nil })
	m.SetSize(40, 10)
	m.Arm(1, ref("CASH-1"), 0)
	m.Resolve()
	if m.State() != StateArmed {
		t.Fatalf("state = %v, want armed rather than a card that spins forever", m.State())
	}
}

// A refresh nobody will answer keeps the document the user is reading, exactly
// as a failed refresh does.
func TestRefreshWithoutAProviderKeepsTheDocument(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(40, 10)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()
	m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: doc("CASH-1", "A title")})

	m.SetResolver(nil)
	m.Refresh()
	if m.State() != StateReady {
		t.Fatalf("state = %v, want the document kept", m.State())
	}
	if m.Refreshing() {
		t.Error("the card still claims a refresh is in flight")
	}
	if !strings.Contains(m.View(), "A title") {
		t.Errorf("the document the user was reading is gone:\n%s", m.View())
	}
}

func TestArmedCardSaysItIsRemembered(t *testing.T) {
	m := New(nil, (&recorder{}).resolver())
	m.SetSize(60, 10)
	m.Arm(1, ref("CASH-1"), 0)
	view := m.View()
	if !strings.Contains(view, "CASH-1") {
		t.Errorf("armed card should name the locator:\n%s", view)
	}
	if !strings.Contains(view, "remembered") {
		t.Errorf("armed card should say the tab survives:\n%s", view)
	}
}

func TestRestoredScrollAppliesWhenTheDocumentArrives(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(20, 3)
	m.Arm(1, ref("CASH-1"), 0)
	m.SetPendingScroll(2)
	m.Resolve()
	id, gen, _ := rec.last()

	body := &resource.Body{Format: resource.FormatText, Text: strings.Repeat("line\n", 40)}
	m.Apply(ResolvedMsg{ModelID: id, Generation: gen,
		Document: resource.Document{Identity: "CASH-1", Title: "t", Body: body}})

	if m.Scroll() != 2 {
		t.Errorf("scroll = %d, want the restored 2", m.Scroll())
	}
}

func TestViewIsAlwaysExactlyItsBox(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	for _, size := range [][2]int{{80, 24}, {20, 4}, {10, 2}, {1, 1}} {
		m.SetSize(size[0], size[1])
		m.Load(1, ref("CASH-1"), 0)
		id, gen, _ := rec.last()
		m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: resource.Document{
			Identity: "CASH-1",
			Title:    strings.Repeat("a very long title ", 20),
			Fields: []resource.Field{
				{Label: strings.Repeat("L", 40), Value: strings.Repeat("V", 200)},
			},
			Body:      &resource.Body{Format: resource.FormatMarkdown, Text: strings.Repeat("word ", 300)},
			SourceURL: "https://example.test/x",
		}})
		got := m.View()
		lines := strings.Split(got, "\n")
		if len(lines) != size[1] {
			t.Errorf("box %dx%d: got %d rows, want %d", size[0], size[1], len(lines), size[1])
		}
	}
}

func TestScrollClampsToContent(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(40, 5)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()
	m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: resource.Document{
		Identity: "CASH-1", Title: "t",
		Body: &resource.Body{Format: resource.FormatText, Text: strings.Repeat("x\n", 100)},
	}})

	m.ScrollTo(10_000)
	if m.Scroll() == 0 || m.Scroll() > 100 {
		t.Errorf("scroll = %d, want it clamped to the content", m.Scroll())
	}
	m.ScrollTo(-5)
	if m.Scroll() != 0 {
		t.Errorf("scroll = %d, want 0", m.Scroll())
	}
}

func TestInvalidReferenceFailsWithoutCallingTheResolver(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(40, 10)
	m.Load(1, resource.Reference{Instance: "", Matcher: "m", Locator: "L"}, 0)
	if len(rec.calls) != 0 {
		t.Fatalf("an invalid reference must not reach the host, got %d calls", len(rec.calls))
	}
	if m.State() != StateError {
		t.Fatalf("state = %v, want error", m.State())
	}
}

func TestBodyLinksAreNeverActivatable(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(60, 30)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()
	m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: resource.Document{
		Identity: "CASH-1", Title: "t",
		Body: &resource.Body{Format: resource.FormatMarkdown,
			Text: "see [the runbook](https://evil.test/x) and <b>html</b>"},
	}})
	view := m.View()
	if strings.Contains(view, "\x1b]8;;") {
		t.Errorf("a provider body must not be able to synthesize a hyperlink:\n%q", view)
	}
	if strings.Contains(view, "evil.test") {
		t.Errorf("a link destination must not survive into the rendered body:\n%q", view)
	}
}

func docWithURL(identity, url string) resource.Document {
	return resource.Document{Identity: identity, Title: identity, SourceURL: url}
}

func longDoc(identity string) resource.Document {
	return resource.Document{
		Identity: identity, Title: identity,
		Body: &resource.Body{Format: resource.FormatText, Text: strings.Repeat("line\n", 200)},
	}
}
