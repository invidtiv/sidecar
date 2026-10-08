package uiapi

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	notification "github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/terminallink"
)

// requestAccess does what the SDK does for a browser with no registration.
func (h *harness) requestAccess(label string) (*ecdsa.PrivateKey, AccessRequestCreated) {
	h.t.Helper()
	key, pub := browserTestKey(h.t)
	return key, h.requestAccessWithKey(pub, label)
}

func (h *harness) requestAccessWithKey(pub BrowserPublicKey, label string) AccessRequestCreated {
	h.t.Helper()
	body, _ := json.Marshal(AccessRequestCreate{PublicKey: pub, Label: label})
	r, b := h.browserDo(req{method: http.MethodPost, path: accessRequestsPath, body: string(body), header: mutationHeaders(h.ownOrigin(), nil)})
	expect(h.t, r, b, http.StatusOK, "")
	var created AccessRequestCreated
	if err := json.Unmarshal(b, &created); err != nil {
		h.t.Fatal(err)
	}
	return created
}

func (h *harness) pollAccess(created AccessRequestCreated) (*http.Response, []byte) {
	h.t.Helper()
	return h.browserDo(req{path: accessRequestsPath + "/" + created.RequestID, header: map[string]string{"Authorization": "Request " + created.PollSecret}})
}

func (h *harness) accessStatus(created AccessRequestCreated) AccessRequestStatus {
	h.t.Helper()
	r, b := h.pollAccess(created)
	expect(h.t, r, b, http.StatusOK, "")
	var status AccessRequestStatus
	if err := json.Unmarshal(b, &status); err != nil {
		h.t.Fatal(err)
	}
	return status
}

func (h *harness) localApprove(code string) (*http.Response, []byte) {
	h.t.Helper()
	body, _ := json.Marshal(AccessApproveRequest{Code: code})
	return h.localDo(req{method: http.MethodPost, path: accessApprovePath, body: string(body)})
}

func (h *harness) accessNotifications() []notification.Notification {
	h.t.Helper()
	all, err := notification.ReadAll(notification.Path(h.state))
	if err != nil {
		h.t.Fatal(err)
	}
	out := []notification.Notification{}
	for _, n := range all {
		if n.Source == notification.SourceAccessRequest && !n.Dismissed() {
			out = append(out, n)
		}
	}
	return out
}

