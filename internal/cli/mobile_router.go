package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !remoteHostsEnabled(env, cfg) || len(cfg.Hosts.List) == 0 {
		return runMobileOwnerService(env)
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	registry, directory, router, err := newMobileCatalogRouter(ctx, env)
	if err != nil {
		return err
	}
	defer registry.Stop()
	// Start configured owner observers before the phone's first catalog request.
	// Snapshot is nonblocking with respect to SSH; the first request can retain
	// local rows while a slow owner remains explicitly connecting.
	if _, err := directory.Snapshot(ctx); err != nil {
		return err
	}
	broker, err := mobilehub.NewProtocolBroker(router)
	if err != nil {
		return err
	}
	return broker.Run(ctx, env.Stdin, env.Stdout)
}

func queryMobileCatalog(env Env, query mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	cfg, err := config.Load()
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if !remoteHostsEnabled(env, cfg) || len(cfg.Hosts.List) == 0 {
		return queryLocalMobileCatalog(ctx, env, query)
	}
	queryCtx, cancel := context.WithTimeout(ctx, mobileSessionsTimeout)
	defer cancel()
	registry, directory, router, err := newMobileCatalogRouter(queryCtx, env)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	defer registry.Stop()
	if err := directory.WaitInitial(queryCtx, mobileInitialOwnerWait); err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	return router.Query(queryCtx, query)
}

func queryLocalMobileCatalog(ctx context.Context, env Env, query mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	host, _ := os.Hostname()
	configGeneration, err := currentMobileConfigGeneration(ctx)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	snapshot, err := mobile.QueryCatalog(ctx, mobileCatalogProvider(env), mobileResolver(env), query, mobile.CatalogIdentity{
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

func newMobileCatalogRouter(ctx context.Context, env Env) (*hosts.Registry, *mobilehub.RegistryDirectory, *mobilehub.CatalogRouter, error) {
	registry := hosts.NewRegistry(hosts.ClientOptions{})
	directory, err := mobilehub.NewRegistryDirectory(ctx, registry, mobileDirectoryProvider(env))
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

func mobileDirectoryProvider(env Env) mobilehub.DirectoryProvider {
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
						return newMobileOwnerService(env, input, output)
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
