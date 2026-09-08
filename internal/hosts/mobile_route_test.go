package hosts

import (
	"context"
	"errors"
	"testing"

	"github.com/marcus/sidecar/internal/hostproto"
)

func routeTestRegistry(t *testing.T, host Host, mobile bool) (*Registry, *Client) {
	t.Helper()
	registry := NewRegistry(ClientOptions{})
	client := NewClient(host, ClientOptions{Dial: func(context.Context) (*Conn, error) { return nil, context.Canceled }, ControlDir: t.TempDir()})
	client.mu.Lock()
	client.health = Health{State: StateOnline, Hello: &hostproto.Hello{Capabilities: hostproto.Capabilities{Verbs: hostproto.VerbCapabilities{MobileServeV0: mobile}}}}
	client.mu.Unlock()
	registry.clients[host.ID] = client
	registry.incarnations[host.ID] = 41
	t.Cleanup(registry.Stop)
	return registry, client
}

func TestMobileRouteAuthorityIsStableForTheCurrentOnlineCapableClient(t *testing.T) {
	host := Host{ID: "book", Target: "book.local", RemoteBinary: "/opt/sidecar", RemoteConfig: "/etc/sidecar.json", Env: []string{"A=1", "B=2"}}
	registry, _ := routeTestRegistry(t, host, true)
	authority, err := registry.BindMobileRoute("book")
	if err != nil {
		t.Fatal(err)
	}
	if authority.HostID != "book" || authority.Incarnation != 41 || authority.RegistrationFingerprint != RegistrationFingerprint(host) {
		t.Fatalf("authority = %+v", authority)
	}
	if err := registry.ValidateMobileRoute(authority); err != nil {
		t.Fatalf("unchanged route refused: %v", err)
	}
	if command, err := registry.MobileSidecarCommand(context.Background(), authority); err != nil || command == nil {
		t.Fatalf("owner command = %v, %v", command, err)
	}
}

func TestMobileRouteAuthorityRefusesHealthOrCapabilityLossAfterBinding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*Client)
		wantErr error
	}{
		{name: "stale", mutate: func(client *Client) { client.health.State = StateStale }, wantErr: ErrMobileRouteUnavailable},
		{name: "capability", mutate: func(client *Client) { client.health.Hello.Capabilities.Verbs.MobileServeV0 = false }, wantErr: ErrMobileRouteUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, client := routeTestRegistry(t, Host{ID: "book", Target: "book"}, true)
			authority, err := registry.BindMobileRoute("book")
			if err != nil {
				t.Fatal(err)
			}
			client.mu.Lock()
			tc.mutate(client)
			client.mu.Unlock()
			if err := registry.ValidateMobileRoute(authority); !errors.Is(err, tc.wantErr) {
				t.Fatalf("changed health error = %v", err)
			}
			if _, err := registry.MobileSidecarCommand(context.Background(), authority); !errors.Is(err, tc.wantErr) {
				t.Fatalf("command after changed health error = %v", err)
			}
		})
	}
}

func TestMobileRouteAuthorityRefusesUnavailableAndMissingCapability(t *testing.T) {
	registry, client := routeTestRegistry(t, Host{ID: "book", Target: "book"}, false)
	if _, err := registry.BindMobileRoute("missing"); !errors.Is(err, ErrMobileRouteUnavailable) {
		t.Fatalf("missing route error = %v", err)
	}
	if _, err := registry.BindMobileRoute("book"); !errors.Is(err, ErrMobileRouteUnsupported) {
		t.Fatalf("capability error = %v", err)
	}
	client.mu.Lock()
	client.health.State = StateStale
	client.mu.Unlock()
	if _, err := registry.BindMobileRoute("book"); !errors.Is(err, ErrMobileRouteUnavailable) {
		t.Fatalf("stale route error = %v", err)
	}
}

func TestMobileRouteAuthorityRefusesRemovalRetargetAndReplacement(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Registry)
	}{
		{name: "removed", mutate: func(r *Registry) { delete(r.clients, "book") }},
		{name: "retargeted", mutate: func(r *Registry) { r.clients["book"].host.Target = "replacement" }},
		{name: "replaced", mutate: func(r *Registry) {
			replacement := NewClient(Host{ID: "book", Target: "book"}, ClientOptions{Dial: func(context.Context) (*Conn, error) { return nil, context.Canceled }, ControlDir: t.TempDir()})
			replacement.mu.Lock()
			replacement.health = Health{State: StateOnline, Hello: &hostproto.Hello{Capabilities: hostproto.Capabilities{Verbs: hostproto.VerbCapabilities{MobileServeV0: true}}}}
			replacement.mu.Unlock()
			r.clients["book"] = replacement
			r.incarnations["book"]++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, _ := routeTestRegistry(t, Host{ID: "book", Target: "book"}, true)
			authority, err := registry.BindMobileRoute("book")
			if err != nil {
				t.Fatal(err)
			}
			registry.mu.Lock()
			tc.mutate(registry)
			registry.mu.Unlock()
			if err := registry.ValidateMobileRoute(authority); !errors.Is(err, ErrMobileRouteChanged) {
				t.Fatalf("changed route error = %v", err)
			}
		})
	}
}

func TestRegistrationFingerprintCoversEveryRoutingField(t *testing.T) {
	base := Host{ID: "book", Target: "book", RemoteBinary: "sidecar", RemoteConfig: "config", Env: []string{"A=1", "B=2"}}
	for name, changed := range map[string]Host{
		"id":          {ID: "other", Target: "book", RemoteBinary: "sidecar", RemoteConfig: "config", Env: []string{"A=1", "B=2"}},
		"target":      {ID: "book", Target: "other", RemoteBinary: "sidecar", RemoteConfig: "config", Env: []string{"A=1", "B=2"}},
		"binary":      {ID: "book", Target: "book", RemoteBinary: "other", RemoteConfig: "config", Env: []string{"A=1", "B=2"}},
		"config":      {ID: "book", Target: "book", RemoteBinary: "sidecar", RemoteConfig: "other", Env: []string{"A=1", "B=2"}},
		"environment": {ID: "book", Target: "book", RemoteBinary: "sidecar", RemoteConfig: "config", Env: []string{"B=2", "A=1"}},
	} {
		if RegistrationFingerprint(base) == RegistrationFingerprint(changed) {
			t.Errorf("%s change retained registration fingerprint", name)
		}
	}
}
