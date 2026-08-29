package notifydelivery

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uirequest"
)

const (
	SoundLedgerFileName = "notification-sound-batches.jsonl"
	soundPollInterval   = 10 * time.Millisecond
)

type soundEventKind string

const (
	soundEnqueued  soundEventKind = "enqueued"
	soundLeader    soundEventKind = "leader"
	soundPlaying   soundEventKind = "playing"
	soundResult    soundEventKind = "result"
	soundCompleted soundEventKind = "completed"
	soundCancelled soundEventKind = "cancelled"
)

type soundItem struct {
	NotificationID string           `json:"notificationId"`
	BatchID        string           `json:"batchId"`
	Cue            Cue              `json:"cue"`
	EnqueuedAt     time.Time        `json:"enqueuedAt"`
	BatchDeadline  time.Time        `json:"batchDeadline"`
	Receipt        *ProviderReceipt `json:"receipt,omitempty"`
	Error          string           `json:"error,omitempty"`
}

type soundBatch struct {
	ID          string    `json:"id"`
	Deadline    time.Time `json:"deadline"`
	Leader      string    `json:"leader,omitempty"`
	LeaderUntil time.Time `json:"leaderUntil,omitempty"`
	WinnerID    string    `json:"winnerId,omitempty"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
}

type soundPlayback struct {
	BatchID    string    `json:"batchId"`
	Owner      string    `json:"owner"`
	LeaseUntil time.Time `json:"leaseUntil"`
}

type soundEvent struct {
	Event          soundEventKind `json:"event"`
	At             time.Time      `json:"at"`
	Item           *soundItem     `json:"item,omitempty"`
	Batch          *soundBatch    `json:"batch,omitempty"`
	Playback       *soundPlayback `json:"playback,omitempty"`
	NotificationID string         `json:"notificationId,omitempty"`
}

type soundState struct {
	items         map[string]soundItem
	batches       map[string]soundBatch
	playback      *soundPlayback
	cancellations map[string]time.Time
	events        int
}

func newSoundState() soundState {
	return soundState{
		items: make(map[string]soundItem), batches: make(map[string]soundBatch),
		cancellations: make(map[string]time.Time),
	}
}

var hostSoundSequence atomic.Uint64

// HostSound coordinates the burst window and playback lease through a small
// locked JSONL file. Independent Sidecar processes therefore share one batch
// winner and one playback slot while the underlying player remains replaceable.
// Construction is I/O-free; enqueue is called only from app commands or CLI.
type HostSound struct {
	stateDir string
	path     string
	player   SoundPlayer
	window   time.Duration
	lease    time.Duration
	clock    Clock
	owner    string
	mu       sync.Mutex
}

var _ SoundCoordinator = (*HostSound)(nil)

func NewHostSound(stateDir string, player SoundPlayer, window, lease time.Duration, clock Clock) *HostSound {
	if window <= 0 {
		window = 75 * time.Millisecond
	}
	if lease <= 0 {
		lease = DefaultLease
	}
	if clock == nil {
		clock = RealClock{}
	}
	return &HostSound{
		stateDir: stateDir, path: filepath.Join(stateDir, SoundLedgerFileName), player: player,
		window: window, lease: lease, clock: clock,
		owner: fmt.Sprintf("%s:%d:%d", uirequest.HostName(), os.Getpid(), hostSoundSequence.Add(1)),
	}
}

func (h *HostSound) Probe(ctx context.Context) Capability {
	if h == nil || h.player == nil {
		return Capability{Reason: "no sound player"}
	}
	return h.player.Probe(ctx)
}

func (h *HostSound) PlayNotification(ctx context.Context, notificationID string, cue Cue) (ProviderReceipt, error) {
	if notificationID == "" || cue.priority() == 0 {
		return ProviderReceipt{}, ErrInvalidClaim
	}
	item, err := h.enqueue(notificationID, cue, h.clock.Now().UTC())
	if err != nil {
		return ProviderReceipt{}, err
	}
	if item.Receipt != nil {
		return *item.Receipt, nil
	}
	if err := h.wait(ctx, item.BatchDeadline); err != nil {
		return ProviderReceipt{}, err
	}
	for {
		step, err := h.prepare(notificationID, h.clock.Now().UTC())
		if err != nil {
			return ProviderReceipt{}, err
		}
		if step.done {
			return step.receipt, step.err
		}
		if step.play {
			receipt, playErr := h.player.Play(ctx, step.cue)
			if err := h.finish(step.batchID, step.winnerID, receipt, playErr, h.clock.Now().UTC()); err != nil {
				return ProviderReceipt{}, err
			}
			continue
		}
		if err := h.waitFor(ctx, step.wait); err != nil {
			return ProviderReceipt{}, err
		}
	}
}

func (h *HostSound) Cancel(_ context.Context, notificationID string) error {
	if notificationID == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.clock.Now().UTC()
	return h.withLock(func(state *soundState) error {
		if _, exists := state.cancellations[notificationID]; exists {
			return nil
		}
		if err := h.append(soundEvent{Event: soundCancelled, At: now, NotificationID: notificationID}); err != nil {
			return err
		}
		state.cancellations[notificationID] = now
		return h.compactIfNeeded(state, now)
	})
}

type soundStep struct {
	done     bool
	receipt  ProviderReceipt
	err      error
	play     bool
	batchID  string
	winnerID string
	cue      Cue
	wait     time.Duration
}

func (h *HostSound) enqueue(notificationID string, cue Cue, now time.Time) (soundItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out soundItem
	err := h.withLock(func(state *soundState) error {
		if _, cancelled := state.cancellations[notificationID]; cancelled {
			receipt := ProviderReceipt{Provider: "host-arbiter", Reason: ClaimCancelled, At: now}
			out = soundItem{NotificationID: notificationID, Cue: cue, EnqueuedAt: now, BatchDeadline: now, Receipt: &receipt}
			return nil
		}
		if existing, ok := state.items[notificationID]; ok {
			out = existing
			return nil
		}
		batchID := ""
		var deadline time.Time
		for id, batch := range state.batches {
			if batch.CompletedAt.IsZero() && now.Before(batch.Deadline) && (deadline.IsZero() || batch.Deadline.Before(deadline)) {
				batchID, deadline = id, batch.Deadline
			}
		}
		if batchID == "" {
			batchID = fmt.Sprintf("snd-%013x-%s", now.UnixNano()/1e3, sanitizeOwner(h.owner))
			deadline = now.Add(h.window)
			state.batches[batchID] = soundBatch{ID: batchID, Deadline: deadline}
		}
		out = soundItem{NotificationID: notificationID, BatchID: batchID, Cue: cue, EnqueuedAt: now, BatchDeadline: deadline}
		if err := h.append(soundEvent{Event: soundEnqueued, At: now, Item: &out, Batch: ptrBatch(state.batches[batchID])}); err != nil {
			return err
		}
		state.items[notificationID] = out
		return h.compactIfNeeded(state, now)
	})
	return out, err
}

func (h *HostSound) prepare(notificationID string, now time.Time) (soundStep, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	step := soundStep{wait: soundPollInterval}
	err := h.withLock(func(state *soundState) error {
		item, ok := state.items[notificationID]
		if !ok {
			return fmt.Errorf("notifydelivery: sound item %s disappeared", notificationID)
		}
		if item.Receipt != nil {
			step.done, step.receipt = true, *item.Receipt
			if item.Error != "" {
				step.err = fmt.Errorf("%s", item.Error)
			}
			return nil
		}
		if _, cancelled := state.cancellations[notificationID]; cancelled {
			receipt := ProviderReceipt{Provider: "host-arbiter", Reason: ClaimCancelled, At: now}
			item.Receipt = &receipt
			state.items[notificationID] = item
			if err := h.append(soundEvent{Event: soundResult, At: now, Item: &item}); err != nil {
				return err
			}
			step.done, step.receipt = true, receipt
			return nil
		}
		batch := state.batches[item.BatchID]
		if now.Before(batch.Deadline) {
			step.wait = batch.Deadline.Sub(now)
			return nil
		}
		if state.playback != nil && now.Before(state.playback.LeaseUntil) {
			step.wait = minDuration(soundPollInterval, state.playback.LeaseUntil.Sub(now))
			return nil
		}
		if batch.Leader != "" && now.Before(batch.LeaderUntil) {
			step.wait = minDuration(soundPollInterval, batch.LeaderUntil.Sub(now))
			return nil
		}
		batch.Leader, batch.LeaderUntil = h.owner, now.Add(h.lease)
		for id, candidate := range state.items {
			if candidate.BatchID != batch.ID || candidate.Receipt != nil {
				continue
			}
			if _, cancelled := state.cancellations[id]; !cancelled {
				continue
			}
			receipt := ProviderReceipt{Provider: "host-arbiter", Reason: ClaimCancelled, At: now}
			candidate.Receipt = &receipt
			state.items[id] = candidate
			if err := h.append(soundEvent{Event: soundResult, At: now, Item: &candidate}); err != nil {
				return err
			}
		}
		winner := winnerForBatch(*state, batch.ID)
		if winner.NotificationID == "" {
			return fmt.Errorf("notifydelivery: sound batch %s has no items", batch.ID)
		}
		batch.WinnerID = winner.NotificationID
		playback := soundPlayback{BatchID: batch.ID, Owner: h.owner, LeaseUntil: now.Add(h.lease)}
		if err := h.append(soundEvent{Event: soundLeader, At: now, Batch: &batch}); err != nil {
			return err
		}
		if err := h.append(soundEvent{Event: soundPlaying, At: now, Playback: &playback}); err != nil {
			return err
		}
		state.batches[batch.ID] = batch
		state.playback = &playback
		for id, candidate := range state.items {
			if candidate.BatchID != batch.ID || id == winner.NotificationID || candidate.Receipt != nil {
				continue
			}
			receipt := ProviderReceipt{Provider: "host-arbiter", Reason: string(notifyReasonRateLimited), At: now}
			candidate.Receipt = &receipt
			state.items[id] = candidate
			if err := h.append(soundEvent{Event: soundResult, At: now, Item: &candidate}); err != nil {
				return err
			}
		}
		step.play, step.batchID, step.winnerID, step.cue = true, batch.ID, winner.NotificationID, winner.Cue
		return nil
	})
	return step, err
}

func (h *HostSound) finish(batchID, winnerID string, receipt ProviderReceipt, playErr error, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.withLock(func(state *soundState) error {
		batch, ok := state.batches[batchID]
		if !ok || batch.WinnerID != winnerID {
			return fmt.Errorf("notifydelivery: sound batch ownership changed")
		}
		if batch.Leader != h.owner || state.playback == nil || state.playback.Owner != h.owner || state.playback.BatchID != batchID {
			return fmt.Errorf("notifydelivery: sound playback lease lost")
		}
		if receipt.At.IsZero() {
			receipt.At = now
		}
		item := state.items[winnerID]
		item.Receipt = &receipt
		if playErr != nil {
			item.Error = playErr.Error()
		}
		batch.CompletedAt = now
		if err := h.append(soundEvent{Event: soundResult, At: now, Item: &item}); err != nil {
			return err
		}
		if err := h.append(soundEvent{Event: soundCompleted, At: now, Batch: &batch}); err != nil {
			return err
		}
		state.items[winnerID] = item
		state.batches[batchID] = batch
		state.playback = nil
		return h.compactIfNeeded(state, now)
	})
}

func winnerForBatch(state soundState, batchID string) soundItem {
	var winner soundItem
	for _, item := range state.items {
		if item.BatchID != batchID || item.Receipt != nil {
			continue
		}
		if winner.NotificationID == "" || item.Cue.priority() > winner.Cue.priority() || (item.Cue.priority() == winner.Cue.priority() && item.NotificationID < winner.NotificationID) {
			winner = item
		}
	}
	return winner
}

func (h *HostSound) wait(ctx context.Context, deadline time.Time) error {
	d := deadline.Sub(h.clock.Now())
	if d <= 0 {
		return nil
	}
	return h.waitFor(ctx, d)
}

func (h *HostSound) waitFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	done := make(chan struct{})
	timer := h.clock.AfterFunc(d, func() { close(done) })
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *HostSound) withLock(fn func(*soundState) error) error {
	if err := config.AssertIsolatedPath(h.stateDir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(h.path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()
	deadline := time.Now().Add(ledgerLockTimeout)
	for {
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("notifydelivery: sound lock timeout")
		}
		time.Sleep(soundPollInterval)
	}
	state, err := h.load()
	if err != nil {
		return err
	}
	return fn(&state)
}

func (h *HostSound) load() (soundState, error) {
	state := newSoundState()
	f, err := os.Open(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event soundEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		state.events++
		switch event.Event {
		case soundEnqueued:
			if event.Item != nil && event.Batch != nil {
				state.items[event.Item.NotificationID] = *event.Item
				state.batches[event.Batch.ID] = *event.Batch
			}
		case soundLeader:
			if event.Batch != nil {
				state.batches[event.Batch.ID] = *event.Batch
			}
		case soundPlaying:
			if event.Playback != nil {
				playback := *event.Playback
				state.playback = &playback
			}
		case soundResult:
			if event.Item != nil {
				state.items[event.Item.NotificationID] = *event.Item
			}
		case soundCompleted:
			if event.Batch != nil {
				state.batches[event.Batch.ID] = *event.Batch
				if state.playback != nil && state.playback.BatchID == event.Batch.ID {
					state.playback = nil
				}
			}
		case soundCancelled:
			if event.NotificationID != "" {
				state.cancellations[event.NotificationID] = event.At.UTC()
			}
		}
	}
	return state, scanner.Err()
}

func (h *HostSound) append(event soundEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (h *HostSound) compactIfNeeded(state *soundState, now time.Time) error {
	changed := false
	for id, batch := range state.batches {
		reclaimAt := batch.CompletedAt
		if reclaimAt.IsZero() {
			reclaimAt = batch.Deadline
			if batch.LeaderUntil.After(reclaimAt) {
				reclaimAt = batch.LeaderUntil
			}
			if state.playback != nil && state.playback.BatchID == id {
				if now.Before(state.playback.LeaseUntil) {
					continue
				}
				if state.playback.LeaseUntil.After(reclaimAt) {
					reclaimAt = state.playback.LeaseUntil
				}
			}
			for _, item := range state.items {
				if item.BatchID != id {
					continue
				}
				if item.EnqueuedAt.After(reclaimAt) {
					reclaimAt = item.EnqueuedAt
				}
				if item.Receipt != nil && item.Receipt.At.After(reclaimAt) {
					reclaimAt = item.Receipt.At
				}
			}
		}
		if reclaimAt.IsZero() || now.Before(reclaimAt.Add(ReceiptRetention)) {
			continue
		}
		delete(state.batches, id)
		for itemID, item := range state.items {
			if item.BatchID == id {
				delete(state.items, itemID)
			}
		}
		if state.playback != nil && state.playback.BatchID == id {
			state.playback = nil
		}
		changed = true
	}
	// A truncated/corrupt event stream can leave an item without its enqueue
	// batch. Bound that state by the same retention window rather than keeping
	// an unreachable record forever.
	for id, item := range state.items {
		if _, ok := state.batches[item.BatchID]; ok || now.Before(item.EnqueuedAt.Add(ReceiptRetention)) {
			continue
		}
		delete(state.items, id)
		changed = true
	}
	for id, cancelledAt := range state.cancellations {
		if !now.Before(cancelledAt.Add(ReceiptRetention)) {
			delete(state.cancellations, id)
			changed = true
		}
	}
	active := len(state.items) + len(state.batches) + len(state.cancellations)
	if !changed && state.events <= active*3+16 {
		return nil
	}
	return h.rewrite(*state)
}

func (h *HostSound) rewrite(state soundState) error {
	tmp := fmt.Sprintf("%s.tmp.%d", h.path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	items := make([]soundItem, 0, len(state.items))
	for _, item := range state.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].NotificationID < items[j].NotificationID })
	for _, item := range items {
		batch := state.batches[item.BatchID]
		itemCopy, batchCopy := item, batch
		if err := enc.Encode(soundEvent{Event: soundEnqueued, At: item.EnqueuedAt, Item: &itemCopy, Batch: &batchCopy}); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if item.Receipt != nil {
			if err := enc.Encode(soundEvent{Event: soundResult, At: item.Receipt.At, Item: &itemCopy}); err != nil {
				_ = f.Close()
				_ = os.Remove(tmp)
				return err
			}
		}
	}
	for _, batch := range state.batches {
		if !batch.CompletedAt.IsZero() {
			batchCopy := batch
			if err := enc.Encode(soundEvent{Event: soundCompleted, At: batch.CompletedAt, Batch: &batchCopy}); err != nil {
				_ = f.Close()
				_ = os.Remove(tmp)
				return err
			}
		}
	}
	for id, at := range state.cancellations {
		if err := enc.Encode(soundEvent{Event: soundCancelled, At: at, NotificationID: id}); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if state.playback != nil {
		playback := *state.playback
		if err := enc.Encode(soundEvent{Event: soundPlaying, At: nowOrZero(playback.LeaseUntil), Playback: &playback}); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, h.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func ptrBatch(batch soundBatch) *soundBatch { return &batch }

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func sanitizeOwner(owner string) string {
	result := make([]rune, 0, len(owner))
	for _, r := range owner {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			result = append(result, r)
		}
	}
	if len(result) > 20 {
		result = result[:20]
	}
	return string(result)
}

func nowOrZero(t time.Time) time.Time { return t }
