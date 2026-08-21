package notes

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestContinueMarkdownListTable(t *testing.T) {
	tests := []struct {
		name    string
		content string
		row     int
		col     int
		want    string
		wantRow int
		wantCol int
		ok      bool
	}{
		{
			name:    "bullet continues",
			content: "- item",
			col:     len("- item"),
			want:    "- item\n- ",
			wantRow: 1,
			wantCol: 2,
			ok:      true,
		},
		{
			name:    "star continues",
			content: "* item",
			col:     len("* item"),
			want:    "* item\n* ",
			wantRow: 1,
			wantCol: 2,
			ok:      true,
		},
		{
			name:    "numbered increments",
			content: "1. first",
			col:     len("1. first"),
			want:    "1. first\n2. ",
			wantRow: 1,
			wantCol: 3,
			ok:      true,
		},
		{
			name:    "paren numbered increments",
			content: "2) next",
			col:     len("2) next"),
			want:    "2) next\n3) ",
			wantRow: 1,
			wantCol: 3,
			ok:      true,
		},
		{
			name:    "checkbox continues unchecked",
			content: "- [x] done",
			col:     len("- [x] done"),
			want:    "- [x] done\n- [ ] ",
			wantRow: 1,
			wantCol: 6,
			ok:      true,
		},
		{
			name:    "star checkbox continues",
			content: "* [ ] todo",
			col:     len("* [ ] todo"),
			want:    "* [ ] todo\n* [ ] ",
			wantRow: 1,
			wantCol: 6,
			ok:      true,
		},
		{
			name:    "empty bullet exits",
			content: "- ",
			col:     2,
			want:    "",
			ok:      true,
		},
		{
			name:    "empty numbered exits",
			content: "1. ",
			col:     3,
			want:    "",
			ok:      true,
		},
		{
			name:    "empty checkbox exits",
			content: "- [ ] ",
			col:     6,
			want:    "",
			ok:      true,
		},
		{
			name:    "nested indent preserved",
			content: "  - child",
			col:     len("  - child"),
			want:    "  - child\n  - ",
			wantRow: 1,
			wantCol: 4,
			ok:      true,
		},
		{
			name:    "empty nested keeps indent",
			content: "    - ",
			col:     6,
			want:    "    ",
			wantCol: 4,
			ok:      true,
		},
		{
			name:    "split mid item",
			content: "- hello world",
			col:     len("- hello"),
			want:    "- hello\n-  world",
			wantRow: 1,
			wantCol: 2,
			ok:      true,
		},
		{
			name:    "plain line is not a list",
			content: "hello",
			col:     5,
			want:    "hello",
			wantCol: 5,
		},
		{
			name:    "date is not a list",
			content: "2026. August",
			col:     12,
			want:    "2026. August",
			wantCol: 12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, row, col, ok := continueMarkdownList(tt.content, tt.row, tt.col)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tt.ok, got)
			}
			if got != tt.want || row != tt.wantRow || col != tt.wantCol {
				t.Fatalf("got %q @(%d,%d), want %q @(%d,%d)", got, row, col, tt.want, tt.wantRow, tt.wantCol)
			}
		})
	}
}

func TestEnterContinuesAndExitsListInEditor(t *testing.T) {
	p := newEditPlugin(t, "- item")
	p.setTextareaCursorPosition(0, len("- item"))
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := p.editorTextarea.Value(); got != "- item\n- " {
		t.Fatalf("continued list = %q", got)
	}
	if p.editorTextarea.Line() != 1 || p.editorTextarea.Column() != 2 {
		t.Fatalf("caret = (%d,%d), want (1,2)", p.editorTextarea.Line(), p.editorTextarea.Column())
	}
	if !p.editorDirty {
		t.Fatal("list continue did not mark dirty")
	}

	typeKey(p, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := p.editorTextarea.Value(); got != "- item\n" {
		t.Fatalf("empty list exit = %q", got)
	}
	if p.editorTextarea.Line() != 1 {
		t.Fatalf("exit caret line = %d, want 1", p.editorTextarea.Line())
	}
}

func TestEnterOnPlainLineStillInsertsNewline(t *testing.T) {
	p := newEditPlugin(t, "hello")
	p.setTextareaCursorPosition(0, len("hello"))
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := p.editorTextarea.Value(); got != "hello\n" {
		t.Fatalf("plain enter = %q", got)
	}
}
