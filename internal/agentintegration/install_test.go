package agentintegration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// The installer suite runs entirely inside t.TempDir with an injected Env, so
// no test reads or writes a real ~/.config/opencode and every one of them can
// be run twice in a row. That is not a nicety: the adversarial fixtures below
// deliberately create world-writable directories, symlinks pointing at nothing,
// and files owned by nobody in particular, and none of that belongs anywhere
// near a developer's actual configuration.

func fixture(t *testing.T, opts ...func(*Env)) (Service, Env, openCodePaths) {
	t.Helper()
	home := t.TempDir()
	env := Env{
		Home:       home,
		ConfigHome: filepath.Join(home, ".config"),
		LookPath: func(file string) (string, error) {
			if file == OpenCodeProvider {
				return filepath.Join(home, "bin", "opencode"), nil
			}
			return "", errors.New("not found")
		},
		ProviderVersion: func(string) string { return "1.18.25" },
		UID:             os.Getuid(),
	}
	for _, o := range opts {
		o(&env)
	}
	if err := os.MkdirAll(filepath.Join(env.ConfigHome, OpenCodeProvider), 0o755); err != nil {
		t.Fatal(err)
	}
	return Service{Env: env, Adapters: DefaultAdapters()}, env, openCodePathsFor(env)
}

func withoutProvider(e *Env) {
	e.LookPath = func(string) (string, error) { return "", errors.New("not found") }
}

func mustStatus(t *testing.T, s Service) Status {
	t.Helper()
	st, err := s.Status(OpenCodeProvider)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return st
}

func mustApply(t *testing.T, s Service, act Action) Plan {
	t.Helper()
	p, err := s.Apply(OpenCodeProvider, act)
	if err != nil {
		t.Fatalf("%s: %v", act, err)
	}
	return p
}

func refusalFrom(t *testing.T, err error) *Refusal {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal, got none")
	}
	r, ok := AsRefusal(err)
	if !ok {
		t.Fatalf("expected a *Refusal, got %T: %v", err, err)
	}
	return r
}

// snapshot records every file under root with its contents, so "the tree is
// exactly as we found it" can be asserted rather than eyeballed.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTheBundledAssetCarriesTheMarkerTheInstallerLooksFor(t *testing.T) {
	asset := (OpenCodeAdapter{}).asset()
	if !strings.Contains(asset.Content, Marker(asset)) {
		t.Fatalf("the bundled asset does not contain %q", Marker(asset))
	}
	id, schema, version, ok := parseMarker(asset.Content)
	if !ok {
		t.Fatal("the bundled asset's marker does not parse")
	}
	if id != asset.Source || schema != asset.SchemaVersion || version != asset.Version {
		t.Fatalf("marker %s/%d/%s does not match the asset %s/%d/%s", id, schema, version, asset.Source, asset.SchemaVersion, asset.Version)
	}
	// The registry decides what a report at this source is trusted to do, so an
	// asset shipping a version the registry has never heard of would grant
	// authority to a version nobody qualified.
	capability, known := agentlifecycle.CapabilityForSource(asset.Source)
	if !known {
		t.Fatalf("no capability entry for %s", asset.Source)
	}
	if capability.AssetVersion != asset.Version {
		t.Fatalf("asset version %q but registry records %q", asset.Version, capability.AssetVersion)
	}
}

