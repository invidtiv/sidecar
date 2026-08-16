package configchecks

import "strings"

// Sidecar's gradients, diffs, and themes are designed in 24-bit color. A
// 256-color terminal still works, but every theme is approximated, so this is a
// readiness item rather than a failure.

// TerminalGuide is what Sidecar can tell a user about their specific terminal.
// It is a small static table: Sidecar recognizes a terminal or it does not, and
// when it does not it offers the generic path rather than guessing.
type TerminalGuide struct {
	// Key is the stable identifier for the terminal, or "" for the generic guide.
	Key string
	// Name is what the user calls it.
	Name string
	// Steps are the specific instructions, in order.
	Steps []string
	// Capable marks a terminal that supports truecolor natively, so the fault
	// is almost always TERM or a tmux override rather than the emulator.
	Capable bool
}

// Instructions is the copyable form of a guide.
func (g TerminalGuide) Instructions() string {
	lines := make([]string, 0, len(g.Steps)+1)
	title := "Enable 24-bit color"
	if g.Name != "" {
		title += " in " + g.Name
	}
	lines = append(lines, title+":")
	for _, step := range g.Steps {
		lines = append(lines, "  - "+step)
	}
	return strings.Join(lines, "\n")
}

// genericGuide is the path for a terminal Sidecar does not recognize. It never
// tells the user to edit a file Sidecar cannot see the shape of.
var genericGuide = TerminalGuide{
	Name: "your terminal",
	Steps: []string{
		"Turn on True Color / 24-bit color in your terminal profile, then restart it.",
		"If the terminal already supports it, export COLORTERM=truecolor in your shell profile.",
		"Inside tmux, add: set -ga terminal-overrides ',*:Tc'  to ~/.tmux.conf and restart tmux.",
	},
}

// terminalGuides is the recognized-terminal table. Adding a terminal is a data
// change; nothing about the repair route changes with it.
var terminalGuides = []TerminalGuide{
	{
		Key: "iterm2", Name: "iTerm2", Capable: true,
		Steps: []string{
			"iTerm2 supports 24-bit color, so the report is usually TERM or tmux.",
			"Settings → Profiles → Terminal → Report Terminal Type: xterm-256color.",
			"Inside tmux, add: set -ga terminal-overrides ',*:Tc'  to ~/.tmux.conf.",
		},
	},
	{
		Key: "apple-terminal", Name: "Terminal.app",
		Steps: []string{
			"macOS Terminal.app does not support 24-bit color; its themes are 256-color.",
			"For full Sidecar color, use iTerm2, Ghostty, WezTerm, kitty, or Alacritty.",
			"Sidecar still works here — colors are approximated to the nearest 256.",
		},
	},
	{
		Key: "wezterm", Name: "WezTerm", Capable: true,
		Steps: []string{
			"WezTerm supports 24-bit color by default.",
			"Make sure nothing in your shell profile overrides COLORTERM.",
			"Inside tmux, add: set -ga terminal-overrides ',*:Tc'  to ~/.tmux.conf.",
		},
	},
	{
		Key: "ghostty", Name: "Ghostty", Capable: true,
		Steps: []string{
			"Ghostty supports 24-bit color by default.",
			"Make sure nothing in your shell profile overrides COLORTERM.",
			"Inside tmux, add: set -ga terminal-overrides ',*:Tc'  to ~/.tmux.conf.",
		},
	},
	{
		Key: "kitty", Name: "kitty", Capable: true,
		Steps: []string{
			"kitty supports 24-bit color by default (TERM=xterm-kitty).",
			"Make sure nothing in your shell profile overrides TERM or COLORTERM.",
			"Inside tmux, add: set -ga terminal-overrides ',*:Tc'  to ~/.tmux.conf.",
		},
	},
	{
		Key: "alacritty", Name: "Alacritty", Capable: true,
		Steps: []string{
			"Alacritty supports 24-bit color by default.",
			"Make sure nothing in your shell profile overrides TERM or COLORTERM.",
			"Inside tmux, add: set -ga terminal-overrides ',*:Tc'  to ~/.tmux.conf.",
		},
	},
	{
		Key: "vscode", Name: "the VS Code terminal", Capable: true,
		Steps: []string{
			"The VS Code integrated terminal supports 24-bit color.",
			"Check that terminal.integrated.env does not clear COLORTERM.",
		},
	},
}

