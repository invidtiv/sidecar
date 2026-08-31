package agentintegration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// The Codex adapter.
//
// Codex needs three owned mutations where Claude needs one, all for the same
// single SessionStart hook:
//
//  1. ~/.codex/hooks.json — the hook entry itself, in a group without a
//     matcher key (unlike Claude's).
//  2. ~/.codex/config.toml — `[features]` `hooks = true`, without which hooks
//     are disabled entirely.
//  3. ~/.codex/config.toml — a `[hooks.state."<key>"]` table whose
//     trusted_hash marks the hook as user-approved; without it Codex prompts
//     for or refuses the hook on every run.
//
// hooks.json follows the shared entry-ownership model in hookconfig.go.
// config.toml is a user-owned TOML file full of comments and unrelated
// configuration, so it is edited line-surgically — never re-serialized — and
// any spelling of the two regions Sidecar must touch that the editor does not
// understand is refused rather than guessed at. Every composed result is
// re-scanned before it is allowed into a plan, so a rewrite that would not
// verify never reaches disk.
//
// # The trust hash
//
// The trusted_hash algorithm was reproduced from the codex-rs source
// (codex-rs/hooks/src/engine/discovery.rs hook_hash and
// codex-rs/config/src/fingerprint.rs version_for_toml) and verified by
// reproducing a live codex-cli 0.151.0 trust record byte for byte: the hash is
// `sha256:` + hex(SHA-256(canonical JSON)) of the normalized hook identity
// {"event_name":"session_start","hooks":[{"async":false,"command":C,
// "timeout":T,"type":"command"}]} with object keys sorted recursively, absent
// optional fields omitted, and no whitespace. The state key is positional —
// "<abs hooks.json path>:session_start:<group>:<hook>" — which is why the key
// is computed from where the entry actually lands and why user edits that
// reorder hooks.json are a recorded known gap. The algorithm is a provider
// implementation detail, not a published contract; TestCodexTrustedHash pins
// the live-verified vector so a drift is a failing test rather than a silent
// re-prompt.

// Codex integration identity.
const (
	CodexProvider = "codex"
	CodexSource   = "sidecar.codex.hooks"

	// CodexAssetVersion is the bundled entry's version. Bump it whenever the
	// canonical entry changes, and append the superseded entry to
	// codexEntrySpec's canonical history so installed copies read as outdated
	// rather than damaged.
	CodexAssetVersion = "1"

	// CodexAssetSchema is the plan/marker schema the asset declares.
	CodexAssetSchema = 1

	// CodexBackupSuffix names the recoverable copy kept beside each file
	// before any rewrite of a pre-existing file.
	CodexBackupSuffix = ".sidecar-backup"
)

// codexCanonicalEntry is the exact hook entry version 1 ships.
func codexCanonicalEntry() json.RawMessage {
	return marshalJSONObject([]jsonMember{
		{key: "type", val: json.RawMessage(`"command"`)},
		{key: "command", val: mustJSONString(reportSessionCommand(CodexProvider))},
		{key: "timeout", val: json.RawMessage("10")},
	})
}

// codexCanonicalGroup is the group the entry is installed in — no matcher key,
// which is how Codex (unlike Claude) spells "every session".
func codexCanonicalGroup() json.RawMessage {
	return marshalJSONObject([]jsonMember{
		{key: "hooks", val: marshalJSONArray([]json.RawMessage{codexCanonicalEntry()})},
	})
}

func codexEntrySpec() hookEntrySpec {
	return hookEntrySpec{
		matcher: nil,
		canonical: []versionedEntry{
			{version: CodexAssetVersion, entry: codexCanonicalEntry()},
		},
	}
}

// codexTrustedHash computes the trusted_hash Codex records for a command hook.
func codexTrustedHash(command string, timeoutSec int) string {
	identity := fmt.Sprintf(`{"event_name":"session_start","hooks":[{"async":false,"command":%s,"timeout":%d,"type":"command"}]}`,
		serdeJSONString(command), timeoutSec)
	sum := sha256.Sum256([]byte(identity))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// serdeJSONString encodes a string the way serde_json does — no HTML escaping —
// so the canonical identity hashes to the same bytes Codex computes.
func serdeJSONString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// A Go string always encodes.
		panic(err)
	}
	return string(bytes.TrimSpace(b.Bytes()))
}

