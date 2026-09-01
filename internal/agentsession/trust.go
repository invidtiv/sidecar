package agentsession

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OfficialSources are the integration sources Sidecar itself ships.
//
// Only a reference reported by one of these is ever marked Reported, and only a
// Reported reference is eligible for automatic resume. A same-cwd adapter
// discovery may propose a candidate for a human to confirm, but it can never
// enter this set, because "the newest conversation in this directory" is a
// guess and resuming the wrong conversation is worse than resuming none.
func OfficialSources() []string {
	return []string{
		"sidecar.codex.hooks",
		"sidecar.claude.hooks",
		"sidecar.opencode.plugin",
		"sidecar.pi.extension",
	}
}

// OfficialSourceFor is the official integration source for a catalog family.
//
// A hook that omits --source gets its provider's own official source rather than
// an empty one, so the ordinary installed path produces a trusted, resumable
// reference and only a caller that deliberately names a different source gets an
// untrusted one.
func OfficialSourceFor(kind string) string {
	switch strings.TrimSpace(kind) {
	case "codex":
		return "sidecar.codex.hooks"
	case "claude":
		return "sidecar.claude.hooks"
	case "opencode":
		return "sidecar.opencode.plugin"
	case "pi":
		return "sidecar.pi.extension"
	default:
		return ""
	}
}

// Official reports whether source is an official Sidecar integration.
func Official(source string) bool {
	for _, known := range OfficialSources() {
		if known == source {
			return true
		}
	}
	return false
}

// Roots describes where a provider is allowed to keep its conversations.
//
// A path reference outside every root is refused rather than stored. The point
// is not that a provider would lie; it is that a hook is untrusted local input,
// and "an absolute path the agent chose" is otherwise a instruction to read an
// arbitrary file later, at restore time, with Sidecar's privileges.
type Roots struct {
	// Home is the user's home directory. Injected so tests do not depend on
	// the developer's own tree.
	Home string
	// Env reads an environment variable. Nil means os.Getenv.
	Env func(string) string
}

func (r Roots) env(name string) string {
	if r.Env == nil {
		return os.Getenv(name)
	}
	return r.Env(name)
}

// OSRoots reads the ambient environment.
func OSRoots() Roots {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return Roots{Home: home}
}

// For returns the approved store roots for a catalog family id.
//
// An unknown provider returns no roots, which makes every path reference for it
// a refusal. That is the intended direction: a provider earns path references by
// having its store location recorded here, not by reporting one.
func (r Roots) For(kind string) []string {
	switch strings.TrimSpace(kind) {
	case "codex":
		base := r.env("CODEX_HOME")
		if base == "" {
			if r.Home == "" {
				return nil
			}
			base = filepath.Join(r.Home, ".codex")
		}
		return []string{
			filepath.Join(base, "sessions"),
			filepath.Join(base, "archived_sessions"),
		}
	case "claude":
		base := r.env("CLAUDE_CONFIG_DIR")
		if base == "" {
			if r.Home == "" {
				return nil
			}
			base = filepath.Join(r.Home, ".claude")
		}
		return []string{filepath.Join(base, "projects")}
	case "opencode":
		base := r.env("XDG_DATA_HOME")
		if base == "" {
			if r.Home == "" {
				return nil
			}
			base = filepath.Join(r.Home, ".local", "share")
		}
		return []string{filepath.Join(base, "opencode")}
	case "muse":
		base := r.env("XDG_DATA_HOME")
		if base == "" {
			if r.Home == "" {
				return nil
			}
			base = filepath.Join(r.Home, ".local", "share")
		}
		return []string{filepath.Join(base, "muse", "sessions")}
	default:
		return nil
	}
}

// WithinRoots reports whether an already-validated absolute path lies inside one
// of the provider's approved roots.
//
// Containment is compared on cleaned paths with a separator boundary, so
// "/home/u/.codexsessions" does not pass as being inside "/home/u/.codex".
func (r Roots) WithinRoots(kind, path string) error {
	roots := r.For(kind)
	if len(roots) == 0 {
		return fmt.Errorf("%w: no approved store root is recorded for provider %q", ErrOutsideStoreRoot, kind)
	}
	// Resolve symlinks on BOTH sides before comparing. A lexical prefix test is
	// not containment: a symlink planted inside an approved root passes it while
	// pointing anywhere on the filesystem, and the target is what actually gets
	// opened. Resolving the root too means a store that is itself a symlink --
	// a dotfiles setup, a relocated home -- still matches its own contents.
	//
	// A path that does not exist yet cannot be resolved, and that is not a
	// refusal: EvalSymlinks fails on a missing leaf, so fall back to the
	// lexical form for the part that does not exist. What must never happen is
	// accepting a path whose RESOLVED form escapes, which is why resolution is
	// preferred wherever it succeeds.
	clean := resolvePath(path)
	for _, root := range roots {
		root = resolvePath(root)
		if clean == root {
			continue // the root itself is a directory, not a transcript
		}
		if strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not under any of: %s", ErrOutsideStoreRoot, path, strings.Join(roots, ", "))
}

// Report is one integration's claim about the conversation in its pane.
//
// It carries only what a provider is allowed to choose: which conversation it is
// talking about. Who it is talking about — host, server, pane, process
// generation — is derived by the caller from the environment and live tmux and
// is never reachable from a flag.
type Report struct {
	// Kind is the catalog family id of the provider.
	Kind string
	// RefKind and Value name the conversation.
	RefKind RefKind
	Value   string
	// Source is the reporting integration.
	Source string
	// Generation is the provider process generation the report came from.
	Generation string
}

// Validate turns an untrusted report into a Ref, or refuses.
//
// Everything a later stage relies on is decided here: bounds, character rules,
// path shape, store-root containment, provider support for the kind, and
// whether the source is official enough to mark the result auto-resumable.
func Validate(rep Report, roots Roots, now func() time.Time) (Ref, error) {
	if err := ValidateSource(rep.Source); err != nil {
		return Ref{}, err
	}
	if strings.TrimSpace(rep.Kind) == "" {
		return Ref{}, fmt.Errorf("%w: the provider kind is empty", ErrInvalidRef)
	}
	if err := ValidateValue(rep.RefKind, rep.Value); err != nil {
		return Ref{}, err
	}
	if rep.RefKind == RefPath {
		if err := roots.WithinRoots(rep.Kind, rep.Value); err != nil {
			return Ref{}, err
		}
	}
	at := time.Time{}
	if now != nil {
		at = now().UTC()
	}
	return Ref{
		Kind:       rep.RefKind,
		Value:      rep.Value,
		Source:     rep.Source,
		Reported:   Official(rep.Source),
		Generation: rep.Generation,
		ReportedAt: at,
	}, nil
}

// resolvePath returns path with symlinks resolved as far as the filesystem
// allows, and cleaned otherwise.
//
// EvalSymlinks refuses a path whose leaf does not exist, which is ordinary here:
// a provider may report a transcript Sidecar has not seen yet. So the deepest
// existing ancestor is resolved and the remainder appended. That still closes
// the escape this exists to stop, because the escape has to go THROUGH a
// component that exists.
func resolvePath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved
	}
	// Walk up to the deepest ancestor that resolves, then re-append the rest.
	rest := ""
	dir := clean
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return clean
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
	}
}
