// Package agentactivity classifies live agent terminal observations without
// depending on tmux, Bubble Tea, or persistent conversation storage.
//
// # Spinner glyph sets are provider-owned
//
// Every provider file declares its own spinner pattern, even where two of them
// would look identical today. Sharing one is a standing bug: the sets are not
// the same and they drift independently as each CLI ships. Codex animates the
// classic ten braille dots frames; Claude, Grok and Cursor cycle the whole
// Braille block (U+2800–U+28FF).
//
// A set that is too narrow does not merely miss activity, it inverts the
// verdict. Claude shares one shared eleven-glyph pattern with Codex until this
// was fixed, so the frames outside that set left the title rule unmatched;
// evaluation fell through to the prompt-box idle rule while subagents were
// still running, and the tracker turned that working→idle transition into a
// completed turn. Sessions reported "done" with work in flight.
//
// When adding or revising a provider, harvest the real frames rather than
// assuming a set, and prefer anchoring the pattern to where the glyph actually
// appears — braille elsewhere in a title or task name is not a spinner.
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
	current := regionText(ob, Rule{Region: RegionCurrent, LastN: 24})
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
	// Any holds alternative corroborating groups: the rule matches when at
	// least one group has all of its literals present. It exists so a retain
	// rule can demand a distinctive phrase *and* surrounding chrome, rather
	// than firing on a word the agent might merely be discussing. Compared
	// case-insensitively, since these are rendered UI hints.
	Any    [][]string
	Regexp *regexp.Regexp
	Not    []string
	LastN  int
	Skip   bool
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

// regionText strips SGR escapes before any rule sees the text. Captures are
// taken with `capture-pane -e`, so styled chrome carries escape bytes inline —
// and ESC is not \s, so a coloured prompt marker defeats every column-anchored
// rule (`^❯`, `^\s*›`, `^[⠀-⣿] `). Stripping here keeps the provider tables
// written against what a human sees rather than against tmux's byte stream.
func regionText(ob Observation, rule Rule) string {
	switch rule.Region {
	case RegionTitle:
		return ansi.Strip(ob.PaneTitle)
	case RegionLastLines:
		lines := strings.Split(ansi.Strip(ob.Screen), "\n")
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
		// Stripping precedes the trim so a row holding nothing but escapes
		// counts as the padding it renders as.
		lines := strings.Split(ansi.Strip(ob.Screen), "\n")
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
		return ansi.Strip(ob.Screen)
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
	if len(rule.Any) > 0 {
		folded := strings.ToLower(text)
		satisfied := false
		for _, group := range rule.Any {
			if containsAll(folded, group) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			return false
		}
	}
	return rule.Regexp == nil || rule.Regexp.MatchString(text)
}

func containsAll(folded string, literals []string) bool {
	for _, literal := range literals {
		if !strings.Contains(folded, strings.ToLower(literal)) {
			return false
		}
	}
	return true
}

// Tracker owns transition policy while Evaluate remains state-free.
type Tracker struct {
	State           State
	Evidence        string
	ChangedAt       time.Time
	Seen            bool
	VisibleBlocker  bool
	// IdleInferred records that the current idle state came from the absence
	// of activity rather than an explicit completion marker. Providers without
	// a completion signal can never assert "done", and views use this to say so
	// instead of letting their absence from the done lane read as a bug.
	IdleInferred    bool
	idleCandidateAt time.Time
	skipSince       time.Time
}

const IdleDebounce = 400 * time.Millisecond

// SkipRetentionCap bounds how long an overlay rule may retain the prior state.
// Retention exists so a transcript or model picker does not erase a live turn,
// but an overlay left open — or a rule matching chrome that never clears —
// would otherwise hold a confident badge forever. Past the cap the tracker
// admits it no longer knows rather than continuing to assert stale evidence.
const SkipRetentionCap = 2 * time.Minute

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
		if t.skipSince.IsZero() {
			t.skipSince = now
		}
		if now.Sub(t.skipSince) < SkipRetentionCap {
			return false
		}
		// Cap exceeded: stop retaining and let the overlay's own StateUnknown
		// land, so the badge reads unknown instead of a stale certainty.
	} else {
		t.skipSince = time.Time{}
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
		t.IdleInferred = result.FallbackIdle
	}
	if result.State != StateIdle {
		t.IdleInferred = false
	}
	return true
}

// Snapshot is the persistable projection of a tracker. It carries only the
// fields that survive a restart meaningfully: transition policy timers are
// in-process concerns and are deliberately dropped.
type Snapshot struct {
	State        string    `json:"state"`
	Evidence     string    `json:"evidence,omitempty"`
	ChangedAt    time.Time `json:"changedAt"`
	Seen         bool      `json:"seen"`
	IdleInferred bool      `json:"idleInferred,omitempty"`
}

func (t Tracker) Snapshot() Snapshot {
	return Snapshot{State: string(t.State), Evidence: t.Evidence, ChangedAt: t.ChangedAt, Seen: t.Seen, IdleInferred: t.IdleInferred}
}

// Restore rebuilds a tracker from persisted state. A restored idle keeps its
// original ChangedAt, so a turn that finished before the restart still reads
// as recently finished rather than as "just observed".
func Restore(s Snapshot) Tracker {
	return Tracker{State: State(s.State), Evidence: s.Evidence, ChangedAt: s.ChangedAt, Seen: s.Seen, IdleInferred: s.IdleInferred}
}

func (t *Tracker) Acknowledge() { t.Seen = true }

func (t Tracker) DisplayState() string {
	if t.State == StateIdle && !t.Seen {
		return "done"
	}
	return string(t.State)
}
