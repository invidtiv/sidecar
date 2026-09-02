package overview

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// Remote hosts in the Sessions browser.
//
// The design rule here is that a remote row is not a special kind of row. A
// host's snapshot is converted to ordinary workspaceinventory results carrying
// a HostID (hosts.ProjectResults), and from that point the catalog, the board,
// the lane grouping, the filter and the pins all treat it as any other
// workspace. The only things this file adds are: where the data enters, how a
// host's own health is shown when it has no rows to contribute, and how a
// selected terminal's separate control connection follows host registration.
//
// Nothing below runs with the feature off: hosts.FromConfig returns no hosts,
// so no registry is created and no ssh child is ever spawned.

// IsHostMessage reports whether a message belongs to the remote-host stream.
//
// The app routes these to the browser unconditionally, not only while the
// global tab is on screen. Two reasons, and the second is the load-bearing
// one: a host's rows must be current the moment a user switches to Sessions
// rather than a poll behind, and — more importantly — each delivery is what
// schedules the next read of the update channel. A message dropped because
// the wrong tab was focused does not merely delay a row; it ends the stream
// for the rest of the session.
func IsHostMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case hostUpdateMsg, hostStaleTickMsg:
		return true
	}
	return false
}

// hostUpdateMsg carries one host's new health or data into the model.
type hostUpdateMsg struct{ Update hosts.Update }

// hostStaleTickMsg ages connected hosts that have gone quiet.
//
// It carries no generation deliberately. Host connections are not bound to the
// refresh generation — that is the whole reason hostCtx exists — so a
// generation here would read as a staleness guard while guarding nothing.
// Exactly one tick chain runs, started once from startHosts.
type hostStaleTickMsg struct{}

// hostStaleTick is how often quiet hosts are re-examined. It is the tick, not
// the staleness window: hosts.DefaultStaleAfter decides when a host is
// actually stale.
const hostStaleTick = 15 * time.Second

// hostRowPrefix separates a host name from a project name in a row's project
// label. The label is what the list groups by under the Project sort and what
// it prefixes each row with, so writing the host into it is the whole of "host
// grouping" — no new field, no renderer change, and the filter searches it
// because it already searches the project.
const hostRowPrefix = " · "

// startHosts brings the configured hosts up and begins consuming their
// updates.
//
// It is called at startup and after a configuration save. Sync reconciles and
// leaves unchanged hosts connected; removals and retargets also revoke any
// selected terminal control process before the registry client changes.
func (m *Model) startHosts() tea.Cmd {
	registered := hosts.FromConfig(m.config)
	disabled := hosts.DisabledFromConfig(m.config)
	nextConfigured := make(map[string]hosts.Host, len(registered))
	for _, host := range registered {
		nextConfigured[host.ID] = host
	}
	// The selected terminal's ssh control process is independent of Registry's
	// serve client. Reconcile it before Registry.Sync stops/replaces that client,
	// or a removed/retargeted HostID keeps accepting input and late history on
	// the old machine.
	if remoteControlInvalidated(m.previewTarget(), m.hostConfigured, nextConfigured) {
		m.closePreviewTerminal()
	}
	m.hostConfigured = nextConfigured
	m.setConfiguredHostIDs(registered, disabled)
	if len(registered) == 0 && len(disabled) == 0 {
		// Either the feature is off or nothing is registered. Tear down any
		// registry a previous config had — which matters once this is called
		// on config reload; see the note above.
		if m.hostRegistry != nil {
			m.hostRegistry.Stop()
			m.hostRegistry = nil
			m.hostResults = nil
			m.hostHealth = nil
			m.hostProjects = nil
			m.hostLastKnown = nil
		}
		m.hostRegistered = make(map[string]bool)
		m.hostIncarnations = make(map[string]uint64)
		m.persistPrunedHiddenHosts()
		m.syncBoard()
		return nil
	}

	first := m.hostRegistry == nil
	if first {
		m.hostRegistered = make(map[string]bool)
		m.hostRegistry = hosts.NewRegistry(hosts.ClientOptions{})
		m.hostResults = make(map[string][]workspaceinventory.ProjectResult)
		m.hostHealth = make(map[string]hosts.Health)
		m.hostProjects = make(map[string][]Project)
		m.hostLastKnown = make(map[string][]workspaceinventory.ProjectResult)
	}
	m.hostRegistered = make(map[string]bool, len(registered)+len(disabled))
	for _, host := range registered {
		m.hostRegistered[host.ID] = true
	}
	for _, id := range disabled {
		m.hostRegistered[id] = true
	}
	m.hostRegistry.Sync(m.hostContext(), registered)
	m.hostIncarnations = make(map[string]uint64, len(registered))
	for _, host := range registered {
		if incarnation, ok := m.hostRegistry.Incarnation(host.ID); ok {
			m.hostIncarnations[host.ID] = incarnation
		}
	}

	// Disabled hosts get no client and no connection, but they do get a row:
	// `disabled` means "off this week", and a machine that silently vanished
	// would be indistinguishable from one whose entry was deleted.
	for _, id := range hosts.DisabledFromConfig(m.config) {
		m.hostHealth[id] = hosts.Health{State: hosts.StateDisabled}
	}

	// Drop retained state for hosts that are no longer registered, or their
	// rows would outlive the host they came from.
	live := make(map[string]bool, len(registered)+len(disabled))
	for _, host := range registered {
		live[host.ID] = true
	}
	for _, id := range disabled {
		live[id] = true
	}
	for id := range m.hostHealth {
		if !live[id] {
			delete(m.hostHealth, id)
			delete(m.hostResults, id)
			delete(m.hostProjects, id)
			delete(m.hostLastKnown, id)
		}
	}
	m.persistPrunedHiddenHosts()
	m.syncBoard()

	if !first {
		return nil
	}
	return tea.Batch(m.waitForHostUpdate(), m.scheduleHostStaleTick())
}

