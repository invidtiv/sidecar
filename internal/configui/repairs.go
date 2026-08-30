package configui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/configchecks"
)

// Focused repair routes. Each one states what was detected, why it matters, and
// what an action will do before it does it. None of them installs anything,
// none of them uses sudo, and none of them writes to a file the user has not
// been shown the exact change to.

// confirmState is a consequential change waiting for an explicit yes. While one
// is open the surface reports the config-confirm context, so the footer and the
// palette describe the only two things that can happen next.
type confirmState struct {
	title  string
	intro  []string
	body   []string
	footer []string
	// apply runs only after the user confirms.
	apply func(*Model) tea.Cmd
}

// buildChild paints the visible child route, back control first.
func (m *Model) buildChild(b *paneBuilder, route Route) {
	title := route.Title
	if m.confirm != nil {
		title = m.confirm.title
	}
	// A route that is about a beta integration says so beside its title, the
	// way the mockup does: a badge stranded on the line below reads as content
	// rather than as a qualifier on the title.
	heading := PaneTitle(title)
	if route.Child == ChildEnableIntegration && m.confirm == nil && m.enable != nil {
		heading += "  " + BetaBadge()
	}
	b.rightControl(heading, regionBack, "", "←  Back to "+m.router.parentLabel(), func(m *Model) tea.Cmd {
		// The back control means exactly what Escape means here, so it tears the
		// route down the same way. Leaving a draft behind would strand a form the
		// user can no longer see — and, with it, its theme preview.
		m.closeProjectForm()
		m.closeRemoteForm()
		m.Back()
		return nil
	})
	b.blank()

	if m.confirm != nil {
		m.buildConfirm(b)
		return
	}

	switch route.Child {
	case ChildAddProject, ChildEditProject:
		m.buildProjectForm(b)
	case ChildAddRemote, ChildEditRemote:
		m.buildRemoteForm(b)
	case ChildRepairTmux:
		m.buildTmuxRepair(b)
	case ChildRepairTerminalColors:
		m.buildColorRepair(b)
	case ChildRepairAgentInstructions:
		m.buildAgentRepair(b)
	case ChildRepairConfiguration:
		m.buildConfigRepair(b)
	case ChildEnableIntegration:
		m.buildEnableRoute(b)
	case ChildNotificationAgentRules:
		m.buildNotificationAgentRules(b)
	case ChildNotificationOtherRules:
		m.buildNotificationOtherRules(b)
	case ChildNotificationQuietHours:
		m.buildNotificationQuietHours(b)
	case ChildNotificationSoundPaths:
		m.buildNotificationSoundPaths(b)
	case ChildNotificationStatus:
		m.buildNotificationStatus(b)
	case ChildNotificationSourceRule:
		m.buildNotificationSourceRule(b)
	case ChildNotificationSSH:
		m.buildNotificationSSH(b)
	default:
		b.lead("This focused route arrives in a later phase.")
	}
}

func (m *Model) buildConfirm(b *paneBuilder) {
	confirm := m.confirm
	for _, line := range confirm.intro {
		b.text(line)
	}
	b.blank()
	for _, line := range confirm.body {
		b.text(line)
	}
	b.blank()
	b.buttons(
		buttonSpec{id: "confirm-apply", key: "enter", label: "Enter  Apply", primary: true, run: func(m *Model) tea.Cmd {
			if m.confirm == nil {
				return nil
			}
			apply := m.confirm.apply
			m.confirm = nil
			m.rowCursor = 0
			if apply == nil {
				return nil
			}
			return apply(m)
		}},
		buttonSpec{id: "confirm-cancel", key: "n", label: "Esc  Cancel", run: func(m *Model) tea.Cmd {
			m.confirm = nil
			m.rowCursor = 0
			return nil
		}},
	)
	if len(confirm.footer) > 0 {
		b.blank()
		for _, line := range confirm.footer {
			b.text(line)
		}
	}
	// y is the other half of the confirm context's yes/no pair. Like every
	// control, it is answered from a frame that may be one event out of date, so
	// it asks whether the confirmation is still open rather than assuming it.
	b.declare("confirm-yes", "y", false, func(m *Model) tea.Cmd {
		if m.confirm == nil {
			return nil
		}
		apply := m.confirm.apply
		m.confirm = nil
		m.rowCursor = 0
		if apply == nil {
			return nil
		}
		return apply(m)
	})
}

