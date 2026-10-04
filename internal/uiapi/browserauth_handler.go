package uiapi

import (
	"errors"
	"net/http"
)

func (s *Server) browserProofOrigin(w http.ResponseWriter, c caller) bool {
	if !s.browserOrigins[c.origin] {
		writeError(w, http.StatusForbidden, CodeOriginRefused, "Browser key proofs require this server's exact Origin header.")
		return false
	}
	return true
}

func (s *Server) handleSessionProofChallenge(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.browserProofOrigin(w, c) {
		return
	}
	var body SessionProofChallengeRequest
	if !decodeBody(w, r, &body) {
		return
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	challenge, err := s.auth.issueBrowserProof(c.origin, body.RegistrationID)
	if err != nil {
		writeBrowserProofError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, challenge)
}

func (s *Server) handleSessionProofVerify(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.browserProofOrigin(w, c) {
		return
	}
	var body SessionProofRequest
	if !decodeBody(w, r, &body) {
		return
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	token, err := s.auth.verifyBrowserProof(c.origin, body)
	if err != nil {
		writeBrowserProofError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, token)
}

func writeBrowserProofError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBrowserProofInvalid):
		writeError(w, http.StatusUnauthorized, CodeSessionProofInvalid, errBrowserProofInvalid.Error())
	case errors.Is(err, errTooManyOutstanding):
		writeError(w, http.StatusTooManyRequests, CodeTooMany, "Too many unused browser proofs or active tokens; retry after an existing proof or token expires.")
	default:
		writeError(w, http.StatusServiceUnavailable, CodeBackend, err.Error())
	}
}