func remoteControlInvalidated(target tty.Target, previous, next map[string]hosts.Host) bool {
	if target.Host == "" {
		return false
	}
	oldHost, existed := previous[target.Host]
	newHost, remains := next[target.Host]
	return !remains || !existed || !oldHost.Same(newHost)
}

// hostContext is the lifetime host connections hang off. It is deliberately
// NOT m.ctx: that context is cancelled and replaced on every refresh
// generation, which would tear down every ssh connection on each poll.
func (m *Model) hostContext() context.Context {
	if m.hostCancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		m.hostCtx, m.hostCancel = ctx, cancel
	}
	return m.hostCtx
}

// stopHosts disconnects every host. Called when the model stops for good.
func (m *Model) stopHosts() {
	if m.hostRegistry != nil {
		m.hostRegistry.Stop()
		m.hostRegistry = nil
	}
	if m.hostCancel != nil {
		m.hostCancel()
		m.hostCancel = nil
		m.hostCtx = nil
	}
}

// waitForHostUpdate blocks a command goroutine on the merged update stream.
// One outstanding wait at a time; each delivery schedules the next.
func (m *Model) waitForHostUpdate() tea.Cmd {
	registry := m.hostRegistry
	if registry == nil {
		return nil
	}
	return func() tea.Msg {
		update, ok := <-registry.Updates()
		if !ok {
			return nil
		}
		return hostUpdateMsg{Update: update}
	}
}

func (m *Model) scheduleHostStaleTick() tea.Cmd {
	return tea.Tick(hostStaleTick, func(time.Time) tea.Msg { return hostStaleTickMsg{} })
}

