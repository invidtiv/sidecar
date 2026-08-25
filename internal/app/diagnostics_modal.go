package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/version"
)

// ensureDiagnosticsModal builds/rebuilds the diagnostics modal.
func (m *Model) ensureDiagnosticsModal() {
	modalW := 55
	if modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}

	// Only rebuild if modal doesn't exist or width changed
	if m.diagnosticsModal != nil && m.diagnosticsModalWidth == modalW {
		return
	}
	m.diagnosticsModalWidth = modalW

	m.diagnosticsModal = modal.New("",
		modal.WithWidth(modalW),
		modal.WithHints(false),
	).
		AddSection(m.diagnosticsLogoSection()).
		AddSection(modal.Spacer()).
		AddSection(m.diagnosticsPluginsSection()).
		AddSection(modal.Spacer()).
		AddSection(m.diagnosticsSystemSection()).
		AddSection(modal.Spacer()).
		AddSection(m.diagnosticsVersionSection()).
		AddSection(m.diagnosticsUpdateSection()).
		AddSection(m.diagnosticsErrorSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
			return m.renderDiagnosticsChips(contentWidth, focusID, hoverID)
		}, m.diagnosticsChipsSectionUpdate))
}

// clearDiagnosticsModal clears the diagnostics modal state.
func (m *Model) clearDiagnosticsModal() {
	m.diagnosticsModal = nil
	m.diagnosticsModalWidth = 0
	m.diagnosticsMouseHandler = nil
}

// diagnosticsLogoSection renders the Sidecar ASCII art logo centered in the modal.
func (m *Model) diagnosticsLogoSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		rawLines := []string{
			`   _____ _     __`,
			`  / ___/(_)___/ /__  _________ ______`,
			`  \__ \/ / __  / _ \/ ___/ __ \/ ___/`,
			` ___/ / / /_/ /  __/ /__/ /_/ / /`,
			`/____/_/\__,_/\___/\___/\__,_/_/`,
		}
		maxW := 0
		for _, l := range rawLines {
			if len(l) > maxW {
				maxW = len(l)
			}
		}
		pad := max(0, (contentWidth-maxW)/2)
		var b strings.Builder
		for i, l := range rawLines {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString(l)
		}
		return modal.RenderedSection{Content: styles.Logo.Render(b.String())}
	}, nil)
}

// diagnosticsPluginsSection renders the plugins status section as a table.
func (m *Model) diagnosticsPluginsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var b strings.Builder
		b.WriteString(styles.Title.Render("Plugins"))
		b.WriteString("\n")

		// surfacePlugins, not the registry alone: the global Tasks host reports
		// its own health (unconfigured, unreadable store) and dropping it out of
		// the registry must not drop it out of diagnostics.
		plugins := m.surfacePlugins()
		unavail := m.registry.Unavailable()

		type pluginItem struct {
			icon string
			name string
		}
		var items []pluginItem
		for _, p := range plugins {
			items = append(items, pluginItem{
				icon: styles.StatusCompleted.Render("✓"),
				name: p.Name(),
			})
		}
		for id := range unavail {
			items = append(items, pluginItem{
				icon: styles.StatusBlocked.Render("✗"),
				name: id,
			})
		}

		if len(items) == 0 {
			b.WriteString(styles.Muted.Render("  No plugins registered\n"))
			return modal.RenderedSection{Content: strings.TrimSuffix(b.String(), "\n")}
		}

		col1Width := contentWidth / 2
		if col1Width < 15 {
			col1Width = 15
		}

		for i := 0; i < len(items); i += 2 {
			left := items[i].icon + " " + items[i].name
			leftWidth := lipgloss.Width(left)

			if i+1 < len(items) {
				right := items[i+1].icon + " " + items[i+1].name
				pad := col1Width - leftWidth - 2
				if pad < 2 {
					pad = 2
				}
				fmt.Fprintf(&b, "  %s%s%s\n", left, strings.Repeat(" ", pad), right)
			} else {
				fmt.Fprintf(&b, "  %s\n", left)
			}
		}

		return modal.RenderedSection{Content: strings.TrimSuffix(b.String(), "\n")}
	}, nil)
}

// diagnosticsSystemSection renders the system info section.
func (m *Model) diagnosticsSystemSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var b strings.Builder
		b.WriteString(styles.Title.Render("System"))
		b.WriteString("\n")
		fmt.Fprintf(&b, "  WorkDir: %s\n", styles.Muted.Render(m.ui.WorkDir))
		fmt.Fprintf(&b, "  Refresh: %s", styles.Muted.Render(m.ui.LastRefresh.Format("15:04:05")))
		return modal.RenderedSection{Content: b.String()}
	}, nil)
}

// diagnosticsVersionSection lists every product Sidecar knows how to update,
// with its version, availability, and install provenance.
func (m *Model) diagnosticsVersionSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var b strings.Builder
		b.WriteString(styles.Title.Render("Version"))

		for _, t := range m.products {
			b.WriteString("\n")
			b.WriteString(m.diagnosticsProductRow(t))
		}

		return modal.RenderedSection{Content: b.String()}
	}, nil)
}

