package notify

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
)

// LaneTracker turns a stream of per-workspace agent states into notifications
// about the *transitions* between them.
//
// It is deliberately state-free with respect to the rest of sidecar: it knows
// nothing about tmux, plugins, or Bubble Tea. A caller — the workspace plugin
// today, a headless watcher tomorrow — resolves whatever it already has into
// []LaneObservation and hands the whole set to Observe on whatever cadence it
// polls at. Everything else (which lanes are worth a notification, how long a
// state must hold before it counts, what self-dismisses) lives here so both
// callers cannot drift.
//
// Two rules do all the work:
//
//   - Only a *settled* lane change posts. A lane must hold for Debounce before
//     it is committed, so an agent flickering working→blocked→working while a
//     prompt renders produces nothing at all.
//   - A committed lane is the tracker's new truth, so the same logical event
//     can never post twice: the next post needs a different settled lane first.
type LaneTracker struct {
	// Debounce is how long a new lane must hold before it counts as a real
	// transition. Zero uses DefaultLaneDebounce.
	Debounce time.Duration
	states   map[string]*laneState
}

// DefaultLaneDebounce is the window a lane must hold before it posts. Long
// enough to swallow the flicker as an agent's prompt renders, short enough that
// "your agent is waiting" still arrives while the user is looking.
const DefaultLaneDebounce = 3 * time.Second

// LaneObservation is one workspace's resolved agent state at a moment in time.
// The caller supplies the identity and the presentation; the tracker never
// re-resolves anything.
type LaneObservation struct {
	// Key is the stable identity of the workspace — a tmux session name or a
	// worktree identity key. It is what the tracker remembers state under, so
	// it must not change while the workspace lives.
	Key string
	// Label names the shell or worktree for a human ("Shell 2", "auth-flow").
	Label string
	// Context is the surrounding place: the project, the branch, the repo. It
	// becomes the notification body so a toast identifies which of five agents
	// is talking.
	Context string
	// Provider is the agent kind ("claude", "codex"), if known.
	Provider string
	// Presentation is the already-resolved agent status for this workspace.
	Presentation agentstatus.Presentation
	// Origin records who this notification belongs to, so an agent that posted
	// through the CLI and a lane trigger about the same shell agree on identity
	// and the origin-checked dismiss path keeps working.
	Origin Origin
	// ProjectRoot is the stable owning project identity already resolved by the
	// producer. It may differ from Origin.WorkDir for an external worktree.
	ProjectRoot string
}

// LaneEvents is what one Observe call produced: notifications to post, and the
// ids of notifications the tracker itself posted earlier and now considers
// answered.
type LaneEvents struct {
	// Post are complete notifications, ids already assigned. The ids are
	// assigned here rather than by the store because the tracker has to be able
	// to name a waiting notification later in order to withdraw it.
	Post []Notification
	// Dismiss are ids of previously posted waiting notifications whose agent has
	// stopped waiting. A "needs input" toast that outlives the wait is worse
	// than no toast at all, so the tracker withdraws its own.
	Dismiss []string
}

// Empty reports whether nothing happened.
func (e LaneEvents) Empty() bool { return len(e.Post) == 0 && len(e.Dismiss) == 0 }

type laneState struct {
	lane        agentstatus.LaneID
	health      bool
	candidate   agentstatus.LaneID
	candHealth  bool
	candidateAt time.Time
	// waitingID is the sticky waiting notification currently on screen for this
	// workspace, if any. Withdrawing it on the transition away is what stops the
	// centre filling with stale "needs input" rows.
	waitingID string
}

func (t *LaneTracker) debounce() time.Duration {
	if t.Debounce > 0 {
		return t.Debounce
	}
	return DefaultLaneDebounce
}

