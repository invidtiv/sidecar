package conversations

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/plugin"
)

type reliabilityAdapter struct {
	mu            sync.Mutex
	paths         []string
	sessions      func(string) ([]adapter.Session, error)
	entered       chan struct{}
	release       chan struct{}
	concurrent    int
	maxConcurrent int
	watchCalls    int
	watchEvents   chan adapter.Event
	watchClose    sync.Once
	detected      bool
	discovery     bool
	pathIDs       map[string]string
	targeted      func(string) (*adapter.Session, error)
	targetEntered chan struct{}
	targetRelease chan struct{}
	targetCalls   int
}

func (a *reliabilityAdapter) ID() string                                 { return "reliable" }
func (a *reliabilityAdapter) Name() string                               { return "Reliable" }
func (a *reliabilityAdapter) Icon() string                               { return "R" }
func (a *reliabilityAdapter) Detect(string) (bool, error)                { return a.detected, nil }
func (a *reliabilityAdapter) Capabilities() adapter.CapabilitySet        { return nil }
func (a *reliabilityAdapter) Messages(string) ([]adapter.Message, error) { return nil, nil }
func (a *reliabilityAdapter) Usage(string) (*adapter.UsageStats, error)  { return nil, nil }
func (a *reliabilityAdapter) WatchScope() adapter.WatchScope             { return adapter.WatchScopeGlobal }
func (a *reliabilityAdapter) WatchForProjectDiscovery() bool             { return a.discovery }
func (a *reliabilityAdapter) SessionIDFromPath(path string) (string, error) {
	if id := a.pathIDs[path]; id != "" {
		return id, nil
	}
	return "", errors.New("unknown session path")
}

func (a *reliabilityAdapter) SessionByID(id string) (*adapter.Session, error) {
	a.mu.Lock()
	a.targetCalls++
	a.mu.Unlock()
	if a.targetEntered != nil {
		select {
		case a.targetEntered <- struct{}{}:
		default:
		}
	}
	if a.targetRelease != nil {
		<-a.targetRelease
	}
	if a.targeted == nil {
		return nil, errors.New("not found")
	}
	return a.targeted(id)
}

func (a *reliabilityAdapter) Sessions(path string) ([]adapter.Session, error) {
	a.mu.Lock()
	a.paths = append(a.paths, path)
	a.concurrent++
	if a.concurrent > a.maxConcurrent {
		a.maxConcurrent = a.concurrent
	}
	a.mu.Unlock()
	if a.entered != nil {
		select {
		case a.entered <- struct{}{}:
		default:
		}
	}
	if a.release != nil {
		<-a.release
	}
	var sessions []adapter.Session
	var err error
	if a.sessions != nil {
		sessions, err = a.sessions(path)
	}
	a.mu.Lock()
	a.concurrent--
	a.mu.Unlock()
	return sessions, err
}

