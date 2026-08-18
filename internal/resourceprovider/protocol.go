package resourceprovider

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/marcus/sidecar/internal/resource"
)

// Method names. An unknown method must return an internal error rather than
// crash the provider; the host never sends one.
const (
	MethodDescribe = "describe"
	MethodResolve  = "resolve"
)

// HostInfo identifies Sidecar to a provider. It carries no user, no project,
// no repository, and no environment.
type HostInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Request is the single JSON object written to a provider's stdin.
//
// DeadlineMs is advisory but accurate: it is exactly the timeout the host is
// about to enforce. A provider that budgets its own I/O inside it can return a
// typed `unavailable` — which gives the user a real error card and a working
// Retry — instead of being SIGKILLed, which gives them an opaque transport
// failure.
type Request struct {
	Protocol   string         `json:"protocol"`
	Method     string         `json:"method"`
	Instance   string         `json:"instance"`
	DeadlineMs int64          `json:"deadlineMs"`
	Host       *HostInfo      `json:"host,omitempty"`
	Params     *ResolveParams `json:"params,omitempty"`
}

// ResolveParams is the whole of what a resolve request carries. Widening it
// requires a named capability and an explicit per-instance permission, not a
// silent field addition.
type ResolveParams struct {
	Matcher string `json:"matcher"`
	Locator string `json:"locator"`
}

// Response is the single JSON object read from a provider's stdout. Exactly one
// of Provider+Matchers (describe), Resource (resolve), or Error is meaningful.
type Response struct {
	Protocol string                 `json:"protocol"`
	Provider *Info                  `json:"provider,omitempty"`
	Matchers []Matcher              `json:"matchers,omitempty"`
	Resource *resource.WireDocument `json:"resource,omitempty"`
	Error    *resource.WireError    `json:"error,omitempty"`
}

// decodeResponse enforces "exactly one JSON object on stdout". Anything else —
// no JSON, unparseable JSON, a second value, or trailing garbage — is a
// transport failure, because a provider that cannot keep its stdout clean is
// not one whose typed answers can be trusted either.
//
// Unknown fields are deliberately allowed through: forward compatibility is a
// protocol rule, so no decoder here may set DisallowUnknownFields.
func decodeResponse(stdout []byte) (*Response, TransportReason, string) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil, ReasonMalformed, "provider wrote nothing to stdout"
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		return nil, ReasonMalformed, "stdout was not one JSON object"
	}
	// Anything after the first value — a second object, a log line, a banner —
	// fails the invocation.
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, ReasonExtraOutput, "stdout carried more than one value"
	}
	if resp.Protocol != resource.Protocol {
		return nil, ReasonProtocol, "response protocol is not " + resource.Protocol
	}
	return &resp, "", ""
}

// hasDescribeShape reports whether the response carries a describe result. A
// provider block with no matchers is legitimate — a provider can be ready and
// currently recognize nothing.
func (r *Response) hasDescribeShape() bool {
	return r.Provider != nil || len(r.Matchers) > 0
}
