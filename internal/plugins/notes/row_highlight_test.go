package notes

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

func noteRowCellBackgrounds(row string) []string {
	var bgs []string
	current := ""
	state := ansi.NormalState
	remaining := row
	for len(remaining) > 0 {
		seq, width, n, next := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if width > 0 {
			for range width {
				bgs = append(bgs, current)
			}
		} else if bg, touches := ui.SGRBackground(seq); touches {
			if bg == ui.RowBackgroundDefault {
				current = ""
			} else {
				current = bg
			}
		}
		state = next
		remaining = remaining[n:]
	}
	return bgs
}

// The selected note row is the same row as the unselected one, highlighted: the
// status icon, the cursor and the pin badge keep their styling, and the
// background covers every cell rather than stopping at the first inner reset.
func TestSelectedNoteRowHighlightsEveryCellAndKeepsItsStyledSpans(t *testing.T) {
	p := layoutTestPlugin(t, "body")
	deleted := time.Now()
	notes := map[string]Note{
		"plain":   {Title: "an ordinary note"},
		"pinned":  {Title: "a pinned note", Pinned: true},
		"archive": {Title: "an archived note", Archived: true},
		"deleted": {Title: "a deleted note", DeletedAt: &deleted},
		"short":   {Title: "x"},
		"long":    {Title: strings.Repeat("a very long title ", 10)},
	}

	want := styles.BgANSISeqFor(styles.BgTertiary)
	const width = 30
	for name, note := range notes {
		t.Run(name, func(t *testing.T) {
			row := p.renderNoteRow(note, true, width)
			bgs := noteRowCellBackgrounds(row)
			if len(bgs) != width {
				t.Fatalf("row occupies %d cells, want %d: %q", len(bgs), width, row)
			}
			for col, bg := range bgs {
				if bg != want {
					t.Fatalf("column %d is not highlighted (bg %q, want %q): %q", col, bg, want, row)
				}
			}
			// The cursor marker survives the highlight.
			if !strings.Contains(ansi.Strip(row), ">") {
				t.Fatalf("selected row lost its cursor: %q", ansi.Strip(row))
			}
			if note.Pinned && !strings.Contains(ansi.Strip(row), "*") {
				t.Fatalf("selected row lost its pin badge: %q", ansi.Strip(row))
			}
			if note.Archived && !strings.Contains(ansi.Strip(row), iconArchived) {
				t.Fatalf("selected row lost its archived icon: %q", ansi.Strip(row))
			}
			if note.DeletedAt != nil && !strings.Contains(ansi.Strip(row), iconDeleted) {
				t.Fatalf("selected row lost its deleted icon: %q", ansi.Strip(row))
			}
		})
	}
}

func TestUnselectedNoteRowCarriesNoHighlight(t *testing.T) {
	p := layoutTestPlugin(t, "body")
	row := p.renderNoteRow(Note{Title: "an ordinary note", Pinned: true}, false, 30)
	for _, bg := range noteRowCellBackgrounds(row) {
		if bg != "" {
			t.Fatalf("an unselected row carries background %q: %q", bg, row)
		}
	}
}
