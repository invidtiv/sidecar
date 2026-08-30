package overview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const remoteHostID = "mac-mini"

func remoteNotifyEvent(at time.Time) hostproto.NotifyEvent {
	origin := hostproto.NotifyOrigin{
		ItemID: "/home/me/api:shell:s1", ProjectKey: "/home/me/api",
		Session: "api-claude", Path: "/home/me/api",
	}
	return hostproto.NotifyEvent{
		Key: hostproto.NotifyKey(origin, hostproto.NotifyWaiting, at), OccurredAt: at,
		Class: hostproto.NotifyWaiting, Source: "waiting", Severity: "warning",
		Title: "Claude pane needs input", Body: "claude · api/main", Sticky: true, Origin: origin,
	}
}

// managedHostModel is a browser with managed-host notifications switched on.
func managedHostModel(t *testing.T, enabled bool) *Model {
	t.Helper()
	m := New(workspaceinventory.Collector{})
	cfg := config.Default()
	cfg.Notifications.SSH.ManagedHosts = enabled
	m.SetConfig(cfg)
	return m
}

// collectPosts runs a command tree and returns the notification messages it
// produced, which is exactly what the app's ordinary post seam would receive.
func collectPosts(t *testing.T, cmd tea.Cmd) ([]notify.Notification, []string) {
	t.Helper()
	if cmd == nil {
		return nil, nil
	}
	var posts []notify.Notification
	var dismissed []string
	var walk func(tea.Msg)
	walk = func(msg tea.Msg) {
		switch typed := msg.(type) {
		case notify.PostMsg:
			posts = append(posts, typed.Notification)
		case notify.DismissMsg:
			dismissed = append(dismissed, typed.ID)
		case tea.BatchMsg:
			for _, sub := range typed {
				if sub != nil {
					walk(sub())
				}
			}
		}
	}
	walk(cmd())
	return posts, dismissed
}

// The steel thread: one live remote transition becomes one ordinary local post
// carrying the remote identity, the transition metadata policy reads, and
// nothing from the wire that policy could be surprised by.
func TestForwardedTransitionBecomesAnOrdinaryLocalPost(t *testing.T) {
	m := managedHostModel(t, true)
	event := remoteNotifyEvent(time.Now().UTC())
	posts, dismissed := collectPosts(t, m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}))
	if len(posts) != 1 || len(dismissed) != 0 {
		t.Fatalf("posts = %d, dismissals = %d", len(posts), len(dismissed))
	}
	n := posts[0]
	if n.ID != notify.RemoteID(remoteHostID, event.Key) {
		t.Errorf("id = %q, want the derived remote id", n.ID)
	}
	if n.Source != notify.SourceWaiting || n.Severity != notify.SeverityWarning || !n.Sticky {
		t.Errorf("notification = %+v", n)
	}
	if n.Title != event.Title || n.Body != event.Body {
		t.Errorf("text = %q / %q", n.Title, n.Body)
	}
	if n.Origin.HostID != remoteHostID || n.Origin.TmuxSession != "api-claude" || n.Origin.WorkDir != "/home/me/api" {
		t.Errorf("origin = %+v", n.Origin)
	}
	if n.Transition == nil || n.Transition.Class != notify.TransitionWaiting {
		t.Fatalf("transition = %+v", n.Transition)
	}
	if !strings.HasPrefix(n.Transition.DedupeKey, remoteHostID+":") {
		t.Errorf("dedupe key %q is not scoped to the destination host", n.Transition.DedupeKey)
	}
	if !n.CreatedAt.Equal(event.OccurredAt) {
		t.Errorf("createdAt = %v, want the remote occurrence time", n.CreatedAt)
	}
}

// The setting governs local consumption, not the stream. With it off the rows
// still arrive; the notification simply is not filed.
func TestManagedHostNotificationsAreOffByDefault(t *testing.T) {
	if config.Default().Notifications.SSH.ManagedHosts {
		t.Fatal("managedHosts defaults on")
	}
	m := managedHostModel(t, false)
	event := remoteNotifyEvent(time.Now().UTC())
	if cmd := m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}); cmd != nil {
		posts, dismissed := collectPosts(t, cmd)
		t.Errorf("consumption is off but produced %d post(s) and %d dismissal(s)", len(posts), len(dismissed))
	}
}

// A transition that happened while nothing was listening, or that queued
// behind a slow link, is not news. Refusing it here means no record at all
// rather than a stale one policy has to keep quiet about.
func TestStaleForwardedEventIsNotStored(t *testing.T) {
	m := managedHostModel(t, true)
	old := remoteNotifyEvent(time.Now().UTC().Add(-notify.LiveEventGrace - time.Second))
	posts, _ := collectPosts(t, m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{old}}))
	if len(posts) != 0 {
		t.Errorf("a stale event produced %d post(s)", len(posts))
	}
}