// codexTrustHashes returns every trusted_hash Sidecar has ever written, newest
// last. A config.toml state table carrying one of these values is Sidecar's,
// which is what lets uninstall find its own stale records without ever
// touching another tool's — the hash of Sidecar's fixed command is as unique
// as the command itself.
func codexTrustHashes() []string {
	return []string{codexTrustedHash(reportSessionCommand(CodexProvider), hookTimeoutSec)}
}

// CodexAdapter installs Sidecar's Codex session-identity hook.
type CodexAdapter struct{}

func (CodexAdapter) Provider() string { return CodexProvider }
func (CodexAdapter) Source() string   { return CodexSource }

// Asset returns the bundled integration identity. As with Claude there is no
// standalone file: Content is the canonical hooks.json Sidecar would create in
// an empty tree, carried so surfaces can show exactly what an install adds.
func (CodexAdapter) Asset() Asset {
	return Asset{
		Name:          "hooks.json",
		Source:        CodexSource,
		SchemaVersion: CodexAssetSchema,
		Version:       CodexAssetVersion,
		Content:       string(renderJSONFile([]jsonMember{{key: "hooks", val: marshalJSONObject([]jsonMember{{key: "SessionStart", val: marshalJSONArray([]json.RawMessage{codexCanonicalGroup()})}})}})),
	}
}

type codexPaths struct {
	Dir          string
	Hooks        string
	Config       string
	HooksBackup  string
	ConfigBackup string
}

// CodexPaths returns the paths the Codex adapter inspects and touches.
func CodexPaths(env Env) []string {
	p := codexPathsFor(env)
	return []string{p.Hooks, p.Config}
}

func codexPathsFor(env Env) codexPaths {
	dir := filepath.Join(env.Home, ".codex")
	return codexPaths{
		Dir:          dir,
		Hooks:        filepath.Join(dir, "hooks.json"),
		Config:       filepath.Join(dir, "config.toml"),
		HooksBackup:  filepath.Join(dir, "hooks.json"+CodexBackupSuffix),
		ConfigBackup: filepath.Join(dir, "config.toml"+CodexBackupSuffix),
	}
}

// codexState is everything one inspection learned.
type codexState struct {
	env          Env
	paths        codexPaths
	spec         hookEntrySpec
	dir          FileState
	hooks        FileState
	config       FileState
	hooksBackup  FileState
	configBackup FileState
	hooksScan    hookTreeScan
	configScan   codexConfigScan

	// wantKey/wantHash are the state key and trusted_hash for the entry where
	// it sits now — meaningful only when exactly one owned entry exists under
	// SessionStart.
	wantKey  string
	wantHash string
	// ownedTables are the config.toml trust tables Sidecar owns.
	ownedTables []tomlStateTable
	// trustConverged reports that config.toml already records exactly the
	// right feature flag and trust for the installed entry.
	trustConverged bool

	providerPath    string
	providerVersion string

	assetStatus agentlifecycle.IntegrationStatus
	status      agentlifecycle.IntegrationStatus
	message     string
	installed   string
}

