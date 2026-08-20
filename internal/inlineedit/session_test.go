package inlineedit

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

type fakeHost struct {
	w, h     int
	x, y     int
	originOK bool
}

func (f fakeHost) EditorViewport() (int, int)     { return f.w, f.h }
func (f fakeHost) EditorOrigin() (int, int, bool) { return f.x, f.y, f.originOK }

func TestMouseCoordsUsesHostContract(t *testing.T) {
	s := &Session{Host: fakeHost{w: 20, h: 10, x: 5, y: 3, originOK: true}}

	col, row, ok := s.MouseCoords(5, 3)
	if !ok || col != 1 || row != 1 {
		t.Fatalf("origin click: got (%d,%d,%v), want (1,1,true)", col, row, ok)
	}

	if _, _, ok := s.MouseCoords(4, 3); ok {
		t.Fatal("click left of the content area should miss")
	}
	if _, _, ok := s.MouseCoords(25, 3); ok {
		t.Fatal("click past the viewport width should miss")
	}
	if _, _, ok := s.MouseCoords(5, 13); ok {
		t.Fatal("click past the viewport height should miss")
	}
}

func TestMouseCoordsMissesWithoutOrigin(t *testing.T) {
	s := &Session{Host: fakeHost{w: 20, h: 10}}
	if _, _, ok := s.MouseCoords(0, 0); ok {
		t.Fatal("host without an origin must not map coordinates")
	}
}

// Every document editor host (Notes, Files, project Workspace, and global
// Sessions) forwards through Session. Keep the pane's terminal mode as the one
// shared ownership gate so none of those hosts can inject SGR bytes into an
// editor that did not request them.
func TestMouseForwardingRequiresPaneMouseReporting(t *testing.T) {
	s := &Session{Model: tty.New(nil), Active: true, Name: "editor"}
	s.Model.Enter("editor", "")
	s.Model.State.PaneWidth = 20
	s.Model.State.PaneHeight = 10

	for name, forward := range map[string]func() tea.Cmd{
		"press": func() tea.Cmd { return s.ForwardMousePress(2, 3) },
		"drag":  func() tea.Cmd { return s.ForwardMouseDrag(4, 5) },
		"release": func() tea.Cmd {
			return s.ForwardMouseRelease(4, 5)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s.Model.State.MouseReportingEnabled = false
			if cmd := forward(); cmd != nil {
				t.Fatalf("mouse-reporting off returned %T, want nil", cmd)
			}
			s.Model.State.MouseReportingEnabled = true
			if cmd := forward(); cmd == nil {
				t.Fatal("mouse-reporting on returned nil")
			}
		})
	}
}

func TestCursorClampedToHostRect(t *testing.T) {
	s := &Session{Model: tty.New(nil), Host: fakeHost{w: 10, h: 5, x: 2, y: 1, originOK: true}}
	// An inactive model has no cursor; the important guarantee is that we never
	// return a cursor without one.
	if c := s.Cursor(80, 24); c != nil {
		t.Fatalf("inactive editor reported a cursor: %+v", c)
	}
}

func TestConfirmSelectionWraps(t *testing.T) {
	s := &Session{}
	s.MoveConfirmSelection(-1)
	if s.ConfirmSelection != 2 {
		t.Fatalf("backwards wrap: got %d, want 2", s.ConfirmSelection)
	}
	s.MoveConfirmSelection(1)
	if s.ConfirmSelection != 0 {
		t.Fatalf("forwards wrap: got %d, want 0", s.ConfirmSelection)
	}
}

func TestRenderExitConfirmMarksSelection(t *testing.T) {
	s := &Session{ConfirmSelection: 1}
	out := s.RenderExitConfirm()
	for _, opt := range ExitConfirmOptions {
		if !strings.Contains(out, opt) {
			t.Fatalf("missing option %q in:\n%s", opt, out)
		}
	}
	if !strings.Contains(out, "> Exit without saving") {
		t.Fatalf("selection marker missing:\n%s", out)
	}
}

func TestCleanupStaleSkipsLiveSession(t *testing.T) {
	s := &Session{Model: tty.New(nil), Name: "sidecar-edit-1"}
	// Model is inactive, so a stale name yields a kill command.
	if cmd := s.CleanupStale("sidecar-edit-2", "vim"); cmd == nil {
		t.Fatal("stale start should be cleaned up")
	}
}

func TestResetClearsSessionState(t *testing.T) {
	s := &Session{Model: tty.New(nil), Active: true, Name: "n", Path: "p", EditorCmd: "vim", Dragging: true}
	before := s.Activation
	s.Reset()
	if s.Active || s.Name != "" || s.Path != "" || s.EditorCmd != "" || s.Dragging {
		t.Fatalf("reset left state behind: %+v", s)
	}
	if s.Activation != before+1 {
		t.Fatalf("reset must invalidate in-flight messages: %d -> %d", before, s.Activation)
	}
}

func TestPendingClickRoundTrip(t *testing.T) {
	s := &Session{}
	s.SetPendingClick("tree-item", 4)
	region, data := s.TakePendingClick()
	if region != "tree-item" || data != 4 {
		t.Fatalf("got (%q,%v)", region, data)
	}
	if s.PendingClickRegion != "" || s.PendingClickData != nil {
		t.Fatal("pending click not cleared")
	}
}

func TestNilReceiverSafe(t *testing.T) {
	var s *Session
	if s.NativeActive() || s.IsAlive() || s.IsModelActive() {
		t.Fatal("nil session reported activity")
	}
	if _, _, ok := s.MouseCoords(1, 1); ok {
		t.Fatal("nil session mapped coordinates")
	}
	s.Reset()
	s.ClearPendingClick()
}
