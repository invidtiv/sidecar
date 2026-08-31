// Package agentresolve is the one place Sidecar turns an observation of a pane
// into that pane's effective agent activity.
//
// Before this package there were three copies of the same four lines — project
// worktree polling, project managed-shell polling, and workspaceinventory each
// built an [agentactivity.Observation] and called [agentactivity.Detect] on it.
// Three copies was survivable while screen detection was the only evidence.
// It stops being survivable the moment a second kind of evidence exists,
// because then each copy has to decide which one wins, and three independent
// answers to that question is exactly the split-brain the M6 plan forbids.
//
// So the rule is: every surface builds its own observation — they genuinely
// differ in how they capture a screen and what they know about a pane — and
// then they all call [Resolve]. No surface holds a private opinion about
// authority.
//
// # The nil source is the important case
//
// A nil [Source], or a source with nothing to say about this pane, produces
// exactly what [agentactivity.Detect] produced before this package existed:
// same result, same evidence string, same flags. That is not a convenience, it
// is the safety property that made this extraction possible to verify. The
// Phase A compatibility fixtures pin the old behavior and were not modified;
// they pass because the no-evidence path through here is the identity
// function.
package agentresolve

import (
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// Evidence is everything a lifecycle source knows about one pane.
//
// It is deliberately the resolver's input shape rather than a smaller one: the
// arbitration in [agentlifecycle.Resolve] needs identity, capability, status,
// liveness, and the latest report together, and assembling that in two places
// would be a second chance to assemble it differently.
type Evidence struct {
	// Live is the pane and run identity as the source currently understands it.
	Live agentlifecycle.Identity
	// ProcessAlive reports whether the matching provider process is running.
	ProcessAlive bool

	// Capability and Status describe the installed integration.
	Capability agentlifecycle.Capability
	Status     agentlifecycle.IntegrationStatus
	// ProviderInTestedRange reports whether the detected provider version falls
	// inside the range the capability's tier was proved against.
	ProviderInTestedRange bool

	// Latest is the newest valid report for the pane, or nil.
	Latest *agentlifecycle.Report

	// StoreUnavailable and InvalidReports withdraw authority without claiming
	// anything about the pane.
	StoreUnavailable bool
	InvalidReports   bool
}

// PaneRef names the pane an observation is about.
//
// Two fields rather than one because the three call sites genuinely know
// different things and neither can be derived from the other for free.
// workspaceinventory already holds the tmux %pane id; the two project polling
// paths hold only the session name, and the capture format strings that would
// have to carry a pane id are asserted by terminal tests and sit on the hot
// path. Rather than widen those, the reference carries whichever is available
// and a [Source] resolves the rest — once, and cached, because the mapping only
// changes when a pane is split or closed.
type PaneRef struct {
	// PaneID is the tmux "%7"-shaped identifier, when the caller knows it.
	PaneID string
	// Session is the tmux session name, which every caller knows.
	Session string

	// Identity is what the caller already knows about the pane's machine, tmux
	// server, and provider, used only to fill in the explanation when no
	// lifecycle evidence applies.
	//
	// It exists because that path previously reported an identity consisting of
	// a pane id and nothing else, so `agent explain` on a pane with no
	// integration printed an empty server field — which reads as "Sidecar could
	// not determine the server" rather than "there was no report to check one
	// against". A caller that has already paid for the answer can supply it; a
	// polling caller leaves it zero and gets exactly the previous behavior.
	//
	// It is never consulted when a source does supply evidence: arbitration
	// builds the live identity from what the source observed, and letting a
	// caller inject one there would make every identity check compare a value
	// with itself.
	Identity agentlifecycle.Identity
}

// Empty reports whether the reference names nothing usable.
func (p PaneRef) Empty() bool { return p.PaneID == "" && p.Session == "" }

// Source supplies lifecycle evidence for a pane.
//
// Implementations must be safe to call from a polling command and must not
// block: a slow source would stall a surface's refresh, and the correct answer
// when evidence cannot be fetched quickly is "no evidence", which degrades to
// ordinary screen detection.
type Source interface {
	// Evidence returns what is known about a pane. The bool reports whether
	// any lifecycle evidence applies at all; false means pure screen fallback
	// and the Evidence value is ignored.
	Evidence(ref PaneRef) (Evidence, bool)
}

// Resolve produces the effective activity result for one observation.
//
// The screen detectors always run. Arbitration chooses between evidence; it
// never decides whether to gather it, because a pane whose integration turns
// out to be stale needs a screen answer already in hand rather than a second
// capture round-trip.
//
// An empty ref is treated as "no lifecycle evidence" rather than as an error:
// an unidentifiable pane is exactly the case where screen detection is the only
// honest answer.
func Resolve(ob agentactivity.Observation, ref PaneRef, src Source, now time.Time) agentlifecycle.Decision {
	screen := agentactivity.Detect(ob)

	ev, ok := lookup(ref, src)
	if !ok {
		// The identity path. Everything a caller sees is what Detect returned,
		// and the explanation says plainly that no integration applied.
		return agentlifecycle.Decision{
			Result: screen,
			Explanation: agentlifecycle.Explanation{
				SchemaVersion:  agentlifecycle.SchemaVersion,
				State:          screen.State,
				Authority:      agentlifecycle.AuthorityScreen,
				Tier:           agentlifecycle.TierScreenFallback,
				Freshness:      agentlifecycle.FreshnessNone,
				FallbackReason: agentlifecycle.ReasonNoIntegration,
				ScreenState:    screen.State,
				ScreenEvidence: screen.Evidence,
				Identity:       noEvidenceIdentity(ref),
			},
		}
	}

	return agentlifecycle.Resolve(agentlifecycle.Input{
		Now:                   now,
		Live:                  ev.Live,
		ProcessAlive:          ev.ProcessAlive,
		Capability:            ev.Capability,
		Status:                ev.Status,
		ProviderInTestedRange: ev.ProviderInTestedRange,
		Latest:                ev.Latest,
		StoreUnavailable:      ev.StoreUnavailable,
		InvalidReports:        ev.InvalidReports,
		Screen:                screen,
	})
}

// Result is the common case: callers that only need the lane and not the
// diagnostic. It exists so the three call sites read as a one-line swap for the
// Detect call they replaced rather than growing a decision-unpacking dance.
func Result(ob agentactivity.Observation, ref PaneRef, src Source, now time.Time) agentactivity.Result {
	return Resolve(ob, ref, src, now).Result
}

// noEvidenceIdentity is the identity reported when nothing but the screen had
// an opinion. The pane id in the reference wins over one in the supplied
// identity, because the reference is what was actually resolved.
func noEvidenceIdentity(ref PaneRef) agentlifecycle.Identity {
	id := ref.Identity
	if ref.PaneID != "" {
		id.PaneID = ref.PaneID
	}
	return id
}

func lookup(ref PaneRef, src Source) (Evidence, bool) {
	// A nil interface and an interface holding a nil pointer are different
	// values and both reach here in practice, because a surface with the
	// feature off stores an untyped nil while one whose construction failed may
	// store a typed one.
	if src == nil || ref.Empty() {
		return Evidence{}, false
	}
	return src.Evidence(ref)
}
