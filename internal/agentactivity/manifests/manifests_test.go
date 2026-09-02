package manifests

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
)

func loadLock(t *testing.T) *Lock {
	t.Helper()
	lock, err := LoadLock()
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	return lock
}

func TestAllVendoredManifestsParseAndValidate(t *testing.T) {
	lock := loadLock(t)
	if len(lock.Agents) == 0 {
		t.Fatal("the lock names no agents")
	}
	for _, agent := range lock.Agents {
		t.Run(agent.ID, func(t *testing.T) {
			data := vendoredBytes(t, agent)
			// AllowIncompatibleRegex mirrors the sync tool: a pattern RE2
			// cannot compile is a recorded incompatibility, not a broken file.
			// TestEveryVendoredRegexCompilesUnderGoRegexp is what watches that set.
			m, err := manifest.ParseRemoteWith(data, manifest.ValidateOptions{AllowIncompatibleRegex: true})
			if err != nil {
				t.Fatalf("%s failed to parse or validate: %v", agent.Path, err)
			}
			if m.ID != agent.ID {
				t.Errorf("%s declares id %q, lock says %q", agent.Path, m.ID, agent.ID)
			}
			if m.Version != agent.Version {
				t.Errorf("%s declares version %q, lock says %q", agent.Path, m.Version, agent.Version)
			}
			if len(m.Rules) != agent.RuleCount {
				t.Errorf("%s has %d rules, lock says %d", agent.Path, len(m.Rules), agent.RuleCount)
			}
		})
	}
}

func vendoredBytes(t *testing.T, agent LockAgent) []byte {
	t.Helper()
	name := strings.TrimPrefix(agent.Path, "upstream/")
	data, err := UpstreamBytes(name)
	if err != nil {
		t.Fatalf("read %s: %v", agent.Path, err)
	}
	return data
}

func TestVendoredManifestsMatchLock(t *testing.T) {
	lock := loadLock(t)
	seen := map[string]bool{}
	for _, agent := range lock.Agents {
		data := vendoredBytes(t, agent)
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != agent.SHA256 {
			t.Errorf("%s sha256 is %s, lock says %s.\n"+
				"Vendored files are byte-for-byte upstream copies and are never edited by hand.\n"+
				"Put the change in internal/agentactivity/manifests/sidecar/<agent>.toml as an overlay,\n"+
				"or re-run scripts/sync-herdr.sh if upstream changed.", agent.Path, got, agent.SHA256)
		}
		if len(data) != agent.Bytes {
			t.Errorf("%s is %d bytes, lock says %d", agent.Path, len(data), agent.Bytes)
		}
		seen[strings.TrimPrefix(agent.Path, "upstream/")] = true
	}

	// Every vendored .toml must be pinned; an unpinned file is one the lock
	// test cannot protect.
	dir, err := Upstream()
	if err != nil {
		t.Fatalf("Upstream: %v", err)
	}
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		t.Fatalf("read upstream: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".toml") || name == "index.toml" {
			continue
		}
		if !seen[name] {
			t.Errorf("upstream/%s is vendored but not named in upstream.lock.json", name)
		}
	}

	indexData, err := UpstreamBytes("index.toml")
	if err != nil {
		t.Fatalf("read index.toml: %v", err)
	}
	sum := sha256.Sum256(indexData)
	if got := hex.EncodeToString(sum[:]); got != lock.Catalog.SHA256 {
		t.Errorf("upstream/index.toml sha256 is %s, lock says %s", got, lock.Catalog.SHA256)
	}
}

