package termnotify

import (
	"fmt"
	"sort"
	"strings"
)

// Detection is the answer to "which terminal is on the other end of this
// connection". Reason is honest status text for Configuration and the CLI when
// nothing was identified: this transport reports unavailable rather than
// guessing, because a guess sends the wrong sequence to a real terminal.
type Detection struct {
	Terminal Terminal
	// Marker names the environment variable that identified the terminal, so
	// status can explain the answer instead of asserting it.
	Marker string
	Reason string
}

// OK reports whether a supported terminal was identified.
func (d Detection) OK() bool { return d.Terminal != "" }

// overrideVar lets a user, a test, or an isolated proof run state the outer
// terminal directly. It is checked first and on its own, so it is the one
// marker that cannot be contradicted. HostingTerminalBundle in
// internal/notifydelivery reads the same variable for the same reason.
const overrideVar = "SIDECAR_TERM_PROGRAM"

// marker is one environment fact that identifies a terminal. A value of "" is
// a presence test: the variable existing at all is the evidence.
type marker struct {
	terminal Terminal
	name     string
	value    string
}

// markers is every environment fact Sidecar trusts. It is a fixed list on
// purpose — auto-detection may only recognise terminals, never infer them.
//
// TERM is worth as much as it is here because ssh forwards it and tmux does
// not: `xterm-ghostty` and `xterm-kitty` are usually the only markers that
// survive an SSH hop, and none of them survive tmux, which replaces TERM with
// its own and (since 3.2) sets TERM_PROGRAM=tmux over whatever was there. That
// is the concrete reason `auto` has to be allowed to fail and an explicit
// terminal choice has to exist.
var markers = []marker{
	{Ghostty, "TERM_PROGRAM", "ghostty"},
	{Ghostty, "TERM", "xterm-ghostty"},
	{Ghostty, "GHOSTTY_RESOURCES_DIR", ""},
	{Ghostty, "GHOSTTY_BIN_DIR", ""},

	{ITerm2, "TERM_PROGRAM", "iterm.app"},
	// LC_TERMINAL is iTerm2's own SSH-forwarded marker: it ships in the LC_*
	// namespace precisely because sshd's default AcceptEnv passes it through.
	{ITerm2, "LC_TERMINAL", "iterm2"},
	{ITerm2, "ITERM_SESSION_ID", ""},

	{WezTerm, "TERM_PROGRAM", "wezterm"},
	{WezTerm, "TERM", "wezterm"},
	{WezTerm, "WEZTERM_PANE", ""},
	{WezTerm, "WEZTERM_EXECUTABLE", ""},
	{WezTerm, "WEZTERM_UNIX_SOCKET", ""},

	{Kitty, "TERM", "xterm-kitty"},
	{Kitty, "TERM_PROGRAM", "kitty"},
	{Kitty, "KITTY_WINDOW_ID", ""},
	{Kitty, "KITTY_PID", ""},
}

// Detect identifies the outer terminal from known environment markers only.
//
// Every marker is evaluated rather than the first match winning. Two different
// terminals claiming one process is ambiguous — a stale KITTY_WINDOW_ID
// inherited into a Ghostty window, say — and choosing between them would send
// one terminal's private sequence to another. Ambiguity is reported as
// unavailable, which the user resolves by naming a terminal explicitly.
func Detect(getenv func(string) string) Detection {
	if getenv == nil {
		return Detection{Reason: "no environment to inspect"}
	}
	if raw := strings.ToLower(strings.TrimSpace(getenv(overrideVar))); raw != "" {
		if term, ok := ParseTerminal(raw); ok {
			return Detection{Terminal: term, Marker: overrideVar}
		}
		return Detection{Reason: fmt.Sprintf("%s=%q is not a terminal with a Sidecar encoder", overrideVar, raw)}
	}

	found := map[Terminal]string{}
	for _, m := range markers {
		raw := strings.TrimSpace(getenv(m.name))
		if raw == "" {
			continue
		}
		if m.value != "" && !strings.EqualFold(raw, m.value) {
			continue
		}
		if _, seen := found[m.terminal]; !seen {
			found[m.terminal] = m.name
		}
	}
	switch len(found) {
	case 0:
		return Detection{Reason: "no supported terminal was identified; SSH commonly drops TERM_PROGRAM, so choose a terminal explicitly"}
	case 1:
		for term, name := range found {
			return Detection{Terminal: term, Marker: name}
		}
	}
	return Detection{Reason: fmt.Sprintf("the environment claims %s at once; choose a terminal explicitly", conflictText(found))}
}

func conflictText(found map[Terminal]string) string {
	parts := make([]string, 0, len(found))
	for term, name := range found {
		parts = append(parts, fmt.Sprintf("%s (%s)", term, name))
	}
	sort.Strings(parts)
	return strings.Join(parts, " and ")
}

// InsideTmux reports whether this process is running in a tmux client, which is
// the one fact that changes how a sequence has to be framed.
func InsideTmux(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	return strings.TrimSpace(getenv("TMUX")) != ""
}
