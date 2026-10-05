package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tty"
)

type shareHarness struct {
	catalog *sharedCatalog
	calls   atomic.Int32
	release chan struct{}
	fence   catalogFence
	fenceMu sync.Mutex
	now     time.Time
	nowMu   sync.Mutex
	fail    atomic.Bool
}

func newShareHarness() *shareHarness {
	h := &shareHarness{fence: catalogFence{configGeneration: "g1", tmuxServer: "s1"}, now: time.Unix(1000, 0)}
	h.catalog = newSharedCatalog(func() mobile.CatalogProvider {
		return func(ctx context.Context) (mobile.CatalogInput, error) {
			n := h.calls.Add(1)
			if h.release != nil {
				select {
				case <-h.release:
				case <-ctx.Done():
					return mobile.CatalogInput{}, ctx.Err()
				}
			}
			if h.fail.Load() {
				return mobile.CatalogInput{}, errors.New("collection failed")
			}
			return mobile.CatalogInput{ObservedAt: time.Unix(int64(n), 0)}, nil
		}
	}, nil)
	h.catalog.fence = func(context.Context) (catalogFence, error) {
		h.fenceMu.Lock()
		defer h.fenceMu.Unlock()
		return h.fence, nil
	}
	h.catalog.now = func() time.Time {
		h.nowMu.Lock()
		defer h.nowMu.Unlock()
		return h.now
	}
	return h
}

func (h *shareHarness) advance(d time.Duration) {
	h.nowMu.Lock()
	h.now = h.now.Add(d)
	h.nowMu.Unlock()
}

func (h *shareHarness) setFence(f catalogFence) {
	h.fenceMu.Lock()
	h.fence = f
	h.fenceMu.Unlock()
}

func (h *shareHarness) get(t *testing.T) int64 {
	t.Helper()
	input, err := h.catalog.Provider()(context.Background())
	if err != nil {
		t.Error(err) // callable from goroutines
		return -1
	}
	return input.ObservedAt.Unix()
}

// The pile-up that reported this machine offline: a page load and every
// connected events stream asked at once and each ran a full collection.
// Concurrent requests now share one.
func TestSharedCatalogCoalescesConcurrentRequests(t *testing.T) {
	h := newShareHarness()
	h.release = make(chan struct{})
	var wg sync.WaitGroup
	got := make([]int64, 12)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = h.get(t)
		}(i)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let every request join
	close(h.release)
	wg.Wait()
	if calls := h.calls.Load(); calls != 1 {
		t.Fatalf("%d collections for one burst, want 1", calls)
	}
	for _, v := range got {
		if v != 1 {
			t.Fatalf("requests saw different collections: %v", got)
		}
	}
}

func TestSharedCatalogReuseIsBoundedByAgeFenceAndInvalidation(t *testing.T) {
	h := newShareHarness()
	first, second := h.get(t), h.get(t)
	if first != 1 || second != 1 {
		t.Fatalf("a fresh collection was not reused (%d, %d)", first, second)
	}
	h.advance(sharedCatalogMaxAge)
	if got := h.get(t); got != 2 {
		t.Fatalf("collection older than max age reused (got %d)", got)
	}
	h.setFence(catalogFence{configGeneration: "g2", tmuxServer: "s1"})
	if got := h.get(t); got != 3 {
		t.Fatalf("collection from another config generation reused (got %d)", got)
	}
	h.setFence(catalogFence{configGeneration: "g2", tmuxServer: "s2"})
	if got := h.get(t); got != 4 {
		t.Fatalf("collection from another tmux server reused (got %d)", got)
	}
	h.catalog.Invalidate()
	if got := h.get(t); got != 5 {
		t.Fatalf("collection reused across an invalidation (got %d)", got)
	}
	if got := h.get(t); got != 5 {
		t.Fatalf("collection after invalidation not reused (got %d)", got)
	}
}

// A request that arrives after an invalidation never joins a collection that
// started before it, even one still running.
func TestSharedCatalogInvalidationDetachesInFlightCollection(t *testing.T) {
	h := newShareHarness()
	h.release = make(chan struct{})
	first := make(chan int64, 1)
	go func() { first <- h.get(t) }()
	for h.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	h.catalog.Invalidate()
	second := make(chan int64, 1)
	go func() { second <- h.get(t) }()
	for h.calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	close(h.release)
	if a, b := <-first, <-second; a == b {
		t.Fatalf("post-invalidation request joined the earlier collection (%d, %d)", a, b)
	}
}