// DismissConfirm cancels a pending confirmation. Escape reaches it before it
// means "go back": the change on screen is what the user is answering.
func (m *Model) DismissConfirm() bool {
	if m.confirm == nil {
		return false
	}
	m.confirm = nil
	m.rowCursor = 0
	return true
}

// --- tmux (mockup 01a) ---------------------------------------------------

func (m *Model) buildTmuxRepair(b *paneBuilder) {
	result := m.result(configchecks.CheckTmux)
	env := m.checkInput.Env
	command := configchecks.TmuxInstallCommand(env)
	prefillable := configchecks.TmuxRepairPrefillable(env)

	observed := "tmux is not available."
	if result.Summary != "" {
		observed = result.Summary
	}
	b.text(Warning(observed))
	b.lead("Workspaces, shells, and the embedded terminal use tmux to stay reliable.")
	if len(result.Evidence) > 0 {
		b.blank()
		for _, line := range result.Evidence {
			b.note(line)
		}
	}

	b.text(SectionHeader("Recommended fix"))
	if prefillable {
		b.text(Indented("This Mac appears to use Homebrew. Sidecar can open a new shell with:"))
	} else {
		b.text(Indented("Install tmux " + configchecks.MinTmuxVersion + " or newer, then return here:"))
	}
	b.blank()
	b.text(IndentedRaw(CodeChip(command)))
	b.blank()
	if prefillable {
		b.note("The command is prefilled, never run automatically, and never uses sudo.")
	} else {
		// Without Homebrew there is nothing to prefill: the recommendation is a
		// place to read, and saying "prefilled" about it would describe a shell
		// this route never offers to open.
		b.note("Sidecar installs nothing for you and never uses sudo.")
	}

	b.text(SectionHeader("Next step"))
	copyLabel, copyNotice := "C  Copy command", "Copied install command"
	if !prefillable {
		copyLabel, copyNotice = "C  Copy instructions", "Copied install instructions"
	}
	var specs []buttonSpec
	if prefillable {
		specs = append(specs, buttonSpec{
			id: "tmux-open-shell", key: "enter", label: "Enter  Open install shell", primary: true,
			run: func(m *Model) tea.Cmd {
				return func() tea.Msg { return OpenShellMsg{Command: command} }
			},
		})
	}
	specs = append(specs,
		buttonSpec{id: "tmux-copy", key: "c", label: copyLabel, run: func(m *Model) tea.Cmd {
			return copyCmd(command, copyNotice)
		}},
		buttonSpec{id: "tmux-recheck", key: "r", label: "R  Recheck", run: func(m *Model) tea.Cmd {
			return m.Recheck()
		}},
		buttonSpec{id: "tmux-not-now", key: "", label: "Esc  Not now", run: func(m *Model) tea.Cmd {
			m.Back()
			return nil
		}},
	)
	b.buttons(specs...)
	b.blank()
	b.lead("After installing, return here and choose Recheck. Sidecar never infers success from a shell closing.")
}

// --- terminal colors (mockup 01b) ---------------------------------------