func (a CodexAdapter) inspect(env Env) codexState {
	p := codexPathsFor(env)
	s := codexState{
		env:          env,
		paths:        p,
		spec:         codexEntrySpec(),
		dir:          inspectDir(env, p.Dir),
		hooks:        inspectFile(env, p.Hooks, a.Asset()),
		config:       inspectFile(env, p.Config, a.Asset()),
		hooksBackup:  FileState{Path: p.HooksBackup, Exists: fileExists(p.HooksBackup)},
		configBackup: FileState{Path: p.ConfigBackup, Exists: fileExists(p.ConfigBackup)},
	}
	if path, ok := env.lookPath(CodexProvider); ok {
		s.providerPath = path
		s.providerVersion = env.providerVersion(CodexProvider)
	}
	_, s.hooksScan = scanEntryFile(s.hooks, s.spec)
	if len(s.hooksScan.owned) > 0 {
		s.hooks.Owned = true
		s.hooks.Version = s.hooksScan.owned[len(s.hooksScan.owned)-1].version
	}

	configRaw, configOK := readEntryFileBytes(s.config)
	if configOK {
		s.configScan = scanCodexConfig(s.config.Exists, configRaw)
	} else {
		s.configScan = codexConfigScan{exists: true, parseErr: "the file exists but could not be read"}
	}

	if s.hooksScan.converged(s.spec) {
		owned := s.hooksScan.owned[0]
		s.wantKey = codexStateKey(p.Hooks, owned.group, owned.hook)
		s.wantHash = codexTrustHashes()[len(codexTrustHashes())-1]
	}
	s.ownedTables = codexOwnedTables(s.configScan, s.wantKey)
	if len(s.ownedTables) > 0 {
		s.config.Owned = true
	}
	s.trustConverged = s.wantKey != "" && s.configScan.parseErr == "" &&
		s.configScan.hooksEnabled() && len(s.ownedTables) == 1 &&
		s.ownedTables[0].key == s.wantKey && s.ownedTables[0].hash == s.wantHash

	s.assetStatus, s.message, s.installed = codexAssetStatus(s)

	s.status = s.assetStatus
	if s.providerPath == "" {
		s.status = agentlifecycle.StatusProviderMissing
		s.message = "the codex CLI was not found on PATH" + orEmpty("; "+s.message, s.message != "")
	}
	return s
}

func codexStateKey(hooksPath string, group, hook int) string {
	return hooksPath + ":session_start:" + strconv.Itoa(group) + ":" + strconv.Itoa(hook)
}

// codexOwnedTables selects the config.toml trust tables that are Sidecar's: a
// table whose hash is one Sidecar computes for its own command, or whose key
// names the position Sidecar's installed entry occupies right now. Everything
// else — herdr's records, hand-written trust — is never touched.
func codexOwnedTables(scan codexConfigScan, wantKey string) []tomlStateTable {
	ours := map[string]bool{}
	for _, h := range codexTrustHashes() {
		ours[h] = true
	}
	var out []tomlStateTable
	for _, t := range scan.state {
		if ours[t.hash] || (wantKey != "" && t.key == wantKey) {
			out = append(out, t)
		}
	}
	return out
}

// readEntryFileBytes reads an inspected file's bytes, honoring the inspection:
// an absent file reads as empty and safe, an unsafe or oversized one as not
// readable.
func readEntryFileBytes(file FileState) ([]byte, bool) {
	if !file.Exists {
		return nil, true
	}
	if file.Unsafe != "" || file.Size > maxAssetBytes {
		return nil, false
	}
	b, err := os.ReadFile(file.Path)
	if err != nil {
		return nil, false
	}
	return b, true
}

// codexAssetStatus decides the status from the inspected files alone.
func codexAssetStatus(s codexState) (agentlifecycle.IntegrationStatus, string, string) {
	st, msg, installed := entryAssetStatus(s.dir, s.hooks, s.hooksScan, s.spec, "hooks.json")
	switch st {
	case agentlifecycle.StatusNotInstalled:
		if s.config.Exists && s.config.Unsafe != "" {
			return agentlifecycle.StatusNeedsRepair, s.config.UnsafeDetail + " (" + s.paths.Config + ")", ""
		}
		if len(s.ownedTables) > 0 {
			return agentlifecycle.StatusNeedsRepair,
				"no hook is installed but Sidecar trust records remain in config.toml; repair reinstalls, uninstall removes them", ""
		}
		if s.configScan.parseErr != "" {
			// Nothing of Sidecar's is here, which is the primary fact; the
			// config.toml problem matters only once an install is attempted,
			// and the install will refuse with the specific reason.
			return st, "config.toml could not be interpreted (" + s.configScan.parseErr + ")", ""
		}
		return st, msg, installed
	case agentlifecycle.StatusCurrent:
		switch {
		case s.config.Exists && s.config.Unsafe != "":
			return agentlifecycle.StatusNeedsRepair, s.config.UnsafeDetail + " (" + s.paths.Config + ")", installed
		case s.configScan.parseErr != "":
			return agentlifecycle.StatusNeedsRepair,
				"config.toml could not be interpreted (" + s.configScan.parseErr + "), so the hook's feature flag and trust cannot be verified", installed
		case !s.configScan.hooksEnabled():
			return agentlifecycle.StatusNeedsRepair,
				"the hook is installed but config.toml does not enable features.hooks, so Codex ignores it entirely; repair enables the flag", installed
		case !s.trustConverged:
			return agentlifecycle.StatusNeedsRepair,
				"the hook is installed but its trust record in config.toml is missing or stale, so Codex will prompt for or refuse it; repair rewrites the record", installed
		}
		return st, msg, installed
	}
	return st, msg, installed
}

