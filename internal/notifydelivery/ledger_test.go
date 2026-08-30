package notifydelivery

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestIndependentJSONLStoresHaveOneClaimWinnerPerChannel(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	one, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	two, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	type result struct {
		won    bool
		reason string
		err    error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for owner, ledger := range map[string]*JSONLLedger{"one": one, "two": two} {
		wg.Add(1)
		go func(owner string, ledger *JSONLLedger) {
			defer wg.Done()
			won, reason, claimErr := ledger.Claim("ntf-1", "sound", owner, now, time.Minute)
			results <- result{won, reason, claimErr}
		}(owner, ledger)
	}
	wg.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.won {
			winners++
		} else if result.reason != ClaimAlreadyClaimed {
			t.Fatalf("loser reason = %q", result.reason)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want one", winners)
	}
	if won, _, err := two.Claim("ntf-1", "native", "two", now, time.Minute); err != nil || !won {
		t.Fatalf("channels must claim independently: won=%v err=%v", won, err)
	}
}

func TestExpiredLeaseRetriesButReceiptDoesNot(t *testing.T) {
	for name, ledger := range map[string]Ledger{
		"memory": NewMemoryLedger(),
		"jsonl":  mustOpenLedger(t),
	} {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			won, _, err := ledger.Claim("ntf-lease", "native", "one", now, time.Second)
			if err != nil || !won {
				t.Fatalf("first claim = %v, %v", won, err)
			}
			won, _, err = ledger.Claim("ntf-lease", "native", "two", now.Add(2*time.Second), time.Second)
			if err != nil || !won {
				t.Fatalf("expired lease claim = %v, %v", won, err)
			}
			if err := ledger.Complete("ntf-lease", "native", Receipt{Owner: "two", Provider: "fake", Succeeded: true, CompletedAt: now.Add(2 * time.Second)}); err != nil {
				t.Fatal(err)
			}
			won, reason, err := ledger.Claim("ntf-lease", "native", "three", now.Add(3*time.Second), time.Second)
			if err != nil || won || reason != ClaimAlreadyClaimed {
				t.Fatalf("completed receipt replayed: won=%v reason=%q err=%v", won, reason, err)
			}
			entries, err := ledger.List("ntf-lease")
			if err != nil || len(entries) != 1 || entries[0].Receipt == nil || !entries[0].Receipt.Succeeded {
				t.Fatalf("receipt list = %+v, %v", entries, err)
			}
		})
	}
}

