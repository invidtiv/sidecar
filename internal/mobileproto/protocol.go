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
	MaxCatalogRows       = 2048
	MaxCatalogHosts      = 128
	MaxCatalogFailures   = 128
	MaxCatalogQueryBytes = 512
	MaxCatalogFilters    = 32
	MaxCandidatesPerRow  = 16
	MaxCatalogCandidates = 2048
	MaxHistoryRows       = 600
	MaxHistoryBytes      = 4 << 20
	OutboundQueueDepth   = 8
	HeartbeatIntervalMS  = 5000
	PresenceTimeoutMS    = 15000
)

const (
	RequestHello     = "hello"
	RequestStatus    = "status"
	RequestSessions  = "sessions"
	RequestHistory   = "history"
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
	ResponseSessions    = "sessions"
	ResponseHistory     = "history"
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
	CatalogQuery                 *CatalogQuery   `json:"catalog_query,omitempty"`
	HistoryRows                  int             `json:"history_rows,omitempty"`
}

// CatalogQuery is a bounded, server-applied Sessions view. The server owns
// ordering and filtering semantics; clients persist preferences and send them
// back rather than reimplementing the rules.
type CatalogQuery struct {
	Sort      string   `json:"sort,omitempty"`
	Search    string   `json:"search,omitempty"`
	Hosts     []string `json:"hosts,omitempty"`
	Providers []string `json:"providers,omitempty"`
	States    []string `json:"states,omitempty"`
}

type CatalogHost struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	Local  bool   `json:"local,omitempty"`
}

type CatalogRow struct {
	ID                  string             `json:"id"`
	OwnerHostID         string             `json:"owner_host_id"`
	ProjectID           string             `json:"project_id"`
	ProjectName         string             `json:"project_name"`
	WorkspaceID         string             `json:"workspace_id,omitempty"`
	WorkspaceKind       string             `json:"workspace_kind"`
	DisplayName         string             `json:"display_name"`
	Branch              string             `json:"branch,omitempty"`
	Task                string             `json:"task,omitempty"`
	Provider            string             `json:"provider,omitempty"`
	Status              string             `json:"status"`
	Group               string             `json:"group"`
	Session             string             `json:"session,omitempty"`
	Pane                string             `json:"pane,omitempty"`
	Target              string             `json:"target,omitempty"`
	CandidateGeneration string             `json:"candidate_generation,omitempty"`
	Candidates          []CatalogCandidate `json:"candidates,omitempty"`
	ExpectedTarget      *TargetIdentity    `json:"expected_target,omitempty"`
	AttachState         string             `json:"attach_state"`
	RefusalCode         string             `json:"refusal_code,omitempty"`
	Refusal             string             `json:"refusal,omitempty"`
	ObservedAt          string             `json:"observed_at"`
	ChangedAt           string             `json:"changed_at,omitempty"`
	Live                bool               `json:"live"`
	Ambiguous           bool               `json:"ambiguous"`
	Stale               bool               `json:"stale"`
	SemanticStatus      bool               `json:"semantic_status"`
	Attention           bool               `json:"attention"`
	AttachmentReady     bool               `json:"attachment_ready"`
}

// CatalogCandidate is one server-owned exact terminal choice for a catalog
// row. Selector is opaque to clients and can be echoed only with the paired
// ExpectedTarget. It remains deterministic across API processes while the
// complete source candidate set and selected terminal identity are unchanged.
type CatalogCandidate struct {
	Selector       string         `json:"selector"`
	DisplayName    string         `json:"display_name"`
	OwnerHostID    string         `json:"owner_host_id"`
	WorkspaceID    string         `json:"workspace_id"`
	WorkspaceKind  string         `json:"workspace_kind"`
	Session        string         `json:"session"`
	Pane           string         `json:"pane"`
	ExpectedTarget TargetIdentity `json:"expected_target"`
}

type CatalogSection struct {
	Title string       `json:"title,omitempty"`
	Key   string       `json:"key,omitempty"`
	Group string       `json:"group,omitempty"`
	Rows  []CatalogRow `json:"rows"`
}

