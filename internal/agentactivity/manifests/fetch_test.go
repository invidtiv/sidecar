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
//
// It also writes a config with detection.remoteManifests on, because the loader
// reads the cache only when the setting is on -- "off" means off, cache
// included. Every test that wants the other half of that rule says so by calling
// setRemoteManifests with "off".
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
	setRemoteManifests(t, config.RemoteManifestsHerdrDev)
	want := filepath.Join(dir, RemoteDirName)
	if got := RemoteDir(); got != want {
		t.Fatalf("RemoteDir() = %q, want %q", got, want)
	}
	return want
}

// setRemoteManifests writes detection.remoteManifests into the config the test
// axis points at, and clears both memoised caches so the next Load sees it.
//
// The value is only ever the gate here: every test drives Fetch with an explicit
// catalog URL, so "herdr.dev" turns the loader on without anything reaching it.
func setRemoteManifests(t *testing.T, value string) {
	t.Helper()
	path := config.ConfigPath()
	body := fmt.Sprintf(`{"detection":{"remoteManifests":%q}}`, value)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	resetLoadCache(t)
}

// writeCache puts bytes in the fetch cache directly, for the cases the fetch
// itself would now refuse to create: a file cached by an older binary, or one
// edited by hand.
func writeCache(t *testing.T, agent, body string) string {
	t.Helper()
	path := RemoteCachePath(agent)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	resetLoadCache(t)
	return path
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

// codexWithRenamedRule is a served codex manifest that the Sidecar overlay
// cannot merge onto: codex's overlay disables upstream's `osc_title_idle`, and a
// served file with no such rule is exactly what a rename upstream looks like.
const codexWithRenamedRule = `
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

// TestAFetchedManifestTheOverlayCannotMergeOntoIsNotCached is the case a
// maintainer has to be told about: upstream renamed a rule the overlay replaces,
// so the merge refuses.
//
// This test used to pin the opposite answer -- cache the file, drop the overlay,
// run the fetched file alone -- and that was wrong. codex's overlay carries six
// rules, cursor's four, claude's five, including the `osc_title_idle` disable,
// the `weak_blocker` replacement and the `\p{Alphabetic}` RE2 rewrites. Running
// a newer upstream file with all of that stripped out is a detection regression
// bought with a version bump, and it would have arrived silently on the day
// upstream renamed one rule id. Known-good detection wins: the file is not
// cached, the status file names the agent and the merge error, and re-cutting
// the overlay is what makes the next check take it.
func TestAFetchedManifestTheOverlayCannotMergeOntoIsNotCached(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml": catalogFor([2]string{"codex", "codex.toml"}),
		"codex.toml": codexWithRenamedRule,
	})
	res := fetchNow(t, server.indexURL())

	if len(res.Updated) != 0 {
		t.Fatalf("updated = %v, want nothing", res.Updated)
	}
	status := res.Status.Agents["codex"]
	if status.LastResult != FetchResultIgnored {
		t.Fatalf("codex result = %q, want %q", status.LastResult, FetchResultIgnored)
	}
	if !strings.Contains(status.LastError, "overlay no longer fits") {
		t.Fatalf("codex error does not say the overlay stopped fitting: %q", status.LastError)
	}
	if _, err := os.Stat(RemoteCachePath("codex")); err == nil {
		t.Fatal("a manifest the overlay cannot merge onto was written to the cache")
	}

	compiled, source, err := Load("codex")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled || !source.OverlayApplied {
		t.Fatalf("codex is not running vendored-plus-overlay: kind=%q overlay=%v", source.Kind, source.OverlayApplied)
	}
	if compiled.Evaluate(manifest.Input{Screen: "fetched codex marker\n"}).MatchedRule != nil {
		t.Fatal("the uncached manifest classified a screen")
	}
}

// TestACachedManifestTheOverlayCannotMergeOntoFallsBackToVendored is the same
// rule one layer down, for the cache the fetch will no longer create: a file
// written by an older binary, or one whose overlay changed after it was cached.
// The whole overlay would otherwise be dropped while the fetched file kept
// winning, which is the loudest possible way to lose Sidecar's own rules
// quietly.
func TestACachedManifestTheOverlayCannotMergeOntoFallsBackToVendored(t *testing.T) {
	remoteState(t)
	writeCache(t, "codex", codexWithRenamedRule)

	compiled, source, err := Load("codex")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q: a stale overlay must not cost the vendored file too",
			source.Kind, KindBundled)
	}
	if !source.OverlayApplied {
		t.Fatalf("the overlay was dropped along with the cached file: %q", source.Diagnostic)
	}
	if source.CachedRemoteVersion != "9999.01.01.1" {
		t.Fatalf("cached remote version = %q, want it reported even though it lost", source.CachedRemoteVersion)
	}
	if !strings.Contains(source.Diagnostic, "overlay no longer fits") ||
		!strings.Contains(source.Diagnostic, RemoteCachePath("codex")) {
		t.Fatalf("diagnostic does not name the cached file and the reason: %q", source.Diagnostic)
	}
	if compiled.Evaluate(manifest.Input{Screen: "fetched codex marker\n"}).MatchedRule != nil {
		t.Fatal("the abandoned cached manifest still classified a screen")
	}
}

// TestAFetchedManifestWithARuleRE2CannotCompileSaysSo is the fetched-file half
// of the rule the override path already holds to: a rule whose pattern cannot
// compile is skipped whole, so it asserts nothing, and a rule that silently
// never fires is the false "done" this engine exists to prevent.
//
// The vendored path cannot reach this state -- every incompatible pattern has an
// overlay rewrite and a test proving it -- but a published file can, the day
// upstream adds a `\p{Alphabetic}` rule the overlay has not caught up with. The
// sync report names those before a human merges them; a runtime fetch has no
// human in it, so the loader has to.
func TestAFetchedManifestWithARuleRE2CannotCompileSaysSo(t *testing.T) {
	remoteState(t)
	// A rule id the overlay does not carry, so the rewrite that keeps upstream's
	// existing `\p{Alphabetic}` rule alive does not cover this one. That is what
	// a newly published upstream rule looks like on the day it lands.
	served := remoteCursor("9999.01.01.1") + `
[[rules]]
id = "new_thinking_working"
state = "working"
priority = 91
region = "whole_recent"
visible_working = true
line_regex = ['^\p{Alphabetic}+ is thinking']
`
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": served,
	})
	fetchNow(t, server.indexURL())

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindRemote {
		t.Fatalf("source kind = %q (diagnostic %q), want %q", source.Kind, source.Diagnostic, KindRemote)
	}
	if !strings.Contains(source.Diagnostic, "never match") ||
		!strings.Contains(source.Diagnostic, "new_thinking_working") {
		t.Fatalf("diagnostic does not name the dead rule: %q", source.Diagnostic)
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
	if len(res.Status.SkippedRows) != 1 || !strings.Contains(res.Status.SkippedRows[0], "newagent") {
		t.Fatalf("the skipped row was not recorded: %v", res.Status.SkippedRows)
	}
}

// TestARowThisClientCannotUseIsSkippedNotFatal is the failure mode that would
// have been permanent and silent: Herdr adds one row this binary cannot resolve
// and every other agent stops updating for good.
//
// Herdr skips an id its own enum has no variant for *before* it validates the
// path (parse_catalog, manifest_update.rs:362). Sidecar keys on the path rather
// than the id, so the same rule has to cover a path it will not resolve too --
// nothing is fetched from such a row either way, and refusing the index over a
// row that was never going to be fetched is the same outage with extra steps.
func TestARowThisClientCannotUseIsSkippedNotFatal(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
	}{
		{"traversing path", "../cursor.toml", "unsafe path"},
		{"absolute path", "/cursor.toml", "unsafe path"},
		{"other host", "https://elsewhere/x.toml", "unsafe path"},
		{"nested path", "a/newthing.toml", "not a plain file name"},
		{"not toml", "newthing.txt", "not a .toml file"},
		{"no vendored manifest", "newthing.toml", "no vendored manifest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remoteState(t)
			server := newCatalogServer(t, map[string]string{
				"index.toml": catalogFor(
					[2]string{"newthing", tc.path},
					[2]string{"cursor", "cursor.toml"},
				),
				"cursor.toml": remoteCursor("9999.01.01.1"),
			})
			res := fetchNow(t, server.indexURL())

			if res.Status.LastResult != FetchResultChecked {
				t.Fatalf("status = %q, want %q: one bad row refused the whole catalog",
					res.Status.LastResult, FetchResultChecked)
			}
			if res.Status.Agents["cursor"].LastResult != FetchResultUpdated {
				t.Fatalf("the usable row was not fetched: %+v", res.Status.Agents["cursor"])
			}
			if len(res.Status.SkippedRows) != 1 || !strings.Contains(res.Status.SkippedRows[0], tc.want) {
				t.Fatalf("skipped rows = %v, want one mentioning %q", res.Status.SkippedRows, tc.want)
			}
		})
	}
}

// TestTheCatalogIsRefusedWholeWhenTheDocumentIsMalformed keeps the refusals that
// are about the catalog as a *document* rather than about one row: there is no
// defensible way to read any of these, so reading none of it is the answer.
func TestTheCatalogIsRefusedWholeWhenTheDocumentIsMalformed(t *testing.T) {
	for _, tc := range []struct {
		name, index, want string
	}{
		{"wrong schema version", "schema_version = 2\n", "schema_version"},
		{"unknown key", "schema_version = 1\nwat = true\n", "parse catalog"},
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

// TestOffMeansOffForACacheAlreadyOnDisk is the setting's other half, and the
// one it did not have.
//
// The setting used to gate fetching alone, so a cache written while it was on
// kept answering after it was turned off: `sidecar agent manifests` reported
// `remote`, `explain` reported `active_source: remote`, and the only way back to
// the vendored tree was deleting a state directory by hand. Herdr's loader reads
// its cache unconditionally and this diverges deliberately: a user turning a
// network feature off is asking the software to stop using what the network gave
// it. The bytes stay on disk for `--refresh` to pick up if it is turned on again.
func TestOffMeansOffForACacheAlreadyOnDisk(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	fetchNow(t, server.indexURL())
	if _, source, err := Load("cursor"); err != nil || source.Kind != KindRemote {
		t.Fatalf("the fetched manifest did not become active: kind=%q err=%v", source.Kind, err)
	}

	setRemoteManifests(t, config.RemoteManifestsOff)

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q with the setting off, want %q", source.Kind, KindBundled)
	}
	if source.CachedRemoteVersion != "" {
		t.Fatalf("cached remote version = %q with the setting off", source.CachedRemoteVersion)
	}
	if compiled.Evaluate(manifest.Input{Screen: "fetched cursor marker\n"}).MatchedRule != nil {
		t.Fatal("a cached rule fired with detection.remoteManifests off")
	}
	// The file is still there. Turning the setting off is not a delete, and
	// ClearCache is the verb for that.
	if _, statErr := os.Stat(RemoteCachePath("cursor")); statErr != nil {
		t.Fatalf("turning the setting off deleted the cache: %v", statErr)
	}
	// And CachedRemote still reports it, which is what lets the table say "you
	// have a fetched file and it is not the one running".
	if cached := CachedRemote("cursor"); cached.Version != "9999.01.01.1" {
		t.Fatalf("CachedRemote version = %q with the setting off, want the file's own", cached.Version)
	}
}

// TestClearCacheRemovesEveryCachedFileAndTheStatus is the recovery path an agent
// can take without a shell full of rm: the setting alone stops the cache being
// used, and this is what makes it stop existing.
func TestClearCacheRemovesEveryCachedFileAndTheStatus(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	fetchNow(t, server.indexURL())

	removed, err := ClearCache()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %v, want the cached manifest and the status file", removed)
	}
	if _, statErr := os.Stat(RemoteCachePath("cursor")); statErr == nil {
		t.Fatal("the cached manifest survived ClearCache")
	}
	if LoadFetchStatus().LastCheckUnix != 0 {
		t.Fatal("the status file survived ClearCache")
	}
	Invalidate("cursor")
	if _, source, loadErr := Load("cursor"); loadErr != nil || source.Kind != KindBundled {
		t.Fatalf("Load did not fall back to the vendored manifest: kind=%q err=%v", source.Kind, loadErr)
	}
	// An empty cache is what the caller asked for, so clearing one twice is
	// success, not an error.
	if removed, err = ClearCache(); err != nil || len(removed) != 0 {
		t.Fatalf("clearing an empty cache = %v, %v", removed, err)
	}
}

// TestAFailedCheckRetriesSoonerThanADay is the train case: a laptop opened with
// no connectivity claimed the day at 08:40 and never tried again when the
// connection came back. Claiming the day and never retrying a failure inside it
// are separable, and only the first of them is the crash-loop protection.
func TestAFailedCheckRetriesSoonerThanADay(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{})
	start := time.Now()

	first, err := Fetch(context.Background(), FetchOptions{CatalogURL: server.indexURL(), Now: start})
	if err == nil {
		t.Fatal("the check against a catalog with no index did not fail")
	}
	if first.Status.LastResult != FetchResultFailed {
		t.Fatalf("status = %q, want %q", first.Status.LastResult, FetchResultFailed)
	}
	afterFirst := server.requests

	// Inside the retry interval, the claim still holds: a crash loop must not
	// become a request loop.
	soon, err := Fetch(context.Background(), FetchOptions{
		CatalogURL: server.indexURL(), Now: start.Add(FetchRetryInterval / 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !soon.Skipped {
		t.Fatal("a retry inside the retry interval did not skip")
	}
	if server.requests != afterFirst {
		t.Fatalf("a skipped retry still made %d requests", server.requests-afterFirst)
	}

	// Past it, and well inside the day the successful path would have claimed.
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.toml") {
			_, _ = w.Write([]byte(catalogFor([2]string{"cursor", "cursor.toml"})))
			return
		}
		_, _ = w.Write([]byte(remoteCursor("9999.01.01.1")))
	})
	retry, err := Fetch(context.Background(), FetchOptions{
		CatalogURL: server.indexURL(), Now: start.Add(FetchRetryInterval + time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Skipped {
		t.Fatalf("a retry past the retry interval skipped: %s", retry.Reason)
	}
	if retry.Status.LastResult != FetchResultChecked {
		t.Fatalf("the retry did not succeed: %+v", retry.Status)
	}
	// And a successful check goes back to claiming the whole day.
	next, err := Fetch(context.Background(), FetchOptions{
		CatalogURL: server.indexURL(), Now: start.Add(FetchRetryInterval + 2*time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !next.Skipped {
		t.Fatal("a check hours after a successful one did not skip")
	}
}

// TestChangingTheCatalogURLDoesNotWaitOutTheOldCatalogsDay: the day belongs to
// the catalog that claimed it. A user who repoints detection.remoteManifests at
// another index has said the cache came from somewhere they no longer want it
// from, and making them wait would be the software arguing with an instruction
// it was just given.
func TestChangingTheCatalogURLDoesNotWaitOutTheOldCatalogsDay(t *testing.T) {
	remoteState(t)
	first := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	start := time.Now()
	if _, err := Fetch(context.Background(), FetchOptions{CatalogURL: first.indexURL(), Now: start}); err != nil {
		t.Fatal(err)
	}

	second := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.2"),
	})
	res, err := Fetch(context.Background(), FetchOptions{
		CatalogURL: second.indexURL(), Now: start.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatalf("a check against a new catalog waited out the old one's day: %s", res.Reason)
	}
	if res.Status.CatalogURL != second.indexURL() {
		t.Fatalf("status catalog = %q, want the new one", res.Status.CatalogURL)
	}
}

// TestAnUnreadableStatusFileIsReportedNotReadAsNeverChecked: this file is where
// the whole feature's observability lives, and reading a corrupt one as an empty
// status made a broken fetch look like one nobody had configured.
func TestAnUnreadableStatusFileIsReportedNotReadAsNeverChecked(t *testing.T) {
	remoteState(t)
	if err := os.MkdirAll(RemoteDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FetchStatusPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	status := LoadFetchStatus()
	if status.ReadError == "" {
		t.Fatal("a status file that could not be parsed reported as never checked")
	}
	if !strings.Contains(status.ReadError, FetchStatusPath()) {
		t.Fatalf("the read error does not name the file: %q", status.ReadError)
	}
	if status.LastCheckUnix != 0 {
		t.Fatal("a corrupt status file produced a check timestamp")
	}
	// And a fetch still runs and rewrites it: a claim nobody can read is not a
	// reason to stop checking forever.
	server := newCatalogServer(t, map[string]string{"index.toml": catalogFor()})
	res, err := Fetch(context.Background(), FetchOptions{CatalogURL: server.indexURL()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatalf("a corrupt status file stopped the next check: %s", res.Reason)
	}
	if LoadFetchStatus().ReadError != "" {
		t.Fatal("the rewritten status file is still unreadable")
	}
}

// TestARedirectToAnotherHostIsRefused closes the door joinURL guards: a catalog
// cannot name another host in a path, and a 302 must not let it name one anyway.
func TestARedirectToAnotherHostIsRefused(t *testing.T) {
	remoteState(t)
	elsewhere := newCatalogServer(t, map[string]string{
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.toml") {
			_, _ = w.Write([]byte(catalogFor([2]string{"cursor", "cursor.toml"})))
			return
		}
		http.Redirect(w, r, elsewhere.URL+"/cursor.toml", http.StatusFound)
	}))
	t.Cleanup(server.Close)

	// The default client is the one under test here, so this is deliberately
	// not the shared fetchNow helper.
	res, err := Fetch(context.Background(), FetchOptions{
		CatalogURL: server.URL + "/index.toml", Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	status := res.Status.Agents["cursor"]
	if status.LastResult != FetchResultFailed {
		t.Fatalf("cursor result = %q, want %q", status.LastResult, FetchResultFailed)
	}
	if !strings.Contains(status.LastError, "another host") {
		t.Fatalf("cursor error does not name the redirect refusal: %q", status.LastError)
	}
	if _, statErr := os.Stat(RemoteCachePath("cursor")); statErr == nil {
		t.Fatal("a manifest fetched across a redirect to another host was cached")
	}
	if elsewhere.requests != 0 {
		t.Fatalf("the other host received %d requests", elsewhere.requests)
	}
}

// TestARedirectWithinTheCatalogHostIsFollowed: the policy is a host pin, not a
// ban. A catalog reorganising its own paths keeps working.
func TestARedirectWithinTheCatalogHostIsFollowed(t *testing.T) {
	remoteState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "index.toml"):
			_, _ = w.Write([]byte(catalogFor([2]string{"cursor", "cursor.toml"})))
		case r.URL.Path == "/v2/cursor.toml":
			_, _ = w.Write([]byte(remoteCursor("9999.01.01.1")))
		default:
			http.Redirect(w, r, "/v2/cursor.toml", http.StatusFound)
		}
	}))
	t.Cleanup(server.Close)

	res, err := Fetch(context.Background(), FetchOptions{
		CatalogURL: server.URL + "/index.toml", Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status := res.Status.Agents["cursor"]; status.LastResult != FetchResultUpdated {
		t.Fatalf("cursor result = %+v, want %q", status, FetchResultUpdated)
	}
}

// TestACachedManifestNamingAnotherAgentByAliasIsRefused is the stricter half of
// the id check on the fetched path: an override may name its agent through an
// alias, because the user wrote it, but a file that arrived from a catalog may
// not. `id = "evil", aliases = ["cursor"]` served at cursor.toml is the one
// thing the id check exists to stop.
func TestACachedManifestNamingAnotherAgentByAliasIsRefused(t *testing.T) {
	remoteState(t)
	writeCache(t, "cursor", `
id = "evil"
aliases = ["cursor"]
version = "9999.01.01.1"
min_engine_version = 1

[[rules]]
id = "evil_idle"
state = "idle"
priority = 500
region = "whole_recent"
visible_idle = true
contains = ["anything at all"]
`)

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, "does not match") {
		t.Fatalf("diagnostic does not say why the cache was refused: %q", source.Diagnostic)
	}
	if compiled.Evaluate(manifest.Input{Screen: "anything at all\n"}).MatchedRule != nil {
		t.Fatal("a cached manifest naming cursor only through an alias classified a cursor screen")
	}
}

// TestAnInvalidLocalOverrideFallsBackToTheCachedManifest is Herdr's
// invalid_local_override_falls_back_to_cached_remote_manifest, which had no
// Sidecar equivalent before the fetch cache existed. The precedence is a stack,
// not a switch: a refused override drops to the next source down, which is the
// cache, not the vendored file.
func TestAnInvalidLocalOverrideFallsBackToTheCachedManifest(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})
	fetchNow(t, server.indexURL())
	path := writeOverride(t, OverrideDir(), "cursor", "id = \"cursor\"\nthis is not toml\n")
	resetLoadCache(t)

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindRemote {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindRemote)
	}
	if !strings.Contains(source.Diagnostic, path) {
		t.Fatalf("diagnostic does not name the refused override: %q", source.Diagnostic)
	}
	if compiled.Evaluate(manifest.Input{Screen: "fetched cursor marker\n"}).MatchedRule == nil {
		t.Fatal("the cached manifest did not answer under a refused override")
	}
}

// TestInvalidateDuringAConcurrentLoadDoesNotMemoiseTheStaleAnswer is the race a
// fetch creates on its own: Invalidate runs while another goroutine is inside
// load(), and with the result kept in a map keyed by agent that goroutine came
// back and wrote its pre-fetch answer over the fresh one, for the life of the
// process. Each Load now writes into the entry it started with, which
// Invalidate has already unlinked.
func TestInvalidateDuringAConcurrentLoadDoesNotMemoiseTheStaleAnswer(t *testing.T) {
	remoteState(t)
	server := newCatalogServer(t, map[string]string{
		"index.toml":  catalogFor([2]string{"cursor", "cursor.toml"}),
		"cursor.toml": remoteCursor("9999.01.01.1"),
	})

	// The stale reader: an entry taken before the fetch, whose load has not
	// finished writing its result yet. Taking the entry is what a Load does
	// first, so holding it here is exactly that goroutine's state.
	loadedMu.Lock()
	stale := &entry{}
	loadedBy["cursor"] = stale
	loadedMu.Unlock()

	fetchNow(t, server.indexURL())

	// It finishes now, after Invalidate has run, and writes the pre-fetch
	// answer into its own entry.
	stale.once.Do(func() { stale.result = loaded{source: Source{Agent: "cursor", Kind: KindBundled}} })

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindRemote {
		t.Fatalf("source kind = %q, want %q: a load that finished after Invalidate was memoised",
			source.Kind, KindRemote)
	}
}
