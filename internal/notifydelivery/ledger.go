// Package notifydelivery owns coordination contracts for external notification
// providers. M0 supplies only the policy-adjacent ledger; no provider can run.
package notifydelivery

import (
	"errors"
	"sync"
	"time"
)

const (
	ClaimWon            = "claimed"
	ClaimAlreadyClaimed = "already_claimed"
	ClaimCancelled      = "cancelled"
	ReceiptRetention    = 24 * time.Hour
)

var (
	ErrNoClaim      = errors.New("notifydelivery: no active claim")
	ErrClaimOwner   = errors.New("notifydelivery: receipt owner does not own claim")
	ErrInvalidClaim = errors.New("notifydelivery: invalid claim")
)

// Receipt is the durable terminal outcome of a delivery attempt. Success and
// failure are both receipts: neither should replay indefinitely.
type Receipt struct {
	Owner       string    `json:"owner"`
	Provider    string    `json:"provider,omitempty"`
	Succeeded   bool      `json:"succeeded"`
	Error       string    `json:"error,omitempty"`
	CompletedAt time.Time `json:"completedAt"`
}

type Entry struct {
	NotificationID string     `json:"notificationId"`
	Channel        string     `json:"channel"`
	Owner          string     `json:"owner"`
	ClaimedAt      time.Time  `json:"claimedAt"`
	LeaseUntil     time.Time  `json:"leaseUntil"`
	Group          string     `json:"group,omitempty"`
	Receipt        *Receipt   `json:"receipt,omitempty"`
	CancelledAt    *time.Time `json:"cancelledAt,omitempty"`
}

