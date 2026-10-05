package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobilehub"
	"github.com/marcus/sidecar/internal/mobileproto"
)

const (
	mobileInitialOwnerWait = 5 * time.Second
	mobileSessionsTimeout  = 14 * time.Second
)

func runMobileHubOrOwner(env Env) error {
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	backend, err := newMobileBackend(ctx, env)
	if err != nil {
		return err
	}
	defer backend.Close()
	return backend.ServeTerminal(ctx, env.Stdin, env.Stdout)
}

// mobileOneShotBackend builds the backend for a one-shot catalog query;
// tests replace it to observe the context it is given.
var mobileOneShotBackend = newMobileBackend

// queryMobileCatalog answers `sidecar mobile sessions`. The whole one-shot
// query is bounded, setup included: building the registry, the first
// directory snapshot and the query share one deadline, so a slow setup cannot
// hang the command. The long-lived API server builds its backend under its
// own lifetime instead and bounds each query in Sessions.
func queryMobileCatalog(env Env, query mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	queryCtx, cancel := context.WithTimeout(ctx, mobileSessionsTimeout)
	defer cancel()
	backend, err := mobileOneShotBackend(queryCtx, env)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	defer backend.Close()
	return backend.Sessions(queryCtx, query)
}

// mobileBackend is the terminal and catalog authority behind every mobile
// protocol transport: stdio for `sidecar mobile serve`, a one-shot query for
// `sidecar mobile sessions`, and the long-lived UI API server. It holds at most
// one remote-host registry and catalog router for its lifetime and gives every
// stream its own broker run (or local owner service), so a server with many
// connections composes exactly what one stdio process does.
//
// Whether remote hosts route through the hub is decided once, at construction,
// from the configuration then in force.
type mobileBackend struct {
	env         Env
	registry    *hosts.Registry
	directory   *mobilehub.RegistryDirectory
	router      *mobilehub.CatalogRouter
	broker      *mobilehub.ProtocolBroker
	initialWait sync.Once
	// catalog is the one local collection concurrent requests share; see
	// sharedCatalog. Every local catalog this backend serves reads it.
	catalog     *sharedCatalog
	watchMu     sync.Mutex
	watchCancel context.CancelFunc
	watchDone   chan struct{}
}

func newMobileBackend(ctx context.Context, env Env) (*mobileBackend, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	backend := &mobileBackend{env: env, catalog: newSharedCatalog(
		func() mobile.CatalogProvider { return mobileCatalogCollectorForProjects(env, configuredProjects) },
		func(input mobile.CatalogInput) mobile.CatalogInput { return authorizeMobileCatalogInput(env, input) })}
	if !remoteHostsEnabled(env, cfg) || len(cfg.Hosts.List) == 0 {
		return backend, nil
	}
	registry, directory, router, err := newMobileCatalogRouter(ctx, env, backend.catalog.Provider())
	if err != nil {
		return nil, err
	}
	// Start configured owner observers before the first catalog request.
	// Snapshot is nonblocking with respect to SSH; the first request can retain
	// local rows while a slow owner remains explicitly connecting.
	if _, err := directory.Snapshot(ctx); err != nil {
		registry.Stop()
		return nil, err
	}
	broker, err := mobilehub.NewProtocolBroker(router)
	if err != nil {
		registry.Stop()
		return nil, err
	}
	backend.registry, backend.directory, backend.router, backend.broker = registry, directory, router, broker
	return backend, nil
}

// Close stops the remote-host registry, if this backend holds one.
func (b *mobileBackend) Close() {
	b.watchMu.Lock()
	stop, done := b.watchCancel, b.watchDone
	b.watchMu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
	if b.registry != nil {
		b.registry.Stop()
	}
}

// ServeTerminal runs one protocol stream until input reaches EOF or ctx ends.
// Without remote hosts the local owner service serves it in-process; with them
// a fresh broker run routes it to the owning Sidecar.
func (b *mobileBackend) ServeTerminal(ctx context.Context, input io.Reader, output io.Writer) error {
	if b.broker == nil {
		service, err := newMobileOwnerServiceWithCatalog(b.env, input, output, b.localCatalog())
		if err != nil {
			return err
		}
		return service.Run(ctx)
	}
	return b.broker.Run(ctx, input, output)
}

// Sessions answers one catalog query: the code path behind both `sidecar
// mobile sessions --json` and GET /api/v0/sessions.
func (b *mobileBackend) Sessions(ctx context.Context, query mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	if b.router == nil {
		return queryLocalMobileCatalogFrom(ctx, b.env, query, b.localCatalog())
	}
	queryCtx, cancel := context.WithTimeout(ctx, mobileSessionsTimeout)
	defer cancel()
	// Only the first query waits for newly started owners to report. Later
	// queries in a long-lived process see a reconnecting owner as an explicit
	// connecting failure, as a stream's own sessions request does.
	var waitErr error
	b.initialWait.Do(func() { waitErr = b.directory.WaitInitial(queryCtx, mobileInitialOwnerWait) })
	if waitErr != nil {
		return mobileproto.CatalogSnapshot{}, waitErr
	}
	return b.router.Query(queryCtx, query)
}