// knownRegexIncompatibilities pins the upstream patterns that Rust's regex
// crate accepts and Go's RE2 cannot compile, so a *new* one fails CI on the
// sync pull request rather than surfacing as a dead rule in a user's pane.
// Each entry is "<file> <rule id> <field>". See
// docs/reference/herdr-detection-parity.md for the rewrite each one needs.
var knownRegexIncompatibilities = map[string]string{
	"antigravity.toml spinner_working line_regex": `\p{Alphabetic}`,
	"cursor.toml spinner_working line_regex":      `\p{Alphabetic}`,
	"kiro.toml tool_spinner_working line_regex":   `\p{Alphabetic}`,
	"qodercli.toml spinner_working line_regex":    `\p{Alphabetic}`,
}

func TestEveryVendoredRegexCompilesUnderGoRegexp(t *testing.T) {
	lock := loadLock(t)
	compiled, failed := 0, 0
	unexpected := map[string]bool{}
	for _, agent := range lock.Agents {
		file := strings.TrimPrefix(agent.Path, "upstream/")
		m, err := manifest.Parse(vendoredBytes(t, agent))
		if err != nil {
			t.Fatalf("parse %s: %v", agent.Path, err)
		}
		for _, pattern := range m.Patterns() {
			key := fmt.Sprintf("%s %s %s", file, pattern.RuleID, pattern.Field)
			if _, err := manifest.CompileRegex(pattern.Pattern); err != nil {
				failed++
				if _, known := knownRegexIncompatibilities[key]; !known {
					unexpected[key] = true
					t.Errorf("new regex incompatibility in %s rule %s (%s):\n  pattern: %s\n  error:   %v",
						agent.Path, pattern.RuleID, pattern.Field, pattern.Pattern, err)
				}
				continue
			}
			compiled++
		}
	}
	if failed != len(knownRegexIncompatibilities) && len(unexpected) == 0 {
		t.Errorf("%d pattern(s) failed to compile but %d are pinned as known; "+
			"a rewrite may have landed upstream, so update knownRegexIncompatibilities",
			failed, len(knownRegexIncompatibilities))
	}
	t.Logf("%d patterns compiled under RE2, %d need a rewrite", compiled, failed)
}

func TestVendoredManifestsDeclareEngineVersionWithinRange(t *testing.T) {
	lock := loadLock(t)
	if lock.EngineVersion != manifest.EngineVersion {
		t.Fatalf("lock engine_version is %d, package EngineVersion is %d",
			lock.EngineVersion, manifest.EngineVersion)
	}
	for _, agent := range lock.Agents {
		if agent.MinEngineVersion < 1 {
			t.Errorf("%s declares min_engine_version %d, want at least 1", agent.Path, agent.MinEngineVersion)
		}
		if agent.MinEngineVersion > manifest.EngineVersion {
			t.Errorf("%s needs engine %d and this engine is %d; it should never have been vendored",
				agent.Path, agent.MinEngineVersion, manifest.EngineVersion)
		}
		m, err := manifest.Parse(vendoredBytes(t, agent))
		if err != nil {
			t.Fatalf("parse %s: %v", agent.Path, err)
		}
		if m.MinEngineVersion == nil || *m.MinEngineVersion != agent.MinEngineVersion {
			t.Errorf("%s declares min_engine_version %v, lock says %d",
				agent.Path, m.MinEngineVersion, agent.MinEngineVersion)
		}
		// A rule using top_non_empty_lines only exists at engine 3.
		for _, rule := range m.Rules {
			if rule.RegionSpec().Kind == manifest.RegionTopNonEmptyLines &&
				agent.MinEngineVersion < manifest.TopNonEmptyLinesEngineVersion {
				t.Errorf("%s rule %s uses top_non_empty_lines at min_engine_version %d",
					agent.Path, rule.ID, agent.MinEngineVersion)
			}
		}
	}
}

