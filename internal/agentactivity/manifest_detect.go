package agentactivity

import (
	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// The manifest lane: the same observation, classified by Herdr's vendored
// manifests through the ported engine instead of by Sidecar's Go rule tables.
//
// In this phase nothing here authors a user-visible verdict. Detect still
// returns the Go tables' answer; when features.ManifestDetection is on, the two
// are compared and disagreements are logged (shadow.go). Phase 2 flips the
// providers over one at a time.

// ManifestAgentID maps a Sidecar provider family to the Herdr agent id whose
// manifest classifies it.
//
// One mapping is not the identity: Sidecar's family is `copilot` and Herdr's
// manifest is `github-copilot.toml`. Everything else Sidecar claims shares
// Herdr's spelling, including `antigravity` (whose *manifest id* is "agy" while
// its file is antigravity.toml — the file name is what the loader keys on).
// Keeping the whole mapping in one function is what stops a second, subtly
// different copy appearing next to a new call site.
func ManifestAgentID(agent string) string {
	switch agent {
	case "copilot":
		return "github-copilot"
	default:
		return agent
	}
}

// HerdrAgentLabel maps a Sidecar provider family to Herdr's *label* for it —
// the string `agent_label` returns (src/detect/mod.rs:121 at e2b85c7), which is
// what `herdr agent explain --agent` takes, what keys its alias table, and what
// names its local override file.
//
// It is a second, different mapping from ManifestAgentID, and the difference is
// not tidy: Herdr's Antigravity manifest lives in antigravity.toml but its label
// is "agy", and its Copilot manifest lives in github-copilot.toml but its label
// is "copilot". Conflating the two silently reads the wrong file for one of the
// two providers, so both mappings are spelled out rather than derived.
func HerdrAgentLabel(agent string) string {
	switch agent {
	case "antigravity":
		return "agy"
	default:
		return agent
	}
}

// processGate applies the per-provider process refusal without evaluating any
// rules. It is Sidecar's, not Herdr's, and it is stricter: Herdr will happily
// evaluate claude.toml against whatever is on a pane it believes is Claude,
// while Sidecar refuses unless the foreground command is Claude or a permitted
// runtime wrapper. That refusal stays through the cutover, so it lives here
// rather than inside the engine.
func processGate(agent, command string, ob Observation) bool {
	switch agent {
	case "claude":
		return claudeProcess(command)
	case "codex":
		return codexProcess(command)
	case "grok":
		return grokProcess(command)
	case "antigravity":
		return antigravityProcess(command)
	case "pi":
		return piProcess(command)
	case "copilot":
		return copilotProcess(command)
	case "cursor":
		// Cursor is the one provider whose gate reads more than the command
		// name: `agent` and `node` are shared, so a resolved argv[0] counts too.
		return cursorProcess(command) || ob.ProcessIdentity == "cursor"
	case "opencode":
		return openCodeProcess(command)
	case "amp":
		return ampProcess(command)
	case "muse":
		return museProcess(command)
	default:
		return false
	}
}

// fallbackIsLowEvidence reports whether a provider's no-match idle should be
// marked FallbackIdle.
//
// It is true everywhere except Antigravity, and the exception is deliberate and
// pre-existing: Antigravity has no stable explicit idle rule, so its fallback is
// its only route to reporting a completed turn, and marking it low-evidence
// would make "done" unreachable for that provider (see antigravity.go and
// TestRealAntigravityCompletedFallbackStillCreatesUnseenDone). The manifest lane
// mirrors it so a shadow-mode disagreement means the two engines really read the
// screen differently, rather than flagging a bookkeeping difference on every
// idle Antigravity pane. Phase 2 decides whether to keep the exception.
func fallbackIsLowEvidence(agent string) bool { return agent != "antigravity" }

// DetectManifest classifies an observation through the vendored Herdr manifest
// for its agent, and returns the explain record alongside the verdict.
//
// The process gate, the evidence strings, and the fallback shape are Sidecar's,
// so a caller can compare this Result with Detect's field by field. Everything
// between them — which rules exist, which regions they read, which one wins —
// is upstream's.
//
// The Explain is nil when the process gate refused or the manifest could not be
// loaded: there is nothing to explain about rules that were never evaluated.
func DetectManifest(ob Observation) (Result, *manifest.Explain) {
	agent := ob.Agent
	if !Supports(agent) {
		return Result{State: StateUnknown, Evidence: "unsupported-agent"}, nil
	}
	if !processGate(agent, ob.CurrentCommand, ob) {
		return Result{State: StateUnknown, Evidence: agent + ".process-mismatch"}, nil
	}

	compiled, _, err := manifests.Load(ManifestAgentID(agent))
	if err != nil {
		return Result{State: StateUnknown, Evidence: agent + ".manifest-unavailable"}, nil
	}

	verdict, explain := compiled.Explain(manifest.Input{
		Screen: ob.Screen,
		Title:  ob.PaneTitle,
		// Progress is always empty under tmux: tmux consumes OSC 9;4 and
		// exposes no payload. The osc_progress rules still evaluate, to a
		// recorded no-match, which is how explain reports the gap honestly.
		Progress: "",
		Rows:     ob.PaneHeight,
	})

	if verdict.MatchedRule == nil {
		return Result{
			State:        StateIdle,
			Evidence:     agent + ".known-live-fallback",
			FallbackIdle: fallbackIsLowEvidence(agent),
		}, explain
	}

	return Result{
		State:           State(verdict.State),
		Evidence:        verdict.MatchedRule.ID,
		VisibleIdle:     verdict.VisibleIdle,
		VisibleWorking:  verdict.VisibleWorking,
		VisibleBlocker:  verdict.VisibleBlocker,
		SkipStateUpdate: verdict.SkipStateUpdate,
	}, explain
}

// ExplainManifest is DetectManifest for a caller that only wants the record,
// such as `sidecar agent explain --file`.
func ExplainManifest(ob Observation) *manifest.Explain {
	_, explain := DetectManifest(ob)
	return explain
}