func TestInstallIntoACleanTreeIsExplicitAndIdempotent(t *testing.T) {
	svc, _, paths := fixture(t)

	if got := mustStatus(t, svc).Status; got != agentlifecycle.StatusNotInstalled {
		t.Fatalf("before install: %s", got)
	}

	p := mustApply(t, svc, ActionInstall)
	if p.Unchanged {
		t.Fatal("the first install reported unchanged")
	}
	if p.StatusAfter != agentlifecycle.StatusCurrent {
		t.Fatalf("after install: %s", p.StatusAfter)
	}

	b, err := os.ReadFile(paths.Owned)
	if err != nil {
		t.Fatalf("the asset was not installed: %v", err)
	}
	if string(b) != (OpenCodeAdapter{}).asset().Content {
		t.Fatal("the installed bytes are not the bundled asset")
	}

	st := mustStatus(t, svc)
	if st.Status != agentlifecycle.StatusCurrent {
		t.Fatalf("status after install: %s (%s)", st.Status, st.Message)
	}
	if st.InstalledVersion != st.BundledVersion {
		t.Fatalf("installed %q, bundled %q", st.InstalledVersion, st.BundledVersion)
	}
	if st.EffectiveTier != agentlifecycle.TierFull {
		t.Fatalf("a current install of a full-tier source resolved to %s (%s)", st.EffectiveTier, st.TierReason)
	}

	// Idempotence is a property of the second run, and it has to be visible:
	// "unchanged" is what tells a caller nothing happened, as distinct from a
	// second write that happened to produce the same bytes.
	again := mustApply(t, svc, ActionInstall)
	if !again.Unchanged || len(again.Ops) != 0 {
		t.Fatalf("the second install was not a no-op: unchanged=%v ops=%d", again.Unchanged, len(again.Ops))
	}
}

func TestADryRunAndTheRealRunDescribeTheSameOperations(t *testing.T) {
	// The whole value of --dry-run is that it is not a separate code path. This
	// compares the serialized plan of a preview with the serialized plan of the
	// mutation that follows it, field for field.
	for _, act := range []Action{ActionInstall, ActionUpdate, ActionRepair, ActionUninstall} {
		t.Run(string(act), func(t *testing.T) {
			svc, _, paths := fixture(t)
			// Put the tree in a state where this verb has work to do. install
			// needs a clean tree — pre-installing made its preview and its
			// mutation two empty op lists, which compared equal for the one
			// reason that proves nothing. uninstall needs something installed,
			// and update and repair need it to be out of date as well.
			if act != ActionInstall {
				mustApply(t, svc, ActionInstall)
				if act != ActionUninstall {
					writeOldAsset(t, paths.Owned)
				}
			}

			preview, err := svc.Plan(OpenCodeProvider, act)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			// The comparison below is only worth making if there is something to
			// compare. Without this, any future change that turned a subtest
			// into a no-op would leave it silently passing.
			if len(preview.Ops) == 0 {
				t.Fatalf("%s previewed no operations, so this subtest compares two empty plans", act)
			}
			applied, err := svc.Apply(OpenCodeProvider, act)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if !preview.DryRun || applied.DryRun {
				t.Fatalf("dryRun flags are wrong: preview=%v applied=%v", preview.DryRun, applied.DryRun)
			}
			assertSamePlanBody(t, preview, applied)
		})
	}
}

// assertSamePlanBody compares everything about two plans except the fields that
// describe the invocation rather than the change.
func assertSamePlanBody(t *testing.T, a, b Plan) {
	t.Helper()
	a.DryRun, b.DryRun = false, false
	a.Applied, b.Applied = false, false
	// StatusAfter is predicted by a dry run and re-inspected by a real one.
	// They must agree, which is the point, so it stays in the comparison.
	x, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(x) != string(y) {
		t.Fatalf("the preview and the mutation differ:\n--- preview ---\n%s\n--- applied ---\n%s", x, y)
	}
}

