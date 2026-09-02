package agentintegration

import (
	"io/fs"
	"path/filepath"

	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/agentsession"
)

// The Pi adapter.
//
// Pi's integration shape is the simplest in the catalog: one file dropped into
// an extension directory, no configuration file to edit, no feature flag, no
// trust record, and no executable bit. Herdr's own installer is three lines --
// resolve the directory, refuse if Pi has never been installed, write the file.
// Everything else here is Sidecar's ownership contract, which is stricter than
// Herdr's in ways worth naming: Herdr's uninstall deletes its file without
// checking a marker, and Sidecar's never removes anything it cannot prove it
// wrote.
//
// Two facts about where Pi reads extensions, both read from Pi 0.84.3 rather
// than from prose, because a plan draft got them wrong:
//
//   - The directory is `$PI_CODING_AGENT_DIR/extensions`, tilde-expanded, and
//     `~/.pi/agent/extensions` when that variable is unset (config.js:405,
//     420-426). There is no PI_CONFIG_DIR; that is Herdr's own override for OMP.
//   - Pi also loads a project-local `<cwd>/.pi/extensions`. Sidecar deliberately
//     does not install there. A per-project copy would follow a checkout into
//     other people's clones and would report from panes Sidecar never set up,
//     and the user-level directory is the one a managed shell always sees.
//
// The collision refusal in Herdr's installer belongs to OMP, not to Pi: OMP is a
// rebranded fork of the same codebase, so setting PI_CODING_AGENT_DIR collapses
// both onto one directory. Sidecar has no OMP adapter yet, so that is the next
// slice's problem and is named here so it is not rediscovered as a bug.

// PiAssetSchema is the marker schema version the bundled asset declares. It
// changes only when the marker line's own format changes, which is why it is
// separate from the asset version.
const PiAssetSchema = 1

// PiBackupSuffix is appended to the asset's name for the recoverable copy kept
// when an installed asset is replaced.
const PiBackupSuffix = ".sidecar-backup"

// PiExtensionsDir is the directory name Pi scans inside its agent directory.
const PiExtensionsDir = "extensions"

// The default agent directory Pi uses when PI_CODING_AGENT_DIR is unset used to
// be spelled out here as well. It is not any more: it is part of the one
// derivation in agentsession.PiAgentDir, and a second copy of a fact that has
// already drifted once is not documentation.

// PiAdapter installs Sidecar's Pi lifecycle extension.
type PiAdapter struct{}

func (PiAdapter) Provider() string { return PiProvider }
func (PiAdapter) Source() string   { return PiSource }

// Assets returns the bundled extension with the identity a marker check compares
// against.
func (PiAdapter) Assets() []Asset {
	return []Asset{{
		Name:          PiAssetName,
		Source:        PiSource,
		SchemaVersion: PiAssetSchema,
		Version:       PiAssetVersion,
		Ownership:     OwnsFile,
		Content:       piAsset,
	}}
}

// asset is the single file this integration drops. Pi loads whole extension
// files from a directory it scans, so there is exactly one and it is Sidecar's.
func (a PiAdapter) asset() Asset { return a.Assets()[0] }

// piPaths are the exact user-level paths this adapter inspects.
type piPaths struct {
	AgentDir string
	OwnedDir string
	Owned    string
	Backup   string
}

// PiPaths returns the paths the Pi adapter would inspect and touch.
//
// It is exported because "show the exact paths before mutating" is a rule, and a
// surface that wants to name them before asking for confirmation should not have
// to reconstruct them.
func PiPaths(env Env) []string {
	return []string{piPathsFor(env).Owned}
}

func piPathsFor(env Env) piPaths {
	agent := piAgentDir(env)
	owned := filepath.Join(agent, PiExtensionsDir)
	return piPaths{
		AgentDir: agent,
		OwnedDir: owned,
		Owned:    filepath.Join(owned, PiAssetName),
		Backup:   filepath.Join(owned, PiAssetName+PiBackupSuffix),
	}
}

