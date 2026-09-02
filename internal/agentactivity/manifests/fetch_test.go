package manifests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/config"
)

// The runtime catalog fetch, Phase 5. Every test here serves its catalog from
// an httptest server: nothing in this package's tests may reach herdr.dev, and
// the one test that proves the "off" setting reaches nothing at all depends on
// a real server being there to notice a request that should never arrive.

// remoteCursor is a served cursor manifest. It keeps upstream's
// `spinner_working` rule id so the Sidecar overlay, which replaces that rule
// with an RE2 rewrite, still merges onto it; the marker rule is what tells a
// verdict "this is the fetched file" rather than "this is the vendored one".
func remoteCursor(version string) string {
	return remoteManifest("cursor", version, "fetched cursor marker")
}

func remoteManifest(id, version, marker string) string {
	return fmt.Sprintf(`
id = %q
version = %q
min_engine_version = 1
updated_at = "2026-09-01T12:00:00Z"

[[rules]]
id = "spinner_working"
state = "working"
priority = 90
region = "bottom_non_empty_lines(8)"
visible_working = true
line_regex = ['^\s*[⠀-⣿]+\s+\w+ing\b']

[[rules]]
id = "fetched_marker_blocked"
state = "blocked"
priority = 400
region = "whole_recent"
visible_blocker = true
contains = [%q]
`, id, version, marker)
}

func catalogFor(entries ...[2]string) string {
	var b strings.Builder
	b.WriteString("schema_version = 1\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, "\n[[agents]]\nid = %q\npath = %q\n", entry[0], entry[1])
	}
	return b.String()
}

// catalogServer serves an index and the files it names, and fails the test on
// any request for something it was not given. requests counts what arrived.
type catalogServer struct {
	*httptest.Server
	requests int
}

func newCatalogServer(t *testing.T, files map[string]string) *catalogServer {
	t.Helper()
	cs := &catalogServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.requests++
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *catalogServer) indexURL() string { return cs.URL + "/index.toml" }

// remoteState points the state axis at a temp directory and clears the
// per-agent load cache, then restores both. Every fetch test goes through it,
// so no test can read or write the developer's real fetch cache.
func remoteState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.SetTestStateDir(dir)
	t.Cleanup(config.ResetTestStateDir)
	// A fetch reads a local override too, through Load, so the config axis is
	// pinned as well: a developer's real ~/.config/sidecar/agent-detection must
	// not decide what these tests see.
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
	resetLoadCache(t)
	want := filepath.Join(dir, RemoteDirName)
	if got := RemoteDir(); got != want {
		t.Fatalf("RemoteDir() = %q, want %q", got, want)
	}
	return want
}

func fetchNow(t *testing.T, url string) FetchResult {
	t.Helper()
	res, err := Fetch(context.Background(), FetchOptions{CatalogURL: url, Force: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return res
}

// TestFetchOffBuildsNoHTTPClientAndSendsNoRequest is the "off" half of the exit
// gate, and it is deliberately two assertions rather than one.
//
// A server that records no request only proves nothing arrived *there*. The
// client counter proves the decision was made before any client existed, which
// is the thing that would be wrong if the setting were ever consulted late: a
// built client with a resolved DNS name and an open transport is a network act
// whether or not a request is written to it. Both have to hold.
func TestFetchOffBuildsNoHTTPClientAndSendsNoRequest(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})

	before := HTTPClientsBuilt()
	for _, value := range []string{"", "off", "OFF"} {
		res, err := FetchFromConfig(context.Background(),
			config.DetectionConfig{RemoteManifests: value}, FetchOptions{})
		if err != nil {
			t.Fatalf("remoteManifests=%q returned an error: %v", value, err)
		}
		if !res.Skipped {
			t.Fatalf("remoteManifests=%q did not skip", value)
		}
	}
	if got := HTTPClientsBuilt(); got != before {
		t.Fatalf("HTTP clients built with fetching off: %d, want %d", got, before)
	}
	if server.requests != 0 {
		t.Fatalf("the catalog server received %d requests with fetching off", server.requests)
	}
	if _, err := os.Stat(FetchStatusPath()); err == nil {
		t.Fatal("fetching off still wrote a status file")
	}

	// And the cache is still untouched, so Load answers from the vendored tree.
	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if source.CachedRemoteVersion != "" {
		t.Fatalf("cached remote version = %q with fetching off", source.CachedRemoteVersion)
	}
}

