package gitstatus

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestWatcherBufferedIndexEventUpgradesToHistory(t *testing.T) {
	w := &Watcher{events: make(chan WatchEvent, 1)}
	w.deliver(WatchEvent{History: false})
	w.deliver(WatchEvent{History: true})

	event := <-w.events
	if !event.History {
		t.Fatal("undrained index event lost the later history invalidation")
	}
	select {
	case extra := <-w.events:
		t.Fatalf("upgrade queued an extra event: %#v", extra)
	default:
	}
}

func initWatcherRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitTest(t, dir, "init")
	return dir
}

func watcherStopped(w *Watcher) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped
}

func TestWatcherReinitRejectsReversedStartsAndOldEvents(t *testing.T) {
	repoA, repoB := initWatcherRepo(t), initWatcherRepo(t)
	p := New()
	ctxA := &plugin.Context{Epoch: 1, WorkDir: repoA}
	if err := p.Init(ctxA); err != nil {
		t.Fatal(err)
	}
	p.activateRepo(repoA)
	startA := p.startWatcher()

	ctxB := &plugin.Context{Epoch: 2, WorkDir: repoB}
	if err := p.Init(ctxB); err != nil {
		t.Fatal(err)
	}
	p.activateRepo(repoB)
	startB := p.startWatcher()
	msgB := startB().(WatchStartedMsg)
	_, listenB := p.Update(msgB)
	if p.watcher != msgB.Watcher || listenB == nil {
		t.Fatal("current watcher was not installed")
	}

	msgA := startA().(WatchStartedMsg)
	_, cmd := p.Update(msgA)
	if cmd != nil || p.watcher != msgB.Watcher {
		t.Fatal("stale watcher replaced current watcher")
	}
	if !watcherStopped(msgA.Watcher) {
		t.Fatal("stale watcher was leaked")
	}

	p.statusRefreshDirty = false
	_, cmd = p.Update(WatchEventMsg{Epoch: 1, RepoRoot: repoA, Watcher: msgA.Watcher, History: true})
	if cmd != nil || p.statusRefreshDirty {
		t.Fatal("old repository event was accepted")
	}
	p.Stop()
}

func TestWatcherStartStopsSupersededWatcher(t *testing.T) {
	repo := initWatcherRepo(t)
	p := New()
	if err := p.Init(&plugin.Context{Epoch: 1, WorkDir: repo}); err != nil {
		t.Fatal(err)
	}
	p.activateRepo(repo)
	first := p.startWatcher()().(WatchStartedMsg)
	p.Update(first)
	second := p.startWatcher()().(WatchStartedMsg)
	p.Update(second)
	if !watcherStopped(first.Watcher) {
		t.Fatal("superseded watcher was not stopped")
	}
	if p.watcher != second.Watcher {
		t.Fatal("replacement watcher was not installed")
	}
	p.Stop()
}

func waitForWatchEvent(t *testing.T, watcher *Watcher, wantHistory bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-watcher.Events():
			if event.History == wantHistory {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for watcher event (history=%v)", wantHistory)
		}
	}
}

func TestWatcherSeesExternalStageAndCommitInNormalAndLinkedWorktrees(t *testing.T) {
	root := t.TempDir()
	runGitTest(t, root, "init")
	runGitTest(t, root, "config", "user.email", "sidecar@example.test")
	runGitTest(t, root, "config", "user.name", "Sidecar Test")
	if err := os.WriteFile(filepath.Join(root, "tracked"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "add", "tracked")
	runGitTest(t, root, "commit", "-m", "base")
	linked := filepath.Join(t.TempDir(), "linked")
	runGitTest(t, root, "worktree", "add", "-b", "watcher-linked", linked)

	for _, dir := range []string{root, linked} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			watcher, err := NewWatcher(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer watcher.Stop()
			if err := os.WriteFile(filepath.Join(dir, "external"), []byte("stage\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGitTest(t, dir, "add", "external")
			waitForWatchEvent(t, watcher, false)
			runGitTest(t, dir, "commit", "-m", "external commit")
			waitForWatchEvent(t, watcher, true)
		})
	}
}
