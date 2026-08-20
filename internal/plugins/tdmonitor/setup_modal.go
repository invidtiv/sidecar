package tdmonitor

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/td/pkg/monitor"
	"github.com/marcus/td/pkg/monitor/modal"
	"github.com/marcus/td/pkg/monitor/mouse"

	"github.com/marcus/sidecar/internal/tdsetup"
)

// SetupModel handles the setup modal when td is on PATH but not initialized in project.
type SetupModel struct {
	baseDir string
	epoch   uint64
	width   int
	height  int

	// Modal state
	modal        *modal.Modal
	mouseHandler *mouse.Handler

	// Checkbox states
	initDB              bool
	installInstructions bool
}

// SetupSkippedMsg is sent when user skips setup.
type SetupSkippedMsg struct{}

// NewSetupModel creates a new setup modal model.
func NewSetupModel(baseDir string, epoch uint64) *SetupModel {
	m := &SetupModel{
		baseDir:             baseDir,
		epoch:               epoch,
		mouseHandler:        mouse.NewHandler(),
		initDB:              true,
		installInstructions: true,
	}
	m.buildModal()
	return m
}

// buildModal creates the declarative modal.
func (m *SetupModel) buildModal() {
	m.modal = modal.New("Set up td", modal.WithWidth(55)).
		AddSection(modal.Text("td is installed but not configured for this project.")).
		AddSection(modal.Spacer()).
		AddSection(modal.Checkbox("init-db", "Initialize td database", &m.initDB)).
		AddSection(modal.Checkbox("install-instructions", "Add instructions to agent file", &m.installInstructions)).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Set Up ", "setup"),
			modal.Btn(" Skip ", "skip"),
		))
	m.modal.Reset()
}

// Init returns the initial command.
func (m *SetupModel) Init() tea.Cmd {
	return nil
}

// Update handles messages for the setup modal.
func (m *SetupModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return nil

	case tea.KeyPressMsg:
		action, cmd := m.modal.HandleKey(msg)
		if action != "" {
			return m.handleAction(action)
		}
		return cmd

	case tea.MouseMsg:
		action := m.modal.HandleMouse(msg, m.mouseHandler)
		if action != "" {
			return m.handleAction(action)
		}
		return nil
	}
	return nil
}

// handleAction processes modal actions.
func (m *SetupModel) handleAction(action string) tea.Cmd {
	switch action {
	case "setup":
		return m.performSetup()
	case "skip", "cancel":
		return func() tea.Msg { return SetupSkippedMsg{} }
	case "init-db":
		// Checkbox was clicked - toggle is handled by the section
		return nil
	case "install-instructions":
		// Checkbox was clicked - toggle is handled by the section
		return nil
	}
	return nil
}

// performSetup executes the selected setup options.
func (m *SetupModel) performSetup() tea.Cmd {
	return func() tea.Msg {
		if m.initDB {
			if err := tdsetup.Initialize(m.baseDir); err != nil {
				return tdsetup.ResultMsg{
					ProjectRoot: m.baseDir,
					Origin:      tdsetup.OriginTDMonitor,
					Epoch:       m.epoch,
					Err:         err,
				}
			}
		}

		if m.installInstructions {
			agentFile := preferredAgentFile(m.baseDir)
			if !hasTDInstructions(agentFile) {
				_ = installInstructions(agentFile)
			}
		}

		return tdsetup.ResultMsg{ProjectRoot: m.baseDir, Origin: tdsetup.OriginTDMonitor, Epoch: m.epoch}
	}
}

// View renders the setup modal.
func (m *SetupModel) View(width, height int) string {
	m.width = width
	m.height = height

	// Render stallion as background
	notInstalled := NewNotInstalledModel()
	background := notInstalled.View(width, height)

	// Render modal content
	modalContent := m.modal.Render(width, height, m.mouseHandler)

	// Overlay modal on dimmed background
	return monitor.OverlayModal(background, modalContent, width, height)
}

// --- Agent instructions helpers (mirrors td/internal/agent/instructions.go) ---

// instructionText is the mandatory td usage instructions to add to agent files.
const instructionText = `## MANDATORY: Use td for Task Management

You must run td usage --new-session at conversation start (or after /clear) to see current work.
Use td usage -q for subsequent reads.
`

// preferredAgentFile returns the best agent file to use for installation.
func preferredAgentFile(baseDir string) string {
	agentsPath := filepath.Join(baseDir, "AGENTS.md")
	claudePath := filepath.Join(baseDir, "CLAUDE.md")

	if fileExists(agentsPath) {
		return agentsPath
	}
	if fileExists(claudePath) {
		return claudePath
	}
	return agentsPath
}

// hasTDInstructions checks if the file already contains td instructions.
func hasTDInstructions(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(content), "td usage")
}

// installInstructions adds td instructions to an agent file.
func installInstructions(path string) error {
	if !fileExists(path) {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(instructionText), 0644)
	}
	return prependToFile(path, instructionText)
}

// prependToFile adds text at a smart location in the file.
func prependToFile(path string, text string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	contentStr := string(content)
	insertPos := 0

	// Skip YAML frontmatter if present
	if strings.HasPrefix(contentStr, "---") {
		if endIdx := strings.Index(contentStr[3:], "---"); endIdx != -1 {
			insertPos = endIdx + 6
			for insertPos < len(contentStr) && contentStr[insertPos] == '\n' {
				insertPos++
			}
		}
	}

	// Skip initial # heading if present
	if insertPos < len(contentStr) && contentStr[insertPos] == '#' {
		if nlIdx := strings.Index(contentStr[insertPos:], "\n"); nlIdx != -1 {
			insertPos += nlIdx + 1
			for insertPos < len(contentStr) && contentStr[insertPos] == '\n' {
				insertPos++
			}
		}
	}

	var newContent strings.Builder
	newContent.WriteString(contentStr[:insertPos])
	newContent.WriteString(text)
	newContent.WriteString("\n")
	newContent.WriteString(contentStr[insertPos:])

	return os.WriteFile(path, []byte(newContent.String()), 0644)
}

// fileExists returns true if the path exists and is a file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
