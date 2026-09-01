// Package manifests holds Sidecar's vendored copy of Herdr's agent-detection
// manifests, the lock that pins them, and the extracted alias and authority
// tables that go with them.
//
// Everything under upstream/ is a byte-for-byte copy of a Herdr file and is
// never edited by hand: TestVendoredManifestsMatchLock hashes each one against
// upstream.lock.json so an edit fails CI. Sidecar's own rule changes belong in
// sidecar/<agent>.toml overlays.
//
// The package embeds the files and does no work at init. Parsing happens
// lazily behind a sync.Once in the accessors, per the startup-latency rule.
//
// It must not import internal/agentactivity; the dependency runs the other way.
package manifests

// LockSchemaVersion is the shape version of upstream.lock.json. Bump it when a
// field changes meaning, so a stale lock fails loudly.
const LockSchemaVersion = 1

// Lock records exactly which upstream bytes are vendored and where they came
// from, so a reader can reproduce the sync and a test can prove the tree has
// not been edited by hand.
type Lock struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedAt   string       `json:"generated_at"`
	EngineVersion int          `json:"engine_version"`
	Herdr         LockUpstream `json:"herdr"`
	Catalog       LockCatalog  `json:"catalog"`
	Agents        []LockAgent  `json:"agents"`
	// Notes carries anything about this sync a reviewer needs and the fields
	// above cannot express, such as an unreachable network.
	Notes []string `json:"notes,omitempty"`
}

// LockUpstream pins the Herdr source the manifests were read from.
type LockUpstream struct {
	Repository string `json:"repository"`
	// Ref is what was asked for: a tag, a branch, or a commit.
	Ref string `json:"ref"`
	// Commit is the resolved commit when it could be determined.
	Commit string `json:"commit,omitempty"`
	// SourceDir records that a local checkout supplied the bytes.
	SourceDir string `json:"source_dir,omitempty"`
	// PinnedReleaseTag is the Herdr release the differential harness runs
	// against. It can differ from Ref while a sync tracks main.
	PinnedReleaseTag string `json:"pinned_release_tag"`
}

// LockCatalog records the published catalog this sync consulted.
type LockCatalog struct {
	URL string `json:"url"`
	// ETag is the catalog's HTTP ETag, or "unknown" when the catalog could not
	// be reached.
	ETag string `json:"etag"`
	// Fetched is false for an offline sync, where the published copies came
	// from the source checkout's distribution directory instead.
	Fetched       bool   `json:"fetched"`
	Source        string `json:"source"`
	SchemaVersion int    `json:"schema_version"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
}

// LockAgent pins one vendored manifest.
type LockAgent struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`

	Version          string `json:"version"`
	MinEngineVersion int    `json:"min_engine_version"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	RuleCount        int    `json:"rule_count"`

	// Source is "bundled" or "published": which of the two copies a Herdr
	// client would load, and therefore which one is vendored here.
	Source       string `json:"source"`
	SourceReason string `json:"source_reason"`

	BundledVersion   string `json:"bundled_version,omitempty"`
	PublishedVersion string `json:"published_version,omitempty"`

	Aliases []string `json:"aliases"`

	// RegexIncompatibilities names every pattern in this file that Rust's
	// regex crate accepts and Go's RE2 cannot compile. Vendoring does not fail
	// on one; an overlay carries the rewrite.
	RegexIncompatibilities []LockRegexIncompatibility `json:"regex_incompatibilities,omitempty"`
}

// LockRegexIncompatibility is one pattern RE2 cannot compile.
type LockRegexIncompatibility struct {
	RuleID  string `json:"rule_id"`
	Field   string `json:"field"`
	Pattern string `json:"pattern"`
	Error   string `json:"error"`
}

// Agent returns the lock entry for an agent id.
func (l *Lock) Agent(id string) (LockAgent, bool) {
	for _, agent := range l.Agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return LockAgent{}, false
}

// SourceBundled and SourcePublished are the two values LockAgent.Source takes.
const (
	SourceBundled   = "bundled"
	SourcePublished = "published"
)
