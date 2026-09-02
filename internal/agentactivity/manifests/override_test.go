package manifests

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/config"
)

// The override used throughout: one rule, an id no vendored manifest carries,
// and a marker no vendored rule matches. That combination is what makes both
// halves of "an override replaces the vendored file" checkable in one Evaluate
// - the override's rule fires and the vendored file's rules are gone.
const overrideBody = `
id = "cursor"
version = "9999.01.01.1"
min_engine_version = 1

[[rules]]
id = "local_only_blocker"
state = "blocked"
priority = 100
visible_blocker = true
region = "whole_recent"
contains = ["sidecar override marker"]
`

// cursorBlockedScreen matches a rule in the vendored cursor manifest, which is
// how a test tells "the override replaced the vendored file" apart from "the
// override was merged on top of it".
const cursorBlockedScreen = "Run this command?\nRun (once) (y)\n"

// overrideDir points the config axis at a temp directory and clears the
// per-agent load cache, then restores both. Every override test goes through it,
// so no test can read or write the developer's real ~/.config/sidecar.
func overrideDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	config.SetTestConfigPath(filepath.Join(home, "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
	resetLoadCache(t)
	dir := filepath.Join(home, OverrideDirName)
	if dir != OverrideDir() {
		t.Fatalf("OverrideDir() = %q, want %q", OverrideDir(), dir)
	}
	return dir
}

// resetLoadCache empties the per-agent sync.Once cache now and again at the end
// of the test, because Load memoises for the life of the process and an override
// written after the first Load would otherwise be invisible. The memoised
// detection setting goes with it, for the same reason: a test that writes a
// config after another test has already read one would otherwise see the other
// test's answer.
func resetLoadCache(t *testing.T) {
	t.Helper()
	clear := func() {
		loadedMu.Lock()
		defer loadedMu.Unlock()
		loadedBy = map[string]*entry{}
		resetRemoteCacheEnabled()
	}
	clear()
	t.Cleanup(clear)
}

func writeOverride(t *testing.T, dir, agent, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, agent+".toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidLocalOverrideReplacesTheVendoredManifestAndItsOverlay(t *testing.T) {
	path := writeOverride(t, overrideDir(t), "cursor", overrideBody)

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
	if source.Version != "9999.01.01.1" {
		t.Fatalf("source version = %q, want the override's own", source.Version)
	}
	if source.Diagnostic != "" {
		t.Fatalf("valid override reported a diagnostic: %s", source.Diagnostic)
	}
	// cursor is one of the eight agents with a Sidecar overlay, so this is the
	// assertion that an override is not a third merge layer.
	if !HasOverlay("cursor") {
		t.Fatal("this test needs an agent that has an overlay to be meaningful")
	}
	if source.OverlayApplied {
		t.Fatal("an override must replace the Sidecar overlay, not merge with it")
	}
	if compiled.Source != source.Label() {
		t.Fatalf("compiled source = %q, label = %q", compiled.Source, source.Label())
	}

	verdict := compiled.Evaluate(manifest.Input{Screen: "waiting: sidecar override marker\n"})
	if verdict.MatchedRule == nil || verdict.MatchedRule.ID != "local_only_blocker" {
		t.Fatalf("override rule did not match: %+v", verdict.MatchedRule)
	}
	if vendored := compiled.Evaluate(manifest.Input{Screen: cursorBlockedScreen}); vendored.MatchedRule != nil {
		t.Fatalf("a vendored rule (%s) still matched under an override that replaced the file",
			vendored.MatchedRule.ID)
	}
}

func TestInvalidLocalOverrideIsIgnoredAndTheVendoredManifestIsUsed(t *testing.T) {
	path := writeOverride(t, overrideDir(t), "cursor", "id = \"cursor\"\nthis is not toml\n")

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !source.OverlayApplied {
		t.Fatal("the vendored manifest and its overlay should both still be in use")
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "invalid") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
	if compiled.Evaluate(manifest.Input{Screen: cursorBlockedScreen}).MatchedRule == nil {
		t.Fatal("the vendored cursor manifest stopped classifying under a broken override")
	}
}

// TestOverrideDiagnosticReachesExplain is the half of "an invalid override must
// be visible" that a user actually sees: explain says the file was found, why it
// was refused, and which manifest is running instead.
func TestOverrideDiagnosticReachesExplain(t *testing.T) {
	path := writeOverride(t, overrideDir(t), "cursor", "id = \"cursor\"\nthis is not toml\n")

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	_, explain := compiled.Explain(manifest.Input{Screen: cursorBlockedScreen})
	if explain.Warning != source.Diagnostic {
		t.Fatalf("explain warning = %q, source diagnostic = %q", explain.Warning, source.Diagnostic)
	}
	if !strings.Contains(explain.Warning, path) {
		t.Fatalf("explain warning does not name the override: %q", explain.Warning)
	}
	if !strings.HasPrefix(explain.ManifestSource, "bundled cursor ") {
		t.Fatalf("explain does not say the vendored manifest is in use: %q", explain.ManifestSource)
	}
}

func TestOverrideWithAMismatchedIdIsRefused(t *testing.T) {
	body := strings.Replace(overrideBody, `id = "cursor"`, `id = "codex"`, 1)
	path := writeOverride(t, overrideDir(t), "cursor", body)

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "does not match") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
}

