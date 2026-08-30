// Package agentcontrol owns provider-aware control of Sidecar-managed shells.
// It is transport-neutral: CLI and UI callers use the same service, while the
// local implementation speaks tmux through Terminal.
package agentcontrol

import (
	"encoding/json"
	"fmt"
	"time"
)

type Status string

const (
	StatusUnknown Status = "unknown"
	StatusIdle    Status = "idle"
	StatusWorking Status = "working"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
)

// Target is the pinned, host-shaped identity of one managed pane.
type Target struct {
	Host      string `json:"host"`
	Project   string `json:"project"`
	Session   string `json:"session"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	PaneID    string `json:"paneId,omitempty"`
	PanePID   int    `json:"panePid,omitempty"`
	// ServerPID is the tmux server process, and it — not ServerIncarnation — is
	// what the occupant check compares.
	//
	// The incarnation string includes the socket's ctime, and tmux rewrites the
	// socket's permission bits whenever the set of attached clients changes
	// (server_update_socket). Attaching M2's own control-mode observer
	// therefore bumps the ctime, and an incarnation-based pin would report
	// every observed target as replaced the instant it began observing it. The
	// server pid answers the question the pin is actually asking — is this the
	// same server process? — and cannot drift because somebody attached.
	ServerPID int `json:"serverPid,omitempty"`
	// ServerIncarnation stays as observed evidence about the socket. It is
	// reported, not compared.
	ServerIncarnation string `json:"serverIncarnation,omitempty"`
}

type AgentState struct {
	Kind             string    `json:"kind,omitempty"`
	Status           Status    `json:"status"`
	Freshness        string    `json:"freshness"`
	Attention        bool      `json:"attention"`
	Evidence         string    `json:"evidence,omitempty"`
	ChangedAt        time.Time `json:"changedAt,omitzero"`
	CapturedAt       time.Time `json:"capturedAt"`
	InteractiveReady bool      `json:"interactiveReady"`
}

// Agent is the stable result shared by list, get, and start.
type Agent struct {
	Target Target     `json:"target"`
	Agent  AgentState `json:"agent"`
}

type ErrorCode string

const (
	ErrNotFound              ErrorCode = "agent_not_found"
	ErrPaneBusy              ErrorCode = "agent_pane_busy"
	ErrKindMismatch          ErrorCode = "agent_kind_mismatch"
	ErrNotReady              ErrorCode = "agent_not_ready"
	ErrStartFailed           ErrorCode = "agent_start_failed"
	ErrBlocked               ErrorCode = "agent_blocked"
	ErrPromptStalled         ErrorCode = "agent_prompt_stalled"
	ErrReplaced              ErrorCode = "agent_replaced"
	ErrTranscriptUnavailable ErrorCode = "transcript_unavailable"
	ErrTimeout               ErrorCode = "timeout"
	ErrTransport             ErrorCode = "transport_failed"
	ErrFeatureDisabled       ErrorCode = "feature_disabled"
)

type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Target  *Target   `json:"target,omitempty"`
	Err     error     `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}
func (e *Error) Unwrap() error { return e.Err }

// ErrorEnvelope is the machine stderr contract.
type ErrorEnvelope struct {
	Error *Error `json:"error"`
}

func MarshalError(err error) []byte {
	var e *Error
	if !AsError(err, &e) {
		e = &Error{Code: ErrTransport, Message: err.Error(), Err: err}
	}
	b, marshalErr := json.Marshal(ErrorEnvelope{Error: e})
	if marshalErr != nil {
		return []byte(fmt.Sprintf(`{"error":{"code":"transport_failed","message":%q}}`, err.Error()))
	}
	return b
}

func AsError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return false
}
