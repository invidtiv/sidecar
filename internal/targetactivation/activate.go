// Package targetactivation is the state-free half of Sidecar's one jump
// service: it answers "what should happen when someone activates this target"
// without touching a model, a plugin, or the filesystem. The app shell owns
// execution (focusing plugins, switching projects); this package owns the
// decision, so a headless caller — a CLI action, a test, a future API — can
// adopt the same answer unchanged.
//
// # Safety rules for every consumer
//
// The scanner (internal/terminallink) deliberately does not sanitize; the
// surfaces do. Both halves of that discipline live here so a new consumer
// cannot miss them:
//
//  1. URL activation refuses anything terminallink.SafeHTTPURL rejects. Resolve
//     enforces it; no caller should open a URL it did not get back in a Plan.
//  2. Any surface that renders untrusted text through terminallink.Decorate
//     must call terminallink.StripOSC8 on the line first. Otherwise a hostile
//     OSC 8 hyperlink already embedded in the text survives decoration and the
//     rendered link no longer means what the scanned span says it means.
//     Activation cannot defend against this — it never sees the raw line — so
//     the rule belongs at every render site that feeds this service.
//
// File targets are additionally constrained to workspace-relative paths: an
// absolute path or one that climbs out of the workspace with ".." is refused
// rather than resolved, because the canonical file message
// (app.NavigateToFileMsg) is defined relative to the project's workdir.
package targetactivation

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/uirequest"
)

// FileBrowserPluginID is the plugin that owns file targets.
const FileBrowserPluginID = "file-browser"

// PlanKind names the executable shape of a resolved activation.
type PlanKind string

const (
	// PlanOpenFile focuses PluginID and asks it to reveal Path (Line optional).
	PlanOpenFile PlanKind = "open-file"
	// PlanOpenURL opens an already-validated http(s) URL in the browser.
	PlanOpenURL PlanKind = "open-url"
)

// Plan is what the shell executes. It is data, not commands: the shell turns
// it into tea.Cmds, and a headless caller can act on the same fields.
type Plan struct {
	Kind     PlanKind
	PluginID string
	Path     string
	Line     int
	URL      string
}

// ErrUnsupportedKind reports a target kind this service does not route yet.
// Callers migrating a surface one kind at a time can keep their existing branch
// for these rather than treating them as malformed.
var ErrUnsupportedKind = errors.New("target kind is not activatable yet")

// Resolve decides how a target is activated. It touches nothing: no model, no
// filesystem, no network. Errors are user-facing refusals ("why nothing
// happened"), never diagnostics.
func Resolve(target uirequest.Target) (Plan, error) {
	value := strings.TrimSpace(target.Value)
	switch target.Kind {
	case uirequest.TargetKindFile:
		return resolveFile(value, target.Line)
	case uirequest.TargetKindURL:
		safe, ok := terminallink.SafeHTTPURL(value)
		if !ok {
			return Plan{}, fmt.Errorf("refusing to open %q: only http and https links are opened", value)
		}
		return Plan{Kind: PlanOpenURL, URL: safe}, nil
	case "":
		return Plan{}, errors.New("target has no kind")
	default:
		return Plan{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, target.Kind)
	}
}

func resolveFile(value string, line int) (Plan, error) {
	if value == "" {
		return Plan{}, errors.New("file target has no path")
	}
	if strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return Plan{}, errors.New("file path contains control characters")
	}
	if line < 0 {
		return Plan{}, fmt.Errorf("file target has a negative line (%d)", line)
	}
	slashed := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(slashed, "/") || strings.HasPrefix(slashed, "~") {
		return Plan{}, fmt.Errorf("refusing to open %q: file targets are relative to the project", value)
	}
	clean := path.Clean(slashed)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return Plan{}, fmt.Errorf("refusing to open %q: file targets cannot leave the project", value)
	}
	return Plan{Kind: PlanOpenFile, PluginID: FileBrowserPluginID, Path: clean, Line: line}, nil
}