// TestAnUnrecognisedSettingNeverFetches pins the safe direction: a value this
// package cannot resolve means off, not on.
func TestAnUnrecognisedSettingNeverFetches(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{"index.toml": catalogFor()})

	before := HTTPClientsBuilt()
	for _, value := range []string{"on", "true", "yes", "herdr", "herdr.dev/catalog", "ftp://herdr.dev/i.toml", "://"} {
		res, err := FetchFromConfig(context.Background(),
			config.DetectionConfig{RemoteManifests: value}, FetchOptions{})
		if err == nil {
			t.Fatalf("remoteManifests=%q was accepted", value)
		}
		if !res.Skipped {
			t.Fatalf("remoteManifests=%q did not skip", value)
		}
	}
	if got := HTTPClientsBuilt(); got != before {
		t.Fatalf("HTTP clients built for an unrecognised setting: %d, want %d", got, before)
	}
	if server.requests != 0 {
		t.Fatalf("the catalog server received %d requests for an unrecognised setting", server.requests)
	}
}

// TestAFetchedManifestNewerThanTheVendoredOneBecomesActive is the "on" half of
// the exit gate: the file is fetched, cached, loaded, and it is what classifies
// the screen, and explain says all of that.
func TestAFetchedManifestNewerThanTheVendoredOneBecomesActive(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})

	res, err := FetchFromConfig(context.Background(),
		config.DetectionConfig{RemoteManifests: server.indexURL()}, FetchOptions{Force: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Skipped {
		t.Fatalf("fetch skipped: %s", res.Reason)
	}
	if len(res.Updated) != 1 || res.Updated[0] != "cursor" {
		t.Fatalf("updated = %v, want [cursor]", res.Updated)
	}
	if got := res.Status.Agents["cursor"].LastResult; got != FetchResultUpdated {
		t.Fatalf("cursor result = %q, want %q", got, FetchResultUpdated)
	}

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindRemote {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindRemote)
	}
	if source.Version != "9999.01.01.1" {
		t.Fatalf("active version = %q, want the fetched one", source.Version)
	}
	if source.CachedRemoteVersion != "9999.01.01.1" {
		t.Fatalf("cached remote version = %q", source.CachedRemoteVersion)
	}
	if source.VendoredVersion == "" || source.VendoredVersion == source.Version {
		t.Fatalf("vendored version = %q, want the version in the binary", source.VendoredVersion)
	}
	if source.Diagnostic != "" {
		t.Fatalf("a clean fetch reported a diagnostic: %s", source.Diagnostic)
	}

	// The fetched file is the one classifying, not the vendored one.
	_, explain := compiled.Explain(manifest.Input{Agent: "cursor", Screen: "fetched cursor marker\n"})
	if explain.MatchedRule == nil || explain.MatchedRule.ID != "fetched_marker_blocked" {
		t.Fatalf("the fetched rule did not match: %+v", explain.MatchedRule)
	}
	if explain.ActiveSource != string(KindRemote) {
		t.Fatalf("explain active_source = %q, want %q", explain.ActiveSource, KindRemote)
	}
	if explain.ManifestVersion != "9999.01.01.1" ||
		explain.CachedRemoteVersion != "9999.01.01.1" ||
		explain.VendoredVersion != source.VendoredVersion {
		t.Fatalf("explain versions are wrong: active=%q remote=%q vendored=%q",
			explain.ManifestVersion, explain.CachedRemoteVersion, explain.VendoredVersion)
	}
	if !strings.Contains(explain.ManifestSource, string(KindRemote)) {
		t.Fatalf("explain manifest_source = %q, want it to name the remote source", explain.ManifestSource)
	}
}

