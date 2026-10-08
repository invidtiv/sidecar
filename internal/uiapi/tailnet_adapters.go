package uiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
)

// TailnetMode selects how the Tailnet listener reaches the tailnet.
type TailnetMode string

const (
	// TailnetModeDirect binds the node's tailnet addresses itself, serves TLS
	// with a `tailscale cert` certificate and identifies every connection with
	// Tailscale's whois. It is the documented default.
	TailnetModeDirect TailnetMode = "direct"
	// TailnetModeServe is the socket listener a `tailscale serve` proxy
	// targets, trusting the Tailscale-User-Login header it adds.
	TailnetModeServe TailnetMode = "serve"
)

// ParseTailnetMode accepts the api.tailnetMode and --tailnet-mode values.
func ParseTailnetMode(value string) (TailnetMode, error) {
	switch mode := TailnetMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case TailnetModeDirect, TailnetModeServe:
		return mode, nil
	}
	return "", fmt.Errorf("tailnet mode %q is not direct or serve", value)
}

// TailnetNode is what direct mode reads about the local node.
type TailnetNode struct {
	// Host is the MagicDNS name without the trailing dot, lowercased.
	Host string
	// OwnerLogin owns the node; it is the default allowed login.
	OwnerLogin string
	// Addresses are the node's own tailnet addresses: what direct mode binds,
	// and the sources it refuses.
	Addresses []netip.Addr
}

// TailnetPeer is Tailscale's answer for the device behind one source address.
type TailnetPeer struct {
	Login  string
	Device string
	Tags   []string
}

// The adapter seams over Tailscale. Each default shells out to the tailscale
// CLI and never changes Tailscale configuration; a tsnet or LocalAPI
// implementation can replace any of them.
type (
	// TailnetNodeFunc reads the local node.
	TailnetNodeFunc func(ctx context.Context) (TailnetNode, error)
	// TailnetWhoisFunc identifies the device that owns a tailnet address.
	TailnetWhoisFunc func(ctx context.Context, addr netip.Addr) (TailnetPeer, error)
	// TailnetCertFunc returns a PEM certificate chain and private key for the
	// node's MagicDNS name.
	TailnetCertFunc func(ctx context.Context, host string) (certPEM, keyPEM []byte, err error)
	// TailnetServeHoldsFunc reports whether a `tailscale serve` (or funnel)
	// route already holds a TCP port on this node.
	TailnetServeHoldsFunc func(ctx context.Context, port int) (bool, error)
	// TailnetListenFunc binds one address; tests map tailnet addresses onto
	// loopback.
	TailnetListenFunc func(network, address string) (net.Listener, error)
)

// TailnetAdapters bundles the direct-mode seams. Nil fields use the CLI
// defaults.
type TailnetAdapters struct {
	Node       TailnetNodeFunc
	Whois      TailnetWhoisFunc
	Cert       TailnetCertFunc
	ServeHolds TailnetServeHoldsFunc
	Listen     TailnetListenFunc
}

func (a TailnetAdapters) withDefaults() TailnetAdapters {
	if a.Node == nil {
		a.Node = TailscaleNode
	}
	if a.Whois == nil {
		a.Whois = TailscaleWhois
	}
	if a.Cert == nil {
		a.Cert = TailscaleCert
	}
	if a.ServeHolds == nil {
		a.ServeHolds = TailscaleServeHolds
	}
	if a.Listen == nil {
		a.Listen = net.Listen
	}
	return a
}

func tailscaleCLI(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return nil, errors.New("the tailscale CLI is not on PATH")
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("tailscale %s: %w: %s", args[0], err, detail)
		}
		return nil, fmt.Errorf("tailscale %s: %w", args[0], err)
	}
	return out, nil
}

// TailscaleNode reads the local node from `tailscale status --json`.
func TailscaleNode(ctx context.Context) (TailnetNode, error) {
	out, err := tailscaleCLI(ctx, "status", "--json")
	if err != nil {
		return TailnetNode{}, err
	}
	return ParseTailscaleNode(out)
}

