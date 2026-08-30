package notifydelivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }
func (fixedClock) AfterFunc(time.Duration, func()) Timer {
	panic("unexpected timer")
}

type fakeAttention struct {
	foreground bool
	err        error
}

func (a fakeAttention) Foreground(notify.Origin) (bool, error) { return a.foreground, a.err }

type fakeNative struct {
	capability Capability
	mu         sync.Mutex
	delivered  []Message
	removed    []string
	err        error
}

func (n *fakeNative) Probe(context.Context) Capability { return n.capability }
func (n *fakeNative) Deliver(_ context.Context, message Message) (ProviderReceipt, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.delivered = append(n.delivered, message)
	return ProviderReceipt{Provider: n.capability.Provider, Delivered: n.err == nil}, n.err
}
func (n *fakeNative) Remove(_ context.Context, group string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.removed = append(n.removed, group)
	return n.err
}

type fakeSound struct {
	capability Capability
	mu         sync.Mutex
	played     []Cue
	err        error
}

type trackingSoundCoordinator struct {
	capability Capability
	mu         sync.Mutex
	cancelled  []string
	cancelErr  error
}

type releaseFailLedger struct {
	Ledger
	err error
}

func (l releaseFailLedger) ReleaseExpired(time.Time) (int, error) { return 0, l.err }

type completeFailLedger struct {
	Ledger
	err error
}

func (l completeFailLedger) Complete(string, string, Receipt) error { return l.err }

func (s *trackingSoundCoordinator) Probe(context.Context) Capability { return s.capability }
func (s *trackingSoundCoordinator) PlayNotification(context.Context, string, Cue) (ProviderReceipt, error) {
	return ProviderReceipt{Provider: s.capability.Provider, Delivered: true}, nil
}
func (s *trackingSoundCoordinator) Cancel(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = append(s.cancelled, id)
	return s.cancelErr
}

func (p *fakeSound) Probe(context.Context) Capability { return p.capability }
func (p *fakeSound) Play(_ context.Context, cue Cue) (ProviderReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.played = append(p.played, cue)
	return ProviderReceipt{Provider: p.capability.Provider, Delivered: p.err == nil}, p.err
}

func enabledPolicy() notify.ResolvedConfig {
	cfg := config.DefaultNotificationsConfig()
	cfg.Native.Mode = config.DeliveryAlways
	cfg.Sound.Mode = config.DeliveryAlways
	notify.ApplyConfig(cfg)
	resolved := notify.CurrentConfig()
	notify.ApplyConfig(config.DefaultNotificationsConfig())
	return resolved
}

func TestServiceClaimsChannelsIndependentlyAndOnlyOnce(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	native := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	sound := &fakeSound{capability: Capability{Available: true, Provider: "sound-fake"}}
	ledger := NewMemoryLedger()
	service := NewService(ServiceOptions{
		Native: native, Sound: sound, Ledger: func() (Ledger, error) { return ledger, nil },
		Attention: fakeAttention{}, Config: enabledPolicy, Clock: fixedClock{now: now}, Owner: "one",
	})
	n := notify.Notification{ID: "ntf-once", Source: notify.SourceWaiting, Severity: notify.SeverityWarning, Title: "needs input", CreatedAt: now}
	first := service.Deliver(context.Background(), Request{Notification: n})
	second := service.Deliver(context.Background(), Request{Notification: n})
	if !first.Native.Delivered || !first.Sound.Delivered {
		t.Fatalf("first delivery = %+v", first)
	}
	if second.Native.Reason != notify.ReasonAlreadyClaimed || second.Sound.Reason != notify.ReasonAlreadyClaimed {
		t.Fatalf("second delivery = %+v, want independent already-claimed outcomes", second)
	}
	if len(native.delivered) != 1 || len(sound.played) != 1 || sound.played[0] != CueAttention {
		t.Fatalf("provider calls native=%d sound=%v", len(native.delivered), sound.played)
	}
	entries, err := ledger.List(n.ID)
	if err != nil || len(entries) != 2 || entries[0].Channel == entries[1].Channel {
		t.Fatalf("ledger entries = %+v, %v", entries, err)
	}
}