// localCatalog is this backend's shared local collection, or a private one for
// a backend built without it (tests construct mobileBackend directly).
func (b *mobileBackend) localCatalog() mobile.CatalogProvider {
	if b.catalog == nil {
		return mobileCatalogProvider(b.env)
	}
	return b.catalog.Provider()
}

// invalidateCatalog retires the shared local collection after a change signal.
func (b *mobileBackend) invalidateCatalog() {
	if b.catalog != nil {
		b.catalog.Invalidate()
	}
}

func queryLocalMobileCatalogFrom(ctx context.Context, env Env, query mobileproto.CatalogQuery, provider mobile.CatalogProvider) (mobileproto.CatalogSnapshot, error) {
	host, _ := os.Hostname()
	configGeneration, err := currentMobileConfigGeneration(ctx)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	snapshot, err := mobile.QueryCatalog(ctx, provider, mobileResolver(env), query, mobile.CatalogIdentity{
		HubID: host, OwnerHostID: "local:" + host, OwnerConfigGeneration: configGeneration,
	})
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	currentGeneration, err := currentMobileConfigGeneration(ctx)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	if currentGeneration != configGeneration {
		return mobileproto.CatalogSnapshot{}, fmt.Errorf("owner configuration changed during catalog collection")
	}
	return snapshot, nil
}

func newMobileCatalogRouter(ctx context.Context, env Env, catalog mobile.CatalogProvider) (*hosts.Registry, *mobilehub.RegistryDirectory, *mobilehub.CatalogRouter, error) {
	registry := hosts.NewRegistry(hosts.ClientOptions{})
	directory, err := mobilehub.NewRegistryDirectory(ctx, registry, mobileDirectoryProvider(env, catalog))
	if err != nil {
		registry.Stop()
		return nil, nil, nil, err
	}
	router, err := mobilehub.NewCatalogRouter(directory)
	if err != nil {
		registry.Stop()
		return nil, nil, nil, err
	}
	return registry, directory, router, nil
}

// mobileDirectoryProvider describes this hub's owners. The local owner serves
// its catalog from catalog; nil gives each local stream a private collection.
func mobileDirectoryProvider(env Env, catalog mobile.CatalogProvider) mobilehub.DirectoryProvider {
	if catalog == nil {
		catalog = mobileCatalogProvider(env)
	}
	hostname, _ := os.Hostname()
	localHostID := "local:" + hostname
	localRegistration := localMobileRegistrationFingerprint(hostname)
	return func(ctx context.Context) (mobilehub.CurrentDirectory, error) {
		cfg, err := config.Load()
		if err != nil {
			return mobilehub.CurrentDirectory{}, err
		}
		generation, err := currentMobileConfigGeneration(ctx)
		if err != nil {
			return mobilehub.CurrentDirectory{}, err
		}
		remotes := []mobilehub.RegisteredOwner{}
		if remoteHostsEnabled(env, cfg) {
			remotes = mobileRegisteredOwners(cfg)
		}
		localEndpoint := mobilehub.OwnerEndpoint{Host: mobileproto.CatalogHost{ID: localHostID, Name: hostname, State: "online", Local: true}, Bind: func(context.Context) (mobilehub.BoundOwner, error) {
			return mobilehub.BoundOwner{Authority: mobilehub.CatalogAuthority{OwnerHostID: localHostID, RegistrationFingerprint: localRegistration},
				Start: func(startCtx context.Context) (mobilehub.LineStream, mobileproto.Response, error) {
					return mobilehub.StartLocal(startCtx, func(input io.Reader, output io.Writer) (*mobile.Service, error) {
						return newMobileOwnerServiceWithCatalog(env, input, output, catalog)
					})
				}, Validate: func(validateCtx context.Context) error {
					current, err := currentMobileConfigGeneration(validateCtx)
					if err != nil {
						return err
					}
					if current != generation {
						return fmt.Errorf("mobile hub: local owner configuration changed")
					}
					return nil
				}}, nil
		}}
		return mobilehub.CurrentDirectory{Identity: mobile.CatalogIdentity{HubID: hostname, OwnerHostID: localHostID, OwnerConfigGeneration: generation},
			Local: localEndpoint, LocalRegistrationFingerprint: localRegistration, Remotes: remotes}, nil
	}
}

func mobileRegisteredOwners(cfg *config.Config) []mobilehub.RegisteredOwner {
	if cfg == nil {
		return nil
	}
	registered := make([]mobilehub.RegisteredOwner, 0, len(cfg.Hosts.List))
	seen := make(map[string]bool, len(cfg.Hosts.List))
	for _, entry := range cfg.Hosts.List {
		target := strings.TrimSpace(entry.Target)
		if target == "" {
			continue
		}
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			id = target
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		registered = append(registered, mobilehub.RegisteredOwner{Host: hosts.Host{ID: id, Target: target,
			RemoteBinary: strings.TrimSpace(entry.Binary), RemoteConfig: strings.TrimSpace(entry.Config), Env: append([]string(nil), entry.Env...)},
			Name: id, Disabled: entry.Disabled})
	}
	return registered
}

func localMobileRegistrationFingerprint(hostname string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{"local-mobile-owner", hostname, config.ConfigPath()}, "\x00")))
	return hex.EncodeToString(sum[:16])
}
