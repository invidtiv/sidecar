package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type outputTestPlugin struct {
	nativeTestPlugin
	content string
	views   int
}

func (p *outputTestPlugin) View(int, int) string {
	p.views++
	return p.content
}

type constrainedOutputTestPlugin struct {
	outputTestPlugin
	constrained bool
}

func (p *constrainedOutputTestPlugin) ViewIsSelfConstrained() bool { return p.constrained }

type dimensionConstrainedTestPlugin struct {
	nativeTestPlugin
	width   int
	height  int
	content string
}

func (p *dimensionConstrainedTestPlugin) View(width, height int) string {
	p.width, p.height = width, height
	renderWidth := max(width, 3)
	renderHeight := max(height, 4)
	line := strings.Repeat("x", renderWidth)
	p.content = strings.Repeat(line+"\n", renderHeight-1) + line
	return p.content
}

func (p *dimensionConstrainedTestPlugin) ViewIsSelfConstrained() bool {
	return p.width >= 3 && p.height >= 4
}

func TestRenderContentTrustsOnlyExplicitSelfConstrainedView(t *testing.T) {
	t.Run("capable plugin returns its frame unchanged", func(t *testing.T) {
		// The short sentinel deliberately violates the capability contract: if
		// the host wrapped it, padding would make the branch observable here.
		// Real opt-ins prove their contract separately, as Workspace does.
		p := &constrainedOutputTestPlugin{
			outputTestPlugin: outputTestPlugin{content: "already constrained"},
			constrained:      true,
		}
		m := routerTestModel(t, p)

		if got := m.renderContent(20, 4); got != p.content {
			t.Fatalf("renderContent changed capable output:\n got %q\nwant %q", got, p.content)
		}
		if p.views != 1 {
			t.Fatalf("View calls = %d, want 1", p.views)
		}
	})

	t.Run("non-capable plugin keeps defensive clamp", func(t *testing.T) {
		p := &outputTestPlugin{content: "123456789\nabcdefghi\nthird row"}
		m := routerTestModel(t, p)

		got := m.renderContent(6, 2)
		lines := strings.Split(got, "\n")
		if len(lines) != 2 {
			t.Fatalf("rendered lines = %d, want 2: %q", len(lines), got)
		}
		for i, line := range lines {
			if width := ansi.StringWidth(line); width > 6 {
				t.Fatalf("line %d width = %d, want <= 6: %q", i, width, line)
			}
		}
		if p.views != 1 {
			t.Fatalf("View calls = %d, want 1", p.views)
		}
	})

	t.Run("capability can decline", func(t *testing.T) {
		p := &constrainedOutputTestPlugin{
			outputTestPlugin: outputTestPlugin{content: "first\nsecond\nthird"},
			constrained:      false,
		}
		m := routerTestModel(t, p)
		if lines := strings.Split(m.renderContent(8, 2), "\n"); len(lines) != 2 {
			t.Fatalf("declining capability rendered %d lines, want 2", len(lines))
		}
	})
}

func TestRenderContentFallsBackAtSelfConstrainedViewDimensionFloor(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		wantFastPath  bool
	}{
		{name: "height 1", width: 8, height: 1},
		{name: "height 2", width: 8, height: 2},
		{name: "height 3", width: 8, height: 3},
		{name: "width 0", width: 0, height: 4},
		{name: "width 1", width: 1, height: 4},
		{name: "width 2", width: 2, height: 4},
		{name: "accepted boundary", width: 3, height: 4, wantFastPath: true},
		{name: "accepted larger", width: 8, height: 5, wantFastPath: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &dimensionConstrainedTestPlugin{}
			m := routerTestModel(t, p)
			got := m.renderContent(tt.width, tt.height)
			if p.content == "" {
				t.Fatal("renderContent skipped View")
			}

			if tt.wantFastPath {
				if got != p.content {
					t.Fatalf("accepted dimensions changed capable output: got %q want %q", got, p.content)
				}
				return
			}
			want := ""
			if tt.width > 0 && tt.height > 0 {
				want = lipgloss.NewStyle().Width(tt.width).Height(tt.height).MaxHeight(tt.height).Render(p.content)
			}
			if got != want {
				t.Fatalf("fallback output differs from defensive clamp: got %q want %q", got, want)
			}
			if renderedHeight := lipgloss.Height(got); renderedHeight > tt.height {
				t.Fatalf("fallback used %d content rows, want <= %d so app chrome remains visible", renderedHeight, tt.height)
			}
			if renderedWidth := lipgloss.Width(got); renderedWidth > tt.width {
				t.Fatalf("fallback used %d columns, want <= %d", renderedWidth, tt.width)
			}
		})
	}
}
