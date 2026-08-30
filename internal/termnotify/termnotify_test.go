package termnotify

import (
	"errors"
	"strings"
	"testing"
)

// The encoders are asserted as literal bytes rather than as "starts with OSC
// 9". A sequence that is one byte wrong is not a degraded notification, it is
// arbitrary text painted into somebody's terminal, so the test has to be able
// to see the difference.
func TestEncodeBytes(t *testing.T) {
	n := Notification{ID: "n-01", Title: "Agent needs input", Body: "sidecar · main"}

	tests := []struct {
		name string
		term Terminal
		tmux bool
		want string
	}{
		{
			name: "ghostty",
			term: Ghostty,
			want: "\x1b]9;Agent needs input: sidecar · main\x07",
		},
		{
			name: "iterm2 shares the OSC 9 encoder",
			term: ITerm2,
			want: "\x1b]9;Agent needs input: sidecar · main\x07",
		},
		{
			name: "wezterm shares the OSC 9 encoder",
			term: WezTerm,
			want: "\x1b]9;Agent needs input: sidecar · main\x07",
		},
		{
			name: "kitty sends title and body as two OSC 99 chunks",
			term: Kitty,
			want: "\x1b]99;i=n-01:d=0:p=title;Agent needs input\x1b\\" +
				"\x1b]99;i=n-01:d=1:p=body;sidecar · main\x1b\\",
		},
		{
			name: "ghostty inside tmux",
			term: Ghostty,
			tmux: true,
			want: "\x1bPtmux;\x1b\x1b]9;Agent needs input: sidecar · main\x07\x1b\\",
		},
		{
			name: "iterm2 inside tmux",
			term: ITerm2,
			tmux: true,
			want: "\x1bPtmux;\x1b\x1b]9;Agent needs input: sidecar · main\x07\x1b\\",
		},
		{
			name: "wezterm inside tmux",
			term: WezTerm,
			tmux: true,
			want: "\x1bPtmux;\x1b\x1b]9;Agent needs input: sidecar · main\x07\x1b\\",
		},
		{
			// Each chunk is wrapped on its own: one DCS carries one payload,
			// and the closing ST of the wrapper is the only ESC left undoubled.
			name: "kitty inside tmux wraps each chunk",
			term: Kitty,
			tmux: true,
			want: "\x1bPtmux;\x1b\x1b]99;i=n-01:d=0:p=title;Agent needs input\x1b\x1b\\\x1b\\" +
				"\x1bPtmux;\x1b\x1b]99;i=n-01:d=1:p=body;sidecar · main\x1b\x1b\\\x1b\\",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Encode(tt.term, n, tt.tmux)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Encode() = %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestEncodeWithoutBody(t *testing.T) {
	n := Notification{ID: "n-01", Title: "Agent finished"}

	got, err := Encode(Ghostty, n, false)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if want := "\x1b]9;Agent finished\x07"; got != want {
		t.Errorf("Encode(Ghostty) = %q, want %q", got, want)
	}

	// A Kitty notification is only shown once a chunk carries d=1, so a
	// bodyless notification has to be one complete chunk rather than a title
	// chunk waiting forever for a body that never arrives.
	got, err = Encode(Kitty, n, false)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if want := "\x1b]99;i=n-01:d=1:p=title;Agent finished\x1b\\"; got != want {
		t.Errorf("Encode(Kitty) = %q, want %q", got, want)
	}
}

func TestEncodePromotesBodyWhenTitleIsEmpty(t *testing.T) {
	n := Notification{ID: "n-01", Body: "sidecar · main"}
	got, err := Encode(Ghostty, n, false)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if want := "\x1b]9;sidecar · main\x07"; got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeRefusesUnsupportedTerminal(t *testing.T) {
	for _, term := range []Terminal{"", "alacritty", "xterm", "Ghostty", "kitty "} {
		if _, err := Encode(term, Notification{Title: "hi"}, false); !errors.Is(err, ErrUnsupportedTerminal) {
			t.Errorf("Encode(%q) error = %v, want ErrUnsupportedTerminal", term, err)
		}
	}
}

func TestEncodeRefusesTextThatSanitizesToNothing(t *testing.T) {
	// Text made entirely of framing bytes leaves nothing to display. An OSC
	// smuggled into a body is not this case: it degrades to harmless literal
	// text, which TestEncodeCannotBreakOutOfTheSequence covers.
	n := Notification{ID: "n-01", Title: "\x1b\x07\n\t", Body: "\x00\x7f  \r\n"}
	for _, term := range Supported() {
		got, err := Encode(term, n, false)
		if !errors.Is(err, ErrEmptyNotification) {
			t.Errorf("Encode(%q) = %q, error = %v, want ErrEmptyNotification", term, got, err)
		}
	}
}

// The adversarial cases are the reason this package exists. Every one of these
// inputs would, unsanitized, end the sequence carrying it and leave the
// remainder to be executed by the terminal.
func TestEncodeCannotBreakOutOfTheSequence(t *testing.T) {
	adversarial := []struct {
		name  string
		title string
		body  string
	}{
		{"bare ESC", "a\x1bb", "c\x1bd"},
		{"BEL closes an OSC 9", "a\x07b", "c\x07d"},
		{"seven-bit string terminator", "a\x1b\\b", "c\x1b\\d"},
		{"eight-bit string terminator", "a\u009cb", "c\u009cd"},
		{"eight-bit OSC introducer", "a\u009d0;pwned\u009cb", "c\u009dd"},
		{"a complete nested OSC", "a\x1b]0;pwned\x07b", "c\x1b]9;pwned\x07d"},
		{"a complete nested DCS", "a\x1bPtmux;\x1b\x1b]9;pwned\x07\x1b\\b", "c\x1bPq\x1b\\d"},
		{"an eight-bit DCS introducer", "a\u0090tmux;pwned\u009cb", "c\u0090d"},
		{"a CSI", "a\x1b[31mb", "c\x1b[2Jd"},
		{"multiline text", "line one\nline two", "body one\r\nbody two"},
		{"tabs", "a\tb", "c\td"},
		{"DEL and NUL", "a\x7fb\x00c", "d\x7fe\x00f"},
		{"a kitty metadata separator", "a:d=1:p=body;pwned", "c;d"},
		{"long unicode", strings.Repeat("日\x1b", 300), strings.Repeat("é\x07", 900)},
	}

	for _, tt := range adversarial {
		n := Notification{ID: "n-01", Title: tt.title, Body: tt.body}
		// Every case must survive sanitization with text left in both fields,
		// or the chunk arithmetic below would silently stop checking anything.
		if Sanitize(tt.title) == "" || Sanitize(tt.body) == "" {
			t.Fatalf("%s: the case sanitizes away entirely and proves nothing", tt.name)
		}
		for _, term := range Supported() {
			for _, tmux := range []bool{false, true} {
				got, err := Encode(term, n, tmux)
				if err != nil {
					t.Fatalf("%s/%s/tmux=%v: Encode() error = %v", tt.name, term, tmux, err)
				}
				assertNoBreakout(t, tt.name, term, tmux, got)
			}
		}
	}
}

// assertNoBreakout checks the one structural property that matters: the only
// escape, BEL and control bytes in the result are the ones the encoder wrote as
// framing. Anything the payload contributed shows up as a surplus, and a
// surplus is a breakout.
func assertNoBreakout(t *testing.T, name string, term Terminal, tmux bool, got string) {
	t.Helper()

	prefix, suffix, escapes, bels := framing(term, tmux)
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("%s/%s/tmux=%v: framing lost: %q", name, term, tmux, got)
	}
	if n := strings.Count(got, "\x1b"); n != escapes {
		t.Errorf("%s/%s/tmux=%v: %d ESC bytes, want %d from framing alone: %q", name, term, tmux, n, escapes, got)
	}
	if n := strings.Count(got, "\x07"); n != bels {
		t.Errorf("%s/%s/tmux=%v: %d BEL bytes, want %d from framing alone: %q", name, term, tmux, n, bels, got)
	}
	if n, want := strings.Count(got, tmuxPassthroughPrefix), tmuxChunks(term, tmux); n != want {
		t.Errorf("%s/%s/tmux=%v: %d tmux passthrough openers, want %d: %q", name, term, tmux, n, want, got)
	}
	for _, r := range got {
		if r == '\x1b' || r == '\x07' {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			t.Errorf("%s/%s/tmux=%v: control rune %U survived: %q", name, term, tmux, r, got)
		}
	}
}

// framing describes what the encoder itself contributes to a notification that
// has both a title and a body, so the assertions can hold the payload to
// "adds nothing".
func framing(term Terminal, tmux bool) (prefix, suffix string, escapes, bels int) {
	switch {
	case term == Kitty && tmux:
		// Per chunk: DCS opener, doubled OSC introducer, doubled ST, wrapper ST.
		return "\x1bPtmux;\x1b\x1b]99;", "\x1b\x1b\\\x1b\\", 6 * 2, 0
	case term == Kitty:
		return "\x1b]99;", "\x1b\\", 2 * 2, 0
	case tmux:
		return "\x1bPtmux;\x1b\x1b]9;", "\x07\x1b\\", 4, 1
	default:
		return "\x1b]9;", "\x07", 1, 1
	}
}

func tmuxChunks(term Terminal, tmux bool) int {
	switch {
	case !tmux:
		return 0
	case term == Kitty:
		return 2
	default:
		return 1
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text is untouched", "Agent needs input", "Agent needs input"},
		{"escape sequences are stripped", "fix\x1b]0;pwned\x07me", "fix]0;pwnedme"},
		{"newlines collapse to one space", "line\n\nline", "line line"},
		{"tabs collapse to one space", "a\t\tb", "a b"},
		{"leading and trailing whitespace vanishes", "  hi  ", "hi"},
		// U+009B is the single-byte CSI: invisible in a diff, dangerous inside
		// a sequence, and not covered by stripping ESC alone.
		{"C1 controls are stripped", "side\u009bcar", "sidecar"},
		{"bidi overrides are stripped", "main\u202egnp.exe", "maingnp.exe"},
		{"zero width joiner survives so emoji stay intact", "👩‍💻", "👩‍💻"},
		{"multibyte text survives", "sidecar · プロジェクト", "sidecar · プロジェクト"},
		{"the replacement character is dropped", "a\ufffdb", "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sanitize(tt.in); got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEncodeTruncatesWithoutSplittingRunes(t *testing.T) {
	n := Notification{
		ID:    "n-01",
		Title: strings.Repeat("日", 400),
		Body:  strings.Repeat("é", 900),
	}
	got, err := Encode(Kitty, n, false)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if strings.ContainsRune(got, '�') {
		t.Error("Encode() truncated in the middle of a rune")
	}
	if want := strings.Repeat("日", maxTitleRunes); !strings.Contains(got, "p=title;"+want+"\x1b\\") {
		t.Errorf("title was not bounded to %d runes: %q", maxTitleRunes, got)
	}
	if want := strings.Repeat("é", maxBodyRunes); !strings.Contains(got, "p=body;"+want+"\x1b\\") {
		t.Errorf("body was not bounded to %d runes", maxBodyRunes)
	}
}

func TestKittyIdentifierIsReducedToKittysAlphabet(t *testing.T) {
	tests := []struct{ in, want string }{
		{"n-01", "n-01"},
		{"", fallbackID},
		{"!!!", fallbackID},
		{"a;d=1:p=body", "ad1pbody"},
		{"a b\x1bc", "abc"},
		{strings.Repeat("z", 100), strings.Repeat("z", maxIDRunes)},
	}
	for _, tt := range tests {
		if got := sanitizeID(tt.in); got != tt.want {
			t.Errorf("sanitizeID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseTerminal(t *testing.T) {
	for _, term := range Supported() {
		if got, ok := ParseTerminal(string(term)); !ok || got != term {
			t.Errorf("ParseTerminal(%q) = %q, %v", term, got, ok)
		}
	}
	for _, name := range []string{"", "off", "auto", "Ghostty", "iterm", "alacritty"} {
		if got, ok := ParseTerminal(name); ok {
			t.Errorf("ParseTerminal(%q) = %q, want no match", name, got)
		}
	}
}
