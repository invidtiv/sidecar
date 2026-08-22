package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// StripRowBackgrounds rewrites a captured row so no cell it draws carries an
// explicit background. Every background-setting SGR parameter is replaced with
// a reset to the terminal default (49); everything else the row styled —
// foreground colors, bold, underline, hyperlinks — survives untouched.
//
// This is the enforcement behind tty.BackgroundBounded and tty.BackgroundNever:
// rendering decides *how far* a carried background may reach; this decides what
// "not that far" looks like on the wire. A compound sequence keeps its
// non-background parameters, so `ESC[1;41m` degrades to bold-on-default rather
// than losing the bold.
func StripRowBackgrounds(line string) string {
	if !strings.Contains(line, "\x1b[") {
		return line
	}
	var out strings.Builder
	out.Grow(len(line))

	state := ansi.NormalState
	remaining := line
	for len(remaining) > 0 {
		seq, _, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			out.WriteString(remaining)
			break
		}
		out.WriteString(stripSequenceBackgrounds(seq))
		state = newState
		remaining = remaining[n:]
	}
	return out.String()
}

// stripSequenceBackgrounds neutralizes one escape sequence's background
// parameters. Non-SGR sequences cannot set a background and pass through; SGR
// sequences are rebuilt from their parameters with every background token
// turned into 49.
func stripSequenceBackgrounds(seq string) string {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return seq
	}
	params := strings.Split(seq[2:len(seq)-1], ";")
	kept := make([]string, 0, len(params))
	touched := false

	for i := 0; i < len(params); i++ {
		param := params[i]
		// Colon-subparameter colors (48:2:…) arrive as one parameter.
		if base, _, ok := strings.Cut(param, ":"); ok {
			switch base {
			case "48":
				kept = append(kept, "49")
				touched = true
			default:
				kept = append(kept, param)
			}
			continue
		}
		switch param {
		case "", "0", "49":
			// Resets already land on the terminal default — exactly where
			// stripping wants the background — so they pass through as-is.
			kept = append(kept, param)
		case "38", "48", "58":
			value, consumed := sgrColorParam(params, i)
			i += consumed
			if param == "48" {
				kept = append(kept, "49")
				touched = true
			} else {
				// A foreground or underline color consumes its own arguments;
				// keep the whole token so later parameters stay aligned.
				kept = append(kept, value)
			}
		default:
			if code, err := strconv.Atoi(param); err == nil &&
				(code >= 40 && code <= 47 || code >= 100 && code <= 107) {
				kept = append(kept, "49")
				touched = true
			} else {
				kept = append(kept, param)
			}
		}
	}
	if !touched {
		return seq
	}
	return "\x1b[" + strings.Join(kept, ";") + "m"
}