func TestSharedCatalogNeverReusesAFailure(t *testing.T) {
	h := newShareHarness()
	h.fail.Store(true)
	if _, err := h.catalog.Provider()(context.Background()); err == nil {
		t.Fatal("failure not reported")
	}
	h.fail.Store(false)
	if got := h.get(t); got != 2 {
		t.Fatalf("failed collection reused (got %d)", got)
	}
}

// The request that started a collection can leave; the others still get it.
func TestSharedCatalogSurvivesTheStartingRequestLeaving(t *testing.T) {
	h := newShareHarness()
	h.release = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := h.catalog.Provider()(ctx)
		firstErr <- err
	}()
	for h.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	second := make(chan int64, 1)
	go func() { second <- h.get(t) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leaving request got %v", err)
	}
	close(h.release)
	if got := <-second; got != 1 || h.calls.Load() != 1 {
		t.Fatalf("remaining request got %d after %d collections", got, h.calls.Load())
	}
}

// A caller that cancels while the managed-target scan is still running must
// not leave a partial scan behind: the same resolver, asked again by a healthy
// caller, finds the live shell.
func TestCatalogShellResolverDoesNotMemoizeACanceledScan(t *testing.T) {
	_, state := setupIsolatedCLI(t)
	root := t.TempDir()
	writeProjectMeta(t, state, "p", root)
	writeProjectShells(t, state, "p", shellstate.Definition{TmuxName: "sidecar-sh-cancel", DisplayName: "Live", Namespace: tmuxenv.Namespace(), CreatedAt: time.Now(), WorkDir: root})
	// The first git call (worktree discovery) blocks until killed; later ones
	// answer at once.
	bin := t.TempDir()
	started := filepath.Join(t.TempDir(), "started")
	script := "#!/bin/sh\nif [ ! -e \"$SCAN_STARTED\" ]; then\n  : > \"$SCAN_STARTED\"\n  exec sleep 120\nfi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SCAN_STARTED", started)
	inspect := func(_ context.Context, session string) (tty.HeadlessTargetIdentity, error) {
		return tty.HeadlessTargetIdentity{ServerPID: 42, SessionID: "$1", SessionCreated: "10", Session: session, Pane: "%1", Width: 80, Height: 24, PaneCount: 1}, nil
	}
	resolver := newMobileCatalogShellResolver(Env{StateDir: state}, inspect)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := resolver(ctx, "sidecar-sh-cancel")
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the authorization scan never started git")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if _, err := resolver(context.Background(), "sidecar-sh-cancel"); err != nil {
		t.Fatalf("healthy caller inherited the canceled scan: %v", err)
	}
}

// Sharing a collection must not share its authorization. After a live pane is
// split (no socket, config or watcher change), a warm request reuses the
// collected inventory but authorizes against tmux as it is now, so it refuses
// the row as a fresh catalog does and never advertises the old identity. The
// refusal's wording can differ: the shared inventory still saw one pane.
func TestSharedCatalogReauthorizesEveryRequest(t *testing.T) {
	t.Run("managed shell", func(t *testing.T) {
		warm, fresh := warmCatalogAfterSplit(t, true)
		if warm.AttachmentReady || warm.ExpectedTarget != nil {
			t.Fatalf("warm shell row still advertises the pre-split identity: %+v (fresh %s/%s)", warm, fresh.AttachState, fresh.RefusalCode)
		}
	})
	t.Run("worktree candidate", func(t *testing.T) {
		warm, fresh := warmCatalogAfterSplit(t, false)
		if len(fresh.Candidates) != 2 {
			t.Fatalf("fresh worktree row should list both panes: %+v", fresh)
		}
		if warm.AttachmentReady || warm.ExpectedTarget != nil {
			t.Fatalf("warm worktree row still advertises its pre-split candidate %s: %+v (fresh %s/%s)", warm.Target, warm, fresh.AttachState, fresh.RefusalCode)
		}
		for _, candidate := range warm.Candidates {
			if candidate.ExpectedTarget.Pane != "" {
				t.Fatalf("warm worktree row authorized candidate %+v", candidate)
			}
		}
	})
}

