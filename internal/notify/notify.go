// Package notify owns the notification model, its source registry, and the
// state-free rules every surface applies to a set of notifications.
//
// Nothing in here draws anything or knows about Bubble Tea: the TUI, the CLI,
// and any future headless caller all read the same model through the same
// resolution helpers, so "what is unread", "what may toast", and "who may
// dismiss this" cannot drift between surfaces.
package notify

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// SourceID names a registered notification source.
type SourceID string

// The hardcoded source set. External registration is a later phase; the ids
// are stable because they are written into the JSONL store.
const (
	SourceAgent   SourceID = "agent"
	SourceWaiting SourceID = "waiting"
	SourceSession SourceID = "session"
	SourceTasks   SourceID = "tasks"
	SourceTD      SourceID = "td"
	SourceSystem  SourceID = "system"
)

// Severity ranks a notification within its source.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Rank orders severities for the loudest-first rules. Unknown values rank
// lowest so a record written by a newer build can never outrank a real error.
func (s Severity) Rank() int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// Hue is the name of a palette entry, not a colour. The model stays free of
// lipgloss so a headless caller can read it; ResolveHue in hue.go is the one
// place that turns a hue into the theme's actual colour.
type Hue string

const (
	HuePrimary   Hue = "primary"
	HueSecondary Hue = "secondary"
	HueAccent    Hue = "accent"
	HueSuccess   Hue = "success"
	HueWarning   Hue = "warning"
	HueError     Hue = "error"
	HueInfo      Hue = "info"
	HueMuted     Hue = "muted"
)

// Source is a registered origin of notifications: its label and glyph in the
// centre's section rules (design frame 1c) and the hue its toast border and
// the header indicator take when it is the loudest unread source.
type Source struct {
	ID    SourceID
	Label string
	Glyph string
	Hue   Hue
	// Priority breaks ties between equally severe notifications. Higher is
	// louder: a waiting agent outranks a finished session outranks a task.
	Priority int
	// DefaultExpiry is how long a toast from this source stays on screen when
	// the poster names no expiry. Zero means the source is sticky by default.
	DefaultExpiry time.Duration
}

// sources is the registry. It is a fixed table rather than a mutable map
// because Phase 1 has no external registrations; the shape is what makes
// adding one later a data change instead of a code change.
var sources = []Source{
	{ID: SourceWaiting, Label: "WAITING", Glyph: "?", Hue: HueWarning, Priority: 60, DefaultExpiry: 0},
	{ID: SourceAgent, Label: "AGENTS", Glyph: "◆", Hue: HuePrimary, Priority: 50, DefaultExpiry: 8 * time.Second},
	{ID: SourceSession, Label: "SESSIONS", Glyph: "✓", Hue: HueSuccess, Priority: 40, DefaultExpiry: 8 * time.Second},
	{ID: SourceTD, Label: "TD", Glyph: "■", Hue: HueSecondary, Priority: 30, DefaultExpiry: 8 * time.Second},
	{ID: SourceTasks, Label: "TASKS", Glyph: "○", Hue: HueInfo, Priority: 20, DefaultExpiry: 8 * time.Second},
	{ID: SourceSystem, Label: "SYSTEM", Glyph: "●", Hue: HueMuted, Priority: 10, DefaultExpiry: 5 * time.Second},
}

// Sources returns the registered sources, loudest first.
func Sources() []Source {
	out := make([]Source, len(sources))
	copy(out, sources)
	return out
}

// Lookup returns the registered source for id.
func Lookup(id SourceID) (Source, bool) {
	for _, s := range sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

// SourceOf returns the source for id, falling back to system for an id no
// build knows about. A notification is never dropped for naming a source that
// has since been removed.
func SourceOf(id SourceID) Source {
	if s, ok := Lookup(id); ok {
		return s
	}
	if s, ok := Lookup(SourceSystem); ok {
		return s
	}
	return Source{ID: id, Label: strings.ToUpper(string(id)), Glyph: "●", Hue: HueMuted}
}

// ValidSource reports whether id names a registered source. The CLI uses it to
// refuse a typo rather than silently filing under system.
func ValidSource(id SourceID) bool {
	_, ok := Lookup(id)
	return ok
}

// SourceIDs lists the registered ids in registry order, for help text.
func SourceIDs() []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, string(s.ID))
	}
	return out
}

// TargetKind classifies a call to action inside a notification. Phase 1 stores
// targets but activates none of them; Phase 5 turns them into jumps.
type TargetKind string

