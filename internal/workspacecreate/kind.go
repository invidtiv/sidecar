package workspacecreate

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/styles"
)

// KindFromClickX maps a click on the kind toggle to Shell (left half) or
// Worktree (right half). regionX/regionW are the toggle's hit-region bounds.
func KindFromClickX(x, regionX, regionW int) Kind {
	if regionW > 0 && x >= regionX+regionW/2 {
		return KindWorktree
	}
	return KindShell
}

func kindToggle(id string, selected *Kind, onChange func()) modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		sel := KindShell
		if selected != nil {
			sel = *selected
		}
		focused := focusID == id
		shellStyle, treeStyle := styles.Button, styles.Button
		if focused {
			if sel == KindWorktree {
				treeStyle = styles.ButtonFocused
			} else {
				shellStyle = styles.ButtonFocused
			}
		} else if sel == KindWorktree {
			treeStyle = styles.ButtonHover
		} else {
			shellStyle = styles.ButtonHover
		}
		shell := shellStyle.Render(" Shell ")
		sep := styles.Muted.Render(" | ")
		tree := treeStyle.Render(" Worktree ")
		content := lipgloss.JoinHorizontal(lipgloss.Top, shell, sep, tree)
		if ansi.StringWidth(content) > contentWidth && contentWidth > 0 {
			content = ansi.Truncate(content, contentWidth, "…")
		}
		return modal.RenderedSection{
			Content: content,
			Focusables: []modal.FocusableInfo{{
				ID: id, OffsetX: 0, OffsetY: 0,
				Width:  ansi.StringWidth(content),
				Height: 1,
			}},
		}
	}, func(msg tea.Msg, focusID string) (string, tea.Cmd) {
		if focusID != id || selected == nil {
			return "", nil
		}
		key, ok := msg.(tea.KeyPressMsg)
		if !ok {
			return "", nil
		}
		prev := *selected
		switch key.String() {
		case "left", "h", "k":
			*selected = KindShell
		case "right", "l", "j":
			*selected = KindWorktree
		}
		if *selected != prev && onChange != nil {
			onChange()
		}
		return "", nil
	})
}
