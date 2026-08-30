package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *JSONLStore {
	t.Helper()
	s, err := OpenPath(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	return s
}

func TestPostRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	s, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	posted, err := s.Post(Notification{
		Source: SourceAgent,
		Title:  "Agent finished",
		Body:   "review the diff",
		Origin: Origin{TmuxSession: "sidecar-1"},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if posted.ID == "" || posted.CreatedAt.IsZero() || posted.Severity != SeverityInfo {
		t.Fatalf("Post did not normalize: %+v", posted)
	}
	if posted.ExpiresAt == nil {
		t.Fatalf("agent notification should take the source's default expiry")
	}

	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	all, err := reopened.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 notification after reopen, got %d", len(all))
	}
	got := all[0]
	if got.ID != posted.ID || got.Title != "Agent finished" || got.Body != "review the diff" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Origin.TmuxSession != "sidecar-1" {
		t.Fatalf("round trip lost origin: %+v", got.Origin)
	}
	if got.Read() || got.Dismissed() {
		t.Fatalf("a fresh notification is unread and undismissed: %+v", got)
	}
}

func TestPostIsIdempotentByID(t *testing.T) {
	s := testStore(t)
	n := Notification{ID: "ntf-fixed", Source: SourceSystem, Title: "once"}
	first, err := s.Post(n)
	if err != nil {
		t.Fatalf("first post: %v", err)
	}
	if !first.Created || first.Reason != PostCreated {
		t.Fatalf("first result = %+v", first)
	}
	n.Title = "twice"
	again, err := s.Post(n)
	if err != nil {
		t.Fatalf("second post: %v", err)
	}
	if again.Title != "once" {
		t.Fatalf("re-posting an id must return the stored record, got %q", again.Title)
	}
	if again.Created || again.Reason != PostExistingID {
		t.Fatalf("idempotent result = %+v", again)
	}
	all, _ := s.List()
	if len(all) != 1 {
		t.Fatalf("expected 1 record, got %d", len(all))
	}
}

func TestLogicalTransitionDedupeAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	one, _ := OpenPath(path)
	two, _ := OpenPath(path)
	now := time.Now().UTC()
	base := Notification{Source: SourceSession, Severity: SeverityInfo, CreatedAt: now, Title: "Shell finished", Transition: &TransitionMetadata{Class: TransitionDone, LaneKey: "shell:a", DedupeKey: "origin-a:done"}}
	first, err := one.Post(base)
	if err != nil || !first.Created {
		t.Fatalf("first post = %+v, %v", first, err)
	}
	base.ID = "different-id"
	second, err := two.Post(base)
	if err != nil || second.Created || second.Reason != PostExistingLogical || second.ID != first.ID {
		t.Fatalf("logical duplicate = %+v, %v", second, err)
	}
	base.ID = "later-turn"
	base.CreatedAt = now.Add(LogicalDedupeWindow + time.Second)
	later, err := two.Post(base)
	if err != nil || !later.Created {
		t.Fatalf("later logical event = %+v, %v", later, err)
	}
}

func TestReadAndDismissSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	keep, _ := s.Post(Notification{Source: SourceTD, Title: "keep"})
	gone, _ := s.Post(Notification{Source: SourceTD, Title: "gone"})
	if err := s.MarkRead(keep.ID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if err := s.Dismiss(gone.ID); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if err := s.Dismiss("nope"); err == nil {
		t.Fatalf("dismissing an unknown id should fail")
	}

	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	all, _ := reopened.List()
	if len(all) != 2 {
		t.Fatalf("dismissed items stay for the retention window, got %d", len(all))
	}
	for _, n := range all {
		switch n.ID {
		case keep.ID:
			if !n.Read() || n.Dismissed() {
				t.Fatalf("keep should be read and undismissed: %+v", n)
			}
		case gone.ID:
			if !n.Dismissed() || !n.Read() {
				t.Fatalf("dismissed implies read: %+v", n)
			}
		}
	}
	if UnreadCount(all) != 0 {
		t.Fatalf("nothing should be unread, got %d", UnreadCount(all))
	}
}

func TestCompactionOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	a, _ := s.Post(Notification{Source: SourceSystem, Title: "a"})
	b, _ := s.Post(Notification{Source: SourceSystem, Title: "b"})
	_ = s.MarkRead(a.ID)
	_ = s.Dismiss(b.ID)

	if lines := countLines(t, path); lines != 4 {
		t.Fatalf("expected 4 appended events before compaction, got %d", lines)
	}

	if _, err := OpenPath(path); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if lines := countLines(t, path); lines != 2 {
		t.Fatalf("compaction should leave one line per record, got %d", lines)
	}

	// And the folded state must survive the rewrite.
	reopened, _ := OpenPath(path)
	all, _ := reopened.List()
	if len(all) != 2 {
		t.Fatalf("expected 2 records after compaction, got %d", len(all))
	}
	for _, n := range all {
		if n.ID == a.ID && !n.Read() {
			t.Fatalf("compaction lost the read flag")
		}
		if n.ID == b.ID && !n.Dismissed() {
			t.Fatalf("compaction lost the dismissed flag")
		}
	}
}

