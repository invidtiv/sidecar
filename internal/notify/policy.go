package notify

import (
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// LiveEventGrace is the age window in which a notification discovered through
// a cross-process sweep is still live rather than startup backlog.
const LiveEventGrace = 15 * time.Second

type Channel string

const (
	ChannelNative Channel = "native"
	ChannelSound  Channel = "sound"
)

type Cue string

const (
	CueNone      Cue = "none"
	CueAttention Cue = "attention"
	CueDone      Cue = "done"
	CueFailure   Cue = "failure"
)

// Reason is a stable machine-readable delivery outcome.
type Reason string

const (
	ReasonChannelOff     Reason = "channel_off"
	ReasonSourceOff      Reason = "source_off"
	ReasonForeground     Reason = "foreground"
	ReasonQuietHours     Reason = "quiet_hours"
	ReasonStale          Reason = "stale"
	ReasonUnavailable    Reason = "unavailable"
	ReasonAlreadyClaimed Reason = "already_claimed"
	ReasonCancelled      Reason = "cancelled"
	ReasonRateLimited    Reason = "rate_limited"
	ReasonNotRequested   Reason = "not_requested"
	ReasonCoordination   Reason = "coordination_failed"
)

type CapabilitySet struct {
	Native bool
	Sound  bool
}

// RuntimeContext contains only already-resolved facts. ResolveDelivery does no
// I/O and is shared unchanged by TUI, CLI, tests, and future headless callers.
type RuntimeContext struct {
	Now          time.Time
	Foreground   bool
	Discovered   bool
	ExplicitTest bool
	Capabilities CapabilitySet
}

type ChannelDecision struct {
	Deliver bool   `json:"deliver"`
	Reason  Reason `json:"reason,omitempty"`
}

type DeliveryDecision struct {
	Native ChannelDecision `json:"native"`
	Sound  ChannelDecision `json:"sound"`
	Cue    Cue             `json:"cue"`
}

// ResolveDelivery applies the complete state-free delivery policy.
func ResolveDelivery(n Notification, cfg ResolvedConfig, runtime RuntimeContext) DeliveryDecision {
	now := runtime.Now
	rule := cfg.SourceRule(n.Source)
	cue := resolveCue(n, rule.Sound)
	return DeliveryDecision{
		Native: decideChannel(cfg.NativeMode, rule.Native, runtime.Capabilities.Native, n, cfg, runtime, now),
		Sound:  decideChannel(cfg.SoundMode, cue != CueNone, runtime.Capabilities.Sound, n, cfg, runtime, now),
		Cue:    cue,
	}
}

func decideChannel(mode config.DeliveryMode, sourceOn, available bool, n Notification, cfg ResolvedConfig, runtime RuntimeContext, now time.Time) ChannelDecision {
	// Dismissal is authoritative lifecycle state, not a policy preference.
	// Even an explicit provider test must never resurrect a dismissed record.
	if n.Dismissed() {
		return ChannelDecision{Reason: ReasonCancelled}
	}
	if mode == config.DeliveryOff {
		return ChannelDecision{Reason: ReasonChannelOff}
	}
	if !sourceOn {
		return ChannelDecision{Reason: ReasonSourceOff}
	}
	if !available {
		return ChannelDecision{Reason: ReasonUnavailable}
	}
	if !runtime.ExplicitTest {
		// A zero time means the caller has not supplied a clock fact. Keep the
		// pure resolver deterministic by skipping only time-dependent refusals.
		if !now.IsZero() && inQuietHours(cfg.QuietHours, now) {
			return ChannelDecision{Reason: ReasonQuietHours}
		}
		if runtime.Discovered && !now.IsZero() && now.UTC().Sub(n.CreatedAt.UTC()) > LiveEventGrace {
			return ChannelDecision{Reason: ReasonStale}
		}
		if mode == config.DeliveryBackground && runtime.Foreground {
			return ChannelDecision{Reason: ReasonForeground}
		}
	}
	return ChannelDecision{Deliver: true}
}

func resolveCue(n Notification, configured config.SoundCue) Cue {
	switch configured {
	case config.SoundNone:
		return CueNone
	case config.SoundAttention:
		return CueAttention
	case config.SoundDone:
		return CueDone
	case config.SoundFailure:
		return CueFailure
	case config.SoundEvent:
		if n.Severity == SeverityError {
			return CueFailure
		}
		if n.Source == SourceWaiting {
			return CueAttention
		}
		return CueDone
	default:
		return CueNone
	}
}

func inQuietHours(q ResolvedQuietHours, now time.Time) bool {
	if !q.Enabled {
		return false
	}
	minute := now.Hour()*60 + now.Minute()
	if q.StartMinute == q.EndMinute {
		return true
	}
	if q.StartMinute < q.EndMinute {
		return minute >= q.StartMinute && minute < q.EndMinute
	}
	return minute >= q.StartMinute || minute < q.EndMinute
}
