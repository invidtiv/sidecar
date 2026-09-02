package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
	"github.com/marcus/sidecar/internal/agentintegration"
)

// herdrCheckout is where the Herdr source is expected during development. The
// tests that need it skip when it is absent, so ordinary CI stays offline and
// needs no Rust, no Herdr binary, and no network.
const herdrCheckout = "~/code/herdr"

func herdrSource(t *testing.T) string {
	t.Helper()
	dir, err := expandPath(herdrCheckout)
	if err != nil {
		t.Skipf("cannot expand %s: %v", herdrCheckout, err)
	}
	if _, err := os.Stat(filepath.Join(dir, bundledDir)); err != nil {
		t.Skipf("no Herdr checkout at %s; skipping the sync round trip", dir)
	}
	return dir
}

// syncIntoTemp runs a full offline sync into temp directories and returns the
// manifest output directory plus the lock it wrote.
func syncIntoTemp(t *testing.T) (string, *manifests.Lock, string) {
	t.Helper()
	out, _, lock, _, source := syncIntoTempFull(t)
	return out, lock, source
}

// syncIntoTempFull is syncIntoTemp with both output roots and both locks, for
// the tests that care about the integration tree.
func syncIntoTempFull(t *testing.T) (string, string, *manifests.Lock, *agentintegration.UpstreamLock, string) {
	t.Helper()
	source := herdrSource(t)
	root := t.TempDir()
	out := filepath.Join(root, "manifests")
	integrationOut := filepath.Join(root, "agentintegration")
	report, err := sync(options{
		ref:            "e2b85c7",
		releaseTag:     "v0.8.2",
		catalogURL:     defaultCatalogURL,
		sourceDir:      source,
		offline:        true,
		out:            out,
		integrationOut: integrationOut,
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return out, integrationOut, report.Lock, report.Integration, source
}

func TestSyncFromLocalCheckoutWritesTheExpectedTree(t *testing.T) {
	out, lock, source := syncIntoTemp(t)

	if lock.SchemaVersion != manifests.LockSchemaVersion {
		t.Errorf("schema_version = %d, want %d", lock.SchemaVersion, manifests.LockSchemaVersion)
	}
	if lock.EngineVersion != 3 {
		t.Errorf("engine_version = %d, want 3", lock.EngineVersion)
	}
	if lock.Herdr.Ref != "e2b85c7" {
		t.Errorf("ref = %q", lock.Herdr.Ref)
	}
	if lock.Herdr.PinnedReleaseTag != "v0.8.2" {
		t.Errorf("pinned_release_tag = %q, want v0.8.2", lock.Herdr.PinnedReleaseTag)
	}
	if lock.Herdr.SourceDir != source {
		t.Errorf("source_dir = %q, want %q", lock.Herdr.SourceDir, source)
	}
	if lock.Catalog.Fetched {
		t.Error("an offline sync reported the catalog as fetched")
	}
	if lock.Catalog.ETag != "unknown" {
		t.Errorf("offline etag = %q, want unknown", lock.Catalog.ETag)
	}
	if lock.GeneratedAt == "" || !strings.HasSuffix(lock.GeneratedAt, "Z") {
		t.Errorf("generated_at = %q, want an RFC3339 UTC timestamp", lock.GeneratedAt)
	}
	if len(lock.Agents) != 21 {
		t.Errorf("vendored %d manifests, want the 21 Herdr bundles", len(lock.Agents))
	}

	for _, path := range []string{
		"upstream.lock.json", "aliases.upstream.json", "authority.upstream.json", "report.md",
		filepath.Join("upstream", "index.toml"),
		filepath.Join("upstream", "NOTICE"),
		filepath.Join("upstream", "LICENSE"),
	} {
		if _, err := os.Stat(filepath.Join(out, path)); err != nil {
			t.Errorf("sync did not write %s: %v", path, err)
		}
	}

	// Every vendored byte must equal the byte in the source checkout, from
	// whichever of the two directories won.
	for _, agent := range lock.Agents {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(agent.Path)))
		if err != nil {
			t.Fatalf("read %s: %v", agent.Path, err)
		}
		base := filepath.Base(agent.Path)
		var wantDir string
		switch agent.Source {
		case manifests.SourceBundled:
			wantDir = bundledDir
		case manifests.SourcePublished:
			wantDir = publishedDir
		default:
			t.Fatalf("%s has source %q", agent.ID, agent.Source)
		}
		want, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(wantDir), base))
		if err != nil {
			t.Fatalf("read source %s: %v", base, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not a byte-for-byte copy of %s/%s", agent.Path, wantDir, base)
		}
		if agent.SHA256 != sha256Hex(want) {
			t.Errorf("%s lock digest does not match the source bytes", agent.Path)
		}
	}
}

// TestSyncPinsTheAttributionFiles: LICENSE and NOTICE carry the attribution the
// whole vendored tree rests on, so the lock digests them exactly like a
// manifest and an edit to either fails the manifests package test.
func TestSyncPinsTheAttributionFiles(t *testing.T) {
	out, lock, _ := syncIntoTemp(t)
	for _, path := range []string{"upstream/LICENSE", "upstream/NOTICE"} {
		entry, ok := lock.File(path)
		if !ok {
			t.Errorf("the lock does not pin %s", path)
			continue
		}
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if entry.SHA256 != sha256Hex(data) {
			t.Errorf("%s digest %s does not match the written bytes %s", path, entry.SHA256, sha256Hex(data))
		}
		if entry.Bytes != len(data) {
			t.Errorf("%s is %d bytes, the lock says %d", path, len(data), entry.Bytes)
		}
		if entry.Origin == "" {
			t.Errorf("%s records no origin", path)
		}
	}
	if got := len(lock.Files); got != 2 {
		t.Errorf("the lock pins %d non-manifest files, want 2", got)
	}
}