func TestServiceRefusesAllRemoteSSHDeliveryWithoutProbingOrClaiming(t *testing.T) {
	now := time.Now().UTC()
	native := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	sound := &fakeSound{capability: Capability{Available: true, Provider: "sound-fake"}}
	ledgerCalls := 0
	service := NewService(ServiceOptions{
		Native: native, Sound: sound,
		Ledger: func() (Ledger, error) { ledgerCalls++; return NewMemoryLedger(), nil },
		Config: enabledPolicy, Clock: fixedClock{now: now},
		Getenv: func(name string) string {
			if name == "SSH_CONNECTION" {
				return "client 123 host 22"
			}
			return ""
		},
	})
	status := service.Status(context.Background())
	if !status.Remote || status.Native.Available || status.Sound.Available || status.Native.Reason != RemoteUnavailableReason || status.Sound.Reason != RemoteUnavailableReason {
		t.Fatalf("remote status = %+v", status)
	}
	n := notify.Notification{ID: "ntf-remote", Source: notify.SourceWaiting, Severity: notify.SeverityWarning, CreatedAt: now}
	result := service.Deliver(context.Background(), Request{Notification: n, ExplicitTest: true})
	if result.Native.Reason != notify.ReasonUnavailable || result.Sound.Reason != notify.ReasonUnavailable || result.Native.Attempted || result.Sound.Attempted {
		t.Fatalf("remote explicit test = %+v", result)
	}
	if ledgerCalls != 0 || len(native.delivered) != 0 || len(sound.played) != 0 {
		t.Fatalf("remote delivery touched ledger/providers: ledger=%d native=%d sound=%d", ledgerCalls, len(native.delivered), len(sound.played))
	}
}

func TestServiceDoesNotProbeOrClaimSuppressedEvents(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	native := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	sound := &fakeSound{capability: Capability{Available: true, Provider: "sound-fake"}}
	ledgerCalls := 0
	service := NewService(ServiceOptions{
		Native: native, Sound: sound,
		Ledger:    func() (Ledger, error) { ledgerCalls++; return NewMemoryLedger(), nil },
		Attention: fakeAttention{foreground: true}, Config: enabledPolicy, Clock: fixedClock{now: now},
	})
	backgroundPolicy := config.DefaultNotificationsConfig()
	backgroundPolicy.Native.Mode = config.DeliveryBackground
	backgroundPolicy.Sound.Mode = config.DeliveryBackground
	notify.ApplyConfig(backgroundPolicy)
	resolved := notify.CurrentConfig()
	notify.ApplyConfig(config.DefaultNotificationsConfig())
	service.config = func() notify.ResolvedConfig { return resolved }
	n := notify.Notification{ID: "ntf-foreground", Source: notify.SourceWaiting, CreatedAt: now}
	result := service.Deliver(context.Background(), Request{Notification: n})
	if result.Native.Reason != notify.ReasonForeground || result.Sound.Reason != notify.ReasonForeground || ledgerCalls != 0 {
		t.Fatalf("result=%+v ledgerCalls=%d", result, ledgerCalls)
	}
	if len(native.delivered) != 0 || len(sound.played) != 0 {
		t.Fatalf("suppressed event invoked providers")
	}
}

func TestServiceSkipsStaleDiscoveredBacklog(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	native := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	sound := &fakeSound{capability: Capability{Available: true, Provider: "sound-fake"}}
	service := NewService(ServiceOptions{
		Native: native, Sound: sound, Ledger: func() (Ledger, error) { return NewMemoryLedger(), nil },
		Config: enabledPolicy, Clock: fixedClock{now: now},
	})
	n := notify.Notification{ID: "ntf-old", Source: notify.SourceWaiting, CreatedAt: now.Add(-notify.LiveEventGrace - time.Second)}
	result := service.Deliver(context.Background(), Request{Notification: n, Discovered: true})
	if result.Native.Reason != notify.ReasonStale || result.Sound.Reason != notify.ReasonStale {
		t.Fatalf("stale result = %+v", result)
	}
	if len(native.delivered) != 0 || len(sound.played) != 0 {
		t.Fatal("backlog invoked providers")
	}
}

func TestServiceCompletesProviderFailureWithoutReplaying(t *testing.T) {
	now := time.Now().UTC()
	native := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}, err: errors.New("no permission")}
	ledger := NewMemoryLedger()
	service := NewService(ServiceOptions{
		Native: native, Sound: &fakeSound{}, Ledger: func() (Ledger, error) { return ledger, nil },
		Config: enabledPolicy, Clock: fixedClock{now: now}, Owner: "owner",
	})
	n := notify.Notification{ID: "ntf-fail", Source: notify.SourceAgent, CreatedAt: now}
	first := service.Deliver(context.Background(), Request{Notification: n})
	second := service.Deliver(context.Background(), Request{Notification: n})
	if first.Native.Error == "" || second.Native.Reason != notify.ReasonAlreadyClaimed || len(native.delivered) != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d", first, second, len(native.delivered))
	}
}

