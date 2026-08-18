package resourceprovider

import (
	"errors"
	"fmt"

	"github.com/marcus/sidecar/internal/resource"
)

// TransportReason names the ways an invocation can fail without the provider
// ever having told Sidecar anything typed. Every one of them is attributed to
// the provider, never to the service it talks to.
type TransportReason string

const (
	// ReasonSpawn is a command that could not be started at all: not on PATH,
	// not executable, no such directory.
	ReasonSpawn TransportReason = "spawn"
	// ReasonExit is a non-zero exit status.
	ReasonExit TransportReason = "exit"
	// ReasonTimeout is exceeding the configured timeout.
	ReasonTimeout TransportReason = "timeout"
	// ReasonCanceled is the caller withdrawing the request.
	ReasonCanceled TransportReason = "canceled"
	// ReasonMalformed is no JSON or unparseable JSON on stdout.
	ReasonMalformed TransportReason = "malformed"
	// ReasonExtraOutput is more than one top-level JSON value on stdout.
	ReasonExtraOutput TransportReason = "extra-output"
	// ReasonOversize is stdout past the response byte limit.
	ReasonOversize TransportReason = "oversize"
	// ReasonProtocol is a missing or mismatched protocol field.
	ReasonProtocol TransportReason = "protocol"
	// ReasonInvalidDescribe is a describe result that failed host validation:
	// an uncompilable pattern, a duplicate matcher ID, a bound exceeded.
	ReasonInvalidDescribe TransportReason = "invalid-describe"
	// ReasonInvalidRequest is a request the host refused to send.
	ReasonInvalidRequest TransportReason = "invalid-request"
)

// TransportError is a failure of the process boundary rather than of the
// service. It deliberately carries no stdout, no stderr, and no locator: the
// Detail is host-authored text, so logging or rendering it cannot leak provider
// output.
type TransportError struct {
	Instance string
	Method   string
	Reason   TransportReason
	Detail   string
	Err      error
}

func (e *TransportError) Error() string {
	msg := fmt.Sprintf("provider %q %s: %s", e.Instance, e.Method, e.Reason)
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

func (e *TransportError) Unwrap() error { return e.Err }

// ResourceError maps a transport failure onto the typed code a card shows.
//
// The mapping is deliberate: `unavailable` means "the thing on the other end
// did not answer in time", which is what a timeout is from the user's side;
// `invalid_config` means "this instance is not set up", which is what an
// unstartable command is; everything else is the provider misbehaving, which is
// `internal` and retryable because a retry is free and sometimes works.
func (e *TransportError) ResourceError() *resource.Error {
	switch e.Reason {
	case ReasonSpawn:
		return &resource.Error{
			Code:      resource.CodeInvalidConfig,
			Message:   "The configured provider command could not be started.",
			Retryable: false,
		}
	case ReasonTimeout:
		return &resource.Error{
			Code:      resource.CodeUnavailable,
			Message:   "The provider did not answer in time.",
			Retryable: true,
		}
	case ReasonCanceled:
		return &resource.Error{
			Code:      resource.CodeUnavailable,
			Message:   "The request was canceled.",
			Retryable: true,
		}
	case ReasonProtocol:
		return &resource.Error{
			Code:      resource.CodeInvalidConfig,
			Message:   "The provider does not speak " + resource.Protocol + ".",
			Retryable: false,
		}
	case ReasonInvalidRequest:
		return &resource.Error{
			Code:      resource.CodeInternal,
			Message:   "Sidecar refused to send this request.",
			Retryable: false,
		}
	default:
		return &resource.Error{
			Code:      resource.CodeInternal,
			Message:   "The provider returned something Sidecar could not use.",
			Retryable: true,
		}
	}
}

// AsResourceError turns any error from a Provider into the typed error a view
// can render. A provider's own typed failure passes through unchanged; a
// transport failure is mapped; anything else is internal.
func AsResourceError(err error) *resource.Error {
	if err == nil {
		return nil
	}
	var rerr *resource.Error
	if errors.As(err, &rerr) {
		return rerr
	}
	var terr *TransportError
	if errors.As(err, &terr) {
		return terr.ResourceError()
	}
	return resource.Errorf(resource.CodeInternal, "provider failed")
}

// OutcomeCode is the single token a log line records for an invocation. It is
// either "ok", a typed error code, or a transport reason — never a message.
func OutcomeCode(err error) string {
	if err == nil {
		return "ok"
	}
	var terr *TransportError
	if errors.As(err, &terr) {
		return string(terr.Reason)
	}
	var rerr *resource.Error
	if errors.As(err, &rerr) {
		return string(rerr.Code)
	}
	return "internal"
}