// TestSyncReproducesTheCommittedSourceChoices is the plan's "published-versus-
// bundled choice recorded in the lock matches what the tool decides" check.
func TestSyncReproducesTheCommittedSourceChoices(t *testing.T) {
	_, fresh, _ := syncIntoTemp(t)
	committed, err := manifests.LoadLock()
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(fresh.Agents) != len(committed.Agents) {
		t.Fatalf("fresh sync produced %d agents, the committed lock has %d",
			len(fresh.Agents), len(committed.Agents))
	}
	for _, agent := range fresh.Agents {
		want, ok := committed.Agent(agent.ID)
		if !ok {
			t.Errorf("fresh sync produced %s, which the committed lock does not have", agent.ID)
			continue
		}
		if agent.Source != want.Source {
			t.Errorf("%s: fresh sync chose %s, the committed lock says %s",
				agent.ID, agent.Source, want.Source)
		}
		if agent.SHA256 != want.SHA256 {
			t.Errorf("%s: fresh sync digest %s, committed %s", agent.ID, agent.SHA256, want.SHA256)
		}
		if agent.Version != want.Version {
			t.Errorf("%s: fresh sync version %s, committed %s", agent.ID, agent.Version, want.Version)
		}
	}
	// The two documented exceptions, asserted by name so a change upstream is
	// a review conversation rather than a silent flip.
	if grok, ok := fresh.Agent("grok"); !ok || grok.Source != manifests.SourceBundled {
		t.Errorf("grok source = %+v, want bundled (published 2026.07.16.1 is older than bundled 2026.07.16.2)", grok)
	}
	if muse, ok := fresh.Agent("muse"); !ok || muse.Source != manifests.SourceBundled || muse.PublishedVersion != "" {
		t.Errorf("muse source = %+v, want bundled only (it is not in the published catalog)", muse)
	}
}

// TestDirSourceReadsBytesFromTheRequestedRef is the guard for the bug where the
// lock could attest a commit whose bytes were never read: the tool read the
// working tree while recording whatever --ref resolved to. Every read now goes
// through `git show <commit>:<path>`, so the two can no longer disagree.
func TestDirSourceReadsBytesFromTheRequestedRef(t *testing.T) {
	dir := herdrSource(t)
	head := revParse(t, dir, "HEAD")
	prev, err := gitRevParse(dir, "HEAD~1")
	if err != nil {
		t.Skipf("no HEAD~1 in %s: %v", dir, err)
	}
	if head == prev {
		t.Skip("HEAD and HEAD~1 are the same commit")
	}

	src, err := newDirSource(dir, prev)
	if err != nil {
		t.Fatalf("newDirSource at %s: %v", prev, err)
	}
	if src.commit() != prev {
		t.Errorf("commit() = %s, want %s", src.commit(), prev)
	}

	// A file whose content differs between the two commits separates "read the
	// requested ref" from "read whatever the checkout currently holds".
	rel := fileChangedBetween(t, dir, prev, head)
	got, err := src.read(rel)
	if err != nil {
		t.Fatalf("read %s at %s: %v", rel, prev, err)
	}
	wantPrev := showAt(t, dir, prev, rel)
	if !bytes.Equal(got, wantPrev) {
		t.Errorf("%s: dirSource returned %d bytes, %s holds %d", rel, len(got), prev, len(wantPrev))
	}
	if atHead := showAt(t, dir, head, rel); bytes.Equal(got, atHead) {
		t.Errorf("%s: dirSource returned the bytes at HEAD although it was pinned to %s", rel, prev)
	}
	if working, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err == nil && bytes.Equal(got, working) {
		t.Errorf("%s: dirSource returned the working-tree bytes although it was pinned to %s", rel, prev)
	}

	// Listings must come from the same commit, or the file set and the bytes
	// could still disagree.
	names, err := src.list(bundledDir)
	if err != nil {
		t.Fatalf("list %s at %s: %v", bundledDir, prev, err)
	}
	if len(names) == 0 {
		t.Errorf("list %s at %s returned nothing", bundledDir, prev)
	}
}

func TestDirSourceRefusesARefTheCheckoutDoesNotHave(t *testing.T) {
	dir := herdrSource(t)
	if _, err := newDirSource(dir, "no-such-ref-0000000"); err == nil {
		t.Fatal("newDirSource accepted a ref the checkout cannot resolve")
	}
	if _, err := sync(options{
		ref:            "no-such-ref-0000000",
		releaseTag:     "v0.8.2",
		catalogURL:     defaultCatalogURL,
		sourceDir:      dir,
		offline:        true,
		out:            t.TempDir(),
		integrationOut: t.TempDir(),
	}); err == nil {
		t.Fatal("sync vendored bytes for a ref the checkout cannot resolve")
	}
}

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	sha, err := gitRevParse(dir, ref)
	if err != nil {
		t.Fatalf("git rev-parse %s in %s: %v", ref, dir, err)
	}
	return sha
}

func showAt(t *testing.T, dir, ref, rel string) []byte {
	t.Helper()
	data, err := gitOutput(dir, "show", ref+":"+rel)
	if err != nil {
		t.Fatalf("git show %s:%s: %v", ref, rel, err)
	}
	return data
}

// fileChangedBetween names a path that exists at both commits with different
// content.
func fileChangedBetween(t *testing.T, dir, from, to string) string {
	t.Helper()
	out, err := gitOutput(dir, "diff", "--name-only", "--diff-filter=M", from, to)
	if err != nil {
		t.Fatalf("git diff %s %s: %v", from, to, err)
	}
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	t.Skipf("no file was modified between %s and %s", from, to)
	return ""
}

func TestSyncRefusesAnUnreadableSourceDir(t *testing.T) {
	if _, err := sync(options{sourceDir: filepath.Join(t.TempDir(), "nope"), offline: true,
		out: t.TempDir(), integrationOut: t.TempDir()}); err == nil {
		t.Fatal("sync accepted a source dir that does not exist")
	}
}