func TestServiceRemovesOnlyAPreviouslyDeliveredStickyNativeGroup(t *testing.T) {
	now := time.Now().UTC()
	native := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	ledger := NewMemoryLedger()
	service := NewService(ServiceOptions{
		Native: native, Sound: &fakeSound{}, Ledger: func() (Ledger, error) { return ledger, nil },
		Config: enabledPolicy, Clock: fixedClock{now: now}, Owner: "owner",
	})
	undelivered := notify.Notification{
		ID: "ntf-undelivered", Source: notify.SourceWaiting, Sticky: true, CreatedAt: now,
		Origin: notify.Origin{TmuxSession: "sidecar-sh-one"},
	}
	if err := service.Remove(context.Background(), undelivered); err != nil || len(native.removed) != 0 {
		t.Fatalf("undelivered remove err=%v calls=%v", err, native.removed)
	}
	n := undelivered
	n.ID = "ntf-sticky"
	result := service.Deliver(context.Background(), Request{Notification: n})
	if !result.Native.Delivered {
		t.Fatalf("delivery = %+v", result)
	}
	if err := service.Remove(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(native.removed) != 1 || native.removed[0] != GroupFor(n) {
		t.Fatalf("removed = %v", native.removed)
	}
}

func TestServiceRemoveCancelsSoundBeforeBlockedNativeLedger(t *testing.T) {
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, LedgerFileName)
	blockerLedger := mustOpenLedgerPath(t, path)
	removeLedger := mustOpenLedgerPath(t, path)
	player := &fakeSound{capability: Capability{Available: true, Provider: "fake"}}
	hostSound := NewHostSound(stateDir, player, 75*time.Millisecond, time.Second, RealClock{})
	n := notify.Notification{ID: "dismiss-before-cue", Source: notify.SourceWaiting, Sticky: true, CreatedAt: time.Now().UTC()}
	soundDone := make(chan ProviderReceipt, 1)
	go func() {
		receipt, _ := hostSound.PlayNotification(context.Background(), n.ID, CueAttention)
		soundDone <- receipt
	}()
	item := waitSoundItems(t, hostSound, 1)[n.ID]

	nativeStarted := make(chan struct{})
	releaseNative := make(chan struct{})
	nativeDone := make(chan NativeOperationResult, 1)
	go func() {
		nativeDone <- blockerLedger.DeliverNative("native-blocker", "blocker-group", "blocker", time.Now().UTC(), time.Second, func() (ProviderReceipt, error) {
			close(nativeStarted)
			<-releaseNative
			return ProviderReceipt{Provider: "fake", Delivered: true, At: time.Now().UTC()}, nil
		})
	}()
	<-nativeStarted

	service := NewService(ServiceOptions{
		SoundCoordinator: hostSound,
		Ledger:           func() (Ledger, error) { return removeLedger, nil },
		Clock:            RealClock{}, Owner: "remover",
	})
	removeDone := make(chan error, 1)
	go func() { removeDone <- service.Remove(context.Background(), n) }()
	waitSoundCancellation(t, hostSound, n.ID)

	wait := time.Until(item.BatchDeadline) + 20*time.Millisecond
	if wait > 0 {
		timer := time.NewTimer(wait)
		<-timer.C
	}
	receipt := <-soundDone
	if receipt.Reason != ClaimCancelled || len(player.played) != 0 {
		t.Fatalf("sound receipt=%+v played=%v", receipt, player.played)
	}
	select {
	case err := <-removeDone:
		t.Fatalf("native-ledger removal unexpectedly unblocked: %v", err)
	default:
	}
	close(releaseNative)
	if result := <-nativeDone; result.Err != nil {
		t.Fatalf("blocking native delivery = %+v", result)
	}
	if err := <-removeDone; err != nil {
		t.Fatal(err)
	}
}

