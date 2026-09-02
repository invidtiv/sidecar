package agentactivity

import (
	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// The screen lane: every observation Sidecar classifies is classified here, by
// Herdr's vendored manifests through the ported engine.
//
// There used to be a second lane — a Go rule table per provider — and a shadow
// mode that ran both and logged the differences. Phase 2 of
// docs/plans/active/herdr-detection-parity.md moved the providers over one at a
// time and deleted both. What is still Sidecar's, and lives in this file, is
// the process gate that refuses before any rule is evaluated, the two evidence
// strings no manifest can produce, and the fallback policy.

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
// It is true everywhere except Antigravity, and the exception is deliberate,
// pre-existing, and was re-examined and kept in the Phase 2 cutover.
//
// Upstream `antigravity.toml` has three rules and none of them is an idle rule,
// and neither Sidecar capture of an idle Antigravity pane matches anything at
// all (testdata/antigravity/idle_fallback.txt, interrupted.txt). So the fallback
// is that provider's only route to reporting a completed turn, and marking it
// low-evidence would make "done" unreachable for it; see
// TestRealAntigravityCompletedFallbackStillCreatesUnseenDone.
//
// The cost is stated rather than hidden: an Antigravity screen nobody has
// captured reads as a finished turn rather than as an unknown one. That is why
// the two speculative retain rules its old rule table carried were deleted
// rather than carried forward — see antigravity.go. Remove the exception the day
// upstream gains an idle rule, or a captured idle screen justifies an overlay
// one.
func fallbackIsLowEvidence(agent string) bool { return agent != "antigravity" }

// manifestInput is the observation as the engine takes it, and the two refusals
// that happen before any rule is evaluated.
//
// The bool reports whether there is anything to evaluate. When it is false the
// Result is final and no manifest was loaded, which is why the explain-producing
// path returns a nil record in that case: there is nothing to explain about
// rules that never ran.
func manifestInput(ob Observation) (*manifest.Compiled, manifest.Input, Result, bool) {
	agent := ob.Agent
	if !Supports(agent) {
		return nil, manifest.Input{}, Result{State: StateUnknown, Evidence: "unsupported-agent"}, false
	}
	if !processGate(agent, ob.CurrentCommand, ob) {
		return nil, manifest.Input{}, Result{State: StateUnknown, Evidence: agent + ".process-mismatch"}, false
	}
	compiled, _, err := manifests.Load(ManifestAgentID(agent))
	if err != nil {
		return nil, manifest.Input{}, Result{State: StateUnknown, Evidence: agent + ".manifest-unavailable"}, false
	}
	return compiled, manifest.Input{
		Screen: ob.Screen,
		Title:  ob.PaneTitle,
		// Progress is always empty under tmux: tmux consumes OSC 9;4 and
		// exposes no payload. The osc_progress rules still evaluate, to a
		// recorded no-match, which is how explain reports the gap honestly.
		Progress: "",
		Rows:     ob.PaneHeight,
	}, Result{}, true
}

// manifestVerdict translates one engine verdict into Sidecar's Result. The
// evidence strings and the fallback shape are Sidecar's, so a caller can compare
// this Result with a Go rule table's field by field; everything the verdict
// itself carries is upstream's.
func manifestVerdict(agent string, verdict manifest.Verdict) Result {
	if verdict.MatchedRule == nil {
		return Result{
			State:        StateIdle,
			Evidence:     agent + ".known-live-fallback",
			FallbackIdle: fallbackIsLowEvidence(agent),
		}
	}
	return Result{
		State:           State(verdict.State),
		Evidence:        verdict.MatchedRule.ID,
		VisibleIdle:     verdict.VisibleIdle,
		VisibleWorking:  verdict.VisibleWorking,
		VisibleBlocker:  verdict.VisibleBlocker,
		SkipStateUpdate: verdict.SkipStateUpdate,
	}
}

// DetectManifestResult classifies an observation through the vendored Herdr
// manifest for its agent and returns the verdict alone.
//
// This is the live path, and it is separate from DetectManifest for one reason:
// building an explain record costs an EvaluatedRule struct and a 240-character
// region preview per rule — around eighteen of each for Codex — and the poller
// runs every 200ms per visible pane and throws all of it away. Evaluate does the
// same work and allocates none of it.
func DetectManifestResult(ob Observation) Result {
	compiled, input, refused, ok := manifestInput(ob)
	if !ok {
		return refused
	}
	return manifestVerdict(ob.Agent, compiled.Evaluate(input))
}

// DetectManifest is DetectManifestResult with the explain record beside the
// verdict, for a caller that wants to show its reasoning: `sidecar agent
// explain`, the census, the differential harness.
//
// The Explain is nil when the process gate refused or the manifest could not be
// loaded: there is nothing to explain about rules that were never evaluated.
func DetectManifest(ob Observation) (Result, *manifest.Explain) {
	compiled, input, refused, ok := manifestInput(ob)
	if !ok {
		return refused, nil
	}
	verdict, explain := compiled.Explain(input)
	return manifestVerdict(ob.Agent, verdict), explain
}

// ExplainManifest is DetectManifest for a caller that only wants the record,
// such as `sidecar agent explain --file`.
func ExplainManifest(ob Observation) *manifest.Explain {
	_, explain := DetectManifest(ob)
	return explain
}
