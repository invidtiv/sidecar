package resource

import "fmt"

// Code is a stable v1 protocol error code. It is control flow; the message
// beside it is not.
type Code string

// The stable v1 codes. Anything else coerces to CodeInternal.
const (
	CodeNotFound      Code = "not_found"
	CodeUnauthorized  Code = "unauthorized"
	CodeForbidden     Code = "forbidden"
	CodeRateLimited   Code = "rate_limited"
	CodeInvalidConfig Code = "invalid_config"
	CodeUnavailable   Code = "unavailable"
	CodeInternal      Code = "internal"
)

// CoerceCode maps an arbitrary provider string onto a known code. An empty or
// unrecognized code is CodeInternal: the host would rather say "something went
// wrong" than invent a meaning a provider did not commit to.
func CoerceCode(v string) Code {
	switch Code(v) {
	case CodeNotFound:
		return CodeNotFound
	case CodeUnauthorized:
		return CodeUnauthorized
	case CodeForbidden:
		return CodeForbidden
	case CodeRateLimited:
		return CodeRateLimited
	case CodeInvalidConfig:
		return CodeInvalidConfig
	case CodeUnavailable:
		return CodeUnavailable
	default:
		return CodeInternal
	}
}

// DefaultRetryable is what the protocol document's table says a code typically
// means. It is the fallback when a provider omits `retryable`.
func DefaultRetryable(code Code) bool {
	switch code {
	case CodeRateLimited, CodeUnavailable, CodeInternal:
		return true
	default:
		return false
	}
}

// Error is a typed failure a provider reported, or one the host synthesized on
// its behalf. It is displayed, not interpreted: Message and SetupHint are text
// the user reads and may copy, and Sidecar never executes SetupHint.
type Error struct {
	Code      Code
	Message   string
	Retryable bool
	// SetupHint is copyable text only.
	SetupHint string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil resource error>"
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Errorf builds a host-synthesized typed error with the code's default
// retryability.
func Errorf(code Code, format string, args ...any) *Error {
	c := CoerceCode(string(code))
	return &Error{
		Code:      c,
		Message:   SanitizeLine(fmt.Sprintf(format, args...), MaxMessageChars),
		Retryable: DefaultRetryable(c),
	}
}
