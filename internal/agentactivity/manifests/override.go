package manifests

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
// An override that cannot be read, parsed, validated, compiled, that declares
// no id, or that names another agent is *ignored*, never fatal, and the reason
// is carried on the Source's Diagnostic so `sidecar agent explain` can say the
// file was found and why it was refused. A broken scratch file must never take a
// working vendored manifest down with it.
//
// An override that loads can carry a diagnostic too. The one that matters is a
// rule Go's regexp cannot compile: that rule is dead, and because an override
// replaces the overlay rather than layering over it, a user who copies one of
// the four vendored files carrying `\p{Alphabetic}` also drops the overlay
// rewrite that made the rule live. Nothing else in the record says so above the
// per-rule evidence, so the diagnostic says it.
//
// There is no hot reload: the file is read once per agent, behind the same
// sync.Once that compiles the vendored manifest, and `explain` reports the
// loaded source so a process running a stale copy is obvious.

// OverrideDirName is the directory under ~/.config/sidecar that holds local
// overrides. It is Herdr's spelling, because the two directories hold the same
// files and a user tuning a rule will have both open.
const OverrideDirName = "agent-detection"

// overrideReads counts how many times an override path has been consulted.
// It exists so TestOverrideIsReadOnceAtFirstLoadAndNotBefore can prove the
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
// non-empty only when a file was actually found, so "no override" and "a
// problem with an override" are distinguishable by the caller. A non-nil
// manifest with a diagnostic is an override that is in use and has something
// wrong with it that does not stop it being used.
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
		// A dangling symlink opens as ErrNotExist and is indistinguishable here
		// from no override at all, which Herdr also cannot tell apart. Sidecar
		// diverges and says so, because the two are not the same event: nobody
		// creates a symlink by accident, and a link into a dotfiles repository
		// that has not been checked out yet is a plausible way to end up with
		// one. The Lstat costs a second syscall on the far commoner "no
		// override" path, once per agent, inside the same sync.Once as the read.
		if link, linkErr := os.Lstat(path); linkErr == nil && link.Mode()&os.ModeSymlink != 0 {
			return nil, path, fmt.Sprintf("ignored override %s because it is a symlink whose target does not exist", path)
		}
		return nil, "", ""
	case err != nil:
		return nil, path, fmt.Sprintf("ignored override %s because it could not be loaded: %v", path, err)
	}

	// The same limits as any manifest. AllowIncompatibleRegex is set for the
	// reason it is set on the vendored files: RE2 cannot express upstream's four
	// `\p{Alphabetic}` patterns, and a user whose override starts life as a copy
	// of one of those four vendored files would otherwise have the whole file
	// refused over a rule they never touched. What the file loses is said out
	// loud below rather than left to per-rule explain evidence: an override that
	// silently drops a rule is the false "done" this engine exists to prevent.
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
	if override.ID == "" {
		return nil, path, fmt.Sprintf("ignored override %s because it declares no manifest id", path)
	}
	if !overrideNamesAgent(override, agent, vendored) {
		return nil, path, fmt.Sprintf("ignored override %s because manifest id %q does not match %q",
			path, override.ID, agent)
	}
	if dead := deadRules(override); len(dead) > 0 {
		return override, path, fmt.Sprintf(
			"override %s is in use, but Go's regexp cannot compile the patterns in these rules, so they never match: %s. "+
				"An override also replaces the Sidecar overlay for this agent, and the overlay is where an RE2 rewrite "+
				"of an upstream rule lives, so a vendored file copied here loses that rewrite along with the rule",
			path, strings.Join(dead, ", "))
	}
	return override, path, ""
}

// deadRules names the rules in an override that RE2 cannot compile, in file
// order and without repeats.
//
// A rule with an incompatible pattern is skipped whole (see
// manifest.RegexIncompatibleNote), so this is the list of rules the user wrote
// or copied that assert nothing. It is deliberately rule ids rather than
// manifest.RegexIncompatibility.String(): the pattern and RE2's own error are
// already in the per-rule explain evidence, and a warning line that carries a
// regex is a warning line nobody reads.
func deadRules(m *manifest.Manifest) []string {
	var out []string
	seen := map[string]bool{}
	for _, bad := range m.RegexIncompatibilities() {
		if seen[bad.RuleID] {
			continue
		}
		seen[bad.RuleID] = true
		out = append(out, bad.RuleID)
	}
	return out
}

// overrideNamesAgent is Herdr's manifest_matches_agent (manifest.rs:1137): a
// file in the wrong place must not be evaluated against the wrong panes, and the
// declared id is the only place that can be caught, because the loader keys on
// the file name.
//
// A blank id never reaches here: the caller refuses it, because Herdr does.
// `AgentManifest.id` carries no `#[serde(default)]` (manifest.rs:141), so a file
// with no id fails deserialization outright, and an explicit `id = ""` matches
// no label and no alias and is refused by this function's Rust original. An
// override is not an overlay: it replaces the manifest rather than amending the
// file it is named after, so there is nothing for a blank id to inherit from.
//
// Both the file's own base name and the vendored manifest's id count as a match,
// because those two differ for `antigravity` (file antigravity.toml, id "agy")
// and an override copied from the vendored file carries the id, not the base
// name.
func overrideNamesAgent(override *manifest.Manifest, agent string, vendored *manifest.Manifest) bool {
	if override.ID == "" {
		return false
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