// TestTheSidecarOverlayMergesOntoAFetchedManifest is why the remote file
// replaces the vendored one and not the overlay: the overlay's rules are
// Sidecar's amendments to upstream's file for that agent, and a newer copy of
// that file is still that file.
func TestTheSidecarOverlayMergesOntoAFetchedManifest(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	fetchNow(t, server.indexURL())

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if !HasOverlay("cursor") {
		t.Fatal("this test needs an agent that has an overlay to be meaningful")
	}
	if !source.OverlayApplied {
		t.Fatalf("the overlay was not merged onto the fetched manifest: %s", source.Diagnostic)
	}
	verdict := compiled.Evaluate(manifest.Input{
		Screen: "Waiting for decision (y/n/p)...\n",
	})
	if verdict.MatchedRule == nil || verdict.MatchedRule.ID != "sidecar.decision_blocked" {
		t.Fatalf("a Sidecar overlay rule stopped firing under a fetched manifest: %+v", verdict.MatchedRule)
	}
}

// TestAnOverlayThatNoLongerFitsAFetchedManifestIsDroppedNotFatal covers the
// case a maintainer has to be told about: upstream renamed a rule the overlay
// replaces, so the merge refuses. The fetched file still runs.
func TestAnOverlayThatNoLongerFitsAFetchedManifestIsDroppedNotFatal(t *testing.T) {
	remoteState(t)
	// codex's overlay disables upstream's `osc_title_idle`; a served file with
	// no such rule is exactly the rename case.
	served := `
id = "codex"
version = "9999.01.01.1"
min_engine_version = 1

[[rules]]
id = "renamed_idle"
state = "idle"
priority = 100
region = "whole_recent"
contains = ["fetched codex marker"]
`
	server := newCatalogServer(t, map[string]string{
		"index.toml": catalogFor([2]string{"codex", "codex.toml"}),
		"codex.toml": served,
	})
	fetchNow(t, server.indexURL())

	compiled, source, err := Load("codex")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindRemote {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindRemote)
	}
	if source.OverlayApplied {
		t.Fatal("an overlay that cannot merge must be dropped, not applied")
	}
	if !strings.Contains(source.Diagnostic, "sidecar/codex.toml") {
		t.Fatalf("diagnostic does not name the overlay it dropped: %q", source.Diagnostic)
	}
	if compiled.Evaluate(manifest.Input{Screen: "fetched codex marker\n"}).MatchedRule == nil {
		t.Fatal("the fetched manifest stopped classifying when its overlay was dropped")
	}
}

// TestAFetchedManifestOlderThanTheVendoredOneIsCachedButNotActive is Herdr's
// read_remote_manifest rule: a catalog that has rolled back cannot walk a
// running Sidecar backwards past the file its release shipped.
func TestAFetchedManifestOlderThanTheVendoredOneIsCachedButNotActive(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("1.0.0"),
	})
	fetchNow(t, server.indexURL())

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if source.CachedRemoteVersion != "1.0.0" {
		t.Fatalf("cached remote version = %q, want it reported even though it lost", source.CachedRemoteVersion)
	}
	if !strings.Contains(source.Diagnostic, "older than vendored") {
		t.Fatalf("diagnostic does not say why the cache lost: %q", source.Diagnostic)
	}
	if compiled.Evaluate(manifest.Input{Screen: "fetched cursor marker\n"}).MatchedRule != nil {
		t.Fatal("an older cached manifest classified a screen")
	}
}

// TestAFetchedManifestRequiringANewerEngineIsIgnoredNeverFatal is the
// min_engine_version rule. It is checked on the way in, so the file never
// reaches the cache at all, and the status file says which agent it was.
func TestAFetchedManifestRequiringANewerEngineIsIgnoredNeverFatal(t *testing.T) {
	remoteState(t)
	tooNew := strings.Replace(remoteCursor("9999.01.01.1"),
		"min_engine_version = 1",
		fmt.Sprintf("min_engine_version = %d", manifest.EngineVersion+1), 1)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": tooNew,
	})
	res := fetchNow(t, server.indexURL())

	if len(res.Updated) != 0 {
		t.Fatalf("updated = %v, want nothing", res.Updated)
	}
	status := res.Status.Agents["cursor"]
	if status.LastResult != FetchResultIgnored {
		t.Fatalf("cursor result = %q, want %q", status.LastResult, FetchResultIgnored)
	}
	if !strings.Contains(status.LastError, "requires engine") {
		t.Fatalf("cursor error does not name the engine version: %q", status.LastError)
	}
	if _, err := os.Stat(RemoteCachePath("cursor")); err == nil {
		t.Fatal("a manifest needing a newer engine was written to the cache")
	}
	if _, source, err := Load("cursor"); err != nil || source.Kind != KindBundled {
		t.Fatalf("Load did not fall back to the vendored manifest: kind=%q err=%v", source.Kind, err)
	}
}