// IdentifyTerminal names the terminal emulator Sidecar is running in, using
// TERM_PROGRAM first and the terminal-specific variables terminals set when it
// is absent. It returns the generic guide when nothing matches; false says the
// terminal was not recognized.
func IdentifyTerminal(env Env) (TerminalGuide, bool) {
	env = env.withDefaults()
	key := ""
	switch strings.ToLower(strings.TrimSpace(env.Getenv("TERM_PROGRAM"))) {
	case "iterm.app":
		key = "iterm2"
	case "apple_terminal":
		key = "apple-terminal"
	case "wezterm":
		key = "wezterm"
	case "ghostty":
		key = "ghostty"
	case "vscode":
		key = "vscode"
	}
	if key == "" {
		term := strings.ToLower(env.Getenv("TERM"))
		switch {
		case env.Getenv("KITTY_WINDOW_ID") != "" || strings.Contains(term, "kitty"):
			key = "kitty"
		case env.Getenv("ALACRITTY_SOCKET") != "" || env.Getenv("ALACRITTY_WINDOW_ID") != "" || strings.Contains(term, "alacritty"):
			key = "alacritty"
		case env.Getenv("WEZTERM_EXECUTABLE") != "":
			key = "wezterm"
		case env.Getenv("GHOSTTY_RESOURCES_DIR") != "":
			key = "ghostty"
		}
	}
	for _, guide := range terminalGuides {
		if guide.Key == key {
			return guide, true
		}
	}
	return genericGuide, false
}

// TerminalName is the display name for the detected terminal, or "" when
// Sidecar does not recognize it.
func TerminalName(env Env) string {
	guide, ok := IdentifyTerminal(env)
	if !ok {
		return ""
	}
	return guide.Name
}

// TruecolorEvidence is what the check observed, as displayable lines. Absent
// variables are shown as absent: "COLORTERM is not set" is the finding.
func TruecolorEvidence(env Env) []string {
	env = env.withDefaults()
	var lines []string
	for _, name := range []string{"COLORTERM", "TERM", "TERM_PROGRAM"} {
		if value := strings.TrimSpace(env.Getenv(name)); value != "" {
			lines = append(lines, name+"="+value)
		} else {
			lines = append(lines, name+" is not set")
		}
	}
	return lines
}

// TruecolorAvailable reports whether the terminal advertises 24-bit color.
func TruecolorAvailable(env Env) bool {
	env = env.withDefaults()
	switch strings.ToLower(strings.TrimSpace(env.Getenv("COLORTERM"))) {
	case "truecolor", "24bit":
		return true
	}
	term := strings.ToLower(env.Getenv("TERM"))
	if strings.Contains(term, "truecolor") || strings.Contains(term, "direct") {
		return true
	}
	// A recognized terminal that supports truecolor natively is trusted even
	// when it forgot to advertise it: kitty and Alacritty routinely do not set
	// COLORTERM, and telling that user to fix a working terminal is noise.
	if guide, ok := IdentifyTerminal(env); ok && guide.Capable {
		return true
	}
	return false
}

func checkTerminalColors(in Input) Result {
	env := in.env()
	evidence := TruecolorEvidence(env)
	if TruecolorAvailable(env) {
		summary := "Truecolor available"
		if name := TerminalName(env); name != "" {
			summary = "Truecolor available in " + name
		}
		return Result{
			ID: CheckTerminalColors, Title: "Terminal colors", OK: true,
			Summary: summary, Evidence: evidence,
		}
	}
	detail := "This terminal is not advertising 24-bit color"
	if name := TerminalName(env); name != "" {
		detail = name + " is not advertising 24-bit color"
	}
	return Result{
		ID:           CheckTerminalColors,
		Title:        "Terminal colors",
		OK:           false,
		Summary:      detail + " · themes will be approximated",
		Evidence:     evidence,
		Action:       "Check your terminal colors",
		ActionDetail: "Sidecar's themes are designed for 24-bit color",
		Badge:        BadgeFix,
		Repair:       RepairTerminalColors,
	}
}