func TestJSONLLedgerSkipsUnreadableLineAndCompactsRetention(t *testing.T) {
	ledger := mustOpenLedger(t)
	now := time.Now().UTC()
	if won, _, err := ledger.Claim("ntf-old", "sound", "owner", now, time.Minute); err != nil || !won {
		t.Fatalf("claim = %v, %v", won, err)
	}
	if err := ledger.Complete("ntf-old", "sound", Receipt{Owner: "owner", Succeeded: false, Error: "fake failure", CompletedAt: now}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(ledger.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{broken\n")
	_ = f.Close()
	removed, err := ledger.ReleaseExpired(now.Add(ReceiptRetention + time.Second))
	if err != nil || removed != 1 {
		t.Fatalf("release = %d, %v", removed, err)
	}
	entries, err := ledger.List("")
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
}

func TestNativeOldDismissAfterReplacementDoesNotRemoveNewGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	one := mustOpenLedgerPath(t, path)
	two := mustOpenLedgerPath(t, path)
	now := time.Now().UTC()
	current := ""
	if result := one.DeliverNative("wait-old", "sidecar-group", "one", now, time.Minute, func() (ProviderReceipt, error) {
		current = "wait-old"
		return ProviderReceipt{Provider: "fake", Delivered: true, At: now}, nil
	}); result.Err != nil || !result.Receipt.Delivered {
		t.Fatalf("old delivery = %+v", result)
	}
	if result := two.DeliverNative("done-new", "sidecar-group", "two", now.Add(time.Second), time.Minute, func() (ProviderReceipt, error) {
		current = "done-new"
		return ProviderReceipt{Provider: "fake", Delivered: true, At: now.Add(time.Second)}, nil
	}); result.Err != nil || !result.Receipt.Delivered {
		t.Fatalf("replacement delivery = %+v", result)
	}
	removed := 0
	cancel := one.Cancel("wait-old", "sidecar-group", "one", now.Add(2*time.Second), func() error {
		removed++
		current = ""
		return nil
	})
	if cancel.Err != nil || cancel.Removed || removed != 0 || current != "done-new" {
		t.Fatalf("cancel=%+v removed=%d current=%q", cancel, removed, current)
	}
}

func TestNativeRemovalBeforeReplacementAllowsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	one := mustOpenLedgerPath(t, path)
	two := mustOpenLedgerPath(t, path)
	now := time.Now().UTC()
	current := ""
	one.DeliverNative("wait-old", "sidecar-group", "one", now, time.Minute, func() (ProviderReceipt, error) {
		current = "wait-old"
		return ProviderReceipt{Provider: "fake", Delivered: true, At: now}, nil
	})
	cancel := two.Cancel("wait-old", "sidecar-group", "two", now.Add(time.Second), func() error {
		current = ""
		return nil
	})
	if cancel.Err != nil || !cancel.Removed || current != "" {
		t.Fatalf("cancel=%+v current=%q", cancel, current)
	}
	delivery := one.DeliverNative("done-new", "sidecar-group", "one", now.Add(2*time.Second), time.Minute, func() (ProviderReceipt, error) {
		current = "done-new"
		return ProviderReceipt{Provider: "fake", Delivered: true, At: now.Add(2 * time.Second)}, nil
	})
	if delivery.Err != nil || !delivery.Receipt.Delivered || current != "done-new" {
		t.Fatalf("replacement=%+v current=%q", delivery, current)
	}
}

func TestFailedNativeReplacementDoesNotBecomeCurrentGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	one := mustOpenLedgerPath(t, path)
	two := mustOpenLedgerPath(t, path)
	now := time.Now().UTC()
	current := ""
	if result := one.DeliverNative("wait-old", "sidecar-group", "one", now, time.Minute, func() (ProviderReceipt, error) {
		current = "wait-old"
		return ProviderReceipt{Provider: "fake", Delivered: true, At: now}, nil
	}); result.Err != nil || !result.Receipt.Delivered {
		t.Fatalf("old delivery = %+v", result)
	}
	failed := two.DeliverNative("failed-new", "sidecar-group", "two", now.Add(time.Second), time.Minute, func() (ProviderReceipt, error) {
		return ProviderReceipt{Provider: "fake", Delivered: false, At: now.Add(time.Second)}, fmt.Errorf("provider failed")
	})
	if failed.Err == nil || failed.Receipt.Delivered {
		t.Fatalf("failed replacement = %+v", failed)
	}
	removed := 0
	cancel := one.Cancel("failed-new", "sidecar-group", "one", now.Add(2*time.Second), func() error {
		removed++
		current = ""
		return nil
	})
	if cancel.Err != nil || cancel.Removed || removed != 0 || current != "wait-old" {
		t.Fatalf("cancel=%+v removed=%d current=%q", cancel, removed, current)
	}
}

func TestNativeCurrentGroupSurvivesReceiptRetentionUntilCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	old := time.Now().UTC().Add(-ReceiptRetention - time.Hour)
	ledger := mustOpenLedgerPath(t, path)
	current := ""
	if result := ledger.DeliverNative("sticky-old", "sidecar-group", "one", old, time.Minute, func() (ProviderReceipt, error) {
		current = "sticky-old"
		return ProviderReceipt{Provider: "fake", Delivered: true, At: old}, nil
	}); result.Err != nil || !result.Receipt.Delivered {
		t.Fatalf("delivery = %+v", result)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}

	// Opening after receipt retention compacts historical attempts, but the
	// still-visible provider group must remain removable.
	reopened := mustOpenLedgerPath(t, path)
	entries, err := reopened.List("sticky-old")
	if err != nil || len(entries) != 0 {
		t.Fatalf("expired receipt history = %+v, %v", entries, err)
	}
	replayed := 0
	redelivery := reopened.DeliverNative("sticky-old", "sidecar-group", "two", time.Now().UTC(), time.Minute, func() (ProviderReceipt, error) {
		replayed++
		return ProviderReceipt{Provider: "fake", Delivered: true}, nil
	})
	if redelivery.Reason != ClaimAlreadyClaimed || redelivery.Attempted || replayed != 0 {
		t.Fatalf("current group replayed after receipt compaction: %+v calls=%d", redelivery, replayed)
	}
	removed := 0
	cancel := reopened.Cancel("sticky-old", "sidecar-group", "two", time.Now().UTC(), func() error {
		removed++
		current = ""
		return nil
	})
	if cancel.Err != nil || !cancel.Removed || removed != 1 || current != "" {
		t.Fatalf("cancel=%+v removed=%d current=%q", cancel, removed, current)
	}

	state := mustOpenLedgerPath(t, path)
	if len(state.groups) != 0 {
		t.Fatalf("cancelled current groups = %+v", state.groups)
	}
}

