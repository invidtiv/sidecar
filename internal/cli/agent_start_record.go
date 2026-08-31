package cli

import (
	"log/slog"
	"strings"

	"github.com/marcus/sidecar/internal/shellstate"
)

// recordStartedAgentKind writes the provider a successful `agent start` put in
// a shell onto that shell's manifest record.
//
// It is deliberately after the start rather than before it: the value recorded
// is a provider agentcontrol has already observed occupying the pane, not an
// intention. Recording an intention would put a claim in the manifest that the
// report gate then treats as evidence.
//
// A failure here is logged and swallowed. The agent is running by the time this
// runs, so turning a bookkeeping problem into a non-zero exit would tell the
// caller the start failed when it did not — and the only cost of the missing
// record is that the bind-time kind gate abstains for this shell, exactly as it
// did before this call existed.
func recordStartedAgentKind(tgt shellTarget, kind string) {
	kind = strings.TrimSpace(kind)
	if kind == "" || tgt.ManifestPath == "" || tgt.Session == "" {
		return
	}
	err := shellstate.RecordAgentKindAtPath(tgt.ManifestPath,
		shellstate.Identity{TmuxName: tgt.Session, Namespace: tgt.Namespace}, kind)
	if err != nil {
		slog.Debug("agent start: could not record the started kind",
			"shell", tgt.Session, "kind", kind, "error", err)
	}
}
