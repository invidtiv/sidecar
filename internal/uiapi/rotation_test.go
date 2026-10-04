package uiapi

import (
	"net/http"
	"testing"
)

// Pairing an origin again rotates its token. Rotation is how an owner
// replaces a token they believe leaked, so it must also end every terminal and
// events stream opened with the old token, not only refuse new requests.
func TestRepairingAnOriginClosesStreamsOpenedWithTheOldToken(t *testing.T) {
	h, _ := eventsHarness(t)
	const app = "http://rotate.example"
	old := h.pairOrigin(app)
	bearer := http.Header{"Authorization": {"Bearer " + old}}
	terminal, err := h.dialBrowser(t, "", bearer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.CloseNow() })
	writeText(t, terminal, `{"before":true}`)
	if got := readText(t, terminal); got != `{"before":true}` {
		t.Fatalf("echo = %q", got)
	}
	events := dialEvents(t, h, "", bearer, false)
	t.Cleanup(func() { _ = events.CloseNow() })
	initialEvents(t, events)

	fresh := h.pairOrigin(app)
	if fresh == old {
		t.Fatal("re-pairing did not rotate the token")
	}
	if code, _ := closeStatus(t, terminal); code != CloseUnauthenticated {
		t.Fatalf("terminal opened with the rotated-out token closed with %d, want %d", code, CloseUnauthenticated)
	}
	if code, _ := closeStatus(t, events); code != CloseUnauthenticated {
		t.Fatalf("events opened with the rotated-out token closed with %d, want %d", code, CloseUnauthenticated)
	}
	// The new token works.
	conn, err := h.dialBrowser(t, "", http.Header{"Authorization": {"Bearer " + fresh}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	writeText(t, conn, `{"after":true}`)
	if got := readText(t, conn); got != `{"after":true}` {
		t.Fatalf("echo with rotated token = %q", got)
	}
}
