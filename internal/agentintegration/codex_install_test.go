package agentintegration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// The Codex adapter suite runs entirely inside t.TempDir with an injected Env,
// for the same reason the Claude suite does: the real ~/.codex on a developer
// machine carries live hooks and live trust records, and a suite that touched
// them would be rewriting the trust decisions of a tool the user runs.

func codexFixture(t *testing.T, opts ...func(*Env)) (Service, Env, codexPaths) {
	t.Helper()
	home := t.TempDir()
	env := Env{
		Home:       home,
		ConfigHome: filepath.Join(home, ".config"),
		LookPath: func(file string) (string, error) {
			if file == CodexProvider {
				return filepath.Join(home, "bin", "codex"), nil
			}
			return "", errors.New("not found")
		},
		ProviderVersion: func(string) string { return "0.151.0" },
		UID:             os.Getuid(),
	}
	for _, o := range opts {
		o(&env)
	}
	return Service{Env: env, Adapters: DefaultAdapters()}, env, codexPathsFor(env)
}

func codexStatus(t *testing.T, s Service) Status {
	t.Helper()
	st, err := s.Status(CodexProvider)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return st
}

// TestCodexTrustedHashReproducesALiveTrustRecord pins the trusted_hash
// algorithm against a vector captured from a real codex-cli 0.151.0
// installation: the recorded [hooks.state] hash for herdr's SessionStart hook,
// reproduced byte for byte from that hook's definition. The algorithm is a
// provider implementation detail reproduced from the codex-rs source
// (hook_hash + version_for_toml), so this fixed vector is what stands between
// "Sidecar writes trust records Codex accepts" and a silent re-prompt on every
// session.
func TestCodexTrustedHashReproducesALiveTrustRecord(t *testing.T) {
	got := codexTrustedHash("bash '/Users/marcus/.codex/herdr-agent-state.sh' session", 10)
	want := "sha256:3ba226dc8a801008979970c6fc0be17f922060591382e1408aa745718925abcf"
	if got != want {
		t.Fatalf("trusted hash algorithm drifted:\n got %s\nwant %s", got, want)
	}
}

func TestCodexBundledEntryMatchesTheRegistry(t *testing.T) {
	asset := (CodexAdapter{}).Asset()
	capability, known := agentlifecycle.CapabilityForSource(asset.Source)
	if !known {
		t.Fatalf("no capability entry for %s", asset.Source)
	}
	if capability.AssetVersion != asset.Version {
		t.Fatalf("asset version %q but registry records %q", asset.Version, capability.AssetVersion)
	}
	// Session identity only: Codex's shipped hook must never carry lifecycle
	// authority, whatever the upstream event vocabulary would allow. Lifecycle
	// tiers are Phase D's to earn with traces.
	if capability.Tier != agentlifecycle.TierSessionIdentity {
		t.Fatalf("tier %q, want session-identity", capability.Tier)
	}
	if !reflect.DeepEqual(capability.Covered, []agentlifecycle.Transition{agentlifecycle.TransitionSessionIdentity}) {
		t.Fatalf("covered %v claims more than the shipped hook proves", capability.Covered)
	}
	if want := "sidecar agent report-session --kind codex --hook-stdin"; reportSessionCommand(CodexProvider) != want {
		t.Fatalf("command %q, want %q", reportSessionCommand(CodexProvider), want)
	}
}

func TestCodexInstallIntoACleanTreeWritesAllThreeMutations(t *testing.T) {
	svc, _, paths := codexFixture(t)

	if got := codexStatus(t, svc).Status; got != agentlifecycle.StatusNotInstalled {
		t.Fatalf("before install: %s", got)
	}

	p := applyTo(t, svc, CodexProvider, ActionInstall)
	if p.Unchanged {
		t.Fatal("the first install reported unchanged")
	}
	if p.StatusAfter != agentlifecycle.StatusCurrent {
		t.Fatalf("after install: %s", p.StatusAfter)
	}

	wantHooks := `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "sidecar agent report-session --kind codex --hook-stdin",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
`
	if got := readFileForTest(t, paths.Hooks); got != wantHooks {
		t.Fatalf("hooks.json:\n%s\nwant:\n%s", got, wantHooks)
	}
	// Note: no matcher key — Codex groups carry none, unlike Claude's.
	if strings.Contains(wantHooks, "matcher") {
		t.Fatal("the codex fixture grew a matcher")
	}

	wantConfig := "[features]\nhooks = true\n\n" +
		"[hooks.state.\"" + paths.Hooks + ":session_start:0:0\"]\n" +
		"trusted_hash = \"" + codexTrustHashes()[0] + "\"\n"
	if got := readFileForTest(t, paths.Config); got != wantConfig {
		t.Fatalf("config.toml:\n%s\nwant:\n%s", got, wantConfig)
	}

	st := codexStatus(t, svc)
	if st.Status != agentlifecycle.StatusCurrent {
		t.Fatalf("status after install: %s (%s)", st.Status, st.Message)
	}
	if st.EffectiveTier != agentlifecycle.TierSessionIdentity {
		t.Fatalf("tier %q, want session-identity", st.EffectiveTier)
	}

	again := applyTo(t, svc, CodexProvider, ActionInstall)
	if !again.Unchanged || len(again.Ops) != 0 {
		t.Fatalf("reinstall over a current install was not a visible no-op: %+v", again)
	}
}