// diagnosticsProductRow renders one product's line in diagnostics.
func (m *Model) diagnosticsProductRow(t version.Target) string {
	label := fmt.Sprintf("  %-9s", strings.ToLower(t.DisplayName)+":")

	switch {
	case !t.Installed:
		if t.Product == version.ProductTasks {
			// Sidecar embeds the Tasks TUI at build time; the standalone
			// commands are a separate install. Configuration → Panels runs
			// that install after the user confirms the command.
			d := version.TasksDescriptor()
			return fmt.Sprintf("%s %s\n%s", label,
				styles.Muted.Render("embedded only · standalone not installed"),
				styles.Muted.Render("             Panels → Install Tasks, or: "+d.InstallHint()))
		}
		return label + " " + styles.Muted.Render("not installed")

	case t.CheckFailed:
		return fmt.Sprintf("%s %s %s", label,
			styles.Muted.Render(t.CurrentVersion),
			styles.Muted.Render("· update status unknown"))

	case t.HasUpdate:
		return fmt.Sprintf("%s %s → %s %s %s", label,
			styles.Muted.Render(t.CurrentVersion),
			t.LatestVersion,
			styles.StatusModified.Render("available"),
			styles.Muted.Render("· "+m.provenanceLabel(t)))

	default:
		return fmt.Sprintf("%s %s %s %s", label,
			styles.Muted.Render(t.CurrentVersion),
			styles.StatusCompleted.Render("✓"),
			styles.Muted.Render("· "+m.provenanceLabel(t)))
	}
}

// provenanceLabel describes how a product would be updated.
func (m *Model) provenanceLabel(t version.Target) string {
	if !t.Install.Managed {
		if t.Install.Detail != "" {
			return t.Install.Detail + " · manual"
		}
		return "manual"
	}
	return t.Install.Method.String()
}

// diagnosticsUpdateSection renders the update status/hint section.
func (m *Model) diagnosticsUpdateSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		count := m.availableUpdateCount()
		if count == 0 && !m.needsRestart {
			return modal.RenderedSection{}
		}

		var b strings.Builder

		if m.needsRestart {
			b.WriteString("\n  ")
			b.WriteString(styles.StatusCompleted.Render("✓ "))
			b.WriteString("Update complete. ")
			b.WriteString(styles.StatusModified.Render("Restart sidecar to use new version"))
			return modal.RenderedSection{Content: b.String()}
		}

		b.WriteString("\n  ")
		b.WriteString(styles.StatusModified.Render("⬆ "))
		if count == 1 {
			b.WriteString("1 update available")
		} else {
			fmt.Fprintf(&b, "%d updates available", count)
		}

		return modal.RenderedSection{Content: b.String()}
	}, nil)
}

// diagnosticsChips is the one inline action line in the footer hint style:
// [u] Update when an update is actually available, [esc] Close always. The
// keyboard's u keeps its own path, so the chip is the mouse/focus twin of a
// key that already works.
func (m *Model) diagnosticsChips() []ui.KeyChip {
	chips := []ui.KeyChip{{Keys: "[esc]", Label: "Close", ID: "close"}}
	if m.hasUpdatesAvailable() {
		chips = append([]ui.KeyChip{{Keys: "[u]", Label: "Update", ID: "update"}}, chips...)
	}
	return chips
}

// renderDiagnosticsChips paints the action line, registering each chip as a
// real focusable control so a click and Enter both fire its action — the same
// shape the update journey's chips use.
func (m *Model) renderDiagnosticsChips(contentW int, _, _ string) modal.RenderedSection {
	line, regions := ui.RenderKeyChips(m.diagnosticsChips(), contentW)
	if line == "" {
		return modal.RenderedSection{}
	}
	focusables := make([]modal.FocusableInfo, 0, len(regions))
	for _, r := range regions {
		focusables = append(focusables, modal.FocusableInfo{
			ID:      r.ID,
			OffsetX: r.OffsetX,
			Width:   r.Width,
			Height:  1,
		})
	}
	return modal.RenderedSection{Content: line, Focusables: focusables}
}

// diagnosticsChipsSectionUpdate fires a focused chip's action on Enter or
// space; HandleKey routes the returned ID through the same switch as clicks.
func (m *Model) diagnosticsChipsSectionUpdate(msg tea.Msg, focusID string) (string, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || focusID == "" {
		return "", nil
	}
	switch key.String() {
	case "enter", " ", "space":
		return focusID, nil
	}
	return "", nil
}

// diagnosticsErrorSection renders the last error section if present.
func (m *Model) diagnosticsErrorSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if m.lastError == nil {
			return modal.RenderedSection{}
		}
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(styles.Title.Render("Last Error"))
		b.WriteString("\n")
		b.WriteString(styles.StatusBlocked.Render(fmt.Sprintf("  %s", m.lastError.Error())))
		return modal.RenderedSection{Content: b.String()}
	}, nil)
}

// renderDiagnosticsModal renders the diagnostics modal.
func (m *Model) renderDiagnosticsModal(content string) string {
	m.ensureDiagnosticsModal()
	if m.diagnosticsModal == nil {
		return content
	}

	if m.diagnosticsMouseHandler == nil {
		m.diagnosticsMouseHandler = mouse.NewHandler()
	}
	modalContent := m.diagnosticsModal.Render(m.width, m.height, m.diagnosticsMouseHandler)
	return ui.OverlayModal(content, modalContent, m.width, m.height)
}

// handleDiagnosticsModalMouse handles mouse events for the diagnostics modal.
func (m *Model) handleDiagnosticsModalMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.ensureDiagnosticsModal()
	if m.diagnosticsModal == nil {
		return m, nil
	}
	if m.diagnosticsMouseHandler == nil {
		m.diagnosticsMouseHandler = mouse.NewHandler()
	}
	action := m.diagnosticsModal.HandleMouse(msg, m.diagnosticsMouseHandler)
	switch action {
	case "close", "cancel":
		m.showDiagnostics = false
		return m, nil
	case "update":
		// Same convergence as the keyboard path: reopen the updater in its
		// current phase, including mid-batch.
		if m.openUpdateModal() {
			m.updateContext()
			return m, nil
		}
	}
	return m, nil
}