// TestAFetchedFileOverTheCapIsRefused pins Herdr's 256 KiB limit, and pins that
// it is enforced on the bytes read rather than on a Content-Length the server
// supplies.
func TestAFetchedFileOverTheCapIsRefused(t *testing.T) {
	remoteState(t)
	oversized := remoteCursor("9999.01.01.1") +
		"\n# " + strings.Repeat("x", MaxFetchBytes) + "\n"
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": oversized,
	})
	res := fetchNow(t, server.indexURL())

	status := res.Status.Agents["cursor"]
	if status.LastResult != FetchResultFailed {
		t.Fatalf("cursor result = %q, want %q", status.LastResult, FetchResultFailed)
	}
	if !strings.Contains(status.LastError, "exceeded") {
		t.Fatalf("cursor error does not name the cap: %q", status.LastError)
	}
	if _, err := os.Stat(RemoteCachePath("cursor")); err == nil {
		t.Fatal("an oversized file was written to the cache")
	}
}

// TestFetchRefusesADowngradeAndKeepsTheCachedManifest and the test after it are
// Herdr's process_agent_manifest refusals, which protect a cache that is
// already ahead of both the catalog and the binary.
func TestFetchRefusesADowngradeAndKeepsTheCachedManifest(t *testing.T) {
	remoteState(t)
	newest := remoteCursor("9999.01.01.2")
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": newest,
	})
	fetchNow(t, server.indexURL())

	older := remoteCursor("9999.01.01.1")
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.toml") {
			_, _ = w.Write([]byte(catalogFor([2]string{"cursor", "cursor.toml"})))
			return
		}
		_, _ = w.Write([]byte(older))
	})
	res := fetchNow(t, server.indexURL())

	if status := res.Status.Agents["cursor"]; status.LastResult != FetchResultFailed ||
		!strings.Contains(status.LastError, "older than cached") {
		t.Fatalf("downgrade was not refused: %+v", status)
	}
	cached, err := os.ReadFile(RemoteCachePath("cursor"))
	if err != nil {
		t.Fatal(err)
	}
	if string(cached) != newest {
		t.Fatal("a refused downgrade overwrote the cached manifest")
	}
}

func TestFetchRefusesASameVersionWhoseContentChanged(t *testing.T) {
	remoteState(t)
	first := remoteCursor("9999.01.01.1")
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": first,
	})
	fetchNow(t, server.indexURL())

	changed := remoteManifest("cursor", "9999.01.01.1", "a different marker")
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.toml") {
			_, _ = w.Write([]byte(catalogFor([2]string{"cursor", "cursor.toml"})))
			return
		}
		_, _ = w.Write([]byte(changed))
	})
	res := fetchNow(t, server.indexURL())

	if status := res.Status.Agents["cursor"]; status.LastResult != FetchResultFailed ||
		!strings.Contains(status.LastError, "without a version bump") {
		t.Fatalf("a silent content change was accepted: %+v", status)
	}
	cached, err := os.ReadFile(RemoteCachePath("cursor"))
	if err != nil {
		t.Fatal(err)
	}
	if string(cached) != first {
		t.Fatal("a refused content change overwrote the cached manifest")
	}
}

func TestFetchReportsAnUnchangedManifestAsCurrent(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	fetchNow(t, server.indexURL())
	res := fetchNow(t, server.indexURL())

	if len(res.Updated) != 0 {
		t.Fatalf("updated = %v on an unchanged catalog", res.Updated)
	}
	if status := res.Status.Agents["cursor"]; status.LastResult != FetchResultCurrent {
		t.Fatalf("cursor result = %q, want %q", status.LastResult, FetchResultCurrent)
	}
}

