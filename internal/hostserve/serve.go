// Package hostserve is the headless half of Sidecar's remote-host support: the
// loop behind `sidecar host serve --stdio`.
//
// It is orchestration, not new behaviour. The awareness stack —
// workspaceinventory, agentactivity, agentstatus, shellliveness, tmuxserver —
// has no Bubble Tea dependency and takes injectable runners, captures, and
// clocks, so the whole of it runs headlessly with no reimplementation. What
// this package adds is the cycle that the Overview model contributes locally:
// the identity/inventory/status phase order, the shell-claim pass between
// them, the adaptive poll cadence, and tracker commit at the end of a
// completed generation.
//
// # Read-only
//
// Serve writes nothing: it does not touch shells.json, does not reap, does not
// take a geometry lease, and does not resize a pane. When mutations arrive in
// Phase C they go through the existing guarded writers in
// workspaceops/shellstate, never through bespoke logic here.
//
// Be precise about what kind of guarantee that is, because overstating it is
// how it quietly stops being true. It is NOT "the package links in no writer":
// hostserve depends on internal/tty, which contains resize-window, send-keys,
// kill-session and the geometry lease. The guarantee is call-graph discipline
// — the only tty function reached from here is CapturePaneWithState, and the
// only subprocesses this package can cause are `tmux list-panes`,
// `tmux capture-pane`, `tmux display-message`, git, and ps.
//
// TestServeIsReadOnly enforces that by asserting on the commands actually
// issued, which is the level the guarantee actually holds at. Anyone adding a
// call into tty from this package is responsible for keeping it true; the
// shells-wipe incident (td-8d18de) is what the care is for.
//
// The one place that discipline needs care is the capture path, because a
// capture is an observation that can have a side effect: the Overview's
// semantic preview resizes a pane under a geometry lease before capturing it.
// Serve never calls that path. It captures at whatever size the pane already
// is, exactly as the ordinary status pass does.
package hostserve

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/buildinfo"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Poll cadences. These are the Overview's numbers and the comment explaining
// them is the Overview's reasoning: an idle agent is not inert, it is an agent
// at a prompt that can start a turn at any moment, so only a host with nothing
// live at all earns the quiet cadence.
const (
	DefaultLivePoll  = 5 * time.Second
	DefaultReadyPoll = 10 * time.Second
	DefaultIdlePoll  = 30 * time.Second

	// DefaultInventoryEvery is how often the expensive phase — git worktree
	// listing and state-tree reads — runs. In between, only tmux evidence is
	// refreshed, which is the same split RefreshProjectStatus exists to make.
	DefaultInventoryEvery = 60 * time.Second

	// DefaultPreviewLines matches the status pass's own capture depth, so
	// shipping a preview costs no extra capture at all: serve reuses the text
	// the status pass already took.
	DefaultPreviewLines = 80

	// DefaultPreviewBytes bounds one preview on the wire. The local capture
	// discipline is a byte cap (plugins.workspace.tmuxCaptureMaxBytes); this
	// is the same idea applied per row before transmission.
	DefaultPreviewBytes = 16 << 10

	// DefaultSnapshotPreviewBytes bounds the previews in ONE snapshot, which
	// the per-row cap does not: a host with enough agent panes would otherwise
	// build a message past hostproto.MaxLineBytes, and a viewer whose scanner
	// hits that limit is dead for the rest of the connection with no resync.
	// Rows past the budget ship without a preview rather than not at all.
	DefaultSnapshotPreviewBytes = 1 << 20

	// DefaultMaxCaptures bounds concurrent capture-pane subprocesses, the same
	// bound the Overview applies for the same reason: an unbounded fan-out
	// over a machine's panes is felt by the human sitting at that machine.
	DefaultMaxCaptures = 4
)

// Project names one project the host should observe. It mirrors the remote
// machine's own config `projects.list` entries; discovery is unchanged.
type Project struct {
	Name string
	Path string
}

