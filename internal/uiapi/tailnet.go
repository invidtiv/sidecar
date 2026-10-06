package uiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// TailnetIdentity is what the Tailnet listener needs from Tailscale: the node's
// MagicDNS name and the login that owns the node.
type TailnetIdentity struct {
	Host       string
	OwnerLogin string
}

// TailnetIdentityFunc is the adapter seam over Tailscale. The default reads
// `tailscale status --json` and never changes Tailscale configuration.
type TailnetIdentityFunc func(ctx context.Context) (TailnetIdentity, error)

// TailscaleStatusIdentity reads the local node's identity from the tailscale
// CLI.
func TailscaleStatusIdentity(ctx context.Context) (TailnetIdentity, error) {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return TailnetIdentity{}, errors.New("--tailnet needs the tailscale CLI on PATH")
	}
	out, err := exec.CommandContext(ctx, path, "status", "--json").Output()
	if err != nil {
		return TailnetIdentity{}, fmt.Errorf("tailscale status --json: %w", err)
	}
	return ParseTailscaleStatus(out)
}

// ParseTailscaleStatus extracts the node identity from `tailscale status
// --json` output.
func ParseTailscaleStatus(data []byte) (TailnetIdentity, error) {
	var status struct {
		Self *struct {
			DNSName string          `json:"DNSName"`
			UserID  json.RawMessage `json:"UserID"`
		} `json:"Self"`
		User map[string]struct {
			LoginName string `json:"LoginName"`
		} `json:"User"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return TailnetIdentity{}, fmt.Errorf("parse tailscale status: %w", err)
	}
	if status.Self == nil || strings.TrimSuffix(status.Self.DNSName, ".") == "" {
		return TailnetIdentity{}, errors.New("tailscale status reports no MagicDNS name for this node; enable MagicDNS or log in to Tailscale")
	}
	identity := TailnetIdentity{Host: strings.ToLower(strings.TrimSuffix(status.Self.DNSName, "."))}
	userID := strings.Trim(string(status.Self.UserID), `"`)
	if _, err := strconv.ParseInt(userID, 10, 64); err == nil {
		identity.OwnerLogin = status.User[userID].LoginName
	}
	return identity, nil
}

// HTTPSURL returns the exact public origin protected by the Tailnet listener.
// The configured port does not change the listener's Unix socket or bind address.
func (o TailnetOptions) HTTPSURL() (string, error) {
	if o.HTTPSPort < 0 || o.HTTPSPort > 65535 {
		return "", errors.New("api.tailnetHTTPSPort must be a number from 1 to 65535, or 0 for the default 443")
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(o.Host), "."))
	if o.HTTPSPort != 0 && o.HTTPSPort != 443 {
		host += ":" + strconv.Itoa(o.HTTPSPort)
	}
	return "https://" + host, nil
}
