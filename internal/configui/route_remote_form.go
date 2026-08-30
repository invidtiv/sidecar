package configui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
)

// Add host is a focused child route of Remote Hosts, the way Add project is a
// child of Projects: the header, the sidebar and the footer stay where they
// were, and nothing is written until Save. Edit host is the same form with the
// values already in it.
//
// The fields are config.HostConfig exactly, and the help under each one is that
// struct's own doc comment, because those comments already answer the questions
// this form raises. In particular they explain what is NOT here: reachability.
// The target is whatever the user's ssh_config resolves — their keys, their
// ProxyJump, their agent — so Sidecar offers no second place to describe how to
// reach a machine, and a host that `ssh <target>` cannot reach is not a host
// this form can fix.

const (
	ChildAddRemote  ChildID = "add-remote"
	ChildEditRemote ChildID = "edit-remote"
)

const (
	regionRemoteFormTarget   = "config-remote-form-target"
	regionRemoteFormName     = "config-remote-form-name"
	regionRemoteFormBinary   = "config-remote-form-binary"
	regionRemoteFormConfig   = "config-remote-form-config"
	regionRemoteFormEnv      = "config-remote-form-env"
	regionRemoteFormDisabled = "config-remote-form-disabled"
	regionRemoteFormSave     = "config-remote-form-save"
	regionRemoteFormCancel   = "config-remote-form-cancel"
	remoteFormFieldWidth     = 46
)

// remoteForm is the draft. It is deliberately separate from the configuration:
// a draft that is abandoned changes nothing.
type remoteForm struct {
	edit bool
	// originalID is the host being edited, empty when adding. It is the name the
	// entry had when the form opened, which is what a rename has to be applied
	// against.
	originalID string

	target textinput.Model
	name   textinput.Model
	binary textinput.Model
	config textinput.Model
	env    textinput.Model

	// disabled is the draft's connect switch, inverted on screen: a user asks
	// to connect to a machine, not to un-disable it.
	disabled bool

	message string
}

// isRemoteFormRoute reports that the visible route is the host form, which is
// what makes an open draft the thing the keyboard answers for.
func isRemoteFormRoute(route Route) bool {
	return route.IsChild() && (route.Child == ChildAddRemote || route.Child == ChildEditRemote)
}

// OpenAddRemote opens the Add host route with Target focused: the target is the
// one field with no useful default, and every other field can be left alone.
func (m *Model) OpenAddRemote() {
	m.Navigate(PageRemotes)
	m.startRemoteForm(nil)
	m.PushChild(ChildAddRemote, "Add host")
	m.editRemoteField(regionRemoteFormTarget)
}

// OpenEditRemote opens the same form over a registered machine.
func (m *Model) OpenEditRemote(id string) {
	for _, host := range m.remotes() {
		if !strings.EqualFold(config.HostIDFor(host), id) {
			continue
		}
		entry := host
		m.startRemoteForm(&entry)
		m.PushChild(ChildEditRemote, "Edit host")
		return
	}
}

func remoteFormInput(placeholder string, limit int) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.CharLimit = limit
	return input
}

// startRemoteForm builds the draft. An edit starts from the saved entry; an add
// starts empty.
func (m *Model) startRemoteForm(host *config.HostConfig) {
	form := &remoteForm{
		target: remoteFormInput("hostname, or an ssh_config alias", 200),
		name:   remoteFormInput("same as the target", 60),
		binary: remoteFormInput("found on the host's PATH", 200),
		config: remoteFormInput("the host's own config", 200),
		env:    remoteFormInput("KEY=VALUE KEY=VALUE", 400),
	}
	if host != nil {
		normalized := config.NormalizeHost(*host)
		form.edit = true
		form.originalID = normalized.ID
		form.target.SetValue(normalized.Target)
		// The name field shows what was actually recorded, not the resolved
		// default: an entry that never named itself should stay that way when it
		// is saved again, so the placeholder keeps telling the truth.
		form.name.SetValue(strings.TrimSpace(host.ID))
		form.binary.SetValue(normalized.Binary)
		form.config.SetValue(normalized.Config)
		form.env.SetValue(strings.Join(normalized.Env, " "))
		form.disabled = normalized.Disabled
	}
	m.remoteForm = form
}

