package uiapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// tailscale serve adds the owner's login to every request from the owner's
// devices, so on the Tailnet listener the login is ambient. It must not stand
// in for a paired origin's token: a page served from a paired origin (a dev
// server port on another tailnet machine, say) proves nothing by its Origin,
// and revoking or rotating that origin must reach its tailnet streams.
func TestPairedOriginOnTailnetNeedsItsOwnToken(t *testing.T) {
	h := newHarness(t)
	const app = "http://localhost:5173"
	token := h.pairOrigin(app)
	login := map[string]string{"Origin": app, tailscaleLoginHead: testTailnetLogin}

	// The ambient login alone opens nothing for the paired origin.
	response, data := h.tailnetDo(req{path: "/api/v0/sessions", header: login})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	conn := h.dialTailnet(t, http.Header{"Origin": {app}, tailscaleLoginHead: {testTailnetLogin}})
	if code, _ := closeStatus(t, conn); code != CloseUnauthenticated {
		t.Fatalf("paired origin with only the ambient login: close %d, want %d", code, CloseUnauthenticated)
	}
	// Another origin's token does not count either.
	other := h.pairOrigin("http://other.example")
	response, data = h.tailnetDo(req{path: "/api/v0/sessions", header: map[string]string{"Origin": app, tailscaleLoginHead: testTailnetLogin, "Authorization": "Bearer " + other}})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("another origin's token was accepted: %s", data)
	}

	// With its token it works, and its ticket opens a terminal.
	withToken := map[string]string{"Origin": app, tailscaleLoginHead: testTailnetLogin, "Authorization": "Bearer " + token}
	response, data = h.tailnetDo(req{path: "/api/v0/sessions", header: withToken})
	expect(t, response, data, http.StatusOK, "")
	response, data = h.tailnetDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(app, map[string]string{tailscaleLoginHead: testTailnetLogin, "Authorization": "Bearer " + token})})
	expect(t, response, data, http.StatusOK, "")
	var ticket TicketResponse
	if err := json.Unmarshal(data, &ticket); err != nil {
		t.Fatal(err)
	}
	conn = h.dialTailnetQuery(t, "?ticket="+ticket.Ticket, http.Header{"Origin": {app}, tailscaleLoginHead: {testTailnetLogin}})
	writeText(t, conn, `{"paired":true}`)
	if got := readText(t, conn); got != `{"paired":true}` {
		t.Fatalf("echo = %q", got)
	}

	// Revoking the origin closes that tailnet terminal.
	response, data = h.localDo(req{method: http.MethodDelete, path: "/api/v0/origins?origin=" + app})
	expect(t, response, data, http.StatusOK, "")
	if code, _ := closeStatus(t, conn); code != CloseUnauthenticated {
		t.Fatalf("revoked paired origin's tailnet terminal: close %d, want %d", code, CloseUnauthenticated)
	}

	// The listener's own origin keeps ambient access.
	own := h.dialTailnet(t, http.Header{"Origin": {"https://" + testTailnetHost}, tailscaleLoginHead: {testTailnetLogin}})
	writeText(t, own, `{"own":true}`)
	if got := readText(t, own); got != `{"own":true}` {
		t.Fatalf("own origin echo = %q", got)
	}
	_ = own.CloseNow()
}
