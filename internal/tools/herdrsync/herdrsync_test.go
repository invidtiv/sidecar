package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifests"
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

// syncIntoTemp runs a full offline sync into a temp directory and returns the
// output directory plus the lock it wrote.
func syncIntoTemp(t *testing.T) (string, *manifests.Lock, string) {
	t.Helper()
	source := herdrSource(t)
	out := t.TempDir()
	report, err := sync(options{
		ref:        "e2b85c7",
		releaseTag: "v0.8.2",
		catalogURL: defaultCatalogURL,
		sourceDir:  source,
		offline:    true,
		out:        out,
	})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return out, report.Lock, source
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
		ref:        "no-such-ref-0000000",
		releaseTag: "v0.8.2",
		catalogURL: defaultCatalogURL,
		sourceDir:  dir,
		offline:    true,
		out:        t.TempDir(),
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
	if _, err := sync(options{sourceDir: filepath.Join(t.TempDir(), "nope"), offline: true, out: t.TempDir()}); err == nil {
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
		ref:        "e2b85c7",
		releaseTag: "v0.8.2",
		catalogURL: defaultCatalogURL,
		sourceDir:  herdrCheckout,
		offline:    true,
		out:        t.TempDir(),
	}); err == nil {
		t.Error("sync wrote a report from a working directory outside the Sidecar repository")
	}
}

func TestSyncRefusesOfflineWithoutACheckout(t *testing.T) {
	if _, err := sync(options{offline: true, out: t.TempDir()}); err == nil {
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

func TestExtractAuthorityFromAnInlineSnippet(t *testing.T) {
	authority, err := extractAuthority(stubAssets(), "stub")
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
	if _, err := extractAuthority(src, "stub"); err == nil {
		t.Fatal("extractAuthority accepted a display name with no agent id mapping")
	}
}

func TestExtractAuthorityFailsOnDisagreeingAssetVersions(t *testing.T) {
	src := stubAssets()
	src.files[assetsDir+"/claude/herdr-agent-state.ps1"] = "# HERDR_INTEGRATION_VERSION=7\n"
	if _, err := extractAuthority(src, "stub"); err == nil {
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

	authority, err := extractAuthority(src, "e2b85c7")
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
		"Engine not yet wired; see Phase 1.",
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
