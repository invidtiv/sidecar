package uiapi

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
	Code string `json:"code"`
	Next string `json:"next,omitempty"`
}
type OriginRequest struct {
	Origin string   `json:"origin"`
	Scopes []string `json:"scopes,omitempty"`
}