func TestAccessCodeRules(t *testing.T) {
	for input, want := range map[string]string{"K7Q-4MX": "K7Q4MX", "k7q4mx": "K7Q4MX", " k7q 4mx ": "K7Q4MX", "O1L-IOO": "011100"} {
		got, ok := NormalizeAccessCode(input)
		if !ok || got != want {
			t.Fatalf("NormalizeAccessCode(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "K7Q4M", "K7Q4MXX", "K7Q-4MU", "K7Q_4MX"} {
		if _, ok := NormalizeAccessCode(input); ok {
			t.Fatalf("NormalizeAccessCode(%q) accepted", input)
		}
	}
	if FormatAccessCode("K7Q4MX") != "K7Q-4MX" {
		t.Fatal("display form")
	}
	for i := 0; i < 200; i++ {
		code, err := newAccessCode()
		if err != nil {
			t.Fatal(err)
		}
		if normalized, ok := NormalizeAccessCode(code); !ok || normalized != code {
			t.Fatalf("generated code %q is not canonical", code)
		}
	}
	if got := cleanDeviceLabel("  Safari\u202e on\tmacOS\x00 \n"); got != "Safari on macOS" {
		t.Fatalf("label = %q", got)
	}
	if got := cleanDeviceLabel(strings.Repeat("é", 100)); len([]rune(got)) != maxDeviceLabelRunes {
		t.Fatalf("label not capped: %d runes", len([]rune(got)))
	}
}

func TestAccessApproverAuthorizationRule(t *testing.T) {
	for _, c := range []caller{{auth: "local"}, {auth: "session", client: "session:abc"}, {auth: "tailnet", login: "a@b"}} {
		if !mayApprove(c) {
			t.Fatalf("%+v may approve", c)
		}
	}
	for _, c := range []caller{{auth: "bearer", client: "origin:https://app.example"}, {auth: "ticket"}, {}} {
		if mayApprove(c) {
			t.Fatalf("%+v must not approve", c)
		}
	}
	cases := []struct {
		c       caller
		surface string
		want    string
		ok      bool
	}{
		{caller{auth: "local"}, "", approvedViaCLI, true},
		{caller{auth: "local"}, "tui", approvedViaTUI, true},
		{caller{auth: "local"}, "web", "", false},
		{caller{auth: "session", client: "session:abc"}, "", "browser:abc", true},
		{caller{auth: "session", client: "session:abc"}, "tui", "", false},
		{caller{auth: "tailnet", login: "a@b"}, "", "tailnet:a@b", true},
		{caller{auth: "bearer"}, "", "", false},
	}
	for _, tc := range cases {
		got, err := approvedVia(tc.c, tc.surface)
		if (err == nil) != tc.ok || got != tc.want {
			t.Fatalf("approvedVia(%+v, %q) = %q, %v", tc.c, tc.surface, got, err)
		}
	}
}

// The whole journey: request, approve by code, poll, renew with the same key.
// Approval creates a registration for exactly the requesting key and origin
// and hands no credential to anyone.
func TestAccessRequestApprovalBindsTheRequestingKey(t *testing.T) {
	h := newHarness(t)
	key, created := h.requestAccess("Safari on macOS")
	if created.RequestID == "" || created.PollSecret == "" || len(created.Code) != 7 || created.Code[3] != '-' {
		t.Fatalf("created = %+v", created)
	}
	if !created.ExpiresAt.Equal(h.clock.Now().Add(accessRequestTTL)) {
		t.Fatalf("expires_at = %v", created.ExpiresAt)
	}
	if status := h.accessStatus(created); status.Status != accessStatusPending || status.RegistrationID != "" {
		t.Fatalf("status = %+v", status)
	}
	r, b := h.localDo(req{path: accessRequestsPath})
	expect(t, r, b, http.StatusOK, "")
	if strings.Contains(string(b), strings.ReplaceAll(created.Code, "-", "")) || strings.Contains(string(b), created.Code) || strings.Contains(string(b), created.PollSecret) {
		t.Fatalf("the list leaks the code or poll secret: %s", b)
	}
	var list AccessRequestList
	if err := json.Unmarshal(b, &list); err != nil || len(list.Requests) != 1 {
		t.Fatalf("list = %s (%v)", b, err)
	}
	if got := list.Requests[0]; got.RequestID != created.RequestID || got.Label != "Safari on macOS" || got.Origin != h.ownOrigin() || got.Address != "127.0.0.1" {
		t.Fatalf("listed = %+v", got)
	}
	// Session proof is refused until approval: no registration exists yet.
	id := browserRegistrationID(h.ownOrigin(), publicTestKey(key))
	body, _ := json.Marshal(SessionProofChallengeRequest{RegistrationID: id})
	r, b = h.browserDo(req{method: http.MethodPost, path: "/api/v0/pairing/session-proof", body: string(body), header: mutationHeaders(h.ownOrigin(), nil)})
	expect(t, r, b, http.StatusUnauthorized, CodeSessionProofInvalid)

	r, b = h.localApprove(strings.ToLower(created.Code))
	expect(t, r, b, http.StatusOK, "")
	if strings.Contains(string(b), "token") {
		t.Fatalf("approval returned a credential: %s", b)
	}
	var approval AccessApproval
	if err := json.Unmarshal(b, &approval); err != nil {
		t.Fatal(err)
	}
	if approval.RegistrationID != id || approval.ApprovedVia != approvedViaCLI || approval.Origin != h.ownOrigin() || approval.RequestID != created.RequestID {
		t.Fatalf("approval = %+v", approval)
	}
	status := h.accessStatus(created)
	if status.Status != accessStatusApproved || status.RegistrationID != id {
		t.Fatalf("status = %+v", status)
	}
	// The requesting key renews a session; any other key cannot use the
	// registration, and the registration is bound to its origin.
	token := h.renewBrowser(key, id)
	h.expectBearer(token.Token, http.StatusOK)
	other, _ := browserTestKey(t)
	proof := signedProof(t, other, h.ownOrigin(), id, h.proofChallenge(id))
	r, b = h.submitProof(h.ownOrigin(), proof)
	expect(t, r, b, http.StatusUnauthorized, CodeSessionProofInvalid)
	localhost := strings.Replace(h.ownOrigin(), "127.0.0.1", "localhost", 1)
	r, b = h.browserDo(req{method: http.MethodPost, path: "/api/v0/pairing/session-proof", body: string(body), header: mutationHeaders(localhost, nil)})
	expect(t, r, b, http.StatusUnauthorized, CodeSessionProofInvalid)

	// The device list shows how it got in.
	r, b = h.localDo(req{path: devicesPath})
	expect(t, r, b, http.StatusOK, "")
	var devices DeviceList
	if err := json.Unmarshal(b, &devices); err != nil || len(devices.Devices) != 1 {
		t.Fatalf("devices = %s (%v)", b, err)
	}
	if d := devices.Devices[0]; d.ID != id || d.Label != "Safari on macOS" || d.ApprovedVia != approvedViaCLI || d.ApprovedAt.IsZero() || d.Current {
		t.Fatalf("device = %+v", d)
	}
	// Approving the same code again finds nothing pending.
	r, b = h.localApprove(created.Code)
	expect(t, r, b, http.StatusNotFound, CodeAccessCodeInvalid)
}

// The unauthenticated routes take the pairing exchange's guards: own exact
// Origin, mutation headers, Browser listener only, and never a credential.
func TestAccessRequestRoutesKeepTheExchangeGuards(t *testing.T) {
	h := newHarness(t)
	_, pub := browserTestKey(t)
	body, _ := json.Marshal(AccessRequestCreate{PublicKey: pub})
	const app = "http://app.example:5173"
	appToken := h.pairOrigin(app)
	for name, r := range map[string]req{
		"foreign origin":       {method: http.MethodPost, path: accessRequestsPath, body: string(body), header: mutationHeaders("http://evil.example", nil)},
		"paired origin":        {method: http.MethodPost, path: accessRequestsPath, body: string(body), header: mutationHeaders(app, nil)},
		"bearer and no origin": {method: http.MethodPost, path: accessRequestsPath, body: string(body), header: map[string]string{"Authorization": "Bearer " + appToken, "Content-Type": "application/json", "X-Sidecar-Request": "1"}},
		"no origin":            {method: http.MethodPost, path: accessRequestsPath, body: string(body), header: map[string]string{"Content-Type": "application/json", "X-Sidecar-Request": "1"}},
	} {
		r, b := h.browserDo(r)
		expect(t, r, b, http.StatusForbidden, CodeOriginRefused)
		_ = name
	}
	r, b := h.browserDo(req{method: http.MethodPost, path: accessRequestsPath, body: string(body), header: map[string]string{"Origin": h.ownOrigin(), "Content-Type": "text/plain"}})
	expect(t, r, b, http.StatusForbidden, CodeMutationRefused)
	r, b = h.browserDo(req{method: http.MethodPost, path: accessRequestsPath, body: string(body), host: "evil.example:80", header: mutationHeaders(h.ownOrigin(), nil)})
	expect(t, r, b, http.StatusMisdirectedRequest, CodeHostRefused)
	r, b = h.browserDo(req{method: http.MethodPost, path: accessRequestsPath, body: `{"public_key":{"kty":"EC","crv":"P-256","x":"AA","y":"AA"}}`, header: mutationHeaders(h.ownOrigin(), nil)})
	expect(t, r, b, http.StatusBadRequest, CodeInvalidRequest)
	r, b = h.localDo(req{method: http.MethodPost, path: accessRequestsPath, body: string(body)})
	expect(t, r, b, http.StatusForbidden, CodeNotServedHere)
	r, b = h.tailnetDo(req{method: http.MethodPost, path: accessRequestsPath, body: string(body), header: mutationHeaders("https://"+testTailnetHost, map[string]string{tailscaleLoginHead: testTailnetLogin})})
	expect(t, r, b, http.StatusForbidden, CodeNotServedHere)

	created := h.requestAccessWithKey(pub, "")
	// Polling needs the request's own secret; a wrong one looks unknown.
	r, b = h.browserDo(req{path: accessRequestsPath + "/" + created.RequestID})
	expect(t, r, b, http.StatusNotFound, CodeAccessNotFound)
	r, b = h.browserDo(req{path: accessRequestsPath + "/" + created.RequestID, header: map[string]string{"Authorization": "Request wrong"}})
	expect(t, r, b, http.StatusNotFound, CodeAccessNotFound)
	r, b = h.browserDo(req{path: accessRequestsPath + "/" + created.RequestID, header: map[string]string{"Authorization": "Bearer " + created.PollSecret}})
	expect(t, r, b, http.StatusNotFound, CodeAccessNotFound)
	r, b = h.browserDo(req{path: accessRequestsPath + "/nope", header: map[string]string{"Authorization": "Request " + created.PollSecret}})
	expect(t, r, b, http.StatusNotFound, CodeAccessNotFound)
	r, b = h.browserDo(req{path: accessRequestsPath + "/" + created.RequestID, header: map[string]string{"Authorization": "Request " + created.PollSecret, "Origin": app}})
	expect(t, r, b, http.StatusForbidden, CodeOriginRefused)
	localhost := strings.Replace(h.ownOrigin(), "127.0.0.1", "localhost", 1)
	r, b = h.browserDo(req{path: accessRequestsPath + "/" + created.RequestID, header: map[string]string{"Authorization": "Request " + created.PollSecret, "Origin": localhost}})
	expect(t, r, b, http.StatusNotFound, CodeAccessNotFound)
	r, b = h.browserDo(req{path: accessRequestsPath + "/" + created.RequestID, header: map[string]string{"Authorization": "Request " + created.PollSecret, "Origin": h.ownOrigin()}})
	expect(t, r, b, http.StatusOK, "")
	r, _ = h.browserDo(req{method: http.MethodHead, path: accessRequestsPath + "/" + created.RequestID, header: map[string]string{"Authorization": "Request " + created.PollSecret}})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("HEAD poll = %d", r.StatusCode)
	}
	r, b = h.localDo(req{path: accessRequestsPath + "/" + created.RequestID, header: map[string]string{"Authorization": "Request " + created.PollSecret}})
	expect(t, r, b, http.StatusForbidden, CodeNotServedHere)
}

func TestAccessRequestLimitsAndReplacement(t *testing.T) {
	h := newHarness(t)
	_, first := h.requestAccess("one")
	_, pub := browserTestKey(t)
	h.requestAccessWithKey(pub, "two")
	_, third := browserTestKey(t)
	body, _ := json.Marshal(AccessRequestCreate{PublicKey: third})
	r, b := h.browserDo(req{method: http.MethodPost, path: accessRequestsPath, body: string(body), header: mutationHeaders(h.ownOrigin(), nil)})
	expect(t, r, b, http.StatusTooManyRequests, CodeTooMany)
	// The same browser asking again replaces its earlier request instead of
	// counting against the limit; the superseded request reports expired.
	again := h.requestAccessWithKey(pub, "two again")
	if again.Code == "" {
		t.Fatal("replacement refused")
	}
	if got := h.s.auth.listAccessRequests(); len(got) != 2 {
		t.Fatalf("pending = %+v", got)
	}
	// Approving frees a slot.
	r, b = h.localApprove(first.Code)
	expect(t, r, b, http.StatusOK, "")
	h.requestAccessWithKey(third, "three")

	// The total limit, across addresses, through the store.
	store := newAuthStore(time.Now)
	for i := 0; i < maxPendingAccessRequests; i++ {
		_, pub := browserTestKey(t)
		if _, _, err := store.createAccessRequest("http://127.0.0.1:1", "10.0.0."+string(rune('a'+i)), "", pub); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	_, pub17 := browserTestKey(t)
	if _, _, err := store.createAccessRequest("http://127.0.0.1:1", "10.0.1.1", "", pub17); err != errTooManyOutstanding {
		t.Fatalf("17th request: %v", err)
	}
	codes := map[string]bool{}
	for _, r := range store.access {
		if codes[r.code] {
			t.Fatal("two pending requests share a code")
		}
		codes[r.code] = true
	}
}

func TestAccessRequestExpiryAndRetention(t *testing.T) {
	h := newHarness(t)
	_, created := h.requestAccess("")
	h.clock.Advance(accessRequestTTL)
	if status := h.accessStatus(created); status.Status != accessStatusExpired {
		t.Fatalf("status = %+v", status)
	}
	if got := h.s.auth.listAccessRequests(); len(got) != 0 {
		t.Fatalf("expired request still listed: %+v", got)
	}
	r, b := h.localApprove(created.Code)
	expect(t, r, b, http.StatusNotFound, CodeAccessCodeInvalid)
	h.clock.Advance(accessSettledRetention)
	r, b = h.pollAccess(created)
	expect(t, r, b, http.StatusNotFound, CodeAccessNotFound)
}

func TestAccessWrongCodeLimitPerApprover(t *testing.T) {
	h := newHarness(t)
	_, created := h.requestAccess("")
	wrong := "000000"
	if strings.ReplaceAll(created.Code, "-", "") == wrong {
		wrong = "111111"
	}
	r, b := h.localApprove("bad")
	expect(t, r, b, http.StatusBadRequest, CodeInvalidRequest) // malformed: not counted
	for i := 0; i < maxAccessCodeFailures; i++ {
		r, b = h.localApprove(wrong)
		expect(t, r, b, http.StatusNotFound, CodeAccessCodeInvalid)
	}
	// Locked out: even the right code is refused until the window passes.
	r, b = h.localApprove(created.Code)
	expect(t, r, b, http.StatusTooManyRequests, CodeTooManyAttempts)
	// Another approver has its own budget.
	token := h.pairBrowser()
	body, _ := json.Marshal(AccessApproveRequest{Code: wrong})
	r, b = h.browserDo(req{method: http.MethodPost, path: accessApprovePath, body: string(body), header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + token})})
	expect(t, r, b, http.StatusNotFound, CodeAccessCodeInvalid)
	h.clock.Advance(accessCodeFailureWindow)
	r, b = h.localApprove(created.Code)
	expect(t, r, b, http.StatusOK, "")
}

