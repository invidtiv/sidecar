package notifydelivery

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

const LedgerFileName = "notification-delivery.jsonl"

type ledgerEventKind string

const (
	ledgerClaimed     ledgerEventKind = "claimed"
	ledgerCompleted   ledgerEventKind = "completed"
	ledgerReleased    ledgerEventKind = "released"
	ledgerCancelled   ledgerEventKind = "cancelled"
	ledgerNativeGroup ledgerEventKind = "native_group"
)

type ledgerEvent struct {
	Event          ledgerEventKind   `json:"event"`
	At             time.Time         `json:"at"`
	NotificationID string            `json:"notificationId"`
	Channel        string            `json:"channel"`
	Entry          *Entry            `json:"entry,omitempty"`
	Receipt        *Receipt          `json:"receipt,omitempty"`
	Group          *NativeGroupState `json:"group,omitempty"`
}

type JSONLLedger struct {
	mu            sync.Mutex
	path          string
	entries       map[string]Entry
	cancellations map[string]time.Time
	groups        map[string]NativeGroupState
	events        int
}

var _ Ledger = (*JSONLLedger)(nil)

func Open(stateDir string) (*JSONLLedger, error) {
	if stateDir == "" {
		return nil, fmt.Errorf("notifydelivery: empty state dir")
	}
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return nil, err
	}
	return OpenPath(filepath.Join(stateDir, LedgerFileName))
}

func OpenPath(path string) (*JSONLLedger, error) {
	if path == "" {
		return nil, fmt.Errorf("notifydelivery: empty ledger path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &JSONLLedger{
		path: path, entries: make(map[string]Entry), cancellations: make(map[string]time.Time),
		groups: make(map[string]NativeGroupState),
	}
	if err := l.withLock(func() error {
		if err := l.reload(); err != nil {
			return err
		}
		removed := releaseExpired(l.entries, l.cancellations, l.groups, time.Now().UTC())
		if removed > 0 || l.events > l.liveEventCount()*2+8 {
			return l.rewrite()
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *JSONLLedger) Claim(notificationID, channel, owner string, now time.Time, lease time.Duration) (bool, string, error) {
	if err := validateClaim(notificationID, channel, owner, lease); err != nil {
		return false, "", err
	}
	now = now.UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	won := false
	reason := ClaimAlreadyClaimed
	err := l.withLock(func() error {
		if err := l.reload(); err != nil {
			return err
		}
		key := ledgerKey(notificationID, channel)
		if _, cancelled := l.cancellations[notificationID]; cancelled {
			reason = ClaimCancelled
			return nil
		}
		if existing, ok := l.entries[key]; ok && (existing.Receipt != nil || now.Before(existing.LeaseUntil)) {
			return nil
		}
		entry := Entry{NotificationID: notificationID, Channel: channel, Owner: owner, ClaimedAt: now, LeaseUntil: now.Add(lease)}
		if err := l.append(ledgerEvent{Event: ledgerClaimed, At: now, NotificationID: notificationID, Channel: channel, Entry: &entry}); err != nil {
			return err
		}
		l.entries[key] = entry
		won, reason = true, ClaimWon
		return nil
	})
	return won, reason, err
}

func (l *JSONLLedger) Complete(notificationID, channel string, receipt Receipt) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.withLock(func() error {
		if err := l.reload(); err != nil {
			return err
		}
		key := ledgerKey(notificationID, channel)
		entry, ok := l.entries[key]
		if !ok || entry.Receipt != nil {
			return ErrNoClaim
		}
		if receipt.Owner == "" || receipt.Owner != entry.Owner {
			return ErrClaimOwner
		}
		if receipt.CompletedAt.IsZero() {
			receipt.CompletedAt = time.Now().UTC()
		} else {
			receipt.CompletedAt = receipt.CompletedAt.UTC()
		}
		if err := l.append(ledgerEvent{Event: ledgerCompleted, At: receipt.CompletedAt, NotificationID: notificationID, Channel: channel, Receipt: &receipt}); err != nil {
			return err
		}
		entry.Receipt = &receipt
		l.entries[key] = entry
		return nil
	})
}

func (l *JSONLLedger) ReleaseExpired(now time.Time) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	removed := 0
	err := l.withLock(func() error {
		if err := l.reload(); err != nil {
			return err
		}
		removed = releaseExpired(l.entries, l.cancellations, l.groups, now.UTC())
		if removed > 0 || l.events > l.liveEventCount()*2+8 {
			return l.rewrite()
		}
		return nil
	})
	return removed, err
}

func (l *JSONLLedger) List(notificationID string) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.reload(); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, 2)
	for _, entry := range l.entries {
		if notificationID == "" || entry.NotificationID == notificationID {
			if at, ok := l.cancellations[entry.NotificationID]; ok {
				cancelledAt := at
				entry.CancelledAt = &cancelledAt
			}
			out = append(out, cloneEntry(entry))
		}
	}
	sortEntries(out)
	return out, nil
}

func (l *JSONLLedger) Cancelled(notificationID string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var cancelled bool
	err := l.withLock(func() error {
		if err := l.reload(); err != nil {
			return err
		}
		_, cancelled = l.cancellations[notificationID]
		return nil
	})
	return cancelled, err
}

