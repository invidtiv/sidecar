package ui

import (
	"testing"
)

func TestStripRowBackgroundsDropsExplicitColors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"256-color", "\x1b[48;5;22mtext\x1b[0m", "\x1b[49mtext\x1b[0m"},
		{"truecolor", "\x1b[48;2;1;93;1mtext\x1b[49m", "\x1b[49mtext\x1b[49m"},
		{"legacy", "\x1b[41mred bg\x1b[49m", "\x1b[49mred bg\x1b[49m"},
		{"bright legacy", "\x1b[101mbright\x1b[49m", "\x1b[49mbright\x1b[49m"},
	}
	for _, tc := range cases {
		if got := StripRowBackgrounds(tc.in); got != tc.want {
			t.Errorf("%s: StripRowBackgrounds(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestStripRowBackgroundsKeepsForegroundAndAttributes(t *testing.T) {
	in := "\x1b[38;2;102;102;102mdim text\x1b[39m and \x1b[1mbold\x1b[22m"
	if got := StripRowBackgrounds(in); got != in {
		t.Fatalf("foreground-only row was rewritten: %q -> %q", in, got)
	}

	// A compound sequence keeps its non-background parameters: bold survives,
	// the red background becomes default.
	if got, want := StripRowBackgrounds("\x1b[1;41mtx\x1b[0m"), "\x1b[1;49mtx\x1b[0m"; got != want {
		t.Fatalf("compound strip = %q, want %q", got, want)
	}
}

func TestStripRowBackgroundsKeepsColorArgumentAlignment(t *testing.T) {
	// Exact strings: a compound fg+bg sequence must rebuild into ONE well-formed
	// SGR. The earlier implementation spliced sgrColorParam's complete sequence
	// into the parameter list, producing ESC[ESC[38;2;…m;49m — the inner CSI
	// won and the orphaned ";49m" rendered as literal text down opencode's
	// left margin (td-e8d3cf).
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"fg truecolor + bg 256", "\x1b[38;2;200;200;200;48;5;22mtx\x1b[0m",
			"\x1b[38;2;200;200;200;49mtx\x1b[0m"},
		{"bg first, fg second", "\x1b[48;5;22;38;2;10;20;30mtx\x1b[0m",
			"\x1b[49;38;2;10;20;30mtx\x1b[0m"},
		{"underline color kept", "\x1b[58;2;255;0;0;48;5;22mtx\x1b[0m",
			"\x1b[58;2;255;0;0;49mtx\x1b[0m"},
	}
	for _, tc := range cases {
		if got := StripRowBackgrounds(tc.in); got != tc.want {
			t.Errorf("%s: StripRowBackgrounds(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestStripRowBackgroundsLeavesPlainTextAlone(t *testing.T) {
	in := "plain text with [brackets] and 48;5 numbers"
	if got := StripRowBackgrounds(in); got != in {
		t.Fatalf("plain text rewritten: %q -> %q", in, got)
	}
}
