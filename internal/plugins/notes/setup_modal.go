package notes

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tdsetup"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	setupActionInitialize  = "initialize-td"
	setupActionPreferences = "notes-preferences"
	setupActionDismiss     = "not-now"
)

// ensureSetupModal follows the shared modal lifecycle rule: Update calls it
// before handling the first key, and View calls it before rendering.
func (p *Plugin) ensureSetupModal() {
	if !p.showSetupModal {
		return
	}
	if p.setupMouseHandler == nil {
		p.setupMouseHandler = mouse.NewHandler()
	}
	modalW := 68
	if modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 34 {
		modalW = 34
	}
	if p.setupModal != nil && p.setupModalWidth == modalW {
		return
	}
	p.setupModalWidth = modalW
	m := modal.New("Set up Notes",
		modal.WithWidth(modalW),
		modal.WithVariant(modal.VariantInfo),
		modal.WithPrimaryAction(setupActionInitialize),
	).
		AddSection(modal.Text("Notes stores project notes in td. Initializing creates a local .todos folder and td may add .todos/ to .gitignore."))
	m = m.AddSection(modal.Spacer()).
		AddSection(modal.Text("Sidecar will answer no to td's optional agent-guidance prompt. AGENTS.md and CLAUDE.md will not be modified."))
	if p.setupErr != nil {
		m = m.AddSection(modal.Spacer()).
			AddSection(modal.Text("Could not initialize td: " + p.setupErr.Error()))
	}
	m = m.AddSection(modal.Spacer())
	if p.setupInitializing {
		m = m.AddSection(modal.Text("Initializing td…")).
			AddSection(modal.Buttons(
				modal.Btn(" Notes preferences ", setupActionPreferences),
				modal.Btn(" Not now ", setupActionDismiss),
			))
	} else {
		m = m.AddSection(modal.Buttons(
			modal.Btn(" Initialize td ", setupActionInitialize, modal.BtnPrimary()),
			modal.Btn(" Notes preferences ", setupActionPreferences),
			modal.Btn(" Not now ", setupActionDismiss),
		))
	}
	p.setupModal = m
}

func (p *Plugin) clearSetupModal() {
	p.setupModal = nil
	p.setupModalWidth = 0
}

func (p *Plugin) renderSetupModal() string {
	background := p.renderInitMessage()
	p.ensureSetupModal()
	if p.setupModal == nil {
		return background
	}
	content := p.setupModal.Render(p.width, p.height, p.setupMouseHandler)
	return ui.OverlayModal(background, content, p.width, p.height)
}

func (p *Plugin) handleSetupAction(action string) tea.Cmd {
	switch action {
	case setupActionInitialize:
		if p.setupInitializing || p.ctx == nil {
			return nil
		}
		p.setupInitializing = true
		p.setupErr = nil
		p.clearSetupModal()
		projectRoot := p.ctx.ProjectRoot
		epoch := p.ctx.Epoch
		return func() tea.Msg {
			return tdsetup.ResultMsg{
				ProjectRoot: projectRoot,
				Origin:      tdsetup.OriginNotes,
				Epoch:       epoch,
				Err:         tdsetup.Initialize(projectRoot),
			}
		}
	case setupActionPreferences:
		p.showSetupModal = false
		p.setupDismissed = true
		p.clearSetupModal()
		return app.OpenNotesPreferences()
	case setupActionDismiss, "cancel":
		p.showSetupModal = false
		p.setupDismissed = true
		p.clearSetupModal()
		return nil
	}
	return nil
}

func (p *Plugin) handleSetupModalKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	p.ensureSetupModal()
	if p.setupModal == nil {
		return nil, false
	}
	// A setup result and a key can be delivered before Bubble Tea has painted
	// the new modal. Seed its focus order here so that first key is not lost.
	p.setupModal.Render(p.width, p.height, p.setupMouseHandler)
	action, cmd := p.setupModal.HandleKey(msg)
	if action != "" {
		return p.handleSetupAction(action), true
	}
	return cmd, true
}

func (p *Plugin) handleSetupModalMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	p.ensureSetupModal()
	if p.setupModal == nil {
		return nil, false
	}
	action := p.setupModal.HandleMouse(msg, p.setupMouseHandler)
	if action != "" {
		return p.handleSetupAction(action), true
	}
	return nil, true
}
