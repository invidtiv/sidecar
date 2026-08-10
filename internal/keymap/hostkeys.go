package keymap

// This file names the two key sets sidecar's key ladder is built on. They live
// here, not in internal/app, because both sides of the ladder need them: the
// host enforces them, and a plugin that routes its own keys has to reason about
// them. internal/app imports the plugin packages, so the plugin packages cannot
// import internal/app.

// HostReservedKeys are keys no plugin may take from sidecar's global handling,
// whatever its key router claims.
//
// These are the host's non-negotiables. `ctrl+c` and `q` are the only ways out
// of sidecar, and an embedded plugin must never be able to swallow the exit.
// `?` opens the merged contextual help, which is the surface that lists every
// plugin command — including the ones the plugin would rather bind `?` to.
//
// A plugin still gets these keys where it genuinely owns the keyboard: an
// overlay or text-input context (precedence level 2) is forwarded everything
// except ctrl+c, and that runs before any claim is consulted.
var HostReservedKeys = map[string]bool{
	"ctrl+c": true,
	"q":      true,
	"?":      true,
}

// GlobalKeys are the keys sidecar's own root key handler acts on while an
// ordinary plugin tab is focused: tab cycling and direct tab selection, the
// palette, diagnostics, the project/worktree/theme switchers, Open In, the
// issue input, and the quit flow.
//
// It exists so a plugin can tell "this key collides with sidecar" from "this
// key is mine alone" without duplicating the host's switch statement, and so
// that collision policy can be stated as a rule instead of a list of accidents.
//
// `r` is deliberately absent: sidecar's refresh yields to the plugin in any
// context isGlobalRefreshContext does not name, which is every plugin context
// that binds `r` itself.
//
// internal/app's TestGlobalKeysAreTheOnesTheHostActuallyHandles pins this
// against the real handler, so it cannot drift from the switch it describes.
var GlobalKeys = map[string]bool{
	"`": true, "~": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true,
	"7": true, "8": true, "9": true,
	"?": true, "!": true, "@": true, "K": true, "W": true, "#": true,
	"^": true, "i": true, "q": true, "ctrl+c": true,
}
