package manifest

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// DefaultDetectionRows is the row count the read window falls back to when the
// pane's own height is unknown. It is Herdr's DEFAULT_DETECTION_ROWS
// (src/pane/terminal.rs:41 at e2b85c7), and Sidecar's pre-manifest
// RegionCurrent used the same 24 by coincidence rather than by measurement.
const DefaultDetectionRows = 24

// Input is one observation as the engine sees it.
//
// Progress is always "" under tmux: tmux consumes OSC 9;4 and exposes no
// payload, so every osc_progress rule resolves to the empty string. The field
// exists so those rules still evaluate — to a recorded no-match with empty
// region evidence — rather than being silently dropped, which is the honest
// rendering of a permanent terminal gap.
type Input struct {
	// Agent is the agent the caller asked about, and it is what an Explain
	// reports as `agent`. It is Herdr's own `agent` parameter
	// (explain_with_input, manifest.rs:384, rendered by agent_label at :501),
	// and it is deliberately not the loaded manifest's id: those two coincide
	// for every vendored file and can differ under a local override, which may
	// declare one agent's id while carrying another's alias. Reporting the id
	// there would answer a question nobody asked, about a file rather than
	// about the pane. Empty on a path with no requested agent; nothing about
	// evaluation depends on it.
	Agent string
	// Screen is the raw capture. It may carry SGR escapes and scrollback; the
	// engine strips and bounds it itself, so callers pass what tmux gave them.
	Screen string
	// Title is tmux #{pane_title}, which is where Herdr's osc_title lands.
	Title string
	// Progress is Herdr's osc_progress. Always "" under tmux.
	Progress string
	// Rows is the pane's own row count (tmux #{pane_height}). Zero or negative
	// means unknown and selects DefaultDetectionRows.
	Rows int
}

// rows returns the effective read-window height.
func (in Input) rows() int {
	if in.Rows <= 0 {
		return DefaultDetectionRows
	}
	return in.Rows
}

// ReadWindow is the detection text: the exact string Herdr's engine calls
// `whole_recent`, and the string every other screen region is carved out of.
//
// Herdr reads the tail of the terminal buffer, anchored at the bottom
// regardless of where the viewport is scrolled, N rows deep where N is the
// pane's own row count (src/pane/terminal.rs:2801-2813 ghostty_recent_read_range,
// :2616-2623 for N, :41 for the 24 fallback). Rows are wrapped physical rows,
// each right-trimmed (ghostty_screen_row, :2841-2865); trailing blank rows are
// dropped after the window is selected and the window is not extended upward to
// compensate; interior blanks survive; rows join with "\n" plus one trailing
// "\n" (:2767-2768, :3358-3372). Measured against the 0.8.2 binary and recorded
// in docs/reference/herdr-detection-parity.md ("Read window"): a pane printing
// 2000 numbered lines in a 39-row pane returned lines 1963-2000.
//
// Sidecar's capture carries up to 600 lines of scrollback, so this bound is
// what keeps a resolved historical prompt from winning a rule.
func ReadWindow(screen string, rows int) string {
	if rows <= 0 {
		rows = DefaultDetectionRows
	}
	// The strip comes first so a row holding nothing but escape bytes counts as
	// the blank it renders as, at every step below.
	lines := strings.Split(ansi.Strip(screen), "\n")

	// One trailing "" goes before anything else, and it is not a row. A capture
	// is newline-*terminated*, so `strings.Split` always leaves an extra empty
	// piece after the last row; counting it as a grid row would spend a row of
	// the budget on nothing and drop the topmost visible row of every pane.
	//
	// That distinction is invisible on a synthetic screen and load-bearing on a
	// real one. `tmux capture-pane -p -e -N -S -600` pads the visible region out
	// to the full pane height, so the pane's blank cursor row arrives as a real
	// (empty) row of its own *and* the capture still ends with "\n". Dropping
	// exactly one piece keeps that row and discards the terminator: a 20-row
	// pane showing three lines of output then returns all three, where counting
	// the terminator returned only the last two.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Order matters here and it is the order Herdr uses, not the tidier one.
	// Select the last N rows *first*, then trim: a blank row inside the window
	// spends a row of the budget, and the window is never extended upward to
	// replace one. Trimming first and windowing after reaches further up the
	// buffer than the pane can show, which is how a resolved historical prompt
	// gets back into view.
	//
	// The blank cursor row a real pane carries is a row and does spend budget,
	// which is what reproduces the measurement in
	// docs/reference/herdr-detection-parity.md ("Read window"): a pane printing
	// 2000 numbered lines at 39 rows returns lines 1963-2000, 38 rows, because
	// the 39th is the cursor sitting blank below them.
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		// Herdr right-trims with Rust's trim_end(), which is Unicode
		// White_Space, not an ASCII pair (ghostty_screen_row, :2864).
		out[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	// Trailing blank rows go last, after the window is fixed and each row is
	// trimmed (terminal.rs:3357-3369). Interior blanks survive.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// splitLines ports Rust's str::lines(): split on '\n', strip one trailing '\r'
// from each piece, and drop the empty final piece a trailing '\n' produces.
//
// The '\r' handling is reproduced rather than tidied because the offset
// arithmetic in the region helpers below is Herdr's, and Herdr's assumes
// len(line)+1 bytes per line — which under-counts by one on a CRLF line. The
// detection text this engine evaluates never carries '\r', so the quirk is
// unreachable in practice; reproducing it keeps the port honest anyway.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, part := range parts {
		parts[i] = strings.TrimSuffix(part, "\r")
	}
	return parts
}