// Inspect implements [Adapter].
func (a CodexAdapter) Inspect(env Env) Status {
	return a.statusOf(a.inspect(env))
}

func (a CodexAdapter) statusOf(s codexState) Status {
	capability, _ := agentlifecycle.CapabilityForSource(CodexSource)
	inRange := versionInRange(s.providerVersion, capability.TestedProviderRange)
	tier, reason := capability.TierFor(s.status, inRange)

	st := Status{IntegrationReport: agentlifecycle.IntegrationReport{
		SchemaVersion:         agentlifecycle.SchemaVersion,
		Provider:              CodexProvider,
		Source:                CodexSource,
		Status:                s.status,
		BundledVersion:        CodexAssetVersion,
		InstalledVersion:      s.installed,
		ProviderVersion:       s.providerVersion,
		ProviderInTestedRange: inRange,
		EffectiveTier:         tier,
		TierReason:            reason,
		TargetPaths:           []string{s.paths.Hooks, s.paths.Config},
		KnownGaps:             capability.KnownGaps,
		Message:               s.message,
	}}
	st.ProviderPath = s.providerPath
	st.Files = []FileState{s.dir, s.hooks, s.config, s.hooksBackup, s.configBackup}
	for _, act := range Actions() {
		if _, err := a.plan(s, act); err == nil {
			st.Offered = append(st.Offered, act)
		}
	}
	return st
}

// Plan implements [Adapter].
func (a CodexAdapter) Plan(env Env, act Action) (Plan, error) {
	return a.plan(a.inspect(env), act)
}

