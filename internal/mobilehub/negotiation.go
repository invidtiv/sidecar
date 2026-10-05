package mobilehub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// ErrOwnerNegotiationUnsupported distinguishes an older owner's hello refusal
// from a changed target or route. Never retry or silently downgrade the hello.
var ErrOwnerNegotiationUnsupported = errors.New("owning service does not support requested terminal capabilities")

// Target lookup refusals. Only a real identity mismatch tells the client the
// terminal it listed is gone; an unreachable owner is a transient condition the
// client retries, so the two must never share a wire code.
var (
	ErrIncompleteTargetSelection = errors.New("mobile hub: incomplete public target selection")
	ErrTargetOtherHub            = errors.New("mobile hub: this terminal belongs to another hub; refresh the sessions list")
	ErrTargetIdentityChanged     = errors.New("this terminal was replaced or restarted since the sessions list was loaded; refresh to reattach")
	ErrOwnerUnavailable          = errors.New("the host that owns this terminal is unavailable; retrying")
	ErrOwnerRemoved              = errors.New("the host that owns this terminal is no longer configured; refresh the sessions list")
	ErrOwnerNotServing           = errors.New("the host that owns this terminal is disabled or cannot serve terminals; enable or upgrade it in Sidecar")
)

// ownerUnavailable separates an owner that may come back (offline, connecting,
// unreachable) from one that will not without a configuration change.
func ownerUnavailable(state string) error {
	switch state {
	case "":
		return ErrOwnerRemoved
	case string(hosts.StateDisabled), "unsupported":
		return fmt.Errorf("%w (%s)", ErrOwnerNotServing, state)
	default:
		return ErrOwnerUnavailable
	}
}

// lookupRefusal maps a target lookup failure to its wire code and whether a
// retry can succeed.
func lookupRefusal(err error) (code string, retry bool) {
	var resolveErr *mobile.ResolveError
	switch {
	case errors.Is(err, ErrOwnerNegotiationUnsupported):
		return mobileproto.ErrorUnsupported, false
	case errors.Is(err, ErrIncompleteTargetSelection):
		return mobileproto.ErrorInvalidRequest, false
	case errors.Is(err, ErrTargetIdentityChanged), errors.Is(err, ErrTargetOtherHub), errors.Is(err, hosts.ErrMobileRouteChanged):
		return mobileproto.ErrorIdentityChanged, false
	case errors.Is(err, ErrOwnerRemoved):
		return mobileproto.ErrorNotFound, false
	case errors.Is(err, ErrOwnerNotServing), errors.Is(err, hosts.ErrMobileRouteUnsupported):
		return mobileproto.ErrorUnsupported, false
	case errors.As(err, &resolveErr) && resolveErr.Code != "":
		return resolveErr.Code, resolveErr.Code != mobileproto.ErrorIdentityChanged && resolveErr.Code != mobileproto.ErrorNotFound
	default:
		return mobileproto.ErrorBackend, true
	}
}

func ownerNegotiationError(request mobileproto.Request, line []byte) error {
	if request.Capabilities == nil && request.Viewer == nil {
		return nil
	}
	var hello mobileproto.Response
	if json.Unmarshal(line, &hello) != nil || hello.RequestID != "" && hello.RequestID != request.RequestID {
		return nil
	}
	if hello.Type == mobileproto.ResponseError && hello.Error != nil {
		switch hello.Error.Code {
		case mobileproto.ErrorInvalidRequest, mobileproto.ErrorUnsupported, mobileproto.ErrorProtocolMismatch:
			return fmt.Errorf("%w: %s", ErrOwnerNegotiationUnsupported, hello.Error.Message)
		}
	}
	if hello.Type == mobileproto.ResponseHello && hello.RequestID == request.RequestID && hello.APIInstance != "" {
		if hello.Version != mobileproto.Version || hello.Capabilities == nil || !brokerRequestedCapabilities(request.Capabilities, *hello.Capabilities) {
			return ErrOwnerNegotiationUnsupported
		}
	}
	return nil
}

// ownerHelloOptions travel with the stream lifetime rather than the lookup
// deadline. The owner must receive these on its first and only hello.
type ownerHelloOptions struct {
	capabilities *mobileproto.ClientCapabilities
	viewer       *mobileproto.Viewer
}

type ownerHelloKey struct{}

func withOwnerHello(ctx context.Context, capabilities *mobileproto.ClientCapabilities, viewer *mobileproto.Viewer) context.Context {
	options := ownerHelloOptions{}
	if capabilities != nil {
		copy := *capabilities
		options.capabilities = &copy
	}
	if viewer != nil {
		copy := *viewer
		options.viewer = &copy
	}
	return context.WithValue(ctx, ownerHelloKey{}, options)
}

func ownerHelloRequest(ctx context.Context) mobileproto.Request {
	options, _ := ctx.Value(ownerHelloKey{}).(ownerHelloOptions)
	return mobileproto.Request{Version: mobileproto.Version, Type: mobileproto.RequestHello, RequestID: "hub-owner-hello",
		Capabilities: options.capabilities, Viewer: options.viewer}
}

func brokerRequestedCapabilities(requested *mobileproto.ClientCapabilities, offered mobileproto.Capabilities) bool {
	return requested == nil || (!requested.Presence || offered.Presence) &&
		(!requested.ResetFreeFrames || offered.ResetFreeFrames) &&
		(!requested.CoalescedFrames || offered.CoalescedFrames) &&
		(!requested.ServerPaste || offered.ServerPaste) &&
		(!requested.HolderLabels || offered.HolderLabels)
}