func TestNativeCancellationIsDurableBeforeFutureDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	one := mustOpenLedgerPath(t, path)
	two := mustOpenLedgerPath(t, path)
	now := time.Now().UTC()
	if result := one.Cancel("dismissed", "sidecar-group", "one", now, nil); result.Err != nil {
		t.Fatal(result.Err)
	}
	invoked := 0
	result := two.DeliverNative("dismissed", "sidecar-group", "two", now.Add(time.Second), time.Minute, func() (ProviderReceipt, error) {
		invoked++
		return ProviderReceipt{Delivered: true}, nil
	})
	if result.Reason != ClaimCancelled || result.Attempted || invoked != 0 {
		t.Fatalf("delivery=%+v invoked=%d", result, invoked)
	}
	if won, reason, err := two.Claim("dismissed", ChannelSound, "two", now.Add(time.Second), time.Minute); err != nil || won || reason != ClaimCancelled {
		t.Fatalf("sound claim after cancel won=%v reason=%q err=%v", won, reason, err)
	}
}

func TestNativeInFlightDismissAndReplacementEndWithReplacementVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	one := mustOpenLedgerPath(t, path)
	two := mustOpenLedgerPath(t, path)
	three := mustOpenLedgerPath(t, path)
	now := time.Now().UTC()
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	current := ""
	oldDone := make(chan NativeOperationResult, 1)
	go func() {
		oldDone <- one.DeliverNative("wait-old", "sidecar-group", "one", now, time.Minute, func() (ProviderReceipt, error) {
			close(started)
			<-release
			mu.Lock()
			current = "wait-old"
			mu.Unlock()
			return ProviderReceipt{Provider: "fake", Delivered: true, At: now}, nil
		})
	}()
	<-started
	cancelDone := make(chan NativeCancelResult, 1)
	go func() {
		cancelDone <- two.Cancel("wait-old", "sidecar-group", "two", now.Add(time.Second), func() error {
			mu.Lock()
			current = ""
			mu.Unlock()
			return nil
		})
	}()
	newDone := make(chan NativeOperationResult, 1)
	go func() {
		newDone <- three.DeliverNative("done-new", "sidecar-group", "three", now.Add(2*time.Second), time.Minute, func() (ProviderReceipt, error) {
			mu.Lock()
			current = "done-new"
			mu.Unlock()
			return ProviderReceipt{Provider: "fake", Delivered: true, At: now.Add(2 * time.Second)}, nil
		})
	}()
	close(release)
	if result := <-oldDone; result.Err != nil {
		t.Fatalf("old delivery: %+v", result)
	}
	if result := <-cancelDone; result.Err != nil {
		t.Fatalf("cancel: %+v", result)
	}
	if result := <-newDone; result.Err != nil || !result.Receipt.Delivered {
		t.Fatalf("new delivery: %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if current != "done-new" {
		t.Fatalf("final provider group = %q", current)
	}
}

func mustOpenLedger(t *testing.T) *JSONLLedger {
	t.Helper()
	ledger, err := OpenPath(filepath.Join(t.TempDir(), LedgerFileName))
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

func mustOpenLedgerPath(t *testing.T, path string) *JSONLLedger {
	t.Helper()
	ledger, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}
