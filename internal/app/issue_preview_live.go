package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/livewatch"
)

// Live refresh for the app's issue preview modal — the third host of an
// issueview card, after the workspace pane and the global browser's preview.
//
// Landing a refresh on two of the three would be the parity bug AGENTS.md warns
// about: the same card, opened a different way, quietly behaving differently.
// The modal is the surface most likely to be open while an agent works, since
// it is what "preview this issue" from anywhere produces.
//
// The watcher lives exactly as long as the modal. It is started when the modal
// opens, from inside a tea.Cmd, and stopped when the modal closes — a modal is
// not a place to hold descriptors for the life of the session.

type (
	issuePreviewWatchStartedMsg struct {
		IssueID string
		Watcher *livewatch.PathWatcher
	}
	issuePreviewStoreChangedMsg struct{}
)

// startIssuePreviewWatch begins watching the td store behind the previewed
// issue. It is called when the modal opens.
func (m *Model) startIssuePreviewWatch(workDir, issueID string) tea.Cmd {
	m.stopIssuePreviewWatch()
	if workDir == "" {
		return nil
	}
	// Resolving the store location walks parents and can shell out to git, so it
	// happens inside the command with the rest of the setup.
	return func() tea.Msg {
		targets := issueview.StoreTargets(workDir)
		if len(targets) == 0 {
			return issuePreviewWatchStartedMsg{IssueID: issueID}
		}
		w, err := livewatch.NewPathWatcher(livewatch.Config{
			// The store moves whenever any issue changes, and one `td` command
			// can write several times. A generous settle turns a burst into one
			// re-read; the ticket asks for a second or two, not sub-second.
			Quiet:      400 * time.Millisecond,
			MaxLatency: 2 * time.Second,
		})
		if err != nil {
			return issuePreviewWatchStartedMsg{IssueID: issueID}
		}
		w.Watch(targets...)
		return issuePreviewWatchStartedMsg{IssueID: issueID, Watcher: w}
	}
}

// stopIssuePreviewWatch releases the modal's watcher. Stop blocks until the
// watcher goroutine drains, so it runs detached rather than stalling the close.
func (m *Model) stopIssuePreviewWatch() {
	if m.issuePreviewWatcher == nil {
		return
	}
	w := m.issuePreviewWatcher
	m.issuePreviewWatcher = nil
	go w.Stop()
}

// handleIssuePreviewWatchStarted adopts the watcher, or stops it if the modal
// closed while it was being created.
func (m *Model) handleIssuePreviewWatchStarted(msg issuePreviewWatchStartedMsg) tea.Cmd {
	if msg.Watcher == nil {
		return nil
	}
	if !m.showIssuePreview || m.issuePreviewView == nil ||
		m.issuePreviewView.IssueID() != msg.IssueID {
		go msg.Watcher.Stop()
		return nil
	}
	m.stopIssuePreviewWatch()
	m.issuePreviewWatcher = msg.Watcher
	return livewatch.Listen(m.issuePreviewWatcher, issuePreviewStoreChangedMsg{})
}

// handleIssuePreviewStoreChanged re-reads the previewed issue and re-arms the
// listener.
func (m *Model) handleIssuePreviewStoreChanged() tea.Cmd {
	cmds := []tea.Cmd{livewatch.Listen(m.issuePreviewWatcher, issuePreviewStoreChangedMsg{})}
	if !m.showIssuePreview || m.issuePreviewView == nil {
		return tea.Batch(cmds...)
	}
	m.issuePreviewView.Observe()
	if cmd := m.issuePreviewView.Refresh(false); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// invalidateIssuePreviewModal drops the cached modal so the next frame rebuilds
// it from the refreshed card.
//
// The modal is cached on width and height alone, so a card whose content
// changed underneath a cache hit would keep rendering the old issue. Every
// path that changes the data has to say so; this is the refresh path's.
func (m *Model) invalidateIssuePreviewModal() {
	m.issuePreviewModal = nil
	m.issuePreviewModalWidth = 0
	m.issuePreviewModalHeight = 0
}