func TestServiceRemoveCancelsSoundDespiteLedgerAndNativeErrors(t *testing.T) {
	n := notify.Notification{
		ID: "dismiss-errors", Source: notify.SourceWaiting, Sticky: true,
		Origin: notify.Origin{TmuxSession: "sidecar-sh-errors"}, CreatedAt: time.Now().UTC(),
	}
	soundErr := errors.New("sound state unavailable")
	ledgerErr := errors.New("delivery ledger unavailable")
	tracker := &trackingSoundCoordinator{cancelErr: soundErr}
	service := NewService(ServiceOptions{
		SoundCoordinator: tracker,
		Ledger:           func() (Ledger, error) { return nil, ledgerErr },
	})
	err := service.Remove(context.Background(), n)
	if !errors.Is(err, soundErr) || !errors.Is(err, ledgerErr) || len(tracker.cancelled) != 1 || tracker.cancelled[0] != n.ID {
		t.Fatalf("remove err=%v cancelled=%v", err, tracker.cancelled)
	}

	tracker = &trackingSoundCoordinator{}
	nativeErr := errors.New("native remove failed")
	native := &fakeNative{capability: Capability{Available: true, Provider: "fake"}, err: nativeErr}
	ledger := NewMemoryLedger()
	group := GroupFor(n)
	if result := ledger.DeliverNative(n.ID, group, "deliverer", n.CreatedAt, time.Minute, func() (ProviderReceipt, error) {
		return ProviderReceipt{Provider: "fake", Delivered: true, At: n.CreatedAt}, nil
	}); result.Err != nil {
		t.Fatal(result.Err)
	}
	service = NewService(ServiceOptions{
		Native: native, SoundCoordinator: tracker,
		Ledger: func() (Ledger, error) { return ledger, nil }, Clock: RealClock{}, Owner: "remover",
	})
	err = service.Remove(context.Background(), n)
	if !errors.Is(err, nativeErr) || len(tracker.cancelled) != 1 || tracker.cancelled[0] != n.ID {
		t.Fatalf("native error remove err=%v cancelled=%v", err, tracker.cancelled)
	}
}

func TestServiceDeliveryMaintainsAlreadyOpenLedger(t *testing.T) {
	ledger := mustOpenLedger(t)
	now := time.Now().UTC()
	old := now.Add(-ReceiptRetention - time.Hour)
	if won, _, err := ledger.Claim("expired-history", ChannelSound, "old-owner", old, time.Minute); err != nil || !won {
		t.Fatalf("old claim won=%v err=%v", won, err)
	}
	if err := ledger.Complete("expired-history", ChannelSound, Receipt{Owner: "old-owner", Succeeded: true, CompletedAt: old}); err != nil {
		t.Fatal(err)
	}
	native := &fakeNative{capability: Capability{Available: true, Provider: "fake"}}
	service := NewService(ServiceOptions{
		Native: native, Ledger: func() (Ledger, error) { return ledger, nil },
		Config: enabledPolicy, Clock: fixedClock{now: now}, Owner: "live-process",
	})
	service.Deliver(context.Background(), Request{Notification: notify.Notification{ID: "maintenance-trigger", Source: notify.SourceAgent, CreatedAt: now}})
	entries, err := ledger.List("expired-history")
	if err != nil || len(entries) != 0 {
		t.Fatalf("expired history survived live delivery maintenance: %+v, %v", entries, err)
	}
}

func TestServiceReportsLedgerOpenAndMaintenanceFailuresPerRequestedChannel(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	native := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	sound := &fakeSound{capability: Capability{Available: true, Provider: "sound-fake"}}
	n := notify.Notification{ID: "ntf-coordination", Source: notify.SourceWaiting, CreatedAt: now}

	openErr := errors.New("ledger fixture cannot open")
	service := NewService(ServiceOptions{
		Native: native, Sound: sound, Ledger: func() (Ledger, error) { return nil, openErr },
		Config: enabledPolicy, Clock: fixedClock{now: now}, Owner: "owner",
	})
	result := service.Deliver(context.Background(), Request{Notification: n})
	for name, channel := range map[string]ChannelResult{"native": result.Native, "sound": result.Sound} {
		if channel.Reason != notify.ReasonCoordination || !strings.Contains(channel.Error, "open delivery ledger") {
			t.Fatalf("%s open failure = %+v", name, channel)
		}
	}

	maintenanceErr := errors.New("ledger fixture cannot maintain")
	service = NewService(ServiceOptions{
		Native: native, Sound: sound,
		Ledger: func() (Ledger, error) {
			return releaseFailLedger{Ledger: NewMemoryLedger(), err: maintenanceErr}, nil
		},
		Config: enabledPolicy, Clock: fixedClock{now: now}, Owner: "owner",
	})
	result = service.Deliver(context.Background(), Request{Notification: n, Channel: ChannelSound})
	if result.Native.Reason != notify.ReasonNotRequested || result.Native.Error != "" {
		t.Fatalf("unrequested native failure = %+v", result.Native)
	}
	if result.Sound.Reason != notify.ReasonCoordination || !strings.Contains(result.Sound.Error, "maintain delivery ledger") {
		t.Fatalf("requested sound maintenance failure = %+v", result.Sound)
	}
	if len(native.delivered) != 0 || len(sound.played) != 0 {
		t.Fatal("coordination failure invoked a provider")
	}
}

