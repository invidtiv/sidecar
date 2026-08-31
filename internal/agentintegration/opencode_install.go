package agentintegration

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// The OpenCode adapter.
//
// OpenCode is the simplest integration shape there is: one file dropped into a
// plugin directory, and no existing user configuration file to merge with. That
// makes it a poor test of a merge engine and an excellent test of everything
// else — ownership, checksums, the double-load trap, atomic replacement,
// backups, and an uninstall that removes exactly what it put there.
//
// The one genuinely surprising thing about it is measured, not assumed:
// OpenCode loads BOTH ~/.config/opencode/plugin/ and
// ~/.config/opencode/plugins/. A copy of the asset in each fires every event
// twice, which doubles every sequence number and makes the store's ordering
// contract meaningless. Sidecar therefore owns exactly one of those directories
// and treats anything with its asset's name in the other as damage: it is
// reported as needs-repair, it is removed by repair when Sidecar owns it, and
// it is refused rather than deleted when Sidecar does not.

// OpenCodeAssetSchema is the marker schema version the bundled asset declares.
// It changes only when the marker line's own format changes, which is why it is
// separate from the asset version.
const OpenCodeAssetSchema = 1

// OpenCodeBackupSuffix is appended to the asset's name for the recoverable
// copy kept when an installed asset is replaced.
const OpenCodeBackupSuffix = ".sidecar-backup"

// OpenCodeAdapter installs Sidecar's OpenCode lifecycle plugin.
type OpenCodeAdapter struct{}

func (OpenCodeAdapter) Provider() string { return OpenCodeProvider }
func (OpenCodeAdapter) Source() string   { return OpenCodeSource }

// Asset returns the bundled plugin with the identity a marker check compares
// against.
func (OpenCodeAdapter) Assets() []Asset {
	return []Asset{{
		Name:          OpenCodeAssetName,
		Source:        OpenCodeSource,
		SchemaVersion: OpenCodeAssetSchema,
		Version:       OpenCodeAssetVersion,
		Ownership:     OwnsFile,
		Content:       openCodeAsset,
	}}
}

// asset is the single file this integration drops. OpenCode loads whole plugin
// files from a directory it scans, so there is exactly one and it is Sidecar's.
func (a OpenCodeAdapter) asset() Asset { return a.Assets()[0] }

// openCodePaths are the exact user-level paths this adapter inspects.
type openCodePaths struct {
	ConfigDir   string
	OwnedDir    string
	Owned       string
	ConflictDir string
	Conflict    string
	Backup      string
}

// OpenCodePaths returns the paths the OpenCode adapter would inspect and touch.
//
// It is exported because "show the exact paths before mutating" is a rule, and
// a surface that wants to name them before asking for confirmation should not
// have to reconstruct them.
func OpenCodePaths(env Env) []string {
	p := openCodePathsFor(env)
	return []string{p.Owned, p.Conflict}
}

func openCodePathsFor(env Env) openCodePaths {
	config := env.ConfigHome
	if config == "" {
		config = filepath.Join(env.Home, ".config")
	}
	dir := filepath.Join(config, OpenCodeProvider)
	owned := filepath.Join(dir, OpenCodeOwnedDir)
	conflict := filepath.Join(dir, OpenCodeConflictDir)
	return openCodePaths{
		ConfigDir:   dir,
		OwnedDir:    owned,
		Owned:       filepath.Join(owned, OpenCodeAssetName),
		ConflictDir: conflict,
		Conflict:    filepath.Join(conflict, OpenCodeAssetName),
		Backup:      filepath.Join(owned, OpenCodeAssetName+OpenCodeBackupSuffix),
	}
}

// openCodeState is everything one inspection learned. Both [OpenCodeAdapter.Inspect]
// and [OpenCodeAdapter.Plan] are built from it, so a plan can never be based on
// a different reading of the disk than the status the user was shown.
type openCodeState struct {
	env    Env
	paths  openCodePaths
	asset  Asset
	config FileState
	dir    FileState
	owned  FileState
	// conflict is the copy in the directory Sidecar does not own.
	conflict FileState
	backup   FileState

	providerPath    string
	providerVersion string

	// assetStatus is what the files alone say. status is that overlaid with
	// provider availability, which is what the user is shown.
	assetStatus agentlifecycle.IntegrationStatus
	status      agentlifecycle.IntegrationStatus
	message     string
	installed   string
}

