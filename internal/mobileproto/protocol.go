// Package mobileproto defines the bounded JSONL contract spoken by
// `sidecar mobile serve --stdio` over an authenticated SSH exec channel.
package mobileproto

const (
	Version              = 0
	MaxLineBytes         = 8 << 20
	MaxRequestIDBytes    = 128
	MaxTargetBytes       = 512
	MaxAttachmentIDBytes = 128
	MaxInputBytes        = 64 << 10
	MaxColumns           = 512
	MaxRows              = 256
	OutboundQueueDepth   = 8
	HeartbeatIntervalMS  = 5000
	PresenceTimeoutMS    = 15000
)

const (
	RequestHello     = "hello"
	RequestStatus    = "status"
	RequestResolve   = "resolve"
	RequestOpen      = "open"
	RequestControl   = "control"
	RequestInput     = "input"
	RequestResize    = "resize"
	RequestHeartbeat = "heartbeat"
	RequestRelease   = "release"
	RequestReconnect = "reconnect"
	RequestClose     = "close"
)

const (
	ResponseHello       = "hello"
	ResponseStatus      = "status"
	ResponseResolved    = "resolved"
	ResponseOpened      = "opened"
	ResponseReconnected = "reconnected"
	ResponseControl     = "control"
	ResponseAccepted    = "accepted"
	ResponseResized     = "resized"
	ResponseHeartbeat   = "heartbeat"
	ResponseReleased    = "released"
	ResponseClosed      = "closed"
	ResponseFrame       = "frame"
	ResponseReset       = "reset"
	ResponseError       = "error"
)

const (
	ErrorProtocolMismatch = "protocol_mismatch"
	ErrorHandshake        = "handshake_required"
	ErrorInvalidRequest   = "invalid_request"
	ErrorNotFound         = "target_not_found"
	ErrorAmbiguous        = "target_ambiguous"
	ErrorUnsupported      = "unsupported"
	ErrorUnsupportedMode  = "unsupported_input_mode"
	ErrorIdentityChanged  = "identity_changed"
	ErrorAttachment       = "attachment_invalid"
	ErrorControlRequired  = "control_required"
	ErrorOperationOrder   = "operation_sequence"
	ErrorLease            = "geometry_lease"
	ErrorBackend          = "backend"
	ErrorOverflow         = "overflow"
)

// Request is the only client-to-server envelope. Fields not used by Type must
// be omitted. RequestID correlates exactly one response; asynchronous frame and
// reset events omit it.
type Request struct {
	Version                      int             `json:"version"`
	Type                         string          `json:"type"`
	RequestID                    string          `json:"request_id"`
	Target                       string          `json:"target,omitempty"`
	TargetHandle                 string          `json:"target_handle,omitempty"`
	AttachmentHandle             string          `json:"attachment_handle,omitempty"`
	AttachmentID                 string          `json:"attachment_id,omitempty"`
	OperationSequence            uint64          `json:"operation_sequence,omitempty"`
	DataBase64                   string          `json:"data_base64,omitempty"`
	Columns                      int             `json:"columns,omitempty"`
	Rows                         int             `json:"rows,omitempty"`
	LastOutputSequence           uint64          `json:"last_output_sequence,omitempty"`
	LastResetGeneration          uint64          `json:"last_reset_generation,omitempty"`
	PreviousAttachmentGeneration uint64          `json:"previous_attachment_generation,omitempty"`
	ExpectedTarget               *TargetIdentity `json:"expected_target,omitempty"`
}

// TargetIdentity is the stable, cross-process authority a reconnect must
// match after opening a fresh SSH exec channel. Process-scoped handles and the
// API instance are deliberately absent.
type TargetIdentity struct {
	HubID                 string `json:"hub_id"`
	OwnerHostID           string `json:"owner_host_id"`
	OwnerConfigGeneration string `json:"owner_config_generation"`
	WorkspaceID           string `json:"workspace_id"`
	WorkspaceKind         string `json:"workspace_kind"`
	Session               string `json:"session"`
	Pane                  string `json:"pane"`
	ServerIncarnation     string `json:"server_incarnation"`
	TargetGeneration      string `json:"target_generation"`
}

func (t Target) Identity() TargetIdentity {
	return TargetIdentity{HubID: t.HubID, OwnerHostID: t.OwnerHostID, OwnerConfigGeneration: t.OwnerConfigGeneration,
		WorkspaceID: t.WorkspaceID, WorkspaceKind: t.WorkspaceKind, Session: t.Session, Pane: t.Pane,
		ServerIncarnation: t.ServerIncarnation, TargetGeneration: t.TargetGeneration}
}

