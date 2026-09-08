package mobilehub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// RegisteredOwner is one configured remote owner in display order. Disabled
// registrations remain visible but never create a registry client or route.
type RegisteredOwner struct {
	Host     hosts.Host
	Name     string
	Disabled bool
}

// CurrentDirectory is the complete current hub authority. Provider calls must
// reload the owning config rather than return a startup snapshot.
type CurrentDirectory struct {
	Identity                     mobile.CatalogIdentity
	Local                        OwnerEndpoint
	LocalRegistrationFingerprint string
	Remotes                      []RegisteredOwner
}

type DirectoryProvider func(context.Context) (CurrentDirectory, error)

// RegistryDirectory projects current config and registry health into the
// catalog router. Terminal authority remains in each owning mobile service.
type RegistryDirectory struct {
	lifetime context.Context
	registry directoryRegistry
	current  DirectoryProvider
}

const directoryReadinessPoll = 25 * time.Millisecond

type directoryRegistry interface {
	RouteRegistry
	BindMobileRoute(string) (hosts.MobileRouteAuthority, error)
	Sync(context.Context, []hosts.Host)
	MarkStaleIfQuiet() bool
	Health(string) (hosts.Health, bool)
}

type hostsDirectoryRegistry struct{ *hosts.Registry }

func (r hostsDirectoryRegistry) Health(id string) (hosts.Health, bool) {
	client, ok := r.Client(id)
	if !ok {
		return hosts.Health{}, false
	}
	return client.Health(), true
}

func NewRegistryDirectory(lifetime context.Context, registry *hosts.Registry, current DirectoryProvider) (*RegistryDirectory, error) {
	if lifetime == nil || registry == nil || current == nil {
		return nil, fmt.Errorf("mobile hub: registry directory requires lifetime, registry and current config")
	}
	return newRegistryDirectory(lifetime, hostsDirectoryRegistry{registry}, current)
}

func newRegistryDirectory(lifetime context.Context, registry directoryRegistry, current DirectoryProvider) (*RegistryDirectory, error) {
	if lifetime == nil || registry == nil || current == nil {
		return nil, fmt.Errorf("mobile hub: registry directory requires lifetime, registry and current config")
	}
	return &RegistryDirectory{lifetime: lifetime, registry: registry, current: current}, nil
}

func (d *RegistryDirectory) Snapshot(ctx context.Context) (DirectorySnapshot, error) {
	current, err := d.current(ctx)
	if err != nil {
		return DirectorySnapshot{}, err
	}
	if err := validateCurrentDirectory(current); err != nil {
		return DirectorySnapshot{}, err
	}
	fingerprint := currentDirectoryFingerprint(current)
	enabled := make([]hosts.Host, 0, len(current.Remotes))
	for _, remote := range current.Remotes {
		if !remote.Disabled {
			enabled = append(enabled, remote.Host)
		}
	}
	d.registry.Sync(d.lifetime, enabled)
	d.registry.MarkStaleIfQuiet()

	snapshot := DirectorySnapshot{Identity: current.Identity, Hosts: make([]mobileproto.CatalogHost, 0, len(current.Remotes)+1),
		Endpoints: make([]OwnerEndpoint, 0, len(current.Remotes)+1), Failures: make([]mobileproto.CatalogFailure, 0, len(current.Remotes))}
	local := current.Local
	bindLocal := local.Bind
	local.Bind = func(bindCtx context.Context) (BoundOwner, error) {
		owner, err := bindLocal(bindCtx)
		if err != nil {
			return BoundOwner{}, err
		}
		if owner.Authority.OwnerHostID != current.Local.Host.ID || owner.Authority.RegistrationFingerprint != current.LocalRegistrationFingerprint {
			return BoundOwner{}, fmt.Errorf("mobile hub: local owner authority changed")
		}
		return owner, nil
	}
	snapshot.Hosts = append(snapshot.Hosts, local.Host)
	snapshot.Endpoints = append(snapshot.Endpoints, local)
	for _, registered := range current.Remotes {
		host := mobileproto.CatalogHost{ID: registered.Host.ID, Name: registered.Name}
		if host.Name == "" {
			host.Name = registered.Host.ID
		}
		if registered.Disabled {
			host.State, host.Detail = string(hosts.StateDisabled), "remote owner is disabled"
			snapshot.Hosts = append(snapshot.Hosts, host)
			continue
		}
		health, ok := d.registry.Health(registered.Host.ID)
		if !ok {
			host.State, host.Detail = string(hosts.StateConnecting), "remote owner is starting"
			snapshot.Hosts = append(snapshot.Hosts, host)
			snapshot.Failures = append(snapshot.Failures, mobileproto.CatalogFailure{Scope: "host", ID: host.ID, Name: host.Name, State: host.State, Detail: host.Detail})
			continue
		}
		host.State, host.Detail = string(health.State), health.Detail
		if health.State == hosts.StateOnline && (health.Hello == nil || !health.Hello.Capabilities.Verbs.MobileOwnerServeV0) {
			host.State, host.Detail = "unsupported", "remote owner did not advertise mobile owner serve v0"
		}
		snapshot.Hosts = append(snapshot.Hosts, host)
		if host.State != string(hosts.StateOnline) {
			if len(snapshot.Failures) < mobileproto.MaxCatalogFailures {
				snapshot.Failures = append(snapshot.Failures, mobileproto.CatalogFailure{Scope: "host", ID: host.ID, Name: host.Name, State: host.State, Detail: host.Detail})
			}
			continue
		}
		hostCopy := host
		hostID := registered.Host.ID
		snapshot.Endpoints = append(snapshot.Endpoints, OwnerEndpoint{Host: hostCopy, Bind: func(context.Context) (BoundOwner, error) {
			authority, err := d.registry.BindMobileRoute(hostID)
			if err != nil {
				return BoundOwner{}, err
			}
			return BoundOwner{Authority: CatalogAuthority{OwnerHostID: hostID, RegistrationFingerprint: authority.RegistrationFingerprint},
				Start: func(startCtx context.Context) (LineStream, mobileproto.Response, error) {
					return StartOwner(startCtx, d.registry, authority)
				}, Validate: func(context.Context) error {
					d.registry.MarkStaleIfQuiet()
					return d.registry.ValidateMobileRoute(authority)
				}}, nil
		}})
	}
	snapshot.Validate = func(validateCtx context.Context) error {
		latest, err := d.current(validateCtx)
		if err != nil {
			return err
		}
		if err := validateCurrentDirectory(latest); err != nil {
			return err
		}
		if currentDirectoryFingerprint(latest) != fingerprint {
			return fmt.Errorf("mobile hub: owner directory changed")
		}
		return nil
	}
	return snapshot, nil
}