func (a OpenCodeAdapter) inspect(env Env) openCodeState {
	asset := a.asset()
	p := openCodePathsFor(env)
	s := openCodeState{
		env:      env,
		paths:    p,
		asset:    asset,
		config:   inspectDir(env, p.ConfigDir),
		dir:      inspectDir(env, p.OwnedDir),
		owned:    inspectFile(env, p.Owned, asset),
		conflict: inspectFile(env, p.Conflict, asset),
		backup:   inspectFile(env, p.Backup, asset),
	}
	if path, ok := env.lookPath(OpenCodeProvider); ok {
		s.providerPath = path
		s.providerVersion = env.providerVersion(OpenCodeProvider)
	}
	s.assetStatus, s.message = openCodeAssetStatus(s)
	switch {
	case s.owned.Owned:
		s.installed = s.owned.Version
	case s.conflict.Owned:
		s.installed = s.conflict.Version
	}

	s.status = s.assetStatus
	if s.providerPath == "" {
		// The provider CLI being absent is the more actionable of the two true
		// statements, and it is also the one that decides authority: with no
		// opencode there is nothing to emit reports, so TierFor is right to
		// return screen fallback. The asset's own state is still reported in
		// the message and in Files, so an uninstall after removing the provider
		// is still discoverable.
		s.status = agentlifecycle.StatusProviderMissing
		s.message = "the opencode CLI was not found on PATH" + orEmpty("; "+s.message, s.message != "")
	}
	return s
}

func orEmpty(s string, when bool) string {
	if when {
		return s
	}
	return ""
}

// openCodeAssetStatus decides the status from the inspected files alone.
//
// This is the file-inspection status the Phase B integration lacked: nothing
// here trusts a version a report claimed. The installed bytes are hashed and
// compared with the bundled asset's hash, so a truncated, hand-edited, or
// half-written asset reads as needs-repair rather than as current.
func openCodeAssetStatus(s openCodeState) (agentlifecycle.IntegrationStatus, string) {
	switch {
	case s.dir.Exists && s.dir.Unsafe != "":
		return agentlifecycle.StatusNeedsRepair, s.dir.UnsafeDetail + " (" + s.paths.OwnedDir + ")"
	case s.owned.Exists && s.owned.Unsafe != "":
		return agentlifecycle.StatusNeedsRepair, s.owned.UnsafeDetail + " (" + s.paths.Owned + ")"
	case s.conflict.Exists && s.conflict.Unsafe != "":
		return agentlifecycle.StatusNeedsRepair, s.conflict.UnsafeDetail + " (" + s.paths.Conflict + ")"
	case s.owned.Exists && !s.owned.Owned:
		return agentlifecycle.StatusNeedsRepair, "a file that is not Sidecar's occupies " + s.paths.Owned + "; Sidecar will not modify or remove it"
	case s.conflict.Exists:
		// Anything with the asset's name in the directory Sidecar does not own
		// is damage whether or not Sidecar wrote it: OpenCode loads both
		// directories, so this file fires every event a second time.
		if s.owned.Owned {
			return agentlifecycle.StatusNeedsRepair, "the asset is installed in both " + OpenCodeOwnedDir + "/ and " + OpenCodeConflictDir + "/; OpenCode loads both, so every event is reported twice"
		}
		if s.conflict.Owned {
			return agentlifecycle.StatusNeedsRepair, "the asset is installed in " + OpenCodeConflictDir + "/, which Sidecar does not own; move it to " + OpenCodeOwnedDir + "/"
		}
		return agentlifecycle.StatusNotInstalled, "a file that is not Sidecar's occupies " + s.paths.Conflict + "; Sidecar will not modify or remove it"
	case !s.owned.Exists:
		return agentlifecycle.StatusNotInstalled, ""
	case s.owned.Checksum == s.asset.Checksum():
		return agentlifecycle.StatusCurrent, ""
	case s.owned.Version != s.asset.Version:
		return agentlifecycle.StatusOutdated, "version " + s.owned.Version + " is installed; this build ships version " + s.asset.Version
	}
	return agentlifecycle.StatusNeedsRepair, "the installed asset claims version " + s.owned.Version + " but its contents do not match the bundled asset"
}

