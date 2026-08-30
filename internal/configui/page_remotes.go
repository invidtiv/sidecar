package configui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// Remote Hosts is the registry of other machines this Sidecar watches over SSH:
// one row per registered machine, its live condition beside it, and add, edit,
// remove and switch-off.
//
// Two things about the page are deliberate and easy to get wrong later.
//
// The health on each row is read from the running host registry, through the
// RemoteHosts the app hands this surface. The page never opens a connection of
// its own — a settings screen probing a machine the browser is already
// connected to would be a second answer to the same question, and the two would
// disagree the moment one of them was slower.
//
// The page is here whether or not the feature flag is on. A configuration
// surface that hides the thing being configured is how a flag becomes a secret,
// so with the flag off the page still lists and edits the registry and says, at
// the top, that nothing is being watched and where the switch is.

const (
	regionRemoteAdd     = "config-remote-add"
	regionRemoteRow     = "config-remote-row-"
	regionRemoteEdit    = "config-remote-edit-"
	regionRemoteRemove  = "config-remote-remove-"
	regionRemoteEnable  = "config-remote-flag"
	remoteActionEditKey = "e"
	remoteActionDropKey = "d"
)

// remotesState is the page's selection.
type remotesState struct {
	cursor int
	// selectID is a host to select once it exists — what returning from Add
	// host with a new machine means.
	selectID string
}

func (m *Model) remotesPage() *remotesState {
	if m.remotesState == nil {
		m.remotesState = &remotesState{}
	}
	return m.remotesState
}

// remotes is the registered host list.
func (m *Model) remotes() []config.HostConfig { return m.Config().Hosts.List }

// clampRemoteCursor keeps the selection inside the registry and honours a
// pending selection request from the form. It reports whether it resolved one,
// which outranks where the row cursor happens to be: a host just registered is
// the host the page should be on.
func (m *Model) clampRemoteCursor() bool {
	state := m.remotesState
	if state == nil {
		return false
	}
	resolved := false
	list := m.remotes()
	if state.selectID != "" {
		for i, host := range list {
			if strings.EqualFold(config.HostIDFor(host), state.selectID) {
				state.cursor = i
				state.selectID = ""
				resolved = true
				break
			}
		}
	}
	if state.cursor >= len(list) {
		state.cursor = max(0, len(list)-1)
	}
	if state.cursor < 0 {
		state.cursor = 0
	}
	return resolved
}

// syncRemoteCursor points the page's selection at the row the pane's cursor is
// on, for the same reason Projects does it: the list has one block per host and
// no window of its own, so deciding the selection twice is how the detail below
// ends up describing a machine the user is no longer on.
func (m *Model) syncRemoteCursor() {
	state := m.remotesState
	if state == nil || !strings.HasPrefix(m.focusedID, regionRemoteRow) {
		return
	}
	index, err := strconv.Atoi(strings.TrimPrefix(m.focusedID, regionRemoteRow))
	if err != nil || index < 0 || index >= len(m.remotes()) {
		return
	}
	state.cursor = index
}

// remoteHealth is what the running registry last said about a machine. The
// second result is false for a host the registry is not watching at all, which
// is a different thing from a host it cannot reach.
func (m *Model) remoteHealth(id string) (RemoteHost, bool) {
	for _, remote := range m.host.RemoteHosts {
		if strings.EqualFold(remote.ID, id) {
			return remote, true
		}
	}
	return RemoteHost{}, false
}

func (m *Model) remotesEnabled() bool { return m.flagEnabled(features.SidecarRemoteHosts.Name) }

