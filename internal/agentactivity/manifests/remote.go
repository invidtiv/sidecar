package manifests

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/config"
)

// The runtime fetch cache: <state dir>/agent-detection/remote/<file>.toml, with
// <state dir>/agent-detection/status.json beside it recording the last check.
//
// This is Herdr's layout (state_root, remote_manifest_path and status_path in
// manifest_update.rs at e2b85c7) with one deliberate difference: the status file
// is JSON rather than TOML, because every other file Sidecar writes under its
// state directory is JSON and a lone TOML file there would be the odd one out.
// The manifests themselves are cached as the bytes that were served, TOML and
// all, because they are upstream's file and must stay byte-identical to it.
//
// Nothing here is on the startup path. The cache is read inside the same
// sync.Once that compiles the vendored manifest (load.go), and it is written
// only by Fetch, which runs in a tea.Cmd after the first frame.

// RemoteDirName is the directory under the state directory that holds the
// runtime fetch cache and its status file. It is Herdr's spelling, the same one
// OverrideDirName uses on the config axis, because the two directories hold the
// same kind of file.
const RemoteDirName = "agent-detection"

// remoteReads counts how many times the cache has been consulted, so a test can
// prove the read happens at first use of an agent's manifest and never on the
// startup path. It is the same instrument overrideReads is, for the same rule.
var remoteReads atomic.Int64

// FetchStatusSchemaVersion is the version of the status.json document.
const FetchStatusSchemaVersion = 1

// The values a fetch records for the run as a whole and for one agent. They are
// Herdr's strings (manifest_update.rs) plus "ignored", which Herdr has no need
// for because it refuses a file rather than keeping a note about it.
const (
	// FetchResultChecking is written before the network work starts, and is
	// what a second process sees while a first one is still fetching.
	FetchResultChecking = "checking"
	// FetchResultChecked means the catalog was read and every agent has its own
	// result below.
	FetchResultChecked = "checked"
	// FetchResultUpdated means a newer manifest was cached for this agent.
	FetchResultUpdated = "updated"
	// FetchResultCurrent means the published manifest matched what is cached.
	FetchResultCurrent = "current"
	// FetchResultFailed means the fetch or the validation failed.
	FetchResultFailed = "failed"
	// FetchResultIgnored means the file was served and read but must not be
	// used: it needs a newer engine, or it names another agent.
	FetchResultIgnored = "ignored"
)

