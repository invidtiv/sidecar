//go:build !darwin && !linux

package agentactivity

// Platforms with no process-identity implementation. Sidecar still works here;
// a pane running a shared runtime (node, bun) simply falls back to
// screen-chrome detection, so its provider is a guess rather than a fact.
//
// The host protocol's Capabilities.ProcessIdentity reports that honestly, so a
// viewer can say why a row's provider is uncertain instead of presenting the
// guess as truth.
func platformForegroundProcessGroup(int) int { return 0 }
func platformForegroundArgv0s(int) []string  { return nil }

const processIdentitySupported = false