// closeRemoteForm abandons the draft.
func (m *Model) closeRemoteForm() {
	if m.remoteForm == nil {
		return
	}
	m.remoteForm = nil
	m.closeEditor()
}

// --- rendering ----------------------------------------------------------

func (m *Model) buildRemoteForm(b *paneBuilder) {
	form := m.remoteForm
	if form == nil {
		b.lead("This form is no longer open.")
		return
	}

	b.text(PaneTitle("Host details"))
	b.blank()
	if !form.edit {
		b.lead("Register another machine running Sidecar. Its shells, worktrees and agents appear in Sessions beside this machine's own.")
		b.blank()
	}

	m.buildRemoteField(b, regionRemoteFormTarget, "SSH target", &form.target,
		"Whatever `ssh <target>` already resolves here: a hostname, or an alias from your ssh_config, with its keys and ProxyJump. Sidecar adds no second place to describe how to reach a machine, so a target ssh cannot reach is not something this form can fix.")
	b.blank()

	m.buildRemoteField(b, regionRemoteFormName, "Name", &form.name,
		"What this machine is called in Sidecar, and what scopes its rows. Defaults to the target.")
	b.blank()

	m.buildRemoteField(b, regionRemoteFormBinary, "Sidecar path", &form.binary,
		"Usually unnecessary: the connection runs through a login shell, which finds a Homebrew or package-managed install. Set it when the host puts sidecar somewhere a login shell does not look.")
	b.blank()

	m.buildRemoteField(b, regionRemoteFormConfig, "Config path", &form.config,
		"An optional -config path for the remote Sidecar, so a machine can be watched against a config other than its user default.")
	b.blank()

	m.buildRemoteField(b, regionRemoteFormEnv, "Environment", &form.env,
		"Space-separated KEY=VALUE pairs for the remote Sidecar. This is how a proof host is pinned to its own tmux server and state tree: TMUX_TMPDIR, XDG_STATE_HOME, SIDECAR_ISOLATED_STATE.")
	b.blank()

	connect := b.toggleRow(regionRemoteFormDisabled, "Connect to this host", !form.disabled, m.toggleRemoteFormConnect)
	if connect.Focused {
		b.help("Off keeps the machine registered without connecting to it, which is what a machine that is off this week wants: the entry keeps its settings.")
	}
	b.blank()

	if form.message != "" {
		b.text(IndentedRaw(Warning(form.message)))
		b.blank()
	}

	saveLabel := "Enter  Add host"
	if form.edit {
		saveLabel = "Enter  Save changes"
	}
	b.buttons(
		buttonSpec{id: regionRemoteFormSave, label: saveLabel, primary: true, run: func(m *Model) tea.Cmd {
			return m.saveRemoteForm()
		}},
		buttonSpec{id: regionRemoteFormCancel, label: "Esc  Back to Remote Hosts", run: func(m *Model) tea.Cmd {
			m.closeRemoteForm()
			m.Back()
			return nil
		}},
	)
}

// buildRemoteField paints one field, with its explanation under the field the
// user is on and only there.
//
// Every field here needs a sentence — half of them exist for a reason nobody
// would guess — but the Configuration detail pane truncates rather than
// scrolling, and five fields each carrying three permanent lines of help pushed
// Save off the bottom of an ordinary terminal, which is exactly where a user
// who has just filled the form in cannot reach it. Feature Flags solved the
// same problem the same way.
func (m *Model) buildRemoteField(b *paneBuilder, id, label string, input *textinput.Model, help string) {
	editing := m.editingID() == id
	state := b.declare(id, "", true, func(m *Model) tea.Cmd {
		m.editRemoteField(id)
		return nil
	})
	b.paintRow(id, state, func(s State) string {
		if editing {
			s.Focused = true
			return FormRow(label, Field(input, b.controlWidth(remoteFormFieldWidth), s), s)
		}
		return FormRow(label, StaticField(input.Value(), b.controlWidth(remoteFormFieldWidth), s), s)
	})
	if state.Focused || editing {
		b.help(help)
	}
}

