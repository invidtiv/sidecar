package configui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// remotesFixture is a model with two registered machines and the feature on.
func remotesFixture(t *testing.T) *Model {
	t.Helper()
	cfg := config.Default()
	cfg.Features.Flags = map[string]bool{features.SidecarRemoteHosts.Name: true}
	cfg.Hosts.List = []config.HostConfig{
		{ID: "book", Target: "marcusbook"},
		{Target: "proof-host", Disabled: true},
	}
	m, _ := configFixture(t, cfg)
	m.SetHostState(HostState{
		Config: loadSaved(t),
		RemoteHosts: []RemoteHost{
			{ID: "book", State: "online", Connected: true},
		},
	})
	return m
}

func TestRemotesPageListsHostsAndTheirCondition(t *testing.T) {
	m := remotesFixture(t)
	m.Open(PageRemotes)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{"Remote Hosts", "2 registered", "A  Add host", "book", "marcusbook", "online", "proof-host", "switched off"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Remote Hosts is missing %q:\n%s", want, view)
		}
	}
	// Reachability is not configured here, and the page has to say why rather
	// than leaving a user hunting for a field that does not exist.
	if !strings.Contains(view, "ssh_config") {
		t.Fatalf("the page never explains where reachability comes from:\n%s", view)
	}
}

// A machine's condition comes from the running registry, not from a probe this
// page runs. An unreachable host shows the registry's own fix line, so this
// page and the Sessions browser say the same thing about the same machine.
func TestRemotesPageShowsRegistryHealth(t *testing.T) {
	m := remotesFixture(t)
	m.SetRemoteHosts([]RemoteHost{{
		ID:    "book",
		State: "unreachable",
		Fix:   "check the machine is on and `ssh <target>` works from here",
	}})
	m.Open(PageRemotes)
	view := ansi.Strip(m.View(160, 45))

	if !strings.Contains(view, "unreachable") {
		t.Fatalf("the page did not show the registry's state:\n%s", view)
	}
	if !strings.Contains(view, "check the machine is on") {
		t.Fatalf("the page did not show the registry's fix line:\n%s", view)
	}
}

// With the flag off the page stays, still lists the registry, and says where
// the switch is. A configuration surface that hides the thing being configured
// is how a flag becomes a secret.
func TestRemotesPageSurvivesTheFlagBeingOff(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags = map[string]bool{features.SidecarRemoteHosts.Name: false}
	cfg.Hosts.List = []config.HostConfig{{ID: "book", Target: "marcusbook"}}
	m, _ := configFixture(t, cfg)
	m.SetHostState(HostState{Config: loadSaved(t)})

	found := false
	for _, page := range AllPages() {
		if page.ID == PageRemotes {
			found = true
		}
	}
	if !found {
		t.Fatal("Remote Hosts left the sidebar when the feature was off")
	}

	m.Open(PageRemotes)
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Turn on Remote hosts") {
		t.Fatalf("the page did not offer the way to turn the feature on:\n%s", view)
	}
	if !strings.Contains(view, "book") {
		t.Fatalf("the page hid the registry it is for:\n%s", view)
	}

	// The switch itself lives on Feature Flags, so there is one control for one
	// setting rather than two free to disagree.
	handled, cmd := m.Key(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if !handled {
		t.Fatal("the page did not answer its own advertised key")
	}
	_ = cmd
	if m.Page() != PageFlags {
		t.Fatalf("the control landed on %q, want Feature Flags", m.Page())
	}
}