// TestOverrideMayDeclareTheVendoredManifestId is the case a strict
// override.ID == fileName check would get wrong: antigravity's vendored file is
// antigravity.toml and its manifest id is "agy", so an override made by copying
// that file carries an id the file name does not equal. Herdr's
// manifest_matches_agent accepts it and so must this.
func TestOverrideMayDeclareTheVendoredManifestId(t *testing.T) {
	body := strings.Replace(overrideBody, `id = "cursor"`, `id = "agy"`, 1)
	writeOverride(t, overrideDir(t), "antigravity", body)

	_, source, err := Load("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindLocalOverride {
		t.Fatalf("source kind = %q (diagnostic %q), want %q",
			source.Kind, source.Diagnostic, KindLocalOverride)
	}
}

func TestOverrideRequiringANewerEngineIsRefused(t *testing.T) {
	body := strings.Replace(overrideBody, "min_engine_version = 1",
		"min_engine_version = "+strconv.Itoa(manifest.EngineVersion+1), 1)
	path := writeOverride(t, overrideDir(t), "cursor", body)

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "requires engine") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
}

// TestOverrideCarryingAnIncompatibleRegexLoadsAndSaysWhichRulesAreDead is the
// silent false "done" this whole engine exists to prevent, arriving through the
// front door.
//
// Four vendored manifests carry `\p{Alphabetic}`, which RE2 cannot compile, and
// each has an overlay rule rewriting it. Copy one of those files into the
// override directory to tune an unrelated rule and two things happen at once:
// the incompatible pattern comes with it, and the overlay that rewrote it is
// replaced along with the vendored file. The rule is now dead. The file still
// loads, deliberately -- refusing it over a rule the user never touched is
// worse -- so the warning is the only thing standing between the user and a
// working pane reading as a finished turn.
func TestOverrideCarryingAnIncompatibleRegexLoadsAndSaysWhichRulesAreDead(t *testing.T) {
	body := strings.Replace(overrideBody,
		`contains = ["sidecar override marker"]`,
		`line_regex = ["^\\p{Alphabetic}+ working"]`, 1)
	path := writeOverride(t, overrideDir(t), "cursor", body)

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindLocalOverride {
		t.Fatalf("source kind = %q (diagnostic %q), want the override to still be in use",
			source.Kind, source.Diagnostic)
	}
	for _, want := range []string{path, "local_only_blocker", "never match", "overlay"} {
		if !strings.Contains(source.Diagnostic, want) {
			t.Fatalf("diagnostic does not mention %q: %q", want, source.Diagnostic)
		}
	}
	// The warning has to ride the compiled manifest, not only the Source: that
	// is what carries it into both explain paths.
	if compiled.Warning != source.Diagnostic {
		t.Fatalf("compiled warning = %q, source diagnostic = %q", compiled.Warning, source.Diagnostic)
	}
	_, explain := compiled.Explain(manifest.Input{Screen: "spinning working\n"})
	if explain.Warning != source.Diagnostic {
		t.Fatalf("explain warning = %q, want the diagnostic", explain.Warning)
	}
	if explain.MatchedRule != nil {
		t.Fatalf("the dead rule matched: %+v", explain.MatchedRule)
	}
}

