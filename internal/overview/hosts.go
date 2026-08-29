package overview

import (
	"context"
	"fmt"
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
// host's own health is shown when it has no rows to contribute, and the rule
// that a remote row is never acted on.
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
type hostStaleTickMsg struct{ Generation int }

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
// updates. Safe to call repeatedly: Sync reconciles, leaving unchanged hosts
// connected.
func (m *Model) startHosts() tea.Cmd {
	registered := hosts.FromConfig(m.config)
	if len(registered) == 0 {
		// Either the feature is off or nothing is registered. Tear down any
		// registry a previous config had, so turning the feature off at
		// runtime actually stops the ssh children.
		if m.hostRegistry != nil {
			m.hostRegistry.Stop()
			m.hostRegistry = nil
			m.hostResults = nil
			m.hostHealth = nil
			m.hostProjects = nil
		}
		return nil
	}

	first := m.hostRegistry == nil
	if first {
		m.hostRegistry = hosts.NewRegistry(hosts.ClientOptions{})
		m.hostResults = make(map[string][]workspaceinventory.ProjectResult)
		m.hostHealth = make(map[string]hosts.Health)
		m.hostProjects = make(map[string][]Project)
	}
	m.hostRegistry.Sync(m.hostContext(), registered)

	// Drop retained state for hosts that are no longer registered, or their
	// rows would outlive the host they came from.
	live := make(map[string]bool, len(registered))
	for _, host := range registered {
		live[host.ID] = true
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
	generation := m.generation
	return tea.Tick(hostStaleTick, func(time.Time) tea.Msg {
		return hostStaleTickMsg{Generation: generation}
	})
}

// handleHostUpdate folds one host's new state into the model.
func (m *Model) handleHostUpdate(msg hostUpdateMsg) tea.Cmd {
	if m.hostRegistry == nil {
		return nil
	}
	update := msg.Update
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
func (m *Model) handleHostStaleTick(msg hostStaleTickMsg) tea.Cmd {
	if m.hostRegistry == nil {
		return nil
	}
	if m.hostRegistry.MarkStaleIfQuiet() {
		// The transition arrives as an ordinary update; nothing to do here but
		// let it come.
		_ = msg
	}
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
func (m *Model) eachHostWorkspace(visit func(project Project, label string, workspace workspaceinventory.Workspace, stale bool)) {
	for _, id := range m.hostOrder() {
		stale := !m.hostHealth[id].State.Healthy()
		for _, project := range m.hostProjects[id] {
			label := id + hostRowPrefix + project.Name
			for _, result := range m.hostResults[id] {
				if result.ProjectKey != project.Key {
					continue
				}
				for _, workspace := range result.Workspaces {
					visit(project, label, workspace, stale)
				}
			}
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
func hostHealthRowID(id string) string { return "host-health:" + id }

// IsHostHealthRow reports whether a selected row is a host's health rather
// than a workspace.
func IsHostHealthRow(id string) bool { return strings.HasPrefix(id, "host-health:") }

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

// HostSummary is one line about every registered host, for the places that
// want to say how many machines are being watched and whether they are well.
func (m *Model) HostSummary() string {
	if m.hostRegistry == nil {
		return ""
	}
	ids := m.hostOrder()
	if len(ids) == 0 {
		return ""
	}
	online := 0
	for _, id := range ids {
		if m.hostHealth[id].State.Healthy() {
			online++
		}
	}
	return fmt.Sprintf("%d/%d hosts online", online, len(ids))
}