func (a CodexAdapter) plan(s codexState, act Action) (Plan, error) {
	p := Plan{
		SchemaVersion: InstallSchemaVersion,
		Provider:      CodexProvider,
		Source:        CodexSource,
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

// planConverge builds the plan that ends with exactly one canonical entry in
// hooks.json, the feature flag on, and one matching trust record — and
// everything Sidecar does not own byte-identical.
func (a CodexAdapter) planConverge(s codexState, p Plan, act Action) (Plan, error) {
	if s.providerPath == "" {
		return Plan{}, refuse(RefuseProviderMissing, "",
			"the codex CLI was not found on PATH, so Sidecar will not modify %s for it; install codex first", s.paths.Dir)
	}
	if err := gateConvergeVerb(s.assetStatus, act, s.paths.Hooks, CodexProvider, s.installed, s.message); err != nil {
		return Plan{}, err
	}
	if err := refuseUnsafeEntryFile(s.dir, s.hooks, s.hooksScan); err != nil {
		return Plan{}, err
	}
	if s.config.Exists && s.config.Unsafe != "" {
		return Plan{}, refuse(s.config.Unsafe, s.paths.Config, "%s: %s", s.paths.Config, s.config.UnsafeDetail)
	}
	if s.configScan.parseErr != "" {
		return Plan{}, refuse(RefuseUnreadable, s.paths.Config,
			"%s could not be interpreted (%s); Sidecar will not edit a file it cannot read — fix or move it yourself and run this again", s.paths.Config, s.configScan.parseErr)
	}

	if s.hooksScan.converged(s.spec) && s.trustConverged {
		p.Unchanged = true
		return p, nil
	}

	// hooks.json first: an interruption between the two writes then leaves an
	// installed hook Codex declines to trust — visible and safe — rather than
	// a trust record for content that does not exist.
	wantKey, wantHash := s.wantKey, s.wantHash
	if !s.hooksScan.converged(s.spec) {
		top, _, err := stripOwnedHookEntries(s.hooksScan)
		if err != nil {
			return Plan{}, refuse(RefuseUnreadable, s.paths.Hooks, "%s: %v", s.paths.Hooks, err)
		}
		group, err := sessionStartGroupCount(top)
		if err != nil {
			return Plan{}, refuse(RefuseUnreadable, s.paths.Hooks, "%s: %v", s.paths.Hooks, err)
		}
		top, err = appendCanonicalGroup(top, codexCanonicalGroup())
		if err != nil {
			return Plan{}, refuse(RefuseUnreadable, s.paths.Hooks, "%s: %v", s.paths.Hooks, err)
		}
		wantKey = codexStateKey(s.paths.Hooks, group, 0)
		wantHash = codexTrustHashes()[len(codexTrustHashes())-1]
		p.Ops = entryFileOps(p.Ops, s.env, s.dir, s.hooks, s.hooksBackup, renderJSONFile(top),
			"write the Sidecar session-identity hook entry, preserving every other hook")
	}

	content, err := codexConfigConverge(s.configScan, wantKey, wantHash)
	if err != nil {
		return Plan{}, refuse(RefuseUnreadable, s.paths.Config, "%s: %v", s.paths.Config, err)
	}
	if content != nil {
		note := "enable features.hooks and record the hook's trusted_hash, preserving every other line"
		if s.configScan.hooksEnabled() {
			note = "record the hook's trusted_hash, preserving every other line"
		}
		// The directory op, if any, was already planned with hooks.json.
		dirPlanned := s.dir
		if len(p.Ops) > 0 {
			dirPlanned = FileState{Path: s.paths.Dir, Exists: true}
		}
		p.Ops = entryFileOps(p.Ops, s.env, dirPlanned, s.config, s.configBackup, content,
			note)
	}

	if len(p.Ops) == 0 {
		p.Unchanged = true
		return p, nil
	}
	p.StatusAfter = agentlifecycle.StatusCurrent
	return p, nil
}

// planUninstall removes Sidecar's hook entry and its trust records, and
// deliberately leaves features.hooks alone: other hooks in the same file may
// depend on it, and disabling a feature the user's other tools rely on is not
// an uninstall's business.
func (a CodexAdapter) planUninstall(s codexState, p Plan) (Plan, error) {
	// Nothing of Sidecar's is visible and both files read cleanly enough to
	// say so: there is nothing to do. An unreadable file that visibly holds
	// something of Sidecar's — or that cannot be ruled out while its sibling
	// does — refuses below instead.
	hooksClean := !s.hooks.Exists || (len(s.hooksScan.owned) == 0 && s.hooksScan.parseErr == "")
	if hooksClean && len(s.ownedTables) == 0 {
		p.Unchanged = true
		return p, nil
	}
	if err := refuseUnsafeEntryFile(s.dir, s.hooks, s.hooksScan); err != nil {
		return Plan{}, err
	}
	if s.config.Exists && s.config.Unsafe != "" {
		return Plan{}, refuse(s.config.Unsafe, s.paths.Config, "%s: %s", s.paths.Config, s.config.UnsafeDetail)
	}
	if s.configScan.parseErr != "" {
		return Plan{}, refuse(RefuseUnreadable, s.paths.Config,
			"%s could not be interpreted (%s); Sidecar will not edit a file it cannot read, and removing the hook while its trust records cannot be checked would leave them behind", s.paths.Config, s.configScan.parseErr)
	}

	if len(s.hooksScan.owned) > 0 {
		top, changed, err := stripOwnedHookEntries(s.hooksScan)
		if err != nil {
			return Plan{}, refuse(RefuseUnreadable, s.paths.Hooks, "%s: %v", s.paths.Hooks, err)
		}
		if changed {
			p.Ops = append(p.Ops, removalOps(s.hooks, s.hooksBackup, top,
				"remove the Sidecar session-identity hook entry, preserving every other hook")...)
		}
	}
	if len(s.ownedTables) > 0 {
		content, err := codexConfigWithoutOwnedTables(s.configScan, s.wantKey)
		if err != nil {
			return Plan{}, refuse(RefuseUnreadable, s.paths.Config, "%s: %v", s.paths.Config, err)
		}
		p.Ops = entryFileOps(p.Ops, s.env, s.dir, s.config, s.configBackup, content,
			"remove Sidecar's hook trust records, preserving every other line including features.hooks")
	}

	if len(p.Ops) == 0 {
		p.Unchanged = true
		return p, nil
	}
	p.StatusAfter = agentlifecycle.StatusNotInstalled
	if s.providerPath == "" {
		p.StatusAfter = agentlifecycle.StatusProviderMissing
	}
	return p, nil
}

// sessionStartGroupCount reports how many groups hooks.SessionStart holds.
func sessionStartGroupCount(top []jsonMember) (int, error) {
	hooksIdx, ok := lastMember(top, "hooks")
	if !ok {
		return 0, nil
	}
	events, err := parseJSONObject(top[hooksIdx].val)
	if err != nil {
		return 0, err
	}
	evIdx, ok := lastMember(events, "SessionStart")
	if !ok {
		return 0, nil
	}
	groups, err := parseJSONArray(events[evIdx].val)
	if err != nil {
		return 0, err
	}
	return len(groups), nil
}

// --- the line-surgical config.toml editor ---

// tomlStateTable is one [hooks.state."<key>"] table: its unquoted key, its
// trusted_hash value, and the exact line span it occupies.
type tomlStateTable struct {
	key   string
	hash  string
	start int
	end   int
}

// codexConfigScan is one reading of config.toml, precise about exactly the two
// regions Sidecar edits and deliberately ignorant of everything else.
type codexConfigScan struct {
	exists bool
	lines  []string
	// features is the [features] header line, -1 when absent.
	features int
	// hooksFlag is the parsed features.hooks value, nil when the key is absent.
	hooksFlag     *bool
	hooksFlagLine int
	state         []tomlStateTable
	// parseErr names why the file cannot be safely interpreted or edited in
	// the regions Sidecar cares about. Refusing on it is the whole safety
	// story of a line-level editor: a spelling this scanner does not
	// understand is a spelling it must not edit around.
	parseErr string
}

func (s codexConfigScan) hooksEnabled() bool {
	return s.hooksFlag != nil && *s.hooksFlag
}

func scanCodexConfig(exists bool, b []byte) codexConfigScan {
	s := codexConfigScan{exists: exists, features: -1, hooksFlagLine: -1}
	if !exists {
		return s
	}
	content := string(b)
	if strings.Contains(content, `"""`) || strings.Contains(content, "'''") {
		// A multi-line string could contain text that looks exactly like a
		// table header, and a line scanner cannot tell the difference. Refuse
		// rather than guess.
		s.parseErr = "the file contains multi-line strings, which this editor does not support"
		return s
	}
	s.lines = strings.Split(content, "\n")
	table := ""
	open := -1
	closeOpen := func(end int) {
		if open >= 0 {
			s.state[open].end = end
			open = -1
		}
	}
	for i, rawLine := range s.lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			closeOpen(i)
			name, array, ok := tomlHeaderName(line)
			if !ok {
				s.parseErr = fmt.Sprintf("line %d is a table header this editor cannot parse", i+1)
				return s
			}
			if array {
				if name == "features" || name == "hooks" || strings.HasPrefix(name, "hooks.") {
					s.parseErr = "features or hooks are configured as arrays of tables, which this editor does not edit"
					return s
				}
				table = "\x00array:" + name
				continue
			}
			table = name
			switch {
			case name == "features":
				s.features = i
			case strings.HasPrefix(name, "hooks.state."):
				key, ok := tomlQuotedKey(strings.TrimPrefix(name, "hooks.state."))
				if !ok {
					s.parseErr = fmt.Sprintf("line %d names a hooks.state table in a form this editor cannot parse", i+1)
					return s
				}
				s.state = append(s.state, tomlStateTable{key: key, start: i, end: len(s.lines)})
				open = len(s.state) - 1
			}
			continue
		}
		key, val, cut := strings.Cut(line, "=")
		if !cut {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case table == "features" && key == "hooks":
			switch tomlBareValue(val) {
			case "true":
				v := true
				s.hooksFlag, s.hooksFlagLine = &v, i
			case "false":
				v := false
				s.hooksFlag, s.hooksFlagLine = &v, i
			default:
				s.parseErr = "features.hooks has a value this editor does not understand"
				return s
			}
		case table == "" && (key == "features" || strings.HasPrefix(key, "features.")):
			s.parseErr = "the hooks feature flag is configured in a form this editor does not edit"
			return s
		case table == "" && (key == "hooks" || strings.HasPrefix(key, "hooks.")):
			s.parseErr = "hook trust state is configured in a form this editor does not edit"
			return s
		case table == "hooks" && (key == "state" || strings.HasPrefix(key, "state.")):
			s.parseErr = "hook trust state is configured in a form this editor does not edit"
			return s
		case table == "hooks.state":
			s.parseErr = "hook trust state is configured in a form this editor does not edit"
			return s
		case open >= 0 && key == "trusted_hash":
			s.state[open].hash = tomlQuotedValue(val)
		}
	}
	closeOpen(len(s.lines))
	return s
}

