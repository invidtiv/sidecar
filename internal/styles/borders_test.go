package styles

import (
	"strings"
	"testing"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxWidth int
		want     string
	}{
		{
			name:     "empty string",
			input:    "",
			maxWidth: 10,
			want:     "",
		},
		{
			name:     "zero width",
			input:    "hello",
			maxWidth: 0,
			want:     "",
		},
		{
			name:     "negative width",
			input:    "hello",
			maxWidth: -5,
			want:     "",
		},
		{
			name:     "string shorter than max",
			input:    "hello",
			maxWidth: 10,
			want:     "hello",
		},
		{
			name:     "string equal to max",
			input:    "hello",
			maxWidth: 5,
			want:     "hello",
		},
		{
			name:     "string longer than max",
			input:    "hello world",
			maxWidth: 5,
			want:     "hello",
		},
		{
			name:     "ANSI color code preserved",
			input:    "\x1b[31mred\x1b[0m",
			maxWidth: 3,
			want:     "\x1b[31mred\x1b[0m",
		},
		{
			name:     "ANSI color code with truncation",
			input:    "\x1b[31mred text\x1b[0m",
			maxWidth: 5,
			want:     "\x1b[31mred t", // trailing reset not included (after truncation)
		},
		{
			name:     "multiple ANSI codes",
			input:    "\x1b[31mred\x1b[0m \x1b[32mgreen\x1b[0m",
			maxWidth: 5,
			want:     "\x1b[31mred\x1b[0m \x1b[32mg",
		},
		{
			name:     "RGB ANSI code (24-bit color)",
			input:    "\x1b[38;2;255;0;0mRED\x1b[0m",
			maxWidth: 3,
			want:     "\x1b[38;2;255;0;0mRED\x1b[0m",
		},
		{
			name:     "truncate within RGB ANSI content",
			input:    "\x1b[38;2;255;0;0mHello World\x1b[0m",
			maxWidth: 5,
			want:     "\x1b[38;2;255;0;0mHello", // trailing reset not included
		},
		{
			name:     "mixed plain and ANSI",
			input:    "plain \x1b[1mbold\x1b[0m plain",
			maxWidth: 10,
			want:     "plain \x1b[1mbold\x1b[0m",
		},
		{
			name:     "unicode preserved",
			input:    "héllo",
			maxWidth: 5,
			want:     "héllo",
		},
		{
			name:     "unicode truncated",
			input:    "héllo world",
			maxWidth: 5,
			want:     "héllo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.input, tt.maxWidth)
			if got != tt.want {
				t.Errorf("truncateString(%q, %d) = %q, want %q",
					tt.input, tt.maxWidth, got, tt.want)
			}
		})
	}
}

func TestIsTerminator(t *testing.T) {
	tests := []struct {
		b    byte
		want bool
	}{
		{'A', true},
		{'Z', true},
		{'a', true},
		{'z', true},
		{'m', true}, // most common ANSI terminator
		{'0', false},
		{';', false},
		{'[', false},
		{'\x1b', false},
	}

	for _, tt := range tests {
		t.Run(string(tt.b), func(t *testing.T) {
			if got := isTerminator(tt.b); got != tt.want {
				t.Errorf("isTerminator(%q) = %v, want %v", tt.b, got, tt.want)
			}
		})
	}
}

func TestDecodeRune(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantRune rune
		wantSize int
	}{
		{"empty", "", 0, 0},
		{"ASCII", "a", 'a', 1},
		{"2-byte UTF-8", "é", 'é', 2},
		{"3-byte UTF-8", "中", '中', 3},
		{"4-byte UTF-8", "😀", '😀', 4},
		// Invalid continuation byte tests - should fallback to single byte
		{"invalid 2-byte continuation", "\xC0\x00", 0xC0, 1},    // \x00 not valid continuation
		{"invalid 3-byte continuation", "\xE0\x80\x00", 0xE0, 1}, // \x00 not valid continuation
		{"invalid 4-byte continuation", "\xF0\x80\x80\x00", 0xF0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRune, gotSize := decodeRune(tt.input)
			if gotRune != tt.wantRune || gotSize != tt.wantSize {
				t.Errorf("decodeRune(%q) = (%q, %d), want (%q, %d)",
					tt.input, gotRune, gotSize, tt.wantRune, tt.wantSize)
			}
		})
	}
}

func TestRuneWidth(t *testing.T) {
	tests := []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{'Z', 1},
		{'中', 2}, // CJK
		{'한', 2}, // Hangul
		{'ａ', 2}, // Fullwidth Latin
		{'é', 1},  // Latin extended
		// Emoji tests - most render as width 2 in terminals
		{'😀', 2}, // Emoticons (U+1F600)
		{'🌍', 2}, // Misc Symbols (U+1F30D)
		{'☀', 2},  // Misc Symbols (U+2600)
		{'✅', 2}, // Dingbats (U+2705)
	}

	for _, tt := range tests {
		t.Run(string(tt.r), func(t *testing.T) {
			if got := runeWidth(tt.r); got != tt.want {
				t.Errorf("runeWidth(%q) = %d, want %d", tt.r, got, tt.want)
			}
		})
	}
}

func TestRenderGradientBorderWithBgFillsEmptyLines(t *testing.T) {
	grad := NewGradient([]string{"#ff0000", "#00ff00"}, 30)
	bg := "#202020"
	content := "Short line 1\nShort line 2"

	// Request a box of 40x10 (innerHeight = 8, content has only 2 lines, so 6 trailing lines)
	out := RenderGradientBorderWithBg(content, 40, 10, grad, 1, bg)

	bgANSI := HexToRGB(bg).ToANSIBg()
	if !strings.Contains(out, bgANSI) {
		t.Errorf("expected output to contain bg ANSI code %q, got %q", bgANSI, out)
	}

	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d", len(lines))
	}

	// Check that a trailing empty line (e.g. line 6) contains the bgANSI sequence
	trailingLine := lines[6]
	if !strings.Contains(trailingLine, bgANSI) {
		t.Errorf("expected trailing line %q to contain background ANSI code %q", trailingLine, bgANSI)
	}
}