// piAgentDir resolves Pi's agent directory the way Pi itself does.
//
// The derivation itself is agentsession's, and calling it rather than repeating
// it is the point. The same directory decides two independent things -- where
// this installer writes the extension, and which store root a session binding
// from that extension is allowed to name -- and when the two derived it
// separately they drifted: this side trimmed the environment value and the root
// did not, so a whitespace-only PI_CODING_AGENT_DIR installed into
// ~/.pi/agent/extensions while the approved root became "  /sessions" and every
// binding was refused. An installer that works and a trust boundary that refuses
// everything it sends is the worst shape that disagreement can take, because
// nothing fails loudly.
func piAgentDir(env Env) string {
	return agentsession.PiAgentDir(env.Home, env.PiAgentDir)
}

// piNeverSetUp is the one sentence for "pi's agent directory is not there".
//
// It is a function because the same fact has to reach a user through two
// different surfaces -- the refusal a caller gets from Plan, and the message on a
// status that offers no install -- and a status that stayed silent while the
// refusal explained itself is how the missing action looked like a bug.
func piNeverSetUp(agentDir string) string {
	return "pi's agent directory " + agentDir + " does not exist, so pi has not been set up on this machine; " +
		"run pi once (or set PI_CODING_AGENT_DIR) and try again"
}

// piState is everything one inspection learned. Both [PiAdapter.Inspect] and
// [PiAdapter.Plan] are built from it, so a plan can never be based on a
// different reading of the disk than the status the user was shown.
type piState struct {
	env    Env
	paths  piPaths
	asset  Asset
	agent  FileState
	dir    FileState
	owned  FileState
	backup FileState

	providerPath    string
	providerVersion string

	// assetStatus is what the files alone say. status is that overlaid with
	// provider availability, which is what the user is shown.
	assetStatus agentlifecycle.IntegrationStatus
	status      agentlifecycle.IntegrationStatus
	message     string
	installed   string
}

func (a PiAdapter) inspect(env Env) piState {
	asset := a.asset()
	p := piPathsFor(env)
	s := piState{
		env:    env,
		paths:  p,
		asset:  asset,
		agent:  inspectDir(env, p.AgentDir),
		dir:    inspectDir(env, p.OwnedDir),
		owned:  inspectFile(env, p.Owned, asset),
		backup: inspectFile(env, p.Backup, asset),
	}
	if path, ok := env.lookPath(PiProvider); ok {
		s.providerPath = path
		s.providerVersion = env.providerVersion(PiProvider)
	}
	s.assetStatus, s.message = piAssetStatus(s)
	if s.owned.Owned {
		s.installed = s.owned.Version
	}

	s.status = s.assetStatus
	if s.providerPath == "" {
		// The provider CLI being absent is the more actionable of the two true
		// statements, and it is also the one that decides authority: with no pi
		// there is nothing to load the extension, so TierFor is right to return
		// screen fallback. The asset's own state is still reported in the
		// message and in Files, so an uninstall after removing the provider is
		// still discoverable.
		s.status = agentlifecycle.StatusProviderMissing
		s.message = "the pi CLI was not found on PATH" + orEmpty("; "+s.message, s.message != "")
	}
	return s
}

// piAssetStatus decides the status from the inspected files alone.
//
// Nothing here trusts a version a report claimed. The installed bytes are hashed
// and compared with the bundled asset's hash, so a truncated, hand-edited, or
// half-written asset reads as needs-repair rather than as current.
func piAssetStatus(s piState) (agentlifecycle.IntegrationStatus, string) {
	switch {
	case s.dir.Exists && s.dir.Unsafe != "":
		return agentlifecycle.StatusNeedsRepair, s.dir.UnsafeDetail + " (" + s.paths.OwnedDir + ")"
	case s.owned.Exists && s.owned.Unsafe != "":
		return agentlifecycle.StatusNeedsRepair, s.owned.UnsafeDetail + " (" + s.paths.Owned + ")"
	case s.owned.Exists && !s.owned.Owned:
		// Herdr's own Pi extension lives in this same directory on a machine
		// that has both installed, under a different name. It is not Sidecar's
		// and Sidecar never touches it; only a foreign file at Sidecar's own
		// asset path is a problem, and even then the answer is to say so rather
		// than to adopt it.
		return agentlifecycle.StatusNeedsRepair, "a file that is not Sidecar's occupies " + s.paths.Owned + "; Sidecar will not modify or remove it"
	case !s.owned.Exists:
		if !s.agent.Exists {
			// The status has to carry this, not only the refusal. Without it a
			// machine where pi is on PATH but has never been run reads as a
			// plain not-installed with an empty message and no install offered,
			// and nothing anywhere on the status surface says why the one action
			// that would fix it is missing. Offered is computed by asking the
			// planner, so the absence is real; this is the sentence that
			// explains it. It is deliberately the same sentence planConverge
			// refuses with.
			return agentlifecycle.StatusNotInstalled, piNeverSetUp(s.paths.AgentDir)
		}
		return agentlifecycle.StatusNotInstalled, ""
	case s.owned.Checksum == s.asset.Checksum():
		return agentlifecycle.StatusCurrent, ""
	case s.owned.Version != s.asset.Version:
		return agentlifecycle.StatusOutdated, "version " + s.owned.Version + " is installed; this build ships version " + s.asset.Version
	}
	return agentlifecycle.StatusNeedsRepair, "the installed asset claims version " + s.owned.Version + " but its contents do not match the bundled asset"
}