func TestLockRecordsBothManifestSourceKinds(t *testing.T) {
	lock := loadLock(t)
	for _, agent := range lock.Agents {
		switch agent.Source {
		case SourceBundled, SourcePublished:
		default:
			t.Errorf("%s has source %q, want %q or %q", agent.ID, agent.Source, SourceBundled, SourcePublished)
		}
		if agent.SourceReason == "" {
			t.Errorf("%s records no reason for choosing %s", agent.ID, agent.Source)
		}
		if agent.Source == SourceBundled && agent.PublishedVersion != "" &&
			manifest.CompareVersions(agent.BundledVersion, agent.PublishedVersion) <= 0 {
			t.Errorf("%s vendors the bundled copy (%s) although the published one (%s) is not older",
				agent.ID, agent.BundledVersion, agent.PublishedVersion)
		}
		if agent.Source == SourcePublished &&
			manifest.CompareVersions(agent.PublishedVersion, agent.BundledVersion) < 0 {
			t.Errorf("%s vendors the published copy (%s) although it is older than the bundled one (%s)",
				agent.ID, agent.PublishedVersion, agent.BundledVersion)
		}
	}
}

func TestCatalogIndexListsEveryPublishedAgent(t *testing.T) {
	lock := loadLock(t)
	indexData, err := UpstreamBytes("index.toml")
	if err != nil {
		t.Fatalf("read index.toml: %v", err)
	}
	index := string(indexData)
	if !strings.Contains(index, "schema_version = 1") {
		t.Error("upstream/index.toml does not declare schema_version = 1")
	}
	for _, agent := range lock.Agents {
		if agent.Source != SourcePublished {
			continue
		}
		if !strings.Contains(index, fmt.Sprintf("id = %q", agent.ID)) {
			t.Errorf("%s is vendored from the published catalog but index.toml does not list it", agent.ID)
		}
	}
}

