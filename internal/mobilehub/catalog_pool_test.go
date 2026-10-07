package mobilehub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

type catalogPoolRegistry struct {
	mu          sync.Mutex
	host        hosts.Host
	available   bool
	starts      int
	mode        string
	commands    []*exec.Cmd
	incarnation uint64
}

func (r *catalogPoolRegistry) Sync(_ context.Context, configured []hosts.Host) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.available = len(configured) != 0
	if r.available && (r.incarnation == 0 || hosts.RegistrationFingerprint(configured[0]) != hosts.RegistrationFingerprint(r.host)) {
		r.incarnation++
		r.host = configured[0]
	}
}

func (r *catalogPoolRegistry) MarkStaleIfQuiet() bool { return false }
func (r *catalogPoolRegistry) Health(string) (hosts.Health, bool) {
	return hosts.Health{State: hosts.StateOnline, Hello: &hostproto.Hello{Capabilities: hostproto.Capabilities{Verbs: hostproto.VerbCapabilities{MobileOwnerServeV0: true}}}}, true
}
func (r *catalogPoolRegistry) BindMobileRoute(id string) (hosts.MobileRouteAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return hosts.MobileRouteAuthority{HostID: id, RegistrationFingerprint: hosts.RegistrationFingerprint(r.host), Incarnation: r.incarnation}, nil
}
func (r *catalogPoolRegistry) ValidateMobileRoute(authority hosts.MobileRouteAuthority) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.available || authority.Incarnation != r.incarnation || authority.RegistrationFingerprint != hosts.RegistrationFingerprint(r.host) {
		return hosts.ErrMobileRouteChanged
	}
	return nil
}
func (r *catalogPoolRegistry) MobileSidecarCommand(ctx context.Context, authority hosts.MobileRouteAuthority) (*exec.Cmd, error) {
	if err := r.ValidateMobileRoute(authority); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCatalogPoolHelperProcess$", "--")
	cmd.Env = append(os.Environ(), "SIDECAR_CATALOG_POOL_HELPER=1", "SIDECAR_CATALOG_POOL_MODE="+r.mode)
	r.commands = append(r.commands, cmd)
	return cmd, nil
}
func (r *catalogPoolRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

func newCatalogPoolRig(t *testing.T, ctx context.Context, mode string) (*RegistryDirectory, *CatalogRouter, *catalogPoolRegistry, *CurrentDirectory) {
	t.Helper()
	local := OwnerEndpoint{Host: mobileproto.CatalogHost{ID: "local:hub", State: "online", Local: true}, Bind: func(context.Context) (BoundOwner, error) {
		stream := newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local"))
		return fakeCatalogEndpoint("local:hub", "local-registration", stream).Bind(context.Background())
	}}
	current := &CurrentDirectory{Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"},
		Local: local, LocalRegistrationFingerprint: "local-registration", Remotes: []RegisteredOwner{{Host: hosts.Host{ID: "book", Target: "book"}}}}
	registry := &catalogPoolRegistry{mode: mode}
	directory, err := newRegistryDirectory(ctx, registry, func(context.Context) (CurrentDirectory, error) { return *current, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(directory.Close)
	router, err := NewCatalogRouter(directory)
	if err != nil {
		t.Fatal(err)
	}
	return directory, router, registry, current
}

func requirePoolCatalog(t *testing.T, router *CatalogRouter) {
	t.Helper()
	catalog, err := router.Query(context.Background(), mobileproto.CatalogQuery{Sort: "project"})
	if err != nil || catalog.Total != 2 || len(catalog.Failures) != 0 {
		t.Fatalf("catalog total=%d failures=%+v err=%v", catalog.Total, catalog.Failures, err)
	}
}

func TestCatalogRouterIdleWindowKeepsOneWarmRemoteOwner(t *testing.T) {
	_, router, registry, _ := newCatalogPoolRig(t, context.Background(), "")
	requirePoolCatalog(t, router)
	// A fixed window at a much higher polling rate than the phone catches the
	// old per-snapshot spawn and repeated request IDs on a warm protocol.
	deadline := time.NewTimer(500 * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	queries := 1
	for {
		select {
		case <-deadline.C:
			if queries < 10 || registry.count() != 1 {
				t.Fatalf("%d snapshots spawned %d owners; want one warm owner", queries, registry.count())
			}
			return
		case <-ticker.C:
			requirePoolCatalog(t, router)
			queries++
		}
	}
}

func TestCatalogPoolConcurrentQueriesShareWarmOwner(t *testing.T) {
	_, router, registry, _ := newCatalogPoolRig(t, context.Background(), "")
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			catalog, err := router.Query(context.Background(), mobileproto.CatalogQuery{})
			if err != nil || catalog.Total != 2 || len(catalog.Failures) != 0 {
				t.Errorf("concurrent catalog total=%d failures=%+v err=%v", catalog.Total, catalog.Failures, err)
			}
		}()
	}
	wait.Wait()
	if registry.count() != 1 {
		t.Fatalf("concurrent queries spawned %d owners", registry.count())
	}
}

func TestCatalogPoolCanceledQueryDrainsLateReplyWithoutRespawning(t *testing.T) {
	_, router, registry, _ := newCatalogPoolRig(t, context.Background(), "delay")
	requirePoolCatalog(t, router)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _ = router.Query(ctx, mobileproto.CatalogQuery{})
	if time.Since(started) > 90*time.Millisecond {
		t.Fatal("canceled catalog waited for the owner's late reply")
	}
	// This caller times out waiting for the prior response to drain. It must
	// neither send another request nor retire the shared connection.
	waitCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	_, _ = router.Query(waitCtx, mobileproto.CatalogQuery{})
	requirePoolCatalog(t, router)
	if registry.count() != 1 {
		t.Fatalf("caller cancellation respawned a healthy owner: %d", registry.count())
	}
}

func TestCatalogPoolRequestErrorDoesNotRetireHealthyOwner(t *testing.T) {
	_, router, registry, _ := newCatalogPoolRig(t, context.Background(), "error-once")
	catalog, err := router.Query(context.Background(), mobileproto.CatalogQuery{})
	if err != nil || len(catalog.Failures) != 1 || catalog.Total != 1 {
		t.Fatalf("request refusal total=%d failures=%+v err=%v", catalog.Total, catalog.Failures, err)
	}
	requirePoolCatalog(t, router)
	if registry.count() != 1 {
		t.Fatalf("request-level error respawned a healthy owner: %d", registry.count())
	}
}

func TestCatalogPoolDoesNotShareTerminalLookupConnection(t *testing.T) {
	directory, router, registry, _ := newCatalogPoolRig(t, context.Background(), "")
	catalog, err := router.Query(context.Background(), mobileproto.CatalogQuery{Hosts: []string{"book"}})
	if err != nil || catalog.Total != 1 {
		t.Fatalf("remote catalog total=%d err=%v", catalog.Total, err)
	}
	warm := poolStream(t, directory)
	row := catalog.Sections[0].Rows[0]
	_, terminal, _, err := router.Lookup(context.Background(), row.Target, *row.ExpectedTarget)
	if err != nil {
		t.Fatal(err)
	}
	terminal.Close()
	requirePoolCatalog(t, router)
	if registry.count() != 2 || warm != poolStream(t, directory) || warm.isClosing() {
		t.Fatalf("terminal lookup affected warm catalog stream; spawns=%d closing=%v", registry.count(), warm.isClosing())
	}
}

func poolStream(t *testing.T, directory *RegistryDirectory) *OwnerStream {
	t.Helper()
	directory.pool.mu.Lock()
	entry := directory.pool.entries["book"]
	directory.pool.mu.Unlock()
	if entry == nil {
		t.Fatal("warm owner missing")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.stream
}

func TestCatalogPoolReopensOnlyAfterTransportLoss(t *testing.T) {
	directory, router, registry, _ := newCatalogPoolRig(t, context.Background(), "")
	requirePoolCatalog(t, router)
	stream := poolStream(t, directory)
	if err := stream.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := stream.Wait(ctx); err == nil {
		t.Fatal("killed transport did not report loss")
	}
	requirePoolCatalog(t, router)
	requirePoolCatalog(t, router)
	if registry.count() != 2 {
		t.Fatalf("transport loss spawned %d owners; want original plus replacement", registry.count())
	}
}

func TestCatalogPoolRouteIncarnationChangeRetiresWarmOwner(t *testing.T) {
	directory, router, registry, _ := newCatalogPoolRig(t, context.Background(), "")
	requirePoolCatalog(t, router)
	old := poolStream(t, directory)
	registry.mu.Lock()
	registry.incarnation++
	registry.mu.Unlock()
	requirePoolCatalog(t, router)
	requirePoolCatalog(t, router)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := old.Wait(ctx); errors.Is(err, context.DeadlineExceeded) || !old.isClosing() {
		t.Fatalf("old route survived replacement: %v", err)
	}
	if registry.count() != 2 || old == poolStream(t, directory) {
		t.Fatalf("route incarnation change spawns=%d", registry.count())
	}
}

func TestCatalogPoolDisabledRegistrationClosesWarmOwner(t *testing.T) {
	directory, router, registry, current := newCatalogPoolRig(t, context.Background(), "")
	requirePoolCatalog(t, router)
	old := poolStream(t, directory)
	current.Remotes[0].Disabled = true
	if _, err := directory.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := old.Wait(ctx); errors.Is(err, context.DeadlineExceeded) || !old.isClosing() {
		t.Fatalf("disabled owner survived: %v", err)
	}
	current.Remotes[0].Disabled = false
	requirePoolCatalog(t, router)
	if registry.count() != 2 {
		t.Fatalf("reenabled registration spawns=%d", registry.count())
	}
}

func TestCatalogPoolRetargetRemovalAndShutdownRetireWarmOwners(t *testing.T) {
	directory, router, registry, current := newCatalogPoolRig(t, context.Background(), "")
	requirePoolCatalog(t, router)
	old := poolStream(t, directory)
	current.Remotes[0].Host.Target = "new-book"
	requirePoolCatalog(t, router)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := old.Wait(ctx); err != nil {
		t.Fatalf("retarget cleanup: %v", err)
	}
	if registry.count() != 2 {
		t.Fatalf("retarget spawned %d owners", registry.count())
	}
	removed := poolStream(t, directory)
	current.Remotes = nil
	if _, err := directory.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := removed.Wait(ctx); err != nil {
		t.Fatalf("removal cleanup: %v", err)
	}
	current.Remotes = []RegisteredOwner{{Host: hosts.Host{ID: "book", Target: "new-book"}}}
	requirePoolCatalog(t, router)
	last := poolStream(t, directory)
	directory.Close()
	directory.Close()
	if err := last.Wait(ctx); err != nil {
		t.Fatalf("shutdown cleanup: %v", err)
	}
	authority, _ := registry.BindMobileRoute("book")
	if _, _, err := directory.pool.acquire(ctx, authority); !errors.Is(err, ErrOwnerClosed) {
		t.Fatalf("closed pool accepted a new owner: %v", err)
	}
}

func TestCatalogPoolLifetimeCancellationClosesIdleOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	directory, router, _, _ := newCatalogPoolRig(t, ctx, "")
	requirePoolCatalog(t, router)
	stream := poolStream(t, directory)
	cancel()
	waitCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := stream.Wait(waitCtx); errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lifetime cleanup: %v", err)
	}
}

func TestCatalogPoolHelperProcess(t *testing.T) {
	if os.Getenv("SIDECAR_CATALOG_POOL_HELPER") != "1" {
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	seen := make(map[string]bool)
	refused := false
	for scanner.Scan() {
		var request mobileproto.Request
		if json.Unmarshal(scanner.Bytes(), &request) != nil || seen[request.RequestID] {
			os.Exit(3)
		}
		seen[request.RequestID] = true
		response := mobileproto.Response{Version: mobileproto.Version, Type: request.Type, RequestID: request.RequestID, APIInstance: "pool-owner"}
		switch request.Type {
		case mobileproto.RequestHello:
			caps := mobileproto.DefaultCapabilities()
			response.Capabilities = &caps
		case mobileproto.RequestSessions:
			if os.Getenv("SIDECAR_CATALOG_POOL_MODE") == "delay" {
				time.Sleep(100 * time.Millisecond)
			}
			catalog := rawOwnerCatalog("remote-owner", "remote", "remote")
			response.Catalog = &catalog
			if os.Getenv("SIDECAR_CATALOG_POOL_MODE") == "error-once" && !refused {
				refused = true
				response.Type, response.Catalog = mobileproto.ResponseError, nil
				response.Error = &mobileproto.Error{Code: mobileproto.ErrorBackend, Message: "catalog temporarily unavailable"}
			}
		default:
			fmt.Fprintln(os.Stderr, "unexpected pooled request", request.Type)
			os.Exit(4)
		}
		if err := encoder.Encode(response); err != nil {
			os.Exit(5)
		}
	}
	os.Exit(0)
}
