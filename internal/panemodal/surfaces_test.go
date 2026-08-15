package panemodal

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/projectsearch"
)

// These drive the real surfaces rather than a synthetic modal, which is the
// only way to see which branch panemodal actually takes in the app. A synthetic
// three-line modal is roomy in every box; the surfaces size themselves, and an
// earlier version of them padded their list to the full box height, which made
// the roomy branch — the one every comment described as the normal case —
// unreachable for any pane shorter than about forty rows.

func surfaceFiles() []string {
	return []string{
		".claude/skills/create-modal/SKILL.md",
		".claude/skills/create-plugin/SKILL.md",
		"internal/plugins/filebrowser/view.go",
		"internal/plugins/workspace/doc_panes.go",
		"README.md",
	}
}

func finderDraw(f *filefind.Finder) Draw {
	return func(width, height int, fill bool, h *mouse.Handler) string {
		f.SetFill(fill)
		return f.View(width, height, h)
	}
}

func searchDraw(s *projectsearch.Search) Draw {
	return func(width, height int, fill bool, h *mouse.Handler) string {
		s.SetFill(fill)
		return s.View(width, height, h)
	}
}

// branchOf reports whether the pane content survived around the surface
// ("dimmed") or the surface took the box ("filled").
func branchOf(out string) string {
	if strings.Contains(ansi.Strip(out), bgMarker) {
		return "dimmed"
	}
	return "filled"
}

func TestRealSurfacesTakeBothBranches(t *testing.T) {
	// The finder's box likes to be 80 cells and the search's 120, so they cross
	// from one branch to the other at different pane widths. What matters is
	// that each surface reaches both branches at sizes a real pane has.
	boxes := []struct {
		box    Box
		finder string
		search string
	}{
		{Box{X: 4, Y: 2, W: 180, H: 44}, "dimmed", "dimmed"},
		{Box{X: 0, Y: 0, W: 140, H: 40}, "dimmed", "dimmed"},
		{Box{X: 0, Y: 0, W: 120, H: 30}, "dimmed", "dimmed"},
		{Box{X: 2, Y: 3, W: 80, H: 24}, "dimmed", "dimmed"},
		// Panes with no room to spare: the surface owns the box.
		{Box{X: 1, Y: 1, W: 56, H: 20}, "dimmed", "filled"},
		{Box{X: 0, Y: 0, W: 40, H: 12}, "filled", "filled"},
		{Box{X: 0, Y: 0, W: 30, H: 8}, "filled", "filled"},
	}

	for _, tc := range boxes {
		finder := filefind.NewFinder(&filefind.Cache{Files: surfaceFiles(), OK: true}, "/root", 1)
		finder.Open()
		search := projectsearch.New("/root", 1)

		for name, surface := range map[string]struct {
			draw Draw
			want string
		}{
			"finder": {finderDraw(finder), tc.finder},
			"search": {searchDraw(search), tc.search},
		} {
			draw, want := surface.draw, surface.want
			out := RenderFunc(tc.box, background(tc.box), mouse.NewHandler(), draw)

			lines := strings.Split(out, "\n")
			if len(lines) != tc.box.H {
				t.Errorf("%s %dx%d: %d lines, want %d", name, tc.box.W, tc.box.H, len(lines), tc.box.H)
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w != tc.box.W {
					t.Errorf("%s %dx%d: line %d is %d cells, want %d", name, tc.box.W, tc.box.H, i, w, tc.box.W)
				}
			}
			if got := branchOf(out); got != want {
				t.Errorf("%s %dx%d: took the %s branch, want %s:\n%s",
					name, tc.box.W, tc.box.H, got, want, ansi.Strip(out))
			}
		}
	}
}

// The tight branch is not "a small box on an empty field": the surface is the
// box, edge to edge, with its border on the box's own first and last row.
func TestFilledSurfaceOwnsTheWholeBox(t *testing.T) {
	for _, box := range []Box{{X: 3, Y: 1, W: 34, H: 20}, {X: 0, Y: 0, W: 30, H: 24}, {X: 0, Y: 0, W: 40, H: 12}} {
		finder := filefind.NewFinder(&filefind.Cache{Files: surfaceFiles(), OK: true}, "/root", 1)
		finder.Open()
		out := ansi.Strip(RenderFunc(box, background(box), mouse.NewHandler(), finderDraw(finder)))
		lines := strings.Split(out, "\n")

		if !strings.HasPrefix(lines[0], "╭") || !strings.HasSuffix(lines[0], "╮") {
			t.Errorf("%dx%d: first row is not the surface's top border: %q", box.W, box.H, lines[0])
		}
		last := lines[len(lines)-1]
		if !strings.HasPrefix(last, "╰") || !strings.HasSuffix(last, "╯") {
			t.Errorf("%dx%d: last row is not the surface's bottom border: %q", box.W, box.H, last)
		}
	}
}

// The dimmed branch keeps a margin wide enough to read as pane content rather
// than as vertical confetti down each side of the box.
func TestDimmedSurfaceKeepsAReadableMargin(t *testing.T) {
	box := Box{X: 6, Y: 2, W: 180, H: 44}
	finder := filefind.NewFinder(&filefind.Cache{Files: surfaceFiles(), OK: true}, "/root", 1)
	finder.Open()
	out := ansi.Strip(RenderFunc(box, background(box), mouse.NewHandler(), finderDraw(finder)))

	for _, line := range strings.Split(out, "\n") {
		runes := []rune(line)
		start, end := -1, -1
		for i, r := range runes {
			if r == '╭' {
				start = i
			}
			if r == '╮' {
				end = i
			}
		}
		if start < 0 || end < 0 {
			continue
		}
		right := len(runes) - 1 - end
		if start < dimMarginX || right < dimMarginX {
			t.Fatalf("margins left=%d right=%d, want at least %d cells each side", start, right, dimMarginX)
		}
		return
	}
	t.Fatal("no border row in a box that should have been centred")
}
