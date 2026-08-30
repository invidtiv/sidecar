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
		}
		m.hostRegistered = make(map[string]bool)
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
	}
	m.hostRegistered = make(map[string]bool, len(registered)+len(disabled))
	for _, host := range registered {
		m.hostRegistered[host.ID] = true
	}
	for _, id := range disabled {
		m.hostRegistered[id] = true
	}
	m.hostRegistry.Sync(m.hostContext(), registered)

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
		}
	}
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
	}
	if !update.Health.State.Shows() {
		// A host that cannot show rows must not leave its last ones on screen
		// looking current. Its health row says what happened instead.
		delete(m.hostResults, update.HostID)
		delete(m.hostProjects, update.HostID)
	}

	m.syncBoard()
	return m.waitForHostUpdate()
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

// eachHostWorkspace visits every remote workspace in display order, with the
// project label a row should carry.
//
// It exists so the list and the board iterate remote rows the same way. Two
// loops over the same data is how the two projections start disagreeing, which
// is the failure the shared catalog exists to prevent.
func (m *Model) eachHostWorkspace(visit func(order int, label string, workspace workspaceinventory.Workspace, stale bool)) {
	// Ordinals are global across hosts, not per host. Restarting the index at
	// zero for each machine made every host's first project tie with every
	// other's, so the Project sort — stable, ties keep insertion order —
	// interleaved two machines' rows instead of grouping them.
	order := 0
	for _, id := range m.hostOrder() {
		stale := !m.hostHealth[id].State.Healthy()
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
				visit(order, label, workspace, stale)
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
	for _, id := range m.hostOrder() {
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
