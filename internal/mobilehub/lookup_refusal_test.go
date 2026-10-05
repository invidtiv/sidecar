package mobilehub

import (
	"context"
	"fmt"
	"testing"

	"github.com/marcus/sidecar/internal/hosts"
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
		{"owner removed", ownerUnavailable(""), mobileproto.ErrorNotFound, false},
		{"owner disabled", ownerUnavailable("disabled"), mobileproto.ErrorUnsupported, false},
		{"owner unsupported", ownerUnavailable("unsupported"), mobileproto.ErrorUnsupported, false},
		{"owner unreachable", ownerUnavailable("unreachable"), mobileproto.ErrorBackend, true},
		{"owner connecting", ownerUnavailable("connecting"), mobileproto.ErrorBackend, true},
		{"route changed", fmt.Errorf("%w: host book was removed", hosts.ErrMobileRouteChanged), mobileproto.ErrorIdentityChanged, false},
		{"route unsupported", fmt.Errorf("%w: no v0", hosts.ErrMobileRouteUnsupported), mobileproto.ErrorUnsupported, false},
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

// An owner that was removed, disabled, or cannot serve terminals will not come
// back by retrying, so a lookup against it is refused for good.
func TestLookupAgainstAPermanentlyUnavailableOwnerIsNotRetried(t *testing.T) {
	for _, state := range []string{"removed", "disabled", "unsupported"} {
		t.Run(state, func(t *testing.T) {
			d := newFakeRouterDirectory(newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local")), newFakeCatalogLineStream(rawOwnerCatalog("remote-owner", "repo", "owner")))
			d.snapshot.Endpoints = d.snapshot.Endpoints[:1]
			if state == "removed" {
				d.snapshot.Hosts = d.snapshot.Hosts[:1]
			} else {
				d.snapshot.Hosts[1].State = state
			}
			router, err := NewCatalogRouter(d)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, err = router.Lookup(context.Background(), "stale-selector", mobileproto.TargetIdentity{HubID: "hub", OwnerHostID: "book"})
			if code, retry := lookupRefusal(err); retry {
				t.Fatalf("%s owner: code=%s retry=%v err=%v; want a permanent refusal", state, code, retry, err)
			}
		})
	}
}
