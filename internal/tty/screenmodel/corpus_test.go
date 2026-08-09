package screenmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// corpusStep is one action in a fixture: either a chunk of raw bytes emitted
// into the pane, or a pane resize.
type corpusStep struct {
	Write   string
	ResizeW int
	ResizeH int
}

func write(s string) corpusStep     { return corpusStep{Write: s} }
func resize(w, h int) corpusStep    { return corpusStep{ResizeW: w, ResizeH: h} }
func (s corpusStep) isResize() bool { return s.ResizeW > 0 && s.ResizeH > 0 }

// corpusEntry is one deterministic byte fixture.
type corpusEntry struct {
	Name string
	// Categories are the plan's "Deterministic byte corpus" bullets this entry
	// covers. The coverage test asserts every declared bullet has an owner.
	Categories []string
	Width      int
	Height     int
	Steps      []corpusStep

	// KnownGaps are the mismatch signatures this fixture is *expected* to
	// produce because of a documented emulator defect. The runner asserts the
	// observed signature set equals this exactly, so both a regression and an
	// upstream fix fail the test and force the evidence doc to be updated.
	KnownGaps []string
	// KnownGapReason is mandatory whenever KnownGaps is non-empty. It names the
	// defect and points at the slice 0 evidence document.
	KnownGapReason string

	// KnownSeedGaps are the mismatch signatures the seed round trip is expected
	// to produce, and KnownSeedGapReason explains them.
	KnownSeedGaps      []string
	KnownSeedGapReason string

	// KnownOutputGaps are the mismatch signatures re-seeding from the frame's own
	// [Frame.Output] is expected to produce, and KnownOutputGapReason explains
	// them. Empty means Frame.Output is a fixed point: rendering the model and
	// parsing the rendering back gives the same canonical cells.
	KnownOutputGaps      []string
	KnownOutputGapReason string

	// KnownSplitGaps are the mismatch signatures a split replay is expected to
	// produce relative to the single-write replay, and KnownSplitGapReason
	// explains them. Empty means splitting must be invisible.
	KnownSplitGaps      []string
	KnownSplitGapReason string

	// SkipHistoryAssert suppresses the absolute history-size comparison for
	// fixtures where tmux and the model legitimately disagree.
	SkipHistoryAssert bool
	// SkipHistoryReason must be set whenever SkipHistoryAssert is.
	SkipHistoryReason string

	// SkipCursorAssert marks fixtures where tmux and the model legitimately
	// disagree about the cursor for a documented reason.
	SkipCursorAssert bool
	// SkipCursorReason must be set whenever SkipCursorAssert is.
	SkipCursorReason string
}

// fingerprint identifies the fixture's input. A recorded oracle whose
// fingerprint no longer matches the Go corpus is stale and fails the run
// rather than silently comparing against the wrong expectation.
func (e corpusEntry) fingerprint() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d|", e.Name, e.Width, e.Height)
	for _, s := range e.Steps {
		fmt.Fprintf(h, "%q;%d;%d|", s.Write, s.ResizeW, s.ResizeH)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// planCategories mirrors the "Deterministic byte corpus" bullet list in
// docs/plans/active/td-64c916-byte-fed-tmux-screen-model.md. Anything listed
// here that is out of scope for slice 0 carries an explicit reason.
var planCategories = map[string]string{
	"crlf-tab-backspace-wrap-phantom": "",
	"cursor-motion-save-restore":      "",
	"erase-insert-delete":             "",
	"scroll-regions-origin":           "",
	"alt-screen":                      "",
	"sgr-colors-attrs":                "",
	"osc8-links":                      "",
	"unicode-graphemes":               "",
	"modes-cursor-paste-sync-reset":   "",
	"device-queries":                  "",
	"resize":                          "",
	"control-transport": "slice 1: octal payload escapes, long notifications, " +
		"pause/continue and dead control connections are properties of the " +
		"control protocol, not of the screen model. There is no pane byte " +
		"stream to record for them here.",
}

// esc builds an escape sequence.
func csi(s string) string { return "\x1b[" + s }

// repeatLines produces n numbered lines, used to push history.
func repeatLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %02d\r\n", i)
	}
	return b.String()
}

