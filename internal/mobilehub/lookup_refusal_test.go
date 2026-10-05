package mobilehub

import (
	"context"
	"fmt"
	"testing"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// A client gives up on identity_changed, so only a real identity mismatch may
// carry it. An unreachable owner or a slow catalog is retried.
func TestLookupRefusalSeparatesChangedTargetsFromTransientFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  string
		retry bool
	}{
		{"identity changed", ErrTargetIdentityChanged, mobileproto.ErrorIdentityChanged, false},
		{"other hub", ErrTargetOtherHub, mobileproto.ErrorIdentityChanged, false},
		{"incomplete", ErrIncompleteTargetSelection, mobileproto.ErrorInvalidRequest, false},
		{"negotiation", fmt.Errorf("wrap: %w", ErrOwnerNegotiationUnsupported), mobileproto.ErrorUnsupported, false},
		{"owner unavailable", ErrOwnerUnavailable, mobileproto.ErrorBackend, true},
		{"timeout", context.DeadlineExceeded, mobileproto.ErrorBackend, true},
		{"owner resolve not found", &mobile.ResolveError{Code: mobileproto.ErrorNotFound, Message: "gone"}, mobileproto.ErrorNotFound, false},
		{"owner config changed", &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "owner configuration changed"}, mobileproto.ErrorIdentityChanged, false},
	} {
		code, retry := lookupRefusal(tc.err)
		if code != tc.code || retry != tc.retry {
			t.Errorf("%s: lookupRefusal = %q, %v; want %q, %v", tc.name, code, retry, tc.code, tc.retry)
		}
	}
}
