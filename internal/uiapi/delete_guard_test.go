package uiapi

import (
	"net/http"
	"testing"
)

// The mutation guard reads headers only. A DELETE needs the JSON content type
// and X-Sidecar-Request like any mutation, but its body is never parsed: none,
// {} and anything else under the size cap reach the route alike. The Local
// socket applies no mutation guard.
func TestDeleteNeedsMutationHeadersButNoBody(t *testing.T) {
	h := newHarness(t)
	key, paired := h.pairBrowserKey()
	bearer := h.renewBrowser(key, paired.RegistrationID).Token
	path := "/api/v0/pairing/sessions/" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	headers := func(contentType string) map[string]string {
		out := map[string]string{"Origin": h.ownOrigin(), "X-Sidecar-Request": "1", "Authorization": "Bearer " + bearer}
		if contentType != "" {
			out["Content-Type"] = contentType
		}
		return out
	}
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		code                    string
	}{
		{"no content type", "", "", http.StatusForbidden, CodeMutationRefused},
		{"text/plain", "text/plain", "{}", http.StatusForbidden, CodeMutationRefused},
		{"JSON type, no body", "application/json", "", http.StatusNotFound, CodeDeviceNotFound},
		{"JSON type, empty object", "application/json", "{}", http.StatusNotFound, CodeDeviceNotFound},
		{"JSON type with charset", "application/json; charset=utf-8", "{}", http.StatusNotFound, CodeDeviceNotFound},
		{"JSON type, body ignored", "application/json", "not json", http.StatusNotFound, CodeDeviceNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b := h.browserDo(req{method: http.MethodDelete, path: path, body: tc.body, header: headers(tc.contentType)})
			expect(t, r, b, tc.status, tc.code)
		})
	}
	r, b := h.browserDo(req{method: http.MethodDelete, path: path, header: map[string]string{"Origin": h.ownOrigin(), "Content-Type": "application/json", "Authorization": "Bearer " + bearer}})
	expect(t, r, b, http.StatusForbidden, CodeMutationRefused)
	r, b = h.localDo(req{method: http.MethodDelete, path: path})
	expect(t, r, b, http.StatusNotFound, CodeDeviceNotFound)
}