// toggleRemoteFormConnect flips the draft's connect switch. Only Save writes it.
func (m *Model) toggleRemoteFormConnect(*Model) tea.Cmd {
	if form := m.remoteForm; form != nil {
		form.disabled = !form.disabled
	}
	return nil
}

// --- fields -------------------------------------------------------------

// remoteFormFieldOrder is the order Tab and Enter walk the form in. It is
// written once so the two cannot disagree about what comes next.
var remoteFormFieldOrder = []string{
	regionRemoteFormTarget,
	regionRemoteFormName,
	regionRemoteFormBinary,
	regionRemoteFormConfig,
	regionRemoteFormEnv,
}

func (m *Model) remoteFormInputFor(id string) *textinput.Model {
	form := m.remoteForm
	if form == nil {
		return nil
	}
	switch id {
	case regionRemoteFormTarget:
		return &form.target
	case regionRemoteFormName:
		return &form.name
	case regionRemoteFormBinary:
		return &form.binary
	case regionRemoteFormConfig:
		return &form.config
	case regionRemoteFormEnv:
		return &form.env
	}
	return nil
}

// editRemoteField gives one field the keyboard. Tab and Enter both move to the
// next field, and past the last one they leave the editor on Save — a form
// whose last field submits on Enter would register a host the moment a user
// finished typing an environment variable.
func (m *Model) editRemoteField(id string) {
	input := m.remoteFormInputFor(id)
	if input == nil {
		return
	}
	advance := func(m *Model) {
		for i, field := range remoteFormFieldOrder {
			if field != id {
				continue
			}
			if i+1 < len(remoteFormFieldOrder) {
				m.editRemoteField(remoteFormFieldOrder[i+1])
				return
			}
		}
		m.closeEditor()
		m.focusControlByID(regionRemoteFormSave)
	}
	m.openEditor(&editorState{
		id:    id,
		input: input,
		submit: func(m *Model) (tea.Cmd, bool) {
			advance(m)
			return nil, true
		},
		keys: func(m *Model, msg tea.KeyPressMsg) (bool, tea.Cmd) {
			if msg.String() == "tab" {
				advance(m)
				return true, nil
			}
			return false, nil
		},
	})
	m.focusControlByID(id)
}

// --- saving -------------------------------------------------------------

// saveRemoteForm validates the draft and writes it. Validation is
// config.ValidateHost — the same state-free function `sidecar host add` calls —
// so the screen and the CLI accept exactly the same entries and refuse the rest
// in the same words.
func (m *Model) saveRemoteForm() tea.Cmd {
	form := m.remoteForm
	if form == nil {
		return nil
	}
	m.closeEditor()

	draft := config.HostConfig{
		ID:       form.name.Value(),
		Target:   form.target.Value(),
		Binary:   form.binary.Value(),
		Config:   form.config.Value(),
		Env:      strings.Fields(form.env.Value()),
		Disabled: form.disabled,
	}

	skip := -1
	if form.edit {
		if _, index, ok := config.FindHost(m.remotes(), form.originalID); ok {
			skip = index
		}
	}
	if message := config.ValidateHost(m.remotes(), draft, skip); message != "" {
		form.message = message
		return nil
	}
	form.message = ""

	saved := config.NormalizeHost(draft)
	editing, original := form.edit, form.originalID

	// Select the saved host when Remote Hosts comes back into view, including
	// after a rename: the entry the user was working on is the one the page
	// should be on.
	m.remotesPage().selectID = saved.ID
	m.remoteForm = nil
	m.Back()

	if editing {
		return SaveCmd("Saved host: "+saved.ID, func() error {
			_, err := config.UpdateHost(original, func(host *config.HostConfig) { *host = saved })
			return err
		})
	}
	return SaveCmd("Added host: "+saved.ID, func() error {
		_, err := config.AddHost(saved)
		return err
	})
}