// The alias gap scan reads Sidecar's own source. When it cannot, the sync fails:
// "every alias appears" and "the file could not be read" render identically, and
// the reassuring one is the wrong answer to publish in a report.
func TestSyncFailsWhenSidecarSourceCannotBeRead(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := sidecarAliasGaps(&manifests.Aliases{Agents: map[string][]string{"claude": {"claude", "claude-code"}}}); err == nil {
		t.Error("the alias gap scan reported no gaps although it could not read internal/agentactivity/activity.go")
	}
	if _, err := sync(options{
		ref:            "e2b85c7",
		releaseTag:     "v0.8.2",
		catalogURL:     defaultCatalogURL,
		sourceDir:      herdrCheckout,
		offline:        true,
		out:            t.TempDir(),
		integrationOut: t.TempDir(),
	}); err == nil {
		t.Error("sync wrote a report from a working directory outside the Sidecar repository")
	}
}

func TestSyncRefusesOfflineWithoutACheckout(t *testing.T) {
	if _, err := sync(options{offline: true, out: t.TempDir(), integrationOut: t.TempDir()}); err == nil {
		t.Fatal("sync accepted --offline with no --source-dir")
	}
}

// --- extractors ------------------------------------------------------------------

// stubSource serves inline content, so the extractors' shape assumptions can be
// tested without a checkout.
type stubSource struct {
	files map[string]string
	dirs  map[string][]string
}

func (s *stubSource) read(p string) ([]byte, error) {
	body, ok := s.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(body), nil
}

func (s *stubSource) list(p string) ([]string, error) {
	var names []string
	for name := range s.files {
		dir, base := filepath.Split(name)
		if strings.TrimSuffix(dir, "/") == p {
			names = append(names, base)
		}
	}
	return names, nil
}

// readAt ignores the ref: the stub has one version of every file, which is all
// the extractor tests need. The port-diff path is exercised against the real
// checkout instead, where a ref means something.
func (s *stubSource) readAt(_ string, p string) ([]byte, error) { return s.read(p) }

func (s *stubSource) listDirs(p string) ([]string, error) { return s.dirs[p], nil }
func (s *stubSource) commit() string                      { return "stub" }
func (s *stubSource) localDir() string                    { return "" }

const stubModRS = `
pub fn agent_label(agent: Agent) -> &'static str {
    match agent {
        Agent::Claude => "claude",
        Agent::Muse => "muse",
    }
}

fn lookup_agent(name: &str) -> Option<Agent> {
    let name = path_basename(name);
    match name {
        "claude" | "claude-code" => Some(Agent::Claude),
        "muse" | "muse-code" | "muse-cli" => Some(Agent::Muse),
        _ if is_muse_versioned_binary(name) => Some(Agent::Muse),
        _ => None,
    }
}

fn is_muse_versioned_binary(name: &str) -> bool {
    path_basename(name)
        .strip_prefix("muse-bin-")
        .is_some_and(|rest| rest.starts_with(|c: char| c.is_ascii_digit()))
}

fn normalized_agent_lookup_name(name: &str) -> String {
    let mut name = name.trim().to_lowercase();
    for suffix in [".exe", ".cmd"] {
        if name.ends_with(suffix) {
            name.truncate(name.len() - suffix.len());
            break;
        }
    }
    name
}

fn is_generic_runtime_or_shell(name: &str) -> bool {
    let name = normalized_agent_lookup_name(path_basename(name));
    is_python_runtime(&name)
        || matches!(
            name.as_str(),
            "sh" | "bash" | "node"
        )
}

fn is_python_runtime(name: &str) -> bool {
    name == "python"
        || name.strip_prefix("python").is_some_and(|version| !version.is_empty())
}
`

func TestExtractAliasesFromAnInlineSnippet(t *testing.T) {
	src := &stubSource{files: map[string]string{aliasSource: stubModRS}}
	aliases, err := extractAliases(src, "stub")
	if err != nil {
		t.Fatalf("extractAliases: %v", err)
	}
	if got := aliases.Agents["claude"]; strings.Join(got, ",") != "claude,claude-code" {
		t.Errorf("claude aliases = %v", got)
	}
	if got := aliases.Agents["muse"]; strings.Join(got, ",") != "muse,muse-cli,muse-code" {
		t.Errorf("muse aliases = %v", got)
	}
	if got := aliases.GenericRuntimes; strings.Join(got, ",") != "bash,node,sh" {
		t.Errorf("generic runtimes = %v", got)
	}
	if aliases.VersionedBinaryPrefixes["muse"] != "muse-bin-" {
		t.Errorf("versioned prefixes = %v", aliases.VersionedBinaryPrefixes)
	}
	if strings.Join(aliases.NormalizedSuffixes, ",") != ".exe,.cmd" {
		t.Errorf("normalized suffixes = %v", aliases.NormalizedSuffixes)
	}
}

func TestExtractAliasesFailsLoudlyWhenTheShapeChanges(t *testing.T) {
	cases := map[string]string{
		"no lookup_agent":   strings.Replace(stubModRS, "fn lookup_agent", "fn lookup_agent_v2", 1),
		"no agent_label":    strings.Replace(stubModRS, "pub fn agent_label", "pub fn agent_name", 1),
		"arm shape changed": strings.ReplaceAll(stubModRS, "=> Some(Agent::", "=> Ok(Agent::"),
		"unlabelled agent":  strings.Replace(stubModRS, `Agent::Muse => "muse",`, "", 1),
		"no python runtime": strings.Replace(stubModRS, `strip_prefix("python")`, `starts_with("python")`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			src := &stubSource{files: map[string]string{aliasSource: body}}
			if _, err := extractAliases(src, "stub"); err == nil {
				t.Fatal("extractAliases silently accepted a changed source shape")
			}
		})
	}
}

const stubAgentsMDX = `
## Supported agents

| Agent | State authority | Integration role |
| --- | --- | --- |
| Pi | lifecycle hooks when installed; otherwise screen manifest | state and session |
| Claude Code | screen manifest | session |
| Amp | screen manifest | none |

Detected but less thoroughly tested: Gemini CLI and Cline. Unsupported agents still run normally.
`

