package resourceprovider

import (
	"log/slog"
	"os"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/resource"
)

// HostVersion identifies Sidecar to providers. The app sets it once at startup;
// until then providers see the placeholder the protocol examples use.
var HostVersion = "0.0.0"

// Options carries the host context a set of providers is built with. Everything
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
}

// FromConfig builds one CommandProvider per enabled configured instance, in
// configuration order — which is matcher precedence — and returns the IDs of
// the instances that are configured but disabled.
//
// It performs no I/O: it does not look up the command on PATH, does not stat
// anything, and does not start a process. That is what makes it safe to call
// from a command after the first frame without having done anything before it.
func FromConfig(cfg config.TerminalResourcesConfig, opts Options) ([]Provider, []string, error) {
	hostEnv := opts.HostEnv
	if hostEnv == nil {
		hostEnv = os.Environ()
	}

	enabled := cfg.EnabledProviders()
	providers := make([]Provider, 0, len(enabled))
	for _, p := range enabled {
		cp, err := NewCommandProvider(CommandConfig{
			Instance:       p.ID,
			Argv:           p.Command,
			Dir:            opts.Dir,
			PassEnv:        p.PassEnv,
			HostEnv:        hostEnv,
			ResolveTimeout: p.Timeout,
			Runner:         opts.Runner,
			Host:           HostInfo{Name: "sidecar", Version: HostVersion},
			Log:            opts.Log,
		})
		if err != nil {
			return nil, nil, err
		}
		providers = append(providers, cp)
	}
	return providers, cfg.DisabledProviderIDs(), nil
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
}