func TestCodexInstallPreservesForeignHooksTrustAndComments(t *testing.T) {
	svc, _, paths := codexFixture(t)
	originalHooks := `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "bash '/Users/someone/.codex/herdr-agent-state.sh' session",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
`
	// A realistic config.toml: comments, unrelated keys, an existing feature
	// flag block, and a foreign trust record (herdr's) that must survive
	// byte for byte.
	originalConfig := `# managed by hand, do not lose my comments
model = "gpt-5.6"

[features]
hooks = true # herdr needs this
memories = true

[hooks.state]

[hooks.state."` + paths.Hooks + `:session_start:0:0"]
trusted_hash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`
	writeFileForTest(t, paths.Hooks, originalHooks)
	writeFileForTest(t, paths.Config, originalConfig)

	applyTo(t, svc, CodexProvider, ActionInstall)

	// The foreign hook survives; Sidecar's group is appended after it.
	hooks := mustParseAny(t, readFileForTest(t, paths.Hooks)).(map[string]any)["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(hooks) != 2 {
		t.Fatalf("%d SessionStart groups, want herdr's plus Sidecar's", len(hooks))
	}
	if !reflect.DeepEqual(hooks[0], mustParseAny(t, originalHooks).(map[string]any)["hooks"].(map[string]any)["SessionStart"].([]any)[0]) {
		t.Fatal("the foreign hook group changed")
	}

	config := readFileForTest(t, paths.Config)
	for _, want := range []string{
		"# managed by hand, do not lose my comments",
		`model = "gpt-5.6"`,
		"hooks = true # herdr needs this",
		"memories = true",
		`trusted_hash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config.toml lost %q:\n%s", want, config)
		}
	}
	// Sidecar's trust record is keyed to its actual position: group 1, after
	// the foreign group.
	wantKey := paths.Hooks + ":session_start:1:0"
	if !strings.Contains(config, "[hooks.state.\""+wantKey+"\"]") {
		t.Fatalf("config.toml lacks the positional trust key %q:\n%s", wantKey, config)
	}
	if !strings.Contains(config, codexTrustHashes()[0]) {
		t.Fatalf("config.toml lacks Sidecar's trusted_hash:\n%s", config)
	}

	st := codexStatus(t, svc)
	if st.Status != agentlifecycle.StatusCurrent {
		t.Fatalf("status: %s (%s)", st.Status, st.Message)
	}

	// Uninstall removes exactly Sidecar's entry and Sidecar's trust record.
	// hooks.json returns to its original bytes; config.toml keeps everything
	// else including the feature flag Sidecar found already on.
	applyTo(t, svc, CodexProvider, ActionUninstall)
	if got := readFileForTest(t, paths.Hooks); got != originalHooks {
		t.Fatalf("uninstall did not restore hooks.json:\n%s\nwant:\n%s", got, originalHooks)
	}
	if got := readFileForTest(t, paths.Config); got != originalConfig {
		t.Fatalf("uninstall did not restore config.toml:\n%s\nwant:\n%s", got, originalConfig)
	}
}

func TestCodexInstallEnablesTheFeatureFlagItsHookNeeds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
	}{
		{"no config.toml at all", ""},
		{"features table without hooks", "[features]\nmemories = true\n"},
		{"hooks explicitly disabled", "[features]\nhooks = false\n"},
		{"no features table", "model = \"gpt-5.6\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, paths := codexFixture(t)
			if tc.config != "" {
				writeFileForTest(t, paths.Config, tc.config)
			}
			applyTo(t, svc, CodexProvider, ActionInstall)
			config := readFileForTest(t, paths.Config)
			scan := scanCodexConfig(true, []byte(config))
			if scan.parseErr != "" || !scan.hooksEnabled() {
				t.Fatalf("features.hooks is not enabled after install:\n%s", config)
			}
			if got := codexStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
				t.Fatalf("status: %s", got)
			}
			// The pre-existing unrelated lines survived.
			for _, keep := range []string{"memories = true", `model = "gpt-5.6"`} {
				if strings.Contains(tc.config, keep) && !strings.Contains(config, keep) {
					t.Fatalf("config.toml lost %q:\n%s", keep, config)
				}
			}
		})
	}
}

func TestCodexUninstallLeavesTheFeatureFlagOn(t *testing.T) {
	svc, _, paths := codexFixture(t)
	applyTo(t, svc, CodexProvider, ActionInstall)
	applyTo(t, svc, CodexProvider, ActionUninstall)

	// hooks.json held only Sidecar's entry and is gone; config.toml keeps the
	// flag deliberately — other hooks may depend on it, and disabling a
	// feature the user's other tools rely on is not an uninstall's business.
	if _, err := os.Lstat(paths.Hooks); !os.IsNotExist(err) {
		t.Fatal("a hooks.json that held nothing but Sidecar's entry was left behind")
	}
	config := readFileForTest(t, paths.Config)
	scan := scanCodexConfig(true, []byte(config))
	if !scan.hooksEnabled() {
		t.Fatalf("uninstall disabled features.hooks:\n%s", config)
	}
	if strings.Contains(config, "hooks.state") || strings.Contains(config, "trusted_hash") {
		t.Fatalf("uninstall left trust records behind:\n%s", config)
	}
	if got := codexStatus(t, svc).Status; got != agentlifecycle.StatusNotInstalled {
		t.Fatalf("after uninstall: %s", got)
	}
}

func TestCodexDryRunAndTheRealRunDescribeTheSameOperations(t *testing.T) {
	svc, env, _ := codexFixture(t)
	preview, err := svc.Plan(CodexProvider, ActionInstall)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, env.Home)

	real := applyTo(t, svc, CodexProvider, ActionInstall)

	preview.DryRun, real.Applied, real.StatusAfter = false, false, preview.StatusAfter
	pb, _ := json.Marshal(preview)
	rb, _ := json.Marshal(real)
	if string(pb) != string(rb) {
		t.Fatalf("dry-run and real run describe different plans:\n%s\n%s", pb, rb)
	}
	if reflect.DeepEqual(before, snapshot(t, env.Home)) {
		t.Fatal("the real run changed nothing")
	}
}

func TestCodexNeverAdoptsAForeignLookalikeAnywhere(t *testing.T) {
	svc, _, paths := codexFixture(t)
	originalHooks := `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "echo sidecar agent report-session"
          },
          {
            "type": "command",
            "command": "sidecar agent report-session-v2 --kind codex"
          }
        ]
      }
    ]
  }
}
`
	// A foreign trust record whose KEY resembles Sidecar's but whose hash is
	// someone else's, at a position Sidecar's entry does not occupy.
	originalConfig := "[features]\nhooks = true\n\n" +
		"[hooks.state.\"" + paths.Hooks + ":session_start:0:0\"]\n" +
		"trusted_hash = \"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"\n"
	writeFileForTest(t, paths.Hooks, originalHooks)
	writeFileForTest(t, paths.Config, originalConfig)

	if got := codexStatus(t, svc).Status; got != agentlifecycle.StatusNotInstalled {
		t.Fatalf("lookalikes read as %s, want not-installed", got)
	}

	applyTo(t, svc, CodexProvider, ActionInstall)
	applyTo(t, svc, CodexProvider, ActionUninstall)
	if got := readFileForTest(t, paths.Hooks); got != originalHooks {
		t.Fatalf("foreign lookalike hooks were touched:\n%s", got)
	}
	if got := readFileForTest(t, paths.Config); got != originalConfig {
		t.Fatalf("the foreign trust record was touched:\n%s", got)
	}
}

func TestCodexMalformedFilesRefuseRatherThanClobber(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hooks  string
		config string
	}{
		{"invalid hooks.json", `{"hooks": [`, ""},
		{"hooks.json event is an object", `{"hooks": {"SessionStart": {}}}`, ""},
		{"config.toml multiline string", "", "[features]\nhooks = true\nnote = \"\"\"\n[features]\n\"\"\"\n"},
		{"config.toml inline features", "", "features = { hooks = true }\n"},
		{"config.toml dotted state", "", "[hooks]\nstate.\"k\" = { trusted_hash = \"sha256:cc\" }\n"},
		{"config.toml unparseable flag", "", "[features]\nhooks = \"yes\"\n"},
		{"config.toml features.hooks is a table", "", "[features.hooks]\nenabled = true\n"},
		{"config.toml nested trust table", "", "[hooks.state.\"k\".extra]\nnote = \"x\"\n"},
		{"config.toml hooks as an array of tables", "", "[[hooks]]\nname = \"x\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, paths := codexFixture(t)
			if tc.hooks != "" {
				writeFileForTest(t, paths.Hooks, tc.hooks)
			}
			if tc.config != "" {
				writeFileForTest(t, paths.Config, tc.config)
			}
			before := snapshot(t, filepath.Dir(paths.Dir))

			_, err := svc.Apply(CodexProvider, ActionInstall)
			r := refusalFrom(t, err)
			if r.Code != RefuseUnreadable && r.Code != RefuseNeedsRepair {
				t.Fatalf("install refused with %q", r.Code)
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(paths.Dir))) {
				t.Fatal("a refused install still changed the tree")
			}
		})
	}
}

func TestCodexSymlinkedFilesAreRefusedUnwritten(t *testing.T) {
	for _, link := range []string{"hooks.json", "config.toml"} {
		t.Run(link, func(t *testing.T) {
			svc, env, paths := codexFixture(t)
			target := filepath.Join(env.Home, "elsewhere")
			writeFileForTest(t, target, "{}")
			if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(paths.Dir, link)); err != nil {
				t.Fatal(err)
			}
			_, err := svc.Apply(CodexProvider, ActionInstall)
			r := refusalFrom(t, err)
			if r.Code != RefuseUnsafePath && r.Code != RefuseNeedsRepair && r.Code != RefuseUnreadable {
				t.Fatalf("refused with %q", r.Code)
			}
			if got := readFileForTest(t, target); got != "{}" {
				t.Fatalf("the symlink's target was written: %q", got)
			}
		})
	}
}

func TestCodexStatusIsDecidedFromTheFilesNotFromAClaim(t *testing.T) {
	t.Run("tampered entry", func(t *testing.T) {
		svc, _, paths := codexFixture(t)
		applyTo(t, svc, CodexProvider, ActionInstall)
		content := strings.Replace(readFileForTest(t, paths.Hooks), `"timeout": 10`, `"timeout": 600`, 1)
		writeFileForTest(t, paths.Hooks, content)

		st := codexStatus(t, svc)
		if st.Status != agentlifecycle.StatusNeedsRepair {
			t.Fatalf("a tampered entry reads as %s, want needs-repair", st.Status)
		}
		if st.EffectiveTier != agentlifecycle.TierScreenFallback {
			t.Fatalf("a tampered install still holds tier %q", st.EffectiveTier)
		}
		applyTo(t, svc, CodexProvider, ActionRepair)
		if got := codexStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
			t.Fatalf("after repair: %s", got)
		}
	})

	t.Run("feature flag turned off behind the hook", func(t *testing.T) {
		svc, _, paths := codexFixture(t)
		applyTo(t, svc, CodexProvider, ActionInstall)
		content := strings.Replace(readFileForTest(t, paths.Config), "hooks = true", "hooks = false", 1)
		writeFileForTest(t, paths.Config, content)

		st := codexStatus(t, svc)
		if st.Status != agentlifecycle.StatusNeedsRepair {
			t.Fatalf("a disabled feature flag reads as %s, want needs-repair", st.Status)
		}
		applyTo(t, svc, CodexProvider, ActionRepair)
		if got := codexStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
			t.Fatalf("after repair: %s", got)
		}
	})

	t.Run("trust record deleted behind the hook", func(t *testing.T) {
		svc, _, paths := codexFixture(t)
		applyTo(t, svc, CodexProvider, ActionInstall)
		content := readFileForTest(t, paths.Config)
		content = content[:strings.Index(content, "[hooks.state.")]
		writeFileForTest(t, paths.Config, content)

		st := codexStatus(t, svc)
		if st.Status != agentlifecycle.StatusNeedsRepair {
			t.Fatalf("a missing trust record reads as %s, want needs-repair", st.Status)
		}
		applyTo(t, svc, CodexProvider, ActionRepair)
		if got := codexStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
			t.Fatalf("after repair: %s", got)
		}
	})

	t.Run("stale trust record with no hook", func(t *testing.T) {
		svc, _, paths := codexFixture(t)
		applyTo(t, svc, CodexProvider, ActionInstall)
		if err := os.Remove(paths.Hooks); err != nil {
			t.Fatal(err)
		}
		st := codexStatus(t, svc)
		if st.Status != agentlifecycle.StatusNeedsRepair {
			t.Fatalf("a stray trust record reads as %s, want needs-repair", st.Status)
		}
		// Uninstall cleans the stray record rather than insisting on repair.
		applyTo(t, svc, CodexProvider, ActionUninstall)
		if strings.Contains(readFileForTest(t, paths.Config), "hooks.state") {
			t.Fatal("uninstall left the stray trust record")
		}
	})
}

func TestCodexRepairRekeysTheTrustRecordWhenTheEntryMoved(t *testing.T) {
	// The trust key is positional. If the user's edits moved Sidecar's group,
	// the recorded key no longer names the entry's position and Codex would
	// re-prompt; status must see that from the files and repair must fix it.
	svc, _, paths := codexFixture(t)
	applyTo(t, svc, CodexProvider, ActionInstall)

	// Insert a foreign group BEFORE Sidecar's, shifting it to index 1.
	hooks := readFileForTest(t, paths.Hooks)
	foreign := `{"hooks": [{"type": "command", "command": "echo hello"}]},`
	hooks = strings.Replace(hooks, "\"SessionStart\": [\n", "\"SessionStart\": [\n"+foreign+"\n", 1)
	writeFileForTest(t, paths.Hooks, hooks)

	st := codexStatus(t, svc)
	if st.Status != agentlifecycle.StatusNeedsRepair {
		t.Fatalf("a mispositioned trust record reads as %s, want needs-repair", st.Status)
	}
	applyTo(t, svc, CodexProvider, ActionRepair)
	config := readFileForTest(t, paths.Config)
	if !strings.Contains(config, paths.Hooks+":session_start:1:0") {
		t.Fatalf("repair did not re-key the trust record:\n%s", config)
	}
	if strings.Contains(config, ":session_start:0:0") {
		t.Fatalf("repair left the stale trust record:\n%s", config)
	}
	if got := codexStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
		t.Fatalf("after repair: %s", got)
	}
}

func TestCodexProviderMissingRefusesInstallButAllowsUninstall(t *testing.T) {
	svc, _, paths := codexFixture(t)
	applyTo(t, svc, CodexProvider, ActionInstall)

	gone := Service{Env: svc.Env, Adapters: svc.Adapters}
	gone.Env.LookPath = func(string) (string, error) { return "", errors.New("not found") }

	st, err := gone.Status(CodexProvider)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != agentlifecycle.StatusProviderMissing {
		t.Fatalf("status %s, want provider-missing", st.Status)
	}
	if st.EffectiveTier != agentlifecycle.TierScreenFallback {
		t.Fatalf("tier %q with no provider", st.EffectiveTier)
	}
	_, err = gone.Apply(CodexProvider, ActionInstall)
	if r := refusalFrom(t, err); r.Code != RefuseProviderMissing {
		t.Fatalf("install refused with %q", r.Code)
	}
	if p := applyTo(t, gone, CodexProvider, ActionUninstall); p.Unchanged {
		t.Fatal("uninstall had nothing to remove")
	}
	if _, err := os.Lstat(paths.Hooks); !os.IsNotExist(err) {
		t.Fatal("the hook entry was not removed")
	}
	if strings.Contains(readFileForTest(t, paths.Config), "trusted_hash") {
		t.Fatal("the trust record was not removed")
	}
}

func TestCodexOfferedActionsAreExactlyTheOnesThatWouldNotRefuse(t *testing.T) {
	svc, _, _ := codexFixture(t)
	for _, step := range []struct {
		name string
		act  Action
	}{
		{"not installed", ""},
		{"installed", ActionInstall},
	} {
		if step.act != "" {
			applyTo(t, svc, CodexProvider, step.act)
		}
		st := codexStatus(t, svc)
		offered := map[Action]bool{}
		for _, a := range st.Offered {
			offered[a] = true
		}
		for _, act := range Actions() {
			_, err := svc.Plan(CodexProvider, act)
			if wouldRun := err == nil; wouldRun != offered[act] {
				t.Fatalf("%s: %s offered=%v but planning err=%v", step.name, act, offered[act], err)
			}
		}
	}
}

// --- the config.toml editor's honesty ---
//
// The line scanner and the TOML oracle are two independent readings of the same
// file, and the tests below hold each of them to its own promise. The scanner
// promises never to *silently* miss a legal spelling of the two regions Sidecar
// edits: it either reads it correctly or it refuses. The oracle promises that
// nothing the user owns can leave the file unnoticed. A silent miss in the
// scanner is not a cosmetic bug — the composer would go on to append a second
// [features] table or a duplicate key, and the result is a config.toml Codex
// cannot parse, which is every Codex setting the user had, gone.

func TestCodexScannerNeverSilentlyMissesALegalSpelling(t *testing.T) {
	const key = "/home/u/.codex/hooks.json:session_start:0:0"
	for _, tc := range []struct {
		name   string
		config string
		// seen reports that the scanner read the region correctly. A case that
		// is neither seen nor refused is the silent miss this test forbids.
		seen func(codexConfigScan) bool
		what string
	}{
		{
			name:   "quoted table name",
			config: "[\"features\"]\nhooks = true\n",
			seen:   func(s codexConfigScan) bool { return s.features == 0 && s.hooksEnabled() },
			what:   "the features table",
		},
		{
			name:   "literal-quoted table name",
			config: "['features']\nhooks = true\n",
			seen:   func(s codexConfigScan) bool { return s.features == 0 && s.hooksEnabled() },
			what:   "the features table",
		},
		{
			name:   "quoted key",
			config: "[features]\n\"hooks\" = true\n",
			seen:   func(s codexConfigScan) bool { return s.hooksFlagLine == 1 && s.hooksEnabled() },
			what:   "the features.hooks key",
		},
		{
			name:   "literal-quoted key",
			config: "[features]\n'hooks' = true\n",
			seen:   func(s codexConfigScan) bool { return s.hooksFlagLine == 1 && s.hooksEnabled() },
			what:   "the features.hooks key",
		},
		{
			name:   "inline array element that looks like a table header",
			config: "[features]\nhooks = true\n\n[profiles]\ngrid = [\n  [40, 50]\n]\n",
			seen: func(s codexConfigScan) bool {
				return s.features == 0 && s.hooksEnabled() && len(s.state) == 0
			},
			what: "the features table with no phantom table from the array",
		},
		{
			// The dangerous one: an array element spelled exactly like the
			// header Sidecar looks for. A scanner without bracket-depth
			// tracking records the *array element's* line as the [features]
			// header, and the composer then writes `hooks = true` into the
			// middle of an array.
			name:   "array element spelled like the features header",
			config: "tables = [\n  [\"features\"]\n]\n\n[features]\nhooks = true\n",
			seen: func(s codexConfigScan) bool {
				return s.features == 4 && s.hooksFlagLine == 5 && s.hooksEnabled()
			},
			what: "the real features header at line 5, not the array element at line 2",
		},
		{
			name:   "spaced dotted header",
			config: "[features]\nhooks = true\n\n[ hooks . state . \"" + key + "\" ]\ntrusted_hash = \"sha256:aa\"\n",
			seen: func(s codexConfigScan) bool {
				return len(s.state) == 1 && s.state[0].key == key && s.state[0].hash == "sha256:aa"
			},
			what: "the hooks.state trust table",
		},
		{
			name:   "spaced dotted header with quoted segments",
			config: "[features]\nhooks = true\n\n[ \"hooks\" . state . '" + key + "' ]\ntrusted_hash = \"sha256:aa\"\n",
			seen: func(s codexConfigScan) bool {
				return len(s.state) == 1 && s.state[0].key == key && s.state[0].hash == "sha256:aa"
			},
			what: "the hooks.state trust table",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every fixture must be legal TOML, or the case proves nothing
			// about a spelling a user could actually have written.
			if _, err := codexTOMLDoc([]byte(tc.config)); err != nil {
				t.Fatalf("the fixture is not valid TOML, so it tests nothing: %v", err)
			}
			scan := scanCodexConfig(true, []byte(tc.config))
			if scan.parseErr != "" {
				// A refusal is an acceptable answer; a wrong one is not.
				return
			}
			if !tc.seen(scan) {
				t.Fatalf("the scanner neither refused nor found %s:\nconfig:\n%s\nfeatures=%d hooksFlagLine=%d hooksEnabled=%v state=%+v",
					tc.what, tc.config, scan.features, scan.hooksFlagLine, scan.hooksEnabled(), scan.state)
			}
		})
	}
}

// TestCodexInstallOverALegalSpellingNeverDuplicatesTheFeaturesTable is the
// consequence the scanner test exists to prevent, checked end to end: whatever
// spelling the file uses, an install either refuses or leaves a file that is
// still valid TOML with exactly one features.hooks.
func TestCodexInstallOverALegalSpellingNeverDuplicatesTheFeaturesTable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
	}{
		{"quoted table name", "[\"features\"]\nhooks = true\n"},
		{"literal-quoted table name", "['features']\nhooks = true\n"},
		{"quoted key", "[features]\n\"hooks\" = true\n"},
		{"array element spelled like the features header", "tables = [\n  [\"features\"]\n]\n\n[features]\nhooks = true\n"},
		{"spaced dotted header", "[ features ]\nhooks = true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, paths := codexFixture(t)
			writeFileForTest(t, paths.Config, tc.config)
			before := snapshot(t, filepath.Dir(paths.Dir))

			if _, err := svc.Apply(CodexProvider, ActionInstall); err != nil {
				// Refusing is allowed; changing anything while refusing is not.
				if r := refusalFrom(t, err); r.Code == "" {
					t.Fatalf("refused without a code: %v", err)
				}
				if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(paths.Dir))) {
					t.Fatal("a refused install still changed the tree")
				}
				return
			}
			got := readFileForTest(t, paths.Config)
			doc, err := codexTOMLDoc([]byte(got))
			if err != nil {
				t.Fatalf("install produced a config.toml Codex cannot parse (%v):\n%s", err, got)
			}
			features, _ := doc["features"].(map[string]any)
			if features["hooks"] != true {
				t.Fatalf("features.hooks is not true after install:\n%s", got)
			}
			if n := strings.Count(got, "[features]"); n > 1 {
				t.Fatalf("install appended a duplicate [features] table:\n%s", got)
			}
		})
	}
}

// TestCodexRefusesAFeaturesTableItCannotRead pins the refusal side of the same
// contract: a legal spelling the scanner cannot normalise must stop the install
// dead rather than let the composer append a second [features] table beside the
// one it failed to see.
func TestCodexRefusesAFeaturesTableItCannotRead(t *testing.T) {
	svc, _, paths := codexFixture(t)
	// A quoted key carrying an escape: legal TOML, spelling "features", and a
	// form this editor deliberately does not try to interpret.
	config := "[\"fea\\u0074ures\"]\nhooks = true\n"
	if _, err := codexTOMLDoc([]byte(config)); err != nil {
		t.Fatalf("the fixture is not valid TOML, so it tests nothing: %v", err)
	}
	writeFileForTest(t, paths.Config, config)
	before := snapshot(t, filepath.Dir(paths.Dir))

	_, err := svc.Apply(CodexProvider, ActionInstall)
	r := refusalFrom(t, err)
	if r.Code != RefuseUnreadable && r.Code != RefuseNeedsRepair {
		t.Fatalf("install refused with %q, want an unreadable/needs-repair refusal", r.Code)
	}
	if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(paths.Dir))) {
		t.Fatal("a refused install still changed the tree")
	}
	if got := readFileForTest(t, paths.Config); got != config {
		t.Fatalf("the config Sidecar could not read was rewritten:\n%s", got)
	}
}

// TestCodexUninstallKeepsWhatTheUserParkedAtTheEnd is blocker 2. Sidecar always
// appends its trust table last, so a span that ran to the next header or to EOF
// swallowed everything the user added after installing. A table's span now ends
// at its last own line; trailing blank lines and comments belong to the file.
func TestCodexUninstallKeepsWhatTheUserParkedAtTheEnd(t *testing.T) {
	svc, _, paths := codexFixture(t)
	writeFileForTest(t, paths.Config, "# my codex config\nmodel = \"gpt-5.6\"\n")
	applyTo(t, svc, CodexProvider, ActionInstall)

	installed := readFileForTest(t, paths.Config)
	const parked = "# a note the user parked at the end\n\n[my.notes]\nscratch = \"keep me\"\n"
	writeFileForTest(t, paths.Config, installed+parked)

	applyTo(t, svc, CodexProvider, ActionUninstall)
	got := readFileForTest(t, paths.Config)

	// The parked block survives byte for byte, not merely in spirit.
	if !strings.Contains(got, parked) {
		t.Fatalf("uninstall did not keep the parked block byte for byte:\n%q", got)
	}
	for _, want := range []string{"# my codex config", `model = "gpt-5.6"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("uninstall destroyed %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hooks.state") || strings.Contains(got, "trusted_hash") {
		t.Fatalf("uninstall left its own trust record behind:\n%s", got)
	}
	doc, err := codexTOMLDoc([]byte(got))
	if err != nil {
		t.Fatalf("uninstall produced a config.toml Codex cannot parse (%v):\n%s", err, got)
	}
	notes, _ := doc["my"].(map[string]any)["notes"].(map[string]any)
	if notes["scratch"] != "keep me" {
		t.Fatalf("the user's table did not survive uninstall:\n%s", got)
	}
}

// TestCodexOracleRejectsARewriteThatLosesAUserKey is the evidence that the
// oracle is load-bearing rather than decorative. The image below is exactly
// what a composer bug that dropped a line would produce: it passes every check
// the line scanner knows how to make — it scans clean, features.hooks is true,
// and it records exactly one trust table with the right key and hash — while
// having silently eaten the user's `model` setting. Only the semantic diff
// against the pre-image can tell.
func TestCodexOracleRejectsARewriteThatLosesAUserKey(t *testing.T) {
	const wantKey = "/home/u/.codex/hooks.json:session_start:0:0"
	wantHash := codexTrustHashes()[0]
	pre := []byte("# hand written\nmodel = \"gpt-5.6\"\n\n[features]\nhooks = false\nmemories = true\n")
	broken := []byte("[features]\nhooks = true\nmemories = true\n\n" + codexStateBlock(wantKey, wantHash))

	// Everything the scanner-only verifier used to check passes on this image.
	scan := scanCodexConfig(true, broken)
	if scan.parseErr != "" {
		t.Fatalf("the doctored image does not even scan (%s); it would prove nothing", scan.parseErr)
	}
	if !scan.hooksEnabled() {
		t.Fatal("the doctored image does not enable features.hooks; it would prove nothing")
	}
	owned := codexOwnedTables(scan, wantKey)
	if len(owned) != 1 || owned[0].key != wantKey || owned[0].hash != wantHash {
		t.Fatalf("the doctored image does not record exactly one trust table; it would prove nothing: %+v", owned)
	}

	// The oracle catches it anyway, and names what would have been lost.
	_, err := codexVerifyConverged(pre, broken, []string{wantKey}, wantKey, wantHash)
	if err == nil {
		t.Fatal("the oracle accepted a rewrite that dropped the user's model setting")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Fatalf("the refusal does not name what would be lost: %v", err)
	}
}

func TestCodexOracleDiffNamesEveryKindOfDamage(t *testing.T) {
	const base = "model = \"gpt-5.6\"\n\n[ui]\ntheme = \"dark\"\nwidths = [10, 20]\n"
	for _, tc := range []struct {
		name string
		post string
		want string
	}{
		{"a dropped key", "model = \"gpt-5.6\"\n\n[ui]\nwidths = [10, 20]\n", "drop"},
		{"a changed value", "model = \"gpt-5.7\"\n\n[ui]\ntheme = \"dark\"\nwidths = [10, 20]\n", "change"},
		{"a changed array", "model = \"gpt-5.6\"\n\n[ui]\ntheme = \"dark\"\nwidths = [10, 30]\n", "change"},
		{"an invented key", base + "\n[extra]\nsurprise = true\n", "add"},
		{"a dropped table", "model = \"gpt-5.6\"\n", "drop"},
		{"an emptied table", "model = \"gpt-5.6\"\n\n[ui]\n", "drop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := codexOracleDiff([]byte(base), []byte(tc.post), nil)
			if err == nil {
				t.Fatalf("the oracle accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal for %s reads %q, want it to say %q", tc.name, err, tc.want)
			}
		})
	}
	// And it accepts a rewrite confined to the region Sidecar owns.
	const owned = "/x:session_start:0:0"
	pre := base + "\n[hooks.state.\"" + owned + "\"]\ntrusted_hash = \"sha256:aa\"\n"
	post := base + "\n[features]\nhooks = true\n\n[hooks.state.\"" + owned + "\"]\ntrusted_hash = \"sha256:bb\"\n"
	if err := codexOracleDiff([]byte(pre), []byte(post), []string{owned}); err != nil {
		t.Fatalf("the oracle refused a rewrite confined to Sidecar's own region: %v", err)
	}
}