// Inspect implements [Adapter].
func (a PiAdapter) Inspect(env Env) Status {
	return a.statusOf(a.inspect(env))
}

func (a PiAdapter) statusOf(s piState) Status {
	capability, _ := agentlifecycle.CapabilityForSource(PiSource)
	inRange := versionInRange(s.providerVersion, capability.TestedProviderRange)
	tier, reason := capability.TierFor(s.status, inRange)

	st := Status{IntegrationReport: agentlifecycle.IntegrationReport{
		SchemaVersion:         agentlifecycle.SchemaVersion,
		Provider:              PiProvider,
		Source:                PiSource,
		Status:                s.status,
		BundledVersion:        s.asset.Version,
		InstalledVersion:      s.installed,
		ProviderVersion:       s.providerVersion,
		ProviderInTestedRange: inRange,
		EffectiveTier:         tier,
		TierReason:            reason,
		TargetPaths:           []string{s.paths.Owned},
		KnownGaps:             capability.KnownGaps,
		Message:               s.message,
	}}
	st.ProviderPath = s.providerPath
	st.Files = []FileState{s.dir, s.owned, s.backup}

	// Offered is computed by asking the planner, not by restating its rules in a
	// second place. A verb a surface offers is therefore a verb that will not
	// refuse when it is pressed.
	for _, act := range Actions() {
		if _, err := a.plan(s, act); err == nil {
			st.Offered = append(st.Offered, act)
		}
	}
	return st
}

// Plan implements [Adapter].
func (a PiAdapter) Plan(env Env, act Action) (Plan, error) {
	return a.plan(a.inspect(env), act)
}

