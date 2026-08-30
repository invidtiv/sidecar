package notifydelivery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
)

func soundOnlyPolicy() notify.ResolvedConfig {
	cfg := config.DefaultNotificationsConfig()
	cfg.Sound.Mode = config.DeliveryAlways
	notify.ApplyConfig(cfg)
	resolved := notify.CurrentConfig()
	notify.ApplyConfig(config.DefaultNotificationsConfig())
	return resolved
}

func TestHostSoundBatchesAcrossIndependentServicesAndNeverOverlaps(t *testing.T) {
	stateDir := t.TempDir()
	deliveryPath := filepath.Join(stateDir, LedgerFileName)
	ledgerOne := mustOpenLedgerPath(t, deliveryPath)
	ledgerTwo := mustOpenLedgerPath(t, deliveryPath)
	player := &blockingSound{
		capability: Capability{Available: true, Provider: "shared-player"},
		started:    make(chan Cue, 3), release: make(chan struct{}, 3),
	}
	window := 75 * time.Millisecond
	hostOne := NewHostSound(stateDir, player, window, time.Second, RealClock{})
	hostTwo := NewHostSound(stateDir, player, window, time.Second, RealClock{})
	policy := soundOnlyPolicy()
	now := time.Now().UTC()
	serviceOne := NewService(ServiceOptions{
		SoundCoordinator: hostOne, Ledger: func() (Ledger, error) { return ledgerOne, nil },
		Config: func() notify.ResolvedConfig { return policy }, Clock: fixedClock{now: now}, Owner: "service-one",
	})
	serviceTwo := NewService(ServiceOptions{
		SoundCoordinator: hostTwo, Ledger: func() (Ledger, error) { return ledgerTwo, nil },
		Config: func() notify.ResolvedConfig { return policy }, Clock: fixedClock{now: now}, Owner: "service-two",
	})
	requests := []struct {
		service *Service
		n       notify.Notification
	}{
		{serviceOne, notify.Notification{ID: "done", Source: notify.SourceSession, Severity: notify.SeverityInfo, CreatedAt: now}},
		{serviceTwo, notify.Notification{ID: "failure", Source: notify.SourceSession, Severity: notify.SeverityError, CreatedAt: now}},
	}
	results := make(chan Result, 3)
	for _, request := range requests {
		request := request
		go func() { results <- request.service.Deliver(context.Background(), Request{Notification: request.n}) }()
	}
	waitSoundItems(t, hostOne, 2)
	if cue := <-player.started; cue != CueFailure {
		t.Fatalf("first batch played %q, want failure", cue)
	}

	// A later batch can become due while the first player is still blocked. It
	// must wait for the shared playback lease rather than overlapping it.
	later := notify.Notification{ID: "later-attention", Source: notify.SourceWaiting, Severity: notify.SeverityWarning, CreatedAt: now.Add(time.Second)}
	go func() { results <- serviceOne.Deliver(context.Background(), Request{Notification: later}) }()
	laterItem := waitSoundItems(t, hostTwo, 3)[later.ID]
	delay := time.Until(laterItem.BatchDeadline) + 20*time.Millisecond
	if delay < 20*time.Millisecond {
		delay = 20 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	<-timer.C
	select {
	case cue := <-player.started:
		t.Fatalf("later cue %q overlapped the first playback", cue)
	default:
	}
	player.release <- struct{}{}
	if cue := <-player.started; cue != CueAttention {
		t.Fatalf("later batch played %q, want attention", cue)
	}
	player.release <- struct{}{}

	delivered, limited := 0, 0
	for i := 0; i < 3; i++ {
		result := <-results
		if result.Sound.Delivered {
			delivered++
		} else if result.Sound.Reason == notify.ReasonRateLimited {
			limited++
		}
	}
	player.mu.Lock()
	defer player.mu.Unlock()
	if delivered != 2 || limited != 1 || player.maxActive != 1 || len(player.played) != 2 || player.played[0] != CueFailure || player.played[1] != CueAttention {
		t.Fatalf("delivered=%d limited=%d maxActive=%d played=%v", delivered, limited, player.maxActive, player.played)
	}
}

func TestHostSoundExpiredLeaderAndPlaybackLeaseRecover(t *testing.T) {
	stateDir := t.TempDir()
	player := &fakeSound{capability: Capability{Available: true, Provider: "recovered-player"}}
	host := NewHostSound(stateDir, player, time.Millisecond, 50*time.Millisecond, RealClock{})
	now := time.Now().UTC()
	item := soundItem{NotificationID: "recover", BatchID: "old-batch", Cue: CueFailure, EnqueuedAt: now.Add(-time.Second), BatchDeadline: now.Add(-time.Second)}
	batch := soundBatch{ID: item.BatchID, Deadline: item.BatchDeadline, Leader: "dead-owner", LeaderUntil: now.Add(-time.Second), WinnerID: item.NotificationID}
	playback := soundPlayback{BatchID: batch.ID, Owner: "dead-owner", LeaseUntil: now.Add(-time.Second)}
	if err := host.withLock(func(state *soundState) error {
		if err := host.append(soundEvent{Event: soundEnqueued, At: item.EnqueuedAt, Item: &item, Batch: &batch}); err != nil {
			return err
		}
		if err := host.append(soundEvent{Event: soundLeader, At: item.EnqueuedAt, Batch: &batch}); err != nil {
			return err
		}
		if err := host.append(soundEvent{Event: soundPlaying, At: item.EnqueuedAt, Playback: &playback}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := host.PlayNotification(context.Background(), item.NotificationID, item.Cue)
	if err != nil || !receipt.Delivered || len(player.played) != 1 || player.played[0] != CueFailure {
		t.Fatalf("receipt=%+v err=%v played=%v", receipt, err, player.played)
	}
}

func TestHostSoundCancellationSuppressesDelayedPlaybackAndStateCompacts(t *testing.T) {
	stateDir := t.TempDir()
	player := &fakeSound{capability: Capability{Available: true, Provider: "fake"}}
	host := NewHostSound(stateDir, player, time.Millisecond, time.Second, RealClock{})
	if err := host.Cancel(context.Background(), "dismissed"); err != nil {
		t.Fatal(err)
	}
	receipt, err := host.PlayNotification(context.Background(), "dismissed", CueAttention)
	if err != nil || receipt.Reason != ClaimCancelled || len(player.played) != 0 {
		t.Fatalf("receipt=%+v err=%v played=%v", receipt, err, player.played)
	}

	old := time.Now().UTC().Add(-ReceiptRetention - time.Second)
	if err := host.withLock(func(state *soundState) error {
		state.cancellations["old"] = old
		batch := soundBatch{ID: "old-batch", Deadline: old, CompletedAt: old}
		receipt := ProviderReceipt{Provider: "fake", Delivered: true, At: old}
		state.batches[batch.ID] = batch
		state.items["old"] = soundItem{NotificationID: "old", BatchID: batch.ID, Cue: CueDone, EnqueuedAt: old, BatchDeadline: old, Receipt: &receipt}
		state.events = 100
		return host.rewrite(*state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.withLock(func(state *soundState) error { return host.compactIfNeeded(state, time.Now().UTC()) }); err != nil {
		t.Fatal(err)
	}
	state, err := host.load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.items["old"]; ok {
		t.Fatal("old completed sound item survived compaction")
	}
	data, err := os.ReadFile(filepath.Join(stateDir, SoundLedgerFileName))
	if err != nil || len(data) > 4096 {
		t.Fatalf("compacted sound ledger len=%d err=%v", len(data), err)
	}
}

func TestHostSoundCompactsCrashAbandonedIncompleteBatches(t *testing.T) {
	stateDir := t.TempDir()
	player := &fakeSound{capability: Capability{Available: true, Provider: "fake"}}
	host := NewHostSound(stateDir, player, time.Second, time.Second, RealClock{})
	now := time.Now().UTC()
	old := now.Add(-ReceiptRetention - time.Hour)
	if err := host.withLock(func(state *soundState) error {
		for i := 0; i < 100; i++ {
			id := fmt.Sprintf("abandoned-%03d", i)
			batchID := "batch-" + id
			batch := soundBatch{ID: batchID, Deadline: old, Leader: "dead-owner", LeaderUntil: old.Add(time.Second)}
			item := soundItem{NotificationID: id, BatchID: batchID, Cue: CueDone, EnqueuedAt: old, BatchDeadline: old}
			state.batches[batchID] = batch
			state.items[id] = item
		}
		// Model a crash after the winner result was durable but before the
		// completed event cleared its expired playback lease.
		batch := soundBatch{ID: "result-only-batch", Deadline: old, Leader: "dead-owner", LeaderUntil: old.Add(time.Second), WinnerID: "result-only"}
		receipt := ProviderReceipt{Provider: "fake", Delivered: true, At: old.Add(2 * time.Second)}
		state.batches[batch.ID] = batch
		state.items[batch.WinnerID] = soundItem{NotificationID: batch.WinnerID, BatchID: batch.ID, Cue: CueFailure, EnqueuedAt: old, BatchDeadline: old, Receipt: &receipt}
		state.playback = &soundPlayback{BatchID: batch.ID, Owner: "dead-owner", LeaseUntil: old.Add(time.Second)}
		state.events = 500
		return host.rewrite(*state)
	}); err != nil {
		t.Fatal(err)
	}

	// A later ordinary enqueue performs event-driven maintenance. Only its live
	// batch/item should remain; repeated crash-abandoned state stays bounded.
	if _, err := host.enqueue("fresh", CueAttention, now); err != nil {
		t.Fatal(err)
	}
	state, err := host.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.batches) != 1 || len(state.items) != 1 || state.playback != nil {
		t.Fatalf("compacted state batches=%d items=%d playback=%+v", len(state.batches), len(state.items), state.playback)
	}
	if _, ok := state.items["fresh"]; !ok {
		t.Fatalf("fresh item missing after compaction: %+v", state.items)
	}
}

func waitSoundItems(t *testing.T, host *HostSound, want int) map[string]soundItem {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := host.load()
		if err == nil && len(state.items) >= want {
			return state.items
		}
		if time.Now().After(deadline) {
			t.Fatalf("sound items did not reach %d (last err %v)", want, err)
		}
		time.Sleep(time.Millisecond)
	}
}