// Adding a host goes through the same Load→mutate→Save boundary every other
// page uses, and the surface reads the result back rather than keeping a copy.
func TestAddRemoteFormSaves(t *testing.T) {
	m := remotesFixture(t)
	m.Open(PageRemotes)
	m.View(160, 45)

	m.OpenAddRemote()
	if !isRemoteFormRoute(m.Route()) {
		t.Fatalf("Add host opened %v", m.Route())
	}
	m.remoteForm.target.SetValue("thirdmachine")
	m.remoteForm.name.SetValue("third")
	m.remoteForm.env.SetValue("TMUX_TMPDIR=/tmp/proof SIDECAR_ISOLATED_STATE=1")

	cmd := m.saveRemoteForm()
	if cmd == nil {
		t.Fatal("saving the form produced no write")
	}
	reload(t, m, cmd())

	saved := loadSaved(t)
	if len(saved.Hosts.List) != 3 {
		t.Fatalf("registry = %+v, want three machines", saved.Hosts.List)
	}
	entry, _, ok := config.FindHost(saved.Hosts.List, "third")
	if !ok {
		t.Fatalf("the new host is not in the registry: %+v", saved.Hosts.List)
	}
	if entry.Target != "thirdmachine" || len(entry.Env) != 2 {
		t.Fatalf("saved host = %+v", entry)
	}
}

// The form refuses exactly what `sidecar host add` refuses, because both call
// config.ValidateHost. A refusal is shown on the form and nothing is written.
func TestRemoteFormRefusesWhatTheCLIRefuses(t *testing.T) {
	m := remotesFixture(t)
	m.Open(PageRemotes)
	m.View(160, 45)

	m.OpenAddRemote()
	m.remoteForm.target.SetValue("elsewhere")
	m.remoteForm.name.SetValue("book")

	if cmd := m.saveRemoteForm(); cmd != nil {
		t.Fatal("a duplicate name was written")
	}
	if !strings.Contains(m.remoteForm.message, "already registered") {
		t.Fatalf("form message = %q", m.remoteForm.message)
	}
	if want := config.ValidateHost(m.remotes(), config.HostConfig{ID: "book", Target: "elsewhere"}, -1); m.remoteForm.message != want {
		t.Fatalf("the form refused in different words from the shared validator: %q vs %q", m.remoteForm.message, want)
	}
}

// Editing applies against the name the entry had when the form opened, so a
// rename lands on the right machine.
func TestEditRemoteFormRenames(t *testing.T) {
	m := remotesFixture(t)
	m.Open(PageRemotes)
	m.View(160, 45)

	m.OpenEditRemote("book")
	if m.remoteForm == nil || !m.remoteForm.edit {
		t.Fatal("Edit host did not open on the saved entry")
	}
	m.remoteForm.name.SetValue("laptop")

	cmd := m.saveRemoteForm()
	if cmd == nil {
		t.Fatal("saving the edit produced no write")
	}
	reload(t, m, cmd())

	saved := loadSaved(t)
	if _, _, ok := config.FindHost(saved.Hosts.List, "laptop"); !ok {
		t.Fatalf("the rename did not land: %+v", saved.Hosts.List)
	}
	if _, _, ok := config.FindHost(saved.Hosts.List, "book"); ok {
		t.Fatalf("the old name survived the rename: %+v", saved.Hosts.List)
	}
}

// Switching a host off keeps it registered. Deleting the entry to stop the
// reconnect attempts would lose its settings, which is what `disabled` exists
// to avoid.
func TestSwitchingARemoteOffKeepsItRegistered(t *testing.T) {
	m := remotesFixture(t)
	m.Open(PageRemotes)
	m.View(160, 45)

	reload(t, m, m.toggleRemoteEnabled("book", false)())
	saved := loadSaved(t)
	entry, _, ok := config.FindHost(saved.Hosts.List, "book")
	if !ok || !entry.Disabled {
		t.Fatalf("book = %+v, want it registered and switched off", entry)
	}
}

// Removing is a confirmed change, and nothing happens until it is confirmed.
func TestRemoveRemoteRequiresConfirmation(t *testing.T) {
	m := remotesFixture(t)
	m.Open(PageRemotes)
	m.View(160, 45)

	m.confirmRemoveRemote("book")
	if m.confirm == nil {
		t.Fatal("removing a host did not ask")
	}
	m.DismissConfirm()
	if len(loadSaved(t).Hosts.List) != 2 {
		t.Fatal("a dismissed confirmation still removed the host")
	}

	m.confirmRemoveRemote("book")
	reload(t, m, m.confirm.apply(m)())
	if _, _, ok := config.FindHost(loadSaved(t).Hosts.List, "book"); ok {
		t.Fatal("the confirmed removal did not happen")
	}
}