// Observe folds one complete round of observations into the tracker and returns
// whatever transitions settled. Passing the *whole* set matters: a workspace
// missing from obs is treated as gone, which is how a closed shell withdraws
// its waiting notification.
func (t *LaneTracker) Observe(obs []LaneObservation, now time.Time) LaneEvents {
	if t.states == nil {
		t.states = make(map[string]*laneState, len(obs))
	}
	now = now.UTC()
	var events LaneEvents

	present := make(map[string]bool, len(obs))
	for _, o := range obs {
		if o.Key == "" {
			continue
		}
		present[o.Key] = true
		lane := o.Presentation.Lane
		if lane == "" {
			continue
		}
		st, ok := t.states[o.Key]
		if !ok {
			// First sight is a baseline, never a notification. Without this,
			// starting sidecar next to four blocked agents opens with four
			// toasts about states the user already knew about.
			t.states[o.Key] = &laneState{lane: lane, health: o.Presentation.Health}
			continue
		}
		if lane == st.lane && o.Presentation.Health == st.health {
			st.candidate = ""
			continue
		}
		if lane != st.candidate || o.Presentation.Health != st.candHealth {
			st.candidate = lane
			st.candHealth = o.Presentation.Health
			st.candidateAt = now
			continue
		}
		if now.Sub(st.candidateAt) < t.debounce() {
			continue
		}
		prior := st.lane
		st.lane = lane
		st.health = o.Presentation.Health
		st.candidate = ""
		t.commit(st, prior, o, now, &events)
	}

	for key, st := range t.states {
		if present[key] {
			continue
		}
		// A workspace that vanished takes its waiting notification with it. It
		// does not earn a "session died" of its own: a shell the user closed is
		// not an incident, and a session that actually failed reaches the paused
		// health lane first, while it is still being observed.
		if st.waitingID != "" {
			events.Dismiss = append(events.Dismiss, st.waitingID)
		}
		delete(t.states, key)
	}
	return events
}

// commit turns one settled lane change into notifications.
func (t *LaneTracker) commit(st *laneState, prior agentstatus.LaneID, o LaneObservation, now time.Time, events *LaneEvents) {
	// Leaving the blocked lane answers the wait, whatever it moved to.
	if prior == agentstatus.LaneBlocked && st.waitingID != "" {
		events.Dismiss = append(events.Dismiss, st.waitingID)
		st.waitingID = ""
	}

	switch {
	case st.lane == agentstatus.LaneBlocked:
		n := laneNotification(o, now, SourceWaiting, SeverityWarning,
			TransitionWaiting, fmt.Sprintf("%s needs input", laneName(o)), laneBody(o))
		n.Sticky = true
		st.waitingID = n.ID
		events.Post = append(events.Post, n)

	case st.lane == agentstatus.LanePaused && o.Presentation.Health:
		// Health in the paused lane is the failure signal: missing, orphaned,
		// errored. Only worth saying if the agent was live a moment ago.
		if prior != agentstatus.LaneWorking && prior != agentstatus.LaneBlocked && prior != agentstatus.LaneDone {
			return
		}
		events.Post = append(events.Post, laneNotification(o, now, SourceSession, SeverityError,
			TransitionFailure, fmt.Sprintf("%s session ended", laneName(o)), laneBody(o)))

	case st.lane == agentstatus.LaneDone:
		// Done means "just finished a turn". Reaching it from idle or paused is
		// bookkeeping, not a finish.
		if prior != agentstatus.LaneWorking && prior != agentstatus.LaneBlocked {
			return
		}
		events.Post = append(events.Post, laneNotification(o, now, SourceSession, SeverityInfo,
			TransitionDone, fmt.Sprintf("%s finished", laneName(o)), laneBody(o)))
	}
}

func laneNotification(o LaneObservation, now time.Time, source SourceID, sev Severity, class TransitionClass, title, body string) Notification {
	stableOrigin := o.Origin.StableKey()
	dedupeKey := o.Key + ":" + string(class)
	if stableOrigin != "" {
		dedupeKey = stableOrigin + ":" + string(class)
	}
	return Notification{
		ID:        NewID(),
		Source:    source,
		Severity:  sev,
		Title:     title,
		Body:      body,
		CreatedAt: now.UTC(),
		Origin:    o.Origin,
		Transition: &TransitionMetadata{
			Class: class, LaneKey: o.Key, DedupeKey: dedupeKey, ReplacementKey: stableOrigin,
			ProjectRoot: lexicalProjectRoot(o.ProjectRoot),
		},
	}
}

