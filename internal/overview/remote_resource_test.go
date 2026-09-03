package overview

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"errors"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/pluginhost"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/tty"
)

const hostResourceTitle = "HOST-TICKET-CASH-1245"

type fakeRemoteResourceSource struct {
	mu            sync.Mutex
	file          fakeRemoteFileSource
	descriptors   []contentservice.ProviderDescriptor
	describeCalls int
	lastIfRev     string
	notModified   bool
	doc           resource.Document
	resolveErr    error
	resolves      int
	lastRefresh   bool
	lastRef       resource.Reference
}

func (f *fakeRemoteResourceSource) Resolve(ctx context.Context, src contentpanes.SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	return f.file.Resolve(ctx, src, pending)
}
func (f *fakeRemoteResourceSource) LoadDocument(ctx context.Context, src contentpanes.SourceContext, req contentpanes.DocumentReadRequest) (contentpanes.DocumentReadResult, error) {
	return f.file.LoadDocument(ctx, src, req)
}
func (f *fakeRemoteResourceSource) LoadIssue(context.Context, contentpanes.SourceContext, contentpanes.IssueReadRequest) (contentpanes.IssueReadResult, error) {
	return contentpanes.IssueReadResult{}, nil
}
func (f *fakeRemoteResourceSource) LoadNote(context.Context, contentpanes.SourceContext, contentpanes.NoteReadRequest) (contentpanes.NoteReadResult, error) {
	return contentpanes.NoteReadResult{}, nil
}
func (f *fakeRemoteResourceSource) LoadDiff(context.Context, contentpanes.SourceContext, contentpanes.DiffReadRequest) (contentpanes.DiffReadResult, error) {
	return contentpanes.DiffReadResult{}, nil
}
func (f *fakeRemoteResourceSource) Describe(_ context.Context, ifRevision string) (contentservice.DescribeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.describeCalls++
	f.lastIfRev = ifRevision
	fp := contentservice.FingerprintDescriptors(f.descriptors)
	if f.notModified || (ifRevision != "" && ifRevision == fp) {
		return contentservice.DescribeResult{Fingerprint: fp, NotModified: true}, nil
	}
	return contentservice.DescribeResult{Fingerprint: fp, Descriptors: f.descriptors}, nil
}
func (f *fakeRemoteResourceSource) ResolveResource(_ context.Context, _ contentpanes.SourceContext, ref resource.Reference, refresh bool) (resource.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves++
	f.lastRefresh = refresh
	f.lastRef = ref
	if f.resolveErr != nil {
		return resource.Document{}, f.resolveErr
	}
	doc := f.doc
	if doc.Identity == "" && doc.Title == "" && f.resolveErr == nil {
		doc = resource.Document{Identity: ref.Locator, Title: hostResourceTitle, FreshFor: time.Hour, Body: &resource.Body{Text: "host body"}}
	}
	return doc, nil
}

func (f *fakeRemoteResourceSource) stats() (describes, resolves int, lastIf string, lastRefresh bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.describeCalls, f.resolves, f.lastIfRev, f.lastRefresh
}

func hostJiraDescriptors() []contentservice.ProviderDescriptor {
	return []contentservice.ProviderDescriptor{{
		Instance: "jira-work", Order: 0,
		Matchers: []contentservice.ResourceMatcherDTO{{ID: "project-key", Pattern: `CASH-\d+`}},
	}}
}

func showingRemoteResourceModel(t *testing.T) (*Model, *fakeRemoteResourceSource) {
	t.Helper()
	src := &fakeRemoteResourceSource{
		descriptors: hostJiraDescriptors(),
		file:        fakeRemoteFileSource{body: remoteMarker + "\n" + resourceLine + "\n", revision: "v1:1"},
		doc: resource.Document{
			Identity:  "CASH-1245",
			Title:     hostResourceTitle,
			FreshFor:  time.Hour,
			SourceURL: "https://jira.example.test/browse/CASH-1245",
			Body:      &resource.Body{Text: "host body"},
		},
	}
	m, _, _ := showingRemoteTwinModel(t, nil)
	m.contentSource = src
	putPreviewLine(t, m, resourceLine)
	return m, src
}