// writeOldAsset installs a marked asset at an older version, which is what
// "outdated" means on disk.
func writeOldAsset(t *testing.T, path string) {
	t.Helper()
	old := "// sidecar-integration: id=" + OpenCodeSource + " schema=1 version=0\n// an earlier Sidecar asset\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallLeavesUnrelatedContentExactlyAsItFoundIt(t *testing.T) {
	svc, _, paths := fixture(t)

	// Unrelated content a applied user would have: another plugin, and OpenCode's
	// own configuration file.
	if err := os.MkdirAll(paths.OwnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(paths.OwnedDir, "someone-elses-plugin.js")
	if err := os.WriteFile(other, []byte("export const Other = async () => ({})\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(paths.ConfigDir, "opencode.json")
	if err := os.WriteFile(cfg, []byte(`{"theme":"opencode","model":"anthropic/claude"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	before := snapshot(t, paths.ConfigDir)

	mustApply(t, svc, ActionInstall)
	if _, err := os.Stat(paths.Owned); err != nil {
		t.Fatalf("install did not land: %v", err)
	}

	mustApply(t, svc, ActionUninstall)

	after := snapshot(t, paths.ConfigDir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the tree changed across install+uninstall\nbefore: %v\nafter:  %v", keysOf(before), keysOf(after))
	}
	if got := mustStatus(t, svc).Status; got != agentlifecycle.StatusNotInstalled {
		t.Fatalf("after uninstall: %s", got)
	}

	// Uninstalling nothing is a no-op rather than an error, so a cleanup script
	// can run unconditionally.
	p := mustApply(t, svc, ActionUninstall)
	if !p.Unchanged {
		t.Fatal("uninstalling nothing was not reported as unchanged")
	}
}

func TestUninstallRemovesThePluginDirectoryOnlyWhenSidecarEmptiedIt(t *testing.T) {
	svc, _, paths := fixture(t)
	mustApply(t, svc, ActionInstall)
	mustApply(t, svc, ActionUninstall)
	if _, err := os.Stat(paths.OwnedDir); !os.IsNotExist(err) {
		t.Fatalf("the plugin directory Sidecar created was left behind: %v", err)
	}

	// With something else in it, the directory is not Sidecar's to delete.
	mustApply(t, svc, ActionInstall)
	other := filepath.Join(paths.OwnedDir, "keep-me.js")
	if err := os.WriteFile(other, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustApply(t, svc, ActionUninstall)
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("an unrelated plugin was removed with the directory: %v", err)
	}
	if _, err := os.Stat(paths.OwnedDir); err != nil {
		t.Fatalf("a directory holding another plugin was removed: %v", err)
	}
}

func TestUninstallSucceedsWhenSomethingLandsInTheDirectoryBetweenPlanAndApply(t *testing.T) {
	// OpRmdir is planned from a plan-time reading of the directory's contents.
	// If anything appears in it before the plan runs, os.Remove returns
	// ENOTEMPTY — and failing there would report exit 1, "the change was
	// attempted and failed part-way", for an uninstall that had already
	// correctly removed the asset, the duplicate, and the backup. The tidy-up is
	// conditional by construction, so losing that race is a no-op.
	//
	// Applying a plan built before the intruder arrived is the exact shape of
	// the window, rather than an approximation of it.
	svc, _, paths := fixture(t)
	mustApply(t, svc, ActionInstall)

	plan, err := svc.Plan(OpenCodeProvider, ActionUninstall)
	if err != nil {
		t.Fatal(err)
	}
	var sawRmdir bool
	for _, op := range plan.Ops {
		if op.Kind == OpRmdir && op.Path == paths.OwnedDir {
			sawRmdir = true
		}
	}
	if !sawRmdir {
		t.Fatal("the uninstall plan does not remove the directory it emptied, so this test proves nothing")
	}

	intruder := filepath.Join(paths.OwnedDir, "arrived-late.js")
	if err := os.WriteFile(intruder, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Apply(plan); err != nil {
		t.Fatalf("uninstall reported failure after removing everything it owned: %v", err)
	}
	if _, err := os.Stat(paths.Owned); !os.IsNotExist(err) {
		t.Fatalf("the asset survived the uninstall: %v", err)
	}
	if b, err := os.ReadFile(intruder); err != nil || string(b) != "mine\n" {
		t.Fatalf("the file that arrived late was disturbed: %v %q", err, string(b))
	}
	if _, err := os.Stat(paths.OwnedDir); err != nil {
		t.Fatalf("a directory that was no longer empty was removed anyway: %v", err)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestACopyInBothPluginDirectoriesIsNeedsRepairAndRepairRemovesTheDuplicate(t *testing.T) {
	// This is the Phase A finding that no vendor documentation mentions: both
	// plugin/ and plugins/ are loaded, so a copy in each reports every event
	// twice, doubling sequence numbers and breaking ordering outright.
	svc, _, paths := fixture(t)
	mustApply(t, svc, ActionInstall)
	if err := os.MkdirAll(paths.ConflictDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Conflict, []byte((OpenCodeAdapter{}).asset().Content), 0o644); err != nil {
		t.Fatal(err)
	}

	st := mustStatus(t, svc)
	if st.Status != agentlifecycle.StatusNeedsRepair {
		t.Fatalf("a double install reported %s, not needs-repair", st.Status)
	}
	if !strings.Contains(st.Message, "twice") {
		t.Fatalf("the message does not say what is wrong: %q", st.Message)
	}
	if st.EffectiveTier != agentlifecycle.TierScreenFallback {
		t.Fatalf("a damaged install kept tier %s", st.EffectiveTier)
	}

	// install and update refuse and name the verb that does help.
	for _, act := range []Action{ActionInstall, ActionUpdate} {
		_, err := svc.Plan(OpenCodeProvider, act)
		r := refusalFrom(t, err)
		if r.Code != RefuseNeedsRepair {
			t.Fatalf("%s refused with %s, want needs_repair", act, r.Code)
		}
		if !strings.Contains(r.Message, "repair") {
			t.Fatalf("%s refusal does not name repair: %q", act, r.Message)
		}
	}

	mustApply(t, svc, ActionRepair)
	if _, err := os.Stat(paths.Conflict); !os.IsNotExist(err) {
		t.Fatalf("repair left the duplicate in place: %v", err)
	}
	if _, err := os.Stat(paths.Owned); err != nil {
		t.Fatalf("repair removed the copy Sidecar owns: %v", err)
	}
	if got := mustStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
		t.Fatalf("after repair: %s", got)
	}
}

func TestAnAssetInstalledOnlyInTheDirectorySidecarDoesNotOwnIsRepaired(t *testing.T) {
	svc, _, paths := fixture(t)
	if err := os.MkdirAll(paths.ConflictDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Conflict, []byte((OpenCodeAdapter{}).asset().Content), 0o644); err != nil {
		t.Fatal(err)
	}
	st := mustStatus(t, svc)
	if st.Status != agentlifecycle.StatusNeedsRepair {
		t.Fatalf("got %s: %s", st.Status, st.Message)
	}
	mustApply(t, svc, ActionRepair)
	if _, err := os.Stat(paths.Conflict); !os.IsNotExist(err) {
		t.Fatal("repair left the copy in the unowned directory")
	}
	if got := mustStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
		t.Fatalf("after repair: %s", got)
	}
}

func TestAFileSidecarDoesNotOwnIsNeverAdoptedOverwrittenOrDeleted(t *testing.T) {
	// The rule the plan states outright: never auto-adopt a similarly named
	// existing script. A filename is not ownership, so all three mutating verbs
	// have to refuse and leave the bytes alone.
	const theirs = "// someone else's plugin that happens to share a name\nexport const X = async () => ({})\n"

	// Each case names the exact refusal, because the two situations are
	// genuinely different and collapsing them would hide that.
	//
	// A stranger's file at Sidecar's own path makes the installation damaged:
	// there is something where Sidecar's asset belongs, so the status is
	// needs-repair and only repair gets as far as discovering it cannot help.
	//
	// A stranger's file in the directory Sidecar does not own leaves Sidecar
	// simply not installed. Installing is what would create the double-load, so
	// install is the verb that refuses; update and repair refuse earlier for
	// the ordinary reason that there is nothing installed to act on.
	//
	// The third case is the combination that let a real defect through: a
	// stranger's file in the unowned directory *while* Sidecar's own asset is
	// installed. The status used to call that "installed in both", which claimed
	// ownership of a file it will not touch and then offered a Repair the
	// planner refuses.
	cases := []struct {
		name      string
		where     string
		installed bool
		status    agentlifecycle.IntegrationStatus
		message   string
		want      map[Action]RefusalCode
	}{
		{
			name:   "owned",
			where:  "owned",
			status: agentlifecycle.StatusNeedsRepair,
			want: map[Action]RefusalCode{
				ActionInstall:   RefuseNeedsRepair,
				ActionUpdate:    RefuseNeedsRepair,
				ActionRepair:    RefuseForeignFile,
				ActionUninstall: RefuseForeignFile,
			},
		},
		{
			name:   "conflict",
			where:  "conflict",
			status: agentlifecycle.StatusNotInstalled,
			want: map[Action]RefusalCode{
				ActionInstall: RefuseForeignFile,
				ActionUpdate:  RefuseNotInstalled,
				ActionRepair:  RefuseNotInstalled,
				// Nothing of Sidecar's is installed, so uninstall is a no-op
				// that leaves the stranger's file exactly where it is.
				ActionUninstall: "",
			},
		},
		{
			name:      "conflict-alongside-a-real-install",
			where:     "conflict",
			installed: true,
			status:    agentlifecycle.StatusNeedsRepair,
			// The status must say the file is not Sidecar's rather than claim
			// Sidecar installed it in both places.
			message: "not Sidecar's",
			want: map[Action]RefusalCode{
				ActionInstall: RefuseNeedsRepair,
				ActionUpdate:  RefuseNeedsRepair,
				// repair cannot help: the duplicate is not Sidecar's to remove.
				ActionRepair: RefuseForeignFile,
				// uninstall removes Sidecar's own asset and leaves the
				// stranger's file exactly where it is.
				ActionUninstall: "",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, paths := fixture(t)
			if tc.installed {
				mustApply(t, svc, ActionInstall)
			}
			target, dir := paths.Owned, paths.OwnedDir
			if tc.where == "conflict" {
				target, dir = paths.Conflict, paths.ConflictDir
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(theirs), 0o644); err != nil {
				t.Fatal(err)
			}

			st := mustStatus(t, svc)
			if st.Status != tc.status {
				t.Fatalf("status %s, want %s (%s)", st.Status, tc.status, st.Message)
			}
			if tc.message != "" && !strings.Contains(st.Message, tc.message) {
				t.Fatalf("the status does not say %q: %q", tc.message, st.Message)
			}
			if strings.Contains(st.Message, "installed in both") {
				t.Fatalf("the status claims Sidecar installed a file it does not own: %q", st.Message)
			}
			if !strings.Contains(st.Message, target) {
				t.Fatalf("the status does not name the file it refuses to touch: %q", st.Message)
			}

			for _, act := range Actions() {
				_, err := svc.Plan(OpenCodeProvider, act)
				want := tc.want[act]
				if want == "" {
					if err != nil {
						t.Fatalf("%s refused unexpectedly: %v", act, err)
					}
					continue
				}
				if r := refusalFrom(t, err); r.Code != want {
					t.Fatalf("%s refused with %s, want %s: %q", act, r.Code, want, r.Message)
				} else if want == RefuseForeignFile && r.Path != target {
					t.Fatalf("%s refusal names %q, want %q", act, r.Path, target)
				}
			}

			b, err := os.ReadFile(target)
			if err != nil || string(b) != theirs {
				t.Fatalf("the file was modified: %v %q", err, string(b))
			}
		})
	}
}

func TestASymlinkAtTheAssetPathIsRefusedRatherThanFollowed(t *testing.T) {
	// Following it would write outside the directory Sidecar owns, which an
	// ordinary Stat would never reveal: the link resolves to a perfectly
	// healthy regular file.
	svc, _, paths := fixture(t)
	if err := os.MkdirAll(paths.OwnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "important.js")
	if err := os.WriteFile(elsewhere, []byte("do not clobber me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, paths.Owned); err != nil {
		t.Fatal(err)
	}

	st := mustStatus(t, svc)
	if st.Status != agentlifecycle.StatusNeedsRepair {
		t.Fatalf("a symlinked asset path reported %s", st.Status)
	}
	for _, act := range Actions() {
		_, err := svc.Plan(OpenCodeProvider, act)
		r := refusalFrom(t, err)
		if r.Code != RefuseUnsafePath && r.Code != RefuseNeedsRepair {
			t.Fatalf("%s refused with %s, want unsafe_path", act, r.Code)
		}
	}
	b, err := os.ReadFile(elsewhere)
	if err != nil || string(b) != "do not clobber me\n" {
		t.Fatalf("the symlink target was written through: %v %q", err, string(b))
	}
}

func TestAWorldWritablePluginDirectoryIsRefused(t *testing.T) {
	// Anyone in that group could replace the file OpenCode loads and executes.
	// Installing into it would be handing that away.
	svc, _, paths := fixture(t)
	if err := os.MkdirAll(paths.OwnedDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.OwnedDir, 0o777); err != nil {
		t.Fatal(err)
	}
	st := mustStatus(t, svc)
	if st.Status != agentlifecycle.StatusNeedsRepair {
		t.Fatalf("got %s: %s", st.Status, st.Message)
	}
	_, err := svc.Plan(OpenCodeProvider, ActionInstall)
	r := refusalFrom(t, err)
	if r.Code != RefuseNeedsRepair && r.Code != RefuseUnsafeMode {
		t.Fatalf("refused with %s", r.Code)
	}
	_, err = svc.Plan(OpenCodeProvider, ActionRepair)
	r = refusalFrom(t, err)
	if r.Code != RefuseUnsafeMode {
		t.Fatalf("repair refused with %s, want unsafe_mode", r.Code)
	}
}

func TestStatusComesFromTheInstalledBytesNotFromAClaimedVersion(t *testing.T) {
	// This is the Phase B gap Phase C closes. An asset that says it is version
	// 1 but is not the bundled version 1 must not read as current, because a
	// current full-tier source authors the pane's lane against the screen.
	svc, _, paths := fixture(t)
	mustApply(t, svc, ActionInstall)

	tampered := (OpenCodeAdapter{}).asset().Content + "\n// a line someone added\n"
	if err := os.WriteFile(paths.Owned, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	st := mustStatus(t, svc)
	if st.Status != agentlifecycle.StatusNeedsRepair {
		t.Fatalf("a modified asset reported %s: %s", st.Status, st.Message)
	}
	if st.EffectiveTier != agentlifecycle.TierScreenFallback {
		t.Fatalf("a modified asset kept tier %s", st.EffectiveTier)
	}
	mustApply(t, svc, ActionRepair)
	if got := mustStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
		t.Fatalf("after repair: %s", got)
	}
}

func TestAnOutdatedAssetIsUpdatedAndTheReplacedCopyIsRecoverable(t *testing.T) {
	svc, _, paths := fixture(t)
	mustApply(t, svc, ActionInstall)
	writeOldAsset(t, paths.Owned)
	old, err := os.ReadFile(paths.Owned)
	if err != nil {
		t.Fatal(err)
	}

	st := mustStatus(t, svc)
	if st.Status != agentlifecycle.StatusOutdated {
		t.Fatalf("got %s: %s", st.Status, st.Message)
	}
	if st.InstalledVersion != "0" {
		t.Fatalf("installed version %q", st.InstalledVersion)
	}
	// install refuses and points at update rather than quietly converging: the
	// user said something Sidecar knows to be untrue.
	_, err = svc.Plan(OpenCodeProvider, ActionInstall)
	r := refusalFrom(t, err)
	if r.Code != RefuseAlreadyInstalled || !strings.Contains(r.Message, "update") {
		t.Fatalf("install refused with %s: %q", r.Code, r.Message)
	}

	mustApply(t, svc, ActionUpdate)
	if got := mustStatus(t, svc).Status; got != agentlifecycle.StatusCurrent {
		t.Fatalf("after update: %s", got)
	}
	backup, err := os.ReadFile(paths.Backup)
	if err != nil {
		t.Fatalf("no recoverable backup: %v", err)
	}
	if string(backup) != string(old) {
		t.Fatal("the backup is not the file that was replaced")
	}
	// And the backup does not survive an uninstall, or a "clean" tree would
	// still have a Sidecar file in it.
	mustApply(t, svc, ActionUninstall)
	if _, err := os.Stat(paths.Backup); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the backup behind: %v", err)
	}
}

func TestAMissingProviderRefusesInstallButStillAllowsCleanup(t *testing.T) {
	svc, _, paths := fixture(t)
	mustApply(t, svc, ActionInstall)

	gone := Service{Env: svc.Env, Adapters: svc.Adapters}
	withoutProvider(&gone.Env)

	st, err := gone.Status(OpenCodeProvider)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != agentlifecycle.StatusProviderMissing {
		t.Fatalf("got %s", st.Status)
	}
	if !strings.Contains(st.Message, "PATH") {
		t.Fatalf("the message does not say how it decided: %q", st.Message)
	}
	// The installed asset is still visible, so a user who removed opencode can
	// find and remove what Sidecar left behind.
	if st.InstalledVersion == "" {
		t.Fatal("the installed asset disappeared from the report")
	}

	_, err = gone.Plan(OpenCodeProvider, ActionInstall)
	if r := refusalFrom(t, err); r.Code != RefuseProviderMissing {
		t.Fatalf("install refused with %s", r.Code)
	}
	if _, err := gone.Apply(OpenCodeProvider, ActionUninstall); err != nil {
		t.Fatalf("uninstall refused with no provider present: %v", err)
	}
	if _, err := os.Stat(paths.Owned); !os.IsNotExist(err) {
		t.Fatal("uninstall did not remove the asset")
	}
}

func TestThePluginDirectoryInheritsTheConfigDirectorysMode(t *testing.T) {
	// A user who keeps ~/.config/opencode at 0700 should not discover that
	// installing an integration created a world-readable directory inside it.
	svc, _, paths := fixture(t)
	if err := os.Chmod(paths.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mustApply(t, svc, ActionInstall)
	info, err := os.Stat(paths.OwnedDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("the plugin directory is %v, not 0700", info.Mode().Perm())
	}
}

func TestAWriteLeavesNoTemporaryFilesBehind(t *testing.T) {
	// The asset is written through a temp file and a rename so a reader never
	// sees half of it. A leaked temp file would be loaded by OpenCode too.
	svc, _, paths := fixture(t)
	mustApply(t, svc, ActionInstall)
	writeOldAsset(t, paths.Owned)
	mustApply(t, svc, ActionUpdate)

	entries, err := os.ReadDir(paths.OwnedDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("a temporary file was left in the plugin directory: %s", e.Name())
		}
	}
}

func TestOfferedActionsAreExactlyTheOnesThatWouldNotRefuse(t *testing.T) {
	// A surface that offers a button which then refuses is worse than one that
	// hides it, so Offered is derived from the planner rather than restated.
	svc, _, paths := fixture(t)
	for _, stage := range []string{"clean", "installed", "outdated", "damaged"} {
		switch stage {
		case "installed":
			mustApply(t, svc, ActionInstall)
		case "outdated":
			writeOldAsset(t, paths.Owned)
		case "damaged":
			if err := os.WriteFile(paths.Owned, []byte((OpenCodeAdapter{}).asset().Content+"\n// edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		st := mustStatus(t, svc)
		offered := map[Action]bool{}
		for _, a := range st.Offered {
			offered[a] = true
		}
		for _, act := range Actions() {
			_, err := svc.Plan(OpenCodeProvider, act)
			if got, want := offered[act], err == nil; got != want {
				t.Fatalf("%s: %s offered=%v but planning %s", stage, act, got, describeErr(err))
			}
		}
	}
}

func describeErr(err error) string {
	if err == nil {
		return "succeeded"
	}
	return "refused: " + err.Error()
}

func TestProvidersWithNoBundledAssetAreReportedHonestlyRatherThanHidden(t *testing.T) {
	svc, _, _ := fixture(t)
	list := svc.List()
	if len(list) < 2 {
		t.Fatalf("only %d providers listed", len(list))
	}
	shipped := map[string]bool{}
	for _, a := range DefaultAdapters() {
		shipped[a.Provider()] = true
	}
	var sawOpenCode, sawUnsupported bool
	for _, st := range list {
		if st.Provider == OpenCodeProvider {
			sawOpenCode = true
			continue
		}
		if shipped[st.Provider] {
			// A provider with a bundled adapter answers from its own
			// inspection; its honesty is covered by its own suite.
			continue
		}
		if st.Status != agentlifecycle.StatusUnsupported {
			t.Fatalf("%s has no adapter but reports %s", st.Provider, st.Status)
		}
		if st.EffectiveTier != agentlifecycle.TierScreenFallback {
			t.Fatalf("%s is unsupported but claims tier %s", st.Provider, st.EffectiveTier)
		}
		sawUnsupported = true

		_, err := svc.Plan(st.Provider, ActionInstall)
		if r := refusalFrom(t, err); r.Code != RefuseUnsupported {
			t.Fatalf("%s install refused with %s", st.Provider, r.Code)
		}
	}
	if !sawOpenCode || !sawUnsupported {
		t.Fatalf("list did not cover both cases: opencode=%v unsupported=%v", sawOpenCode, sawUnsupported)
	}
	// Deterministically ordered, so two runs of `integration list` never
	// differ: agents that can have an integration first, then the evaluation
	// records, alphabetically within each group.
	//
	// The grouping is load-bearing rather than decorative. There are more
	// evaluation records than integrations, so plain alphabetical order put
	// every actionable row below a screenful of agents the user cannot install.
	seenUnsupported := false
	for i, st := range list {
		unsupported := st.Status == agentlifecycle.StatusUnsupported
		if seenUnsupported && !unsupported {
			t.Fatalf("%s is installable but sorted below an unsupported agent", st.Provider)
		}
		seenUnsupported = seenUnsupported || unsupported
		if i > 0 && list[i-1].Status == st.Status && list[i-1].Provider > st.Provider {
			t.Fatalf("list is not sorted within its group: %s before %s", list[i-1].Provider, st.Provider)
		}
	}
}

func TestAnUnknownProviderIsRefusedWithoutTouchingAnything(t *testing.T) {
	svc, _, _ := fixture(t)
	if _, err := svc.Status("definitely-not-an-agent"); refusalFrom(t, err).Code != RefuseUnknownProvider {
		t.Fatal("an unknown provider was not refused as unknown")
	}
	_, err := svc.Plan("definitely-not-an-agent", ActionInstall)
	if refusalFrom(t, err).Code != RefuseUnknownProvider {
		t.Fatal("planning for an unknown provider was not refused as unknown")
	}
}

func TestEveryRefusalCodeInTheVocabularyIsDistinctAndNamed(t *testing.T) {
	seen := map[RefusalCode]bool{}
	for _, c := range RefusalCodes() {
		if c == "" {
			t.Fatal("an empty refusal code is in the vocabulary")
		}
		if seen[c] {
			t.Fatalf("duplicate refusal code %q", c)
		}
		seen[c] = true
	}
}

func TestAMarkerFromADifferentIntegrationIsNotOwnership(t *testing.T) {
	// Two integrations could each ship an asset; one must never adopt the
	// other's.
	svc, env, paths := fixture(t)
	if err := os.MkdirAll(paths.OwnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "// sidecar-integration: id=sidecar.codex.hooks schema=1 version=1\n"
	if err := os.WriteFile(paths.Owned, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	st := inspectFile(env, paths.Owned, (OpenCodeAdapter{}).asset())
	if st.Owned {
		t.Fatal("an asset belonging to another integration was claimed as ours")
	}
	_, err := svc.Plan(OpenCodeProvider, ActionUninstall)
	if r := refusalFrom(t, err); r.Code != RefuseForeignFile {
		t.Fatalf("uninstall refused with %s", r.Code)
	}
}