func (a PiAdapter) plan(s piState, act Action) (Plan, error) {
	p := Plan{
		SchemaVersion: InstallSchemaVersion,
		Provider:      PiProvider,
		Source:        PiSource,
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
// at the bundled version, in the directory Pi loads.
//
// install, update, and repair share it because the target state is identical;
// they differ only in which starting states they accept.
func (a PiAdapter) planConverge(s piState, p Plan, act Action) (Plan, error) {
	if s.providerPath == "" {
		return Plan{}, refuse(RefuseProviderMissing, "",
			"the pi CLI was not found on PATH, so Sidecar will not create %s for it; install pi first", s.paths.OwnedDir)
	}
	// Herdr's ensure_extension_dir creates the extensions directory only when
	// its parent already exists, and errors with "install pi first" otherwise.
	// The semantics are worth keeping and the reason is not fussiness: Pi's
	// agent directory is created by Pi, so its absence means Pi has never run
	// here, and creating a whole ~/.pi/agent tree for an agent that may be about
	// to be configured somewhere else is Sidecar inventing a provider's private
	// state. Expressed as Sidecar's own refusal code rather than as an io error.
	if !s.agent.Exists {
		return Plan{}, refuse(RefuseProviderMissing, s.paths.AgentDir, "%s", piNeverSetUp(s.paths.AgentDir))
	}

	// Which starting states each verb accepts. The point of separate verbs is
	// that the user says what they believe the situation is, and Sidecar
	// disagrees out loud when it is something else.
	switch s.assetStatus {
	case agentlifecycle.StatusNotInstalled:
		if act != ActionInstall {
			return Plan{}, refuse(RefuseNotInstalled, s.paths.Owned,
				"nothing is installed at %s; run sidecar agent integration install %s", s.paths.Owned, PiProvider)
		}
	case agentlifecycle.StatusOutdated:
		if act == ActionInstall {
			return Plan{}, refuse(RefuseAlreadyInstalled, s.paths.Owned,
				"version %s is already installed at %s; run sidecar agent integration update %s", s.installed, s.paths.Owned, PiProvider)
		}
	case agentlifecycle.StatusNeedsRepair:
		if act != ActionRepair {
			return Plan{}, refuse(RefuseNeedsRepair, s.paths.Owned,
				"the installation needs repair (%s); run sidecar agent integration repair %s", s.message, PiProvider)
		}
	}

	// Safety. Every path this plan would write or remove is proved usable here,
	// before a single operation is emitted, so Apply never has to decide
	// anything.
	if s.dir.Exists && s.dir.Unsafe != "" {
		return Plan{}, refuse(s.dir.Unsafe, s.paths.OwnedDir, "%s: %s", s.paths.OwnedDir, s.dir.UnsafeDetail)
	}
	if s.agent.Exists && s.agent.Unsafe != "" {
		return Plan{}, refuse(s.agent.Unsafe, s.paths.AgentDir, "%s: %s", s.paths.AgentDir, s.agent.UnsafeDetail)
	}
	if s.owned.Exists && s.owned.Unsafe != "" {
		return Plan{}, refuse(s.owned.Unsafe, s.paths.Owned, "%s: %s", s.paths.Owned, s.owned.UnsafeDetail)
	}
	if s.owned.Exists && !s.owned.Owned {
		return Plan{}, refuse(RefuseForeignFile, s.paths.Owned,
			"%s exists but does not carry Sidecar's integration marker, so Sidecar will not overwrite it; move or delete it yourself and run this again", s.paths.Owned)
	}
	if s.backup.Exists && s.backup.Unsafe != "" {
		return Plan{}, refuse(s.backup.Unsafe, s.paths.Backup, "%s: %s", s.paths.Backup, s.backup.UnsafeDetail)
	}

	if !s.dir.Exists {
		mode := fs.FileMode(0o755)
		if s.agent.Exists && s.agent.Mode != "" {
			// Inherit the agent directory's mode rather than imposing 0755. A
			// user who keeps ~/.pi/agent at 0700 should not find that installing
			// an integration created a world-readable directory inside it.
			if m := parseMode(s.agent.Mode); m != 0 {
				mode = m
			}
		}
		p.Ops = append(p.Ops, Op{
			Kind:   OpMkdir,
			Path:   s.paths.OwnedDir,
			Mode:   renderMode(mode),
			mode:   mode,
			Note:   "create the extension directory Pi loads",
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
			Note:     "write version " + s.asset.Version + " of the Sidecar lifecycle extension",
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
//
// Herdr's uninstall_pi deletes its file without checking that the file is still
// its own. Sidecar does not copy that: ownership is proved from the file's own
// bytes, and a file that has stopped carrying Sidecar's marker is somebody
// else's now.
func (a PiAdapter) planUninstall(s piState, p Plan) (Plan, error) {
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
			Note:   "remove the Sidecar lifecycle extension",
			Before: s.owned, After: FileState{Path: s.paths.Owned},
		})
		removed = append(removed, s.paths.Owned)
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

	// The extension directory goes only if removing Sidecar's own files empties
	// it. On a machine that also has Herdr installed this directory holds
	// Herdr's own extension, which is exactly the case this rule exists for.
	if dirEmptyExcept(s.paths.OwnedDir, removed) {
		p.Ops = append(p.Ops, Op{
			Kind: OpRmdir, Path: s.paths.OwnedDir,
			Note:   "remove the extension directory, which holds nothing else",
			Before: s.dir, After: FileState{Path: s.paths.OwnedDir},
		})
	}

	p.StatusAfter = agentlifecycle.StatusNotInstalled
	if s.providerPath == "" {
		p.StatusAfter = agentlifecycle.StatusProviderMissing
	}
	return p, nil
}

var _ Adapter = PiAdapter{}