func putPreviewLine(t *testing.T, m *Model, line string) {
	t.Helper()
	leaf := m.previewTerminalLeaf()
	if leaf.Buffer == nil {
		leaf.Buffer = tty.NewOutputBuffer(200)
	}
	if buf := m.previewBuffer(); buf != nil {
		buf.Update(line + "\n")
	}
}

func runRemoteDescribe(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.ensureRemoteResourceDescribe()
	if cmd == nil {
		t.Fatal("expected an immediate remote describe")
	}
	run(t, m, cmd)
}

func resourceSpansOn(t *testing.T, m *Model, line string) []string {
	t.Helper()
	m.PrepareTerminalLinks()
	deliverPreviewLinkResults(t, m, m.terminalLinks.TakeCmd())
	m.PrepareTerminalLinks()
	var keys []string
	for _, span := range m.previewTerminalLeaf().LinkState.Spans(line, 0) {
		if span.Kind == terminallink.KindResource {
			keys = append(keys, span.Value)
		}
	}
	return keys
}

func TestRemoteLocalOnlyProviderNeverClaimsHostText(t *testing.T) {
	m, fake, _ := showingRemoteTwinModel(t, nil)
	resolver := &fakeResolver{}
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver(resolver.resolve)
	putPreviewLine(t, m, resourceLine)

	if keys := resourceSpansOn(t, m, resourceLine); len(keys) != 0 {
		t.Fatalf("local-only provider claimed remote text: %v", keys)
	}
	cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
		Kind: targetactivation.PlanOpenResource, Provider: "jira-work", Matcher: "project-key", Locator: "CASH-1245",
	})
	run(t, m, cmd)
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("remote row asked the local resolver: %v", refs)
	}
	if handled && m.preview.resource != nil {
		view := m.preview.resource.view()
		if view != nil {
			if doc, ok := view.Document(); ok && strings.Contains(doc.Title, "Ticket CASH") {
				t.Fatal("opened a local provider document on a remote row")
			}
		}
	}
	_ = fake
}

func TestRemoteOnlyProviderUnderlinesAndOpensHostDocument(t *testing.T) {
	m, src := showingRemoteResourceModel(t)
	resolver := &fakeResolver{}
	m.SetResourceMatchers(nil)
	m.SetResourceResolver(resolver.resolve)
	runRemoteDescribe(t, m)

	keys := resourceSpansOn(t, m, resourceLine)
	if len(keys) == 0 || keys[0] != "CASH-1245" {
		t.Fatalf("host provider did not claim CASH-1245: %v", keys)
	}

	m.WorkspacesView(previewWide, previewTall)
	cmd, claimed := m.activatePreviewLinkAt(previewNeedleAction(t, m, "CASH-1245"), false)
	if !claimed || cmd == nil {
		t.Fatalf("click claimed=%v cmd=%v", claimed, cmd != nil)
	}
	run(t, m, cmd)
	if m.preview.resource == nil || m.preview.resource.view() == nil {
		t.Fatal("click opened no Resource pane")
	}
	view := m.preview.resource.view()
	if view.State() != resourceview.StateReady {
		t.Fatalf("state = %v", view.State())
	}
	doc, ok := view.Document()
	if !ok || doc.Title != hostResourceTitle {
		t.Fatalf("document = %+v, want host title", doc)
	}
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("click asked the local resolver: %v", refs)
	}
	if _, resolves, _, _ := src.stats(); resolves == 0 {
		t.Fatal("did not resolve through the remote source")
	}
}

func TestLocalSessionsResourceStartsNoContentDescribeOrResolve(t *testing.T) {
	stub := &remoteRunnerStub{}
	stub.install(t)
	m := resourcePreviewModel(t)
	m.SetResourceMatchers(jiraMatchers())
	resolver := &fakeResolver{}
	m.SetResourceResolver(resolver.resolve)

	if keys := resourceSpansOn(t, m, resourceLine); len(keys) != 2 {
		t.Fatalf("local matchers = %v", keys)
	}
	if cmd := m.ensureRemoteResourceDescribe(); cmd != nil {
		t.Fatal("local row started content describe")
	}
	clickResourceKey(t, m, "CASH-1245")
	if m.preview.resource == nil {
		t.Fatal("local resource click opened no pane")
	}
	if len(stub.calls) != 0 {
		t.Fatalf("local row invoked sidecar: %v", stub.calls)
	}
}

