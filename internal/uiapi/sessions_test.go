package uiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func restartSessionHarness(t *testing.T, h *harness) {
	t.Helper()
	opts := h.s.opts
	opts.Port = h.s.browserPort // exact browser origin remains stable
	if err := h.s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	next, err := Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	h.s = next
	h.local = unixClient(next.Endpoint().UnixSocket)
	t.Cleanup(func() { _ = next.Shutdown(context.Background()) })
}

func TestBrowserSessionsSurviveRestartAndRevocation(t *testing.T) {
	for _, route := range []string{"all", "origin-sessions", "origin"} {
		t.Run(route, func(t *testing.T) {
			h := newHarness(t)
			token := h.pairBrowser()
			second := h.pairBrowser() // pairing a second tab must preserve the first
			restartSessionHarness(t, h)
			for _, tok := range []string{token, second} {
				r, b := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + tok, "Origin": h.ownOrigin()}})
				expect(t, r, b, http.StatusOK, "")
			}
			// A token cannot change its exact host binding after restart.
			r, b := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + token, "Origin": "http://localhost:" + h.browserPort()}})
			expect(t, r, b, http.StatusForbidden, CodeOriginRefused)
			switch route {
			case "all":
				h.revokeSessions("")
			case "origin-sessions":
				h.revokeSessions("?origin=" + url.QueryEscape(h.ownOrigin()))
			case "origin":
				r, b = h.localDo(req{method: http.MethodPost, path: "/api/v0/origins", body: `{"origin":"` + h.ownOrigin() + `"}`})
				expect(t, r, b, http.StatusOK, "")
				r, b = h.localDo(req{method: http.MethodDelete, path: "/api/v0/origins?origin=" + url.QueryEscape(h.ownOrigin())})
				expect(t, r, b, http.StatusOK, "")
			}
			restartSessionHarness(t, h)
			for _, tok := range []string{token, second} {
				r, b := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + tok}})
				expect(t, r, b, http.StatusUnauthorized, CodeUnauthenticated)
			}
		})
	}
}

func TestBrowserSessionSlidingExpiryAndAbsoluteCap(t *testing.T) {
	h := newHarness(t)
	token := h.pairBrowser()
	created := h.clock.Now()
	for i := 0; i < 8; i++ {
		h.clock.Advance(20 * 24 * time.Hour)
		r, b := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + token}})
		expect(t, r, b, http.StatusOK, "")
		restartSessionHarness(t, h)
	}
	h.clock.Advance(created.Add(sessionAbsoluteTTL).Sub(h.clock.Now()))
	r, b := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + token}})
	expect(t, r, b, http.StatusUnauthorized, CodeUnauthenticated)
	token = h.pairBrowser()
	h.clock.Advance(sessionIdleTTL)
	restartSessionHarness(t, h)
	r, b = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + token}})
	expect(t, r, b, http.StatusUnauthorized, CodeUnauthenticated)
}

func TestBrowserSessionStoreHashesModeAndCorruption(t *testing.T) {
	h := newHarness(t)
	token := h.pairBrowser()
	path := filepath.Join(Dir(h.state), sessionsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) || !strings.Contains(string(data), hashToken(token)) {
		t.Fatal("store must contain only the token hash")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	restartSessionHarness(t, h)
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("restart did not tighten copied file permissions")
	}
	if err := os.WriteFile(path, []byte(`{"sessions":{"bad":{}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	r, b := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + token}})
	expect(t, r, b, http.StatusUnauthorized, CodeUnauthenticated)
	opts := h.s.opts
	if err := h.s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := Start(opts)
	if s != nil {
		_ = s.Shutdown(context.Background())
		t.Fatal("corrupt store started a server")
	}
	if err == nil || !strings.Contains(err.Error(), "browser session store") || !strings.Contains(err.Error(), "pair browsers again") {
		t.Fatalf("corruption message: %v", err)
	}
}

func TestSessionStoreConcurrentWritersCannotResurrectRevocation(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	path := filepath.Join(t.TempDir(), sessionsFileName)
	stores := []*authStore{newAuthStore(clock.Now), newAuthStore(clock.Now)}
	for _, s := range stores {
		s.sessionPath = path
	}
	token, err := stores[0].newSession("http://localhost:7861")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := stores[1].lookupSession(token); !ok {
		t.Fatal("second process did not read credential")
	}
	var wg sync.WaitGroup
	for _, s := range stores {
		wg.Add(1)
		go func(a *authStore) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, err := a.newSession("http://localhost:7861"); err != nil {
					t.Error(err)
				}
			}
		}(s)
	}
	wg.Wait()
	all, err := readSessions(path)
	if err != nil || len(all) != 21 {
		t.Fatalf("concurrent writer lost sessions: count=%d err=%v", len(all), err)
	}
	if _, err := stores[0].revokeSessions(""); err != nil {
		t.Fatal(err)
	}
	if _, err := stores[1].newSession("http://localhost:7861"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := stores[1].lookupSession(token); ok {
		t.Fatal("stale writer resurrected revoked token")
	}
	data, _ := os.ReadFile(path)
	var stored sessionsFile
	if err := json.Unmarshal(data, &stored); err != nil || len(stored.Sessions) != 1 {
		t.Fatalf("atomic store: %s %v", data, err)
	}
}