// tomlHeaderName parses a `[name]` or `[[name]]` line, tolerating a trailing
// comment.
func tomlHeaderName(line string) (name string, array bool, ok bool) {
	array = strings.HasPrefix(line, "[[")
	body := strings.TrimPrefix(line, "[")
	if array {
		body = strings.TrimPrefix(body, "[")
	}
	closing := strings.LastIndex(body, "]")
	if closing < 0 {
		return "", false, false
	}
	rest := body[closing+1:]
	inner := body[:closing]
	if array {
		if !strings.HasSuffix(inner, "]") {
			return "", false, false
		}
		inner = strings.TrimSuffix(inner, "]")
	}
	rest = strings.TrimSpace(rest)
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", false, false
	}
	inner = strings.TrimSpace(inner)
	if inner == "" || strings.ContainsAny(inner, "[]") {
		return "", false, false
	}
	return inner, array, true
}

// tomlQuotedKey unquotes a basic or literal TOML string key with no escapes.
func tomlQuotedKey(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	quote := s[0]
	if quote != '"' && quote != '\'' {
		return "", false
	}
	if s[len(s)-1] != quote {
		return "", false
	}
	inner := s[1 : len(s)-1]
	if strings.ContainsRune(inner, rune(quote)) || strings.ContainsRune(inner, '\\') {
		return "", false
	}
	return inner, true
}