func TestRemoteMissingContentReadV1LeavesResourceTextPlain(t *testing.T) {
	m, _ := remoteTwinSessionsModel(t)
	bindShowingRemoteHost(m, hostproto.VerbCapabilities{})
	resolver := &fakeResolver{}
	m.SetResourceMatchers(jiraMatchers())
	m.SetResourceResolver(resolver.resolve)
	putPreviewLine(t, m, resourceLine)
	stub := &remoteRunnerStub{}
	stub.install(t)

	if keys := resourceSpansOn(t, m, resourceLine); len(keys) != 0 {
		t.Fatalf("missing capability underlined resource text: %v", keys)
	}
	if cmd := m.ensureRemoteResourceDescribe(); cmd != nil {
		t.Fatal("missing ContentReadV1 started describe")
	}
	cmd, _ := m.activatePreviewPlan(targetactivation.Plan{
		Kind: targetactivation.PlanOpenResource, Provider: "jira-work", Matcher: "project-key", Locator: "CASH-1245",
	})
	toast, ok := toastFrom(t, cmd)
	if !ok || !strings.Contains(toast.Message, "Update Sidecar on mac-mini") {
		t.Fatalf("toast = %#v", toast)
	}
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("missing capability asked the local resolver: %v", refs)
	}
	if m.preview.resource != nil {
		t.Fatal("missing capability opened a Resource pane")
	}
	if len(stub.calls) != 0 {
		t.Fatalf("host without ContentReadV1 was invoked: %v", stub.calls)
	}
}

func TestRemoteResourceCacheHitRefreshBypassAndInvalidation(t *testing.T) {
	m, src := showingRemoteResourceModel(t)
	runRemoteDescribe(t, m)
	resourceSpansOn(t, m, resourceLine)
	m.WorkspacesView(previewWide, previewTall)
	cmd, claimed := m.activatePreviewLinkAt(previewNeedleAction(t, m, "CASH-1245"), false)
	if !claimed {
		t.Fatal("click was not claimed")
	}
	run(t, m, cmd)
	_, resolves, _, _ := src.stats()
	if resolves != 1 {
		t.Fatalf("first open resolves = %d", resolves)
	}

	ctx, ok := m.previewDeckContext()
	if !ok {
		t.Fatal("no deck context")
	}
	ref := resource.Reference{Instance: "jira-work", Matcher: "project-key", Locator: "CASH-1245"}
	run(t, m, m.remoteResourceResolveCmd(ctx, m.preview.workspaceID, m.preview.contentEpoch, 1, 1, ref, false))
	if _, got, _, _ := src.stats(); got != 1 {
		t.Fatalf("cache miss on ordinary resolve: resolves=%d", got)
	}

	run(t, m, m.remoteResourceResolveCmd(ctx, m.preview.workspaceID, m.preview.contentEpoch, 2, 2, ref, true))
	if _, got, _, lastRefresh := src.stats(); got != 2 || !lastRefresh {
		t.Fatalf("refresh did not bypass cache: resolves=%d refresh=%v", got, lastRefresh)
	}

	src.mu.Lock()
	src.descriptors = []contentservice.ProviderDescriptor{{
		Instance: "jira-work", Order: 0,
		Matchers: []contentservice.ResourceMatcherDTO{{ID: "project-key", Pattern: `HOST-\d+`}},
	}}
	src.mu.Unlock()
	m.markRemoteDescribeImmediate("mac-mini")
	runRemoteDescribe(t, m)
	run(t, m, m.remoteResourceResolveCmd(ctx, m.preview.workspaceID, m.preview.contentEpoch, 3, 3, ref, false))
	if _, got, _, _ := src.stats(); got != 3 {
		t.Fatalf("fingerprint change did not invalidate cache: resolves=%d", got)
	}

	m.hostIncarnations["mac-mini"] = 99
	ctx.Source.HostIncarnation = 99
	m.discardRemoteMatchers("mac-mini")
	run(t, m, m.remoteResourceResolveCmd(ctx, m.preview.workspaceID, m.preview.contentEpoch, 4, 4, ref, false))
	if _, got, _, _ := src.stats(); got != 4 {
		t.Fatalf("incarnation change did not invalidate cache: resolves=%d", got)
	}

	now := m.now()
	until := contentservice.DocumentFreshUntil(resource.Document{FreshFor: 10 * time.Second}, now)
	if !until.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("DocumentFreshUntil = %v, want %v", until, now.Add(10*time.Second))
	}
	key := m.remoteResourceCacheKey(ctx.Source, ref)
	m.remoteResources.mu.Lock()
	entry, ok := m.remoteResources.entries[key]
	if !ok {
		m.remoteResources.mu.Unlock()
		t.Fatal("expected a cache entry after resolve")
	}
	entry.expires = m.now().Add(-time.Second)
	m.remoteResources.entries[key] = entry
	m.remoteResources.mu.Unlock()
	run(t, m, m.remoteResourceResolveCmd(ctx, m.preview.workspaceID, m.preview.contentEpoch, 5, 5, ref, false))
	if _, got, _, _ := src.stats(); got != 5 {
		t.Fatalf("expired cache was reused: resolves=%d", got)
	}
}

