package pluginhost

import (
	"log/slog"
	"os"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/resource"
)

// HostVersion identifies Sidecar to plugins. The app sets it once at startup;
// until then plugins see the placeholder the protocol examples use.
var HostVersion = "0.0.0"

// Options carries the host context a set of plugins is built with. Everything
// in it is resolved before any child runs.
type Options struct {
	// Dir is the neutral working directory every child gets — a Sidecar config
	// directory, never a selected repository.
	Dir string
	// HostEnv is the os.Environ()-shaped environment to draw the documented
	// base from. Nil means the current process environment.
	HostEnv []string
	// Runner overrides the process adapter. Nil means ExecRunner.
	Runner Runner
	// Log receives metadata-only invocation records.
	Log *slog.Logger
	// Home bounds declared watch paths. Empty resolves from HostEnv and then
	// from the OS.
	Home string
}

// FromConfig builds one CommandProvider per enabled terminal resource provider,
// in configuration order — which is matcher precedence — and returns the IDs of
// the instances that are configured but disabled.
//
// It performs no I/O: it does not look up the command on PATH, does not stat
// anything, and does not start a process. That is what makes it safe to call
// from a command after the first frame without having done anything before it.
func FromConfig(cfg config.TerminalResourcesConfig, opts Options) ([]Provider, []string, error) {
	instances := make([]config.PluginInstance, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		instances = append(instances, config.PluginInstance{
			PluginInstanceConfig: config.PluginInstanceConfig{
				ID:         p.ID,
				Command:    p.Command,
				PassEnv:    p.PassEnv,
				Enabled:    p.Enabled,
				Scope:      config.PluginScopeGlobal,
				Placements: []string{config.PluginPlacementPanes},
				Timeout:    p.Timeout,
				ClaimHosts: p.ClaimHosts,
			},
			Source: config.PluginSourceTerminalResources,
		})
	}
	return FromInstances(instances, opts)
}

// FromInstances builds one CommandProvider per enabled instance of the merged
// list, in order, and returns the IDs of the ones that are configured but off.
//
// The protocol identifier each instance is dispatched with comes from the
// section it was configured in, never from anything the executable says. That
// is the whole of how the two dialects coexist: a plugin cannot upgrade itself
// by answering with a different string, and a resource provider whose entry was
// never moved keeps being asked exactly what it has always been asked.
//
// It performs no I/O for the same reason FromConfig does not.
func FromInstances(instances []config.PluginInstance, opts Options) ([]Provider, []string, error) {
	hostEnv := opts.HostEnv
	if hostEnv == nil {
		hostEnv = os.Environ()
	}

	providers := make([]Provider, 0, len(instances))
	var disabled []string
	for _, p := range instances {
		if !p.Enabled {
			disabled = append(disabled, p.ID)
			continue
		}
		protocol := Protocol
		if p.IsLegacyResourceProvider() {
			protocol = resource.Protocol
		}
		cp, err := NewCommandProvider(CommandConfig{
			Instance:       p.ID,
			Argv:           p.Command,
			Dir:            opts.Dir,
			PassEnv:        p.PassEnv,
			ClaimHosts:     p.ClaimHosts,
			HostEnv:        hostEnv,
			ResolveTimeout: p.Timeout,
			Runner:         opts.Runner,
			Host:           HostInfo{Name: "sidecar", Version: HostVersion},
			Log:            opts.Log,
			Protocol:       protocol,
			Home:           opts.Home,
		})
		if err != nil {
			return nil, nil, err
		}
		providers = append(providers, cp)
	}
	return providers, disabled, nil
}

// configBoundsMatchProtocol is asserted by a test rather than at run time; it
// exists so the duplicated constants in internal/config cannot drift from the
// ones in internal/resource. internal/config stays a leaf package on purpose,
// which is why the numbers are stated twice at all.
var configBoundsMatchProtocol = [...]bool{
	config.MaxTerminalResourceProviders == resource.MaxProviders,
	config.MaxTerminalResourceProviderIDChars == resource.MaxInstanceIDChars,
	config.DefaultTerminalResourceTimeout == resource.DefaultResolveTimeout,
	config.MinTerminalResourceTimeout == resource.MinResolveTimeout,
	config.MaxTerminalResourceTimeout == resource.MaxResolveTimeout,
	config.MaxExternalPlugins == resource.MaxProviders,
	config.MaxExternalPluginIDChars == resource.MaxInstanceIDChars,
}
