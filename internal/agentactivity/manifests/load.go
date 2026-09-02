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

// Source describes which files produced a compiled manifest, in the style of
// Herdr's `manifest_source` explain field, so a `sidecar agent explain` record
// and a `herdr agent explain` record read the same way side by side.
type Source struct {
	// Agent is the Herdr agent id the manifest was loaded for.
	Agent string
	// Version is the vendored upstream manifest's version.
	Version string
	// OverlayApplied records that sidecar/<agent>.toml was merged in.
	OverlayApplied bool
	// Diagnostic is a human-readable note about something that went wrong but
	// did not stop the load: an overlay that failed to parse, merge, or
	// validate. The upstream manifest is used alone in that case, because a
	// broken Sidecar addition must never take a working vendored file down
	// with it. Empty when nothing went wrong.
	Diagnostic string
}

// Label renders the Herdr-style source string, e.g.
// "bundled claude 2026.08.29.1 + sidecar overlay".
func (s Source) Label() string {
	label := "bundled " + s.Agent
	if s.Version != "" {
		label += " " + s.Version
	}
	if s.OverlayApplied {
		label += " + sidecar overlay"
	}
	return label
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

// Load returns the compiled manifest for a Herdr agent id, merging the Sidecar
// overlay when one exists.
//
// The work happens on first use per agent behind a sync.Once, never at package
// init: a Sidecar start that never opens a pane running Claude should not pay
// to parse claude.toml, and the startup-latency rule forbids doing it in Init()
// anyway. The compiled result is immutable and shared; callers must not mutate
// it.
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

	source := Source{Agent: agent, Version: upstream.Version}
	merged := upstream

	if overlayBytes, overlayErr := sidecarFiles.ReadFile("sidecar/" + agent + ".toml"); overlayErr == nil {
		overlay, parseErr := manifest.ParseOverlay(overlayBytes)
		if parseErr != nil {
			source.Diagnostic = fmt.Sprintf("ignored sidecar/%s.toml: %v", agent, parseErr)
		} else if candidate, mergeErr := manifest.Merge(upstream, overlay); mergeErr != nil {
			source.Diagnostic = fmt.Sprintf("ignored sidecar/%s.toml: %v", agent, mergeErr)
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
		source.Diagnostic = fmt.Sprintf("ignored sidecar/%s.toml: merged manifest did not compile: %v", agent, err)
		source.OverlayApplied = false
		compiled, err = manifest.Compile(upstream)
		if err != nil {
			return loaded{source: source, err: err}
		}
	}
	compiled.Source = source.Label()
	compiled.OverlayApplied = source.OverlayApplied
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