// Inspect implements [Adapter].
func (a OpenCodeAdapter) Inspect(env Env) Status {
	return a.statusOf(a.inspect(env))
}

func (a OpenCodeAdapter) statusOf(s openCodeState) Status {
	capability, _ := agentlifecycle.CapabilityForSource(OpenCodeSource)
	inRange := versionInRange(s.providerVersion, capability.TestedProviderRange)
	tier, reason := capability.TierFor(s.status, inRange)

	st := Status{IntegrationReport: agentlifecycle.IntegrationReport{
		SchemaVersion:         agentlifecycle.SchemaVersion,
		Provider:              OpenCodeProvider,
		Source:                OpenCodeSource,
		Status:                s.status,
		BundledVersion:        s.asset.Version,
		InstalledVersion:      s.installed,
		ProviderVersion:       s.providerVersion,
		ProviderInTestedRange: inRange,
		EffectiveTier:         tier,
		TierReason:            reason,
		TargetPaths:           []string{s.paths.Owned, s.paths.Conflict},
		KnownGaps:             capability.KnownGaps,
		Message:               s.message,
	}}
	st.ProviderPath = s.providerPath
	st.Files = []FileState{s.dir, s.owned, s.conflict, s.backup}

	// Offered is computed by asking the planner, not by restating its rules in
	// a second place. A verb a surface offers is therefore a verb that will not
	// refuse when it is pressed.
	for _, act := range Actions() {
		if _, err := a.plan(s, act); err == nil {
			st.Offered = append(st.Offered, act)
		}
	}
	return st
}

// Plan implements [Adapter].
func (a OpenCodeAdapter) Plan(env Env, act Action) (Plan, error) {
	return a.plan(a.inspect(env), act)
}

func (a OpenCodeAdapter) plan(s openCodeState, act Action) (Plan, error) {
	p := Plan{
		SchemaVersion: InstallSchemaVersion,
		Provider:      OpenCodeProvider,
		Source:        OpenCodeSource,
		Action:        act,
		StatusBefore:  s.status,
		StatusAfter:   s.status,
	}
	switch act {
	case ActionUninstall:
		return a.planUninstall(s, p)
	case ActionInstall, ActionUpdate, ActionRepair:
		return a.planConverge(s, p, act)
	}
	return Plan{}, refuse(RefuseUnknownProvider, "", "unknown action %q", act)
}