func TestOverrideDeclaringNoManifestIdIsRefused(t *testing.T) {
	body := strings.Replace(overrideBody, "id = \"cursor\"\n", "", 1)
	path := writeOverride(t, overrideDir(t), "cursor", body)

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "no manifest id") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
}

func TestEmptyOverrideFileIsRefusedAsInvalid(t *testing.T) {
	path := writeOverride(t, overrideDir(t), "cursor", "")

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "invalid") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
}

// TestOverrideWithAByteOrderMarkIsRefusedWithAReason covers the file a user
// produces by editing a manifest in an editor that writes a BOM. TOML has no
// BOM, so the parser refuses it, and the point of the test is that the refusal
// arrives as a diagnostic naming the file rather than as a silent fallback to
// the vendored manifest.
func TestOverrideWithAByteOrderMarkIsRefusedWithAReason(t *testing.T) {
	path := writeOverride(t, overrideDir(t), "cursor", "\xef\xbb\xbf"+overrideBody)

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "invalid") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
}

func TestUnreadableOverrideIsRefusedWithAReason(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a file with no read bits")
	}
	path := writeOverride(t, overrideDir(t), "cursor", overrideBody)
	if err := os.Chmod(path, 0o200); err != nil {
		t.Fatal(err)
	}

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "could not be loaded") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
}

// TestOverridePathThatIsADirectoryIsRefusedWithAReason covers `mkdir
// cursor.toml`, which is what an interrupted copy or a misaimed `git clone`
// leaves behind.
func TestOverridePathThatIsADirectoryIsRefusedWithAReason(t *testing.T) {
	dir := overrideDir(t)
	path := filepath.Join(dir, "cursor.toml")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "could not be loaded") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
}

// TestDanglingOverrideSymlinkIsReportedRatherThanReadAsAbsent is a deliberate
// divergence from Herdr, which cannot tell a broken link from no file at all.
// A symlink is not something a user creates by accident: the plausible way to
// have one is a link into a dotfiles repository that is not checked out here, and
// "your override is not being used" is the news in that case.
func TestDanglingOverrideSymlinkIsReportedRatherThanReadAsAbsent(t *testing.T) {
	dir := overrideDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cursor.toml")
	if err := os.Symlink(filepath.Join(dir, "nowhere", "cursor.toml"), path); err != nil {
		t.Fatal(err)
	}

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled {
		t.Fatalf("source kind = %q, want %q", source.Kind, KindBundled)
	}
	if !strings.Contains(source.Diagnostic, path) || !strings.Contains(source.Diagnostic, "symlink") {
		t.Fatalf("diagnostic does not name the file and the reason: %q", source.Diagnostic)
	}
}

// TestNoOverrideAtAllReportsNothing is the negative control for the symlink
// case above: an empty override directory must stay silent, or every explain
// record on every machine grows a warning line.
func TestNoOverrideAtAllReportsNothing(t *testing.T) {
	overrideDir(t)

	_, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindBundled || source.Diagnostic != "" {
		t.Fatalf("no override produced kind %q diagnostic %q", source.Kind, source.Diagnostic)
	}
}

