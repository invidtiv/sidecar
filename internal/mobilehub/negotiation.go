package mobilehub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/marcus/sidecar/internal/mobileproto"
)

// ErrOwnerNegotiationUnsupported distinguishes an older owner's hello refusal
// from a changed target or route. Never retry or silently downgrade the hello.
var ErrOwnerNegotiationUnsupported = errors.New("owning service does not support requested terminal capabilities")

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