func TestRemoteResourceDescribeCadenceSelectionAndHiddenSilence(t *testing.T) {
	if remoteResourceDescribeInterval < 2*time.Second || remoteResourceDescribeInterval > 30*time.Second {
		t.Fatalf("cadence = %s", remoteResourceDescribeInterval)
	}
	m, src := showingRemoteResourceModel(t)
	runRemoteDescribe(t, m)
	describes, _, lastIf, _ := src.stats()
	if describes != 1 || lastIf != "" {
		t.Fatalf("immediate describe calls=%d ifRev=%q", describes, lastIf)
	}

	m.markRemoteDescribeImmediate("mac-mini")
	runRemoteDescribe(t, m)
	describes, _, lastIf, _ = src.stats()
	if describes != 2 {
		t.Fatalf("selection describe calls=%d", describes)
	}
	if lastIf == "" {
		t.Fatal("second describe omitted if-revision")
	}
	m.remoteMatchers.mu.Lock()
	armed := m.remoteMatchers.tickArmed
	m.remoteMatchers.mu.Unlock()
	if !armed {
		if m.remoteResourceDescribeCmd() == nil {
			t.Fatal("visible remote row did not arm the describe cadence")
		}
	}

	src.mu.Lock()
	src.notModified = true
	src.mu.Unlock()
	m.markRemoteDescribeImmediate("mac-mini")
	runRemoteDescribe(t, m)
	if keys := resourceSpansOn(t, m, resourceLine); len(keys) == 0 {
		t.Fatal("notModified dropped host matchers")
	}

	if !m.workspaces.SelectID("a") {
		t.Fatal("could not select the local row")
	}
	run(t, m, m.previewSync())
	before, _, _, _ := src.stats()
	if cmd := m.ensureRemoteResourceDescribe(); cmd != nil {
		t.Fatal("hidden/local row started describe")
	}
	if after, _, _, _ := src.stats(); after != before {
		t.Fatalf("hidden row described: before=%d after=%d", before, after)
	}
}

func TestRemoteNestedResourceFromDocumentStaysOnHost(t *testing.T) {
	m, src := showingRemoteResourceModel(t)
	runRemoteDescribe(t, m)
	src.file.body = "see CASH-1245\n"
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "twin.txt"}, "Document"))
	cmd := m.activatePreviewDocLink(contentlink.Ref{
		Kind: contentlink.KindResource, Provider: "jira-work", Matcher: "project-key", Value: "CASH-1245",
	})
	if cmd == nil {
		t.Fatal("nested resource from a remote document was refused")
	}
	run(t, m, cmd)
	if m.preview.resource == nil || m.preview.resource.view() == nil {
		t.Fatal("nested resource opened no pane")
	}
	doc, ok := m.preview.resource.view().Document()
	if !ok || doc.Title != hostResourceTitle {
		t.Fatalf("nested resource = %+v", doc)
	}
}