// TestVendoredAttributionFilesMatchLock digest-pins NOTICE and LICENSE. They
// are the attribution the whole vendored tree rests on, so an edit to either
// has to fail here rather than pass a substring check.
func TestVendoredAttributionFilesMatchLock(t *testing.T) {
	lock := loadLock(t)
	for _, want := range []string{"upstream/LICENSE", "upstream/NOTICE"} {
		file, ok := lock.File(want)
		if !ok {
			t.Errorf("upstream.lock.json does not pin %s; re-run scripts/sync-herdr.sh", want)
			continue
		}
		data, err := UpstreamBytes(strings.TrimPrefix(want, "upstream/"))
		if err != nil {
			t.Errorf("read %s: %v", want, err)
			continue
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != file.SHA256 {
			t.Errorf("%s sha256 is %s, lock says %s.\n"+
				"LICENSE is an upstream copy and NOTICE is generated by the sync tool;\n"+
				"neither is edited by hand. Re-run scripts/sync-herdr.sh.", want, got, file.SHA256)
		}
		if len(data) != file.Bytes {
			t.Errorf("%s is %d bytes, lock says %d", want, len(data), file.Bytes)
		}
		if file.Origin == "" {
			t.Errorf("%s records no origin", want)
		}
	}
	if notice, ok := lock.File("upstream/NOTICE"); ok && notice.Origin != GeneratedNotice {
		t.Errorf("upstream/NOTICE origin = %q, want %q", notice.Origin, GeneratedNotice)
	}
}

func TestNoticeAndLicenseTravelWithTheVendoredFiles(t *testing.T) {
	notice, err := UpstreamBytes("NOTICE")
	if err != nil {
		t.Fatalf("read NOTICE: %v", err)
	}
	for _, want := range []string{"Herdr", "https://github.com/herdrdev/herdr", "Apache License", "unmodified copies"} {
		if !strings.Contains(string(notice), want) {
			t.Errorf("NOTICE does not mention %q", want)
		}
	}
	lock := loadLock(t)
	if lock.Herdr.Commit != "" && !strings.Contains(string(notice), lock.Herdr.Commit) {
		t.Errorf("NOTICE does not name the vendored commit %s", lock.Herdr.Commit)
	}
	license, err := UpstreamBytes("LICENSE")
	if err != nil {
		t.Fatalf("read LICENSE: %v", err)
	}
	if !strings.Contains(string(license), "Apache License") {
		t.Error("upstream/LICENSE is not the Apache licence text")
	}
}

func TestAliasesCoverEveryVendoredAgent(t *testing.T) {
	aliases, err := LoadAliases()
	if err != nil {
		t.Fatalf("LoadAliases: %v", err)
	}
	lock := loadLock(t)
	for _, agent := range lock.Agents {
		names, ok := aliases.Agents[agent.ID]
		if !ok {
			t.Errorf("aliases.upstream.json has no entry for %s", agent.ID)
			continue
		}
		if !slicesContain(names, agent.ID) {
			t.Errorf("aliases for %s do not include the canonical id: %v", agent.ID, names)
		}
	}
	for _, runtime := range []string{"sh", "bash", "zsh", "fish", "tmux", "node", "bun", "cmd", "powershell", "pwsh"} {
		if !slicesContain(aliases.GenericRuntimes, runtime) {
			t.Errorf("generic runtime list is missing %q", runtime)
		}
	}
	if aliases.PythonRuntimeRule == "" {
		t.Error("the python runtime rule was not recorded")
	}
	if aliases.VersionedBinaryPrefixes["muse"] != "muse-bin-" {
		t.Errorf("versioned binary prefixes = %v, want muse-bin- for muse", aliases.VersionedBinaryPrefixes)
	}
}

func TestAuthorityCoversEveryVendoredAgent(t *testing.T) {
	authority, err := LoadAuthority()
	if err != nil {
		t.Fatalf("LoadAuthority: %v", err)
	}
	lock := loadLock(t)
	for _, agent := range lock.Agents {
		entry, ok := authority.Agents[agent.ID]
		if !ok {
			t.Errorf("authority.upstream.json has no entry for %s", agent.ID)
			continue
		}
		switch entry.LifecycleAuthority {
		case AuthorityHooks, AuthoritySessionIdentity, AuthorityNone:
		default:
			t.Errorf("%s has lifecycle_authority %q", agent.ID, entry.LifecycleAuthority)
		}
	}
	// The plan's research record: six agents where Herdr's integration is the
	// state authority. A change here is a real upstream change worth reviewing.
	var hooks []string
	for id, entry := range authority.Agents {
		if entry.LifecycleAuthority == AuthorityHooks {
			hooks = append(hooks, id)
		}
	}
	sort.Strings(hooks)
	want := []string{"kilo", "kimi", "mastracode", "omp", "opencode", "pi"}
	if strings.Join(hooks, ",") != strings.Join(want, ",") {
		t.Errorf("agents with hooks authority = %v, want %v", hooks, want)
	}
}

func TestUpstreamAccessorDoesNotExposeTheDirectoryPrefix(t *testing.T) {
	dir, err := Upstream()
	if err != nil {
		t.Fatalf("Upstream: %v", err)
	}
	if _, err := fs.ReadFile(dir, "claude.toml"); err != nil {
		t.Fatalf("claude.toml is not reachable at the root of the upstream FS: %v", err)
	}
	if _, err := fs.ReadFile(dir, "upstream/claude.toml"); err == nil {
		t.Error("the upstream FS still carries the upstream/ prefix")
	}
}

// TestNoVendoredFileIsRewritten guards the one thing a byte-for-byte copy must
// never pick up: a translated regex. The dialect translation belongs in the
// engine, never in the vendored bytes.
func TestNoVendoredFileIsRewritten(t *testing.T) {
	lock := loadLock(t)
	uEscape := regexp.MustCompile(`\\u\{?[0-9a-fA-F]`)
	sawU := false
	for _, agent := range lock.Agents {
		if uEscape.MatchString(string(vendoredBytes(t, agent))) {
			sawU = true
			break
		}
	}
	if !sawU {
		t.Error("no vendored file contains a Rust \\u escape any more; either upstream changed " +
			"its patterns or something rewrote the vendored bytes")
	}
}

func slicesContain(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
