package uiapi

import "time"

// Hello is GET /api/v0/hello.
type Hello struct {
	APIVersion    int              `json:"api_version"`
	APIInstance   string           `json:"api_instance"`
	ServerVersion string           `json:"server_version"`
	Capabilities  []string         `json:"capabilities"`
	Terminal      TerminalProtocol `json:"terminal"`
}

type TerminalProtocol struct {
	Protocol string `json:"protocol"`
	Version  int    `json:"version"`
}

type TicketRequest struct{}
type PairingCodeRequest struct {
	Next string `json:"next,omitempty"`
}
type PairingExchangeRequest struct {
	PublicKey BrowserPublicKey `json:"public_key"`
	Code      string           `json:"code"`
	Next      string           `json:"next,omitempty"`
	// Label is the device name the pairing page suggests ("Safari on
	// macOS"), cleaned like an access request's label: a claim, never proof.
	Label string `json:"label,omitempty"`
}
type OriginRequest struct {
	Origin string   `json:"origin"`
	Scopes []string `json:"scopes,omitempty"`
}

// SessionProofChallengeRequest names an origin-bound public-key registration.
type SessionProofChallengeRequest struct {
	RegistrationID string `json:"registration_id"`
}
type SessionProofChallenge struct {
	Nonce     string    `json:"nonce"`
	Timestamp int64     `json:"timestamp"`
	ExpiresAt time.Time `json:"expires_at"`
}
type SessionProofRequest struct {
	RegistrationID string `json:"registration_id"`
	Nonce          string `json:"nonce"`
	Timestamp      int64  `json:"timestamp"`
	Signature      string `json:"signature"`
}
type SessionToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AccessRequestCreate is POST /api/v0/pairing/requests: a browser with no
// credential asks for access. public_key is the browser's own non-extractable
// P-256 key; label is a device name the browser suggests for itself.
type AccessRequestCreate struct {
	PublicKey BrowserPublicKey `json:"public_key"`
	Label     string           `json:"label,omitempty"`
}

// AccessRequestCreated is returned once, to the requesting browser only.
// poll_secret authorizes GET /api/v0/pairing/requests/{id}; code is what the
// browser shows for an approver to type, in its display form (K7Q-4MX).
type AccessRequestCreated struct {
	RequestID  string    `json:"request_id"`
	PollSecret string    `json:"poll_secret"`
	Code       string    `json:"code"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// AccessRequestStatus is GET /api/v0/pairing/requests/{id}. registration_id
// is present once approved: the browser then renews a session with its key.
type AccessRequestStatus struct {
	Status         string    `json:"status" jsonschema:"enum=pending,enum=approved,enum=denied,enum=expired"`
	ExpiresAt      time.Time `json:"expires_at"`
	RegistrationID string    `json:"registration_id,omitempty"`
}

// AccessRequestInfo is one pending request as approvers see it. It never
// carries the code: the approver types the code the browser shows.
type AccessRequestInfo struct {
	RequestID string    `json:"request_id"`
	Label     string    `json:"label"`
	Origin    string    `json:"origin"`
	Address   string    `json:"address"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AccessRequestList is GET /api/v0/pairing/requests.
type AccessRequestList struct {
	Requests []AccessRequestInfo `json:"requests"`
}

// AccessApproveRequest is POST /api/v0/pairing/requests/approve. surface is
// accepted only on the Local socket, where it names the local surface (cli or
// tui) recorded as approved_via.
type AccessApproveRequest struct {
	Code    string `json:"code"`
	Surface string `json:"surface,omitempty" jsonschema:"enum=cli,enum=tui"`
}

// AccessApproval is the approved request and the registration it created.
type AccessApproval struct {
	RequestID      string    `json:"request_id"`
	RegistrationID string    `json:"registration_id"`
	Label          string    `json:"label"`
	Origin         string    `json:"origin"`
	Address        string    `json:"address"`
	ApprovedVia    string    `json:"approved_via"`
	ApprovedAt     time.Time `json:"approved_at"`
}

// AccessDenyRequest is POST /api/v0/pairing/requests/deny.
type AccessDenyRequest struct {
	RequestID string `json:"request_id"`
}

// AccessDenial is the denied request.
type AccessDenial struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status" jsonschema:"enum=denied"`
}

// Device is one browser registration in GET /api/v0/pairing/sessions.
// current marks the caller's own registration.
type Device struct {
	ID          string    `json:"id"`
	Origin      string    `json:"origin"`
	Label       string    `json:"label"`
	ApprovedVia string    `json:"approved_via"`
	ApprovedAt  time.Time `json:"approved_at"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Current     bool      `json:"current"`
}

// DeviceList is GET /api/v0/pairing/sessions.
type DeviceList struct {
	Devices []Device `json:"devices"`
}

// DeviceRevocation is DELETE /api/v0/pairing/sessions/{id}.
type DeviceRevocation struct {
	ID              string `json:"id"`
	Revoked         bool   `json:"revoked"`
	TerminalsClosed int    `json:"terminals_closed"`
}