// tomlBareValue strips a trailing comment from an unquoted value.
func tomlBareValue(val string) string {
	if i := strings.Index(val, "#"); i >= 0 {
		val = val[:i]
	}
	return strings.TrimSpace(val)
}

// tomlQuotedValue extracts a basic-string value, tolerating a trailing
// comment. A value with escapes or an unexpected shape reads as "", which is
// owned by nobody and therefore never touched.
func tomlQuotedValue(val string) string {
	if len(val) < 2 || val[0] != '"' {
		return ""
	}
	closing := strings.Index(val[1:], `"`)
	if closing < 0 {
		return ""
	}
	inner := val[1 : 1+closing]
	rest := strings.TrimSpace(val[2+closing:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return ""
	}
	if strings.ContainsRune(inner, '\\') {
		return ""
	}
	return inner
}

// codexConfigConverge composes the config.toml content that enables the
// feature flag and records exactly one trust table for wantKey/wantHash,
// dropping Sidecar's stale trust tables and changing nothing else. It returns
// nil when the file already says all of that.
func codexConfigConverge(scan codexConfigScan, wantKey, wantHash string) ([]byte, error) {
	if scan.parseErr != "" {
		return nil, fmt.Errorf("%s", scan.parseErr)
	}
	converged := scan.hooksEnabled()
	owned := codexOwnedTables(scan, wantKey)
	if converged && len(owned) == 1 && owned[0].key == wantKey && owned[0].hash == wantHash {
		return nil, nil
	}

	if !scan.exists || len(scan.lines) == 0 {
		content := "[features]\nhooks = true\n\n" + codexStateBlock(wantKey, wantHash)
		return codexVerify([]byte(content), wantKey, wantHash)
	}

	drop := lineDropSet(scan, owned)
	var kept []string
	for i, line := range scan.lines {
		if drop[i] {
			continue
		}
		if i == scan.hooksFlagLine && !scan.hooksEnabled() {
			kept = append(kept, "hooks = true")
			continue
		}
		if i == scan.features && scan.hooksFlag == nil {
			kept = append(kept, line, "hooks = true")
			continue
		}
		kept = append(kept, line)
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	if scan.features == -1 && scan.hooksFlag == nil {
		kept = append(kept, "", "[features]", "hooks = true")
	}
	kept = append(kept, "", strings.TrimSuffix(codexStateBlock(wantKey, wantHash), "\n"))
	return codexVerify([]byte(strings.Join(kept, "\n")+"\n"), wantKey, wantHash)
}

// codexConfigWithoutOwnedTables composes the uninstall content: Sidecar's
// trust tables gone, every other line — including features.hooks — untouched.
func codexConfigWithoutOwnedTables(scan codexConfigScan, wantKey string) ([]byte, error) {
	if scan.parseErr != "" {
		return nil, fmt.Errorf("%s", scan.parseErr)
	}
	owned := codexOwnedTables(scan, wantKey)
	if len(owned) == 0 {
		return nil, nil
	}
	drop := lineDropSet(scan, owned)
	var kept []string
	for i, line := range scan.lines {
		if drop[i] {
			continue
		}
		kept = append(kept, line)
	}
	content := strings.Join(kept, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	rescanned := scanCodexConfig(true, []byte(content))
	if rescanned.parseErr != "" || len(codexOwnedTables(rescanned, wantKey)) != 0 {
		return nil, fmt.Errorf("the composed file did not verify; refusing to write it")
	}
	return []byte(content), nil
}

// lineDropSet marks the line spans of the given tables, plus one immediately
// preceding blank line each, for removal.
func lineDropSet(scan codexConfigScan, tables []tomlStateTable) map[int]bool {
	drop := map[int]bool{}
	for _, t := range tables {
		for i := t.start; i < t.end && i < len(scan.lines); i++ {
			drop[i] = true
		}
		if t.start > 0 && strings.TrimSpace(scan.lines[t.start-1]) == "" {
			drop[t.start-1] = true
		}
	}
	return drop
}

func codexStateBlock(key, hash string) string {
	return "[hooks.state." + `"` + key + `"` + "]\ntrusted_hash = \"" + hash + "\"\n"
}

// codexVerify re-scans composed content and proves it says exactly what the
// plan intends before the bytes are allowed anywhere near disk. A line editor
// earns trust by checking its own work, not by being clever.
func codexVerify(content []byte, wantKey, wantHash string) ([]byte, error) {
	scan := scanCodexConfig(true, content)
	if scan.parseErr != "" {
		return nil, fmt.Errorf("the composed file did not verify (%s); refusing to write it", scan.parseErr)
	}
	if !scan.hooksEnabled() {
		return nil, fmt.Errorf("the composed file did not enable features.hooks; refusing to write it")
	}
	owned := codexOwnedTables(scan, wantKey)
	if len(owned) != 1 || owned[0].key != wantKey || owned[0].hash != wantHash {
		return nil, fmt.Errorf("the composed file did not record exactly one trust table; refusing to write it")
	}
	return content, nil
}

var _ Adapter = CodexAdapter{}
