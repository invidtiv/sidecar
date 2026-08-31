package contentservice

import (
	"errors"
	"fmt"
)

// Code classifies a content-service failure for CLI exit mapping.
//
// Usage/unknown-kind is version skew between two Sidecars (exit 2).
// Rejected is a value the host understood and refused (exit 5): unknown
// workspace, containment, not found. Internal is a load failure (exit 1).
type Code string

const (
	CodeUsage       Code = "usage"
	CodeUnknownKind Code = "unknown-kind"
	CodeRejected    Code = "rejected"
	CodeInternal    Code = "internal"
)

// Error is a classified content-service failure.
type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ExitCode is the CLI status this failure should produce.
func (e *Error) ExitCode() int {
	if e == nil {
		return 1
	}
	switch e.Code {
	case CodeUsage, CodeUnknownKind:
		return 2
	case CodeRejected:
		return 5
	default:
		return 1
	}
}

func Usage(format string, args ...any) *Error {
	return &Error{Code: CodeUsage, Message: fmt.Sprintf(format, args...)}
}

func UnknownKind(kind string) *Error {
	return &Error{Code: CodeUnknownKind, Message: fmt.Sprintf("unknown content kind %q", kind)}
}

func Rejected(format string, args ...any) *Error {
	return &Error{Code: CodeRejected, Message: fmt.Sprintf(format, args...)}
}

func Internal(message string, err error) *Error {
	return &Error{Code: CodeInternal, Message: message, Err: err}
}

// IsRejected reports a value-refusal the CLI maps to exit 5.
func IsRejected(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == CodeRejected
}

// MissingCapabilityError is a viewer-side refusal when the host does not
// advertise ContentReadV1. Slice 3 renders it as a toast; this slice only
// names the host.
type MissingCapabilityError struct {
	HostID string
}

func (e *MissingCapabilityError) Error() string {
	name := "that host"
	if e != nil && e.HostID != "" {
		name = e.HostID
	}
	return fmt.Sprintf("Update Sidecar on %s to open files and issues from that host.", name)
}
