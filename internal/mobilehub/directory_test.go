package mobilehub

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

type fakeDirectoryRegistry struct {
	synced      []hosts.Host
	health      map[string]hosts.Health
	healthFn    func(string) (hosts.Health, bool)
	markCalls   int
	validateErr error
}

func (r *fakeDirectoryRegistry) Sync(_ context.Context, configured []hosts.Host) {
	r.synced = append([]hosts.Host(nil), configured...)
}
func (r *fakeDirectoryRegistry) MarkStaleIfQuiet() bool { r.markCalls++; return false }
func (r *fakeDirectoryRegistry) Health(id string) (hosts.Health, bool) {
	if r.healthFn != nil {
		return r.healthFn(id)
	}
	health, ok := r.health[id]
	return health, ok
}

func TestRegistryDirectoryWaitInitialObservesReadyOwnersAndBoundsConnectingOnes(t *testing.T) {
	local := OwnerEndpoint{Host: mobileproto.CatalogHost{ID: "local:hub", State: "online", Local: true}, Bind: func(context.Context) (BoundOwner, error) {
		return BoundOwner{Authority: CatalogAuthority{OwnerHostID: "local:hub", RegistrationFingerprint: "local-registration"}}, nil
	}}
	current := CurrentDirectory{Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"},
		Local: local, LocalRegistrationFingerprint: "local-registration", Remotes: []RegisteredOwner{{Host: hosts.Host{ID: "book", Target: "book"}}}}
	calls := 0
	registry := &fakeDirectoryRegistry{healthFn: func(string) (hosts.Health, bool) {
		calls++
		if calls == 1 {
			return hosts.Health{}, false
		}
		return hosts.Health{State: hosts.StateOnline, Hello: &hostproto.Hello{Capabilities: hostproto.Capabilities{Verbs: hostproto.VerbCapabilities{MobileOwnerServeV0: true}}}}, true
	}}
	directory, _ := newRegistryDirectory(context.Background(), registry, func(context.Context) (CurrentDirectory, error) { return current, nil })
	if err := directory.WaitInitial(context.Background(), time.Second); err != nil || calls < 2 {
		t.Fatalf("wait err=%v health calls=%d", err, calls)
	}

	registry.healthFn = func(string) (hosts.Health, bool) { return hosts.Health{}, false }
	started := time.Now()
	if err := directory.WaitInitial(context.Background(), 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("bounded wait elapsed %v", elapsed)
	}
	snapshot, err := directory.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Hosts[1].State != string(hosts.StateConnecting) || len(snapshot.Failures) != 1 {
		t.Fatalf("timed-out owner not retained as partial failure: %+v", snapshot)
	}
}

func TestRegistryDirectoryWaitOwnerDoesNotWaitForAnotherConnectingHost(t *testing.T) {
	local := OwnerEndpoint{Host: mobileproto.CatalogHost{ID: "local:hub", State: "online", Local: true}, Bind: func(context.Context) (BoundOwner, error) {
		return BoundOwner{Authority: CatalogAuthority{OwnerHostID: "local:hub", RegistrationFingerprint: "local-registration"}}, nil
	}}
	current := CurrentDirectory{Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"}, Local: local,
		LocalRegistrationFingerprint: "local-registration", Remotes: []RegisteredOwner{{Host: hosts.Host{ID: "book", Target: "book"}}, {Host: hosts.Host{ID: "offline", Target: "offline"}}}}
	bookCalls := 0
	registry := &fakeDirectoryRegistry{healthFn: func(id string) (hosts.Health, bool) {
		if id == "offline" {
			return hosts.Health{}, false
		}
		bookCalls++
		if bookCalls == 1 {
			return hosts.Health{}, false
		}
		return hosts.Health{State: hosts.StateOnline, Hello: &hostproto.Hello{Capabilities: hostproto.Capabilities{Verbs: hostproto.VerbCapabilities{MobileOwnerServeV0: true}}}}, true
	}}
	directory, _ := newRegistryDirectory(context.Background(), registry, func(context.Context) (CurrentDirectory, error) { return current, nil })
	if err := directory.WaitOwner(context.Background(), "book", time.Second); err != nil {
		t.Fatal(err)
	}
	if bookCalls < 2 {
		t.Fatalf("selected owner was not re-observed: %d", bookCalls)
	}
}
func (r *fakeDirectoryRegistry) BindMobileRoute(id string) (hosts.MobileRouteAuthority, error) {
	return hosts.MobileRouteAuthority{HostID: id, Incarnation: 1, RegistrationFingerprint: id + "-registration"}, nil
}
func (r *fakeDirectoryRegistry) ValidateMobileRoute(hosts.MobileRouteAuthority) error {
	return r.validateErr
}
func (r *fakeDirectoryRegistry) MobileSidecarCommand(context.Context, hosts.MobileRouteAuthority) (*exec.Cmd, error) {
	return nil, errors.New("not started by directory projection")
}

