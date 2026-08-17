package tty

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The editor is launched through the user's login + interactive shell so the
// profile runs: PATH additions, functions, and the profile's own EDITOR all
// apply, which is what makes a custom vim/nvim setup behave here the way it
// does in a terminal. The cost is one profile load per editor open (~235ms on
// a plain zsh; more on a machine with an endpoint security agent). That is
// deliberately confined to a keypress the user already experiences as a
// context switch — nothing on this path may run during Init()/Start().
//
// The file path is never spliced into shell text. It travels as a positional
// parameter, so a name containing spaces, quotes, `$`, or backticks is data
// the shell expands exactly once and never re-parses.

const (
	// posixExecArgs runs the argv it was handed and nothing else.
	posixExecArgs = `exec "$@"`

	// posixResolveEditor lets the profile name the editor. It is used only
	// when Sidecar's own environment named none, so an explicit choice is
	// never overridden.
	posixResolveEditor = `exec "${EDITOR:-${VISUAL:-vim}}" "$@"`

	// fish has no "$@"; its positional parameters are $argv, and variable
	// expansion there does not word-split, so the path stays one argument.
	fishExecArgs = `exec $argv`

	fishResolveEditor = `test -n "$EDITOR"; and exec $EDITOR $argv
test -n "$VISUAL"; and exec $VISUAL $argv
exec vim $argv`
)

// shellArg0 is the $0 the wrapper script runs under. It appears only in shell
// diagnostics, so it names Sidecar rather than the editor.
const shellArg0 = "sidecar-editor"

type shellFamily int

const (
	shellNone shellFamily = iota
	shellPOSIX
	shellFish
)

// EditorArgv returns the argv that opens path in editor, at a one-based line
// when line > 0. The second result reports whether the user's shell profile is
// in the picture; when it is false the argv is a direct exec of the editor,
// which is also the fallback whenever no usable login shell can be identified.
func EditorArgv(editor string, line int, path string) ([]string, bool) {
	tail := editorTailArgs(line, path)
	direct := append([]string{editor}, tail...)

	shell, family := profileShell()
	if family == shellNone {
		return direct, false
	}
	script, args := profileScript(family, editor, tail)
	argv := make([]string, 0, len(args)+5)
	argv = append(argv, shell, "-l", "-i", "-c", script)
	return append(argv, args...), true
}

// DirectEditorArgv is the no-profile launch of the same editor. It is the
// fallback a caller runs when the shell route failed to start at all.
func DirectEditorArgv(editor string, line int, path string) []string {
	return append([]string{editor}, editorTailArgs(line, path)...)
}

func editorTailArgs(line int, path string) []string {
	if line > 0 {
		return []string{fmt.Sprintf("+%d", line), path}
	}
	return []string{path}
}

// profileShell identifies the user's login shell and the script dialect it
// speaks. A shell that is unset, missing, or not one whose -l/-i/-c semantics
// are known reports shellNone, and the caller execs the editor directly rather
// than guessing at flags.
func profileShell() (string, shellFamily) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		return "", shellNone
	}
	family := shellFamilyFor(shell)
	if family == shellNone {
		return "", shellNone
	}
	resolved, err := exec.LookPath(shell)
	if err != nil {
		return "", shellNone
	}
	return resolved, family
}

func shellFamilyFor(shell string) shellFamily {
	switch strings.TrimSuffix(filepath.Base(shell), ".exe") {
	case "zsh", "bash", "ksh", "ksh93", "mksh", "pdksh", "dash", "ash", "sh":
		return shellPOSIX
	case "fish":
		return shellFish
	}
	// csh/tcsh reject a combined login+interactive -c, and nologin/false are
	// not shells at all. Both fall back to the direct exec.
	return shellNone
}

// profileScript pairs the wrapper script with the operands it reads. The
// editor is an operand, never part of the script, except in the one case where
// the script is asked to resolve it from the profile.
func profileScript(family shellFamily, editor string, tail []string) (string, []string) {
	resolve := editorUnsetInEnvironment() && editor == fallbackEditor
	switch family {
	case shellFish:
		if resolve {
			return fishResolveEditor, append([]string{}, tail...)
		}
		return fishExecArgs, append([]string{editor}, tail...)
	default:
		if resolve {
			return posixResolveEditor, append([]string{shellArg0}, tail...)
		}
		return posixExecArgs, append([]string{shellArg0, editor}, tail...)
	}
}

// editorUnsetInEnvironment reports that nothing in Sidecar's own environment
// named an editor, so the value in hand is the built-in fallback and the
// profile is free to do better.
func editorUnsetInEnvironment() bool {
	return os.Getenv("EDITOR") == "" && os.Getenv("VISUAL") == ""
}
