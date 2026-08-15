package uirequest

import "time"

// Action identifies the requested UI presentation mutation.
type Action string

const (
	ActionOpen Action = "open"
)

// TargetKind identifies the type of object to reveal.
type TargetKind string

const (
	TargetKindFile  TargetKind = "file"
	TargetKindIssue TargetKind = "issue"
	TargetKindDiff  TargetKind = "diff"
)

// Status describes the host's response to a UI request.
type Status string

const (
	StatusOpened     Status = "opened"
	StatusQueued     Status = "queued"
	StatusRetargeted Status = "retargeted"
	StatusDeclined   Status = "declined"
	StatusError      Status = "error"
)

// Origin identifies the calling process and its owning Sidecar project shell.
type Origin struct {
	TmuxSession string `json:"tmuxSession"`
	Namespace   string `json:"namespace"`
	ProjectKey  string `json:"projectKey"`
	WorkDir     string `json:"workDir"`
	PID         int    `json:"pid"`
}

// Target defines what to open in a split pane.
type Target struct {
	Kind  TargetKind `json:"kind"`
	Value string     `json:"value"`
	Line  int        `json:"line"`
}

// Options specifies optional placement flags. There is deliberately no focus
// option: an open request never moves the user's selection or focus.
type Options struct {
	Split string `json:"split,omitempty"` // "auto", "right", "below"
}

// Request is the payload written by the CLI into the request bus.
type Request struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	TTLMs     int       `json:"ttlMs"`
	Origin    Origin    `json:"origin"`
	Action    Action    `json:"action"`
	Target    Target    `json:"target"`
	Options   Options   `json:"options,omitempty"`
}

// Ack is the acknowledgement written by each Sidecar instance handling a request.
type Ack struct {
	Instance string    `json:"instance"`
	Host     string    `json:"host"`
	PID      int       `json:"pid"`
	Status   Status    `json:"status"`
	Reason   string    `json:"reason,omitempty"`
	Surface  string    `json:"surface,omitempty"`
	Pane     int       `json:"pane,omitempty"`
	At       time.Time `json:"at"`
}

// How a request chose its destination. Values are stable for --json callers.
const (
	ResolvedCurrentShell = "current-shell"
	ResolvedShell        = "shell"
	ResolvedProject      = "project"
	ResolvedInstance     = "instance"
)

// Result is the consolidated outcome presented to the agent or caller.
type Result struct {
	Action    Action `json:"action"`
	Target    Target `json:"target"`
	Shell     string `json:"shell"`
	Name      string `json:"name"`
	Project   string `json:"project"`
	Resolved  string `json:"resolved"`
	Delivered int    `json:"delivered"`
	Results   []Ack  `json:"results"`
}