// Options configures a serve loop. Every field has a working default, and the
// injectable ones exist so tests can drive the whole loop with no tmux, no
// filesystem, and a fake clock.
type Options struct {
	// Out receives the JSONL stream. In production this is os.Stdout, which is
	// the ssh pipe.
	Out io.Writer

	// Projects is what to observe. Empty means the loop still runs and still
	// emits a hello and empty snapshots, which is the correct behaviour for a
	// host that has sidecar installed but no projects configured — a viewer
	// must be able to tell that apart from an unreachable host.
	Projects []Project

	// HostID scopes every emitted item ID. A viewer keying rows on the
	// collector's ID alone would collide two hosts that happen to agree.
	HostID string

	Now     func() time.Time
	Runner  workspaceinventory.Runner
	Capture workspaceinventory.CaptureFunc

	// ServerIncarnation reads the tmux server's identity. Injectable so a test
	// can drive a server death without killing one.
	ServerIncarnation func() tmuxserver.Incarnation

	// Hostname, TmuxPath and TmuxVersion feed the hello. Injectable for the
	// same reason.
	Hostname    func() string
	TmuxPath    func() (string, bool)
	TmuxVersion func() string

	LivePoll       time.Duration
	ReadyPoll      time.Duration
	IdlePoll       time.Duration
	InventoryEvery time.Duration

	PreviewLines int
	PreviewBytes int
	// SnapshotPreviewBytes bounds the total preview payload in one snapshot.
	SnapshotPreviewBytes int
	MaxCaptures          int

	// Cycles bounds how many collection cycles run before Serve returns. Zero
	// means run until the context is cancelled. A measurement harness sets it
	// so a run terminates on its own.
	Cycles int

	// OnCycle is called after each completed cycle with its wall duration. It
	// exists for the Phase 0 measurement harness; production leaves it nil.
	OnCycle func(generation uint64, elapsed time.Duration, captures int64)
}

func (o Options) withDefaults() Options {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Runner == nil {
		o.Runner = workspaceinventory.ExecRunner{}
	}
	if o.ServerIncarnation == nil {
		o.ServerIncarnation = tmuxserver.Socket
	}
	if o.Hostname == nil {
		o.Hostname = func() string {
			name, err := os.Hostname()
			if err != nil {
				return "unknown"
			}
			return name
		}
	}
	if o.TmuxPath == nil {
		o.TmuxPath = func() (string, bool) {
			path, err := exec.LookPath("tmux")
			return path, err == nil
		}
	}
	if o.TmuxVersion == nil {
		o.TmuxVersion = func() string {
			out, err := exec.Command("tmux", "-V").Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}
	}
	if o.LivePoll <= 0 {
		o.LivePoll = DefaultLivePoll
	}
	if o.ReadyPoll <= 0 {
		o.ReadyPoll = DefaultReadyPoll
	}
	if o.IdlePoll <= 0 {
		o.IdlePoll = DefaultIdlePoll
	}
	if o.InventoryEvery <= 0 {
		o.InventoryEvery = DefaultInventoryEvery
	}
	if o.PreviewLines <= 0 {
		o.PreviewLines = DefaultPreviewLines
	}
	if o.PreviewBytes <= 0 {
		o.PreviewBytes = DefaultPreviewBytes
	}
	if o.SnapshotPreviewBytes <= 0 {
		o.SnapshotPreviewBytes = DefaultSnapshotPreviewBytes
	}
	if o.MaxCaptures <= 0 {
		o.MaxCaptures = DefaultMaxCaptures
	}
	if o.HostID == "" {
		o.HostID = o.Hostname()
	}
	return o
}