func stubAssets() *stubSource {
	return &stubSource{
		files: map[string]string{
			authoritySource:                            stubAgentsMDX,
			assetsDir + "/pi/herdr-agent-state.ts":     "// HERDR_INTEGRATION_VERSION=8\n",
			assetsDir + "/claude/herdr-agent-state.sh": "# HERDR_INTEGRATION_VERSION=9\n",
		},
		dirs: map[string][]string{assetsDir: {"claude", "pi"}},
	}
}

// stubAuthority reads the stub's assets the way sync does, then extracts.
func stubAuthority(src source) (*manifests.Authority, error) {
	dirs, _, err := integrationAssets(src)
	if err != nil {
		return nil, err
	}
	return extractAuthority(src, "stub", dirs)
}

func TestExtractAuthorityFromAnInlineSnippet(t *testing.T) {
	authority, err := stubAuthority(stubAssets())
	if err != nil {
		t.Fatalf("extractAuthority: %v", err)
	}
	want := map[string]string{
		"pi":     manifests.AuthorityHooks,
		"claude": manifests.AuthoritySessionIdentity,
		"amp":    manifests.AuthorityNone,
		"gemini": manifests.AuthorityNone,
		"cline":  manifests.AuthorityNone,
	}
	for id, authorityWant := range want {
		entry, ok := authority.Agents[id]
		if !ok {
			t.Errorf("no authority entry for %s", id)
			continue
		}
		if entry.LifecycleAuthority != authorityWant {
			t.Errorf("%s lifecycle_authority = %q, want %q", id, entry.LifecycleAuthority, authorityWant)
		}
	}
	if got := authority.Agents["claude"].IntegrationVersion; got != 9 {
		t.Errorf("claude integration version = %d, want 9", got)
	}
	if got := authority.Agents["pi"].IntegrationVersion; got != 8 {
		t.Errorf("pi integration version = %d, want 8", got)
	}
	if got := authority.Agents["amp"].IntegrationVersion; got != 0 {
		t.Errorf("amp integration version = %d, want 0", got)
	}
}

func TestExtractAuthorityFailsOnAnUnmappedDisplayName(t *testing.T) {
	src := stubAssets()
	src.files[authoritySource] = strings.Replace(stubAgentsMDX,
		"| Claude Code |", "| Brand New Agent |", 1)
	if _, err := stubAuthority(src); err == nil {
		t.Fatal("extractAuthority accepted a display name with no agent id mapping")
	}
}

func TestExtractAuthorityFailsOnDisagreeingAssetVersions(t *testing.T) {
	src := stubAssets()
	src.files[assetsDir+"/claude/herdr-agent-state.ps1"] = "# HERDR_INTEGRATION_VERSION=7\n"
	if _, err := stubAuthority(src); err == nil {
		t.Fatal("extractAuthority accepted two versions in one asset directory")
	}
}

func TestExtractorsRunAgainstTheRealHerdrSource(t *testing.T) {
	dir := herdrSource(t)
	src, err := newDirSource(dir, "e2b85c7")
	if err != nil {
		t.Skipf("checkout at %s does not have e2b85c7: %v", dir, err)
	}

	aliases, err := extractAliases(src, "e2b85c7")
	if err != nil {
		t.Fatalf("extractAliases against the real source: %v", err)
	}
	if len(aliases.Agents) < 20 {
		t.Errorf("extracted %d agents from lookup_agent, want at least 20", len(aliases.Agents))
	}
	for id, want := range map[string]string{
		"claude": "claude-code", "opencode": "opencode2", "qodercli": "qoderclicn", "cursor": "cursor-agent",
	} {
		found := false
		for _, alias := range aliases.Agents[id] {
			if alias == want {
				found = true
			}
		}
		if !found {
			t.Errorf("aliases for %s do not include %q: %v", id, want, aliases.Agents[id])
		}
	}

	assetDirs, _, err := integrationAssets(src)
	if err != nil {
		t.Fatalf("integrationAssets against the real source: %v", err)
	}
	authority, err := extractAuthority(src, "e2b85c7", assetDirs)
	if err != nil {
		t.Fatalf("extractAuthority against the real source: %v", err)
	}
	for id, want := range map[string]string{
		"pi": manifests.AuthorityHooks, "omp": manifests.AuthorityHooks,
		"kimi": manifests.AuthorityHooks, "opencode": manifests.AuthorityHooks,
		"kilo": manifests.AuthorityHooks, "mastracode": manifests.AuthorityHooks,
		"claude": manifests.AuthoritySessionIdentity, "codex": manifests.AuthoritySessionIdentity,
		"amp": manifests.AuthorityNone, "kiro": manifests.AuthorityNone,
		"gemini": manifests.AuthorityNone, "cline": manifests.AuthorityNone,
	} {
		if got := authority.Agents[id].LifecycleAuthority; got != want {
			t.Errorf("%s lifecycle_authority = %q, want %q", id, got, want)
		}
	}
}

func TestReportNamesTheThingsAReviewerLooksFor(t *testing.T) {
	out, _, _ := syncIntoTemp(t)
	body, err := os.ReadFile(filepath.Join(out, "report.md"))
	if err != nil {
		t.Fatalf("read report.md: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"# Herdr detection sync report",
		"## Published versus bundled",
		"## Regex compatibility",
		"## Alias table",
		"## Authority gaps",
		"## Fixture verdict flips",
		"## Overlay rules",
		"grok",
		"muse",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report.md does not contain %q", want)
		}
	}
}

