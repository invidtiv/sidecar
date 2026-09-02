package manifests

import (
	"embed"
	"fmt"
	"io/fs"
	"sync"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
)

//go:embed sidecar
var sidecarFiles embed.FS

// SourceKind names which of the manifest sources produced a compiled manifest.
// It is Herdr's ManifestSource::kind (manifest.rs:64 at e2b85c7) with Sidecar's
// third source folded in as a flag on the bundled kind rather than a kind of
// its own, because an overlay amends a bundled file rather than replacing it.
//
// Phase 5 of docs/plans/active/herdr-detection-parity.md adds a fourth source,
// a manifest fetched from the published catalog, which slots in between the
// bundled file and the local override: an override still wins over it. Adding
// it is a constant here and a branch in load, not a change to this contract.
type SourceKind string

// The kinds a Source takes.
//
// These are not the lock file's SourceBundled/SourcePublished (lock.go), which
// record which of Herdr's two *distribution* copies the sync tool vendored.
// This is which file the running engine loaded.
const (
	// KindBundled is the vendored Herdr manifest, alone or with its overlay.
	KindBundled SourceKind = "bundled"
	// KindLocalOverride is ~/.config/sidecar/agent-detection/<agent>.toml.
	KindLocalOverride SourceKind = "local override"
)

// Source describes which files produced a compiled manifest, in the style of
// Herdr's `manifest_source` explain field, so a `sidecar agent explain` record
// and a `herdr agent explain` record read the same way side by side.
type Source struct {
	// Agent is the Herdr agent id the manifest was loaded for.
	Agent string
	// Kind names which source won. The zero value reads as KindBundled, so a
	// caller that only cares about the vendored path can leave it unset.
	Kind SourceKind
	// Version is the loaded manifest's version: the vendored one for a bundled
	// source, the override's own for a local override.
	Version string
	// OverlayApplied records that sidecar/<agent>.toml was merged in. It is
	// never true for a local override, which replaces the overlay along with
	// the vendored file.
	OverlayApplied bool
	// Path is the file a local override was read from. Empty for every other
	// kind.
	Path string
	// Diagnostic is a human-readable note about something that went wrong but
	// did not stop the load: an overlay that failed to parse, merge, or
	// validate, or a local override that was found and refused, in which case
	// the next source down is used, because a broken user-owned file must never
	// take a working vendored file with it. It also carries the one problem an
	// override can have while still being used: a rule Go's regexp cannot
	// compile, which is dead in a file that has no overlay under it to rewrite
	// it. Empty when nothing went wrong.
	Diagnostic string
}

// Label renders the Herdr-style source string, e.g.
// "bundled claude 2026.08.29.1 + sidecar overlay", or, for an override, the
// path it was read from, which is what Herdr's own label is.
func (s Source) Label() string {
	if s.Kind == KindLocalOverride {
		return string(KindLocalOverride) + " " + s.Path
	}
	label := "bundled " + s.Agent
	if s.Version != "" {
		label += " " + s.Version
	}
	if s.OverlayApplied {
		label += " + sidecar overlay"
	}
	return label
}

// note appends a diagnostic, keeping any already recorded. One load can refuse
// a local override *and* an overlay, and explain has to be able to report both:
// "your override is broken" and "so is the overlay under it" are different
// pieces of news and dropping either one sends the reader to the wrong file.
func (s *Source) note(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if s.Diagnostic == "" {
		s.Diagnostic = msg
		return
	}
	s.Diagnostic += "; " + msg
}

type loaded struct {
	compiled *manifest.Compiled
	source   Source
	err      error
}

var (
	loadedMu sync.Mutex
	loadedBy = map[string]*sync.Once{}
	loadedAt = map[string]loaded{}
)

// Load returns the compiled manifest for a Herdr agent id: a valid local
// override if the user has one, otherwise the vendored manifest with its
// Sidecar overlay merged in.
//
// The work happens on first use per agent behind a sync.Once, never at package
// init: a Sidecar start that never opens a pane running Claude should not pay
// to parse claude.toml, and the startup-latency rule forbids doing it in Init()
// anyway. The one filesystem read on this path -- the local override -- is
// inside that same sync.Once for the same reason. The compiled result is
// immutable and shared; callers must not mutate it.
func Load(agent string) (*manifest.Compiled, Source, error) {
	loadedMu.Lock()
	once, ok := loadedBy[agent]
	if !ok {
		once = &sync.Once{}
		loadedBy[agent] = once
	}
	loadedMu.Unlock()

	once.Do(func() {
		result := load(agent)
		loadedMu.Lock()
		loadedAt[agent] = result
		loadedMu.Unlock()
	})

	loadedMu.Lock()
	result := loadedAt[agent]
	loadedMu.Unlock()
	return result.compiled, result.source, result.err
}