// Serve runs the collection loop until ctx is cancelled, the configured cycle
// budget is exhausted, or the output pipe dies. A dead pipe is a normal
// ending: it means the viewer disconnected and this ephemeral process has no
// further reason to exist.
func Serve(ctx context.Context, opts Options) error {
	if opts.Out == nil {
		return fmt.Errorf("hostserve: no output writer")
	}
	opts = opts.withDefaults()

	previews := newPreviewStore(opts.PreviewBytes)
	capture := opts.Capture
	if capture == nil {
		capture = tty.CapturePaneWithState
	}
	// Wrapping the capture function is how previews reach the wire without the
	// collector knowing previews exist. The status pass already captures ~80
	// lines per agent pane and then discards the text once it has extracted
	// activity from it; this decorator keeps the last text per pane. No
	// collector change, no second capture, no extra load on the remote
	// machine.
	instrumented := func(target string, lines int) (string, tty.PaneState, error) {
		text, state, err := capture(target, lines)
		if err == nil {
			previews.put(target, text)
		}
		return text, state, err
	}

	collector := workspaceinventory.Collector{
		Runner:  opts.Runner,
		Capture: instrumented,
		Now:     opts.Now,
	}.WithDefaults()

	liveness := shellliveness.NewTracker()
	encoder := hostproto.NewEncoder(opts.Out)
	encoder.SetClock(opts.Now)

	if err := encoder.Encode(hostproto.Message{Kind: hostproto.KindHello, Hello: buildHello(opts)}); err != nil {
		return err
	}

	var (
		generation          uint64
		inventories         = make(map[string]workspaceinventory.ProjectResult, len(opts.Projects))
		lastInventory       time.Time
		previous            map[string]hostproto.Item
		previousIncarnation uint64
	)

	for {
		if ctx.Err() != nil {
			return nil
		}
		generation++
		cycleStart := opts.Now()
		// Previews describe this cycle only. See previewStore.
		previews.reset()

		now := opts.Now()
		fullInventory := lastInventory.IsZero() || now.Sub(lastInventory) >= opts.InventoryEvery
		if fullInventory {
			for _, project := range opts.Projects {
				inventories[project.Path] = collector.CollectProjectInventory(ctx, project.Name, project.Path)
			}
			lastInventory = now
		}

		// One global pane listing per cycle. Every project's status pass is
		// correlated against this same list, which is what makes shell
		// collision resolution possible at all.
		panes, paneErr := collector.ListPanes(ctx)
		incarnation := opts.ServerIncarnation()
		liveness.ObserveServer(incarnation)

		ordered := make([]workspaceinventory.ProjectResult, 0, len(opts.Projects))
		roots := make([]string, 0, len(opts.Projects))
		for _, project := range opts.Projects {
			result, ok := inventories[project.Path]
			if !ok {
				continue
			}
			ordered = append(ordered, result)
			roots = append(roots, project.Path)
		}
		claims := workspaceinventory.BuildShellClaims(ordered)
		refresh := collector.ForRefresh(opts.MaxCaptures, claims)

		previewBudget := opts.SnapshotPreviewBytes
		snapshot := hostproto.Snapshot{
			Generation:        generation,
			ObservedAt:        now,
			ServerIncarnation: incarnationID(incarnation),
		}
		for _, project := range opts.Projects {
			base, ok := inventories[project.Path]
			if !ok {
				continue
			}
			result := refresh.RefreshProjectStatus(ctx, base, roots, panes)
			inventories[project.Path] = result
			for _, workspace := range result.Workspaces {
				if workspace.Live && workspace.TmuxName != "" {
					liveness.Observe(workspace.TmuxName)
				}
			}
			snapshot.Projects = append(snapshot.Projects, projectMessage(result, opts.HostID, previews, &previewBudget))
		}
		refresh.CommitTrackers()

		if paneErr != nil {
			// ListPanes already answers "no server running" and "no sessions"
			// with an empty list and no error, so a non-nil error here means
			// tmux is present and something else went wrong. Reporting it as
			// ErrNoTmux would tell the user to install software they already
			// have; ErrCollect says what is true.
			code := hostproto.ErrCollect
			if strings.Contains(paneErr.Error(), exec.ErrNotFound.Error()) {
				code = hostproto.ErrNoTmux
			}
			if err := encoder.Encode(hostproto.Message{Kind: hostproto.KindError, Error: &hostproto.Error{
				Code:    code,
				Message: fmt.Sprintf("tmux inventory failed on %s: %v", opts.HostID, paneErr),
			}}); err != nil {
				return err
			}
		}

		// The server event goes out BEFORE this cycle's rows. A viewer that
		// learned liveness was suspect only after applying the new server's
		// rows would have already trusted them.
		if generation > 1 && previousIncarnation != snapshot.ServerIncarnation {
			if err := encoder.Encode(hostproto.Message{Kind: hostproto.KindEvent, Event: &hostproto.Event{
				Kind:              hostproto.EventServer,
				Generation:        generation,
				ServerIncarnation: snapshot.ServerIncarnation,
			}}); err != nil {
				return err
			}
		}

		current := indexItems(snapshot)
		// A full snapshot goes out on the first cycle and whenever the
		// expensive inventory re-ran, so a viewer that missed events for any
		// reason reconverges without asking. Everything in between is a delta.
		if fullInventory {
			if err := encoder.Encode(hostproto.Message{Kind: hostproto.KindSnapshot, Snapshot: &snapshot}); err != nil {
				return err
			}
		} else {
			for _, event := range diffItems(previous, current, generation) {
				if err := encoder.Encode(hostproto.Message{Kind: hostproto.KindEvent, Event: &event}); err != nil {
					return err
				}
			}
		}
		previous = current
		previousIncarnation = snapshot.ServerIncarnation

		if opts.OnCycle != nil {
			opts.OnCycle(generation, opts.Now().Sub(cycleStart), refresh.Metrics().Captures)
		}
		if opts.Cycles > 0 && generation >= uint64(opts.Cycles) {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(pollInterval(snapshot, opts)):
		}
	}
}

