package manifests

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/config"
)

// Local overrides: ~/.config/sidecar/agent-detection/<agent>.toml, mirroring
// Herdr's ~/.config/herdr/agent-detection/<agent>.toml.
//
// An override *replaces* the vendored manifest and its Sidecar overlay for that
// agent; it is not a third merge layer. That is Herdr's own precedence
// (load_manifest_uncached, manifest.rs:599 at e2b85c7), and it is the point:
// the override is a scratch file for tuning a rule against a live pane, so
// having it silently inherit twenty rules from two other files would make what
// the user is testing impossible to reason about. Whatever survives tuning
// becomes an overlay, which is the layer that does merge.
//
// An override that cannot be read, parsed, validated, compiled, or that names
// another agent is *ignored*, never fatal, and the reason is carried on the
// Source's Diagnostic so `sidecar agent explain` can say the file was found and
// why it was refused. A broken scratch file must never take a working vendored
// manifest down with it.
//
// There is no hot reload: the file is read once per agent, behind the same
// sync.Once that compiles the vendored manifest, and `explain` reports the
// loaded source so a process running a stale copy is obvious.

// OverrideDirName is the directory under ~/.config/sidecar that holds local
// overrides. It is Herdr's spelling, because the two directories hold the same
// files and a user tuning a rule will have both open.
const OverrideDirName = "agent-detection"

// overrideReads counts how many times an override path has been consulted.
// It exists so TestOverrideDirectoryIsNotReadUntilFirstLoad can prove the
// startup-latency rule holds: reading a user file is a filesystem hit, and it
// must happen at first use of an agent's manifest, never at package init and
// never on a path Init() can reach.
var overrideReads atomic.Int64

// OverrideDir returns the directory local overrides are read from, or "" when
// Sidecar cannot resolve its config directory at all.
//
// It derives from config.ConfigPath rather than from os.UserHomeDir, which is
// what makes `-config <temp path>` and a test's SetTestConfigPath move the
// override directory with everything else on the config axis. Note that
// XDG_CONFIG_HOME deliberately moves nothing here, exactly as it moves nothing
// for config.json itself.
func OverrideDir() string {
	path := config.ConfigPath()
	if path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(path), OverrideDirName)
}

// OverridePath returns the file a local override for an agent would be read
// from. The base name is the vendored manifest's file name, so
// `github-copilot.toml` overrides `upstream/github-copilot.toml` and
// `sidecar/github-copilot.toml`. Herdr names its override files after its agent
// *label* instead, which differs for exactly two agents (`copilot` and `agy`);
// following the vendored file name keeps one rule -- an override is named after
// the file it replaces -- rather than two spellings that agree nineteen times
// out of twenty-one.
func OverridePath(agent string) string {
	dir := OverrideDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, agent+".toml")
}

// readOverride loads the local override for an agent, if there is one.
//
// It returns the parsed override, the path it came from, and a diagnostic. The
// manifest is nil whenever the override is not usable; the diagnostic is
// non-empty only when a file was actually found and refused, so "no override"
// and "a bad override" are distinguishable by the caller.
func readOverride(agent string, vendored *manifest.Manifest) (*manifest.Manifest, string, string) {
	path := OverridePath(agent)
	if path == "" {
		return nil, "", ""
	}
	// A test binary asserts isolated state by default, so this is what keeps
	// `go test ./...` from reading the developer's real
	// ~/.config/sidecar/agent-detection while it runs. It is a silent skip
	// rather than a diagnostic: from the engine's point of view there is no
	// override, and a warning about isolation in every explain record a test
	// produces would be noise.
	if config.AssertIsolatedPath(path) != nil {
		return nil, "", ""
	}

	overrideReads.Add(1)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, "", ""
	case err != nil:
		return nil, path, fmt.Sprintf("ignored override %s because it could not be loaded: %v", path, err)
	}

	// The same limits as any manifest. AllowIncompatibleRegex is set for the
	// reason it is set on the vendored files: RE2 cannot express upstream's four
	// `\p{Alphabetic}` patterns, and a user whose override starts life as a copy
	// of one of those four vendored files would otherwise have the whole file
	// refused over a rule they never touched. Compile records the offending rule
	// in explain evidence instead, which is the honest answer.
	override, err := manifest.ParseAndValidateWith(data,
		manifest.ValidateOptions{AllowIncompatibleRegex: true})
	if err != nil {
		return nil, path, fmt.Sprintf("ignored override %s because it is invalid: %v", path, err)
	}
	// Herdr checks min_engine_version only on a manifest that arrived over the
	// network, because a local override there is the user's own file. Sidecar
	// checks it on an override too: the way a user gets an override in the first
	// place is by copying a published manifest, and a file written for a newer
	// engine would otherwise be evaluated with its newer regions silently
	// unresolvable rather than refused with a reason.
	if override.MinEngineVersion != nil && *override.MinEngineVersion > manifest.EngineVersion {
		tooNew := &manifest.EngineTooNewError{
			ManifestID: override.ID,
			Required:   *override.MinEngineVersion,
			Engine:     manifest.EngineVersion,
		}
		return nil, path, fmt.Sprintf("ignored override %s because %v", path, tooNew)
	}
	if !overrideNamesAgent(override, agent, vendored) {
		return nil, path, fmt.Sprintf("ignored override %s because manifest id %q does not match %q",
			path, override.ID, agent)
	}
	return override, path, ""
}

// overrideNamesAgent is Herdr's manifest_matches_agent (manifest.rs:1137): a
// file in the wrong place must not be evaluated against the wrong panes, and the
// declared id is the only place that can be caught, because the loader keys on
// the file name.
//
// A blank id is accepted, matching the overlay loader's rule that a file which
// declares no id is amending whatever file it is named after. Both the file's
// own base name and the vendored manifest's id count as a match, because those
// two differ for `antigravity` (file antigravity.toml, id "agy") and an override
// copied from the vendored file carries the id, not the base name.
func overrideNamesAgent(override *manifest.Manifest, agent string, vendored *manifest.Manifest) bool {
	if override.ID == "" {
		return true
	}
	wanted := []string{agent}
	if vendored != nil && vendored.ID != "" {
		wanted = append(wanted, vendored.ID)
	}
	for _, want := range wanted {
		if override.ID == want {
			return true
		}
		for _, alias := range override.Aliases {
			if alias == want {
				return true
			}
		}
	}
	return false
}
