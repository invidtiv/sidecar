package agentactivity

import "strings"

// Muse Spark CLI's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/muse.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md). This file is what remains that
// is Sidecar's: the process gate.
//
// muse.toml is the one vendored manifest Herdr does not publish — it ships
// bundled only, at 2026.08.26.1, because Herdr will not publish a manifest for
// an agent whose process it cannot yet identify reliably. The loader does not
// care: `manifests.Load` reads the vendored file by name and the published
// catalog never enters it, which TestEveryClaimedProviderHasAVendoredManifest
// and TestMuseIsClassifiedByItsBundledOnlyManifest both pin. Identity is
// Sidecar's own: `identifyProcessName` claims `muse` and any `muse-` prefix,
// which is a superset of upstream's `muse`, `muse-code`, `muse-cli` and
// `muse-bin-<digit>`.
//
// The Go rule table is gone and this is the provider where that buys the most.
// It was harvested from Muse Code 1.0.1 in a single sitting and it shows:
// `muse.screen.working` matched the bare words "Thinking" or "Working"
// anywhere in the last sixteen lines, case-insensitively, so a turn that
// printed "still working on it" — or a *finished* turn whose transcript said
// so — kept the pane on the working lane. `muse.screen.blocked` was an
// eight-way alternation including "Proceed?" and "approve". Upstream's rules
// are pairs: `blocked_approval` requires "Allow this stage once" *and* "Always
// allow in this workspace" (or one of two older pairs), `pick_request_blocked`
// requires "Enter to select" *and* "Tab for an optional note",
// `workspace_trust_blocked` requires the trust question and its own control.
// Upstream's own comment says why: Muse emits any one of those phrases as
// ordinary assistant text after a completed turn.
//
// Upstream also carries two things Sidecar had no equivalent for — `menu_overlay`
// for the /theme and /skills menus, and `idle_status_fallback` for the
// `model · effort · cwd` footer — and one thing it does not: a title rule.
// `muse.title.working` read a braille frame in the pane title as working and
// upstream has no `osc_title` rule for Muse at all. That signal is dropped
// rather than overlaid: no fixture captured it, upstream's evidence note
// describes Muse's activity in terms of the `esc to interrupt` hint that
// `working_esc_interrupt` reads, and inventing a title rule for a provider
// whose manifest upstream has not yet published would be exactly the
// speculative rule this cutover is removing elsewhere.

// DetectMuse classifies a Muse pane. The process gate runs first and refuses
// before any manifest is evaluated; everything after it is upstream's.
func DetectMuse(ob Observation) Result {
	if ob.Agent != "muse" {
		return Result{State: StateUnknown, Evidence: "muse.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

func museProcess(command string) bool {
	return command == "muse" || strings.HasPrefix(command, "muse-")
}