func TestLockIsValidJSONWithSortedAgents(t *testing.T) {
	out, _, _ := syncIntoTemp(t)
	data, err := os.ReadFile(filepath.Join(out, "upstream.lock.json"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	var lock manifests.Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("lock is not valid JSON: %v", err)
	}
	for i := 1; i < len(lock.Agents); i++ {
		if lock.Agents[i-1].ID >= lock.Agents[i].ID {
			t.Fatalf("lock agents are not sorted by id: %s then %s", lock.Agents[i-1].ID, lock.Agents[i].ID)
		}
	}
}

// --- integration assets ------------------------------------------------------------

// TestSyncVendorsTheIntegrationAssetsByteForByte is the step-5 equivalent of the
// manifest round trip: every file under Herdr's src/integration/assets is
// vendored verbatim, in upstream's own directory shape, and pinned.
func TestSyncVendorsTheIntegrationAssetsByteForByte(t *testing.T) {
	_, integrationOut, _, lock, source := syncIntoTempFull(t)
	if lock == nil {
		t.Fatal("the sync produced no integration lock")
	}
	if lock.SchemaVersion != agentintegration.UpstreamLockSchemaVersion {
		t.Errorf("schema_version = %d, want %d", lock.SchemaVersion, agentintegration.UpstreamLockSchemaVersion)
	}
	if lock.Herdr.AssetsDir != assetsDir {
		t.Errorf("assets_dir = %q, want %q", lock.Herdr.AssetsDir, assetsDir)
	}
	if len(lock.Providers) != 17 {
		t.Errorf("vendored %d providers, want the 17 Herdr asset directories", len(lock.Providers))
	}

	pinned := 0
	for _, provider := range lock.Providers {
		if provider.Directory == "" || provider.ID == "" {
			t.Errorf("provider %+v is missing an id or a directory", provider)
		}
		for _, file := range provider.Files {
			pinned++
			assertVendoredCopy(t, integrationOut, source, file)
		}
	}
	for _, file := range lock.Files {
		if file.Origin == agentintegration.UpstreamGeneratedNotice {
			continue
		}
		pinned++
		assertVendoredCopy(t, integrationOut, source, file)
	}
	// 34 upstream assets plus the LICENSE that has to travel with them.
	if pinned != 35 {
		t.Errorf("pinned %d upstream files, want 35", pinned)
	}

	// The one directory name that is not its agent id.
	if provider, ok := lock.Provider("agy"); !ok || provider.Directory != "antigravity_cli" {
		t.Errorf("agy is vendored as %+v, want directory antigravity_cli", provider)
	}
	// The shared test file lives at the root of the assets directory upstream
	// and must land at the root here, not inside a provider.
	if _, ok := lock.File("upstream/herdr-agent-state.test.ts"); !ok {
		t.Error("the shared herdr-agent-state.test.ts is not pinned at the root of the vendored tree")
	}
	for _, want := range []string{"upstream/LICENSE", "upstream/NOTICE"} {
		if _, ok := lock.File(want); !ok {
			t.Errorf("the integration lock does not pin %s", want)
		}
	}
}

// assertVendoredCopy proves one locked file is the byte-for-byte upstream file
// its Origin names, and that the digest in the lock describes those same bytes.
func assertVendoredCopy(t *testing.T, out, source string, file agentintegration.UpstreamFile) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(file.Path)))
	if err != nil {
		t.Errorf("read %s: %v", file.Path, err)
		return
	}
	want := showAt(t, source, "e2b85c7", file.Origin)
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not a byte-for-byte copy of %s", file.Path, file.Origin)
	}
	if file.SHA256 != sha256Hex(want) {
		t.Errorf("%s lock digest does not match the upstream bytes", file.Path)
	}
	if file.Bytes != len(want) {
		t.Errorf("%s is locked at %d bytes, upstream has %d", file.Path, file.Bytes, len(want))
	}
}

// TestIntegrationVersionsComeFromTheAssetsThemselves pins the numbers the report
// and the authority table both read, so a half-bumped upstream directory is a
// failing sync rather than a coin toss.
func TestIntegrationVersionsComeFromTheAssetsThemselves(t *testing.T) {
	_, _, _, lock, _ := syncIntoTempFull(t)
	for id, want := range map[string]int{
		"claude": 9, "codex": 8, "opencode": 10, "pi": 8, "kimi": 7, "hermes": 5, "agy": 3,
	} {
		provider, ok := lock.Provider(id)
		if !ok {
			t.Errorf("no vendored provider %s", id)
			continue
		}
		if provider.Version != want {
			t.Errorf("%s integration version = %d, want %d", id, provider.Version, want)
		}
	}
	// A file that declares no version is still vendored and still pinned; it
	// just contributes nothing to the directory's version.
	hermes, _ := lock.Provider("hermes")
	var sawUnversioned bool
	for _, file := range hermes.Files {
		if strings.HasSuffix(file.Path, "plugin.yaml") && file.Version == 0 {
			sawUnversioned = true
		}
	}
	if !sawUnversioned {
		t.Error("hermes/plugin.yaml is not pinned as a version-free file")
	}
}

// TestSyncPrunesAVendoredAssetUpstreamNoLongerShips: an unpinned file is one the
// lock test cannot protect, so a dropped provider has to leave the tree.
func TestSyncPrunesAVendoredAssetUpstreamNoLongerShips(t *testing.T) {
	_, integrationOut, _, _, source := syncIntoTempFull(t)
	stale := filepath.Join(integrationOut, "upstream", "gone", "herdr-agent-state.sh")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("# dropped upstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sync(options{
		ref: "e2b85c7", releaseTag: "v0.8.2", catalogURL: defaultCatalogURL,
		sourceDir: source, offline: true, out: t.TempDir(), integrationOut: integrationOut,
	}); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a vendored asset upstream no longer ships survived the sync")
	}
	if _, err := os.Stat(filepath.Dir(stale)); err == nil {
		t.Error("the directory the pruning emptied was left behind")
	}
	// Pruning must not take the live tree with it.
	if _, err := os.Stat(filepath.Join(integrationOut, "upstream", "claude", "herdr-agent-state.sh")); err != nil {
		t.Errorf("pruning removed a file the sync still produces: %v", err)
	}
}

