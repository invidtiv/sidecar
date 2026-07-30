// Package termtitle formats and emits the terminal window/tab title.
//
// Two titles carry a name, and terminals disagree about which one labels a tab:
// most (Ghostty, WezTerm, kitty, Alacritty, Terminal.app) follow the window
// title, while iTerm2 labels tabs from the icon name. [Set] writes OSC 0, which
// sets both at once.
//
// Sidecar deliberately does not route this through
// [charm.land/bubbletea/v2.View.WindowTitle]. Bubble Tea clears a title it
// owns whenever the renderer stops — including for the length of every
// tea.ExecProcess child — which would blank the terminal title for as long as
// the user stays attached to a session. Owning the sequence here means the
// title simply persists across those handoffs.
package termtitle

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// maxLen caps the rendered title. Terminals truncate tab labels far below this
// anyway; the cap exists so a pathological branch or directory name can't push
// a multi-kilobyte escape sequence at the terminal on every project switch.
const maxLen = 120

// zeroWidthJoiner is U+200D, the one Cf character worth keeping — emoji
// sequences are built from it, and a project name may contain one.
const zeroWidthJoiner = '‍'

// Vars holds the substitutions available to a title template.
type Vars struct {
	Project  string // repo name, e.g. "sidecar"
	Worktree string // branch of a linked worktree, empty on the main worktree
	Plugin   string // active plugin/tab name, e.g. "workspaces"
	Dir      string // base name of the working directory
}

// Render expands tmpl and sanitizes the result. An empty tmpl renders an empty
// title, which is how the feature is turned off.
//
// {worktree} expands with its own leading space and brackets (" [branch]") so
// that the default template, "{project}{worktree}", collapses to just the
// project name when there is no linked worktree.
func Render(tmpl string, v Vars) string {
	if tmpl == "" {
		return ""
	}
	worktree := ""
	if v.Worktree != "" {
		worktree = " [" + v.Worktree + "]"
	}
	return sanitize(strings.NewReplacer(
		"{project}", v.Project,
		"{worktree}", worktree,
		"{plugin}", v.Plugin,
		"{dir}", v.Dir,
	).Replace(tmpl))
}

// sanitize strips anything that can't safely ride inside an OSC sequence and
// tidies the whitespace left behind by empty substitutions.
//
// Branch names, directory names and repo names are attacker-influenced in the
// sense that they come from outside sidecar: an ESC or BEL in one of them would
// terminate the title sequence early and leave the remainder to be interpreted
// as terminal commands.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			// Collapse any run of whitespace (including the newlines and tabs
			// that would otherwise be dropped as control characters) to one
			// space, deferred so trailing runs vanish.
			space = b.Len() > 0
		case unicode.IsControl(r):
			// Drop: ESC, BEL, DEL and the rest of C0/C1.
		case unicode.Is(unicode.Cf, r) && r != zeroWidthJoiner:
			// Drop format characters — a branch named "main‮gnp.exe"
			// would otherwise render a visually reversed tab title. The zero
			// width joiner is spared because emoji sequences are built from it.
		default:
			if space {
				b.WriteRune(' ')
				space = false
			}
			b.WriteRune(r)
		}
	}
	out := b.String()
	if runes := []rune(out); len(runes) > maxLen {
		out = strings.TrimRight(string(runes[:maxLen]), " ")
	}
	return out
}

// Set returns the sequence naming the terminal (OSC 0), which sets the icon
// name and the window title together — between them they cover the tab label of
// every terminal sidecar targets.
func Set(s string) string { return ansi.SetIconNameWindowTitle(s) }

// Save pushes the current titles onto the terminal's title stack, and Restore
// pops them back.
//
// The parameter is 0 — icon name *and* window title, matching what [Set]
// writes — rather than 2, which would cover only the window title and leave the
// iTerm2 tab label pointing at the wrong project after exit. Terminals that
// don't implement the XTWINOPS title stack ignore both, and keep the last title
// sidecar set.
func Save() string { return ansi.WindowOp(22, 0) }

// Restore pops the titles saved by [Save].
func Restore() string { return ansi.WindowOp(23, 0) }