// TestFetchRunsAtMostOncePerDayAcrossProcesses is the once-a-day rule, and the
// reason it is expressed as a timestamp in a file rather than a timer in memory:
// a Sidecar that is restarted five times an hour must still check once.
func TestFetchRunsAtMostOncePerDayAcrossProcesses(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	start := time.Now()

	first, err := Fetch(context.Background(), FetchOptions{CatalogURL: server.indexURL(), Now: start})
	if err != nil {
		t.Fatal(err)
	}
	if first.Skipped {
		t.Fatalf("the first check skipped: %s", first.Reason)
	}
	afterFirst := server.requests

	// A second process, minutes later, with no shared memory at all: only the
	// status file on disk says a check already happened.
	second, err := Fetch(context.Background(), FetchOptions{
		CatalogURL: server.indexURL(), Now: start.Add(23 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Skipped {
		t.Fatal("a second check inside the interval did not skip")
	}
	if server.requests != afterFirst {
		t.Fatalf("a skipped check still made %d requests", server.requests-afterFirst)
	}

	third, err := Fetch(context.Background(), FetchOptions{
		CatalogURL: server.indexURL(), Now: start.Add(FetchInterval + time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.Skipped {
		t.Fatalf("a check past the interval skipped: %s", third.Reason)
	}
	if server.requests <= afterFirst {
		t.Fatal("a check past the interval made no requests")
	}
}

// TestTheClaimIsWrittenBeforeTheNetworkWork is the answer to "what happens when
// two Sidecar processes run at once": the first one to start writes the check
// timestamp before it fetches, so the second sees a fresh claim and skips
// rather than duplicating the whole pass.
func TestTheClaimIsWrittenBeforeTheNetworkWork(t *testing.T) {
	remoteState(t)
	var duringFetch FetchStatus
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if duringFetch.LastCheckUnix == 0 {
			duringFetch = LoadFetchStatus()
		}
		_, _ = w.Write([]byte(catalogFor()))
	}))
	t.Cleanup(server.Close)

	if _, err := Fetch(context.Background(), FetchOptions{CatalogURL: server.URL + "/index.toml"}); err != nil {
		t.Fatal(err)
	}
	if duringFetch.LastCheckUnix == 0 {
		t.Fatal("no check timestamp was on disk while the catalog was being fetched")
	}
	if duringFetch.LastResult != FetchResultChecking {
		t.Fatalf("in-flight status = %q, want %q", duringFetch.LastResult, FetchResultChecking)
	}
}

func TestFetchSkipsACatalogAgentSidecarVendorsNoManifestFor(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml": catalogFor(
			[2]string{"cursor", "cursor.toml"},
			[2]string{"newagent", "newagent.toml"},
		),
		"cursor.toml":   remoteCursor("9999.01.01.1"),
		"newagent.toml": remoteManifest("newagent", "9999.01.01.1", "new agent marker"),
	})
	res := fetchNow(t, server.indexURL())

	if _, ok := res.Status.Agents["newagent"]; ok {
		t.Fatal("an agent with no vendored manifest was fetched")
	}
	if res.Status.Agents["cursor"].LastResult != FetchResultUpdated {
		t.Fatalf("the known agent was not fetched: %+v", res.Status.Agents["cursor"])
	}
}

func TestTheCatalogIsRefusedWholeWhenItIsMalformed(t *testing.T) {
	for _, tc := range []struct {
		name, index, want string
	}{
		{"wrong schema version", "schema_version = 2\n", "schema_version"},
		{"unknown key", "schema_version = 1\nwat = true\n", "parse catalog"},
		{"traversing path", catalogFor([2]string{"cursor", "../cursor.toml"}), "unsafe path"},
		{"absolute path", catalogFor([2]string{"cursor", "/cursor.toml"}), "unsafe path"},
		{"other host", catalogFor([2]string{"cursor", "https://elsewhere/x.toml"}), "unsafe path"},
		{"nested path", catalogFor([2]string{"cursor", "a/cursor.toml"}), "not a plain file name"},
		{"not toml", catalogFor([2]string{"cursor", "cursor.txt"}), "not a .toml file"},
		{"duplicate agent", catalogFor(
			[2]string{"cursor", "cursor.toml"}, [2]string{"cursor", "cursor.toml"}), "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remoteState(t)
			server := newCatalogServer(t, map[string]string{"index.toml": tc.index})
			res, err := Fetch(context.Background(), FetchOptions{CatalogURL: server.indexURL(), Force: true})
			if err == nil {
				t.Fatal("a malformed catalog was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			if res.Status.LastResult != FetchResultFailed {
				t.Fatalf("status = %q, want %q", res.Status.LastResult, FetchResultFailed)
			}
		})
	}
}

