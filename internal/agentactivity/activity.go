// Package agentactivity classifies live agent terminal observations without
// depending on tmux, Bubble Tea, or persistent conversation storage.
package agentactivity

import (
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
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
	// FallbackIdle distinguishes provider-owned explicit idle evidence from the
	// conservative no-match policy for a positively identified live process.
	// Fallback idle may establish/display idle, but cannot announce completion.
	FallbackIdle bool
}

var (
	semanticVersionCommand = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	claudeScreenIdentity   = regexp.MustCompile(`(?ms)(^─{8,}\s*$\n^❯.*$\n^─{8,}\s*$|manual mode on · \? for shortcuts)`)
	codexScreenIdentity    = regexp.MustCompile(`(?im)(^OpenAI Codex \(v[^)]+\)\s*$|^• Working \(.*esc to interrupt\)\s*$|^\s*\d+\. No, and tell Codex what to do differently.*$|^› Write tests for @filename\s*$|^/ T R A N S C R I P T /\s*$)`)
)

// Identify returns the live program owning an agent pane when the existing
// tmux metadata or current UI makes that identity unambiguous. It deliberately
// returns an empty string for shared runtimes such as node and bun unless the
// current screen distinguishes the provider. Callers can retain their prior
// identity in that case without paying for a process-tree scan.
func Identify(ob Observation) string {
	command := strings.ToLower(strings.TrimSpace(ob.CurrentCommand))
	switch {
	case command == "claude" || semanticVersionCommand.MatchString(command):
		return "claude"
	case command == "codex" || command == "codex-cli":
		return "codex"
	case command == "grok" || strings.HasPrefix(command, "grok-"):
		return "grok"
	case command == "agy" || command == "antigravity":
		return "antigravity"
	case command == "pi":
		return "pi"
	case oneOf(command, "copilot", "github-copilot", "ghcs"):
		return "copilot"
	case oneOf(command, "cursor-agent", "cursor"):
		return "cursor"
	case oneOf(command, "opencode", "open-code"):
		return "opencode"
	case oneOf(command, "amp", "amp-local"):
		return "amp"
	case oneOf(command, "sh", "bash", "zsh", "fish", "nu", "pwsh"):
		return "shell"
	}

	if command != "node" && command != "bun" {
		return ""
	}
	current := ansi.Strip(regionText(ob, Rule{Region: RegionCurrent, LastN: 24}))
	if claudeScreenIdentity.MatchString(current) {
		return "claude"
	}
	if codexScreenIdentity.MatchString(current) {
		return "codex"
	}
	return ""
}

type Region string

const (
	RegionScreen    Region = "screen"
	RegionLastLines Region = "last_lines"
	RegionCurrent   Region = "current_bottom"
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
	case "pi":
		return DetectPi(ob)
	case "copilot":
		return DetectCopilot(ob)
	case "cursor":
		return DetectCursor(ob)
	case "opencode":
		return DetectOpenCode(ob)
	case "amp":
		return DetectAmp(ob)
	default:
		return Result{State: StateUnknown, Evidence: "unsupported-agent"}
	}
}

// Supports reports whether Sidecar has provider-owned activity evidence rules.
func Supports(agent string) bool {
	switch agent {
	case "codex", "claude", "grok", "antigravity", "pi", "copilot", "cursor", "opencode", "amp":
		return true
	default:
		return false
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
	case RegionCurrent:
		// capture-pane includes historical scrollback and preserves a tall
		// pane's trailing blank rows. Drop only that padding, then inspect a
		// bounded current-bottom window so resolved historical UI cannot win.
		lines := strings.Split(ob.Screen, "\n")
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		n := rule.LastN
		if n <= 0 {
			n = 24
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
	VisibleBlocker  bool
	idleCandidateAt time.Time
}

const IdleDebounce = 400 * time.Millisecond

// ResetForProcessChange clears semantic state from the prior pane owner while
// allowing a confirmed new process's first explicit idle observation to land
// immediately. That first idle is initialization, not a completion event.
func (t *Tracker) ResetForProcessChange(now time.Time) {
	*t = Tracker{
		State:           StateUnknown,
		Evidence:        "live-process-changed",
		Seen:            true,
		idleCandidateAt: now.Add(-IdleDebounce),
	}
}

func (t *Tracker) Apply(result Result, now time.Time) bool {
	// Visibility belongs to the current capture, not the retained semantic
	// state. Overlay/viewer captures may deliberately retain StateBlocked, but
	// they must not retain evidence that the blocker is still on screen.
	t.VisibleBlocker = result.State == StateBlocked && result.VisibleBlocker && !result.SkipStateUpdate
	if result.SkipStateUpdate {
		return false
	}
	if result.State == StateIdle {
		if t.State == StateIdle {
			t.idleCandidateAt = time.Time{}
			return false
		}
		if result.VisibleIdle {
			t.idleCandidateAt = time.Time{}
		} else {
			if t.idleCandidateAt.IsZero() {
				t.idleCandidateAt = now
				return false
			}
			if now.Sub(t.idleCandidateAt) < IdleDebounce {
				return false
			}
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
	switch result.State {
	case StateWorking, StateBlocked:
		t.Seen = false
	case StateIdle:
		// Initial/restart idle is quiet; only a transition from live work creates done.
		t.Seen = result.FallbackIdle || previous == StateUnknown || previous == ""
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
