package config

import (
	"fmt"
	"strings"
	"time"
)

// Terminal resource provider bounds.
//
// These mirror the protocol document's Limits table and internal/resource's
// constants. They are restated here rather than imported because
// internal/config is deliberately a leaf package: pulling in the resource
// document types would drag the Markdown renderer into everything that reads
// configuration. TestTerminalResourceBoundsMatchTheProtocol in
// internal/resourceprovider fails if the two ever drift.
const (
	// MaxTerminalResourceProviders bounds how many instances may be configured.
	MaxTerminalResourceProviders = 16
	// MaxTerminalResourceProviderIDChars bounds an instance ID, which is a
	// persisted key.
	MaxTerminalResourceProviderIDChars = 64
	// DefaultTerminalResourceTimeout, MinTerminalResourceTimeout and
	// MaxTerminalResourceTimeout clamp a per-instance resolve timeout.
	DefaultTerminalResourceTimeout = 10 * time.Second
	MinTerminalResourceTimeout     = time.Second
	MaxTerminalResourceTimeout     = 60 * time.Second
)

// TerminalResourcesConfig is the app-level `terminalResources` section.
//
// It is app-level rather than per-plugin because providers serve both the
// project Workspace and the global Workspaces browser, and neither owns them.
// It is also deliberately explicit: Sidecar never scans a plugin directory,
// never executes every sidecar-* binary on PATH, and never lets a repository
// declare a provider. A repository must not be able to cause Sidecar to run
// code merely by being opened.
//
// A process boundary is crash isolation, not a sandbox. Enabling a provider
// trusts that executable with the user's full OS privileges.
type TerminalResourcesConfig struct {
	// Providers is ordered, and the order is matcher precedence.
	Providers []TerminalResourceProviderConfig `json:"providers,omitempty"`
}

// TerminalResourceProviderConfig is one configured provider instance.
type TerminalResourceProviderConfig struct {
	// ID is unique and stable. It is the persisted provider key and the
	// authoritative identity of the instance: a provider cannot rename itself.
	ID string `json:"id"`
	// Command is an argv array executed without a shell. The first element may
	// be an absolute path or resolve through PATH.
	Command []string `json:"command"`
	// PassEnv names variables whose current values are inherited on top of the
	// documented base environment. Names only — inline secret values are not
	// supported, and a passed value is never logged or rendered.
	PassEnv []string `json:"passEnv,omitempty"`
	// Enabled defaults to true; a configured instance is on unless it says
	// otherwise.
	Enabled bool `json:"enabled"`
	// Timeout is the per-resolve timeout, clamped to [1s, 60s].
	Timeout time.Duration `json:"timeout,omitempty"`
}

// EnabledProviders returns the enabled instances in configuration order.
func (c TerminalResourcesConfig) EnabledProviders() []TerminalResourceProviderConfig {
	out := make([]TerminalResourceProviderConfig, 0, len(c.Providers))
	for _, p := range c.Providers {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

// DisabledProviderIDs returns the IDs of instances that are configured but off.
// They keep a diagnostic status and contribute no matchers.
func (c TerminalResourcesConfig) DisabledProviderIDs() []string {
	var out []string
	for _, p := range c.Providers {
		if !p.Enabled {
			out = append(out, p.ID)
		}
	}
	return out
}

// validateTerminalResources normalizes and checks the section. Like the rest of
// Validate it repairs what it safely can — a clamped timeout, a trimmed ID —
// and only errors on something it cannot resolve without guessing at intent.
func validateTerminalResources(c *TerminalResourcesConfig) error {
	if len(c.Providers) > MaxTerminalResourceProviders {
		return fmt.Errorf("terminalResources: %d providers configured, the limit is %d",
			len(c.Providers), MaxTerminalResourceProviders)
	}

	seen := make(map[string]bool, len(c.Providers))
	for i := range c.Providers {
		p := &c.Providers[i]

		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			return fmt.Errorf("terminalResources: provider %d has no id", i)
		}
		if len([]rune(p.ID)) > MaxTerminalResourceProviderIDChars {
			return fmt.Errorf("terminalResources: provider id %q is longer than %d characters",
				p.ID, MaxTerminalResourceProviderIDChars)
		}
		if seen[p.ID] {
			return fmt.Errorf("terminalResources: provider id %q is configured more than once", p.ID)
		}
		seen[p.ID] = true

		argv := append(make([]string, 0, len(p.Command)), p.Command...)
		if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
			return fmt.Errorf("terminalResources: provider %q has no command", p.ID)
		}
		argv[0] = strings.TrimSpace(argv[0])
		p.Command = argv

		pass := make([]string, 0, len(p.PassEnv))
		for _, name := range p.PassEnv {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if strings.Contains(name, "=") {
				// An inline value is a secret in the config file. Refuse it
				// loudly rather than silently dropping it: the user needs to
				// know the credential they pasted is not being passed, and that
				// it should be removed from the file.
				return fmt.Errorf("terminalResources: provider %q passEnv entry %q looks like an inline value; passEnv takes variable names only",
					p.ID, strings.SplitN(name, "=", 2)[0]+"=…")
			}
			pass = append(pass, name)
		}
		if len(pass) == 0 {
			pass = nil
		}
		p.PassEnv = pass

		p.Timeout = clampTerminalResourceTimeout(p.Timeout)
	}
	return nil
}

// clampTerminalResourceTimeout brings a configured timeout into range. A
// non-positive value means "unset" and takes the default.
func clampTerminalResourceTimeout(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return DefaultTerminalResourceTimeout
	case d < MinTerminalResourceTimeout:
		return MinTerminalResourceTimeout
	case d > MaxTerminalResourceTimeout:
		return MaxTerminalResourceTimeout
	default:
		return d
	}
}
