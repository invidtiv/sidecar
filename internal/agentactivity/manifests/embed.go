package manifests

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"
)

//go:embed upstream
var upstreamFiles embed.FS

//go:embed upstream.lock.json
var lockBytes []byte

//go:embed aliases.upstream.json
var aliasBytes []byte

//go:embed authority.upstream.json
var authorityBytes []byte

var (
	upstreamOnce sync.Once
	upstreamSub  fs.FS
	upstreamErr  error

	lockOnce   sync.Once
	lockValue  *Lock
	lockErr    error
	aliasOnce  sync.Once
	aliasValue *Aliases
	aliasErr   error

	authorityOnce  sync.Once
	authorityValue *Authority
	authorityErr   error
)

// Upstream returns the vendored Herdr files as a filesystem rooted at the
// upstream directory, so a caller opens "claude.toml" rather than
// "upstream/claude.toml". The embed itself costs nothing at init; only this
// sub-filesystem is built, and only on first use.
func Upstream() (fs.FS, error) {
	upstreamOnce.Do(func() {
		upstreamSub, upstreamErr = fs.Sub(upstreamFiles, "upstream")
	})
	return upstreamSub, upstreamErr
}

// UpstreamBytes reads one vendored file by its path relative to upstream/.
func UpstreamBytes(name string) ([]byte, error) {
	dir, err := Upstream()
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(dir, name)
}

// LockJSON returns the raw upstream.lock.json bytes.
func LockJSON() []byte { return lockBytes }

// Lock returns the parsed lock. It is decoded once and shared; callers must
// not mutate the result.
func LoadLock() (*Lock, error) {
	lockOnce.Do(func() {
		var lock Lock
		if err := json.Unmarshal(lockBytes, &lock); err != nil {
			lockErr = fmt.Errorf("decode upstream.lock.json: %w", err)
			return
		}
		if lock.SchemaVersion != LockSchemaVersion {
			lockErr = fmt.Errorf("upstream.lock.json schema_version is %d, want %d",
				lock.SchemaVersion, LockSchemaVersion)
			return
		}
		lockValue = &lock
	})
	return lockValue, lockErr
}

// Aliases is the process-identity table extracted from Herdr's
// src/detect/mod.rs. Adding a brand-new agent still needs a binary change in
// both projects; this table is what makes the gap visible.
type Aliases struct {
	SchemaVersion int    `json:"schema_version"`
	GeneratedFrom string `json:"generated_from"`
	HerdrRef      string `json:"herdr_ref"`
	// Agents maps a Herdr agent id to every process name lookup_agent accepts
	// for it, the canonical id included.
	Agents map[string][]string `json:"agents"`
	// GenericRuntimes is Herdr's is_generic_runtime_or_shell list, without the
	// python family, which is a rule rather than a list.
	GenericRuntimes []string `json:"generic_runtimes"`
	// PythonRuntimeRule describes how python is matched, in prose, because it
	// is a predicate and not an enumerable set.
	PythonRuntimeRule string `json:"python_runtime_rule"`
	// VersionedBinaryPrefixes maps an agent id to a process-name prefix that
	// must be followed by a digit, Herdr's is_muse_versioned_binary shape.
	VersionedBinaryPrefixes map[string]string `json:"versioned_binary_prefixes"`
	// NormalizedSuffixes are stripped from a process name before lookup.
	NormalizedSuffixes []string `json:"normalized_suffixes"`
}

// LoadAliases returns the parsed alias table.
func LoadAliases() (*Aliases, error) {
	aliasOnce.Do(func() {
		var a Aliases
		if err := json.Unmarshal(aliasBytes, &a); err != nil {
			aliasErr = fmt.Errorf("decode aliases.upstream.json: %w", err)
			return
		}
		aliasValue = &a
	})
	return aliasValue, aliasErr
}

// AliasesJSON returns the raw aliases.upstream.json bytes.
func AliasesJSON() []byte { return aliasBytes }

// Authority is Herdr's per-agent lifecycle authority table. It is a *target*,
// not a claim: Sidecar's own tiers in internal/agentlifecycle are earned by
// traces and are never copied from here.
type Authority struct {
	SchemaVersion int                       `json:"schema_version"`
	GeneratedFrom []string                  `json:"generated_from"`
	HerdrRef      string                    `json:"herdr_ref"`
	Agents        map[string]AuthorityAgent `json:"agents"`
}

// AuthorityAgent is one row of Herdr's published agent table.
type AuthorityAgent struct {
	DisplayName string `json:"display_name"`
	// LifecycleAuthority is "hooks", "session_identity", or "none".
	LifecycleAuthority string `json:"lifecycle_authority"`
	StateAuthority     string `json:"state_authority"`
	IntegrationRole    string `json:"integration_role"`
	// IntegrationVersion is the HERDR_INTEGRATION_VERSION carried by the
	// agent's integration assets, or 0 where Herdr ships no integration.
	IntegrationVersion int `json:"integration_version,omitempty"`
	// IntegrationAssetDir is the directory under src/integration/assets that
	// holds those assets.
	IntegrationAssetDir string `json:"integration_asset_dir,omitempty"`
}

// The three values AuthorityAgent.LifecycleAuthority takes.
const (
	AuthorityHooks           = "hooks"
	AuthoritySessionIdentity = "session_identity"
	AuthorityNone            = "none"
)

// LoadAuthority returns the parsed authority table.
func LoadAuthority() (*Authority, error) {
	authorityOnce.Do(func() {
		var a Authority
		if err := json.Unmarshal(authorityBytes, &a); err != nil {
			authorityErr = fmt.Errorf("decode authority.upstream.json: %w", err)
			return
		}
		authorityValue = &a
	})
	return authorityValue, authorityErr
}

// AuthorityJSON returns the raw authority.upstream.json bytes.
func AuthorityJSON() []byte { return authorityBytes }