// FetchStatus is what the last runtime catalog fetch left behind. It is the
// diagnostic half of this feature: a fetch never fails visibly, so this file is
// the only place a user with fetch on can find out that it has not worked for a
// week. `sidecar agent manifests` prints it.
type FetchStatus struct {
	SchemaVersion int `json:"schemaVersion"`
	// CatalogURL is the index the last check used, so a user who changed the
	// setting can see which catalog the cache came from.
	CatalogURL string `json:"catalogUrl,omitempty"`
	// LastCheckUnix is when the last check *started*, not when it finished. It
	// is written before the network work, which is what makes it a claim: see
	// Fetch.
	LastCheckUnix int64  `json:"lastCheckUnix,omitempty"`
	LastResult    string `json:"lastResult,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	// SkippedRows names catalog rows the last check could not use and passed
	// over: an id this binary has no vendored manifest for, or a path it will
	// not resolve. They are recorded rather than dropped because a row skipped
	// silently is a manifest that quietly stops updating.
	SkippedRows []string `json:"skippedRows,omitempty"`
	// Agents is keyed by the vendored file's base name, the same key Load and
	// OverridePath use, never by Herdr's agent label.
	Agents map[string]AgentFetchStatus `json:"agents,omitempty"`

	// ReadError says why the status file on disk could not be used, when a file
	// was there and could not be read or parsed. It describes *this read*, not
	// the document, so it is never serialised: a caller writing the status back
	// would otherwise persist a note about a file that no longer exists.
	//
	// It exists because this file is where the whole feature's observability
	// lives. A status file that is unreadable used to read as "never checked",
	// which is the one answer that makes a broken fetch look like a fetch that
	// was never configured.
	ReadError string `json:"-"`
}

// AgentFetchStatus is one agent's result from the last check.
type AgentFetchStatus struct {
	// CachedVersion is the version now in the cache, which is not necessarily
	// the version that was served: a served file that was older, invalid, or
	// too new for this engine leaves the previous cached copy in place.
	CachedVersion string `json:"cachedVersion,omitempty"`
	// ServedVersion is the version the catalog served, when it could be read.
	ServedVersion   string `json:"servedVersion,omitempty"`
	LastResult      string `json:"lastResult"`
	LastError       string `json:"lastError,omitempty"`
	LastCheckedUnix int64  `json:"lastCheckedUnix,omitempty"`
}

// RemoteDir returns the directory the runtime fetch cache lives in, or "" when
// Sidecar cannot resolve its state directory at all.
//
// It derives from config.StateDir, which is what makes XDG_STATE_HOME and a
// test's SetTestStateDir move the cache with everything else on the state axis.
// Note that this is the state axis, not the config axis OverrideDir uses: an
// override is a file the user wrote and belongs with their configuration, and a
// cache is data Sidecar can throw away and re-fetch.
func RemoteDir() string {
	dir := config.StateDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, RemoteDirName)
}

// RemoteCachePath returns the file a fetched manifest for an agent is cached
// at. The base name is the vendored manifest's file name, exactly as
// OverridePath's is, so `github-copilot.toml` is one spelling everywhere.
func RemoteCachePath(agent string) string {
	dir := RemoteDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "remote", agent+".toml")
}

// FetchStatusPath returns the status file's path.
func FetchStatusPath() string {
	dir := RemoteDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "status.json")
}

// LoadFetchStatus reads the status file. A missing or unreadable file reads as
// an empty status, because the status is a diagnostic and losing it must never
// be an error in its own right -- a fetch whose claim cannot be read runs, and
// rewrites the file.
//
// A file that was there and could not be used sets ReadError, so a reader can
// tell "nothing has ever checked" apart from "the record of what checked is
// unreadable". Those are opposite pieces of news and this file is the only
// place either of them is written down.
func LoadFetchStatus() FetchStatus {
	empty := FetchStatus{SchemaVersion: FetchStatusSchemaVersion}
	path := FetchStatusPath()
	if path == "" || config.AssertIsolatedPath(path) != nil {
		return empty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			empty.ReadError = fmt.Sprintf("status file %s could not be read: %v", path, err)
		}
		return empty
	}
	var status FetchStatus
	if err := json.Unmarshal(data, &status); err != nil {
		empty.ReadError = fmt.Sprintf("status file %s could not be parsed: %v", path, err)
		return empty
	}
	if status.SchemaVersion != FetchStatusSchemaVersion {
		// A document from a future Sidecar. Reading it as empty means the next
		// fetch rewrites it, which is the same thing every other unrecognised
		// state document does here.
		empty.ReadError = fmt.Sprintf("status file %s has schema version %d, not %d",
			path, status.SchemaVersion, FetchStatusSchemaVersion)
		return empty
	}
	if status.Agents == nil {
		status.Agents = map[string]AgentFetchStatus{}
	}
	return status
}

// saveFetchStatus writes the status file atomically.
func saveFetchStatus(status FetchStatus) error {
	path := FetchStatusPath()
	if path == "" {
		return fmt.Errorf("no state directory")
	}
	if err := config.AssertIsolatedPath(path); err != nil {
		return err
	}
	status.SchemaVersion = FetchStatusSchemaVersion
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'))
}

// atomicWriteFile is Herdr's atomic_write (manifest_update.rs:440): write a
// uniquely named temporary beside the target, fsync it, then rename over the
// target. The rename is what makes a torn file impossible, which matters here
// because a reader is a *different process* -- the running Sidecar whose
// sync.Once has not fired yet -- rather than a later call in this one.
//
// The parent directory is not fsynced. Herdr does and logs a warning when it
// cannot; the guarantee that buys is that the rename survives a machine crash,
// and the worst case here is a cache file that has to be fetched again.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+"."+strconv.Itoa(os.Getpid())+"."+
		strconv.FormatInt(time.Now().UnixNano(), 36)+".tmp")
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// remoteCacheEnabled reports whether the running configuration allows the fetch
// cache to be *used*, which is the same setting that allows it to be filled.
//
// "Off" means off. The setting used to gate fetching alone, so a cache left
// behind by a week when it was on kept answering after the user turned it off:
// `sidecar agent manifests` reported `remote`, `explain` reported
// `active_source: remote`, and there was no way back to the vendored tree short
// of deleting a state directory by hand. Herdr's loader reads its cache
// unconditionally too, and this is a deliberate divergence: a user turning a
// network feature off expects the software to stop using what the network gave
// it, and the cache is still on disk for `--refresh` to pick up again.
//
// The config read is memoised, because it happens inside the same sync.Once
// that compiles a manifest and would otherwise be paid once per agent. Nothing
// here is on the startup path, for the same reason the override read is not.
//
// The memo is keyed on the config path rather than held for the life of the
// process, so a test that points the config axis somewhere else gets its own
// answer instead of whichever test ran first. One process only ever resolves one
// path, so this is still one config read in production.
var (
	remoteEnabledMu   sync.Mutex
	remoteEnabledPath string
	remoteEnabledOK   bool
	remoteEnabledVal  bool
)

func remoteCacheEnabled() bool {
	path := config.ConfigPath()
	remoteEnabledMu.Lock()
	defer remoteEnabledMu.Unlock()
	if remoteEnabledOK && remoteEnabledPath == path {
		return remoteEnabledVal
	}
	remoteEnabledPath, remoteEnabledOK, remoteEnabledVal = path, true, false
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		// A config nobody can load resolves to the default, which is off.
		// Reading an unreadable config as "on" would turn a broken file into a
		// network feature nobody asked for.
		return false
	}
	remoteEnabledVal = cfg.Detection.RemoteManifestsEnabled()
	return remoteEnabledVal
}

// resetRemoteCacheEnabled drops the memoised setting, for a test that rewrites
// the config at the same path between loads. It is the same shape resetLoadCache
// has.
func resetRemoteCacheEnabled() {
	remoteEnabledMu.Lock()
	defer remoteEnabledMu.Unlock()
	remoteEnabledPath, remoteEnabledOK, remoteEnabledVal = "", false, false
}

// readCachedRemote loads the fetched manifest for an agent for the *loader*:
// nothing at all unless detection.remoteManifests names a catalog.
//
// CachedRemote is the ungated form, and the split is the point. A cache that is
// on disk and not in use is something `sidecar agent manifests` has to be able
// to report; it is not something the engine may evaluate.
func readCachedRemote(agent string, vendored *manifest.Manifest) (*manifest.Manifest, string, string) {
	if !remoteCacheEnabled() {
		return nil, "", ""
	}
	return readCachedRemoteFile(agent, vendored)
}

// readCachedRemoteFile loads the fetched manifest for an agent, if one is
// cached, whatever the setting says.
//
// It returns the parsed manifest, the path it came from, and a diagnostic, with
// exactly the contract readOverride has: the manifest is nil whenever the cache
// is not usable, and the diagnostic is non-empty only when a file was actually
// found. A cache that cannot be read, parsed, validated, or that names another
// agent is ignored and the vendored manifest is used, because a corrupt cache
// must never take a working vendored file down with it.
//
// The file is validated with ParseRemoteWith rather than ParseAndValidateWith,
// which is Herdr's own distinction: a manifest that arrived from outside the
// binary has to declare `version` and `min_engine_version`, and one requiring a
// newer engine is refused (parse_remote_manifest_for_agent, manifest.rs:897).
// ValidateWith alone would accept a file with no version at all, which the
// newer-of-cached-and-vendored comparison then has nothing to compare.
//
// AllowIncompatibleRegex is set for the same reason it is set on the vendored
// files: upstream legitimately ships four `\p{Alphabetic}` patterns RE2 cannot
// compile, and refusing a whole published file over one of them would make the
// fetch strictly worse than not fetching. The overlay carries the rewrite and
// is merged on top of whichever upstream file wins, so those rules stay live.
// What the merged file still cannot compile is named by the loader, on the same
// terms an override's dead rules are named.
func readCachedRemoteFile(agent string, vendored *manifest.Manifest) (*manifest.Manifest, string, string) {
	path := RemoteCachePath(agent)
	if path == "" {
		return nil, "", ""
	}
	// A test binary asserts isolated state by default, so this is what keeps
	// `go test ./...` from reading the developer's real fetch cache. Silent,
	// like the override's equivalent: from the engine's point of view there is
	// no cache.
	if config.AssertIsolatedPath(path) != nil {
		return nil, "", ""
	}

	remoteReads.Add(1)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, "", ""
	case err != nil:
		return nil, path, fmt.Sprintf("ignored cached manifest %s because it could not be loaded: %v", path, err)
	}

	remote, err := manifest.ParseRemoteWith(data, manifest.ValidateOptions{AllowIncompatibleRegex: true})
	if err != nil {
		return nil, path, fmt.Sprintf("ignored cached manifest %s because it is invalid: %v", path, err)
	}
	if !remoteNamesAgent(remote, agent, vendored) {
		return nil, path, fmt.Sprintf("ignored cached manifest %s because manifest id %q does not match %q",
			path, remote.ID, agent)
	}
	return remote, path, ""
}

// remoteNamesAgent is the id check for a *fetched* file, and it is deliberately
// stricter than overrideNamesAgent: the declared id must be the agent's own key
// or the id the vendored file declares, and an alias does not count.
//
// An override is a file the user wrote, so accepting an alias there is a
// convenience. A cached file came from a catalog, and an alias match would let a
// catalog serve `id = "evil", aliases = ["cursor"]` at cursor.toml and have it
// become cursor's manifest, which is the one thing the id check exists to stop.
// Herdr keys strictly on the agent label for the same reason
// (process_agent_manifest, manifest_update.rs:315). The fetch path applies the
// same rule against the catalog's declared id before anything is written; this
// is the read-side half, for a cache written by an older binary or edited by
// hand.
func remoteNamesAgent(remote *manifest.Manifest, agent string, vendored *manifest.Manifest) bool {
	if remote.ID == "" {
		return false
	}
	if remote.ID == agent {
		return true
	}
	return vendored != nil && vendored.ID != "" && remote.ID == vendored.ID
}

// RemoteCacheSummary is what one cached file says about itself, for the
// `sidecar agent manifests` table. It reports a refused cache too, because "you
// have a cached file and it is not the one running" is the question the table
// exists to answer.
type RemoteCacheSummary struct {
	Path       string
	Version    string
	Diagnostic string
}

// CachedRemote reports the fetch cache's state for an agent without loading or
// compiling anything else. It returns a zero summary when nothing is cached.
//
// It reads the file whatever detection.remoteManifests says, which is what makes
// "you have a cached file and the setting is off, so it is not the one running"
// something `sidecar agent manifests` can print. Only the loader is gated.
func CachedRemote(agent string) RemoteCacheSummary {
	upstreamBytes, err := UpstreamBytes(agent + ".toml")
	if err != nil {
		return RemoteCacheSummary{}
	}
	vendored, err := manifest.ParseAndValidateWith(upstreamBytes,
		manifest.ValidateOptions{AllowIncompatibleRegex: true})
	if err != nil {
		return RemoteCacheSummary{}
	}
	remote, path, diagnostic := readCachedRemoteFile(agent, vendored)
	summary := RemoteCacheSummary{Path: path, Diagnostic: diagnostic}
	if remote != nil {
		summary.Version = remote.Version
	}
	return summary
}

// ClearCache deletes every cached manifest and the status file, and returns the
// paths it removed, in order.
//
// It is the "way out" the setting on its own does not give anyone. Turning
// fetching off stops the cache being used, but the bytes stay on disk, and a
// user who changed catalogs, or who wants the next check to start from nothing,
// otherwise has to find a state directory and delete files by hand. That is not
// a step a first-class agent user can take from a script, so it is a verb.
//
// Removing nothing is success: the caller asked for an empty cache and there is
// one. The compiled manifests for every agent are invalidated afterwards by the
// caller, not here, because Invalidate is per agent and this is a whole-tree
// operation.
func ClearCache() ([]string, error) {
	dir := RemoteDir()
	if dir == "" {
		return nil, fmt.Errorf("no state directory")
	}
	if err := config.AssertIsolatedPath(dir); err != nil {
		return nil, err
	}
	var removed []string
	remoteDir := filepath.Join(dir, "remote")
	entries, err := os.ReadDir(remoteDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		path := filepath.Join(remoteDir, entry.Name())
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, err
		}
		removed = append(removed, path)
	}
	status := FetchStatusPath()
	if err := os.Remove(status); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return removed, err
		}
	} else {
		removed = append(removed, status)
	}
	return removed, nil
}

// Invalidate drops the memoised load for the named agents, so the next Load
// re-reads the cache and the override from disk.
//
// It is Herdr's reload_manifests_for_agents (manifest_update.rs:175), and it
// exists for the same reason: a fetch that cached a newer manifest would
// otherwise do nothing at all until the process restarted, and a user who
// turned the setting on would see no change and no explanation. Only the agents
// whose cache actually moved are invalidated, so a check that found everything
// current costs no recompilation.
//
// It is safe to call with agents that were never loaded, and it is safe to call
// while another goroutine is inside Load for the same agent: each Load holds the
// entry it started with and writes its answer into that entry, so an
// invalidation mid-load leaves a result nobody can reach rather than overwriting
// the next load's fresher one. See the entry type in load.go.
func Invalidate(agents ...string) {
	if len(agents) == 0 {
		return
	}
	loadedMu.Lock()
	defer loadedMu.Unlock()
	for _, agent := range agents {
		delete(loadedBy, agent)
	}
}

// agentForCatalogPath maps a catalog entry's path onto the key Sidecar loads
// manifests by: the vendored file's base name, with the .toml stripped.
//
// The catalog's `id` is Herdr's agent *label*, which differs from the file name
// for two agents (`agy`/antigravity.toml and `copilot`/github-copilot.toml).
// Keying on the path rather than the id is what keeps one spelling across the
// vendored tree, the overlay, the override directory and this cache. An entry
// whose path is not a plain `<name>.toml` is refused rather than guessed at.
func agentForCatalogPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("catalog entry has an empty path")
	}
	// Herdr's own safety check (parse_catalog, manifest_update.rs:370): a path
	// that escapes the catalog's directory, or names another host entirely, is
	// a catalog trying to make the client fetch or write somewhere else.
	if strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("catalog entry has an unsafe path %q", path)
	}
	for _, part := range strings.Split(trimmed, "/") {
		if part == ".." {
			return "", fmt.Errorf("catalog entry has an unsafe path %q", path)
		}
	}
	if strings.ContainsAny(trimmed, `/\`) {
		return "", fmt.Errorf("catalog entry path %q is not a plain file name", path)
	}
	name, ok := strings.CutSuffix(trimmed, ".toml")
	if !ok || name == "" {
		return "", fmt.Errorf("catalog entry path %q is not a .toml file", path)
	}
	return name, nil
}