// corpus is the deterministic byte corpus.
//
// Every fixture is generated: nothing here comes from a real session, a real
// path, or any personal data.
var corpus = []corpusEntry{
	{
		Name:       "controls_crlf_tab_backspace",
		Categories: []string{"crlf-tab-backspace-wrap-phantom"},
		Width:      40, Height: 8,
		Steps: []corpusStep{
			write("ab\tcd\r\n"),
			write("xyz\b\bQ\r\n"),
			// Bare LF with output post-processing disabled keeps the column.
			write("indent\nkeeps column\r\n"),
			write("A\tB\tC\tD\tE\r\n"),
			// Backspace at column 0 must not wrap backwards.
			write("\b\b\bzero\r\n"),
		},
	},
	{
		Name:       "autowrap_phantom_column",
		Categories: []string{"crlf-tab-backspace-wrap-phantom"},
		Width:      10, Height: 6,
		Steps: []corpusStep{
			// Exactly fills row 0; the cursor parks in the phantom column.
			write("0123456789"),
			// The next printable wraps to row 1.
			write("A"),
			write("\r\n"),
			// Autowrap off: the last column is overwritten in place.
			write(csi("?7l") + "0123456789XY" + csi("?7h")),
			write("\r\n"),
			// A control character in the phantom column must not wrap.
			write("abcdefghij\r"),
			write("Z"),
		},
	},
	{
		Name:       "cursor_motion",
		Categories: []string{"cursor-motion-save-restore"},
		Width:      20, Height: 8,
		Steps: []corpusStep{
			write(csi("5;5H") + "CUP"),
			write(csi("2A") + "up"),
			write(csi("3B") + "down"),
			write(csi("4C") + "right"),
			write(csi("2D") + "left"),
			write(csi("8G") + "CHA"),
			write(csi("2d") + "VPA"),
			write(csi("2E") + "CNL"),
			write(csi("1F") + "CPL"),
			write(csi("7;2f") + "HVP"),
			// Clamping: motion past the edges saturates.
			write(csi("99;99H") + "X"),
			write(csi("1;1H") + csi("9A") + csi("9D") + "O"),
		},
	},
	{
		Name:       "save_restore_cursor_and_attrs",
		Categories: []string{"cursor-motion-save-restore"},
		Width:      24, Height: 6,
		Steps: []corpusStep{
			write(csi("3;3H") + csi("1;31m") + "bold-red"),
			write("\x1b7"), // DECSC saves position and pen
			write(csi("5;1H") + csi("0m") + "plain"),
			write("\x1b8" + "restored"), // DECRC restores both
			write(csi("6;1H") + csi("s") + csi("1;10H") + "moved" + csi("u") + "back"),
		},
		KnownGaps: []string{"cell/attrs", "cell/fg", "cell/grapheme", "cursor/position"},
		KnownGapReason: "GAP-1 in the slice 0 evidence: x/vt registers a CSI 's' " +
			"(SCOSC) handler but no CSI 'u' (SCORC) handler, so the restore is a " +
			"no-op and everything written after it lands at the wrong place.",
	},
	{
		Name:       "erase_insert_delete",
		Categories: []string{"erase-insert-delete"},
		Width:      20, Height: 8,
		Steps: []corpusStep{
			write("AAAAAAAAAAAAAAAAAAAA\r\n"),
			write("BBBBBBBBBBBBBBBBBBBB\r\n"),
			write("CCCCCCCCCCCCCCCCCCCC\r\n"),
			write("DDDDDDDDDDDDDDDDDDDD\r\n"),
			write(csi("1;5H") + csi("K")),   // EL 0
			write(csi("2;10H") + csi("1K")), // EL 1
			write(csi("3;1H") + csi("2K")),  // EL 2
			write(csi("4;5H") + csi("3X")),  // ECH
			write(csi("4;1H") + csi("2@")),  // ICH
			write(csi("4;8H") + csi("3P")),  // DCH
			write(csi("5;1H") + "tail" + csi("6;1H") + "more"),
			write(csi("5;3H") + csi("0J")), // ED 0
		},
	},
	{
		Name:       "insert_delete_lines",
		Categories: []string{"erase-insert-delete"},
		Width:      16, Height: 8,
		Steps: []corpusStep{
			write("1111\r\n2222\r\n3333\r\n4444\r\n5555\r\n"),
			write(csi("2;1H") + csi("2L")), // IL 2
			write(csi("5;1H") + csi("1M")), // DL 1
			write(csi("1;1H") + csi("1L") + "top"),
		},
	},
	{
		Name:       "scroll_region_and_origin",
		Categories: []string{"scroll-regions-origin"},
		Width:      16, Height: 8,
		Steps: []corpusStep{
			write("a\r\nb\r\nc\r\nd\r\ne\r\nf\r\ng\r\nh"),
			write(csi("3;6r")),             // DECSTBM rows 3..6
			write(csi("6;1H") + "\n\n"),    // scroll inside the region
			write(csi("3;1H") + csi("2S")), // SU inside the region
			write(csi("4;1H") + csi("1T")), // SD inside the region
			write(csi("?6h")),              // DECOM: origin is the region top
			write(csi("1;1H") + "ORIGIN"),
			write(csi("?6l") + csi("r")), // reset origin and margins
			write(csi("8;1H") + "END"),
		},
	},
	{
		Name:       "alt_screen_transitions",
		Categories: []string{"alt-screen"},
		Width:      20, Height: 6,
		Steps: []corpusStep{
			write("main one\r\nmain two\r\n"),
			write(csi("?1049h")),
			write("alt content" + csi("2;1H") + "alt line 2"),
			write(csi("?1049l")),
			write("back on main\r\n"),
			write(csi("?1049h") + "second alt"),
			write(csi("?1049l") + "final main"),
		},
	},
	{
		// Captured *while still on the alternate screen*, which is where every
		// full-screen TUI lives. alt_screen_transitions ends back on the main
		// screen, so it can only prove the exit path; without this fixture the
		// alternate screen's cell content, its cursor, and the alternate_on mode
		// assertion are never compared against tmux in a true state, and the seed
		// round trip never reconstructs an alt-screen pane.
		Name:       "alt_screen_active",
		Categories: []string{"alt-screen"},
		Width:      24, Height: 6,
		Steps: []corpusStep{
			// Main-screen content and history first: entering the alternate
			// screen must not disturb either.
			write("main one\r\nmain two\r\nmain three\r\n"),
			write(csi("?1049h")),
			// A TUI's opening moves: clear, hide the cursor, take the mouse.
			write(csi("2J") + csi("?25l") + csi("?1000h") + csi("?1006h")),
			// A reverse-video status bar filling the full pane width.
			write(csi("1;1H") + csi("7m") + " STATUS BAR            " + csi("0m")),
			write(csi("2;1H") + csi("34m") + "blue row" + csi("39m") + " plain"),
			write(csi("3;1H") + "日本語 wide"),
			write(csi("4;1H") + csi("4m") + "underlined" + csi("24m")),
			write(csi("5;1H") + csi("38;5;208m") + "idx208" + csi("48;2;10;20;30m") + "rgbbg" + csi("0m")),
			// Park the cursor mid-screen and stay on the alternate screen: no
			// ?1049l, so the recorder captures the alternate buffer.
			write(csi("6;7H")),
		},
	},
	{
		Name:       "sgr_basic_and_bright",
		Categories: []string{"sgr-colors-attrs"},
		Width:      40, Height: 6,
		Steps: []corpusStep{
			write(csi("31m") + "red" + csi("32m") + "green" + csi("0m") + "plain\r\n"),
			write(csi("44m") + "onblue" + csi("49m") + "default-bg\r\n"),
			write(csi("91m") + "bright-red" + csi("107m") + "bright-bg" + csi("0m") + "\r\n"),
			write(csi("30;47m") + "inverted-ish" + csi("m") + "reset-empty-param\r\n"),
			// A styled line followed by a plain one proves the capture does not
			// bleed pen state between lines.
			write("plain after color\r\n"),
		},
	},
	{
		Name:       "sgr_256_and_truecolor",
		Categories: []string{"sgr-colors-attrs"},
		Width:      40, Height: 6,
		Steps: []corpusStep{
			write(csi("38;5;196m") + "idx196" + csi("48;5;21m") + "bg21" + csi("0m") + "\r\n"),
			write(csi("38;2;18;52;86m") + "rgbfg" + csi("48;2;120;200;40m") + "rgbbg" + csi("0m") + "\r\n"),
			write(csi("38;5;3m") + "idx3-equals-sgr33" + csi("0m") + "\r\n"),
			write(csi("33m") + "sgr33" + csi("0m") + "\r\n"),
		},
	},
	{
		Name:       "sgr_underline_styles",
		Categories: []string{"sgr-colors-attrs"},
		Width:      40, Height: 8,
		Steps: []corpusStep{
			write(csi("4m") + "single" + csi("24m") + " off\r\n"),
			write(csi("21m") + "double" + csi("24m") + "\r\n"),
			write(csi("4:3m") + "curly" + csi("4:0m") + " off\r\n"),
			write(csi("4:4m") + "dotted" + csi("4:5m") + "dashed" + csi("24m") + "\r\n"),
			write(csi("4m") + csi("58;5;208m") + "ul-colored" + csi("59m") + "ul-default" + csi("0m") + "\r\n"),
			write(csi("4m") + csi("58;2;10;20;30m") + "ul-rgb" + csi("0m") + "\r\n"),
		},
		KnownGaps: []string{"cell/underline"},
		KnownGapReason: "GAP-2 in the slice 0 evidence: ultraviolet's ReadStyle has no " +
			"case for SGR 21, so double underline written as 21 is dropped. tmux " +
			"renders it as 4:2.",
	},
	{
		Name:       "sgr_attributes",
		Categories: []string{"sgr-colors-attrs"},
		Width:      40, Height: 8,
		Steps: []corpusStep{
			write(csi("1m") + "bold" + csi("22m") + " off\r\n"),
			write(csi("2m") + "dim" + csi("22m") + " off\r\n"),
			write(csi("3m") + "italic" + csi("23m") + " off\r\n"),
			write(csi("7m") + "inverse" + csi("27m") + " off\r\n"),
			write(csi("8m") + "hidden" + csi("28m") + " off\r\n"),
			write(csi("9m") + "strike" + csi("29m") + " off\r\n"),
			write(csi("5m") + "blink" + csi("25m") + " off\r\n"),
			write(csi("1;3;4;7m") + "combined" + csi("0m") + "\r\n"),
		},
	},
	{
		Name:       "osc8_links",
		Categories: []string{"osc8-links"},
		Width:      40, Height: 6,
		Steps: []corpusStep{
			write("\x1b]8;;https://example.com/a\x1b\\linkA\x1b]8;;\x1b\\ after\r\n"),
			write("\x1b]8;id=xyz;https://example.com/b\x07linkB\x1b]8;;\x07\r\n"),
			write(csi("31m") + "\x1b]8;;https://example.com/c\x1b\\styled-link\x1b]8;;\x1b\\" + csi("0m") + "\r\n"),
		},
		KnownGaps: []string{"cell/link_params", "cell/link_url"},
		KnownGapReason: "GAP-3 in the slice 0 evidence: x/vt's OSC 8 handler assigns " +
			"Link.URL from the params field and Link.Params from the URI, i.e. they " +
			"are swapped.",
		KnownSeedGaps:      []string{"cell/link_params", "cell/link_url"},
		KnownSeedGapReason: "Same GAP-3: re-reading a capture goes through the same handler.",
		KnownOutputGaps:    []string{"cell/link_params", "cell/link_url"},
		KnownOutputGapReason: "GAP-3 reaches Frame.Output, the field that becomes " +
			"ControlSnapshot.Output for real consumers. ultraviolet *renders* the link " +
			"correctly as OSC 8;params;uri from the cell it was given, but x/vt *parsed* " +
			"that cell with URL and Params swapped, so the rendering spells the swap out " +
			"and re-reading it applies the swap a second time. Any consumer that round " +
			"trips Output through the model sees the two fields exchanged.",
	},
	{
		Name:       "osc8_hostile_termination",
		Categories: []string{"osc8-links"},
		Width:      40, Height: 6,
		Steps: []corpusStep{
			// An OSC 0 title immediately followed by an OSC 8.
			write("pre\x1b]0;title\x07\x1b]8;;https://example.com/e\x1b\\L2\x1b]8;;\x1b\\\r\n"),
			// OSC 8 whose URI itself contains a semicolon.
			write("\x1b]8;;https://example.com/f?a=1;b=2\x1b\\L3\x1b]8;;\x1b\\\r\n"),
			// An OSC 8 abandoned by a CAN (0x18), then ordinary text.
			write("\x1b]8;;https://example.com/g\x18plainG\r\n"),
			// Nested OSC introducer inside an OSC 8 payload.
			write("safe\x1b]0;title\x1b]8;;https://example.com/h\x1b\\LABEL\x1b]8;;\x1b\\\r\n"),
		},
		KnownGaps: []string{"cell/link_params", "cell/link_url"},
		KnownGapReason: "GAP-3 (URL/params swap) plus GAP-4: x/vt drops an OSC 8 whose " +
			"payload does not split into exactly three semicolon fields, so any URI " +
			"containing a ';' loses its link, and it discards an OSC abandoned by CAN " +
			"where tmux keeps the link it had already parsed.",
		KnownSeedGaps:        []string{"cell/link_params", "cell/link_url"},
		KnownSeedGapReason:   "Same GAP-3/GAP-4 on the seed path.",
		KnownOutputGaps:      []string{"cell/link_params", "cell/link_url"},
		KnownOutputGapReason: "Same GAP-3 swap reaching Frame.Output as in osc8_links.",
	},
	{
		Name:       "osc8_c1_st_terminator",
		Categories: []string{"osc8-links"},
		Width:      40, Height: 6,
		Steps: []corpusStep{
			// A raw C1 ST (0x9c) is not valid UTF-8. tmux 3.6 in UTF-8 mode
			// does not accept it as a string terminator and keeps consuming
			// the OSC; x/vt does terminate on it. The recorded gap is the
			// evidence for that divergence.
			write("safe\x1b]8;;https://example.com/d\x9cLABEL\x1b]8;;\x9c\r\n"),
			write("after\r\n"),
		},
		KnownGaps: []string{"cell/grapheme", "cell/link_params", "cursor/position"},
		KnownGapReason: "GAP-5 in the slice 0 evidence: x/vt accepts a raw 0x9c byte as " +
			"a C1 string terminator inside a UTF-8 stream; tmux 3.6 in UTF-8 mode does " +
			"not and keeps consuming the OSC.",
		KnownOutputGaps: []string{"cell/link_params", "cell/link_url"},
		KnownOutputGapReason: "Same GAP-3 swap reaching Frame.Output: x/vt does terminate " +
			"this OSC 8 and stores the link with the two fields exchanged, so rendering " +
			"and re-reading it exchanges them again.",
		SkipHistoryAssert: true,
		SkipHistoryReason: "the two emulators disagree about how much text was consumed " +
			"by the unterminated OSC, so the scroll-off count diverges with it.",
	},
	{
		Name:       "unicode_wide_combining_emoji",
		Categories: []string{"unicode-graphemes"},
		Width:      24, Height: 8,
		Steps: []corpusStep{
			write("ascii only\r\n"),
			write("日本語 wide\r\n"),
			write("é combining\r\n"),
			write("你好，世界\r\n"),
			write("❤️ vs ❤\r\n"),
			write("\U0001f469‍\U0001f4bb zwj\r\n"),
			// Deliberately not newline-terminated. Every other step parks the
			// cursor back at column 0, which hides GAP-9's second effect: a
			// cluster split across Write calls is committed as two cells, so the
			// cursor ends one column further right than a whole-write replay
			// leaves it. With the row left open, the split replay observes that
			// as a cursor/position gap instead of only a cell difference.
			write("❤️ vs ❤"),
		},
		KnownGaps: []string{"cell/grapheme"},
		KnownGapReason: "GAP-6: x/vt's handlePrint emits every printable ASCII rune as " +
			"its own grapheme immediately, so a combining mark following an ASCII base " +
			"character (NFD text, which is what macOS produces) never attaches to it.",
		KnownSeedGaps:      []string{"cell/grapheme"},
		KnownSeedGapReason: "Same GAP-6 on the seed path: the capture replays the same NFD bytes.",
		KnownSplitGaps:     []string{"cell/grapheme", "cell/width", "cursor/position"},
		KnownSplitGapReason: "GAP-9, the most consequential finding: x/vt's Write flushes " +
			"its pending grapheme buffer when it reaches the end of the byte slice, so a " +
			"grapheme cluster split across two Write calls is committed as two separate " +
			"cells. Pane bytes arrive from tmux in arbitrary chunks, so this is reachable " +
			"in production. It corrupts the cursor as well as the cells: the final, " +
			"deliberately unterminated row leaves the cursor at column 7 on a whole write " +
			"and at column 6 when the cluster is split, because the two committed cells " +
			"advance the cursor differently from the one they should have formed.",
	},
	{
		Name:       "modes_cursor_paste_sync_reset",
		Categories: []string{"modes-cursor-paste-sync-reset"},
		Width:      24, Height: 6,
		Steps: []corpusStep{
			write(csi("?25l") + "cursor hidden\r\n"),
			write(csi("?25h") + "cursor shown\r\n"),
			write("\x1b[3 q" + "cursor style bar\r\n"),
			write(csi("?1000h") + csi("?1002h") + csi("?1006h")),
			write(csi("?2004h") + "bracketed on\r\n"),
			write(csi("?2026h") + "sync" + csi("?2026l") + " done\r\n"),
		},
	},
	{
		// Device queries. Added in slice 2 after the shadow run found that a
		// single one of these deadlocks the emulator (and, through it, the whole
		// control-client actor) unless the adapter drains the reply pipe. These
		// are exactly the sequences nvim emits before its first paint. On the
		// screen they must be invisible: tmux answers them into the pane's input,
		// so neither side may print anything for them.
		Name:       "device_status_queries",
		Categories: []string{"device-queries"},
		Width:      24, Height: 6,
		Steps: []corpusStep{
			write("before\r\n"),
			write(csi("5n")),                     // DSR: operating status
			write(csi("6n")),                     // DSR: cursor position report
			write(csi("c")),                      // primary device attributes
			write(csi(">c")),                     // secondary device attributes
			write(csi("?69$p") + csi("?2026$p")), // DECRQM mode reports
			write("\x1b]11;?\x07"),               // OSC 11 background colour query
			write("after\r\n"),
		},
	},
	{
		Name:       "terminal_reset",
		Categories: []string{"modes-cursor-paste-sync-reset"},
		Width:      24, Height: 6,
		Steps: []corpusStep{
			write(csi("31m") + "colored\r\nsecond\r\n"),
			write(csi("?25l") + csi("3;8r")),
			write(csi("!p")), // DECSTR soft reset
			write("after soft reset\r\n"),
			write("\x1bc"), // RIS hard reset
			write("after hard reset\r\n"),
		},
		KnownGaps: []string{"cursor/visible", "history/size"},
		KnownGapReason: "GAP-7: the Emulator exposes cursor visibility only as a change " +
			"callback, and RIS resets the cursor struct directly, so no change is " +
			"reported and an adapter mirror desyncs; there is no exported getter to " +
			"re-read it from. GAP-8: tmux pushes the cleared screen into history on " +
			"RIS, x/vt's fullReset discards it.",
	},
	{
		// DECSTR on its own, with no RIS afterwards to mask it.
		Name:       "soft_reset_decstr",
		Categories: []string{"modes-cursor-paste-sync-reset"},
		Width:      20, Height: 8,
		Steps: []corpusStep{
			write("a\r\nb\r\nc\r\nd\r\ne\r\nf\r\ng\r\nh"),
			write(csi("?25l") + csi("3;5r")),
			write(csi("!p")), // DECSTR: clears margins and shows the cursor
			write(csi("8;1H") + "bottom"),
			// With the margins cleared this scrolls the whole screen; with them
			// still in force it only scrolls rows 3..5.
			write("\n\n"),
		},
		// No gap: x/vt registers no CSI ! p handler, and tmux 3.6 does not act on
		// one either — neither clears the scroll region nor restores cursor
		// visibility, so the two agree. Recorded so that "DECSTR is
		// unimplemented" is a tested observation rather than a claim.
	},
	{
		Name:       "resize_wider_then_narrower",
		Categories: []string{"resize"},
		Width:      20, Height: 6,
		Steps: []corpusStep{
			write("aaaaaaaaaaaaaaaaaaaa\r\nbbbb\r\n"),
			resize(30, 6),
			write("after wider\r\n"),
			resize(12, 6),
			write("after narrower\r\n"),
		},
		KnownGaps: []string{"cell/grapheme"},
		KnownGapReason: "By design, not a defect: tmux reflows and rewraps existing rows " +
			"on resize (and pushes overflow into history) while x/vt truncates in place. " +
			"The plan already requires a capture reseed after every resize, so the model " +
			"is never asked to reproduce tmux's reflow.",
		SkipHistoryAssert: true,
		SkipHistoryReason: "tmux's reflow pushes rewrapped rows into history; the model " +
			"does not reflow, so the counts cannot agree before the mandated reseed.",
	},
	{
		Name:       "resize_taller_then_shorter",
		Categories: []string{"resize"},
		Width:      16, Height: 6,
		Steps: []corpusStep{
			write("r1\r\nr2\r\nr3\r\nr4\r\n"),
			resize(16, 10),
			write("taller\r\n"),
			resize(16, 4),
			write("shorter\r\n"),
		},
		KnownGaps: []string{"cell/grapheme"},
		KnownGapReason: "Same reflow-on-resize divergence as resize_wider_then_narrower; " +
			"the planned architecture reseeds from capture after a resize.",
		SkipHistoryAssert: true,
		SkipHistoryReason: "shrinking the pane makes tmux push the lost rows into history; " +
			"the model discards them instead.",
	},
	{
		Name:       "history_scrolloff",
		Categories: []string{"crlf-tab-backspace-wrap-phantom"},
		Width:      16, Height: 6,
		Steps: []corpusStep{
			write(repeatLines(20)),
		},
	},
}