func (m *Model) buildColorRepair(b *paneBuilder) {
	result := m.result(configchecks.CheckTerminalColors)
	guide, recognized := configchecks.IdentifyTerminal(m.checkInput.Env)

	b.text(Warning("Truecolor is not available"))
	detail := "Sidecar detected a terminal that is not advertising 24-bit color."
	if recognized {
		detail = "Sidecar detected " + guide.Name + ", which is not advertising 24-bit color."
	}
	b.text(Indented(detail))

	if len(result.Evidence) > 0 {
		b.text(SectionHeader("What Sidecar saw"))
		for _, line := range result.Evidence {
			b.note(line)
		}
	}

	b.text(SectionHeader("What to do"))
	b.text(Indented("Enable True Color or 24-bit color in your terminal's profile, then restart it."))
	b.note("Sidecar will not change terminal-emulator or shell configuration for you.")

	if m.showColorSteps {
		heading := "Color setup steps"
		if recognized {
			heading += " for " + guide.Name
		}
		b.text(SectionHeader(heading))
		for _, step := range guide.Steps {
			b.note("• " + step)
		}
		if !recognized {
			b.blank()
			b.note("Sidecar does not recognize this terminal, so these are the general steps.")
		}
	}

	stepsLabel := "Enter  View color setup steps"
	if m.showColorSteps {
		stepsLabel = "Enter  Hide color setup steps"
	}
	b.blank()
	b.buttons(
		buttonSpec{id: "colors-steps", key: "enter", label: stepsLabel, primary: true, run: func(m *Model) tea.Cmd {
			m.showColorSteps = !m.showColorSteps
			return nil
		}},
		buttonSpec{id: "colors-copy", key: "c", label: "C  Copy instructions", run: func(m *Model) tea.Cmd {
			return copyCmd(guide.Instructions(), "Copied color setup steps")
		}},
	)
	b.buttons(
		buttonSpec{id: "colors-recheck", key: "r", label: "R  Recheck", run: func(m *Model) tea.Cmd {
			return m.Recheck()
		}},
		buttonSpec{id: "colors-back", key: "", label: "Esc  Back", run: func(m *Model) tea.Cmd {
			m.Back()
			return nil
		}},
	)
}

// --- agent instructions (mockup 05a) ------------------------------------

func (m *Model) buildAgentRepair(b *paneBuilder) {
	result := m.result(configchecks.CheckAgentInstructions)
	path := configchecks.AgentInstructionsFile(m.checkInput.Env, m.checkInput.ProjectDir)
	name := m.checkInput.ProjectName
	if name == "" {
		name = "this project"
	}
	fileName := "AGENTS.md"
	if path != "" {
		fileName = baseName(path)
	}

	if result.OK {
		b.text(Body(fileName + " already points agents at `sidecar --agents` for " + name + "."))
		b.note("Nothing needs to change. Open the file if you want to read or extend it.")
	} else {
		b.text(Warning("Needs attention"))
		b.text(Indented(fileName + " does not include Sidecar guidance for " + name + "."))
		b.note("Add one line so agents can self-serve current Sidecar guidance.")
	}
	if path != "" {
		b.blank()
		b.note(path)
	}

	if !result.OK {
		b.text(SectionHeader("Recommended repair"))
		b.text(Indented("Review the one-line addition before it changes any project file."))
	}

	b.blank()
	var specs []buttonSpec
	if !result.OK && path != "" {
		specs = append(specs, buttonSpec{
			id: "agents-review", key: "enter", label: "Enter  Review guidance", primary: true,
			run: func(m *Model) tea.Cmd { m.reviewAgentInstructions(path, fileName); return nil },
		})
	}
	specs = append(specs, buttonSpec{
		id: "agents-copy", key: "c", label: "C  Copy guidance", run: func(m *Model) tea.Cmd {
			return copyCmd(configchecks.AgentInstructionLine, "Copied agent guidance")
		},
	})
	specs = append(specs,
		buttonSpec{id: "agents-open", key: "o", label: "O  Open " + fileName, run: func(m *Model) tea.Cmd {
			if path == "" {
				return nil
			}
			return func() tea.Msg { return OpenFileMsg{Path: path} }
		}},
		buttonSpec{id: "agents-recheck", key: "r", label: "R  Recheck", run: func(m *Model) tea.Cmd {
			return m.Recheck()
		}},
		buttonSpec{id: "agents-back", key: "", label: "Esc  Back", run: func(m *Model) tea.Cmd {
			m.Back()
			return nil
		}},
	)
	// Two rows of actions, as the mockup lays them out, without leaving a row
	// holding a single lonely pill when the review action is absent.
	split := (len(specs) + 1) / 2
	b.buttons(specs[:split]...)
	b.buttons(specs[split:]...)
	if !result.OK {
		b.blank()
		b.lead("Review preserves existing instructions and requires confirmation before saving.")
	}
}

