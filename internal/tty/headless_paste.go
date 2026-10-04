package tty

// NormalizeHeadlessPaste removes every bracketed-paste start/end marker,
// including markers formed by earlier removals, and normalizes CRLF to LF
// until no CRLF remains. CR runs without a following LF remain unchanged.
// tmux supplies the only paste boundaries and performs its usual LF-to-CR
// translation. Raw input and ordinary interactive TUI paste do not use this
// client-paste policy. The input is never modified.
func NormalizeHeadlessPaste(data []byte) []byte {
	// Keep a marker-free stack. Removing a complete suffix leaves another
	// marker-free prefix; subsequent bytes can form a new suffix, which is
	// removed too. This reaches the fixed point in linear time, even for
	// deeply nested markers near the protocol's maximum input size.
	body := make([]byte, 0, len(data))
	for _, b := range data {
		body = append(body, b)
		n := len(body)
		if n >= 6 && body[n-6] == 0x1b && body[n-5] == '[' && body[n-4] == '2' && body[n-3] == '0' && (body[n-2] == '0' || body[n-2] == '1') && body[n-1] == '~' {
			body = body[:n-6]
		}
	}
	// Normalize after stripping so CRLF formed at a removed marker boundary
	// cannot become two Enter presses in tmux. Remove the entire CR suffix
	// before LF so repeated boundary normalization is idempotent.
	normalized := body[:0]
	for _, b := range body {
		if b == '\n' {
			for len(normalized) > 0 && normalized[len(normalized)-1] == '\r' {
				normalized = normalized[:len(normalized)-1]
			}
		}
		normalized = append(normalized, b)
	}
	return normalized
}