func TestServiceKeepsChannelIndependenceWhenSoundReceiptMaintenanceFails(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	native := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	sound := &fakeSound{capability: Capability{Available: true, Provider: "sound-fake"}}
	completeErr := errors.New("receipt fixture unavailable")
	ledger := completeFailLedger{Ledger: NewMemoryLedger(), err: completeErr}
	service := NewService(ServiceOptions{
		Native: native, Sound: sound, Ledger: func() (Ledger, error) { return ledger, nil },
		Config: enabledPolicy, Clock: fixedClock{now: now}, Owner: "owner",
	})
	result := service.Deliver(context.Background(), Request{Notification: notify.Notification{
		ID: "ntf-receipt-maintenance", Source: notify.SourceWaiting, CreatedAt: now,
	}})
	if !result.Native.Delivered || result.Native.Error != "" {
		t.Fatalf("independent native result = %+v", result.Native)
	}
	if !result.Sound.Delivered || result.Sound.Reason != notify.ReasonCoordination || !strings.Contains(result.Sound.Error, "complete delivery receipt") {
		t.Fatalf("sound receipt failure = %+v", result.Sound)
	}
}

func waitSoundCancellation(t *testing.T, host *HostSound, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := host.load()
		if err == nil {
			if _, ok := state.cancellations[id]; ok {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("sound cancellation %q was not persisted (last err %v)", id, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNativeMessageSanitizesBoundsAndUsesStableOwnedValues(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "tmux")
	t.Setenv("GHOSTTY_RESOURCES_DIR", "/Applications/Ghostty.app/resources")
	n := notify.Notification{
		ID: "ntf-safe", Title: "\x1b]9;secret\x07 [hello]\nworld", Body: string(make([]byte, 4)) + string([]rune("界界界")),
		Origin: notify.Origin{WorkDir: "/private/project"},
	}
	message := NativeMessage(n)
	if message.Title != "[hello] world" || message.ActivationBundleID != "com.mitchellh.ghostty" {
		t.Fatalf("message = %+v", message)
	}
	if message.Group == "" || message.Group == "/private/project" {
		t.Fatalf("group must be stable and path-free: %q", message.Group)
	}
	long := NativeMessage(notify.Notification{Title: strings.Repeat("界", 121)})
	if utf8.RuneCountInString(long.Title) != 120 || !strings.HasSuffix(long.Title, "…") {
		t.Fatalf("bounded title has %d runes: %q", utf8.RuneCountInString(long.Title), long.Title)
	}
}

func TestEmbeddedAssetsMaterializeLazilyAndAtomically(t *testing.T) {
	root := t.TempDir()
	cache := NewEmbeddedAssetCache(root)
	wantDir := filepath.Join(root, "sidecar", "notification-sounds", assetVersion)
	if _, err := os.Stat(wantDir); !os.IsNotExist(err) {
		t.Fatalf("cache existed before use: %v", err)
	}
	var wg sync.WaitGroup
	paths := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := cache.Materialize(CueAttention)
			if err != nil {
				t.Errorf("materialize: %v", err)
				return
			}
			paths <- path
		}()
	}
	wg.Wait()
	close(paths)
	want := filepath.Join(wantDir, "attention.wav")
	for path := range paths {
		if path != want {
			t.Fatalf("path = %q, want %q", path, want)
		}
	}
	data, err := os.ReadFile(want)
	if err != nil || len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("materialized asset is not a WAV: len=%d err=%v", len(data), err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(wantDir, "*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files leaked: %v", leftovers)
	}
}

func TestCacheRootPrefersXDG(t *testing.T) {
	root, err := defaultCacheRoot(func(name string) string {
		if name == "XDG_CACHE_HOME" {
			return "/private/xdg-cache"
		}
		return ""
	}, func() (string, error) { return "/user/cache", nil })
	if err != nil || root != "/private/xdg-cache" {
		t.Fatalf("root=%q err=%v", root, err)
	}
}

type manualTimer struct{}

func (manualTimer) Stop() bool { return true }

type manualClock struct {
	mu  sync.Mutex
	now time.Time
	fn  func()
}

func (c *manualClock) Now() time.Time { return c.now }
func (c *manualClock) AfterFunc(_ time.Duration, fn func()) Timer {
	c.mu.Lock()
	c.fn = fn
	c.mu.Unlock()
	return manualTimer{}
}
func (c *manualClock) fire() {
	c.mu.Lock()
	fn := c.fn
	c.fn = nil
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func TestAudioArbiterSelectsHighestCueFromBurst(t *testing.T) {
	clock := &manualClock{now: time.Now().UTC()}
	player := &fakeSound{capability: Capability{Available: true, Provider: "fake-player"}}
	arbiter := NewAudioArbiter(player, time.Second, clock)
	cues := []Cue{CueDone, CueFailure, CueAttention}
	results := make(chan ProviderReceipt, len(cues))
	for _, cue := range cues {
		cue := cue
		go func() {
			receipt, _ := arbiter.Play(context.Background(), cue)
			results <- receipt
		}()
	}
	deadline := time.Now().Add(time.Second)
	for {
		arbiter.mu.Lock()
		pending := len(arbiter.pending)
		arbiter.mu.Unlock()
		if pending == len(cues) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d requests entered arbiter", pending)
		}
		runtime.Gosched()
	}
	clock.fire()
	delivered, limited := 0, 0
	for range cues {
		receipt := <-results
		if receipt.Delivered {
			delivered++
		} else if receipt.Reason == string(notify.ReasonRateLimited) {
			limited++
		}
	}
	if delivered != 1 || limited != 2 || len(player.played) != 1 || player.played[0] != CueFailure {
		t.Fatalf("delivered=%d limited=%d played=%v", delivered, limited, player.played)
	}
}

type blockingSound struct {
	capability Capability
	started    chan Cue
	release    chan struct{}
	mu         sync.Mutex
	active     int
	maxActive  int
	played     []Cue
}

func (p *blockingSound) Probe(context.Context) Capability { return p.capability }
func (p *blockingSound) Play(_ context.Context, cue Cue) (ProviderReceipt, error) {
	p.mu.Lock()
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	p.played = append(p.played, cue)
	p.mu.Unlock()
	p.started <- cue
	<-p.release
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return ProviderReceipt{Provider: p.capability.Provider, Delivered: true}, nil
}

func TestAudioArbiterNeverOverlapsAndKeepsOnlyOneNextBatch(t *testing.T) {
	clock := &manualClock{now: time.Now().UTC()}
	player := &blockingSound{
		capability: Capability{Available: true, Provider: "blocking-player"},
		started:    make(chan Cue, 2), release: make(chan struct{}, 2),
	}
	arbiter := NewAudioArbiter(player, time.Second, clock)
	results := make(chan ProviderReceipt, 4)
	go func() {
		receipt, _ := arbiter.Play(context.Background(), CueDone)
		results <- receipt
	}()
	waitPending(t, arbiter, 1)
	go clock.fire()
	if cue := <-player.started; cue != CueDone {
		t.Fatalf("first cue = %q", cue)
	}
	for _, cue := range []Cue{CueDone, CueAttention, CueFailure} {
		cue := cue
		go func() {
			receipt, _ := arbiter.Play(context.Background(), cue)
			results <- receipt
		}()
	}
	waitPending(t, arbiter, 3)
	player.release <- struct{}{}
	waitTimer(t, clock)
	go clock.fire()
	if cue := <-player.started; cue != CueFailure {
		t.Fatalf("collapsed next cue = %q, want failure", cue)
	}
	player.release <- struct{}{}
	for i := 0; i < 4; i++ {
		<-results
	}
	player.mu.Lock()
	defer player.mu.Unlock()
	if player.maxActive != 1 || len(player.played) != 2 {
		t.Fatalf("maxActive=%d played=%v", player.maxActive, player.played)
	}
}

func waitPending(t *testing.T, arbiter *AudioArbiter, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		arbiter.mu.Lock()
		got := len(arbiter.pending)
		arbiter.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending=%d, want %d", got, want)
		}
		runtime.Gosched()
	}
}

func waitTimer(t *testing.T, clock *manualClock) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		clock.mu.Lock()
		ready := clock.fn != nil
		clock.mu.Unlock()
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("arbiter did not schedule its next batch")
		}
		runtime.Gosched()
	}
}