// planConverge builds the plan that ends with exactly one Sidecar-owned asset,
// at the bundled version, in the directory Sidecar owns.
//
// install, update, and repair share it because the target state is identical;
// they differ only in which starting states they accept. Keeping one
// convergence means the three verbs cannot drift into producing three slightly
// different installations.
func (a OpenCodeAdapter) planConverge(s openCodeState, p Plan, act Action) (Plan, error) {
	if s.providerPath == "" {
		return Plan{}, refuse(RefuseProviderMissing, "",
			"the opencode CLI was not found on PATH, so Sidecar will not create %s for it; install opencode first", s.paths.OwnedDir)
	}

	// Which starting states each verb accepts. The point of separate verbs is
	// that the user says what they believe the situation is, and Sidecar
	// disagrees out loud when it is something else.
	switch s.assetStatus {
	case agentlifecycle.StatusNotInstalled:
		if act != ActionInstall {
			return Plan{}, refuse(RefuseNotInstalled, s.paths.Owned,
				"nothing is installed at %s; run sidecar agent integration install %s", s.paths.Owned, OpenCodeProvider)
		}
	case agentlifecycle.StatusOutdated:
		if act == ActionInstall {
			return Plan{}, refuse(RefuseAlreadyInstalled, s.paths.Owned,
				"version %s is already installed at %s; run sidecar agent integration update %s", s.installed, s.paths.Owned, OpenCodeProvider)
		}
	case agentlifecycle.StatusNeedsRepair:
		if act != ActionRepair {
			return Plan{}, refuse(RefuseNeedsRepair, s.paths.Owned,
				"the installation needs repair (%s); run sidecar agent integration repair %s", s.message, OpenCodeProvider)
		}
	}

	// Safety. Every path this plan would write or remove is proved usable here,
	// before a single operation is emitted, so Apply never has to decide
	// anything.
	if s.dir.Exists && s.dir.Unsafe != "" {
		return Plan{}, refuse(s.dir.Unsafe, s.paths.OwnedDir, "%s: %s", s.paths.OwnedDir, s.dir.UnsafeDetail)
	}
	if s.config.Exists && s.config.Unsafe != "" {
		return Plan{}, refuse(s.config.Unsafe, s.paths.ConfigDir, "%s: %s", s.paths.ConfigDir, s.config.UnsafeDetail)
	}
	if s.owned.Exists && s.owned.Unsafe != "" {
		return Plan{}, refuse(s.owned.Unsafe, s.paths.Owned, "%s: %s", s.paths.Owned, s.owned.UnsafeDetail)
	}
	if s.owned.Exists && !s.owned.Owned {
		return Plan{}, refuse(RefuseForeignFile, s.paths.Owned,
			"%s exists but does not carry Sidecar's integration marker, so Sidecar will not overwrite it; move or delete it yourself and run this again", s.paths.Owned)
	}
	if s.conflict.Exists && !s.conflict.Owned {
		return Plan{}, refuse(RefuseForeignFile, s.paths.Conflict,
			"%s exists but does not carry Sidecar's integration marker; OpenCode loads both %s/ and %s/, so it would report every event a second time. Sidecar will not remove a file it does not own — move or delete it yourself",
			s.paths.Conflict, OpenCodeOwnedDir, OpenCodeConflictDir)
	}
	if s.backup.Exists && s.backup.Unsafe != "" {
		return Plan{}, refuse(s.backup.Unsafe, s.paths.Backup, "%s: %s", s.paths.Backup, s.backup.UnsafeDetail)
	}

	// The conflicting copy goes first. If the run is interrupted after this
	// operation the pane is momentarily uninstrumented, which is a state
	// Sidecar handles by falling back to screen detection; interrupting after a
	// write instead would leave the double-firing installation the user asked
	// to be rid of.
	if s.conflict.Exists && s.conflict.Owned {
		p.Ops = append(p.Ops, Op{
			Kind:   OpRemove,
			Path:   s.paths.Conflict,
			Note:   "remove the duplicate copy: OpenCode loads both " + OpenCodeOwnedDir + "/ and " + OpenCodeConflictDir + "/, and a copy in each reports every event twice",
			Before: s.conflict,
			After:  FileState{Path: s.paths.Conflict},
		})
	}

	if !s.dir.Exists {
		mode := fs.FileMode(0o755)
		if s.config.Exists && s.config.Mode != "" {
			// Inherit the config directory's mode rather than imposing 0755. A
			// user who keeps ~/.config/opencode at 0700 should not find that
			// installing an integration created a world-readable directory
			// inside it.
			if m := parseMode(s.config.Mode); m != 0 {
				mode = m
			}
		}
		p.Ops = append(p.Ops, Op{
			Kind:   OpMkdir,
			Path:   s.paths.OwnedDir,
			Mode:   renderMode(mode),
			mode:   mode,
			Note:   "create the plugin directory OpenCode loads",
			Before: s.dir,
			After:  FileState{Path: s.paths.OwnedDir, Exists: true, Kind: "dir", Mode: renderMode(mode)},
		})
	}

	if s.owned.Owned && s.owned.Checksum != s.asset.Checksum() {
		p.Ops = append(p.Ops, Op{
			Kind:     OpBackup,
			Path:     s.paths.Backup,
			From:     s.paths.Owned,
			Mode:     "0644",
			mode:     0o644,
			Bytes:    int(s.owned.Size),
			Checksum: s.owned.Checksum,
			Note:     "keep a recoverable copy of the asset being replaced",
			Before:   s.backup,
			After: FileState{
				Path: s.paths.Backup, Exists: true, Kind: "file", Owned: true,
				Version: s.owned.Version, Checksum: s.owned.Checksum, Mode: "0644", Size: s.owned.Size,
			},
		})
	}

	if s.owned.Checksum != s.asset.Checksum() {
		content := []byte(s.asset.Content)
		p.Ops = append(p.Ops, Op{
			Kind:     OpWrite,
			Path:     s.paths.Owned,
			Mode:     "0644",
			mode:     0o644,
			Bytes:    len(content),
			Checksum: s.asset.Checksum(),
			content:  content,
			Note:     "write version " + s.asset.Version + " of the Sidecar lifecycle plugin",
			Before:   s.owned,
			After: FileState{
				Path: s.paths.Owned, Exists: true, Kind: "file", Owned: true,
				Version: s.asset.Version, Checksum: s.asset.Checksum(), Mode: "0644", Size: int64(len(content)),
			},
		})
	}

	if len(p.Ops) == 0 {
		p.Unchanged = true
		return p, nil
	}
	p.StatusAfter = agentlifecycle.StatusCurrent
	return p, nil
}