// TestReportShowsIntegrationBumpsAndPortDiffs is the review surface this phase
// exists for: a bump for a provider nobody has ported is a heads-up line, and a
// ported provider gets the comparison against what its port was written from.
func TestReportShowsIntegrationBumpsAndPortDiffs(t *testing.T) {
	out, _, _, _, _ := syncIntoTempFull(t)
	body, err := os.ReadFile(filepath.Join(out, "report.md"))
	if err != nil {
		t.Fatalf("read report.md: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"## Integration assets",
		"### Upstream changes since each Sidecar port",
		"| `opencode` | `opencode` | 10 |",
		"#### `claude` — ported from herdr `claude` version 9",
		"#### `codex` — ported from herdr `codex` version 8",
		"#### `opencode` — ported from herdr `opencode` version 10",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report.md does not contain %q", want)
		}
	}
	if strings.Contains(text, "Vendoring the assets themselves is Phase 3") {
		t.Error("report.md still carries the pre-Phase-3 caveat")
	}
}

// TestPortDiffShowsTheWholeFileWhenTheStartingPointIsUnknown is the plan's rule
// for a port nobody can attribute: with nothing to diff against, the report owes
// the reader the file.
func TestPortDiffShowsTheWholeFileWhenTheStartingPointIsUnknown(t *testing.T) {
	_, integrationOut, _, lock, source := syncIntoTempFull(t)
	src, err := newDirSource(source, "e2b85c7")
	if err != nil {
		t.Fatalf("newDirSource: %v", err)
	}
	diffs := integrationPortDiffs(src, integrationOut, lock)
	if len(diffs) == 0 {
		t.Fatal("no port diffs were computed")
	}
	for _, entry := range diffs {
		if entry.Ported.Version == agentintegration.UnknownPortedVersion {
			continue
		}
		for _, file := range entry.Files {
			if file.Whole {
				t.Errorf("%s rendered %s as a whole file although its port names version %s",
					entry.Ported.Provider, file.Path, entry.Ported.Version)
			}
		}
	}

	// Force the unknown case, which no shipped record uses today.
	unknown := integrationPortDiffsFor(src, integrationOut, lock, []agentintegration.PortedFrom{{
		Provider: "opencode", UpstreamID: "opencode", UpstreamDir: "opencode",
		Version: agentintegration.UnknownPortedVersion, Evidence: "test",
	}})
	if len(unknown) != 1 || len(unknown[0].Files) == 0 {
		t.Fatalf("unknown-version diff produced %+v", unknown)
	}
	for _, file := range unknown[0].Files {
		if !file.Whole || !file.Changed {
			t.Errorf("%s was not rendered as a whole file for an unknown ported-from version", file.Path)
		}
	}
	if unknown[0].Note == "" {
		t.Error("an unknown ported-from version was rendered without saying why")
	}
}

func TestUnifiedDiffShowsOnlyWhatChanged(t *testing.T) {
	before := []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n")
	after := []byte("one\ntwo\nthree\nfour\nfive\nSIX\nseven\neight\nnine\nten\n")
	body, changed := unifiedDiff("before", "after", before, after, diffLineBudget)
	if !changed {
		t.Fatal("unifiedDiff reported no change between two different files")
	}
	if !strings.Contains(body, "-six") || !strings.Contains(body, "+SIX") {
		t.Errorf("diff does not show the changed line:\n%s", body)
	}
	if strings.Contains(body, " one") {
		t.Errorf("diff carried a line far outside the hunk:\n%s", body)
	}
	if _, changed := unifiedDiff("a", "b", before, before, diffLineBudget); changed {
		t.Error("unifiedDiff reported a change between identical files")
	}
	long := []byte(strings.Repeat("x\n", 200) + "tail\n")
	body, _ = unifiedDiff("before", "after", before, long, 20)
	if !strings.Contains(body, "diff truncated at 20 lines") {
		t.Errorf("an oversized diff was not truncated:\n%s", body)
	}
}

// --- fixture corpus ----------------------------------------------------------------

// demoManifest is a two-rule manifest in Herdr's grammar, small enough to read
// and complete enough to compile. The tests that use it are about the
// comparison, not about the engine, which has its own suite.
const demoManifest = `id = "demo"
version = "2026.01.01.1"
min_engine_version = 1

[[rules]]
id = "demo_working"
state = "working"
priority = 100
region = "whole_recent"
contains = ["esc to interrupt"]
`

func demoSide(t *testing.T, manifests map[string]string) *corpusSide {
	t.Helper()
	bytesByBase := map[string][]byte{}
	for base, body := range manifests {
		bytesByBase[base] = []byte(body)
	}
	return newCorpusSide(bytesByBase, nil)
}

func demoFixture(screen string) corpusFixture {
	return corpusFixture{
		agent: "demo", name: "screen.txt", base: "demo",
		input: manifest.Input{Screen: screen, Rows: 24},
	}
}

// TestTheFlipTableNamesAManifestAddedAndOneNoLongerVendored covers the two
// shape changes a sync can produce that a naive comparison drops on the floor:
// an agent whose manifest is new, and one whose manifest is gone. Neither may
// panic and neither may vanish from the report.
func TestTheFlipTableNamesAManifestAddedAndOneNoLongerVendored(t *testing.T) {
	fixtures := []corpusFixture{demoFixture("running… esc to interrupt\n")}

	added := &corpusComparison{
		fixtures: fixtures,
		before:   demoSide(t, nil),
		after:    demoSide(t, map[string]string{"demo": demoManifest}),
	}
	var b strings.Builder
	added.renderFixtureFlips(&b)
	if !strings.Contains(b.String(), "manifest added this sync") {
		t.Errorf("a manifest that is new this sync is not reported:\n%s", b.String())
	}

	removed := &corpusComparison{
		fixtures: fixtures,
		before:   demoSide(t, map[string]string{"demo": demoManifest}),
		after:    demoSide(t, nil),
	}
	b.Reset()
	removed.renderFixtureFlips(&b)
	if !strings.Contains(b.String(), "manifest no longer vendored") {
		t.Errorf("a manifest that vanished upstream is not reported:\n%s", b.String())
	}
}