// TestALocalOverrideWinsOverAFetchedManifest is the precedence rule the plan
// and Herdr share, and the one the fetch must not quietly change.
func TestALocalOverrideWinsOverAFetchedManifest(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	fetchNow(t, server.indexURL())
	path := writeOverride(t, OverrideDir(), "cursor", overrideBody)
	resetLoadCache(t)

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindLocalOverride {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindLocalOverride)
	}
	if source.Path != path {
		t.Fatalf("source path = %q, want %q", source.Path, path)
	}
	// The cached version is still reported, because "you have a fetched
	// manifest and it is not the one running" is exactly what a user with both
	// needs to be told.
	if source.CachedRemoteVersion != "9999.01.01.1" {
		t.Fatalf("cached remote version = %q under an override", source.CachedRemoteVersion)
	}
	if compiled.Evaluate(manifest.Input{Screen: "fetched cursor marker\n"}).MatchedRule != nil {
		t.Fatal("a fetched rule fired under a local override that replaced the file")
	}
}

// TestACorruptCacheIsIgnoredAndTheVendoredManifestIsUsed: a cache is data
// Sidecar can throw away, so anything wrong with it degrades to the vendored
// tree with a reason, never to no manifest at all.
func TestACorruptCacheIsIgnoredAndTheVendoredManifestIsUsed(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"not toml", "id = \"cursor\"\nthis is not toml\n", "invalid"},
		{"no version", "id = \"cursor\"\nmin_engine_version = 1\n\n[[rules]]\nid = \"r\"\nstate = \"idle\"\ncontains = [\"x\"]\n", "invalid"},
		{"another agent", remoteManifest("codex", "9999.01.01.1", "x"), "does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remoteState(t)
			if err := os.MkdirAll(filepath.Dir(RemoteCachePath("cursor")), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(RemoteCachePath("cursor"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}

			compiled, source, err := Load("cursor")
			if err != nil {
				t.Fatal(err)
			}
			if source.Kind != KindBundled {
				t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
			}
			if !strings.Contains(source.Diagnostic, tc.want) {
				t.Fatalf("diagnostic %q does not say %q", source.Diagnostic, tc.want)
			}
			if compiled.Evaluate(manifest.Input{Screen: cursorBlockedScreen}).MatchedRule == nil {
				t.Fatal("the vendored cursor manifest stopped classifying under a corrupt cache")
			}
		})
	}
}

// TestFetchInvalidatesOnlyTheAgentsWhoseCacheMoved keeps the reload honest: a
// check that found everything current must not throw away every compiled
// manifest in the process.
func TestFetchInvalidatesOnlyTheAgentsWhoseCacheMoved(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})

	beforeCursor, _, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	beforeCodex, _, err := Load("codex")
	if err != nil {
		t.Fatal(err)
	}

	fetchNow(t, server.indexURL())

	afterCursor, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if afterCursor == beforeCursor {
		t.Fatal("cursor was not reloaded after its cache moved")
	}
	if source.Kind != KindRemote {
		t.Fatalf("cursor source kind = %q, want %q", source.Kind, KindRemote)
	}
	afterCodex, _, err := Load("codex")
	if err != nil {
		t.Fatal(err)
	}
	if afterCodex != beforeCodex {
		t.Fatal("codex was recompiled by a check that never touched it")
	}
}

// TestTheCacheIsReadOnFirstLoadAndNotBefore is the startup-latency rule for the
// cache, the same instrument the override has: a filesystem read must happen at
// first use of an agent's manifest, never at package init.
func TestTheCacheIsReadOnFirstLoadAndNotBefore(t *testing.T) {
	remoteState(t)
	before := remoteReads.Load()
	if before != 0 {
		// Not fatal on its own -- another test in this package has run -- but
		// the delta below is what the assertion rests on.
		t.Logf("cache reads before this test: %d", before)
	}
	if _, _, err := Load("amp"); err != nil {
		t.Fatal(err)
	}
	afterFirst := remoteReads.Load()
	if afterFirst != before+1 {
		t.Fatalf("first Load made %d cache reads, want 1", afterFirst-before)
	}
	if _, _, err := Load("amp"); err != nil {
		t.Fatal(err)
	}
	if remoteReads.Load() != afterFirst {
		t.Fatal("a second Load read the cache again; the sync.Once is not holding")
	}
}
