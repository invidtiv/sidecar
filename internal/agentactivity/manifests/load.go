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
// overlay folded in as a flag on the upstream kinds rather than a kind of its
// own, because an overlay amends an upstream file rather than replacing it.
//
// The fourth source the Phase 3 comment reserved is KindRemote, added in
// Phase 5: a manifest fetched from the published catalog, which slots in
// between the bundled file and the local override. An override still wins over
// it, exactly as it does in Herdr.
type SourceKind string

// The kinds a Source takes.
//
// These are not the lock file's SourceBundled/SourcePublished (lock.go), which
// record which of Herdr's two *distribution* copies the sync tool vendored.
// This is which file the running engine loaded.
const (
	// KindBundled is the vendored Herdr manifest, alone or with its overlay.
	KindBundled SourceKind = "bundled"
	// KindRemote is a manifest fetched from the published catalog and cached
	// under the state directory, alone or with its overlay. It appears only
	// when detection.remoteManifests is on and the cached copy is at least as
	// new as the vendored one.
	KindRemote SourceKind = "remote"
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
	// source, the cached one for a remote source, the override's own for a
	// local override.
	Version string
	// VendoredVersion is always the version of the manifest vendored into this
	// binary, whichever source won. It is what makes "am I ahead of the
	// vendored tree?" answerable from one record.
	VendoredVersion string
	// CachedRemoteVersion is the version in the runtime fetch cache, or "" when
	// nothing is cached or the cache was refused. Herdr reports the same field
	// under the same name (manifest.rs:43).
	CachedRemoteVersion string
	// OverlayApplied records that sidecar/<agent>.toml was merged in. It is
	// true for a bundled or a remote source that took the overlay, and never
	// for a local override, which replaces the overlay along with the file it
	// is named after.
	OverlayApplied bool
	// Path is the file a local override or a cached remote manifest was read
	// from. Empty for a bundled source.
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
	kind := s.Kind
	if kind == "" {
		kind = KindBundled
	}
	label := string(kind) + " " + s.Agent
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

	// The runtime fetch cache, read before the override so that its version is
	// on the record whichever source wins. A user who turned fetching on and
	// then put an override in front of it has to be able to see both.
	remote, remotePath, remoteDiagnostic := readCachedRemote(agent, upstream)
	cachedRemoteVersion := ""
	if remote != nil {
		cachedRemoteVersion = remote.Version
	}

	// Precedence, from the plan's "Local overrides" and Phase 5: a valid local
	// override wins over everything; below it, the newer of the cached remote
	// manifest and the vendored one, with the Sidecar overlay merged onto
	// whichever of those two won.
	override, overridePath, diagnostic := readOverride(agent, upstream)
	if diagnostic == "" {
		diagnostic = remoteDiagnostic
	} else if remoteDiagnostic != "" {
		diagnostic += "; " + remoteDiagnostic
	}
	if override != nil {
		// An override can load *and* have something wrong with it -- a rule RE2
		// cannot compile is the case that exists -- so the diagnostic travels
		// onto the source of the file that won, not only onto the fallback.
		source := Source{
			Agent:               agent,
			Kind:                KindLocalOverride,
			Version:             override.Version,
			VendoredVersion:     upstream.Version,
			CachedRemoteVersion: cachedRemoteVersion,
			Path:                overridePath,
			Diagnostic:          diagnostic,
		}
		compiled, compileErr := manifest.Compile(override)
		if compileErr == nil {
			applySource(compiled, source)
			return loaded{compiled: compiled, source: source}
		}
		// Compile refuses only a region spec the validator would already have
		// rejected, so this is close to unreachable; it is handled rather than
		// asserted because the alternative to a wrong belief here is an agent
		// running on a file that could not be built.
		diagnostic = fmt.Sprintf("ignored override %s because it could not be compiled: %v",
			overridePath, compileErr)
	}

	// Herdr's read_remote_manifest (manifest.rs:754): a cached copy older than
	// the bundled one is ignored with a note, and one at least as new is used.
	// The tie going to the cached copy is upstream's own -- it compares only
	// `remote_version < bundled_version` -- and it is worth keeping, because at
	// equal versions the two files are the same file and reporting which one
	// answered is more useful than pretending the fetch did nothing.
	base := upstream
	kind := KindBundled
	basePath := ""
	if remote != nil {
		if manifest.CompareVersions(remote.Version, upstream.Version) < 0 {
			diagnostic = note(diagnostic, fmt.Sprintf(
				"ignored cached manifest %s because cached version %s is older than vendored %s",
				remotePath, remote.Version, upstream.Version))
		} else {
			base = remote
			kind = KindRemote
			basePath = remotePath
		}
	}

	source := Source{
		Agent:               agent,
		Kind:                kind,
		Version:             base.Version,
		VendoredVersion:     upstream.Version,
		CachedRemoteVersion: cachedRemoteVersion,
		Path:                basePath,
		Diagnostic:          diagnostic,
	}
	merged := base

	// The overlay merges onto whichever upstream file won, not only onto the
	// vendored one. That is the whole point of an overlay being data in the
	// same grammar: the four `\p{Alphabetic}` RE2 rewrites and the seventeen
	// Sidecar-owned rules are amendments to *upstream's file for this agent*,
	// and a newer copy of that file is still that file. Dropping them on the
	// day a fetch succeeds would make turning the setting on a detection
	// regression, which is the opposite of what it is for.
	//
	// An overlay that no longer fits -- it disables a rule id upstream renamed,
	// say -- fails Merge, and the note below is what tells a maintainer to
	// re-cut it. The remote file is still used; only the overlay is dropped.
	if overlayBytes, overlayErr := sidecarFiles.ReadFile("sidecar/" + agent + ".toml"); overlayErr == nil {
		overlay, parseErr := manifest.ParseOverlay(overlayBytes)
		if parseErr != nil {
			source.note("ignored sidecar/%s.toml: %v", agent, parseErr)
		} else if candidate, mergeErr := manifest.Merge(base, overlay); mergeErr != nil {
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
		// vendored file compiles in CI. Fall back to the upstream file alone
		// rather than leaving the agent with no manifest at all.
		if !source.OverlayApplied {
			return loaded{source: source, err: err}
		}
		source.note("ignored sidecar/%s.toml: merged manifest did not compile: %v", agent, err)
		source.OverlayApplied = false
		compiled, err = manifest.Compile(base)
		if err != nil {
			return loaded{source: source, err: err}
		}
	}
	applySource(compiled, source)
	return loaded{compiled: compiled, source: source}
}

// applySource copies the loader's answer onto the compiled manifest, which is
// where the engine's explain record reads it from. It exists so the three
// return paths in load cannot drift into setting different subsets of it.
func applySource(compiled *manifest.Compiled, source Source) {
	compiled.Source = source.Label()
	compiled.ActiveSource = string(source.Kind)
	compiled.OverlayApplied = source.OverlayApplied
	compiled.VendoredVersion = source.VendoredVersion
	compiled.CachedRemoteVersion = source.CachedRemoteVersion
	compiled.Warning = source.Diagnostic
}

// note appends to a diagnostic string that is not yet on a Source, keeping any
// already recorded, with the same separator Source.note uses.
func note(existing, msg string) string {
	if existing == "" {
		return msg
	}
	return existing + "; " + msg
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