type Geometry struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// Target identifies the exact local managed terminal that a server-side
// opaque handle names. Clients display these fields but never reconstruct a
// handle from them.
type Target struct {
	Handle                string   `json:"handle"`
	HubID                 string   `json:"hub_id"`
	HubInstance           string   `json:"hub_instance"`
	OwnerHostID           string   `json:"owner_host_id"`
	OwnerConfigGeneration string   `json:"owner_config_generation"`
	WorkspaceID           string   `json:"workspace_id"`
	WorkspaceKind         string   `json:"workspace_kind"`
	Session               string   `json:"session"`
	Pane                  string   `json:"pane"`
	ServerIncarnation     string   `json:"server_incarnation"`
	TargetGeneration      string   `json:"target_generation"`
	DisplayName           string   `json:"display_name,omitempty"`
	Geometry              Geometry `json:"geometry"`
}

type Modes struct {
	InputKnown        bool   `json:"input_known"`
	BracketedPaste    bool   `json:"bracketed_paste"`
	ApplicationCursor bool   `json:"application_cursor"`
	ApplicationKeypad bool   `json:"application_keypad"`
	Autowrap          bool   `json:"autowrap"`
	Origin            bool   `json:"origin"`
	Insert            bool   `json:"insert"`
	MouseAny          bool   `json:"mouse_any"`
	MouseSGR          bool   `json:"mouse_sgr"`
	AlternateScreen   bool   `json:"alternate_screen"`
	CursorVisible     bool   `json:"cursor_visible"`
	CursorShape       string `json:"cursor_shape"`
	CursorBlinking    bool   `json:"cursor_blinking"`
}

type Capabilities struct {
	NormalizedFullFrames bool `json:"normalized_full_frames"`
	ChangedRowFrames     bool `json:"changed_row_frames"`
	Input                bool `json:"input"`
	Resize               bool `json:"resize"`
	Reconnect            bool `json:"reconnect"`
	MaximumColumns       int  `json:"maximum_columns"`
	MaximumRows          int  `json:"maximum_rows"`
	MaximumInputBytes    int  `json:"maximum_input_bytes"`
	MaximumLineBytes     int  `json:"maximum_line_bytes"`
	HeartbeatIntervalMS  int  `json:"heartbeat_interval_ms"`
	PresenceTimeoutMS    int  `json:"presence_timeout_ms"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Retry   bool   `json:"retry,omitempty"`
}

// Response is the server-to-client envelope for both correlated responses and
// asynchronous presentation events.
type Response struct {
	Version              int           `json:"version"`
	Type                 string        `json:"type"`
	RequestID            string        `json:"request_id,omitempty"`
	APIInstance          string        `json:"api_instance,omitempty"`
	Target               *Target       `json:"target,omitempty"`
	Capabilities         *Capabilities `json:"capabilities,omitempty"`
	AttachmentHandle     string        `json:"attachment_handle,omitempty"`
	AttachmentGeneration uint64        `json:"attachment_generation,omitempty"`
	Control              bool          `json:"control,omitempty"`
	OperationSequence    uint64        `json:"operation_sequence,omitempty"`
	OutputSequence       uint64        `json:"output_sequence,omitempty"`
	ResetGeneration      uint64        `json:"reset_generation,omitempty"`
	FrameKind            string        `json:"frame_kind,omitempty"`
	Geometry             *Geometry     `json:"geometry,omitempty"`
	Modes                *Modes        `json:"modes,omitempty"`
	RenderVTBase64       string        `json:"render_vt_base64,omitempty"`
	Reason               string        `json:"reason,omitempty"`
	Error                *Error        `json:"error,omitempty"`
}

func DefaultCapabilities() Capabilities {
	return Capabilities{
		NormalizedFullFrames: true,
		ChangedRowFrames:     false,
		Input:                true,
		Resize:               true,
		Reconnect:            true,
		MaximumColumns:       MaxColumns,
		MaximumRows:          MaxRows,
		MaximumInputBytes:    MaxInputBytes,
		MaximumLineBytes:     MaxLineBytes,
		HeartbeatIntervalMS:  HeartbeatIntervalMS,
		PresenceTimeoutMS:    PresenceTimeoutMS,
	}
}