// TransitionOwnedByProject is the deterministic ownership gate for restart
// seeding and post reconciliation. New structured records compare their
// producer-captured root directly. Legacy records without that field are
// accepted only when Origin.WorkDir is lexically inside the current root;
// basename-only ProjectKey records are ambiguous and remain retained without
// giving this project authority to dismiss them.
func TransitionOwnedByProject(n Notification, projectRoot string) bool {
	meta := n.Transition
	if meta == nil || meta.LaneKey == "" {
		return false
	}
	// A record forwarded from a remote host belongs to no local project, even
	// when that host has a checkout at the same path. Adopting one would seed a
	// local lane tracker with a key no local observation can produce, and the
	// next complete sweep would withdraw the remote agent's wait while it was
	// still waiting.
	if n.Origin.HostID != "" {
		return false
	}
	projectRoot = lexicalProjectRoot(projectRoot)
	if projectRoot == "" {
		return false
	}
	if owner := lexicalProjectRoot(meta.ProjectRoot); owner != "" {
		return owner == projectRoot
	}
	if key := strings.TrimSpace(n.Origin.ProjectKey); key != "" && key != filepath.Base(projectRoot) {
		return false
	}
	workDir := lexicalProjectRoot(n.Origin.WorkDir)
	if workDir == "" {
		return false
	}
	return workDir == projectRoot || strings.HasPrefix(workDir, strings.TrimSuffix(projectRoot, string(filepath.Separator))+string(filepath.Separator))
}

func lexicalProjectRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// Seed restores the latest retained transition for each lane. A retained
// waiting record makes the blocked episode resumable across restart. A
// user-dismissed wait seeds the lane without restoring waitingID, so it stays
// dismissed until the lane leaves and later blocks again.
func (t *LaneTracker) Seed(all []Notification) {
	if t.states == nil {
		t.states = make(map[string]*laneState)
	}
	ordered := append([]Notification(nil), all...)
	SortNewestFirst(ordered)
	seen := make(map[string]bool)
	for _, n := range ordered {
		meta := n.Transition
		if meta == nil || meta.LaneKey == "" || seen[meta.LaneKey] {
			continue
		}
		seen[meta.LaneKey] = true
		if meta.Class != TransitionWaiting {
			continue
		}
		st, exists := t.states[meta.LaneKey]
		if !exists {
			st = &laneState{}
			t.states[meta.LaneKey] = st
		}
		// The seed is authoritative for the episode that survived on disk. This
		// also closes the startup race where an async agent baseline arrives
		// before the seed message: the next settled observation still reconciles
		// the retained wait instead of silently orphaning it.
		st.lane = agentstatus.LaneBlocked
		st.candidate = ""
		st.waitingID = ""
		if !n.Dismissed() {
			st.waitingID = n.ID
		}
	}
}

// ReconcilePosted adopts the store's authoritative id after Post. This matters
// when another process won logical-event dedupe: the local tracker generated a
// different id and must dismiss the retained winner when the lane leaves.
func (t *LaneTracker) ReconcilePosted(n Notification) {
	meta := n.Transition
	if meta == nil || meta.Class != TransitionWaiting || meta.LaneKey == "" || t.states == nil {
		return
	}
	if st := t.states[meta.LaneKey]; st != nil && st.lane == agentstatus.LaneBlocked {
		st.waitingID = n.ID
	}
}

// laneName is how the workspace is identified in a title.
func laneName(o LaneObservation) string {
	if s := strings.TrimSpace(o.Label); s != "" {
		return s
	}
	if s := strings.TrimSpace(o.Provider); s != "" {
		return s
	}
	return "Agent"
}

// laneBody carries the rest of the identity: which agent, where it lives, and
// whatever evidence the status resolver had.
func laneBody(o LaneObservation) string {
	parts := make([]string, 0, 3)
	if s := strings.TrimSpace(o.Provider); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(o.Context); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(o.Presentation.Evidence); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}