// pollInterval mirrors the Overview's cadence rule exactly, including its
// subtlety: idle and done still earn the ready cadence, because an idle agent
// is one at a prompt that can start a turn at any moment.
func pollInterval(snapshot hostproto.Snapshot, opts Options) time.Duration {
	interval := opts.IdlePoll
	for _, project := range snapshot.Projects {
		for _, item := range project.Items {
			if item.Agent == nil {
				continue
			}
			switch agentstatus.LaneID(item.Agent.Lane) {
			case agentstatus.LaneWorking, agentstatus.LaneBlocked:
				return opts.LivePoll
			case agentstatus.LaneIdle, agentstatus.LaneDone:
				interval = opts.ReadyPoll
			}
		}
	}
	return interval
}

func buildHello(opts Options) *hostproto.Hello {
	_, tmuxPresent := opts.TmuxPath()
	hello := &hostproto.Hello{
		Proto:       hostproto.Version,
		Version:     buildinfo.Version(),
		Host:        opts.Hostname(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		TmuxPresent: tmuxPresent,
		Projects:    len(opts.Projects),
		Capabilities: hostproto.Capabilities{
			// argv0 disambiguation of shared-runtime panes is implemented on
			// darwin only; process_identity_other.go is a stub. Saying so in
			// the hello is what lets a viewer render honest confidence instead
			// of presenting a degraded provider guess as fact.
			ProcessIdentity: runtime.GOOS == "darwin",
			IsolatedState:   os.Getenv("SIDECAR_ISOLATED_STATE") == "1",
			// The RESOLVED root, not $XDG_STATE_HOME. The raw variable is
			// empty in an ordinary run and names the parent in an isolated
			// one, so echoing it made the isolation evidence wrong in every
			// case — which is the opposite of what this field is for.
			StateDir: config.StateDir(),
		},
	}
	if tmuxPresent {
		hello.TmuxVersion = opts.TmuxVersion()
	}
	incarnation := opts.ServerIncarnation()
	hello.ServerRunning = incarnation.IsPresent()
	return hello
}

func projectMessage(result workspaceinventory.ProjectResult, hostID string, previews *previewStore, budget *int) hostproto.Project {
	project := hostproto.Project{
		Key:  result.ProjectKey,
		Name: result.ProjectName,
		Root: result.ProjectRoot,
	}
	if result.Err != nil {
		project.Err = result.Err.Error()
	}
	for _, workspace := range result.Workspaces {
		project.Items = append(project.Items, itemMessage(workspace, hostID, previews, budget))
	}
	return project
}

func itemMessage(w workspaceinventory.Workspace, hostID string, previews *previewStore, budget *int) hostproto.Item {
	item := hostproto.Item{
		ID:          w.ID,
		HostID:      hostID,
		ProjectKey:  w.ProjectKey,
		ProjectName: w.ProjectName,
		ProjectRoot: w.ProjectRoot,
		Kind:        string(w.Kind),
		Key:         w.Key,
		Name:        w.Name,
		Path:        w.Path,
		Branch:      w.Branch,
		TaskID:      w.TaskID,
		Provider:    w.Provider,
		PaneID:      w.PaneID,
		Session:     w.TmuxName,
		Live:        w.Live,
		Ambiguous:   w.Ambiguous,
		IsMain:      w.IsMain,
		ObservedAt:  w.ObservedAt,
	}
	if w.HasAgent() {
		presentation := presentationMessage(w.Presentation)
		item.Agent = &presentation
	}
	if w.PaneID != "" {
		if preview := previews.get(w.PaneID); len(preview) <= *budget {
			item.Preview = preview
			*budget -= len(preview)
		}
	}
	return item
}

func presentationMessage(p agentstatus.Presentation) hostproto.Presentation {
	return hostproto.Presentation{
		Lane:       string(p.Lane),
		Icon:       p.Icon,
		Label:      p.Label,
		Attention:  p.Attention,
		Evidence:   p.Evidence,
		ChangedAt:  p.ChangedAt,
		CapturedAt: p.CapturedAt,
		Health:     p.Health,
		Semantic:   p.Semantic,
		Freshness:  string(p.Freshness),
		Inferred:   p.Inferred,
	}
}

func indexItems(snapshot hostproto.Snapshot) map[string]hostproto.Item {
	items := make(map[string]hostproto.Item)
	for _, project := range snapshot.Projects {
		for _, item := range project.Items {
			items[item.ID] = item
		}
	}
	return items
}

// diffItems produces the delta events between two cycles. Ordering is
// deterministic — sorted by item ID — so a transcript is comparable across
// runs, which matters when the transcript is the evidence.
func diffItems(previous, current map[string]hostproto.Item, generation uint64) []hostproto.Event {
	if previous == nil {
		return nil
	}
	ids := make([]string, 0, len(current)+len(previous))
	seen := make(map[string]bool, len(current)+len(previous))
	for id := range current {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for id := range previous {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	var events []hostproto.Event
	for _, id := range ids {
		before, hadBefore := previous[id]
		after, hasAfter := current[id]
		switch {
		case !hadBefore && hasAfter:
			item := after
			events = append(events, hostproto.Event{
				Kind: hostproto.EventAppear, Generation: generation,
				ProjectKey: after.ProjectKey, ItemID: id, Item: &item,
				To: laneOf(after),
			})
		case hadBefore && !hasAfter:
			events = append(events, hostproto.Event{
				Kind: hostproto.EventDisappear, Generation: generation,
				ProjectKey: before.ProjectKey, ItemID: id, From: laneOf(before),
			})
		default:
			if laneOf(before) == laneOf(after) && before.Live == after.Live {
				continue
			}
			item := after
			events = append(events, hostproto.Event{
				Kind: hostproto.EventStatus, Generation: generation,
				ProjectKey: after.ProjectKey, ItemID: id,
				From: laneOf(before), To: laneOf(after), Item: &item,
			})
		}
	}
	return events
}

func laneOf(item hostproto.Item) string {
	if item.Agent == nil {
		return ""
	}
	return item.Agent.Lane
}

// incarnationID hashes tmuxserver's opaque identity into a wire-stable
// integer. The identity itself is (inode, ctime, pid) and has no exported
// numeric form; the viewer only ever needs "same or different", so a hash of
// the canonical string is exactly enough and keeps tmuxserver's internals
// unexported.
func incarnationID(inc tmuxserver.Incarnation) uint64 {
	if inc.IsUnknown() {
		return 0
	}
	if inc.IsAbsent() {
		return 1
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(inc.String()))
	// Reserve 0 and 1 for unknown/absent so a present server can never
	// collide with them.
	return hash.Sum64() | 2
}

// previewStore keeps the capture text taken during the current cycle,
// truncated to the wire budget. Truncation keeps the tail: the bottom of a
// pane is where an agent's current prompt and its question live, which is the
// whole reason a preview cell is worth showing.
//
// It is deliberately per-cycle rather than a cache. Retaining text across
// cycles is worse than showing nothing twice over:
//
//   - A capture that fails for one cycle would ship the previous cycle's
//     screen as if it were current, with a CapturedAt of now. A preview that
//     is silently stale is exactly the failure mode this whole feature exists
//     to avoid.
//   - tmux restarts pane IDs at %0 after a server restart, so a retained entry
//     for %1 would be painted into a brand-new, unrelated pane.
//   - A long-lived connection would accumulate an entry for every pane ID it
//     ever saw, at up to the byte limit each, with nothing ever evicting them.
//
// The cost of resetting is that a row whose capture failed ships no preview
// that cycle, which is the honest answer.
//
// Not synchronised, because the serve loop refreshes projects sequentially
// (workspaceinventory.RefreshProjectStatus is a plain loop, and Serve calls it
// one project at a time). Introducing concurrency into that loop must add a
// mutex here.
type previewStore struct {
	limit int
	text  map[string]string
}

func newPreviewStore(limit int) *previewStore {
	return &previewStore{limit: limit, text: make(map[string]string)}
}

// reset drops every retained preview. Called at the top of each cycle.
func (s *previewStore) reset() {
	clear(s.text)
}

func (s *previewStore) put(paneID, text string) {
	if len(text) > s.limit {
		text = text[len(text)-s.limit:]
		// Never leave a split UTF-8 sequence at the cut.
		for len(text) > 0 && text[0]&0xC0 == 0x80 {
			text = text[1:]
		}
	}
	s.text[paneID] = text
}

func (s *previewStore) get(paneID string) string { return s.text[paneID] }
