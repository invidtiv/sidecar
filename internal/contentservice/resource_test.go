package contentservice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceprovider"
)

type fakeResProvider struct {
	instance string
	desc     resourceprovider.Description
	doc      resource.Document
	err      error
	resolves int
}

func (f *fakeResProvider) Instance() string { return f.instance }
func (f *fakeResProvider) Describe(context.Context) (resourceprovider.Description, error) {
	return f.desc, nil
}
func (f *fakeResProvider) Resolve(_ context.Context, _ resource.Reference) (resource.Document, error) {
	f.resolves++
	if f.err != nil {
		return resource.Document{}, f.err
	}
	return f.doc, nil
}

func testResourceService(t *testing.T, providers ...resourceprovider.Provider) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	root = canonical(root)
	initGitRepo(t, root)
	id := canonical(root) + ":worktree:" + canonical(root)
	mgr := resourceprovider.NewManager(resourceprovider.ManagerOptions{})
	mgr.SetProviders(providers, nil)
	svc := testService(t, root, nil, nil)
	svc.NewResourceManager = func() (*resourceprovider.Manager, error) { return mgr, nil }
	return svc, id
}

func TestFingerprintDescriptorsIsDeterministicAndIgnoresGeneration(t *testing.T) {
	t.Parallel()
	p := &fakeResProvider{
		instance: "jira-work",
		desc: resourceprovider.Description{
			Info:     resourceprovider.Info{Kind: "jira", Name: "Jira"},
			Matchers: []resourceprovider.Matcher{{ID: "issue-key", Pattern: `CASH-\d+`, Priority: 10}},
		},
	}
	svc, _ := testResourceService(t, p)
	first, err := svc.Describe(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !first.ValidRemoteResult() || first.NotModified || first.Fingerprint == "" {
		t.Fatalf("first describe = %+v", first)
	}
	if strings.Contains(first.Fingerprint, "generation") {
		t.Fatalf("fingerprint leaked generation: %q", first.Fingerprint)
	}
	again, err := svc.Describe(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if again.Fingerprint != first.Fingerprint {
		t.Fatalf("fingerprint drifted across describes: %q vs %q", first.Fingerprint, again.Fingerprint)
	}
	mgr, err := svc.resourceManager()
	if err != nil {
		t.Fatal(err)
	}
	if mgr.Snapshot().Generation() < 2 {
		t.Fatalf("generation = %d, want at least 2 so the pin is meaningful", mgr.Snapshot().Generation())
	}
	if again.Fingerprint == "v1:0" || again.Fingerprint == "0" {
		t.Fatal("fingerprint looked like a process-local generation")
	}
	empty := FingerprintDescriptors(nil)
	alsoEmpty := FingerprintDescriptors([]ProviderDescriptor{})
	if empty != alsoEmpty {
		t.Fatalf("nil and empty fingerprints differ: %q vs %q", empty, alsoEmpty)
	}
	if empty == first.Fingerprint {
		t.Fatal("populated descriptors fingerprinted as empty")
	}
}

func TestDescribeNotModifiedWhenIfRevisionMatches(t *testing.T) {
	t.Parallel()
	p := &fakeResProvider{
		instance: "jira-work",
		desc: resourceprovider.Description{
			Matchers: []resourceprovider.Matcher{{ID: "issue-key", Pattern: `CASH-\d+`}},
		},
	}
	svc, _ := testResourceService(t, p)
	first, err := svc.Describe(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	cached, err := svc.Describe(context.Background(), first.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.NotModified || cached.Fingerprint != first.Fingerprint || len(cached.Descriptors) != 0 {
		t.Fatalf("notModified = %+v", cached)
	}
	if !cached.ValidRemoteResult() {
		t.Fatal("notModified failed ValidRemoteResult")
	}
}

func TestDescribeEmptyProvidersIsAValidAnswer(t *testing.T) {
	t.Parallel()
	svc, _ := testResourceService(t)
	got, err := svc.Describe(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ValidRemoteResult() || got.Fingerprint == "" || got.Fingerprint != FingerprintDescriptors(nil) {
		t.Fatalf("empty describe = %+v", got)
	}
}

func TestValidRemoteResultDescribeRefusesALogLine(t *testing.T) {
	t.Parallel()
	var d DescribeResult
	if err := json.Unmarshal([]byte(`{"level":"info","msg":"loading nvm","path":"/usr/local/nvm"}`), &d); err != nil {
		t.Fatal(err)
	}
	if d.ValidRemoteResult() {
		t.Fatalf("log line passed for describe: %+v", d)
	}
	ok := DescribeResult{Fingerprint: "v1:abc", Descriptors: []ProviderDescriptor{}}
	if !ok.ValidRemoteResult() {
		t.Fatal("empty descriptors refused")
	}
}

func TestResolveAndReadResourceWire(t *testing.T) {
	t.Parallel()
	p := &fakeResProvider{
		instance: "jira-work",
		desc: resourceprovider.Description{
			Matchers: []resourceprovider.Matcher{{ID: "issue-key", Pattern: `CASH-\d+`}},
		},
		doc: resource.Document{
			Identity:  "CASH-1245",
			Title:     "Host ticket",
			SourceURL: "https://jira.example.test/browse/CASH-1245",
			FreshFor:  time.Minute,
			Body:      &resource.Body{Format: resource.FormatMarkdown, Text: "host body"},
		},
	}
	svc, id := testResourceService(t, p)
	ref := resource.Reference{Instance: "jira-work", Matcher: "issue-key", Locator: "CASH-1245"}
	resolved, err := svc.ResolveParams(context.Background(), ResolveParams{
		WorkspaceID: id, Kind: KindResource, Target: ref.Locator, Provider: ref.Instance, Matcher: ref.Matcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Provider != "jira-work" || resolved.Target != "CASH-1245" {
		t.Fatalf("resolve = %+v", resolved)
	}

	read, err := svc.ReadParams(context.Background(), ReadParams{
		WorkspaceID: id, Kind: KindResource, Operation: OpResource,
		Target: ref.Locator, Provider: ref.Instance, Matcher: ref.Matcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !read.ValidRemoteResult() || read.Resource == nil || read.Resource.Title != "Host ticket" {
		t.Fatalf("read = %+v", read)
	}
	if read.Resource.Body == nil || read.Resource.Body.Text != "host body" {
		t.Fatalf("wire body = %+v", read.Resource.Body)
	}
	raw, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pass") || strings.Contains(string(raw), "token") || strings.Contains(string(raw), "command") {
		t.Fatalf("wire leaked credentials or command: %s", raw)
	}

	typed := &resource.Error{Code: resource.CodeUnauthorized, Message: "need a token", Retryable: false, SetupHint: "export JIRA_TOKEN"}
	p.err = typed
	fail, err := svc.ReadResource(context.Background(), id, ref, true)
	if err != nil {
		t.Fatal(err)
	}
	if !fail.ValidRemoteResult() || fail.ResourceError == nil || fail.ResourceError.Code != string(resource.CodeUnauthorized) {
		t.Fatalf("typed error envelope = %+v", fail)
	}
}

func TestSanitizeWireDocumentRefusesUnsafeBody(t *testing.T) {
	t.Parallel()
	if _, err := SanitizeWireDocument(&resource.WireDocument{Title: "no identity"}); err == nil {
		t.Fatal("missing identity was adopted")
	}
	if _, err := SanitizeWireDocument(&resource.WireDocument{Identity: "x"}); err == nil {
		t.Fatal("missing title was adopted")
	}
	doc, err := SanitizeWireDocument(&resource.WireDocument{
		Identity: "CASH-1",
		Title:    "ok",
		Body:     &resource.WireBody{Text: "safe\x1b]8;;https://evil.test\x07text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Body != nil && strings.Contains(doc.Body.Text, "\x1b") {
		t.Fatalf("OSC survived sanitization: %q", doc.Body.Text)
	}
}

func TestTerminalMatchersFromCompilesHostDescriptors(t *testing.T) {
	t.Parallel()
	matchers, err := TerminalMatchersFrom([]ProviderDescriptor{{
		Instance: "jira-work",
		Order:    0,
		Matchers: []ResourceMatcherDTO{{ID: "issue-key", Pattern: `CASH-\d+`}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(matchers) != 1 || matchers[0].Provider != "jira-work" || matchers[0].Re == nil {
		t.Fatalf("matchers = %#v", matchers)
	}
	if matchers[0].Re.FindString("see CASH-1245") != "CASH-1245" {
		t.Fatal("compiled matcher did not claim the host locator")
	}
}

func TestValidRemoteResultResourceRefusesALogLine(t *testing.T) {
	t.Parallel()
	logLine := []byte(`{"level":"info","msg":"loading nvm","path":"/usr/local/nvm"}`)
	var read ReadResult
	if err := json.Unmarshal(logLine, &read); err != nil {
		t.Fatal(err)
	}
	if read.ValidRemoteResult() {
		t.Fatalf("log line passed for resource read: %+v", read)
	}
	ok := ReadResult{
		Kind: KindResource, Operation: OpResource, Workspace: "p:shell:s1",
		Revision: "v1:abc", Resource: &resource.WireDocument{Identity: "CASH-1", Title: "T"},
	}
	if !ok.ValidRemoteResult() {
		t.Fatal("real resource read refused")
	}
	okErr := ReadResult{
		Kind: KindResource, Operation: OpResource, Workspace: "p:shell:s1",
		Revision: "v1:abc", ResourceError: &resource.WireError{Code: "unauthorized"},
	}
	if !okErr.ValidRemoteResult() {
		t.Fatal("typed resource error refused")
	}
	okResolve := ResolveResult{Kind: KindResource, Workspace: "p:shell:s1", Target: "CASH-1", Provider: "jira-work", Matcher: "issue-key"}
	if !okResolve.ValidRemoteResult() {
		t.Fatal("resource resolve refused")
	}
}