func (a *reliabilityAdapter) Watch(string) (<-chan adapter.Event, io.Closer, error) {
	a.mu.Lock()
	a.watchCalls++
	a.mu.Unlock()
	if a.watchEvents == nil {
		a.watchEvents = make(chan adapter.Event)
	}
	return a.watchEvents, closerFunc(func() error {
		a.watchClose.Do(func() { close(a.watchEvents) })
		return nil
	}), nil
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func TestLoadSessionsSlowCallRemainsVisibleAndEventuallyCompletes(t *testing.T) {
	oldDelay := adapterSlowLoadNoticeDelay
	adapterSlowLoadNoticeDelay = 10 * time.Millisecond
	defer func() { adapterSlowLoadNoticeDelay = oldDelay }()

	workDir := t.TempDir()
	release := make(chan struct{})
	a := &reliabilityAdapter{
		entered: make(chan struct{}, 1),
		release: release,
		sessions: func(string) ([]adapter.Session, error) {
			return []adapter.Session{{ID: "real-thread", UpdatedAt: time.Now()}}, nil
		},
	}
	p := New()
	p.ctx = &plugin.Context{WorkDir: workDir, Epoch: 7}
	p.adapters = map[string]adapter.Adapter{"reliable": a}

	if _, ok := p.loadSessions()().(LoadingStartedMsg); !ok {
		t.Fatal("loadSessions did not return immediately with LoadingStartedMsg")
	}
	select {
	case <-a.entered:
	case <-time.After(time.Second):
		t.Fatal("Sessions was not started")
	}

	select {
	case batch := <-p.adapterBatchChan:
		if len(batch.Notices) == 0 || batch.Final {
			t.Fatalf("expected a non-final slow-load notice, got %+v", batch)
		}
		p.loadingMu.Lock()
		loading := p.loadingSessions
		p.loadingMu.Unlock()
		if !loading {
			t.Fatal("slow adapter was incorrectly marked complete")
		}
	case <-time.After(time.Second):
		t.Fatal("slow adapter did not report visible loading status")
	}

	close(release)
	var gotSession, gotFinal bool
	deadline := time.After(time.Second)
	for !gotFinal {
		select {
		case batch := <-p.adapterBatchChan:
			gotSession = gotSession || len(batch.Sessions) == 1 && batch.Sessions[0].ID == "real-thread"
			gotFinal = batch.Final
		case <-deadline:
			t.Fatal("slow adapter never completed")
		}
	}
	if !gotSession {
		t.Fatal("slow adapter result was discarded")
	}
}

func TestLoadSessionsSurfacesAdapterErrors(t *testing.T) {
	a := &reliabilityAdapter{sessions: func(string) ([]adapter.Session, error) {
		return nil, errors.New("index unavailable")
	}}
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), Epoch: 2}
	p.adapters = map[string]adapter.Adapter{"reliable": a}
	_ = p.loadSessions()()

	var sawError, sawFinal bool
	deadline := time.After(time.Second)
	for !sawFinal {
		select {
		case batch := <-p.adapterBatchChan:
			for _, notice := range batch.Notices {
				if notice != "" {
					sawError = true
				}
			}
			sawFinal = batch.Final
		case <-deadline:
			t.Fatal("load did not finish")
		}
	}
	if !sawError {
		t.Fatal("adapter error was silently discarded")
	}
}

