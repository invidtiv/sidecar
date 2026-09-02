package manifests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/config"
)

// The opt-in runtime catalog fetch, Phase 5 of
// docs/plans/active/herdr-detection-parity.md.
//
// It is off unless `detection.remoteManifests` says otherwise, and when it is
// off nothing in this file runs: no HTTP client is constructed, no state
// directory is touched, no goroutine is started. HTTPClientsBuilt is the
// instrument that makes that checkable rather than merely stated.
//
// When it is on, the rules are Herdr's, ported from manifest_update.rs at
// e2b85c7 and confirmed against it line by line: a schema_version = 1 catalog
// index, a 256 KiB cap per file, validation before use, a file needing a newer
// engine ignored rather than fatal, a downgrade refused, and a same-version file
// whose bytes changed refused. What Sidecar adds is that the fetch runs at most
// once a day and the vendored tree is always there underneath, so a catalog that
// is down, slow, or lying costs nothing but a line in the status file.

// MaxFetchBytes caps every file the catalog serves, index included. It is
// Herdr's MAX_FETCH_BYTES (manifest_update.rs:19).
const MaxFetchBytes = 256 * 1024

// FetchInterval is the shortest gap between two checks. The plan's rule is
// "never more than once per day"; this is that day.
const FetchInterval = 24 * time.Hour

// fetchTimeout bounds the whole check, index and every manifest, so a catalog
// that accepts a connection and then stops talking cannot leave a goroutine
// parked for the life of the process. Herdr bounds each curl invocation
// instead (--connect-timeout 5 --max-time 15); one budget for the run is the
// same protection with one fewer number to keep in step.
const fetchTimeout = 60 * time.Second

// CatalogSchemaVersion is the only index schema this client understands, as
// Herdr's parse_catalog does.
const CatalogSchemaVersion = 1

// httpClientsBuilt counts every HTTP client this package has constructed.
//
// It exists so a test can prove the "off" setting reaches the network in no
// sense at all, rather than proving only that a request never arrived at some
// particular server. A client that is built and then not used is still a
// decision that was made on the wrong side of the setting, and this is what
// catches it.
var httpClientsBuilt atomic.Int64

// HTTPClientsBuilt reports how many HTTP clients this package has constructed
// in this process.
func HTTPClientsBuilt() int64 { return httpClientsBuilt.Load() }

// FetchOptions tunes one check. The zero value fetches Herdr's own catalog with
// a fresh client at the current time, which is what the app passes.
type FetchOptions struct {
	// CatalogURL is the index to read. Required: the caller resolves it from
	// the setting, so this package never decides on its own that fetching is
	// allowed.
	CatalogURL string
	// Client overrides the HTTP client, for tests. When nil a client is built,
	// which is the event HTTPClientsBuilt counts.
	Client *http.Client
	// Now is the clock. Zero means time.Now().
	Now time.Time
	// Force skips the once-a-day gate. Nothing in the app sets it; it exists
	// for a test and for a future explicit "check now" verb.
	Force bool
}

// FetchResult is what one check did.
type FetchResult struct {
	// Skipped reports that no network work happened, with Reason saying why.
	Skipped bool
	Reason  string
	// Updated names the agents whose cache moved, keyed the way Load keys
	// agents: the vendored file's base name.
	Updated []string
	// Status is the status file as it now stands on disk.
	Status FetchStatus
}

// FetchFromConfig runs a check when the setting enables one.
//
// This is the only entry point the app uses, and it is deliberately the place
// the setting is read: a caller cannot fetch by accident, and the "off" path
// returns before anything is built, opened, or dialled.
func FetchFromConfig(ctx context.Context, detection config.DetectionConfig, opts FetchOptions) (FetchResult, error) {
	url, err := detection.RemoteCatalogURL()
	if err != nil {
		// A value the config loader already refused and replaced with the
		// default. Reaching here means a caller built a DetectionConfig by
		// hand, so it is reported rather than silently treated as off.
		return FetchResult{Skipped: true, Reason: err.Error()}, err
	}
	if url == "" {
		return FetchResult{Skipped: true, Reason: "detection.remoteManifests is off"}, nil
	}
	opts.CatalogURL = url
	return Fetch(ctx, opts)
}

