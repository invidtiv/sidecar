package uiapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

type slowSessionsBackend struct {
	*fakeBackend
	ctxErr chan error
}

func (b *slowSessionsBackend) Sessions(ctx context.Context, q mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	select {
	case <-ctx.Done():
		b.ctxErr <- ctx.Err()
		return mobileproto.CatalogSnapshot{}, ctx.Err()
	case <-time.After(700 * time.Millisecond):
		b.ctxErr <- nil
		return b.fakeBackend.Sessions(ctx, q)
	}
}

// A handler that runs longer than the body read deadline keeps its request
// context, on the Local and Browser listeners.
func TestSlowHandlersOutliveTheBodyReadDeadline(t *testing.T) {
	b := &slowSessionsBackend{fakeBackend: newFakeBackend(), ctxErr: make(chan error, 4)}
	h := newHarness(t, func(o *Options) { o.Backend = b; o.RequestReadTimeout = 200 * time.Millisecond })
	r, body := h.localDo(req{path: "/api/v0/sessions"})
	expect(t, r, body, http.StatusOK, "")
	if err := <-b.ctxErr; err != nil {
		t.Fatalf("local: the read deadline cancelled a slow handler: %v", err)
	}
	token := h.pairBrowser()
	r, body = h.browserDo(req{path: "/api/v0/sessions", header: map[string]string{"Authorization": "Bearer " + token}})
	expect(t, r, body, http.StatusOK, "")
	if err := <-b.ctxErr; err != nil {
		t.Fatalf("browser: the read deadline cancelled a slow handler: %v", err)
	}
}

// With a body, the deadline bounds only the body read: it is cleared before
// dispatch, so net/http's background read cannot cancel a long handler.
func TestBodyReadDeadlineIsClearedBeforeTheHandler(t *testing.T) {
	s := &Server{opts: Options{RequestReadTimeout: 200 * time.Millisecond}}
	type result struct {
		ctxErr error
		body   string
	}
	results := make(chan result, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.readBodyPromptly(w, r) {
			return
		}
		data, _ := io.ReadAll(r.Body)
		time.Sleep(600 * time.Millisecond)
		results <- result{r.Context().Err(), string(data)}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"code":"K7Q-4MX"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	got := <-results
	if resp.StatusCode != http.StatusNoContent || got.ctxErr != nil || got.body != `{"code":"K7Q-4MX"}` {
		t.Fatalf("status %d, handler ctx err %v, body %q", resp.StatusCode, got.ctxErr, got.body)
	}
}

// Keep-alive connections survive idle gaps longer than the deadline, and an
// oversized body is still refused by the handler's cap.
func TestBodyReadDeadlineKeepAliveAndCap(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.RequestReadTimeout = 200 * time.Millisecond })
	token := h.pairBrowser()
	for i := 0; i < 3; i++ {
		r, b := h.browserDo(req{method: http.MethodPost, path: accessDenyPath, body: `{"request_id":"none"}`, header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + token})})
		expect(t, r, b, http.StatusNotFound, CodeAccessNotFound)
		time.Sleep(300 * time.Millisecond)
	}
	big := `{"code":"` + strings.Repeat("x", maxBodyBytes) + `"}`
	r, b := h.browserDo(req{method: http.MethodPost, path: accessApprovePath, body: big, header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + token})})
	expect(t, r, b, http.StatusBadRequest, CodeInvalidRequest)
}

// Typing the code of a request that just expired or was evicted is a stale
// screen, not a guess: it gets its own refusal and does not count toward the
// wrong-code lockout.
func TestStaleCodesDoNotCountTowardTheLockout(t *testing.T) {
	h := newHarness(t)
	_, evicted := h.requestAccess("owner")
	h.clock.Advance(time.Second)
	h.requestAccess("x1")
	h.clock.Advance(time.Second)
	_, live := h.requestAccess("x2")
	if status := h.accessStatus(evicted); status.Status != accessStatusExpired {
		t.Fatalf("evicted = %+v", status)
	}
	for i := 0; i < maxAccessCodeFailures+2; i++ {
		r, b := h.localApprove(evicted.Code)
		expect(t, r, b, http.StatusNotFound, CodeAccessExpired)
	}
	r, b := h.localApprove(live.Code)
	expect(t, r, b, http.StatusOK, "")
	// An approved code typed again is no longer pending, and also not a
	// guess.
	for i := 0; i < maxAccessCodeFailures+1; i++ {
		r, b = h.localApprove(live.Code)
		expect(t, r, b, http.StatusNotFound, CodeAccessCodeInvalid)
	}
	if n := len(recentFailures(h.s.auth.accessFailures["local"], h.clock.Now())); n != 0 {
		t.Fatalf("stale codes counted %d failures", n)
	}
}

// A stored label that a newer sanitizer would clean differently is tidied on
// read, not treated as corruption; a label that breaks the stable invariant
// (control or format characters, over-long) still is.
func TestStoredLabelsAreRecleanedNotRefused(t *testing.T) {
	h := newHarness(t)
	_, paired := h.pairBrowserKey()
	path := h.s.auth.sessionPath
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := h.s.opts
	opts.Port = h.s.browserPort
	// Shutdown can report a listener its Serve goroutine had not yet
	// adopted; the state on disk is what matters here.
	_ = h.s.Shutdown(context.Background())
	rewrite := func(label string) {
		t.Helper()
		var stored map[string]any
		if err := json.Unmarshal(raw, &stored); err != nil {
			t.Fatal(err)
		}
		reg := stored["registrations"].(map[string]any)[paired.RegistrationID].(map[string]any)
		reg["label"] = label
		data, _ := json.Marshal(stored)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	rewrite("Safari\u3164on  iPad") // a filler and a double space: valid, not clean
	next, err := Start(opts)
	if err != nil {
		t.Fatalf("a label the sanitizer would tidy refused the store: %v", err)
	}
	devices, err := next.auth.listDevices("")
	_ = next.Shutdown(context.Background())
	if err != nil || len(devices) != 1 || devices[0].Label != "Safari on iPad" {
		t.Fatalf("devices = %+v (%v)", devices, err)
	}
	for _, bad := range []string{"a\u202eb", "a\x07b", strings.Repeat("x", maxDeviceLabelRunes+1)} {
		rewrite(bad)
		if s, err := Start(opts); err == nil {
			_ = s.Shutdown(context.Background())
			t.Fatalf("label %q accepted", bad)
		}
	}
}
