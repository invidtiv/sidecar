// Package pluginhost runs terminal resource providers: it owns the
// process boundary, the protocol envelope, the compiled matcher snapshot, and
// the host-side cache and concurrency policy.
//
// Two seams, each narrow on purpose:
//
//   - Provider is the in-process adapter the Manager consumes. CommandProvider
//     is the default implementation; tests use an in-memory fake; a future
//     resident transport implements the same two methods.
//   - The executable protocol is the language-agnostic boundary. It returns
//     match declarations and resource data, never a Sidecar interface.
//
// A process boundary is crash isolation, not a sandbox. Enabling a provider
// trusts that executable with the user's full OS privileges.
//
// See docs/reference/terminal-resource-provider-protocol.md.
package pluginhost

import (
	"context"

	"github.com/marcus/sidecar/internal/resource"
)

// Provider is the whole of what the Manager may ask an implementation to do.
// Both methods must be safe for concurrent use and must honor ctx.
type Provider interface {
	// Instance is the configured provider instance ID. It is host-owned: a
	// provider cannot rename itself, and a response never changes it.
	Instance() string

	// Describe reports what the provider is and what it can recognize. It must
	// be local, fast, and non-interactive: no credential prompt, no network.
	Describe(ctx context.Context) (Description, error)

	// Resolve turns one locator into one document. It may perform network I/O.
	Resolve(ctx context.Context, ref resource.Reference) (resource.Document, error)
}

// Info is the informational identity a provider declares. None of it can
// rename or collide with a configured instance ID.
type Info struct {
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	// DocsURL is the only executable-declared Setup action Sidecar will
	// follow, and only after user confirmation. It passes the same http/https
	// validation as a resource's sourceUrl.
	DocsURL string `json:"docsUrl,omitempty"`
}

// Matcher is one declared resource-key shape. The pattern is Go/RE2 and the
// whole match is the locator: there are no capture-group templates and no
// provider code runs in the scanner.
type Matcher struct {
	ID       string `json:"id"`
	Pattern  string `json:"pattern"`
	Priority int    `json:"priority,omitempty"`
}

// Description is a validated describe result. Reaching this type means every
// pattern compiled, every ID was unique, and every bound held.
type Description struct {
	Info     Info
	Matchers []Matcher
}
