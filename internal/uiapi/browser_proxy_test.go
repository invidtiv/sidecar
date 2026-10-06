package uiapi

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserProxyPairingAndGuards(t *testing.T) {
	const origin = "https://node.example.ts.net:7861"
	const host = "node.example.ts.net:7861"
	ui := t.TempDir()
	if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("proxy UI"), 0600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.BrowserProxyOrigin = origin; o.UIDir = ui })
	localhostToken := h.pairBrowser()
	send := func(r req) (*http.Response, []byte) { r.host = host; return h.browserDo(r) }
	r, b := send(req{path: "/"})
	expect(t, r, b, 200, "")
	if string(b) != "proxy UI" {
		t.Fatalf("static UI: %s", b)
	}
	for _, headers := range []map[string]string{nil, {tailscaleLoginHead: testTailnetLogin}, {"Origin": origin, tailscaleLoginHead: testTailnetLogin}} {
		r, b = send(req{path: "/api/v0/hello", header: headers})
		expect(t, r, b, 401, CodeUnauthenticated)
	}
	key, pub := browserTestKey(t)
	code := h.pairingCode("/")
	body, _ := json.Marshal(PairingExchangeRequest{Code: code.Code, PublicKey: pub})
	r, b = send(req{method: "POST", path: "/api/v0/pairing/exchange", body: string(body), header: mutationHeaders(origin, nil)})
	expect(t, r, b, 200, "")
	var paired PairingExchange
	if err := json.Unmarshal(b, &paired); err != nil {
		t.Fatal(err)
	}
	auth := map[string]string{"Origin": origin, "Authorization": "Bearer " + paired.Token}
	r, b = send(req{path: "/api/v0/hello", header: auth})
	expect(t, r, b, 200, "")
	for _, wrong := range []string{"https://node.example.ts.net", "https://node.example.ts.net:7862", "http://node.example.ts.net:7861", "https://evil.example"} {
		r, b = send(req{path: "/api/v0/hello", header: map[string]string{"Origin": wrong, "Authorization": auth["Authorization"], tailscaleLoginHead: testTailnetLogin}})
		expect(t, r, b, 403, CodeOriginRefused)
	}
	r, b = send(req{path: "/api/v0/hello", header: map[string]string{"Origin": origin, "Authorization": "Bearer " + localhostToken}})
	expect(t, r, b, 403, CodeOriginRefused)
	r, b = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + localhostToken}})
	expect(t, r, b, 200, "")
	r, b = h.browserDo(req{path: "/api/v0/hello", host: "node.example.ts.net:7862", header: auth})
	expect(t, r, b, 421, CodeHostRefused)
	// Public-key proof renewal also uses the exact proxy origin.
	challengeBody, _ := json.Marshal(SessionProofChallengeRequest{RegistrationID: paired.RegistrationID})
	r, b = send(req{method: "POST", path: "/api/v0/pairing/session-proof", body: string(challengeBody), header: mutationHeaders(origin, nil)})
	expect(t, r, b, 200, "")
	var challenge SessionProofChallenge
	if err := json.Unmarshal(b, &challenge); err != nil {
		t.Fatal(err)
	}
	proofBody, _ := json.Marshal(signedProof(t, key, origin, paired.RegistrationID, challenge))
	r, b = send(req{method: "POST", path: "/api/v0/pairing/session-proof/verify", body: string(proofBody), header: mutationHeaders(origin, nil)})
	expect(t, r, b, 200, "")
	for _, path := range []string{eventsPath, terminalPath} {
		for _, tc := range []struct {
			origin, login string
			code          websocket.StatusCode
		}{{origin, testTailnetLogin, CloseUnauthenticated}, {"https://evil.example", testTailnetLogin, CloseOriginRefused}} {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			c, _, err := websocket.Dial(ctx, strings.Replace(h.s.BrowserURL(), "http:", "ws:", 1)+path, &websocket.DialOptions{Host: host, HTTPHeader: http.Header{"Origin": {tc.origin}, tailscaleLoginHead: {tc.login}}})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			_, _, err = c.Read(ctx)
			_ = c.CloseNow()
			cancel()
			if websocket.CloseStatus(err) != tc.code {
				t.Fatalf("%s: %v", path, err)
			}
		}
		r, b = send(req{method: "POST", path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(origin, auth)})
		expect(t, r, b, 200, "")
		var ticket TicketResponse
		if err := json.Unmarshal(b, &ticket); err != nil {
			t.Fatal(err)
		}
		c, _, err := websocket.Dial(context.Background(), strings.Replace(h.s.BrowserURL(), "http:", "ws:", 1)+path+"?ticket="+ticket.Ticket, &websocket.DialOptions{Host: host, HTTPHeader: http.Header{"Origin": {origin}}})
		if err != nil {
			t.Fatal(err)
		}
		if path == eventsPath {
			initialEvents(t, c)
		} else {
			writeText(t, c, `{"version":0,"type":"hello","request_id":"proxy"}`)
			if got := readText(t, c); got != `{"version":0,"type":"hello","request_id":"proxy"}` {
				t.Fatalf("fake terminal handshake: %s", got)
			}
		}
		_ = c.CloseNow()
	}
}

func TestBrowserProxyOriginRejectsUnsafeURLs(t *testing.T) {
	for _, origin := range []string{"http://node.example.ts.net:7861", "https://node.example.ts.net/path", "https://user:pass@node.example.ts.net", "https://node.example.ts.net?x=1", "https://node.example.ts.net#x"} {
		_, err := Start(Options{StateDir: shortTempDir(t), Port: 0, Backend: newFakeBackend(), BrowserProxyOrigin: origin})
		if err == nil || !strings.Contains(err.Error(), "browserProxyOrigin") {
			t.Fatalf("invalid proxy origin accepted: %s, %v", origin, err)
		}
	}
}
