package tty

import (
	"strings"
)

// Terminal mode escape sequences
const (
	BracketedPasteEnable  = "\x1b[?2004h" // ESC[?2004h - app enables bracketed paste
	BracketedPasteDisable = "\x1b[?2004l" // ESC[?2004l - app disables bracketed paste
	BracketedPasteStart   = "\x1b[200~"   // ESC[200~ - start of pasted content
	BracketedPasteEnd     = "\x1b[201~"   // ESC[201~ - end of pasted content
)

// Whether the application in a pane has asked for mouse events is tmux's
// #{mouse_any_flag}, read with the capture (tty.ControlSnapshot.MouseReporting).
// It is deliberately not detected from captured text: capture-pane never
// carries the DECSET sequences that would announce it, so a text-based answer
// is silently wrong exactly when it matters.

// DetectBracketedPasteMode checks captured output to determine if the app has
// enabled bracketed paste mode. Looks for the most recent occurrence of either
// the enable (ESC[?2004h) or disable (ESC[?2004l) sequence.
func DetectBracketedPasteMode(output string) bool {
	enableIdx := strings.LastIndex(output, BracketedPasteEnable)
	disableIdx := strings.LastIndex(output, BracketedPasteDisable)
	// If enable was found more recently than disable, bracketed paste is enabled
	return enableIdx > disableIdx
}