// TestCodexOracleRejectsAnUninstallThatEatsAForeignTrustRecord is the same
// evidence on the removal path: the image below satisfies every check the line
// scanner makes for an uninstall — it scans clean and holds none of Sidecar's
// tables — and has taken another tool's trust record with it.
func TestCodexOracleRejectsAnUninstallThatEatsAForeignTrustRecord(t *testing.T) {
	const ours = "/home/u/.codex/hooks.json:session_start:1:0"
	const theirs = "/home/u/.codex/hooks.json:session_start:0:0"
	pre := "[features]\nhooks = true\n\n[hooks.state.\"" + theirs + "\"]\ntrusted_hash = \"sha256:aa\"\n\n" +
		"[hooks.state.\"" + ours + "\"]\ntrusted_hash = \"" + codexTrustHashes()[0] + "\"\n"
	broken := "[features]\nhooks = true\n"

	scan := scanCodexConfig(true, []byte(broken))
	if scan.parseErr != "" || len(codexOwnedTables(scan, ours)) != 0 {
		t.Fatal("the doctored image would have failed the scanner's own check; it proves nothing")
	}
	if _, err := codexVerifyRemoved([]byte(pre), []byte(broken), []string{ours}, ours); err == nil {
		t.Fatal("the oracle accepted an uninstall that removed a foreign trust record")
	}
	// The honest removal passes the same gate.
	good := "[features]\nhooks = true\n\n[hooks.state.\"" + theirs + "\"]\ntrusted_hash = \"sha256:aa\"\n"
	if _, err := codexVerifyRemoved([]byte(pre), []byte(good), []string{ours}, ours); err != nil {
		t.Fatalf("the oracle refused an honest uninstall: %v", err)
	}
	// So does one that leaves features.hooks alone but would flip it.
	flipped := "[features]\nhooks = false\n\n[hooks.state.\"" + theirs + "\"]\ntrusted_hash = \"sha256:aa\"\n"
	if _, err := codexVerifyRemoved([]byte(pre), []byte(flipped), []string{ours}, ours); err == nil {
		t.Fatal("the oracle accepted an uninstall that turned features.hooks off")
	}
}

