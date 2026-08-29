package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// rowCellBackgrounds is the background active at each visible cell of a
// rendered row — the only thing that says whether a highlight has holes.
func rowCellBackgrounds(row string) []string {
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

// The selected entry is two rows built from pre-styled spans — a source-hued
// unread dot, a muted age column, a muted body. Before the shared row-background
// helper those spans' resets punched holes in the highlight, so the selection
// looked different depending on which notification it landed on.
func TestCentreSelectedEntryIsHighlightedAcrossBothRowsAndEveryStyledSpan(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	stored, err := m.notifications.Post(notify.Notification{
		Source: notify.SourceAgent,
		Title:  "Agent finished",
		Body:   "claude · sidecar/notification-center · exit 0",
	})
	if err != nil {
		t.Fatal(err)
	}
	m.refreshNotifications()
	m.toggleNotificationCentre()
	m.focusNotificationCentre()
	m.notificationCentreCursor = 0

	const inner = 40
	lines := m.notificationCentreItemLines(stored.Notification, inner, 0, time.Now())
	if len(lines) != 2 {
		t.Fatalf("entry rendered %d rows, want the title row and the body row", len(lines))
	}

	want := styles.BgANSISeqFor(styles.SurfaceRaised)
	for i, line := range lines {
		bgs := rowCellBackgrounds(line)
		if len(bgs) != inner {
			t.Fatalf("row %d occupies %d cells, want the full %d-column width: %q", i, len(bgs), inner, line)
		}
		for col, bg := range bgs {
			if bg != want {
				t.Fatalf("row %d column %d is not highlighted (bg %q, want %q): %q", i, col, bg, want, line)
			}
		}
	}

	// The highlight must not eat the content that identifies the entry.
	if !strings.Contains(ansi.Strip(lines[0]), "Agent finished") {
		t.Fatalf("title row lost its text: %q", ansi.Strip(lines[0]))
	}
	if !strings.Contains(ansi.Strip(lines[1]), "claude") {
		t.Fatalf("body row lost its text: %q", ansi.Strip(lines[1]))
	}
	if !strings.Contains(ansi.Strip(lines[0]), "●") {
		t.Fatalf("title row lost the unread dot: %q", ansi.Strip(lines[0]))
	}
}

// An unselected row is untouched — no background, no padding to the panel width.
func TestCentreUnselectedRowsCarryNoHighlight(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	stored := postCentreNotification(t, &m, notify.SourceTasks, "a due task")
	m.toggleNotificationCentre()
	m.focusNotificationCentre()
	m.notificationCentreCursor = 0

	lines := m.notificationCentreItemLines(stored, 40, 1, time.Now())
	for _, bg := range rowCellBackgrounds(lines[0]) {
		if bg != "" {
			t.Fatalf("an unselected row carries background %q: %q", bg, lines[0])
		}
	}
}