const (
	TargetIssue   TargetKind = "issue"
	TargetTask    TargetKind = "task"
	TargetCommit  TargetKind = "commit"
	TargetFile    TargetKind = "file"
	TargetSession TargetKind = "session"
	TargetURL     TargetKind = "url"
)

// Target is one actionable reference carried by a notification.
type Target struct {
	Kind    TargetKind `json:"kind"`
	Value   string     `json:"value"`
	Line    int        `json:"line,omitempty"`
	Project string     `json:"project,omitempty"`
}

// Origin identifies who posted a notification, so a CLI caller can dismiss its
// own and nothing else. A notification posted inside the TUI has a zero Origin
// and is therefore not dismissible from outside.
type Origin struct {
	TmuxSession string `json:"tmuxSession,omitempty"`
	ProjectKey  string `json:"projectKey,omitempty"`
	WorkDir     string `json:"workDir,omitempty"`
	PID         int    `json:"pid,omitempty"`
}

// Zero reports whether the origin identifies nobody.
func (o Origin) Zero() bool {
	return o.TmuxSession == "" && o.WorkDir == "" && o.ProjectKey == ""
}

// Matches reports whether caller is the same poster as o. PID is deliberately
// not part of the test: an agent posts from one short-lived CLI process and
// dismisses from another, so identity is the shell it lives in — its tmux
// session, or failing that its working directory.
func (o Origin) Matches(caller Origin) bool {
	if o.Zero() || caller.Zero() {
		return false
	}
	if o.TmuxSession != "" || caller.TmuxSession != "" {
		return o.TmuxSession != "" && o.TmuxSession == caller.TmuxSession
	}
	if o.WorkDir != "" || caller.WorkDir != "" {
		return o.WorkDir != "" && o.WorkDir == caller.WorkDir
	}
	return o.ProjectKey != "" && o.ProjectKey == caller.ProjectKey
}

// Notification is one alert. Times are UTC; a zero time means "not yet".
type Notification struct {
	ID          string     `json:"id"`
	Source      SourceID   `json:"source"`
	Severity    Severity   `json:"severity,omitempty"`
	Title       string     `json:"title"`
	Body        string     `json:"body,omitempty"`
	Targets     []Target   `json:"targets,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ReadAt      *time.Time `json:"readAt,omitempty"`
	DismissedAt *time.Time `json:"dismissedAt,omitempty"`
	// ExpiresAt is when the *toast* stops showing. It never removes the
	// notification from the centre — suppressed is not dropped.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Origin    Origin     `json:"origin,omitempty"`
	// Sticky means the toast has no countdown and waits for the user.
	Sticky bool `json:"sticky,omitempty"`
}

// Read reports whether the notification has been seen.
func (n Notification) Read() bool { return n.ReadAt != nil }

// Dismissed reports whether the notification has been dismissed.
func (n Notification) Dismissed() bool { return n.DismissedAt != nil }

// SourceInfo returns the registered source backing this notification.
func (n Notification) SourceInfo() Source { return SourceOf(n.Source) }

// NewID generates a sortable unique notification id, in the same shape as a
// uirequest id so both are recognisable at a glance in the state tree.
func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("ntf-%013x-%s", time.Now().UTC().UnixNano()/1e3, hex.EncodeToString(b))
}

// Normalize fills in the defaults a poster may leave out: id, creation time,
// severity, and the source's default expiry. It is called by the store, so
// every surface gets the same completion whatever path posted the record.
func Normalize(n Notification, now time.Time) Notification {
	now = now.UTC()
	if n.ID == "" {
		n.ID = NewID()
	}
	if n.Source == "" {
		n.Source = SourceSystem
	}
	if n.Severity == "" {
		n.Severity = SeverityInfo
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	} else {
		n.CreatedAt = n.CreatedAt.UTC()
	}
	if n.ExpiresAt == nil && !n.Sticky {
		if d := SourceOf(n.Source).DefaultExpiry; d > 0 {
			exp := n.CreatedAt.Add(d)
			n.ExpiresAt = &exp
		} else {
			n.Sticky = true
		}
	}
	if n.ExpiresAt != nil {
		exp := n.ExpiresAt.UTC()
		n.ExpiresAt = &exp
		n.Sticky = false
	}
	if n.ReadAt != nil {
		t := n.ReadAt.UTC()
		n.ReadAt = &t
	}
	if n.DismissedAt != nil {
		t := n.DismissedAt.UTC()
		n.DismissedAt = &t
	}
	return n
}