func TestRetentionDropsOldDismissed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Hour)
	stale := Notification{ID: "ntf-stale", Source: SourceSystem, Title: "stale", CreatedAt: old, DismissedAt: &old, ReadAt: &old}
	fresh := Notification{ID: "ntf-fresh", Source: SourceSystem, Title: "fresh", CreatedAt: recent, DismissedAt: &recent, ReadAt: &recent}
	live := Notification{ID: "ntf-live", Source: SourceSystem, Title: "live", CreatedAt: old}
	writeEvents(t, path, stale, fresh, live)

	s, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	all, _ := s.List()
	ids := map[string]bool{}
	for _, n := range all {
		ids[n.ID] = true
	}
	if ids["ntf-stale"] {
		t.Fatalf("a notification dismissed 48h ago must be compacted away")
	}
	if !ids["ntf-fresh"] || !ids["ntf-live"] {
		t.Fatalf("retention dropped too much: %v", ids)
	}
	if lines := countLines(t, path); lines != 2 {
		t.Fatalf("retention should have rewritten the file to 2 lines, got %d", lines)
	}
}

func TestSweepRemovesExpiredDismissed(t *testing.T) {
	s := testStore(t)
	n, _ := s.Post(Notification{Source: SourceSystem, Title: "x"})
	if err := s.Dismiss(n.ID); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if removed, err := s.Sweep(time.Now()); err != nil || removed != 0 {
		t.Fatalf("Sweep(now) = %d, %v; want 0, nil", removed, err)
	}
	removed, err := s.Sweep(time.Now().Add(Retention + time.Minute))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 swept, got %d", removed)
	}
	all, _ := s.List()
	if len(all) != 0 {
		t.Fatalf("expected empty store, got %d", len(all))
	}
}

func TestReadAllDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, _ := OpenPath(path)
	n, _ := s.Post(Notification{Source: SourceTasks, Title: "due"})
	_ = s.MarkRead(n.ID)

	before := countLines(t, path)
	all, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(all) != 1 || !all[0].Read() {
		t.Fatalf("ReadAll folded wrong: %+v", all)
	}
	if after := countLines(t, path); after != before {
		t.Fatalf("ReadAll must not rewrite the log (%d -> %d)", before, after)
	}
	if missing, err := ReadAll(filepath.Join(t.TempDir(), "absent.jsonl")); err != nil || missing != nil {
		t.Fatalf("ReadAll on a missing file = %v, %v; want nil, nil", missing, err)
	}
}

func TestMalformedLineIsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, _ := OpenPath(path)
	if _, err := s.Post(Notification{Source: SourceSystem, Title: "good"}); err != nil {
		t.Fatalf("Post: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()

	all, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(all) != 1 || all[0].Title != "good" {
		t.Fatalf("a bad line must not cost the log: %+v", all)
	}
}

func TestMemStoreSatisfiesTheSameContract(t *testing.T) {
	s := NewMemStore()
	n, err := s.Post(Notification{Source: SourceAgent, Title: "mem"})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if err := s.Dismiss(n.ID); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	all, _ := s.List()
	if len(all) != 1 || !all[0].Dismissed() {
		t.Fatalf("unexpected state: %+v", all)
	}
	if removed, _ := s.Sweep(time.Now().Add(Retention + time.Minute)); removed != 1 {
		t.Fatalf("expected retention sweep to remove 1, got %d", removed)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

func writeEvents(t *testing.T, path string, ns ...Notification) {
	t.Helper()
	var b strings.Builder
	for i := range ns {
		rec := ns[i]
		data, err := json.Marshal(event{Event: eventPosted, At: rec.CreatedAt, ID: rec.ID, Notification: &rec})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestConcurrentStoresDoNotClobber reproduces the loss that folding once at
// Open and re-emitting only memory used to cause: the CLI's direct append is
// invisible to a running TUI, and the TUI's next pruning sweep deletes it.
func TestConcurrentStoresDoNotClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	tui, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath (tui): %v", err)
	}
	// Something old and dismissed, so the sweep below actually prunes and
	// therefore actually rewrites the file.
	stale, err := tui.Post(Notification{Source: SourceSystem, Title: "stale"})
	if err != nil {
		t.Fatalf("Post stale: %v", err)
	}
	if err := tui.Dismiss(stale.ID); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	// A second process — `sidecar notify post` with no instance listening —
	// appends straight to the same log.
	cli, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath (cli): %v", err)
	}
	posted, err := cli.Post(Notification{Source: SourceAgent, Title: "from the CLI"})
	if err != nil {
		t.Fatalf("Post from cli: %v", err)
	}

	// The TUI's 1s heartbeat sweeps, which prunes the stale record and rewrites.
	if _, err := tui.Sweep(time.Now().Add(2 * Retention)); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	for name, store := range map[string]*JSONLStore{"tui": tui, "cli": cli} {
		all, err := store.List()
		if err != nil {
			t.Fatalf("List (%s): %v", name, err)
		}
		if name == "cli" {
			// The CLI store has not re-read; the file is the assertion that
			// matters for it.
			continue
		}
		if len(all) != 1 || all[0].ID != posted.ID {
			t.Fatalf("%s lost the CLI's notification: %+v", name, all)
		}
	}

	onDisk, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(onDisk) != 1 || onDisk[0].ID != posted.ID {
		t.Fatalf("sweep clobbered the log: %+v", onDisk)
	}
}