func load(agent string) loaded {
	upstreamBytes, err := UpstreamBytes(agent + ".toml")
	if err != nil {
		return loaded{err: fmt.Errorf("no vendored Herdr manifest for agent %q: %w", agent, err)}
	}
	// The vendored tree carries upstream's four `\p{Alphabetic}` patterns
	// verbatim, which RE2 cannot compile. AllowIncompatibleRegex keeps them from
	// failing the load; Compile records them per rule and the matching overlay
	// carries the RE2 rewrite.
	upstream, err := manifest.ParseAndValidateWith(upstreamBytes,
		manifest.ValidateOptions{AllowIncompatibleRegex: true})
	if err != nil {
		return loaded{err: fmt.Errorf("vendored %s.toml is invalid: %w", agent, err)}
	}

	// Precedence, from the plan's "Local overrides": a valid local override wins
	// over vendored-plus-overlay for that agent. Phase 5's cached remote manifest
	// goes between these two, and loses to the override just as it does in Herdr.
	override, overridePath, diagnostic := readOverride(agent, upstream)
	if override != nil {
		// An override can load *and* have something wrong with it -- a rule RE2
		// cannot compile is the case that exists -- so the diagnostic travels
		// onto the source of the file that won, not only onto the fallback.
		source := Source{
			Agent:      agent,
			Kind:       KindLocalOverride,
			Version:    override.Version,
			Path:       overridePath,
			Diagnostic: diagnostic,
		}
		compiled, compileErr := manifest.Compile(override)
		if compileErr == nil {
			compiled.Source = source.Label()
			compiled.Warning = source.Diagnostic
			return loaded{compiled: compiled, source: source}
		}
		// Compile refuses only a region spec the validator would already have
		// rejected, so this is close to unreachable; it is handled rather than
		// asserted because the alternative to a wrong belief here is an agent
		// running on a file that could not be built.
		diagnostic = fmt.Sprintf("ignored override %s because it could not be compiled: %v",
			overridePath, compileErr)
	}

	source := Source{Agent: agent, Kind: KindBundled, Version: upstream.Version, Diagnostic: diagnostic}
	merged := upstream

	if overlayBytes, overlayErr := sidecarFiles.ReadFile("sidecar/" + agent + ".toml"); overlayErr == nil {
		overlay, parseErr := manifest.ParseOverlay(overlayBytes)
		if parseErr != nil {
			source.note("ignored sidecar/%s.toml: %v", agent, parseErr)
		} else if candidate, mergeErr := manifest.Merge(upstream, overlay); mergeErr != nil {
			source.note("ignored sidecar/%s.toml: %v", agent, mergeErr)
		} else {
			merged = candidate
			source.OverlayApplied = true
		}
	}

	compiled, err := manifest.Compile(merged)
	if err != nil {
		// Unreachable today: Compile fails only on a nil manifest or a region
		// spec, and Merge has already run the full validator over the merged
		// rules. It is kept because the alternative to a wrong belief here is an
		// agent with no manifest at all, and Compile's contract may widen.
		//
		// A merged manifest that will not compile is the overlay's fault: the
		// vendored file compiles in CI. Fall back to upstream alone rather than
		// leaving the agent with no manifest at all.
		if !source.OverlayApplied {
			return loaded{source: source, err: err}
		}
		source.note("ignored sidecar/%s.toml: merged manifest did not compile: %v", agent, err)
		source.OverlayApplied = false
		compiled, err = manifest.Compile(upstream)
		if err != nil {
			return loaded{source: source, err: err}
		}
	}
	compiled.Source = source.Label()
	compiled.OverlayApplied = source.OverlayApplied
	compiled.Warning = source.Diagnostic
	return loaded{compiled: compiled, source: source}
}

// SidecarOverlays returns the overlay files as a filesystem rooted at the
// sidecar directory, so a caller opens "claude.toml" rather than
// "sidecar/claude.toml".
func SidecarOverlays() (fs.FS, error) { return fs.Sub(sidecarFiles, "sidecar") }

// HasOverlay reports whether an overlay is vendored for an agent.
func HasOverlay(agent string) bool {
	_, err := sidecarFiles.ReadFile("sidecar/" + agent + ".toml")
	return err == nil
}

// Agents lists every vendored upstream manifest id, in file order.
func Agents() ([]string, error) {
	dir, err := Upstream()
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "index.toml" || len(name) < 6 || name[len(name)-5:] != ".toml" {
			continue
		}
		out = append(out, name[:len(name)-5])
	}
	return out, nil
}