type NativeGroupState struct {
	Group          string    `json:"group"`
	NotificationID string    `json:"notificationId"`
	Phase          string    `json:"phase"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type NativeOperationResult struct {
	Attempted bool
	Receipt   ProviderReceipt
	Reason    string
	Err       error
}

type NativeCancelResult struct {
	Removed bool
	Reason  string
	Err     error
}

type Ledger interface {
	Claim(notificationID, channel, owner string, now time.Time, lease time.Duration) (won bool, reason string, err error)
	Complete(notificationID, channel string, receipt Receipt) error
	ReleaseExpired(now time.Time) (int, error)
	List(notificationID string) ([]Entry, error)
	Cancelled(notificationID string) (bool, error)
	DeliverNative(notificationID, group, owner string, now time.Time, lease time.Duration, invoke func() (ProviderReceipt, error)) NativeOperationResult
	Cancel(notificationID, group, owner string, now time.Time, remove func() error) NativeCancelResult
	Close() error
}

// MemoryLedger is the deterministic test and fallback implementation.
type MemoryLedger struct {
	mu            sync.Mutex
	entries       map[string]Entry
	cancellations map[string]time.Time
	groups        map[string]NativeGroupState
}

var _ Ledger = (*MemoryLedger)(nil)

func NewMemoryLedger() *MemoryLedger {
	return &MemoryLedger{
		entries: make(map[string]Entry), cancellations: make(map[string]time.Time),
		groups: make(map[string]NativeGroupState),
	}
}

func ledgerKey(notificationID, channel string) string { return notificationID + "\x00" + channel }

func validateClaim(notificationID, channel, owner string, lease time.Duration) error {
	if notificationID == "" || channel == "" || owner == "" || lease <= 0 {
		return ErrInvalidClaim
	}
	return nil
}

func (l *MemoryLedger) Claim(notificationID, channel, owner string, now time.Time, lease time.Duration) (bool, string, error) {
	if err := validateClaim(notificationID, channel, owner, lease); err != nil {
		return false, "", err
	}
	now = now.UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	key := ledgerKey(notificationID, channel)
	if _, cancelled := l.cancellations[notificationID]; cancelled {
		return false, ClaimCancelled, nil
	}
	if existing, ok := l.entries[key]; ok && (existing.Receipt != nil || now.Before(existing.LeaseUntil)) {
		return false, ClaimAlreadyClaimed, nil
	}
	l.entries[key] = Entry{NotificationID: notificationID, Channel: channel, Owner: owner, ClaimedAt: now, LeaseUntil: now.Add(lease)}
	return true, ClaimWon, nil
}

func (l *MemoryLedger) Complete(notificationID, channel string, receipt Receipt) error {
	l.mu.Lock()
	defer l.mu.Unlock()
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
	entry.Receipt = &receipt
	l.entries[key] = entry
	return nil
}

func (l *MemoryLedger) ReleaseExpired(now time.Time) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	removed := releaseExpired(l.entries, l.cancellations, l.groups, now.UTC())
	return removed, nil
}

func releaseExpired(entries map[string]Entry, cancellations map[string]time.Time, groups map[string]NativeGroupState, now time.Time) int {
	removed := 0
	for key, entry := range entries {
		expiredLease := entry.Receipt == nil && !now.Before(entry.LeaseUntil)
		expiredReceipt := entry.Receipt != nil && !now.Before(entry.Receipt.CompletedAt.Add(ReceiptRetention))
		if expiredLease || expiredReceipt {
			delete(entries, key)
			removed++
		}
	}
	for id, cancelledAt := range cancellations {
		if !now.Before(cancelledAt.Add(ReceiptRetention)) {
			delete(cancellations, id)
			removed++
		}
	}
	// Current native group state is live provider state, not receipt history.
	// A sticky notification can remain active indefinitely, so retain the one
	// current member per group until cancellation or a successful replacement.
	return removed
}

func (l *MemoryLedger) List(notificationID string) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
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

func (l *MemoryLedger) Cancelled(notificationID string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.cancellations[notificationID]
	return ok, nil
}

func (l *MemoryLedger) DeliverNative(notificationID, group, owner string, now time.Time, lease time.Duration, invoke func() (ProviderReceipt, error)) NativeOperationResult {
	if err := validateClaim(notificationID, ChannelNative, owner, lease); err != nil || invoke == nil {
		if err == nil {
			err = ErrInvalidClaim
		}
		return NativeOperationResult{Err: err}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return deliverNativeLocked(l.entries, l.cancellations, l.groups, notificationID, group, owner, now.UTC(), lease, nil, invoke)
}

func (l *MemoryLedger) Cancel(notificationID, group, owner string, now time.Time, remove func() error) NativeCancelResult {
	if notificationID == "" || owner == "" {
		return NativeCancelResult{Err: ErrInvalidClaim}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return cancelLocked(l.cancellations, l.groups, notificationID, group, now.UTC(), nil, remove)
}

type nativeAppend func(kind string, entry *Entry, receipt *Receipt, group *NativeGroupState, at time.Time) error

func deliverNativeLocked(entries map[string]Entry, cancellations map[string]time.Time, groups map[string]NativeGroupState, notificationID, group, owner string, now time.Time, lease time.Duration, appendEvent nativeAppend, invoke func() (ProviderReceipt, error)) NativeOperationResult {
	if _, cancelled := cancellations[notificationID]; cancelled {
		return NativeOperationResult{Reason: ClaimCancelled}
	}
	if current, ok := groups[group]; group != "" && ok && current.NotificationID == notificationID {
		return NativeOperationResult{Reason: ClaimAlreadyClaimed}
	}
	key := ledgerKey(notificationID, ChannelNative)
	if existing, ok := entries[key]; ok && (existing.Receipt != nil || now.Before(existing.LeaseUntil)) {
		return NativeOperationResult{Reason: ClaimAlreadyClaimed}
	}
	entry := Entry{NotificationID: notificationID, Channel: ChannelNative, Owner: owner, ClaimedAt: now, LeaseUntil: now.Add(lease), Group: group}
	if appendEvent != nil {
		if err := appendEvent(string(ledgerClaimed), &entry, nil, nil, now); err != nil {
			return NativeOperationResult{Err: err}
		}
	}
	entries[key] = entry
	providerReceipt, invokeErr := invoke()
	completedAt := providerReceipt.At.UTC()
	if completedAt.IsZero() {
		completedAt = now
	}
	receipt := Receipt{Owner: owner, Provider: providerReceipt.Provider, Succeeded: providerReceipt.Delivered, CompletedAt: completedAt}
	if invokeErr != nil {
		receipt.Error = invokeErr.Error()
	}
	if appendEvent != nil {
		if err := appendEvent(string(ledgerCompleted), nil, &receipt, nil, completedAt); err != nil {
			return NativeOperationResult{Attempted: true, Receipt: providerReceipt, Err: err}
		}
	}
	entry.Receipt = &receipt
	entries[key] = entry
	if group != "" && providerReceipt.Delivered {
		current := NativeGroupState{Group: group, NotificationID: notificationID, Phase: "delivered", UpdatedAt: completedAt}
		if appendEvent != nil {
			if err := appendEvent(string(ledgerNativeGroup), nil, nil, &current, completedAt); err != nil {
				return NativeOperationResult{Attempted: true, Receipt: providerReceipt, Err: err}
			}
		}
		groups[group] = current
	}
	return NativeOperationResult{Attempted: true, Receipt: providerReceipt, Err: invokeErr}
}

func cancelLocked(cancellations map[string]time.Time, groups map[string]NativeGroupState, notificationID, group string, now time.Time, appendEvent nativeAppend, remove func() error) NativeCancelResult {
	if _, exists := cancellations[notificationID]; !exists {
		if appendEvent != nil {
			if err := appendEvent(string(ledgerCancelled), nil, nil, nil, now); err != nil {
				return NativeCancelResult{Err: err}
			}
		}
		cancellations[notificationID] = now
	}
	current, ok := groups[group]
	if group == "" || !ok || current.NotificationID != notificationID || remove == nil {
		return NativeCancelResult{Reason: ClaimCancelled}
	}
	if err := remove(); err != nil {
		return NativeCancelResult{Reason: ClaimCancelled, Err: err}
	}
	cleared := NativeGroupState{Group: group, UpdatedAt: now}
	if appendEvent != nil {
		if err := appendEvent(string(ledgerNativeGroup), nil, nil, &cleared, now); err != nil {
			return NativeCancelResult{Removed: true, Reason: ClaimCancelled, Err: err}
		}
	}
	delete(groups, group)
	return NativeCancelResult{Removed: true, Reason: ClaimCancelled}
}

func cloneEntry(entry Entry) Entry {
	if entry.Receipt != nil {
		receipt := *entry.Receipt
		entry.Receipt = &receipt
	}
	return entry
}

func (l *MemoryLedger) Close() error { return nil }