// planUninstall removes exactly what Sidecar put there and nothing else.
func (a OpenCodeAdapter) planUninstall(s openCodeState, p Plan) (Plan, error) {
	if s.owned.Exists && s.owned.Unsafe != "" {
		return Plan{}, refuse(s.owned.Unsafe, s.paths.Owned, "%s: %s", s.paths.Owned, s.owned.UnsafeDetail)
	}
	if s.owned.Exists && !s.owned.Owned {
		return Plan{}, refuse(RefuseForeignFile, s.paths.Owned,
			"%s does not carry Sidecar's integration marker, so Sidecar will not delete it; there is nothing here that Sidecar installed", s.paths.Owned)
	}

	var removed []string
	if s.owned.Owned {
		p.Ops = append(p.Ops, Op{
			Kind: OpRemove, Path: s.paths.Owned,
			Note:   "remove the Sidecar lifecycle plugin",
			Before: s.owned, After: FileState{Path: s.paths.Owned},
		})
		removed = append(removed, s.paths.Owned)
	}
	if s.conflict.Owned {
		p.Ops = append(p.Ops, Op{
			Kind: OpRemove, Path: s.paths.Conflict,
			Note:   "remove the duplicate copy Sidecar owns in " + OpenCodeConflictDir + "/",
			Before: s.conflict, After: FileState{Path: s.paths.Conflict},
		})
		removed = append(removed, s.paths.Conflict)
	}
	if s.backup.Owned {
		p.Ops = append(p.Ops, Op{
			Kind: OpRemove, Path: s.paths.Backup,
			Note:   "remove the backup Sidecar kept of a replaced asset",
			Before: s.backup, After: FileState{Path: s.paths.Backup},
		})
		removed = append(removed, s.paths.Backup)
	}

	if len(p.Ops) == 0 {
		p.Unchanged = true
		return p, nil
	}

	// The plugin directory goes only if removing Sidecar's own files empties
	// it. Anything else in there belongs to the user, and a directory holding
	// another plugin is not Sidecar's to delete.
	if dirEmptyExcept(s.paths.OwnedDir, removed) {
		p.Ops = append(p.Ops, Op{
			Kind: OpRmdir, Path: s.paths.OwnedDir,
			Note:   "remove the plugin directory, which holds nothing else",
			Before: s.dir, After: FileState{Path: s.paths.OwnedDir},
		})
	}

	p.StatusAfter = agentlifecycle.StatusNotInstalled
	if s.providerPath == "" {
		p.StatusAfter = agentlifecycle.StatusProviderMissing
	}
	return p, nil
}

// dirEmptyExcept reports whether dir contains nothing but the given paths.
func dirEmptyExcept(dir string, paths []string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	keep := map[string]bool{}
	for _, p := range paths {
		if filepath.Dir(p) == dir {
			keep[filepath.Base(p)] = true
		}
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			return false
		}
	}
	return true
}

var _ Adapter = OpenCodeAdapter{}