func (l *JSONLLedger) DeliverNative(notificationID, group, owner string, now time.Time, lease time.Duration, invoke func() (ProviderReceipt, error)) NativeOperationResult {
	if err := validateClaim(notificationID, ChannelNative, owner, lease); err != nil || invoke == nil {
		if err == nil {
			err = ErrInvalidClaim
		}
		return NativeOperationResult{Err: err}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var result NativeOperationResult
	err := l.withLock(func() error {
		if err := l.reload(); err != nil {
			return err
		}
		appendEvent := func(kind string, entry *Entry, receipt *Receipt, groupState *NativeGroupState, at time.Time) error {
			event := ledgerEvent{Event: ledgerEventKind(kind), At: at, NotificationID: notificationID, Channel: ChannelNative, Entry: entry, Receipt: receipt, Group: groupState}
			return l.append(event)
		}
		result = deliverNativeLocked(l.entries, l.cancellations, l.groups, notificationID, group, owner, now.UTC(), lease, appendEvent, invoke)
		return result.Err
	})
	if err != nil && result.Err == nil {
		result.Err = err
	}
	return result
}

func (l *JSONLLedger) Cancel(notificationID, group, owner string, now time.Time, remove func() error) NativeCancelResult {
	if notificationID == "" || owner == "" {
		return NativeCancelResult{Err: ErrInvalidClaim}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var result NativeCancelResult
	err := l.withLock(func() error {
		if err := l.reload(); err != nil {
			return err
		}
		appendEvent := func(kind string, entry *Entry, receipt *Receipt, groupState *NativeGroupState, at time.Time) error {
			event := ledgerEvent{Event: ledgerEventKind(kind), At: at, NotificationID: notificationID, Channel: ChannelNative, Entry: entry, Receipt: receipt, Group: groupState}
			return l.append(event)
		}
		result = cancelLocked(l.cancellations, l.groups, notificationID, group, now.UTC(), appendEvent, remove)
		return result.Err
	})
	if err != nil && result.Err == nil {
		result.Err = err
	}
	return result
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].NotificationID != entries[j].NotificationID {
			return entries[i].NotificationID < entries[j].NotificationID
		}
		return entries[i].Channel < entries[j].Channel
	})
}

func (l *JSONLLedger) reload() error {
	l.entries = make(map[string]Entry)
	l.cancellations = make(map[string]time.Time)
	l.groups = make(map[string]NativeGroupState)
	l.events = 0
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event ledgerEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		l.events++
		key := ledgerKey(event.NotificationID, event.Channel)
		switch event.Event {
		case ledgerClaimed:
			if event.Entry != nil {
				l.entries[key] = *event.Entry
			}
		case ledgerCompleted:
			if entry, ok := l.entries[key]; ok && event.Receipt != nil {
				entry.Receipt = event.Receipt
				l.entries[key] = entry
			}
		case ledgerReleased:
			delete(l.entries, key)
		case ledgerCancelled:
			l.cancellations[event.NotificationID] = event.At.UTC()
		case ledgerNativeGroup:
			if event.Group == nil || event.Group.NotificationID == "" {
				if event.Group != nil {
					delete(l.groups, event.Group.Group)
				}
			} else {
				l.groups[event.Group.Group] = *event.Group
			}
		}
	}
	return scanner.Err()
}

func (l *JSONLLedger) append(event ledgerEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	l.events++
	return nil
}

func (l *JSONLLedger) rewrite() error {
	tmp := fmt.Sprintf("%s.tmp.%d", l.path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	entries, _ := l.snapshot("")
	for i := range entries {
		entry := entries[i]
		if err := enc.Encode(ledgerEvent{Event: ledgerClaimed, At: entry.ClaimedAt, NotificationID: entry.NotificationID, Channel: entry.Channel, Entry: &entry}); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if entry.Receipt != nil {
			if err := enc.Encode(ledgerEvent{Event: ledgerCompleted, At: entry.Receipt.CompletedAt, NotificationID: entry.NotificationID, Channel: entry.Channel, Receipt: entry.Receipt}); err != nil {
				_ = f.Close()
				_ = os.Remove(tmp)
				return err
			}
		}
	}
	for id, at := range l.cancellations {
		if err := enc.Encode(ledgerEvent{Event: ledgerCancelled, At: at, NotificationID: id, Channel: ChannelNative}); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	for _, group := range l.groups {
		groupCopy := group
		if err := enc.Encode(ledgerEvent{Event: ledgerNativeGroup, At: group.UpdatedAt, NotificationID: group.NotificationID, Channel: ChannelNative, Group: &groupCopy}); err != nil {
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
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	l.events = len(entries) + len(l.cancellations) + len(l.groups)
	for _, entry := range entries {
		if entry.Receipt != nil {
			l.events++
		}
	}
	return nil
}

func (l *JSONLLedger) snapshot(notificationID string) ([]Entry, error) {
	out := make([]Entry, 0, len(l.entries))
	for _, entry := range l.entries {
		if notificationID == "" || entry.NotificationID == notificationID {
			out = append(out, cloneEntry(entry))
		}
	}
	sortEntries(out)
	return out, nil
}

func (l *JSONLLedger) liveEventCount() int {
	count := len(l.entries) + len(l.cancellations) + len(l.groups)
	for _, entry := range l.entries {
		if entry.Receipt != nil {
			count++
		}
	}
	return count
}

const ledgerLockTimeout = 45 * time.Second

func (l *JSONLLedger) withLock(fn func() error) error {
	lock, err := os.OpenFile(l.path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()
	deadline := time.Now().Add(ledgerLockTimeout)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return fn()
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("notifydelivery: lock acquisition timeout after %v", ledgerLockTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (l *JSONLLedger) Close() error { return nil }