func TestAccessApproversAndPairedOriginRefusal(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	appToken := h.pairOrigin(app)
	appHeaders := func(method string) map[string]string {
		headers := map[string]string{"Authorization": "Bearer " + appToken, "Origin": app}
		if method != http.MethodGet {
			headers["Content-Type"] = "application/json"
			headers["X-Sidecar-Request"] = "1"
		}
		return headers
	}
	_, created := h.requestAccess("")
	for _, r := range []req{
		{path: accessRequestsPath},
		{method: http.MethodPost, path: accessApprovePath, body: `{"code":"` + created.Code + `"}`},
		{method: http.MethodPost, path: accessDenyPath, body: `{"request_id":"` + created.RequestID + `"}`},
		{path: devicesPath},
		{method: http.MethodDelete, path: devicesPath + "/" + strings.Repeat("a", 64)},
	} {
		r.header = appHeaders(r.method)
		response, data := h.browserDo(r)
		expect(t, response, data, http.StatusForbidden, CodeApproverRefused)
		// The same paired origin on the tailnet listener presents its own
		// token and is still not an approver.
		r.header[tailscaleLoginHead] = testTailnetLogin
		response, data = h.tailnetDo(r)
		expect(t, response, data, http.StatusForbidden, CodeApproverRefused)
	}
	if status := h.accessStatus(created); status.Status != accessStatusPending {
		t.Fatalf("a paired origin changed the request: %+v", status)
	}

	// A signed-in browser approves, recorded as browser:<its registration>.
	_, paired := h.pairBrowserKey()
	session := paired.Token
	auth := map[string]string{"Authorization": "Bearer " + session}
	r, b := h.browserDo(req{path: accessRequestsPath, header: auth})
	expect(t, r, b, http.StatusOK, "")
	body, _ := json.Marshal(AccessApproveRequest{Code: created.Code, Surface: "tui"})
	r, b = h.browserDo(req{method: http.MethodPost, path: accessApprovePath, body: string(body), header: mutationHeaders(h.ownOrigin(), auth)})
	expect(t, r, b, http.StatusBadRequest, CodeInvalidRequest)
	body, _ = json.Marshal(AccessApproveRequest{Code: created.Code})
	r, b = h.browserDo(req{method: http.MethodPost, path: accessApprovePath, body: string(body), header: mutationHeaders(h.ownOrigin(), auth)})
	expect(t, r, b, http.StatusOK, "")
	var approval AccessApproval
	_ = json.Unmarshal(b, &approval)
	if approval.ApprovedVia != "browser:"+paired.RegistrationID {
		t.Fatalf("approved_via = %q", approval.ApprovedVia)
	}
	// The browser's own registration is marked current in its device list.
	r, b = h.browserDo(req{path: devicesPath, header: auth})
	expect(t, r, b, http.StatusOK, "")
	var devices DeviceList
	_ = json.Unmarshal(b, &devices)
	current := 0
	for _, d := range devices.Devices {
		if d.Current {
			current++
			if d.ID != paired.RegistrationID {
				t.Fatalf("current device = %s", d.ID)
			}
		}
	}
	if len(devices.Devices) != 2 || current != 1 {
		t.Fatalf("devices = %+v", devices)
	}

	// An allowed tailnet login approves too.
	_, next := h.requestAccess("")
	login := mutationHeaders("https://"+testTailnetHost, map[string]string{tailscaleLoginHead: testTailnetLogin})
	body, _ = json.Marshal(AccessApproveRequest{Code: next.Code})
	r, b = h.tailnetDo(req{method: http.MethodPost, path: accessApprovePath, body: string(body), header: login})
	expect(t, r, b, http.StatusOK, "")
	_ = json.Unmarshal(b, &approval)
	if approval.ApprovedVia != "tailnet:"+testTailnetLogin {
		t.Fatalf("approved_via = %q", approval.ApprovedVia)
	}
	// Without a credential nobody lists or approves.
	r, b = h.browserDo(req{path: accessRequestsPath})
	expect(t, r, b, http.StatusUnauthorized, CodeUnauthenticated)
	r, b = h.browserDo(req{method: http.MethodPost, path: accessApprovePath, body: string(body), header: mutationHeaders(h.ownOrigin(), nil)})
	expect(t, r, b, http.StatusUnauthorized, CodeUnauthenticated)
}