// TestAFixtureWithNoManifestOnEitherSideIsNamedRatherThanDropped is the third
// shape: a fixture directory for an agent nothing vendors a manifest for. It is
// not a flip, and it must not be silence either.
func TestAFixtureWithNoManifestOnEitherSideIsNamedRatherThanDropped(t *testing.T) {
	c := &corpusComparison{
		fixtures: []corpusFixture{demoFixture("idle\n")},
		before:   demoSide(t, nil),
		after:    demoSide(t, nil),
	}
	var b strings.Builder
	c.renderFixtureFlips(&b)
	if !strings.Contains(b.String(), "has no vendored `demo.toml` on either side") {
		t.Errorf("a fixture no side can classify is not named:\n%s", b.String())
	}
}

// TestRedundancyIgnoresTheRuleIDForAdditionsAndReadsItForRewrites pins the
// asymmetry that makes the two checks work.
//
// Folding the rule id into an addition's comparison made REDUNDANT unreachable
// in scripts/herdr-diff.sh, because a `sidecar.` id can never equal the
// upstream id that wins without it. That was a real defect found in the Phase 2
// review. A rewrite is the opposite case: it carries upstream's own id, is
// never a deletion candidate, and reading the id and the visible flags is what
// tells "no fixture covers this" apart from "a fixture covers it and the badge
// is the same".
func TestRedundancyIgnoresTheRuleIDForAdditionsAndReadsItForRewrites(t *testing.T) {
	with := corpusVerdict{state: "working", rule: "sidecar.working_footer", visibleWorking: true}
	without := corpusVerdict{state: "working", rule: "spinner_status_working", visibleWorking: true}

	if !with.sameBadge(without) {
		t.Error("an addition reaching the same state through a different rule must read as redundant")
	}
	if with.sameVerdict(without) || with.sameEvidence(without) {
		t.Error("the flip and rewrite comparisons must both notice the rule id")
	}

	flagged := corpusVerdict{state: "blocked", rule: "weak_blocker", visibleBlocker: true}
	unflagged := corpusVerdict{state: "blocked", rule: "weak_blocker"}
	if !flagged.sameBadge(unflagged) {
		t.Error("the badge comparison is state and fallback only; a flag must not move it")
	}
	if flagged.sameEvidence(unflagged) {
		t.Error("a rewrite that only adds visible_blocker must not read as changing nothing")
	}
}

// TestHarnessExemptionsAreScopedToTheOverlayThatDeclaresThem is the whitelist's
// one load-bearing property: `sidecar.overlay_retain` exists in both claude.toml
// and grok.toml, so an unscoped list would silence one agent's rule because
// another agent's rule of the same name is exempt.
func TestHarnessExemptionsAreScopedToTheOverlayThatDeclaresThem(t *testing.T) {
	exempt := harnessExempt(map[string][]byte{
		"grok":   []byte("# harness-exempt: sidecar.overlay_retain — the title is blanked\nid = \"grok\"\n"),
		"claude": []byte("id = \"claude\"\n"),
	})
	if !exempt["grok:sidecar.overlay_retain"] {
		t.Error("the exemption grok.toml declares was not read")
	}
	if exempt["claude:sidecar.overlay_retain"] {
		t.Error("an exemption leaked from one overlay to another agent's rule of the same name")
	}
}

// TestTheDeclaredExemptionsMatchTheOverlaysOnDisk is the same check against the
// real files, so a `# harness-exempt:` line added in a shape this reader does
// not accept fails here rather than silently going unread.
func TestTheDeclaredExemptionsMatchTheOverlaysOnDisk(t *testing.T) {
	overlays, err := readSidecarOverlays()
	if err != nil {
		t.Fatalf("read overlays: %v", err)
	}
	declared := 0
	for _, data := range overlays {
		declared += strings.Count(string(data), "\n# harness-exempt: ")
	}
	if got := len(harnessExempt(overlays)); got != declared {
		t.Errorf("%d exemption line(s) in the overlays, %d read", declared, got)
	}
}

// TestTheCorpusMapsEveryFixtureDirectoryToItsManifest pins the one mapping this
// tool copies from internal/agentactivity rather than importing. The copy is
// deliberate — the sync writes the tree that package reads, so the dependency
// must not run that way in the tool itself — and this test is what keeps a
// third spelling from appearing.
func TestTheCorpusMapsEveryFixtureDirectoryToItsManifest(t *testing.T) {
	fixtures, err := loadCorpus()
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("the corpus is empty; the comparison would measure nothing")
	}
	for _, fixture := range fixtures {
		if want := agentactivity.ManifestAgentID(fixture.agent); fixture.base != want {
			t.Errorf("%s maps to %q, agentactivity maps it to %q", fixture, fixture.base, want)
		}
		if !agentactivity.HasVendoredManifest(fixture.base) {
			t.Errorf("%s has no vendored %s.toml", fixture, fixture.base)
		}
	}
}

// TestASecondSyncNamesTheFixturesAnUpstreamChangeMoved is the verdict-flip
// table against a real upstream change rather than a synthetic one: Herdr's
// "ignore Cursor Run Everything status" fix, which is exactly the shape of the
// first journey in the plan. The first sync has nothing to compare against, the
// rolled-back file is genuine upstream history, and the second sync must name
// the fixture that moved and nothing else.
func TestASecondSyncNamesTheFixturesAnUpstreamChangeMoved(t *testing.T) {
	source := herdrSource(t)
	root := t.TempDir()
	opts := options{
		ref:            "e2b85c7",
		releaseTag:     "v0.8.2",
		catalogURL:     defaultCatalogURL,
		sourceDir:      source,
		offline:        true,
		out:            filepath.Join(root, "manifests"),
		integrationOut: filepath.Join(root, "agentintegration"),
	}
	first, err := sync(opts)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if !strings.Contains(first.Body, "First sync: the output directory held no vendored manifests") {
		t.Error("a sync with nothing to compare against does not say so")
	}

	// A re-sync of the same ref changes nothing, which is the result the review
	// gate exists to produce.
	unchanged, err := sync(opts)
	if err != nil {
		t.Fatalf("unchanged re-sync: %v", err)
	}
	if !strings.Contains(unchanged.Body, "**No fixture changed verdict.**") {
		t.Errorf("a re-sync of the same ref reported a flip:\n%s", flipSection(unchanged.Body))
	}

	// Roll one vendored file back to the revision before the Cursor fix, so the
	// next sync is a real upstream change rather than a synthetic edit.
	before := showAt(t, source, "fae0b236~1", "src/detect/manifests/cursor.toml")
	path := filepath.Join(opts.out, "upstream", "cursor.toml")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatalf("roll cursor.toml back: %v", err)
	}

	moved, err := sync(opts)
	if err != nil {
		t.Fatalf("sync after the rollback: %v", err)
	}
	section := flipSection(moved.Body)
	if !strings.Contains(section, "| `cursor` | `false_positive_run_everything.txt` |") {
		t.Errorf("the fixture Herdr's Cursor fix moved is not in the flip table:\n%s", section)
	}
	if !strings.Contains(section, "1 of ") {
		t.Errorf("exactly one fixture should have moved:\n%s", section)
	}
}