func TestGlobalAdapterLoadsEveryRelatedPathSerially(t *testing.T) {
	root := t.TempDir()
	paths := []string{root, filepath.Join(root, "worktree"), filepath.Join(root, "deleted")}
	a := &reliabilityAdapter{sessions: func(path string) ([]adapter.Session, error) {
		return []adapter.Session{{ID: path, UpdatedAt: time.Now()}}, nil
	}}
	p := New()
	p.ctx = &plugin.Context{WorkDir: root, Epoch: 3}
	p.adapters = map[string]adapter.Adapter{"reliable": a}
	p.cachedWorktreePaths = paths
	p.cachedWorktreeNames = map[string]string{}
	p.worktreeCacheTime = time.Now()
	_ = p.loadSessions()()

	for {
		select {
		case batch := <-p.adapterBatchChan:
			if batch.Final {
				a.mu.Lock()
				defer a.mu.Unlock()
				if len(a.paths) != len(paths) {
					t.Fatalf("global adapter loaded %d paths, want %d: %v", len(a.paths), len(paths), a.paths)
				}
				if a.maxConcurrent != 1 {
					t.Fatalf("global adapter Sessions calls overlapped: max=%d", a.maxConcurrent)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("load did not finish")
		}
	}
}

func TestStartWatcherReusesLoadedSessionsAndStartsOneGlobalWatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &reliabilityAdapter{pathIDs: map[string]string{path: "real-thread"}}
	p := New()
	p.ctx = &plugin.Context{WorkDir: dir, Epoch: 9}
	p.adapters = map[string]adapter.Adapter{"reliable": a}
	p.sessions = []adapter.Session{{ID: "real-thread", AdapterID: "reliable", Path: path, UpdatedAt: time.Now(), IsActive: true}}
	p.cachedWorktreePaths = []string{dir, filepath.Join(dir, "other")}

	msg, ok := p.startWatcher()().(WatchStartedMsg)
	if !ok {
		t.Fatal("startWatcher did not return WatchStartedMsg")
	}
	a.mu.Lock()
	watchCalls, sessionCalls := a.watchCalls, len(a.paths)
	a.mu.Unlock()
	if watchCalls != 1 {
		t.Fatalf("global Watch called %d times, want 1", watchCalls)
	}
	if sessionCalls != 0 {
		t.Fatalf("watcher setup rescanned Sessions %d times", sessionCalls)
	}
	if id, err := adapter.ResolveSessionID(a, path); err != nil || id != "real-thread" {
		t.Fatalf("adapter-correct path resolution = %q, %v", id, err)
	}
	msg.Cancel()
	_ = msg.Manager.Close()
}

func TestDetectRetainsGlobalAdapterForFirstProjectSession(t *testing.T) {
	a := &reliabilityAdapter{detected: false, discovery: true}
	p := New()
	p.ctx = &plugin.Context{ProjectRoot: t.TempDir(), Epoch: 11, Adapters: map[string]adapter.Adapter{"reliable": a}}
	msg := p.detectAdapters()().(AdaptersDetectedMsg)
	if msg.Adapters["reliable"] == nil || !msg.DiscoveryOnly["reliable"] {
		t.Fatalf("discovery watcher adapter was omitted: %+v", msg)
	}
}

func TestGlobalForeignProjectEventCannotTargetedMerge(t *testing.T) {
	projectA := t.TempDir()
	projectB := t.TempDir()
	foreign := &adapter.Session{ID: "project-b-thread", AdapterID: "reliable", WorktreePath: projectB}
	a := &reliabilityAdapter{targeted: func(id string) (*adapter.Session, error) {
		if id == foreign.ID {
			return foreign, nil
		}
		return nil, errors.New("not found")
	}}
	p := New()
	p.ctx = &plugin.Context{WorkDir: projectA, ProjectRoot: projectA, Epoch: 12}
	p.adapters = map[string]adapter.Adapter{"reliable": a}
	p.sessions = []adapter.Session{{ID: "project-a-thread", AdapterID: "reliable", WorktreePath: projectA}}
	p.coalescer = NewEventCoalescer(5*time.Millisecond, p.coalesceChan)

	if !p.globalEventNeedsProjectRefresh("reliable", foreign.ID) {
		t.Fatal("unknown global session should require a project-filtered full refresh")
	}
	p.Update(WatchEventMsg{Epoch: 12, AdapterID: "reliable", SessionID: foreign.ID})
	select {
	case msg := <-p.coalesceChan:
		if !msg.RefreshAll || len(msg.SessionIDs) != 0 {
			t.Fatalf("foreign global event was not converted to full refresh: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("foreign global event was not coalesced")
	}
	if msg := p.refreshSessions([]string{foreign.ID})(); msg != nil {
		t.Fatalf("foreign session was targeted-merged: %+v", msg)
	}
	a.mu.Lock()
	targetCalls := a.targetCalls
	a.mu.Unlock()
	if targetCalls != 0 {
		t.Fatalf("SessionByID called %d times for a foreign/unknown session", targetCalls)
	}
}

func TestRefreshGateWaitCancelsOnStop(t *testing.T) {
	p := New()
	release, ok := p.acquireSessionCall(context.Background(), "reliable")
	if !ok {
		t.Fatal("failed to occupy adapter gate")
	}
	defer release()

	result := make(chan bool, 1)
	go func(ctx context.Context) {
		_, acquired := p.acquireSessionCall(ctx, "reliable")
		result <- acquired
	}(p.refreshCtx)

	p.Stop()
	select {
	case acquired := <-result:
		if acquired {
			t.Fatal("canceled project refresh acquired the stale adapter gate")
		}
	case <-time.After(time.Second):
		t.Fatal("adapter gate waiter did not cancel on Stop")
	}
}

func TestRepeatedRefreshesDoNotAccumulateBehindHungCall(t *testing.T) {
	targetRelease := make(chan struct{})
	a := &reliabilityAdapter{
		targetEntered: make(chan struct{}, 1),
		targetRelease: targetRelease,
		targeted: func(id string) (*adapter.Session, error) {
			return &adapter.Session{ID: id, AdapterID: "reliable"}, nil
		},
	}
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), Epoch: 13}
	p.adapters = map[string]adapter.Adapter{"reliable": a}
	p.sessions = []adapter.Session{{ID: "known", AdapterID: "reliable"}}

	firstDone := make(chan tea.Msg, 1)
	go func() { firstDone <- p.refreshSessions([]string{"known"})() }()
	select {
	case <-a.targetEntered:
	case <-time.After(time.Second):
		t.Fatal("first targeted refresh did not start")
	}

	const repeats = 50
	for i := 0; i < repeats; i++ {
		if msg := p.refreshSessions([]string{"known"})(); msg != nil {
			t.Fatalf("queued refresh %d should return immediately without work, got %T", i, msg)
		}
	}
	a.mu.Lock()
	targetCalls := a.targetCalls
	a.mu.Unlock()
	if targetCalls != 1 {
		t.Fatalf("hung adapter accumulated calls: got %d, want 1", targetCalls)
	}

	p.Stop()
	close(targetRelease)
	select {
	case msg := <-firstDone:
		if msg != nil {
			t.Fatalf("stale in-flight refresh published after Stop: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight refresh did not unwind after adapter returned")
	}
}

func TestDeriveWorktreeNameFromPath(t *testing.T) {
	tests := []struct {
		name     string
		wtPath   string
		mainPath string
		want     string
	}{
		{
			name:     "standard prefixed path",
			wtPath:   "/Users/foo/code/myrepo-feature-auth",
			mainPath: "/Users/foo/code/myrepo",
			want:     "feature-auth",
		},
		{
			name:     "path without prefix",
			wtPath:   "/Users/foo/code/some-other-dir",
			mainPath: "/Users/foo/code/myrepo",
			want:     "some-other-dir",
		},
		{
			name:     "repo name with hyphen",
			wtPath:   "/Users/foo/code/my-repo-feature",
			mainPath: "/Users/foo/code/my-repo",
			want:     "feature",
		},
		{
			name:     "nested paths",
			wtPath:   "/a/b/c/repo-branch",
			mainPath: "/a/b/c/repo",
			want:     "branch",
		},
		{
			name:     "same directory",
			wtPath:   "/Users/foo/code/myrepo",
			mainPath: "/Users/foo/code/myrepo",
			want:     "myrepo",
		},
		{
			name:     "multi-part branch name",
			wtPath:   "/code/sidecar-fix-bug-123",
			mainPath: "/code/sidecar",
			want:     "fix-bug-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveWorktreeNameFromPath(tt.wtPath, tt.mainPath)
			if got != tt.want {
				t.Errorf("deriveWorktreeNameFromPath(%q, %q) = %q, want %q",
					tt.wtPath, tt.mainPath, got, tt.want)
			}
		})
	}
}

func TestSessionLoadGuard_PreventsDuplicateInFlight(t *testing.T) {
	p := New()

	token, ok := p.beginSessionLoad("codex", "/tmp/repo")
	if !ok {
		t.Fatal("first beginSessionLoad should succeed")
	}
	if token == 0 {
		t.Fatal("token should be non-zero")
	}

	if _, ok := p.beginSessionLoad("codex", "/tmp/repo"); ok {
		t.Fatal("duplicate in-flight load should be rejected")
	}

	p.endSessionLoad("codex", "/tmp/repo", token)

	if _, ok := p.beginSessionLoad("codex", "/tmp/repo"); !ok {
		t.Fatal("beginSessionLoad should succeed after endSessionLoad")
	}
}

func TestSessionLoadGuard_IgnoresStaleTokenOnEnd(t *testing.T) {
	p := New()

	oldToken, ok := p.beginSessionLoad("cursor", "/tmp/repo")
	if !ok {
		t.Fatal("initial beginSessionLoad should succeed")
	}

	// Simulate project reset replacing the in-flight map while an old goroutine is still running.
	p.sessionLoadMu.Lock()
	p.sessionLoads = make(map[string]uint64)
	p.sessionLoadMu.Unlock()

	newToken, ok := p.beginSessionLoad("cursor", "/tmp/repo")
	if !ok {
		t.Fatal("new beginSessionLoad should succeed after reset")
	}
	if newToken == oldToken {
		t.Fatal("session load tokens must remain unique across resets")
	}

	// Old goroutine completion should not clear the newer in-flight entry.
	p.endSessionLoad("cursor", "/tmp/repo", oldToken)

	if _, ok := p.beginSessionLoad("cursor", "/tmp/repo"); ok {
		t.Fatal("stale token must not clear current in-flight load")
	}

	p.endSessionLoad("cursor", "/tmp/repo", newToken)
	if _, ok := p.beginSessionLoad("cursor", "/tmp/repo"); !ok {
		t.Fatal("beginSessionLoad should succeed after valid endSessionLoad")
	}
}
