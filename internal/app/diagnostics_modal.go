package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
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

	m.diagnosticsModal = modal.New("Sidecar",
		modal.WithWidth(modalW),
		modal.WithHints(false),
	).
		AddSection(m.diagnosticsLogoSection()).
		AddSection(m.diagnosticsPluginsSection()).
		AddSection(modal.Spacer()).
		AddSection(m.diagnosticsSystemSection()).
		AddSection(modal.Spacer()).
		AddSection(m.diagnosticsVersionSection()).
		AddSection(m.diagnosticsUpdateSection()).
		AddSection(m.diagnosticsErrorSection()).
		AddSection(m.diagnosticsHintsSection())
}

// clearDiagnosticsModal clears the diagnostics modal state.
func (m *Model) clearDiagnosticsModal() {
	m.diagnosticsModal = nil
	m.diagnosticsModalWidth = 0
	m.diagnosticsMouseHandler = nil
}

// diagnosticsLogoSection renders the Sidecar ASCII art logo.
func (m *Model) diagnosticsLogoSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		logo := `   _____ _     __
  / ___/(_)___/ /__  _________ ______
  \__ \/ / __  / _ \/ ___/ __ \/ ___/
 ___/ / / /_/ /  __/ /__/ /_/ / /
/____/_/\__,_/\___/\___/\__,_/_/     `
		return modal.RenderedSection{Content: styles.Logo.Render(logo)}
	}, nil)
}

// diagnosticsPluginsSection renders the plugins status section.
func (m *Model) diagnosticsPluginsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var b strings.Builder
		b.WriteString(styles.Title.Render("Plugins"))
		b.WriteString("\n")

		plugins := m.registry.Plugins()
		for _, p := range plugins {
			status := styles.StatusCompleted.Render("✓")
			fmt.Fprintf(&b, "  %s %s: active\n", status, p.Name())

			// Check for plugin-specific diagnostics
			if dp, ok := p.(plugin.DiagnosticProvider); ok {
				for _, d := range dp.Diagnostics() {
					var statusIcon string
					switch d.Status {
					case "ok":
						statusIcon = styles.StatusCompleted.Render("•")
					case "warning":
						statusIcon = styles.StatusModified.Render("•")
					case "error":
						statusIcon = styles.StatusBlocked.Render("•")
					default:
						statusIcon = styles.Muted.Render("•")
					}
					fmt.Fprintf(&b, "    %s %s\n", statusIcon, d.Detail)
				}
			}
		}

		unavail := m.registry.Unavailable()
		for id, reason := range unavail {
			status := styles.StatusBlocked.Render("✗")
			fmt.Fprintf(&b, "  %s %s: %s\n", status, id, reason)
		}

		if len(plugins) == 0 && len(unavail) == 0 {
			b.WriteString(styles.Muted.Render("  No plugins registered\n"))
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
			// commands are a separate installation Sidecar will not perform.
			d := version.TasksDescriptor()
			return fmt.Sprintf("%s %s\n%s", label,
				styles.Muted.Render("embedded only · standalone not installed"),
				styles.Muted.Render("             install: "+d.InstallHint()))
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

		b.WriteString("\n  ")
		b.WriteString(styles.Muted.Render("  Press "))
		b.WriteString(styles.KeyHint.Render("u"))
		b.WriteString(styles.Muted.Render(" to view details and update"))

		return modal.RenderedSection{Content: b.String()}
	}, nil)
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

// diagnosticsHintsSection renders the close hint.
func (m *Model) diagnosticsHintsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		return modal.RenderedSection{Content: "\n" + styles.Subtle.Render("Press ! or esc to close")}
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
	case "update":
		if m.hasUpdatesAvailable() && !m.updateInProgress && !m.needsRestart {
			m.openUpdatePreview()
			return m, nil
		}
	}
	return m, nil
}