// ParseTailscaleNode extracts the node's name, owner and addresses from
// `tailscale status --json`, refusing a node that is not running.
func ParseTailscaleNode(data []byte) (TailnetNode, error) {
	identity, err := ParseTailscaleStatus(data)
	if err != nil {
		return TailnetNode{}, err
	}
	var status struct {
		BackendState string `json:"BackendState"`
		Self         *struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return TailnetNode{}, fmt.Errorf("parse tailscale status: %w", err)
	}
	if status.BackendState != "" && status.BackendState != "Running" {
		return TailnetNode{}, fmt.Errorf("tailscale is %s, not Running; connect it with `tailscale up` or the Tailscale app", status.BackendState)
	}
	node := TailnetNode{Host: identity.Host, OwnerLogin: identity.OwnerLogin}
	for _, raw := range status.Self.TailscaleIPs {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		node.Addresses = append(node.Addresses, addr.Unmap())
	}
	if len(node.Addresses) == 0 {
		return TailnetNode{}, errors.New("tailscale status reports no tailnet address for this node")
	}
	return node, nil
}

// TailscaleWhois identifies a peer with `tailscale whois --json`.
func TailscaleWhois(ctx context.Context, addr netip.Addr) (TailnetPeer, error) {
	out, err := tailscaleCLI(ctx, "whois", "--json", addr.String())
	if err != nil {
		return TailnetPeer{}, err
	}
	return ParseTailscaleWhois(out)
}

// ParseTailscaleWhois extracts the login, device and tags from `tailscale
// whois --json`. An answer without a login is an error, never an anonymous
// peer.
func ParseTailscaleWhois(data []byte) (TailnetPeer, error) {
	var whois struct {
		Node *struct {
			Name         string   `json:"Name"`
			ComputedName string   `json:"ComputedName"`
			Tags         []string `json:"Tags"`
		} `json:"Node"`
		UserProfile *struct {
			LoginName string `json:"LoginName"`
		} `json:"UserProfile"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &whois); err != nil {
		return TailnetPeer{}, fmt.Errorf("parse tailscale whois: %w", err)
	}
	if whois.Node == nil || whois.UserProfile == nil || strings.TrimSpace(whois.UserProfile.LoginName) == "" {
		return TailnetPeer{}, errors.New("tailscale whois named no login for this address")
	}
	device := whois.Node.ComputedName
	if device == "" {
		device = strings.SplitN(whois.Node.Name, ".", 2)[0]
	}
	return TailnetPeer{Login: strings.TrimSpace(whois.UserProfile.LoginName), Device: device, Tags: whois.Node.Tags}, nil
}

// TailscaleCert fetches the node's certificate with `tailscale cert`, written
// to stdout so the key never touches a file Sidecar does not own. Tailscale
// keeps and renews its own copy; asking again returns the current one.
func TailscaleCert(ctx context.Context, host string) ([]byte, []byte, error) {
	out, err := tailscaleCLI(ctx, "cert", "--cert-file", "-", "--key-file", "-", host)
	if err != nil {
		return nil, nil, err
	}
	return splitPEM(out)
}

// splitPEM separates certificate blocks from the private key block.
func splitPEM(data []byte) (certPEM, keyPEM []byte, err error) {
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			certPEM = append(certPEM, pem.EncodeToMemory(block)...)
		} else if strings.HasSuffix(block.Type, "PRIVATE KEY") {
			keyPEM = pem.EncodeToMemory(block)
		}
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, nil, errors.New("tailscale cert returned no certificate and key")
	}
	return certPEM, keyPEM, nil
}

// TailscaleServeHolds reads `tailscale serve status --json` for a route on
// port. A serve route is answered inside Tailscale, ahead of any socket
// Sidecar binds, so binding would succeed and never see a request.
func TailscaleServeHolds(ctx context.Context, port int) (bool, error) {
	out, err := tailscaleCLI(ctx, "serve", "status", "--json")
	if err != nil {
		return false, err
	}
	return ParseServeHolds(out, port)
}

// ParseServeHolds reports whether serve status JSON has a TCP route on port.
func ParseServeHolds(data []byte, port int) (bool, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return false, nil
	}
	var status struct {
		TCP map[string]json.RawMessage `json:"TCP"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return false, fmt.Errorf("parse tailscale serve status: %w", err)
	}
	_, held := status.TCP[strconv.Itoa(port)]
	return held, nil
}
