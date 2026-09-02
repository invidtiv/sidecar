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
	// Agents is keyed by the vendored file's base name, the same key Load and
	// OverridePath use, never by Herdr's agent label.
	Agents map[string]AgentFetchStatus `json:"agents,omitempty"`
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
// be an error in its own right.
func LoadFetchStatus() FetchStatus {
	empty := FetchStatus{SchemaVersion: FetchStatusSchemaVersion}
	path := FetchStatusPath()
	if path == "" || config.AssertIsolatedPath(path) != nil {
		return empty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var status FetchStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return empty
	}
	if status.SchemaVersion != FetchStatusSchemaVersion {
		// A document from a future Sidecar. Reading it as empty means the next
		// fetch rewrites it, which is the same thing every other unrecognised
		// state document does here.
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

// readCachedRemote loads the fetched manifest for an agent, if one is cached.
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
func readCachedRemote(agent string, vendored *manifest.Manifest) (*manifest.Manifest, string, string) {
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
	if !overrideNamesAgent(remote, agent, vendored) {
		return nil, path, fmt.Sprintf("ignored cached manifest %s because manifest id %q does not match %q",
			path, remote.ID, agent)
	}
	return remote, path, ""
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
	remote, path, diagnostic := readCachedRemote(agent, vendored)
	summary := RemoteCacheSummary{Path: path, Diagnostic: diagnostic}
	if remote != nil {
		summary.Version = remote.Version
	}
	return summary
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
// It is safe to call with agents that were never loaded.
func Invalidate(agents ...string) {
	if len(agents) == 0 {
		return
	}
	loadedMu.Lock()
	defer loadedMu.Unlock()
	for _, agent := range agents {
		delete(loadedBy, agent)
		delete(loadedAt, agent)
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
