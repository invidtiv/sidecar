package uiapi

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Error codes the API adds to the mobile protocol's refusal vocabulary.
const (
	CodeHostRefused     = "host_refused"
	CodeOriginRefused   = "origin_refused"
	CodeMutationRefused = "mutation_refused"
	CodeUnauthenticated = "unauthenticated"
	CodeLoginRefused    = "tailnet_login_refused"
	CodeLocalOnly       = "local_only"
	CodeNotServedHere   = "not_served_here"
	CodeNotFound        = "not_found"
	CodeMethod          = "method_not_allowed"
	CodeInvalidRequest  = "invalid_request"
	CodeOriginNotFound  = "origin_not_found"
	CodePairingInvalid  = "pairing_code_invalid"
	CodeTooMany         = "too_many_outstanding"
	CodeBackend         = "backend"
	CodeUpgradeRequired = "upgrade_required"
)

var errTooManyOutstanding = errors.New("too many outstanding pairing codes or tickets")

// ErrorBody is the shape of every API error.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail names a machine code and one human sentence that says what to do.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is a decoded API refusal, as the CLI client reports it.
type APIError struct {
	Status int
	ErrorDetail
}

func (e *APIError) Error() string { return e.Message }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// json.Encoder, not Marshal: the trailing newline and HTML escaping are the
	// same bytes `sidecar ... --json` writes for the same value.
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}