// WaitInitial gives newly started remote observers a bounded opportunity to
// publish their first health result. A timeout is not an error: the next
// catalog snapshot keeps those owners visible as connecting failures while
// still returning every available owner. Caller cancellation remains fatal.
func (d *RegistryDirectory) WaitInitial(ctx context.Context, maximum time.Duration) error {
	if maximum <= 0 {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, maximum)
	defer cancel()
	for {
		snapshot, err := d.Snapshot(waitCtx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if waitCtx.Err() != nil {
				return nil
			}
			return err
		}
		pending := false
		for _, host := range snapshot.Hosts {
			if !host.Local && strings.EqualFold(host.State, string(hosts.StateConnecting)) {
				pending = true
				break
			}
		}
		if !pending {
			return nil
		}
		timer := time.NewTimer(directoryReadinessPoll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-waitCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func validateCurrentDirectory(current CurrentDirectory) error {
	if current.Identity.HubID == "" || current.Identity.OwnerHostID == "" || current.Identity.OwnerConfigGeneration == "" ||
		current.Local.Host.ID != current.Identity.OwnerHostID || current.Local.Host.State != string(hosts.StateOnline) || !current.Local.Host.Local ||
		current.Local.Bind == nil || current.LocalRegistrationFingerprint == "" || len(current.Remotes)+1 > mobileproto.MaxCatalogHosts {
		return fmt.Errorf("mobile hub: current directory is incomplete or oversized")
	}
	seen := map[string]bool{current.Local.Host.ID: true}
	for _, remote := range current.Remotes {
		host := remote.Host
		if host.ID == "" || strings.TrimSpace(host.Target) == "" || seen[host.ID] {
			return fmt.Errorf("mobile hub: current directory contains an invalid or duplicate owner")
		}
		seen[host.ID] = true
	}
	return nil
}

func currentDirectoryFingerprint(current CurrentDirectory) string {
	parts := []string{current.Identity.HubID, current.Identity.OwnerHostID, current.Identity.OwnerConfigGeneration,
		current.Local.Host.ID, current.Local.Host.Name, current.LocalRegistrationFingerprint}
	for _, remote := range current.Remotes {
		parts = append(parts, remote.Host.ID, remote.Name, hosts.RegistrationFingerprint(remote.Host), fmt.Sprint(remote.Disabled))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

var _ OwnerDirectory = (*RegistryDirectory)(nil)