func TestAccessDeny(t *testing.T) {
	h := newHarness(t)
	_, created := h.requestAccess("")
	r, b := h.localDo(req{method: http.MethodPost, path: accessDenyPath, body: `{"request_id":"` + created.RequestID + `"}`})
	expect(t, r, b, http.StatusOK, "")
	if status := h.accessStatus(created); status.Status != accessStatusDenied {
		t.Fatalf("status = %+v", status)
	}
	r, b = h.localDo(req{method: http.MethodPost, path: accessDenyPath, body: `{"request_id":"` + created.RequestID + `"}`})
	expect(t, r, b, http.StatusNotFound, CodeAccessNotFound)
	r, b = h.localApprove(created.Code)
	expect(t, r, b, http.StatusNotFound, CodeAccessCodeInvalid)
}

// Revoking one device signs out only that browser: its bearer gets 401 and
// its streams close with 4401, as the bulk revocation does.
func TestDeviceRevokeClosesThatDevicesStreams(t *testing.T) {
	h, _ := eventsHarness(t)
	_, first := h.pairBrowserKey()
	_, second := h.pairBrowserKey()
	victim := dialEvents(t, h, "", http.Header{"Authorization": {"Bearer " + first.Token}}, false)
	initialEvents(t, victim)
	survivor := dialEvents(t, h, "", http.Header{"Authorization": {"Bearer " + second.Token}}, false)
	initialEvents(t, survivor)
	terminal, err := h.dialBrowser(t, "", http.Header{"Authorization": {"Bearer " + first.Token}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = terminal.CloseNow() }()
	// The terminal registers asynchronously; wait until status shows it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		clients, _ := h.s.clients.snapshot()
		n := 0
		for _, c := range clients {
			if c.Kind == "terminal" {
				n++
			}
		}
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, b := h.localDo(req{method: http.MethodDelete, path: devicesPath + "/" + first.RegistrationID})
	expect(t, r, b, http.StatusOK, "")
	var revocation DeviceRevocation
	if err := json.Unmarshal(b, &revocation); err != nil || !revocation.Revoked || revocation.ID != first.RegistrationID || revocation.TerminalsClosed != 1 {
		t.Fatalf("revocation = %s (%v)", b, err)
	}
	if code, _ := closeStatus(t, victim); code != CloseUnauthenticated {
		t.Fatalf("events close = %d", code)
	}
	if code, _ := closeStatus(t, terminal); code != CloseUnauthenticated {
		t.Fatalf("terminal close = %d", code)
	}
	h.expectBearer(first.Token, http.StatusUnauthorized)
	h.expectBearer(second.Token, http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, _, err := survivor.Read(ctx); err == nil || ctx.Err() == nil {
		t.Fatalf("the other device's stream was disturbed: %v", err)
	}
	r, b = h.localDo(req{path: devicesPath})
	expect(t, r, b, http.StatusOK, "")
	if strings.Contains(string(b), first.RegistrationID) || !strings.Contains(string(b), second.RegistrationID) {
		t.Fatalf("devices after revoke = %s", b)
	}
	r, b = h.localDo(req{method: http.MethodDelete, path: devicesPath + "/" + first.RegistrationID})
	expect(t, r, b, http.StatusNotFound, CodeDeviceNotFound)
	// Revocation is durable.
	restartSessionHarness(t, h)
	h.expectBearer(second.Token, http.StatusUnauthorized) // bearers are memory-only
	records, err := readSessions(filepath.Join(Dir(h.state), sessionsFileName))
	if err != nil || records[first.RegistrationID].Origin != "" || records[second.RegistrationID].Origin == "" {
		t.Fatalf("store after revoke = %+v (%v)", records, err)
	}
}

func TestSessionStoreMigratesVersionOneInPlace(t *testing.T) {
	h := newHarness(t)
	_, paired := h.pairBrowserKey()
	path := filepath.Join(Dir(h.state), sessionsFileName)
	records, err := readSessions(path)
	if err != nil {
		t.Fatal(err)
	}
	record := records[paired.RegistrationID]
	if record.ApprovedVia != approvedViaLink || record.ApprovedAt.IsZero() {
		t.Fatalf("link registration = %+v", record)
	}
	// Rewrite it as a version-1 store, without the device fields.
	v1 := map[string]any{"version": 1, "registrations": map[string]any{paired.RegistrationID: map[string]any{
		"public_key": record.PublicKey, "origin": record.Origin, "created_at": record.CreatedAt, "last_used_at": record.LastUsedAt, "expires_at": record.ExpiresAt}}}
	data, _ := json.Marshal(v1)
	opts := h.s.opts
	opts.Port = h.s.browserPort
	if err := h.s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	next, err := Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Shutdown(context.Background()) })
	raw, _ := os.ReadFile(path)
	var stored sessionsFile
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Version != sessionsVersion {
		t.Fatalf("store not migrated: %s (%v)", raw, err)
	}
	migrated := stored.Registrations[paired.RegistrationID]
	if migrated.ApprovedVia != approvedViaLink || !migrated.ApprovedAt.Equal(record.CreatedAt) || migrated.Label != "" {
		t.Fatalf("migrated = %+v", migrated)
	}
	// A version-2 store with an invalid approval record is corrupt.
	bad := strings.Replace(string(raw), `"approved_via": "link"`, `"approved_via": "magic"`, 1)
	if err := next.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Start(opts); err == nil {
		_ = s.Shutdown(context.Background())
		t.Fatal("invalid approved_via accepted")
	}
	// A version-1 store that already carries device fields is corrupt.
	v1bad := strings.Replace(string(data), `"origin"`, `"label":"x","origin"`, 1)
	if err := os.WriteFile(path, []byte(v1bad), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Start(opts); err == nil {
		_ = s.Shutdown(context.Background())
		t.Fatal("version 1 store with device fields accepted")
	}
}

// One notification stands for every pending request, and is withdrawn when
// the last one is approved, denied or expires.
func TestAccessNotificationCoalescesAndWithdraws(t *testing.T) {
	h := newHarness(t)
	_, first := h.requestAccess("Safari on macOS")
	notes := h.accessNotifications()
	if len(notes) != 1 || !strings.Contains(notes[0].Body, `"Safari on macOS"`) || strings.Contains(notes[0].Body, first.Code) {
		t.Fatalf("notifications = %+v", notes)
	}
	// Activating it means approving; nothing in it is a jump target.
	if ctas := notification.CallsToAction(notes[0], terminallink.Options{}); len(ctas) != 0 {
		t.Fatalf("the access notification offers calls to action: %+v", ctas)
	}
	_, second := h.requestAccess("")
	if notes := h.accessNotifications(); len(notes) != 1 {
		t.Fatalf("a burst posted %d notifications", len(notes))
	}
	r, b := h.localDo(req{method: http.MethodPost, path: accessDenyPath, body: `{"request_id":"` + first.RequestID + `"}`})
	expect(t, r, b, http.StatusOK, "")
	if notes := h.accessNotifications(); len(notes) != 1 {
		t.Fatal("withdrawn while a request is still pending")
	}
	r, b = h.localApprove(second.Code)
	expect(t, r, b, http.StatusOK, "")
	if notes := h.accessNotifications(); len(notes) != 0 {
		t.Fatalf("not withdrawn after the last request: %+v", notes)
	}
	// A new request after withdrawal posts again; expiry withdraws it.
	h.requestAccess("")
	if notes := h.accessNotifications(); len(notes) != 1 {
		t.Fatal("no notification for a new request")
	}
	h.clock.Advance(accessRequestTTL)
	h.s.sweepAccess()
	if notes := h.accessNotifications(); len(notes) != 0 {
		t.Fatal("not withdrawn on expiry")
	}
	// A notification left by a dead server is withdrawn at startup.
	h.requestAccess("")
	opts := h.s.opts
	opts.Port = h.s.browserPort
	h.s.accessMu.Lock()
	h.s.accessNoteID = "" // simulate a crash: shutdown cannot withdraw it
	h.s.accessMu.Unlock()
	if err := h.s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notes := h.accessNotifications(); len(notes) != 1 {
		t.Fatal("expected the crashed server's notification to remain")
	}
	next, err := Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Shutdown(context.Background()) })
	if notes := h.accessNotifications(); len(notes) != 0 {
		t.Fatal("stale access notification survived a restart")
	}
}

// Approvers' events streams hear about new requests; a paired origin's do not.
func TestAccessRequestedEventReachesApproversOnly(t *testing.T) {
	h, _ := eventsHarness(t)
	const app = "http://app.example:5173"
	appToken := h.pairOrigin(app)
	local := dialEvents(t, h, "", nil, true)
	initialEvents(t, local)
	paired := dialEvents(t, h, "", http.Header{"Authorization": {"Bearer " + appToken}}, false)
	initialEvents(t, paired)
	_, created := h.requestAccess("Firefox")
	event := readEvent(t, local)
	if event.Type != "access_requested" || event.AccessRequest == nil || event.AccessRequest.RequestID != created.RequestID || event.AccessRequest.Label != "Firefox" {
		t.Fatalf("event = %+v", event)
	}
	raw, _ := json.Marshal(event)
	if strings.Contains(string(raw), created.Code) || strings.Contains(string(raw), strings.ReplaceAll(created.Code, "-", "")) || strings.Contains(string(raw), created.PollSecret) {
		t.Fatalf("event leaks the code or secret: %s", raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, data, err := paired.Read(ctx); err == nil {
		t.Fatalf("a paired origin heard an access request: %s", data)
	}
}
