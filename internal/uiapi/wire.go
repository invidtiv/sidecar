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