// Leaving the blocked lane on the host withdraws the local record. The
// withdrawal names the derived ID, so a process that never saw the post can
// still retire it.
func TestForwardedWithdrawalDismissesTheDerivedRecord(t *testing.T) {
	m := managedHostModel(t, true)
	event := remoteNotifyEvent(time.Now().UTC())
	update := hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{{Withdraws: event.Key}}}
	posts, dismissed := collectPosts(t, m.forwardHostNotifications(update))
	if len(posts) != 0 {
		t.Errorf("a withdrawal posted %d notification(s)", len(posts))
	}
	if len(dismissed) != 1 || dismissed[0] != notify.RemoteID(remoteHostID, event.Key) {
		t.Errorf("dismissed = %v", dismissed)
	}
}

// Two Sidecars on this machine each hold their own connection to the same
// host, and each is told about the same transition. One record, one claim
// winner: the ID is derived from the host and the event, not generated.
func TestTwoLocalProcessesProduceOneRecordAndOneClaimWinner(t *testing.T) {
	stateDir := isolatedStateDir(t)
	event := remoteNotifyEvent(time.Now().UTC())

	first := managedHostModel(t, true)
	second := managedHostModel(t, true)
	update := hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}
	firstPosts, _ := collectPosts(t, first.forwardHostNotifications(update))
	secondPosts, _ := collectPosts(t, second.forwardHostNotifications(update))
	if len(firstPosts) != 1 || len(secondPosts) != 1 {
		t.Fatalf("posts = %d and %d", len(firstPosts), len(secondPosts))
	}
	if firstPosts[0].ID != secondPosts[0].ID {
		t.Fatalf("two viewers derived %q and %q", firstPosts[0].ID, secondPosts[0].ID)
	}

	// Two independent stores over one state tree, as two processes have.
	storeA, err := notify.Open(stateDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = storeA.Close() }()
	storeB, err := notify.Open(stateDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = storeB.Close() }()
	resultA, err := storeA.Post(firstPosts[0])
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resultB, err := storeB.Post(secondPosts[0])
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if !resultA.Created || resultB.Created {
		t.Errorf("created = %v and %v, want exactly one", resultA.Created, resultB.Created)
	}
	all, err := storeA.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("records = %d, want 1: %+v", len(all), all)
	}

	// And one claim: the delivery ledger keyed on that one ID has one winner
	// per channel, so only one of the two processes plays a sound.
	ledgerA, err := notifydelivery.Open(stateDir)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer func() { _ = ledgerA.Close() }()
	ledgerB, err := notifydelivery.Open(stateDir)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer func() { _ = ledgerB.Close() }()
	now := time.Now().UTC()
	wonA, _, err := ledgerA.Claim(all[0].ID, notifydelivery.ChannelSound, "process-a", now, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	wonB, _, err := ledgerB.Claim(all[0].ID, notifydelivery.ChannelSound, "process-b", now, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if wonA == wonB {
		t.Errorf("claim winners = %v and %v, want exactly one", wonA, wonB)
	}
}

// The dedupe boundary is one destination host. Another computer has its own
// state tree, so its user gets their own attention — that is the point.
func TestADifferentLocalComputerDeliversIndependently(t *testing.T) {
	event := remoteNotifyEvent(time.Now().UTC())
	m := managedHostModel(t, true)
	posts, _ := collectPosts(t, m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}))
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}

	// Two state trees, as two computers have. Each stores the record and each
	// claim is uncontested.
	for _, dir := range []string{t.TempDir(), t.TempDir()} {
		t.Setenv("SIDECAR_ISOLATED_STATE", "1")
		store, err := notify.Open(dir)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		result, err := store.Post(posts[0])
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		if !result.Created {
			t.Errorf("%s: the record was suppressed on a second computer", dir)
		}
		ledger, err := notifydelivery.Open(dir)
		if err != nil {
			t.Fatalf("open ledger: %v", err)
		}
		won, reason, err := ledger.Claim(posts[0].ID, notifydelivery.ChannelSound, "owner", time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if !won {
			t.Errorf("%s: claim refused (%s)", dir, reason)
		}
		_ = ledger.Close()
		_ = store.Close()
	}
}