// TestCodexRefusesAConfigThatIsNotValidTOML is the pre-parse gate: Sidecar does
// not edit a file it cannot fully understand, and a line scanner's reading of
// an invalid file is a guess.
func TestCodexRefusesAConfigThatIsNotValidTOML(t *testing.T) {
	for _, tc := range []struct{ name, config string }{
		{"unterminated header", "[features\nhooks = true\n"},
		{"duplicate key", "[features]\nhooks = true\nhooks = false\n"},
		{"duplicate table", "[features]\nhooks = true\n\n[features]\nmemories = true\n"},
		{"garbage", "this is not toml at all\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, paths := codexFixture(t)
			writeFileForTest(t, paths.Config, tc.config)
			before := snapshot(t, filepath.Dir(paths.Dir))

			scan := scanCodexConfig(true, []byte(tc.config))
			if scan.parseErr == "" {
				t.Fatalf("invalid TOML scanned clean:\n%s", tc.config)
			}
			_, err := svc.Apply(CodexProvider, ActionInstall)
			r := refusalFrom(t, err)
			if r.Code != RefuseUnreadable && r.Code != RefuseNeedsRepair {
				t.Fatalf("install refused with %q", r.Code)
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(paths.Dir))) {
				t.Fatal("a refused install still changed the tree")
			}
		})
	}
}
