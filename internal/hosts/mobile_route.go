package hosts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var (
	ErrMobileRouteUnavailable = errors.New("mobile owner route unavailable")
	ErrMobileRouteChanged     = errors.New("mobile owner route changed")
	ErrMobileRouteUnsupported = errors.New("mobile owner route unsupported")
)

// MobileRouteAuthority binds one current registry client. Incarnation prevents
// a replaced client in this hub process from accepting delayed work;
// RegistrationFingerprint remains stable across hub processes and detects a
// changed SSH target, binary, config or environment. Neither replaces the
// owner mobile service's terminal and config identity checks.
type MobileRouteAuthority struct {
	HostID                  string
	Incarnation             uint64
	RegistrationFingerprint string
	client                  *Client
}

// RegistrationFingerprint identifies every registration field that changes
// where or how Sidecar runs. Env order is retained because repeated variables
// are resolved in order by the remote shell.
func RegistrationFingerprint(host Host) string {
	parts := []string{host.ID, host.Target, host.RemoteBinary, host.RemoteConfig}
	parts = append(parts, host.Env...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// BindMobileRoute atomically selects the current registry client and runtime
// incarnation, then requires current online health and the explicit owner
// mobile capability. Call ValidateMobileRoute again after owner handshake and
// before every forwarded target operation.
func (r *Registry) BindMobileRoute(hostID string) (MobileRouteAuthority, error) {
	r.mu.Lock()
	client, ok := r.clients[hostID]
	incarnation := r.incarnations[hostID]
	stopped := r.stopped
	if !ok || stopped || incarnation == 0 {
		r.mu.Unlock()
		return MobileRouteAuthority{}, fmt.Errorf("%w: host %s is not registered", ErrMobileRouteUnavailable, hostID)
	}
	authority := MobileRouteAuthority{HostID: hostID, Incarnation: incarnation,
		RegistrationFingerprint: RegistrationFingerprint(client.Host()), client: client}
	r.mu.Unlock()
	if err := validateMobileClient(client, hostID); err != nil {
		return MobileRouteAuthority{}, err
	}
	if err := r.ValidateMobileRoute(authority); err != nil {
		return MobileRouteAuthority{}, err
	}
	return authority, nil
}

// ValidateMobileRoute refuses a removed, retargeted, replaced, stale or
// capability-losing owner. It never resolves a different client by name.
func (r *Registry) ValidateMobileRoute(authority MobileRouteAuthority) error {
	if authority.HostID == "" || authority.client == nil || authority.Incarnation == 0 || authority.RegistrationFingerprint == "" {
		return fmt.Errorf("%w: incomplete authority", ErrMobileRouteChanged)
	}
	r.mu.Lock()
	client, ok := r.clients[authority.HostID]
	incarnation := r.incarnations[authority.HostID]
	stopped := r.stopped
	r.mu.Unlock()
	if stopped || !ok || client != authority.client || incarnation != authority.Incarnation ||
		RegistrationFingerprint(client.Host()) != authority.RegistrationFingerprint {
		return fmt.Errorf("%w: host %s was removed, retargeted or replaced", ErrMobileRouteChanged, authority.HostID)
	}
	return validateMobileClient(client, authority.HostID)
}

func validateMobileClient(client *Client, hostID string) error {
	health := client.Health()
	if health.State != StateOnline {
		return fmt.Errorf("%w: host %s is %s", ErrMobileRouteUnavailable, hostID, health.State)
	}
	if health.Hello == nil || !health.Hello.Capabilities.Verbs.MobileServeV0 {
		return fmt.Errorf("%w: host %s did not advertise mobile serve v0", ErrMobileRouteUnsupported, hostID)
	}
	return nil
}

// MobileSidecarCommand validates and builds an owner command on the exact
// captured client. The registry lock keeps a retarget from replacing that
// client while the command is constructed; the router validates again after
// the actual owner mobile hello before forwarding any target request.
func (r *Registry) MobileSidecarCommand(ctx context.Context, authority MobileRouteAuthority) (*exec.Cmd, error) {
	if err := r.ValidateMobileRoute(authority); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	client, ok := r.clients[authority.HostID]
	if r.stopped || !ok || client != authority.client || r.incarnations[authority.HostID] != authority.Incarnation ||
		RegistrationFingerprint(client.Host()) != authority.RegistrationFingerprint {
		return nil, fmt.Errorf("%w: host %s was removed, retargeted or replaced", ErrMobileRouteChanged, authority.HostID)
	}
	return client.SidecarCommand(ctx, "mobile", "serve", "--stdio")
}