func TestRemoteResourceViewerSanitizationRefusesUnsafeWire(t *testing.T) {
	m, src := showingRemoteResourceModel(t)
	src.doc = resource.Document{Title: "no identity"}
	runRemoteDescribe(t, m)
	resourceSpansOn(t, m, resourceLine)
	m.WorkspacesView(previewWide, previewTall)
	cmd, claimed := m.activatePreviewLinkAt(previewNeedleAction(t, m, "CASH-1245"), false)
	if !claimed || cmd == nil {
		t.Fatal("click was not claimed")
	}
	run(t, m, cmd)
	view := m.preview.resource.view()
	if view == nil {
		t.Fatal("no resource view")
	}
	if _, ok := view.Document(); ok {
		t.Fatal("unsafe wire body was adopted")
	}
	if view.Err() == nil && view.State() != resourceview.StateError && view.State() != resourceview.StateArmed {
		t.Fatalf("state = %v, want a refusal", view.State())
	}

	src.doc = resource.Document{
		Identity: "CASH-1245", Title: hostResourceTitle,
		Body:     &resource.Body{Text: "ok\x1b]8;;https://evil.test\x07text"},
		FreshFor: time.Hour,
	}
	run(t, m, m.preview.resource.pane.Refresh())
	doc, ok := view.Document()
	if !ok {
		t.Fatal("sanitized document was refused")
	}
	if doc.Body != nil && strings.Contains(doc.Body.Text, "\x1b") {
		t.Fatalf("OSC survived viewer sanitization: %q", doc.Body.Text)
	}
}

func TestRemoteResourceSourceURLOpensLocally(t *testing.T) {
	m, _ := showingRemoteResourceModel(t)
	runRemoteDescribe(t, m)
	resourceSpansOn(t, m, resourceLine)
	m.WorkspacesView(previewWide, previewTall)
	cmd, _ := m.activatePreviewLinkAt(previewNeedleAction(t, m, "CASH-1245"), false)
	run(t, m, cmd)
	open := m.preview.resource.pane.OpenSource()
	if open == nil {
		t.Fatal("validated source URL produced no open command")
	}
}

func TestRemoteResourceDescribeIntervalIsNamed(t *testing.T) {
	if remoteResourceDescribeInterval != 5*time.Second {
		t.Fatalf("cadence = %s", remoteResourceDescribeInterval)
	}
}

func TestRemoteResourceRefreshDoesNotUseLocalResolverWhenHostHidden(t *testing.T) {
	m, _ := showingRemoteResourceModel(t)
	resolver := &fakeResolver{}
	m.SetResourceResolver(resolver.resolve)
	runRemoteDescribe(t, m)
	resourceSpansOn(t, m, resourceLine)
	m.WorkspacesView(previewWide, previewTall)
	cmd, claimed := m.activatePreviewLinkAt(previewNeedleAction(t, m, "CASH-1245"), false)
	if !claimed || cmd == nil {
		t.Fatal("click was not claimed")
	}
	run(t, m, cmd)
	doc, ok := m.preview.resource.view().Document()
	if !ok || doc.Title != hostResourceTitle {
		t.Fatalf("setup document = %+v", doc)
	}
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("open asked local resolver: %v", refs)
	}

	delete(m.hostHealth, "mac-mini")
	run(t, m, m.preview.resource.pane.Refresh())
	if refs := resolver.refs(); len(refs) != 0 {
		t.Fatalf("hidden host asked the local resolver: %v", refs)
	}
	if d, ok := m.preview.resource.view().Document(); !ok || d.Title != hostResourceTitle {
		t.Fatalf("lost host document after host hid: %+v ok=%v", d, ok)
	}
	if d, ok := m.preview.resource.view().Document(); ok && strings.Contains(d.Title, "Ticket CASH") {
		t.Fatalf("adopted local document after host hid: %+v", d)
	}
}

// The plugin-collection twins of ResolveResource. This fake is about the file,
// issue, note or diff journeys; a collection read is not part of what it is
// pinning, so both refuse rather than pretend.
func (*fakeRemoteResourceSource) ListCollection(context.Context, contentpanes.SourceContext, string, contentservice.CollectionParams) (pluginhost.Page, error) {
	return pluginhost.Page{}, errors.New("collection listing is not part of this fixture")
}

func (*fakeRemoteResourceSource) GetCollectionItem(context.Context, contentpanes.SourceContext, string, string, string, bool) (resource.Document, error) {
	return resource.Document{}, errors.New("collection items are not part of this fixture")
}