func (m *Model) buildRemotes(b *paneBuilder) {
	state := m.remotesPage()
	if m.clampRemoteCursor() {
		// A request from the form wins, and takes the row cursor with it, so the
		// two selection sources never end up naming different machines.
		m.focusControlByID(fmt.Sprintf("%s%d", regionRemoteRow, state.cursor))
	} else {
		m.syncRemoteCursor()
	}
	list := m.remotes()

	b.text(PaneTitle(PageTitle(PageRemotes)), "")

	if !m.remotesEnabled() {
		m.buildRemotesFlagOff(b)
	}

	count := fmt.Sprintf("%d registered", len(list))
	if len(list) == 1 {
		count = "1 registered"
	}
	left := Body("Your machines") + "  " + Muted(count)
	b.rightControlPrimary(left, regionRemoteAdd, "a", "A  Add host", func(m *Model) tea.Cmd {
		m.OpenAddRemote()
		return m.drain(nil)
	})
	b.blank()

	if len(list) == 0 {
		b.lead("Sidecar is not watching any other machines yet.")
		b.lead("Register one to see its shells, worktrees and agents in Sessions, beside this machine's own.")
		b.blank()
		m.buildRemotesFooter(b)
		return
	}

	for i := range list {
		m.buildRemoteRow(b, i, list[i])
	}
	b.blank()
	m.buildRemotesFooter(b)
}

// buildRemotesFlagOff is the page's answer when the feature is switched off. It
// says what that means for the entries below it and offers the switch, rather
// than removing the page — a user who cannot find the setting cannot turn it on.
func (m *Model) buildRemotesFlagOff(b *paneBuilder) {
	b.lead("Sidecar is not watching any of these machines: the Remote hosts feature is off.")
	b.buttons(buttonSpec{
		id:    regionRemoteEnable,
		key:   "f",
		label: "F  Turn on Remote hosts",
		run: func(m *Model) tea.Cmd {
			// The flag's own page is where it is set, and it is set there for
			// every flag. Jumping to that control rather than writing the flag
			// here keeps one switch for one setting; two would be free to
			// disagree, which is the rule Panels already follows in reverse.
			m.Navigate(PageFlags)
			m.detailFocus = true
			m.focusControlByID(regionFlag + features.SidecarRemoteHosts.Name)
			return nil
		},
	})
	b.blank()
}

// buildRemotesFooter is the page's standing explanation. Reachability is the
// question users bring here, and the answer is that Sidecar does not own it.
func (m *Model) buildRemotesFooter(b *paneBuilder) {
	b.note("Sidecar reaches a machine through the ssh target your own ssh_config resolves — your keys, your ProxyJump, your agent. If `ssh <target>` works from a terminal here, it works here; if it does not, this page cannot fix it.")
	b.blank()
	b.note("Sidecar must be installed on the machine too, and found by its login shell. Set a binary path on the entry when it is somewhere a login shell does not look.")
}

// buildRemoteRow is one registered machine: name and condition on the first
// line, target and what to do about the condition on the second, and the
// connect switch as its own pill. Edit and Remove appear on the focused row.
func (m *Model) buildRemoteRow(b *paneBuilder, index int, host config.HostConfig) {
	id := fmt.Sprintf("%s%d", regionRemoteRow, index)
	name := config.HostIDFor(host)
	normalized := config.NormalizeHost(host)

	// The row says the target and, when there is anything to do about it, the
	// condition's own fix line. It does not also spell out E and D: the pills
	// below carry their keys in their labels, and they are on screen exactly
	// when the hint would have been.
	detailFor := func(State) string {
		detail := normalized.Target
		if condition := m.remoteCondition(normalized); condition != "" {
			detail += " · " + condition
		}
		return detail
	}

	row := b.panelToggleFocusDetail(id, name, m.remoteBadge(normalized), detailFor, !normalized.Disabled,
		func(m *Model) tea.Cmd { return m.toggleRemoteEnabled(name, normalized.Disabled) })
	if !row.Focused && !row.Hovered && !b.hovering(id+toggleSuffix) {
		return
	}
	m.buildRemoteRowActions(b, index, name)
}