// Fetch reads the catalog index and every manifest it lists, caching the ones
// that are newer than what is cached already.
//
// It never returns a user-visible failure: a caller that ignores the error gets
// the same behaviour a caller that reads it does, because everything a check
// learns is written to the status file for `sidecar agent manifests` and
// `sidecar agent explain` to report. The error return is for tests and for the
// log line.
func Fetch(ctx context.Context, opts FetchOptions) (FetchResult, error) {
	if strings.TrimSpace(opts.CatalogURL) == "" {
		return FetchResult{Skipped: true, Reason: "no catalog URL"}, fmt.Errorf("no catalog URL")
	}
	statusPath := FetchStatusPath()
	if statusPath == "" {
		return FetchResult{Skipped: true, Reason: "no state directory"}, fmt.Errorf("no state directory")
	}
	if err := config.AssertIsolatedPath(statusPath); err != nil {
		// A test binary, or a proof run that asserted isolation and then
		// resolved back into the real tree. Either way this process must not
		// write there, and a check is not important enough to be the thing that
		// breaks the rule.
		return FetchResult{Skipped: true, Reason: err.Error()}, nil
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	status := LoadFetchStatus()
	if status.Agents == nil {
		status.Agents = map[string]AgentFetchStatus{}
	}
	if !opts.Force && status.LastCheckUnix > 0 {
		last := time.Unix(status.LastCheckUnix, 0)
		if elapsed := now.Sub(last); elapsed >= 0 && elapsed < FetchInterval {
			return FetchResult{
				Skipped: true,
				Reason:  fmt.Sprintf("checked %s ago, less than the %s interval", elapsed.Round(time.Minute), FetchInterval),
				Status:  status,
			}, nil
		}
	}

	// The claim. LastCheckUnix is written before any network work, not after
	// it, which is the difference between this and Herdr's version and it is
	// what answers "what happens when two Sidecar processes run at once": the
	// second one to start sees a timestamp minutes old and skips, rather than
	// both fetching because neither had finished. Two processes that start
	// within the same instant do both fetch, and that is harmless -- every
	// write is a rename of bytes both of them fetched from the same catalog.
	//
	// A process killed mid-check leaves the claim standing, so the next attempt
	// is a day later. That is the safe direction: the vendored tree is
	// underneath, and a crash loop must not turn into a request loop.
	status.CatalogURL = opts.CatalogURL
	status.LastCheckUnix = now.Unix()
	status.LastResult = FetchResultChecking
	status.LastError = ""
	if err := saveFetchStatus(status); err != nil {
		return FetchResult{Skipped: true, Reason: err.Error()}, err
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	client := opts.Client
	if client == nil {
		client = newHTTPClient()
	}

	result, err := runCheck(ctx, client, opts.CatalogURL, now, &status)
	if err != nil {
		status.LastResult = FetchResultFailed
		status.LastError = err.Error()
	} else {
		status.LastResult = FetchResultChecked
		status.LastError = ""
	}
	if saveErr := saveFetchStatus(status); saveErr != nil && err == nil {
		err = saveErr
	}
	result.Status = status
	// Only the agents whose cache moved are dropped from the load cache, so a
	// check that found everything current recompiles nothing.
	Invalidate(result.Updated...)
	return result, err
}

// newHTTPClient builds the client Fetch uses. It is the single place a client
// is constructed, which is what makes HTTPClientsBuilt a real assertion rather
// than a decorative one.
func newHTTPClient() *http.Client {
	httpClientsBuilt.Add(1)
	return &http.Client{Timeout: fetchTimeout}
}

func runCheck(ctx context.Context, client *http.Client, catalogURL string, now time.Time, status *FetchStatus) (FetchResult, error) {
	index, err := fetchBytes(ctx, client, catalogURL)
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetch catalog: %w", err)
	}
	entries, err := parseCatalog(index)
	if err != nil {
		return FetchResult{}, err
	}
	base, err := baseURL(catalogURL)
	if err != nil {
		return FetchResult{}, err
	}

	var result FetchResult
	for _, entry := range entries {
		agentStatus := AgentFetchStatus{
			CachedVersion:   CachedRemote(entry.agent).Version,
			LastCheckedUnix: now.Unix(),
		}
		url, err := joinURL(base, entry.path)
		if err != nil {
			agentStatus.LastResult = FetchResultFailed
			agentStatus.LastError = err.Error()
			status.Agents[entry.agent] = agentStatus
			continue
		}
		data, err := fetchBytes(ctx, client, url)
		if err != nil {
			agentStatus.LastResult = FetchResultFailed
			agentStatus.LastError = err.Error()
			status.Agents[entry.agent] = agentStatus
			continue
		}
		updated, served, err := commitFetchedManifest(entry, data)
		agentStatus.ServedVersion = served
		switch {
		case err != nil:
			agentStatus.LastResult = fetchFailureKind(err)
			agentStatus.LastError = err.Error()
		case updated:
			agentStatus.LastResult = FetchResultUpdated
			agentStatus.CachedVersion = served
			result.Updated = append(result.Updated, entry.agent)
		default:
			agentStatus.LastResult = FetchResultCurrent
		}
		status.Agents[entry.agent] = agentStatus
	}
	sort.Strings(result.Updated)
	return result, nil
}

// ignoredFetchError marks a served file that was read and understood and must
// still not be used: it needs a newer engine, or it names another agent. It is
// separated from an outright failure because the two are different news to a
// reader of the status file -- one says upstream has moved past this Sidecar,
// the other says something is broken.
type ignoredFetchError struct{ err error }

func (e *ignoredFetchError) Error() string { return e.err.Error() }
func (e *ignoredFetchError) Unwrap() error { return e.err }

func fetchFailureKind(err error) string {
	var ignored *ignoredFetchError
	if errors.As(err, &ignored) {
		return FetchResultIgnored
	}
	return FetchResultFailed
}

// commitFetchedManifest applies Herdr's process_agent_manifest
// (manifest_update.rs:315) to one served file, returning whether the cache
// moved and the version that was served.
//
// The three refusals are upstream's, in upstream's order:
//
//   - a served version older than what is cached is a downgrade and is refused,
//     so a catalog rollback cannot walk a client backwards;
//   - a served version equal to the cached one whose *bytes* differ is refused,
//     because a file that changed without a version bump is the one shape a
//     version comparison cannot protect anyone from;
//   - a served version equal to the cached one with identical bytes is a
//     no-op, which is the ordinary daily result.
//
// The engine-version check and the id check happen first, inside ParseRemote
// and the id comparison, so a file this engine cannot evaluate is never written
// to the cache at all.
func commitFetchedManifest(entry catalogEntry, data []byte) (bool, string, error) {
	if len(data) == 0 {
		return false, "", fmt.Errorf("served an empty file")
	}
	served, err := manifest.ParseRemoteWith(data, manifest.ValidateOptions{AllowIncompatibleRegex: true})
	if err != nil {
		if _, tooNew := manifest.AsEngineTooNew(err); tooNew {
			return false, "", &ignoredFetchError{err: err}
		}
		return false, "", err
	}
	if served.ID != entry.id {
		return false, served.Version, &ignoredFetchError{
			err: fmt.Errorf("manifest id %q does not match catalog id %q", served.ID, entry.id),
		}
	}

	path := RemoteCachePath(entry.agent)
	if path == "" {
		return false, served.Version, fmt.Errorf("no state directory")
	}
	if cached := CachedRemote(entry.agent); cached.Version != "" {
		switch manifest.CompareVersions(served.Version, cached.Version) {
		case -1:
			return false, served.Version, fmt.Errorf("served version %s is older than cached %s",
				served.Version, cached.Version)
		case 0:
			committed, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Equal(committed, data) {
				return false, served.Version, nil
			}
			return false, served.Version, fmt.Errorf("served version %s changed content without a version bump",
				served.Version)
		}
	}
	if err := atomicWriteFile(path, data); err != nil {
		return false, served.Version, err
	}
	return true, served.Version, nil
}

// catalogEntry is one row of the index, resolved onto Sidecar's own agent key.
type catalogEntry struct {
	// id is the catalog's own id, which is Herdr's agent label.
	id string
	// agent is the vendored file's base name, the key Load uses.
	agent string
	// path is the catalog-relative path the file is served at.
	path string
}

type rawCatalog struct {
	SchemaVersion int             `toml:"schema_version"`
	Agents        []rawCatalogRow `toml:"agents"`
}

type rawCatalogRow struct {
	ID   string `toml:"id"`
	Path string `toml:"path"`
}

// parseCatalog ports Herdr's parse_catalog (manifest_update.rs:350): strict
// decoding, one supported schema version, unsafe paths refused, duplicates
// refused, and an agent this client does not know skipped rather than fatal.
//
// "Does not know" is the one place Sidecar's answer differs from Herdr's, and
// the difference is only in what the set is: Herdr skips an id its Agent enum
// has no variant for, Sidecar skips a file it has no vendored manifest for.
// Both mean the same thing -- a catalog that has moved ahead of this binary
// does not break it -- and the vendored tree is what a Sidecar release ships,
// so it is the right set to compare against.
func parseCatalog(data []byte) ([]catalogEntry, error) {
	var raw rawCatalog
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse catalog TOML: %w", err)
	}
	if raw.SchemaVersion != CatalogSchemaVersion {
		return nil, fmt.Errorf("unsupported catalog schema_version %d", raw.SchemaVersion)
	}

	seen := map[string]bool{}
	var out []catalogEntry
	for _, row := range raw.Agents {
		agent, err := agentForCatalogPath(row.Path)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(row.ID) == "" {
			return nil, fmt.Errorf("catalog entry for %q has an empty id", row.Path)
		}
		if seen[agent] {
			return nil, fmt.Errorf("catalog contains duplicate agent %s", row.ID)
		}
		seen[agent] = true
		if _, err := UpstreamBytes(agent + ".toml"); err != nil {
			continue
		}
		out = append(out, catalogEntry{id: strings.TrimSpace(row.ID), agent: agent, path: row.Path})
	}
	return out, nil
}

// baseURL is Herdr's base_url: everything up to the last "/" of the index URL.
func baseURL(url string) (string, error) {
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return "", fmt.Errorf("catalog URL %s has no base path", url)
	}
	return url[:i], nil
}

// joinURL is Herdr's join_url, with the same refusals: a catalog must not be
// able to point a client at another host or out of its own directory.
func joinURL(base, path string) (string, error) {
	if strings.Contains(path, "://") || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("unsafe manifest path %s", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe manifest path %s", path)
		}
	}
	return strings.TrimRight(base, "/") + "/" + path, nil
}

// fetchBytes reads one file, capped at MaxFetchBytes.
//
// The cap is enforced by reading one byte past it and refusing the response if
// that byte exists, which is Herdr's own shape (take(MAX_FETCH_BYTES + 1), then
// compare). Content-Length is not trusted: a server that lies about it, or
// omits it, would otherwise get an unbounded read.
func fetchBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response from %s: %w", url, err)
	}
	if len(data) > MaxFetchBytes {
		return nil, fmt.Errorf("response from %s exceeded %d bytes", url, MaxFetchBytes)
	}
	return data, nil
}
