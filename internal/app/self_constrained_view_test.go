package app

import (
	"strings"
	"testing"

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