// reviewAgentInstructions shows the exact addition and asks. Nothing is written
// until this confirmation is answered yes; there is no path to the write that
// skips it.
func (m *Model) reviewAgentInstructions(path, fileName string) {
	m.confirm = &confirmState{
		title: "Review guidance",
		intro: []string{
			Body("Sidecar will add this one line to " + fileName + ":"),
			IndentedMuted(path),
		},
		body: []string{
			IndentedRaw(CodeChip(configchecks.AgentInstructionLine)),
			"",
			IndentedMuted("Existing instructions are preserved. The line is placed after any"),
			IndentedMuted("frontmatter and the file's own heading. Nothing else is changed."),
		},
		footer: []string{
			Muted("`sidecar --agents` stays the single source of current guidance, so the file points at it."),
		},
		apply: func(m *Model) tea.Cmd {
			return tea.Batch(writeAgentInstructionsCmd(path), m.Recheck())
		},
	}
	m.rowCursor = 0
}

func writeAgentInstructionsCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := configchecks.AddAgentInstructions(path); err != nil {
			return NoticeMsg{Message: "Could not update " + baseName(path) + ": " + err.Error()}
		}
		return NoticeMsg{Message: "Added Sidecar guidance to " + baseName(path)}
	}
}

// --- configuration recovery (mockup 09a) --------------------------------

func (m *Model) buildConfigRepair(b *paneBuilder) {
	result := m.result(configchecks.CheckConfiguration)
	path := m.checkInput.ConfigPath

	b.text(Warning("Sidecar could not read your configuration"))
	b.text(Indented("Sidecar is running on the configuration it loaded at startup."))
	b.note("It will not rewrite the file for you, and it will not save over an error it cannot read.")

	if len(result.Evidence) > 0 {
		b.text(SectionHeader("What Sidecar saw"))
		for _, line := range result.Evidence {
			b.note(line)
		}
		if path == "" {
			path = result.Evidence[0]
		}
	}

	b.text(SectionHeader("What to do"))
	b.text(Indented("Open the file, fix the reported error, then recheck."))

	details := strings.Join(append([]string{result.Summary}, result.Evidence...), "\n")
	b.blank()
	b.buttons(
		buttonSpec{id: "config-open", key: "enter", label: "Enter  Open config file", primary: true, run: func(m *Model) tea.Cmd {
			if path == "" {
				return nil
			}
			return func() tea.Msg { return OpenFileMsg{Path: path} }
		}},
		buttonSpec{id: "config-copy", key: "c", label: "C  Copy details", run: func(m *Model) tea.Cmd {
			return copyCmd(details, "Copied configuration error")
		}},
	)
	b.buttons(
		buttonSpec{id: "config-recheck", key: "r", label: "R  Recheck", run: func(m *Model) tea.Cmd {
			return m.Recheck()
		}},
		buttonSpec{id: "config-back", key: "", label: "Esc  Back", run: func(m *Model) tea.Cmd {
			m.Back()
			return nil
		}},
	)
}

func copyCmd(text, notice string) tea.Cmd {
	return func() tea.Msg { return CopyMsg{Text: text, Notice: notice} }
}

func baseName(path string) string {
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return path[index+1:]
	}
	return path
}
