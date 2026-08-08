// Package agentactivity classifies live agent terminal observations without
// depending on tmux, Bubble Tea, or persistent conversation storage.
package agentactivity

import (
	"regexp"
	"strings"
	"time"
)

type State string

const (
	StateUnknown State = "unknown"
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateBlocked State = "blocked"
)

type Observation struct {
	Agent          string
	Screen         string
	PaneTitle      string
	CurrentCommand string
	CapturedAt     time.Time
}

type Result struct {
	State           State
	Evidence        string
	VisibleIdle     bool
	VisibleWorking  bool
	VisibleBlocker  bool
	SkipStateUpdate bool
}

type Region string

const (
	RegionScreen    Region = "screen"
	RegionLastLines Region = "last_lines"
	RegionTitle     Region = "pane_title"
)

type Rule struct {
	ID       string
	State    State
	Region   Region
	Contains []string
	Regexp   *regexp.Regexp
	Not      []string
	LastN    int
	Skip     bool
}

// Detect dispatches an observation to the expected provider probe. Keeping
// dispatch here lets the workspace poll remain product-neutral while each
// provider owns its evidence table.
func Detect(ob Observation) Result {
	switch ob.Agent {
	case "codex":
		return DetectCodex(ob)
	case "claude":
		return DetectClaude(ob)
	case "grok":
		return DetectGrok(ob)
	case "antigravity":
		return DetectAntigravity(ob)
	default:
		return Result{State: StateUnknown, Evidence: "unsupported-agent"}
	}
}

// Evaluate applies rules in caller-supplied priority order. Rules are kept
// deliberately small: provider files own their evidence and ordering.
func Evaluate(ob Observation, rules []Rule) Result {
	for _, rule := range rules {
		text := regionText(ob, rule)
		if !matches(text, rule) {
			continue
		}
		result := Result{State: rule.State, Evidence: rule.ID, SkipStateUpdate: rule.Skip}
		result.VisibleIdle = rule.State == StateIdle && !rule.Skip
		result.VisibleWorking = rule.State == StateWorking && !rule.Skip
		result.VisibleBlocker = rule.State == StateBlocked && !rule.Skip
		return result
	}
	return Result{State: StateUnknown, Evidence: "no-match"}
}

func regionText(ob Observation, rule Rule) string {
	switch rule.Region {
	case RegionTitle:
		return ob.PaneTitle
	case RegionLastLines:
		lines := strings.Split(ob.Screen, "\n")
		n := rule.LastN
		if n <= 0 {
			n = 12
		}
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		return strings.Join(lines, "\n")
	default:
		return ob.Screen
	}
}

func matches(text string, rule Rule) bool {
	for _, excluded := range rule.Not {
		if strings.Contains(text, excluded) {
			return false
		}
	}
	for _, literal := range rule.Contains {
		if !strings.Contains(text, literal) {
			return false
		}
	}
	return rule.Regexp == nil || rule.Regexp.MatchString(text)
}

// Tracker owns transition policy while Evaluate remains state-free.
type Tracker struct {
	State           State
	Evidence        string
	ChangedAt       time.Time
	Seen            bool
	idleCandidateAt time.Time
}

const IdleDebounce = 400 * time.Millisecond

func (t *Tracker) Apply(result Result, now time.Time) bool {
	if result.SkipStateUpdate {
		return false
	}
	if result.State == StateIdle {
		if t.State == StateIdle {
			t.idleCandidateAt = time.Time{}
			return false
		}
		if t.idleCandidateAt.IsZero() {
			t.idleCandidateAt = now
			return false
		}
		if now.Sub(t.idleCandidateAt) < IdleDebounce {
			return false
		}
	} else {
		t.idleCandidateAt = time.Time{}
	}
	if result.State == t.State && result.Evidence == t.Evidence {
		return false
	}
	previous := t.State
	t.State = result.State
	t.Evidence = result.Evidence
	t.ChangedAt = now
	if result.State == StateWorking || result.State == StateBlocked {
		t.Seen = false
	} else if result.State == StateIdle {
		// Initial/restart idle is quiet; only a transition from live work creates done.
		t.Seen = previous == StateUnknown || previous == ""
	}
	return true
}

func (t *Tracker) Acknowledge() { t.Seen = true }

func (t Tracker) DisplayState() string {
	if t.State == StateIdle && !t.Seen {
		return "done"
	}
	return string(t.State)
}