// warmCatalogAfterSplit serves one row from a shared collection, splits its
// only pane, and returns the row as a warm request and as a fresh catalog see
// it. managedShell selects a managed-shell row; otherwise the row is the
// project's main worktree reached through an unmanaged session in it.
func warmCatalogAfterSplit(t *testing.T, managedShell bool) (warm, fresh mobileproto.CatalogRow) {
	t.Helper()
	testenv.RequireTmux(t)
	_, state := setupIsolatedCLI(t)
	config.SetTestStateDir(state)
	t.Cleanup(config.ResetTestStateDir)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	initGitRepo(t, root)
	writeProjectMeta(t, state, "p", root)
	session := "plain-worktree-session"
	if managedShell {
		session = "sidecar-sh-warm"
		writeProjectShells(t, state, "p", shellstate.Definition{TmuxName: session, DisplayName: "Live", Namespace: tmuxenv.Namespace(), CreatedAt: time.Now(), WorkDir: root})
	}
	socket := tmuxenv.SocketPath()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	tmux := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("tmux", append([]string{"-f", "/dev/null", "-S", socket}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
	}
	tmux("new-session", "-d", "-s", session, "-c", root, "-x", "80", "-y", "24", "sleep 120")
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigPath(), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := Env{StateDir: state}
	projects := func() ([]hostserve.Project, error) { return []hostserve.Project{{Name: "P", Path: root}}, nil }
	shared := newSharedCatalog(
		func() mobile.CatalogProvider { return mobileCatalogCollectorForProjects(env, projects) },
		func(input mobile.CatalogInput, discovery *worktreeDiscovery) mobile.CatalogInput {
			return authorizeMobileCatalogInput(env, input, discovery)
		})
	host, _ := os.Hostname()
	identity := mobile.CatalogIdentity{HubID: host, OwnerHostID: "local:" + host, OwnerConfigGeneration: "g"}
	row := func(provider mobile.CatalogProvider) mobileproto.CatalogRow {
		t.Helper()
		snapshot, err := mobile.QueryCatalog(context.Background(), provider, mobileResolver(env), mobileproto.CatalogQuery{}, identity)
		if err != nil {
			t.Fatal(err)
		}
		for _, section := range snapshot.Sections {
			for _, r := range section.Rows {
				if managedShell && r.Session == session && r.WorkspaceKind == "shell" ||
					!managedShell && r.WorkspaceKind == "worktree" && r.ProjectID == canonicalMobileSourcePath(root) {
					return r
				}
			}
		}
		t.Fatalf("no row in %+v", snapshot)
		return mobileproto.CatalogRow{}
	}
	if first := row(shared.Provider()); !first.AttachmentReady {
		t.Fatalf("live one-pane row not ready: %+v", first)
	}
	fence := tmuxServerIdentity()
	tmux("split-window", "-d", "-t", session, "-c", root, "sleep 120")
	if tmuxServerIdentity() != fence {
		t.Fatal("a split changed the server fence; the warm path would not be exercised")
	}
	warm = row(shared.Provider())
	fresh = row(mobileCatalogProviderForProjects(env, projects))
	if fresh.AttachmentReady {
		t.Fatalf("fresh catalog accepted the split row: %+v", fresh)
	}
	return warm, fresh
}

// Shared Git discovery runs under its own context: a caller that gives up
// mid-discovery gets nothing, and the next caller gets the complete answer
// from the same run rather than a truncated one.
func TestWorktreeDiscoverySurvivesACanceledCaller(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho x >> \"$DISCOVERY_CALLS\"\nsleep 0.3\nprintf 'worktree /repo\\nHEAD 0\\nbranch refs/heads/main\\n\\nworktree /repo-feature\\nHEAD 0\\nbranch refs/heads/feature\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DISCOVERY_CALLS", calls)
	discovery := newWorktreeDiscovery()
	project := managedtarget.Project{Key: "repo", Path: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if roots := discovery.Discover(ctx, project); roots != nil {
		t.Fatalf("canceled caller got %v", roots)
	}
	roots := discovery.Discover(context.Background(), project)
	if len(roots) != 2 {
		t.Fatalf("healthy caller got %v, want both worktrees", roots)
	}
	if data, _ := os.ReadFile(calls); len(data) != 2 {
		t.Fatalf("git ran %d times, want once", len(data)/2)
	}
}