func TestRegistryDirectoryProjectsCurrentHealthAndBindsExactRoutes(t *testing.T) {
	local := OwnerEndpoint{Host: mobileproto.CatalogHost{ID: "local:hub", Name: "Hub", State: "online", Local: true}, Bind: func(context.Context) (BoundOwner, error) {
		return BoundOwner{Authority: CatalogAuthority{OwnerHostID: "local:hub", RegistrationFingerprint: "local-registration"},
			Start: func(context.Context) (LineStream, mobileproto.Response, error) {
				return nil, mobileproto.Response{}, nil
			},
			Validate: func(context.Context) error { return nil }}, nil
	}}
	current := CurrentDirectory{Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"},
		Local: local, LocalRegistrationFingerprint: "local-registration", Remotes: []RegisteredOwner{
			{Host: hosts.Host{ID: "book", Target: "book"}, Name: "Book"},
			{Host: hosts.Host{ID: "old", Target: "old"}, Name: "Old"},
			{Host: hosts.Host{ID: "off", Target: "off"}, Name: "Off", Disabled: true},
		}}
	registry := &fakeDirectoryRegistry{health: map[string]hosts.Health{
		"book": {State: hosts.StateOnline, Hello: &hostproto.Hello{Capabilities: hostproto.Capabilities{Verbs: hostproto.VerbCapabilities{MobileServeV0: true, MobileOwnerServeV0: true}}}},
		"old":  {State: hosts.StateStale, Detail: "quiet"},
	}}
	directory, err := newRegistryDirectory(context.Background(), registry, func(context.Context) (CurrentDirectory, error) { return current, nil })
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := directory.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hostStates(snapshot.Hosts), []string{"local:hub=online", "book=online", "old=stale", "off=disabled"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
	if len(snapshot.Endpoints) != 2 || snapshot.Endpoints[1].Host.ID != "book" || len(snapshot.Failures) != 1 || snapshot.Failures[0].ID != "old" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if got := []string{registry.synced[0].ID, registry.synced[1].ID}; !reflect.DeepEqual(got, []string{"book", "old"}) {
		t.Fatalf("synced owners = %v", got)
	}
	owner, err := snapshot.Endpoints[1].Bind(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if owner.Authority.OwnerHostID != "book" || owner.Authority.RegistrationFingerprint != "book-registration" {
		t.Fatalf("bound owner = %+v", owner.Authority)
	}
	if err := owner.Validate(context.Background()); err != nil || registry.markCalls < 2 {
		t.Fatalf("route validation err=%v marks=%d", err, registry.markCalls)
	}
	current.Identity.OwnerConfigGeneration = "changed"
	if err := snapshot.Validate(context.Background()); err == nil {
		t.Fatal("config change did not invalidate directory snapshot")
	}
}

func TestRegistryDirectoryRefusesUnsupportedAndMismatchedLocalOwners(t *testing.T) {
	local := OwnerEndpoint{Host: mobileproto.CatalogHost{ID: "local:hub", State: "online", Local: true}, Bind: func(context.Context) (BoundOwner, error) {
		return BoundOwner{Authority: CatalogAuthority{OwnerHostID: "local:hub", RegistrationFingerprint: "different"}}, nil
	}}
	current := CurrentDirectory{Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"},
		Local: local, LocalRegistrationFingerprint: "local-registration", Remotes: []RegisteredOwner{{Host: hosts.Host{ID: "book", Target: "book"}}}}
	registry := &fakeDirectoryRegistry{health: map[string]hosts.Health{"book": {State: hosts.StateOnline, Hello: &hostproto.Hello{Capabilities: hostproto.Capabilities{Verbs: hostproto.VerbCapabilities{MobileServeV0: true}}}}}}
	directory, _ := newRegistryDirectory(context.Background(), registry, func(context.Context) (CurrentDirectory, error) { return current, nil })
	snapshot, err := directory.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Endpoints) != 1 || snapshot.Hosts[1].State != "unsupported" || len(snapshot.Failures) != 1 {
		t.Fatalf("unsupported owner snapshot = %+v", snapshot)
	}
	if _, err := snapshot.Endpoints[0].Bind(context.Background()); err == nil {
		t.Fatal("mismatched local authority was accepted")
	}
}

func hostStates(hosts []mobileproto.CatalogHost) []string {
	states := make([]string, len(hosts))
	for i, host := range hosts {
		states[i] = host.ID + "=" + host.State
	}
	return states
}
