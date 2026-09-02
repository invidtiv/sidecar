//go:build !darwin && !linux

package agentactivity

// Platforms with no process-identity implementation. Sidecar still works here;
// a pane running a shared runtime (node, bun) simply falls back to
// screen-chrome detection, so its provider is a guess rather than a fact.
//
// The host protocol's Capabilities.ProcessIdentity reports that honestly, so a
// viewer can say why a row's provider is uncertain instead of presenting the
// guess as truth.
//
// platformProcessAgentHint answers nothing here for the same reason: without a
// way to read another process's environment there is no hint to find, and
// AgentHintEnv set in Sidecar's *own* environment would name the agent in some
// other pane. A hint is evidence about a specific process or it is not evidence.
func platformForegroundProcessGroup(int) int              { return 0 }
func platformForegroundProcesses(int) []foregroundProcess { return nil }
func platformProcessAgentHint(int) string                 { return "" }

const processIdentitySupported = false