// handleHostUpdate folds one host's new state into the model.
func (m *Model) handleHostUpdate(msg hostUpdateMsg) tea.Cmd {
	if m.hostRegistry == nil {
		return nil
	}
	update := msg.Update
	// A client that has just been stopped can still deliver one last update
	// through the merged stream, after startHosts pruned this host's state.
	// Applying it would resurrect a de-registered machine as a permanent
	// error row.
	if m.hostRegistered != nil && !m.hostRegistered[update.HostID] {
		return m.waitForHostUpdate()
	}
	if m.hostIncarnations != nil {
		expected, ok := m.hostIncarnations[update.HostID]
		if !ok || update.Incarnation != expected {
			return m.waitForHostUpdate()
		}
	}
	m.hostHealth[update.HostID] = update.Health

	if update.Snapshot != nil && update.Health.State.Shows() {
		stale := !update.Health.State.Healthy()
		results := hosts.ProjectResults(update.HostID, *update.Snapshot, stale)
		m.hostResults[update.HostID] = results
		projects := make([]Project, 0, len(results))
		for index, result := range results {
			projects = append(projects, Project{
				Name:  result.ProjectName,
				Path:  result.ProjectRoot,
				Key:   result.ProjectKey,
				Index: index,
			})
		}
		m.hostProjects[update.HostID] = projects
		if m.hostLastKnown == nil {
			m.hostLastKnown = make(map[string][]workspaceinventory.ProjectResult)
		}
		m.hostLastKnown[update.HostID] = copyProjectResults(results)
	}
	if !update.Health.State.Shows() {
		// A host that cannot show rows must not leave its last ones on screen
		// looking current. Its health row says what happened instead. Last-known
		// is kept for the catalog/switcher so `@` can still list them disabled.
		delete(m.hostResults, update.HostID)
		delete(m.hostProjects, update.HostID)
	}

	m.syncBoard()
	// Notifications are forwarded after the rows they belong to have been
	// applied, so a toast about a remote agent can never arrive before the
	// workspace it names exists in the browser.
	return tea.Batch(m.forwardHostNotifications(update), m.forwardHostUIRequests(update), m.waitForHostUpdate())
}

// handleHostStaleTick ages quiet hosts and reschedules itself.
func (m *Model) handleHostStaleTick(hostStaleTickMsg) tea.Cmd {
	if m.hostRegistry == nil {
		return nil
	}
	// A transition arrives as an ordinary update on the stream; there is
	// nothing to apply here.
	m.hostRegistry.MarkStaleIfQuiet()
	return m.scheduleHostStaleTick()
}