type CatalogFailure struct {
	Scope  string `json:"scope"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type CatalogSnapshot struct {
	Generation            string           `json:"generation"`
	ObservedAt            string           `json:"observed_at"`
	HubID                 string           `json:"hub_id"`
	OwnerHostID           string           `json:"owner_host_id"`
	OwnerConfigGeneration string           `json:"owner_config_generation"`
	Query                 CatalogQuery     `json:"query"`
	Hosts                 []CatalogHost    `json:"hosts"`
	Sections              []CatalogSection `json:"sections"`
	Failures              []CatalogFailure `json:"failures"`
	Total                 int              `json:"total"`
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

// HistorySnapshot is one immutable, bounded owner capture. RenderVTBase64
// reconstructs HistoryRows of SwiftTerm scrollback followed by one momentary
// live grid at Geometry; it is never part of the live frame stream.
type HistorySnapshot struct {
	HistorySize    int    `json:"history_size"`
	HistoryRows    int    `json:"history_rows"`
	StartLine      int    `json:"start_line"`
	EndLine        int    `json:"end_line"`
	AtOldest       bool   `json:"at_oldest"`
	RenderVTBase64 string `json:"render_vt_base64"`
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
	CatalogSnapshots     bool `json:"catalog_snapshots"`
	HistorySnapshots     bool `json:"history_snapshots"`
	MaximumColumns       int  `json:"maximum_columns"`
	MaximumRows          int  `json:"maximum_rows"`
	MaximumInputBytes    int  `json:"maximum_input_bytes"`
	MaximumLineBytes     int  `json:"maximum_line_bytes"`
	MaximumHistoryRows   int  `json:"maximum_history_rows"`
	MaximumHistoryBytes  int  `json:"maximum_history_bytes"`
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
	Version              int              `json:"version"`
	Type                 string           `json:"type"`
	RequestID            string           `json:"request_id,omitempty"`
	APIInstance          string           `json:"api_instance,omitempty"`
	Target               *Target          `json:"target,omitempty"`
	Catalog              *CatalogSnapshot `json:"catalog,omitempty"`
	History              *HistorySnapshot `json:"history,omitempty"`
	Capabilities         *Capabilities    `json:"capabilities,omitempty"`
	AttachmentHandle     string           `json:"attachment_handle,omitempty"`
	AttachmentGeneration uint64           `json:"attachment_generation,omitempty"`
	Control              bool             `json:"control,omitempty"`
	OperationSequence    uint64           `json:"operation_sequence,omitempty"`
	OutputSequence       uint64           `json:"output_sequence,omitempty"`
	ResetGeneration      uint64           `json:"reset_generation,omitempty"`
	FrameKind            string           `json:"frame_kind,omitempty"`
	Geometry             *Geometry        `json:"geometry,omitempty"`
	Modes                *Modes           `json:"modes,omitempty"`
	RenderVTBase64       string           `json:"render_vt_base64,omitempty"`
	HistorySize          *int             `json:"history_size,omitempty"`
	Reason               string           `json:"reason,omitempty"`
	Error                *Error           `json:"error,omitempty"`
}

func DefaultCapabilities() Capabilities {
	return Capabilities{
		NormalizedFullFrames: true,
		ChangedRowFrames:     false,
		Input:                true,
		Resize:               true,
		Reconnect:            true,
		CatalogSnapshots:     true,
		HistorySnapshots:     true,
		MaximumColumns:       MaxColumns,
		MaximumRows:          MaxRows,
		MaximumInputBytes:    MaxInputBytes,
		MaximumLineBytes:     MaxLineBytes,
		MaximumHistoryRows:   MaxHistoryRows,
		MaximumHistoryBytes:  MaxHistoryBytes,
		HeartbeatIntervalMS:  HeartbeatIntervalMS,
		PresenceTimeoutMS:    PresenceTimeoutMS,
	}
}
