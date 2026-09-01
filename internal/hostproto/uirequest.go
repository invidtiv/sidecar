package hostproto

import (
	"encoding/json"
	"fmt"
	"time"
)

// UI request field bounds. The encoded announcement must stay well under
// MaxLineBytes: a layout spec that will not fit is refused here rather than
// dropped as a truncated JSONL line.
const (
	MaxUIRequestIDBytes      = 128
	MaxUIRequestFieldBytes   = 1024
	MaxUIRequestTargetBytes  = 4096
	MaxUIRequestPayloadBytes = 256 << 10
	// MaxUIRequestBytes caps the encoded payload, far below MaxLineBytes.
	MaxUIRequestBytes = 256 << 10
)

// UI request actions that serve may announce. Other uirequest actions stay
// local to the host.
const (
	UIRequestActionOpen   = "open"
	UIRequestActionLayout = "layout"
)

// UIRequest is one host-side open/layout request, forwarded so the connected
// viewer can apply it. Serve stamps Origin.HostID from the connection; nothing
// the host CLI wrote in the request file is trusted for that field.
type UIRequest struct {
	ID        string           `json:"id"`
	Action    string           `json:"action"`
	CreatedAt time.Time        `json:"createdAt,omitzero"`
	TTLMs     int              `json:"ttlMs"`
	Origin    UIRequestOrigin  `json:"origin"`
	Target    UIRequestTarget  `json:"target,omitzero"`
	Options   UIRequestOptions `json:"options,omitzero"`
	Payload   json.RawMessage  `json:"payload,omitempty"`
}

// UIRequestOrigin is the request's origin, host-qualified by serve.
type UIRequestOrigin struct {
	TmuxSession string `json:"tmuxSession"`
	Namespace   string `json:"namespace,omitempty"`
	ProjectKey  string `json:"projectKey,omitempty"`
	WorkDir     string `json:"workDir,omitempty"`
	HostID      string `json:"hostId"`
	Sessions    bool   `json:"sessions,omitempty"`
	SessionsRow string `json:"sessionsRow,omitempty"`
}

// UIRequestTarget is the object an open request names.
type UIRequestTarget struct {
	Kind     string `json:"kind,omitempty"`
	Value    string `json:"value,omitempty"`
	Line     int    `json:"line,omitempty"`
	Provider string `json:"provider,omitempty"`
	Matcher  string `json:"matcher,omitempty"`
}

func (t UIRequestTarget) empty() bool {
	return t.Kind == "" && t.Value == "" && t.Line == 0 && t.Provider == "" && t.Matcher == ""
}

// UIRequestOptions are optional placement flags on an open request.
type UIRequestOptions struct {
	Split string `json:"split,omitempty"`
	At    string `json:"at,omitempty"`
}

func (r UIRequest) validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("%w: ui request has no id", ErrInvalid)
	case len(r.ID) > MaxUIRequestIDBytes:
		return fmt.Errorf("%w: ui request id exceeds %d bytes", ErrInvalid, MaxUIRequestIDBytes)
	case r.TTLMs <= 0:
		return fmt.Errorf("%w: ui request has no ttl", ErrInvalid)
	case r.Origin.TmuxSession == "":
		return fmt.Errorf("%w: ui request has no origin session", ErrInvalid)
	case r.Origin.HostID == "":
		return fmt.Errorf("%w: ui request has no host id", ErrInvalid)
	}
	switch r.Action {
	case UIRequestActionOpen:
		if r.Target.empty() {
			return fmt.Errorf("%w: open request has no target", ErrInvalid)
		}
	case UIRequestActionLayout:
		if len(r.Payload) == 0 {
			return fmt.Errorf("%w: layout request has no payload", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown ui request action %q", ErrInvalid, r.Action)
	}
	if len(r.Payload) > MaxUIRequestPayloadBytes {
		return fmt.Errorf("%w: ui request payload exceeds %d bytes", ErrInvalid, MaxUIRequestPayloadBytes)
	}
	for _, field := range []string{
		r.Action, r.Origin.TmuxSession, r.Origin.Namespace, r.Origin.ProjectKey,
		r.Origin.WorkDir, r.Origin.HostID, r.Origin.SessionsRow,
		r.Options.Split, r.Options.At, r.Target.Kind, r.Target.Provider, r.Target.Matcher,
	} {
		if len(field) > MaxUIRequestFieldBytes {
			return fmt.Errorf("%w: ui request field exceeds %d bytes", ErrInvalid, MaxUIRequestFieldBytes)
		}
	}
	if len(r.Target.Value) > MaxUIRequestTargetBytes {
		return fmt.Errorf("%w: ui request target exceeds %d bytes", ErrInvalid, MaxUIRequestTargetBytes)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("%w: ui request is not encodable", ErrInvalid)
	}
	if len(encoded) > MaxUIRequestBytes {
		return fmt.Errorf("%w: ui request exceeds %d bytes", ErrInvalid, MaxUIRequestBytes)
	}
	return nil
}