// Visible-origin resolution has to be host-aware, or a local workspace that
// happens to share a session name would answer for a remote one.
func TestRemoteForegroundIsDecidedPerMachine(t *testing.T) {
	m := managedHostModel(t, true)
	event := remoteNotifyEvent(time.Now().UTC())
	posts, _ := collectPosts(t, m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}))
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}
	origin := uirequest.Origin{
		TmuxSession: posts[0].Origin.TmuxSession, ProjectKey: posts[0].Origin.ProjectKey,
		WorkDir: posts[0].Origin.WorkDir, HostID: posts[0].Origin.HostID,
	}

	visible := func(o plugin.AttentionOrigin) []uirequest.Attention {
		return []uirequest.Attention{{PID: 1, Focused: true, VisibleOrigin: uirequest.Origin{
			TmuxSession: o.TmuxSession, ProjectKey: o.ProjectKey, WorkDir: o.WorkDir, HostID: o.HostID,
		}}}
	}
	remoteRow := workspaceinventory.Workspace{
		HostID: remoteHostID, TmuxName: "api-claude", Path: "/home/me/api",
		ProjectKey: hosts.ScopedKey(remoteHostID, "/home/me/api"),
	}
	if !uirequest.OriginForeground(origin, visible(attentionOriginFor(remoteRow))) {
		t.Error("a viewer showing that remote workspace was not foreground")
	}

	localLookalike := workspaceinventory.Workspace{TmuxName: "api-claude", Path: "/home/me/api", ProjectKey: "api"}
	if uirequest.OriginForeground(origin, visible(attentionOriginFor(localLookalike))) {
		t.Error("a local workspace with the same session name suppressed a remote alert")
	}

	otherRemote := remoteRow
	otherRemote.TmuxName = "api-codex"
	if uirequest.OriginForeground(origin, visible(attentionOriginFor(otherRemote))) {
		t.Error("a different remote workspace on the same host suppressed the alert")
	}

	blurred := visible(attentionOriginFor(remoteRow))
	blurred[0].Focused = false
	if uirequest.OriginForeground(origin, blurred) {
		t.Error("a blurred instance counted as foreground")
	}
}

// Merely having the stream connected is not attention. The remote row has to
// be the one on screen.
func TestConnectedStreamIsNotForeground(t *testing.T) {
	m := managedHostModel(t, true)
	event := remoteNotifyEvent(time.Now().UTC())
	posts, _ := collectPosts(t, m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}))
	origin := uirequest.Origin{
		TmuxSession: posts[0].Origin.TmuxSession, ProjectKey: posts[0].Origin.ProjectKey,
		WorkDir: posts[0].Origin.WorkDir, HostID: posts[0].Origin.HostID,
	}
	// A focused instance showing nothing in particular — Git, Files, the
	// browser with no preview open.
	records := []uirequest.Attention{{PID: 1, Focused: true}}
	if uirequest.OriginForeground(origin, records) {
		t.Error("a connected viewer showing nothing counted as foreground")
	}
}

// A forwarded record belongs to no local project, even when the remote host
// has a checkout at the same path. Adopting one would seed a local lane
// tracker with a key no local observation can produce, and the next sweep
// would withdraw the remote agent's wait while it was still waiting.
func TestForwardedRecordIsNotOwnedByALocalProject(t *testing.T) {
	m := managedHostModel(t, true)
	event := remoteNotifyEvent(time.Now().UTC())
	posts, _ := collectPosts(t, m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}))
	if notify.TransitionOwnedByProject(posts[0], "/home/me/api") {
		t.Error("a local project claimed ownership of a remote transition")
	}
}

// A forwarded record is an ordinary input to the delivery policy. Nothing in
// it is special-cased, so the user's source rules, channel modes, and quiet
// hours govern a remote agent exactly as they govern a local one — and the
// remote host runs no provider either way.
func TestForwardedRecordResolvesThroughTheOrdinaryPolicy(t *testing.T) {
	m := managedHostModel(t, true)
	event := remoteNotifyEvent(time.Now().UTC())
	posts, _ := collectPosts(t, m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}))
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}
	n := posts[0]
	runtime := notify.RuntimeContext{
		Now:          time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		Capabilities: notify.CapabilitySet{Native: true, Sound: true},
	}

	background := config.DefaultNotificationsConfig()
	background.Native.Mode, background.Sound.Mode = config.DeliveryBackground, config.DeliveryBackground
	decision := notify.ResolveDelivery(n, notify.ResolveConfig(background), runtime)
	if !decision.Native.Deliver || !decision.Sound.Deliver {
		t.Fatalf("background decision = %+v", decision)
	}
	if decision.Cue != notify.CueAttention {
		t.Errorf("cue = %q, want the waiting cue", decision.Cue)
	}

	// Foreground: the user is looking at that remote workspace.
	visible := runtime
	visible.Foreground = true
	if decision := notify.ResolveDelivery(n, notify.ResolveConfig(background), visible); decision.Native.Deliver || decision.Sound.Deliver {
		t.Errorf("a visible remote workspace still delivered: %+v", decision)
	}
	// Always delivers either way.
	always := background
	always.Native.Mode, always.Sound.Mode = config.DeliveryAlways, config.DeliveryAlways
	if decision := notify.ResolveDelivery(n, notify.ResolveConfig(always), visible); !decision.Native.Deliver || !decision.Sound.Deliver {
		t.Errorf("always suppressed a visible remote workspace: %+v", decision)
	}

	// A source the user switched off.
	muted := background
	muted.Sources = map[string]config.NotificationSourceConfig{
		string(notify.SourceWaiting): {Native: ptrTo(false), Sound: config.SoundNone},
	}
	if decision := notify.ResolveDelivery(n, notify.ResolveConfig(muted), runtime); decision.Native.Deliver || decision.Sound.Deliver {
		t.Errorf("a muted source still delivered: %+v", decision)
	}

	// Quiet hours suppress the external channels and nothing else.
	quiet := background
	quiet.QuietHours = config.QuietHoursConfig{Enabled: true, Start: "00:00", End: "23:59"}
	if decision := notify.ResolveDelivery(n, notify.ResolveConfig(quiet), runtime); decision.Native.Deliver || decision.Sound.Deliver {
		t.Errorf("quiet hours did not suppress a remote event: %+v", decision)
	}
}