// buildRemoteRowActions paints Edit and Remove under the row the user is on.
//
// They are deliberately not cursor stops. A pill that exists only while its row
// is focused takes the focus away from that row the moment the cursor reaches
// it, which unpaints the pill the cursor just moved onto; the row keeps the
// cursor and the two actions answer to their own keys and to the mouse.
func (m *Model) buildRemoteRowActions(b *paneBuilder, index int, name string) {
	editID := fmt.Sprintf("%s%d", regionRemoteEdit, index)
	removeID := fmt.Sprintf("%s%d", regionRemoteRemove, index)

	editState := b.declare(editID, remoteActionEditKey, false, func(m *Model) tea.Cmd {
		m.OpenEditRemote(name)
		return nil
	})
	removeState := b.declare(removeID, remoteActionDropKey, false, func(m *Model) tea.Cmd {
		m.confirmRemoveRemote(name)
		return nil
	})
	edit := Button("E  Edit", false, editState)
	remove := Button("D  Remove", false, removeState)

	y := len(b.lines)
	b.lines = append(b.lines, ButtonRow(edit, remove))
	x := b.originX + RowIndent
	b.m.mouse.HitMap.AddRect(editID, x, 1+y, ansi.StringWidth(edit), 1, nil)
	x += ansi.StringWidth(edit) + 2 // ButtonRow joins with two spaces
	b.m.mouse.HitMap.AddRect(removeID, x, 1+y, ansi.StringWidth(remove), 1, nil)
}

// remoteBadge is the health state's own name, as the Sessions browser spells
// it. A row that invented its own vocabulary would leave a user matching two
// descriptions of one machine.
func (m *Model) remoteBadge(host config.HostConfig) string {
	if host.Disabled {
		return Badge("switched off", false)
	}
	if !m.remotesEnabled() {
		return Badge("not watched", false)
	}
	remote, known := m.remoteHealth(config.HostIDFor(host))
	if !known {
		return Badge("connecting", false)
	}
	if remote.Connected {
		return Badge("online", false)
	}
	return Badge(remote.State, true)
}

// remoteCondition is the sentence under a row: what went wrong and what to do
// about it, taken from the registry's own fix line so this page and the browser
// row say the same thing.
func (m *Model) remoteCondition(host config.HostConfig) string {
	if host.Disabled {
		return "registered, not connected"
	}
	if !m.remotesEnabled() {
		return "turn on Remote hosts to watch it"
	}
	remote, known := m.remoteHealth(config.HostIDFor(host))
	if !known || remote.Connected {
		return ""
	}
	if fix := strings.TrimSpace(remote.Fix); fix != "" {
		return fix
	}
	return strings.TrimSpace(remote.Detail)
}

// toggleRemoteEnabled switches a machine off without losing its settings, which
// is what `disabled` is for: a host that is off this week is still a host.
func (m *Model) toggleRemoteEnabled(id string, disabled bool) tea.Cmd {
	next := !disabled
	notice := "Connecting to " + id
	if next {
		notice = "Switched off " + id
	}
	return SaveCmd(notice, func() error {
		_, err := config.UpdateHost(id, func(host *config.HostConfig) { host.Disabled = next })
		return err
	})
}

// confirmRemoveRemote asks before unregistering. Removing is a configuration
// change and nothing more: nothing on the other machine is touched, and its
// shells go on running there.
func (m *Model) confirmRemoveRemote(id string) {
	m.confirm = &confirmState{
		title: "Remove host",
		intro: []string{
			Body("Stop watching " + id + "?"),
		},
		body: []string{
			IndentedMuted("Nothing on that machine is touched. Its shells, worktrees and agents go on running there; this Sidecar simply stops connecting."),
			IndentedMuted("The entry and its settings are removed. To stop connecting but keep them, switch the host off instead."),
		},
		apply: func(m *Model) tea.Cmd {
			return SaveCmd("Removed host: "+id, func() error {
				_, err := config.RemoveHost(id)
				return err
			})
		},
	}
	m.rowCursor = 0
}