// TestTheOverlaySectionJudgesEveryVendoredOverlayRule is the redundancy report
// against the real overlays. It asserts the shape rather than the verdicts:
// which rules are redundant is a finding for a maintainer to act on and changes
// as upstream moves, but every rule must be judged and the two kinds must be
// told apart.
func TestTheOverlaySectionJudgesEveryVendoredOverlayRule(t *testing.T) {
	overlays, err := readSidecarOverlays()
	if err != nil {
		t.Fatalf("read overlays: %v", err)
	}
	out, _, _ := syncIntoTemp(t)
	body, err := os.ReadFile(filepath.Join(out, "report.md"))
	if err != nil {
		t.Fatalf("read report.md: %v", err)
	}
	vendored := readVendoredManifests(filepath.Join(out, "upstream"))
	rules, err := overlayRules(overlays, vendored)
	if err != nil {
		t.Fatalf("read overlay rules: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("no overlay rules were read")
	}
	rewrites := 0
	for _, rule := range rules {
		row := "| `" + rule.base + "` | `" + rule.id + "` |"
		if !strings.Contains(string(body), row) {
			t.Errorf("overlay rule %s/%s is not judged in the report", rule.base, rule.id)
		}
		if rule.rewrite {
			rewrites++
		}
	}
	if rewrites == 0 {
		t.Error("no overlay rule was recognised as carrying an upstream id")
	}
}

// flipSection returns the verdict-flip section alone, for a readable failure.
func flipSection(body string) string {
	_, rest, ok := strings.Cut(body, "## Fixture verdict flips")
	if !ok {
		return body
	}
	if section, _, ok := strings.Cut(rest, "## Overlay rules"); ok {
		return section
	}
	return rest
}

func TestBoundReportTruncatesAtALineBoundaryAndSaysSo(t *testing.T) {
	short := "# Report\n\nnothing to see\n"
	if boundReport(short) != short {
		t.Error("a report inside the limit was rewritten")
	}
	long := strings.Repeat("a line of report text that is long enough to matter\n", 4000)
	bounded := boundReport(long)
	if len(bounded) > maxReportChars {
		t.Errorf("bounded report is %d characters, over the %d cap", len(bounded), maxReportChars)
	}
	if !strings.Contains(bounded, "was truncated") {
		t.Error("a truncated report does not say it was truncated")
	}
	if !strings.HasSuffix(strings.TrimRight(bounded, "\n"), "in full.") {
		t.Errorf("the truncation notice is not the last thing in the report:\n%s", bounded[len(bounded)-200:])
	}
}

// --- release selection -------------------------------------------------------------

// TestNewestReleaseTagPrefersTheNewestReleaseIncludingPreviews is the pin the
// differential harness and the vendored ref both follow. Herdr's preview builds
// carry the detection fixes and ship the same release binaries, so filtering
// them out pinned the tree weeks behind the manifests it vendors.
func TestNewestReleaseTagPrefersTheNewestReleaseIncludingPreviews(t *testing.T) {
	list := []byte(`[
	  {"isDraft": true,  "publishedAt": "2026-09-09T00:00:00Z", "tagName": "draft-do-not-pin"},
	  {"isDraft": false, "publishedAt": "2026-08-31T16:14:35Z", "tagName": "preview-2026-08-31-b1ff4582e968"},
	  {"isDraft": false, "publishedAt": "2026-08-19T18:00:03Z", "tagName": "v0.8.2"}
	]`)
	if got := newestReleaseTagFrom(list); got != "preview-2026-08-31-b1ff4582e968" {
		t.Errorf("newestReleaseTagFrom = %q, want the newest preview", got)
	}

	// Order is not gh's to decide: the newest release wins wherever it appears.
	reordered := []byte(`[
	  {"isDraft": false, "publishedAt": "2026-08-19T18:00:03Z", "tagName": "v0.8.2"},
	  {"isDraft": false, "publishedAt": "2026-08-31T16:14:35Z", "tagName": "preview-2026-08-31-b1ff4582e968"}
	]`)
	if got := newestReleaseTagFrom(reordered); got != "preview-2026-08-31-b1ff4582e968" {
		t.Errorf("newestReleaseTagFrom = %q on a reordered list, want the newest preview", got)
	}
}

func TestNewestReleaseTagFallsBackWhenThereIsNothingToChoose(t *testing.T) {
	for name, list := range map[string]string{
		"empty list":      `[]`,
		"drafts only":     `[{"isDraft": true, "publishedAt": "2026-09-09T00:00:00Z", "tagName": "draft"}]`,
		"not valid json":  `nope`,
		"no tag names":    `[{"isDraft": false, "publishedAt": "2026-09-09T00:00:00Z", "tagName": ""}]`,
		"gh was not able": ``,
	} {
		if got := newestReleaseTagFrom([]byte(list)); got != "" {
			t.Errorf("%s: newestReleaseTagFrom = %q, want the caller's fallback", name, got)
		}
	}
	// An offline run never asks GitHub anything.
	if got := newestReleaseTag(true); got != fallbackReleaseTag {
		t.Errorf("offline newestReleaseTag = %q, want %q", got, fallbackReleaseTag)
	}
}