func ptrTo[T any](v T) *T { return &v }

// isolatedStateDir gives a test its own state tree and the marker the state
// helpers refuse to run without.
func isolatedStateDir(t *testing.T) string {
	t.Helper()
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// countingNative and countingSound record whether a provider was ever asked to
// do anything. They deliberately succeed: the assertion is about who was
// invoked, not about whether an invocation would have worked.
type countingNative struct{ delivered int }

func (n *countingNative) Probe(context.Context) notifydelivery.Capability {
	return notifydelivery.Capability{Available: true, Provider: "counting-native"}
}

func (n *countingNative) Deliver(context.Context, notifydelivery.Message) (notifydelivery.ProviderReceipt, error) {
	n.delivered++
	return notifydelivery.ProviderReceipt{Provider: "counting-native", Delivered: true}, nil
}

func (n *countingNative) Remove(context.Context, string) error { return nil }

type countingSound struct{ played int }

func (s *countingSound) Probe(context.Context) notifydelivery.Capability {
	return notifydelivery.Capability{Available: true, Provider: "counting-sound"}
}

func (s *countingSound) Play(context.Context, notifydelivery.Cue) (notifydelivery.ProviderReceipt, error) {
	s.played++
	return notifydelivery.ProviderReceipt{Provider: "counting-sound", Delivered: true}, nil
}

// Managed delivery happens on the local viewer, and only there. A forwarded
// payload must not become a way to make a machine inside SSH — which is what
// the remote host itself is running — invoke a desktop or audio service. M3's
// refusal is a property of the process, not of the notification, and nothing
// on the wire can reach it.
func TestForwardedRecordCannotBypassTheSSHRefusal(t *testing.T) {
	stateDir := isolatedStateDir(t)
	m := managedHostModel(t, true)
	event := remoteNotifyEvent(time.Now().UTC())
	posts, _ := collectPosts(t, m.forwardHostNotifications(hosts.Update{HostID: remoteHostID, Notify: []hostproto.NotifyEvent{event}}))
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}

	cfg := config.DefaultNotificationsConfig()
	cfg.Native.Mode, cfg.Sound.Mode = config.DeliveryAlways, config.DeliveryAlways
	policy := notify.ResolveConfig(cfg)
	native, sound := &countingNative{}, &countingSound{}
	service := notifydelivery.NewService(notifydelivery.ServiceOptions{
		Native: native, Sound: sound,
		Ledger: func() (notifydelivery.Ledger, error) { return notifydelivery.Open(stateDir) },
		Config: func() notify.ResolvedConfig { return policy },
		Owner:  "inside-ssh",
		// A process inside an SSH session, which is exactly what a remote host
		// runs and what a forwarded payload might hope to reach.
		Getenv: func(name string) string {
			if name == "SSH_CONNECTION" {
				return "10.0.0.2 51000 10.0.0.1 22"
			}
			return ""
		},
	})
	result := service.Deliver(context.Background(), notifydelivery.Request{Notification: posts[0]})
	if native.delivered != 0 || sound.played != 0 {
		t.Fatalf("a forwarded payload invoked providers inside SSH: native=%d sound=%d", native.delivered, sound.played)
	}
	if result.Native.Delivered || result.Sound.Delivered {
		t.Errorf("result claims delivery: %+v", result)
	}
	if !strings.Contains(result.Native.Error+result.Sound.Error+string(result.Native.Reason)+string(result.Sound.Reason), "unavailable") {
		t.Errorf("the refusal is not reported as unavailable: %+v", result)
	}
}
