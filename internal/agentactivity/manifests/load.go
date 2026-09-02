package manifests

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
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

// entry is one agent's memoised load: the sync.Once that runs it and the result
// it produced, in one object.
//
// Holding the result *in the entry* rather than in a second map keyed by agent
// is what makes Invalidate safe against a concurrent Load. With two maps, a Load
// that was already inside load() when Invalidate ran would come back and write
// its now-stale result under the agent's key, on top of whatever a later Load
// had since put there, and the process would serve the pre-fetch manifest until
// it restarted. Here that goroutine writes into the entry it started with, which
// Invalidate has already unlinked, so its answer is returned to its own caller
// and reaches nobody else.
type entry struct {
	once   sync.Once
	result loaded
}

var (
	loadedMu sync.Mutex
	loadedBy = map[string]*entry{}
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
	e, ok := loadedBy[agent]
	if !ok {
		e = &entry{}
		loadedBy[agent] = e
	}
	loadedMu.Unlock()

	e.once.Do(func() { e.result = load(agent) })
	return e.result.compiled, e.result.source, e.result.err
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

	// The overlay merges onto whichever upstream file won, not only onto the
	// vendored one. That is the whole point of an overlay being data in the
	// same grammar: the four `\p{Alphabetic}` RE2 rewrites and the seventeen
	// Sidecar-owned rules are amendments to *upstream's file for this agent*,
	// and a newer copy of that file is still that file. Dropping them on the
	// day a fetch succeeds would make turning the setting on a detection
	// regression, which is the opposite of what it is for.
	merged, overlayApplied, overlayErr := mergeOverlay(agent, base)

	// An overlay that no longer fits a *fetched* file -- upstream renamed a rule
	// the overlay replaces or disables -- takes the whole overlay with it, and
	// that is not a cost worth paying for a newer file. codex's overlay alone
	// carries six rules including the `osc_title_idle` disable; cursor's four;
	// claude's five, and dropping them re-exposes the un-rewritten
	// `\p{Alphabetic}` rule the overlay exists to fix. So the fetched file is
	// abandoned rather than the amendments: known-good detection beats a newer
	// file with Sidecar's half stripped out. The vendored tree is always
	// underneath, which is the whole argument for fetching being safe at all.
	//
	// The fetch itself refuses to cache such a file for the same reason
	// (commitFetchedManifest), so reaching here means a cache written by an
	// older binary, or an overlay that changed after it was written. Both are
	// the maintainer's signal to re-cut the overlay, and both are loud: a
	// warning in the log, the diagnostic on every explain record for the agent,
	// and a line under the `sidecar agent manifests` table.
	if overlayErr != nil && kind == KindRemote {
		slog.Warn("detection manifests: the Sidecar overlay no longer fits the fetched manifest, using the vendored one",
			"agent", agent, "cached", remotePath, "cachedVersion", base.Version,
			"vendoredVersion", upstream.Version, "error", overlayErr)
		source.note("the Sidecar overlay no longer fits cached manifest %s (%v), so the vendored manifest %s is running "+
			"instead of cached %s: an overlay carries Sidecar's own rules and its RE2 rewrites, and running the newer "+
			"file without them would lose detection rather than gain it",
			remotePath, overlayErr, upstream.Version, base.Version)
		base = upstream
		kind = KindBundled
		source.Kind = KindBundled
		source.Version = upstream.Version
		source.Path = ""
		merged, overlayApplied, overlayErr = mergeOverlay(agent, base)
	}
	if overlayErr != nil {
		// The vendored file's own overlay does not fit it, which CI catches, so
		// this is a build problem rather than a fetch one. Upstream alone runs.
		source.note("ignored sidecar/%s.toml: %v", agent, overlayErr)
	}
	source.OverlayApplied = overlayApplied

	// What the winning file still cannot compile, said out loud. A rule with an
	// RE2-incompatible pattern is skipped whole, so it asserts nothing, and on
	// the vendored path that is impossible by test. On the fetched path it is
	// exactly what upstream publishing a new `\p{Alphabetic}` rule looks like:
	// the file validates, caches, becomes active, and one rule is quietly dead
	// with no overlay rewrite under it yet. A rule that silently never fires is
	// the false "done" this engine exists to prevent, so it is a diagnostic
	// here, on the same terms an override's dead rules are one.
	if kind == KindRemote {
		if dead := deadRules(merged); len(dead) > 0 {
			slog.Warn("detection manifests: the fetched manifest has rules Go's regexp cannot compile",
				"agent", agent, "cached", remotePath, "rules", dead)
			source.note("cached manifest %s is in use, but Go's regexp cannot compile the patterns in these rules, "+
				"so they never match: %s. A rewrite belongs in sidecar/%s.toml, which is where the existing "+
				"RE2 rewrites of upstream rules live",
				remotePath, strings.Join(dead, ", "), agent)
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

// mergeOverlay applies sidecar/<agent>.toml to a manifest, returning the merged
// result, whether an overlay was applied, and why not when one was not.
//
// An agent with no overlay is not an error: the base comes back unchanged with
// applied false and a nil error. Only a file that exists and cannot be used --
// it does not parse, or it names a rule id the base does not have -- is an
// error, and it is the caller that decides what to do about it, because the
// answer differs by which file the overlay was being merged onto.
func mergeOverlay(agent string, base *manifest.Manifest) (*manifest.Manifest, bool, error) {
	overlayBytes, err := sidecarFiles.ReadFile("sidecar/" + agent + ".toml")
	if err != nil {
		return base, false, nil
	}
	overlay, err := manifest.ParseOverlay(overlayBytes)
	if err != nil {
		return base, false, err
	}
	merged, err := manifest.Merge(base, overlay)
	if err != nil {
		return base, false, err
	}
	return merged, true, nil
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