// TestOverrideIsReadOnceAtFirstLoadAndNotBefore is the startup-latency rule made
// checkable. Reading a user file is a filesystem hit, and it must happen at
// first use of an agent's manifest: never at package init, and never on anything
// a plugin's Init() can reach before its first tea.Cmd runs.
func TestOverrideIsReadOnceAtFirstLoadAndNotBefore(t *testing.T) {
	overrideDir(t)

	base := overrideReads.Load()
	// The cheap introspection a startup path may legitimately do. None of it
	// resolves an override.
	if _, err := Agents(); err != nil {
		t.Fatal(err)
	}
	HasOverlay("cursor")
	OverridePath("cursor")
	if got := overrideReads.Load(); got != base {
		t.Fatalf("%d override reads before the first Load; want none", got-base)
	}

	if _, _, err := Load("cursor"); err != nil {
		t.Fatal(err)
	}
	if got := overrideReads.Load() - base; got != 1 {
		t.Fatalf("first Load did %d override reads, want exactly 1", got)
	}
	if _, _, err := Load("cursor"); err != nil {
		t.Fatal(err)
	}
	if got := overrideReads.Load() - base; got != 1 {
		t.Fatalf("second Load re-read the override; total reads %d, want 1", got)
	}
	if _, _, err := Load("codex"); err != nil {
		t.Fatal(err)
	}
	if got := overrideReads.Load() - base; got != 2 {
		t.Fatalf("a second agent did %d reads in total, want 2", got)
	}
}

// TestTestsNeverReadTheRealOverrideDirectory pins the guard that makes every
// other test in this file safe to run on a developer's machine. With no config
// override in place the path resolves into the real ~/.config/sidecar, and a
// test binary asserts isolated state, so the loader must decline to open it.
func TestTestsNeverReadTheRealOverrideDirectory(t *testing.T) {
	resetLoadCache(t)
	config.ResetTestConfigPath()

	path := OverridePath("cursor")
	if path == "" {
		t.Skip("no home directory to resolve a real config path from")
	}
	if !strings.HasPrefix(path, config.RealUserConfigDir()) {
		t.Fatalf("unisolated override path %q is not under %q", path, config.RealUserConfigDir())
	}
	if err := config.AssertIsolatedPath(path); err == nil {
		t.Fatal("a test binary was allowed to resolve the real override directory")
	}

	before := overrideReads.Load()
	override, gotPath, diagnostic := readOverride("cursor", nil)
	if override != nil || gotPath != "" || diagnostic != "" {
		t.Fatalf("readOverride returned %v, %q, %q from the real config directory", override, gotPath, diagnostic)
	}
	if overrideReads.Load() != before {
		t.Fatal("readOverride opened a path inside the real config directory")
	}
}

// TestExplainNamesTheRequestedAgentNotTheOverridesId is the record defect an
// override is the first thing to expose. Herdr reports the agent that was asked
// about (agent_label, manifest.rs:501), not the id of the file that answered;
// those coincide for every vendored manifest, so nothing before overrides could
// tell them apart. An override may legitimately declare one agent's id and carry
// another's alias -- manifest_matches_agent accepts it and so does this loader --
// and the record must still name the pane's agent rather than the file's.
func TestExplainNamesTheRequestedAgentNotTheOverridesId(t *testing.T) {
	body := strings.Replace(overrideBody, `id = "cursor"`,
		"id = \"claude\"\naliases = [\"cursor\"]", 1)
	writeOverride(t, overrideDir(t), "cursor", body)

	compiled, source, err := Load("cursor")
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != KindLocalOverride {
		t.Fatalf("source kind = %q (diagnostic %q), want the alias to be accepted",
			source.Kind, source.Diagnostic)
	}
	_, explain := compiled.Explain(manifest.Input{
		Agent:  "cursor",
		Screen: "waiting: sidecar override marker\n",
	})
	if explain.Agent != "cursor" {
		t.Fatalf("explain agent = %q, want the requested agent; the manifest id is %q",
			explain.Agent, compiled.Manifest.ID)
	}
}
