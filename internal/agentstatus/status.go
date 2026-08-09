// Package agentstatus resolves product-neutral agent activity and health into
// one presentation shared by human-facing views.
package agentstatus

import (
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

type LaneID string

const (
	LaneWorking LaneID = "working"
	LaneBlocked LaneID = "blocked"
	LaneDone    LaneID = "done"
	LaneIdle    LaneID = "idle"
	LanePaused  LaneID = "paused"
)

type Input struct {
	ProviderSupported bool
	Unavailable       bool
	Ambiguous         bool
	Missing           bool
	Orphaned          bool
	Paused            bool
	Err               bool
	LegacyStatus      string
	LegacyIcon        string
	Activity          agentactivity.Tracker
	CapturedAt        time.Time
	Now               time.Time
	StaleAfter        time.Duration
}

type Freshness string

const (
	FreshnessUnknown     Freshness = "unknown"
	FreshnessCurrent     Freshness = "current"
	FreshnessStale       Freshness = "stale"
	FreshnessUnavailable Freshness = "unavailable"
)

type Presentation struct {
	Lane       LaneID
	Icon       string
	Label      string
	Attention  bool
	Evidence   string
	ChangedAt  time.Time
	CapturedAt time.Time
	Health     bool
	Semantic   bool
	Freshness  Freshness
}

// Resolve applies health/liveness precedence before semantic activity. The
// conservative legacy projection is used only when a provider has no activity
// detector, keeping old integrations usable without manufacturing evidence.
func Resolve(in Input) Presentation {
	p := Presentation{CapturedAt: in.CapturedAt, Freshness: resolveFreshness(in)}
	switch {
	case in.Ambiguous:
		return health(p, "ambiguous", "?")
	case in.Unavailable:
		return health(p, "unavailable", "?")
	case in.Missing:
		p.Freshness = FreshnessUnavailable
		return health(p, "folder missing", "✗")
	case in.Orphaned:
		p.Freshness = FreshnessUnavailable
		return health(p, "session ended", "⚠")
	case in.Err:
		return health(p, "error", choose(in.LegacyIcon, "✗"))
	case in.Paused:
		return health(p, "paused", choose(in.LegacyIcon, "⏸"))
	}

	if in.ProviderSupported {
		p.Semantic = true
		p.Evidence = in.Activity.Evidence
		p.ChangedAt = in.Activity.ChangedAt
		switch in.Activity.DisplayState() {
		case string(agentactivity.StateWorking):
			p.Lane, p.Icon, p.Label = LaneWorking, "●", "working"
		case string(agentactivity.StateBlocked):
			p.Lane, p.Icon, p.Label = LaneBlocked, "◆", "blocked"
			p.Attention = p.Freshness == FreshnessCurrent && in.Activity.VisibleBlocker
		case "done":
			p.Lane, p.Icon, p.Label = LaneDone, "✓", "done"
		case string(agentactivity.StateIdle):
			p.Lane, p.Icon, p.Label = LaneIdle, "○", "idle"
		default:
			p.Lane, p.Icon, p.Label = LanePaused, "?", "unknown"
		}
		return p
	}

	p.Icon = in.LegacyIcon
	switch in.LegacyStatus {
	case "active", "thinking":
		p.Lane = LaneWorking
	case "waiting":
		p.Lane = LaneBlocked
	case "done":
		p.Lane = LaneDone
	default:
		p.Lane = LanePaused
	}
	return p
}

func resolveFreshness(in Input) Freshness {
	if in.Unavailable || in.Ambiguous {
		return FreshnessUnavailable
	}
	if in.CapturedAt.IsZero() {
		return FreshnessUnknown
	}
	if in.StaleAfter > 0 && !in.Now.IsZero() && in.Now.Sub(in.CapturedAt) > in.StaleAfter {
		return FreshnessStale
	}
	return FreshnessCurrent
}

func health(p Presentation, label, icon string) Presentation {
	p.Lane, p.Label, p.Icon, p.Health = LanePaused, label, icon, true
	return p
}

func choose(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