// hostOrder is the stable display order of registered hosts.
func (m *Model) hostOrder() []string {
	ids := make([]string, 0, len(m.hostHealth))
	for id := range m.hostHealth {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// hostShown reports whether a machine's rows belong in the browser right now.
//
// This is the one gate. Both projections read it through the iterators below
// rather than each testing the set themselves, for the same reason they share
// eachHostWorkspace: two places deciding what "hidden" means is how the list
// and the board come to disagree about which machines exist.
func (m *Model) hostShown(id string) bool { return !m.hiddenHosts[id] }

// shownHostOrder is hostOrder without the machines the user has hidden.
func (m *Model) shownHostOrder() []string {
	order := m.hostOrder()
	if len(m.hiddenHosts) == 0 {
		return order
	}
	shown := order[:0:0]
	for _, id := range order {
		if m.hostShown(id) {
			shown = append(shown, id)
		}
	}
	return shown
}

// workspaceShown reports whether a catalog row is on screen. Local rows always
// are; a remote row follows its machine.
//
// The relayed-request paths ask this before binding: a request whose row is
// hidden is a request whose row is not on this viewer's screen, which the open
// and layout contracts already answer by declining rather than acting on a row
// the user cannot see.
func (m *Model) workspaceShown(workspace workspaceinventory.Workspace) bool {
	return workspace.HostID == "" || m.hostShown(workspace.HostID)
}

// hiddenHostIDs is the hidden set as a sorted slice, for persistence and for
// the count the sort control carries.
func (m *Model) hiddenHostIDs() []string {
	ids := make([]string, 0, len(m.hiddenHosts))
	for id, hidden := range m.hiddenHosts {
		if hidden {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// hiddenHostCount is how many *configured* machines are hidden. A stale entry
// for a host that is no longer registered must not be counted, or the control
// would advertise rows nothing can bring back.
func (m *Model) hiddenHostCount() int {
	count := 0
	for _, id := range m.configuredHostIDs() {
		if !m.hostShown(id) {
			count++
		}
	}
	return count
}

// configuredHostIDs is every machine the user has registered, in display
// order, whether or not it is currently contributing rows.
//
// It is the set startHosts last reconciled rather than a fresh read of the
// config, because it is consulted on every sync: the remotes section has to
// list a machine that has not answered yet — a host you cannot see and cannot
// find a checkbox for is the confusion this feature exists to remove — but the
// refresh path must not re-walk the configuration to learn that.
func (m *Model) configuredHostIDs() []string { return m.hostConfiguredIDs }

// setConfiguredHostIDs records the reconciled host set. Registered and
// disabled machines both count: `disabled` means "off this week", and the
// browser still shows it a row.
func (m *Model) setConfiguredHostIDs(registered []hosts.Host, disabled []string) {
	seen := make(map[string]bool, len(registered)+len(disabled))
	ids := make([]string, 0, len(registered)+len(disabled))
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, host := range registered {
		add(host.ID)
	}
	for _, id := range disabled {
		add(id)
	}
	sort.Strings(ids)
	m.hostConfiguredIDs = ids
}

// setHostHidden records one machine's visibility and returns whether anything
// changed. Persistence and re-sync are the caller's, so a batch — the "show
// remotes" master toggle — writes state once.
func (m *Model) setHostHidden(id string, hidden bool) bool {
	if m.hiddenHosts == nil {
		m.hiddenHosts = make(map[string]bool)
	}
	if m.hiddenHosts[id] == hidden {
		return false
	}
	if hidden {
		m.hiddenHosts[id] = true
	} else {
		delete(m.hiddenHosts, id)
	}
	return true
}

// persistPrunedHiddenHosts drops stale hidden entries and writes the result.
func (m *Model) persistPrunedHiddenHosts() {
	if m.pruneHiddenHosts() {
		_ = saveSessionsHiddenHosts(m.hiddenHostIDs())
	}
}

// pruneHiddenHosts drops hidden entries for machines that are no longer
// configured. Without it, deleting a host and adding it back later would
// silently restore a hiding decision made about a different machine.
//
// It does nothing when no host is configured at all. "Every host was removed"
// and "the remote-hosts feature is switched off" are indistinguishable from
// here, and clearing the set in the second case would lose the user's choices
// for no reason the moment they turned the flag off.
func (m *Model) pruneHiddenHosts() bool {
	if len(m.hiddenHosts) == 0 || len(m.hostConfiguredIDs) == 0 {
		return false
	}
	configured := make(map[string]bool)
	for _, id := range m.configuredHostIDs() {
		configured[id] = true
	}
	changed := false
	for id := range m.hiddenHosts {
		if !configured[id] {
			delete(m.hiddenHosts, id)
			changed = true
		}
	}
	return changed
}

// eachHostWorkspace visits every remote workspace in display order, with the
// project label a row should carry and whether its machine is currently shown.
//
// It exists so the list and the board iterate remote rows the same way. Two
// loops over the same data is how the two projections start disagreeing, which
// is the failure the shared catalog exists to prevent.
//
// Hidden machines are visited, not skipped. Hiding is a view filter, and the
// catalog is what the browser knows exists — pins, live terminal splits and
// the remembered selection are all keyed off it, and dropping a hidden host's
// rows from it would read to those as "this workspace was deleted". Each
// caller applies the gate to its own projection instead; see hostShown.
func (m *Model) eachHostWorkspace(visit func(order int, label string, workspace workspaceinventory.Workspace, stale bool, shown bool)) {
	// Ordinals are global across hosts, not per host. Restarting the index at
	// zero for each machine made every host's first project tie with every
	// other's, so the Project sort — stable, ties keep insertion order —
	// interleaved two machines' rows instead of grouping them.
	order := 0
	for _, id := range m.hostOrder() {
		stale := !m.hostHealth[id].State.Healthy()
		shown := m.hostShown(id)
		results := m.hostResults[id]
		for index, project := range m.hostProjects[id] {
			label := id + hostRowPrefix + project.Name
			// hostProjects is built 1:1 from hostResults in handleHostUpdate,
			// so the index pairs them directly. Matching by ProjectKey instead
			// visited every workspace twice whenever a host reported two
			// projects with the same key, producing duplicate rows and
			// duplicate kanban card IDs.
			if index >= len(results) {
				continue
			}
			for _, workspace := range results[index].Workspaces {
				visit(order, label, workspace, stale, shown)
			}
			order++
		}
	}
}

// hostHealthRows are the rows a host contributes when it is not contributing
// workspaces: unreachable, no sidecar, wrong protocol, no tmux, and so on.
//
// A host with a problem is a row, not a silence. The alternative — a machine
// that simply stops appearing — is indistinguishable from a machine with
// nothing running on it, which is exactly the question this feature exists to
// answer.
func (m *Model) hostHealthRows() []workspacelist.Item {
	var rows []workspacelist.Item
	for _, id := range m.shownHostOrder() {
		health := m.hostHealth[id]
		if health.State.Shows() {
			// Healthy and stale hosts speak through their workspaces. A stale
			// one already labels every row.
			continue
		}
		rows = append(rows, hostHealthRow(id, health))
	}
	return rows
}

// hostHealthRow renders one unhealthy host as a list row.
func hostHealthRow(id string, health hosts.Health) workspacelist.Item {
	status := string(health.State)
	if fix := health.Fix(); fix != "" {
		status += " · " + fix
	}
	row := workspacelist.Item{
		ID:      hostHealthRowID(id),
		Name:    id,
		Project: id,
		Status:  status,
		Kind:    "host",
		Group:   workspacelist.GroupPaused,
		Marker:  workspacelist.RowMarker{Icon: "⚠", Tone: workspacelist.MarkerWarning},
	}
	if health.State == hosts.StateConnecting {
		row.Marker = workspacelist.RowMarker{Icon: "◌", Tone: workspacelist.MarkerMuted}
		row.Status = "connecting…"
	}
	if detail := strings.TrimSpace(health.Detail); detail != "" {
		row.Detail = firstLine(detail)
	}
	return row
}

// hostHealthRowID is deliberately unlike any workspace ID, so a health row can
// never be mistaken for a row something can be done to.
func hostHealthRowID(id string) string { return hostHealthPrefix + id }

// hostHealthPrefix marks a row that is a host's condition rather than a
// workspace. It is deliberately unlike any workspace ID, so a health row can
// never be mistaken for a row something can be done to.
const hostHealthPrefix = "host-health:"

// IsHostHealthRow reports whether a row is a host's health rather than a
// workspace.
func IsHostHealthRow(id string) bool { return strings.HasPrefix(id, hostHealthPrefix) }

func firstLine(s string) string {
	if index := strings.IndexByte(s, '\n'); index >= 0 {
		return strings.TrimSpace(s[:index])
	}
	return s
}

// HostControlSpawner is the control-mode proxy Sessions already uses for a
// remote row. The project workspace reuses it rather than forking a second
// ssh/tmux channel.
func (m *Model) HostControlSpawner(hostID string) tty.ControlSpawner {
	return m.hostControlSpawner(hostID)
}

// hostControlSpawner builds the channel-1 spawner for one host: the ssh
// invocation carrying a remote tmux session's control protocol.
//
// It returns nil when the host is not connected, which is what makes a live
// view of an unreachable machine refuse rather than hang on a connect timeout
// inside the render path.
func (m *Model) hostControlSpawner(hostID string) tty.ControlSpawner {
	if m.hostRegistry == nil {
		return nil
	}
	client, ok := m.hostRegistry.Client(hostID)
	if !ok || !m.hostHealth[hostID].State.Shows() {
		return nil
	}
	ctx := m.hostContext()
	return func(session string) *exec.Cmd { return client.ControlCommand(ctx, session) }
}

// remotePreviewSnapshotLines bounds how much of a host's capture is shown
// before a live channel takes over. It is a preview, not a scrollback: enough
// to see what an agent is asking, not so much that it looks live.
const remotePreviewSnapshotLines = 24

// remotePreviewSnapshot renders the capture the host already took.
//
// The tail, not the head: the bottom of a pane is where an agent's prompt and
// its question are, which is the whole reason this is worth showing.
func remotePreviewSnapshot(workspace workspaceinventory.Workspace) string {
	text := strings.TrimRight(workspace.Preview, "\n \t")
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > remotePreviewSnapshotLines {
		lines = lines[len(lines)-remotePreviewSnapshotLines:]
	}
	return strings.Join(lines, "\n")
}

// HostHealthDetail is what the preview shows for a selected host health row:
// the state, what went wrong, and what to do. Empty for anything that is not
// a health row, which is how the preview knows to fall through.
func (m *Model) HostHealthDetail(rowID string) string {
	if !IsHostHealthRow(rowID) {
		return ""
	}
	id := strings.TrimPrefix(rowID, hostHealthPrefix)
	health, ok := m.hostHealth[id]
	if !ok {
		return ""
	}
	lines := []string{id + " — " + string(health.State)}
	if detail := strings.TrimSpace(health.Detail); detail != "" {
		lines = append(lines, "", detail)
	}
	if fix := health.Fix(); fix != "" {
		lines = append(lines, "", fix)
	}
	return strings.Join(lines, "\n")
}
