package uiapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// When the node can no longer say whom to trust, closing the listeners is not
// enough: a connection already accepted keeps sending requests and an open
// stream keeps running. Both stop at once.
func TestDirectTailnetWithdrawnTrustAdmitsNobody(t *testing.T) {
	for _, tc := range []struct {
		name      string
		breakNode func(f *fakeTailnet)
	}{
		{"tagged node", func(f *fakeTailnet) { f.node.Tags = []string{"tag:server"} }},
		{"no owner", func(f *fakeTailnet) { f.node.OwnerLogin, f.node.OwnerID = "", 0 }},
		{"invalid name", func(f *fakeTailnet) { f.node.Host = "Not_A_DNS_Name" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDirectHarnessWith(t, func(o *TailnetOptions) { o.recheckInterval = time.Hour })
			client := h.client()
			kept, data := h.do(client, directOrigin(), req{path: "/api/v0/hello"})
			expect(t, kept, data, http.StatusOK, "")
			conn := h.openTerminal(t)

			h.fake.set(tc.breakNode)
			code, reason := closeStatus(t, conn)
			if code != CloseUnauthenticated || reason != tailnetWithdrawnReason {
				t.Fatalf("stream close = %d %q", code, reason)
			}
			waitFor(t, "the listener to stop", func() bool { return h.s.TailnetStatus().State == TailnetStateWaiting })
			// The same keep-alive connection, accepted before the change.
			response, body := h.do(client, directOrigin(), req{path: "/api/v0/hello"})
			expect(t, response, body, http.StatusForbidden, CodeLoginRefused)
		})
	}
}

// One failed whois during the periodic re-check is Tailscale being slow, not
// the device losing access: the stream closes only after two in a row, and a
// success in between starts the count again.
func TestDirectTailnetRecheckToleratesOneFailedLookup(t *testing.T) {
	h := newDirectHarnessWith(t, func(o *TailnetOptions) { o.recheckInterval = time.Hour })
	conn := h.openTerminal(t)
	stillOpen := func(when string) {
		t.Helper()
		writeText(t, conn, `{"open":true}`)
		if got := readText(t, conn); got != `{"open":true}` {
			t.Fatalf("%s: echo = %q", when, got)
		}
	}
	recheck := func(fail bool) {
		h.fake.set(func(f *fakeTailnet) {
			f.whoisErr = nil
			if fail {
				f.whoisErr = errors.New("tailscaled busy")
			}
		})
		h.clock.Advance(tailnetWhoisTTL + time.Second) // past both cache lifetimes
		h.s.direct.recheckStreams(context.Background())
	}
	recheck(true)
	stillOpen("after one failed lookup")
	recheck(false)
	stillOpen("after a success")
	recheck(true)
	stillOpen("after one more failed lookup")
	recheck(true)
	code, reason := closeStatus(t, conn)
	if code != CloseUnauthenticated || reason != tailnetUnidentifiedReason {
		t.Fatalf("close after two failed lookups = %d %q", code, reason)
	}
}

// A definite refusal still closes at once, and says the device is no longer
// admitted rather than telling a tailnet login to pair again.
func TestDirectTailnetRecheckClosesARefusedDeviceWithAnAccurateReason(t *testing.T) {
	h := newDirectHarnessWith(t, func(o *TailnetOptions) { o.recheckInterval = time.Hour })
	conn := h.openTerminal(t)
	h.fake.set(func(f *fakeTailnet) {
		peer := ownerPeer()
		peer.Tags = []string{"tag:server"}
		f.peers[loopbackPeer] = peer
	})
	h.clock.Advance(tailnetWhoisTTL + time.Second)
	h.s.direct.recheckStreams(context.Background())
	if code, reason := closeStatus(t, conn); code != CloseUnauthenticated || reason != tailnetNotAdmittedReason {
		t.Fatalf("close = %d %q", code, reason)
	}
}

func TestDirectTailnetLoginsChangedReason(t *testing.T) {
	h := newDirectHarnessWith(t, func(o *TailnetOptions) { o.recheckInterval = time.Hour })
	conn := h.openTerminal(t)
	h.fake.set(func(f *fakeTailnet) { f.node.OwnerID, f.node.OwnerLogin = 43, "new-owner@example.com" })
	if code, reason := closeStatus(t, conn); code != CloseUnauthenticated || reason != tailnetLoginsChangedReason {
		t.Fatalf("close = %d %q", code, reason)
	}
}

func TestTailnetCloseReasonsFitAWebSocketClose(t *testing.T) {
	for _, reason := range []string{tailnetNotAdmittedReason, tailnetUnidentifiedReason, tailnetLoginChangedReason, tailnetLoginsChangedReason, tailnetWithdrawnReason} {
		if len(reason) > maxCloseReasonBytes || strings.Contains(reason, "pair again") {
			t.Fatalf("reason %q (%d bytes)", reason, len(reason))
		}
	}
}

// Requests from outside the tailnet are counted for the summary, not logged
// one address at a time.
func TestDirectTailnetCountsNonTailnetRefusals(t *testing.T) {
	h := newDirectHarnessWith(t, func(o *TailnetOptions) { o.peerPrefixes = nil })
	for i := 0; i < 3; i++ {
		response, data := h.directDo(req{path: "/api/v0/hello"})
		expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	}
	logs := h.s.direct.logs
	if got := logs.foreign.Load(); got != 3 {
		t.Fatalf("counted %d foreign refusals", got)
	}
	logs.mu.Lock()
	logged := len(logs.refusals)
	logs.mu.Unlock()
	if logged != 0 {
		t.Fatalf("%d foreign addresses logged individually", logged)
	}

	recorder := &logRecorder{}
	summary := newTailnetLogs(recorder.logf)
	summary.foreign.Add(7)
	summary.summary()
	if !strings.Contains(recorder.all(), "refused 7 requests from addresses outside the tailnet") {
		t.Fatalf("summary = %q", recorder.all())
	}
	summary.summary()
	if recorder.count() != 1 {
		t.Fatal("an empty summary was logged")
	}
}

// Writing the single .pem retires the separate .crt and .key files an
// earlier build kept.
func TestDirectTailnetRemovesLegacyCertificateFiles(t *testing.T) {
	h := newDirectHarness(t)
	dir := filepath.Join(Dir(h.state), "tls", "tailnet")
	legacy := []string{testTailnetHost + ".crt", testTailnetHost + ".key", "old.example.ts.net.key"}
	for _, name := range legacy {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	calls := h.fake.count(func(f *fakeTailnet) int { return f.certCalls })
	h.clock.Advance(tailnetCertInterval + time.Minute)
	waitFor(t, "a certificate refresh", func() bool { return h.fake.count(func(f *fakeTailnet) int { return f.certCalls }) > calls })
	waitFor(t, "legacy files to go", func() bool {
		for _, name := range legacy {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				return false
			}
		}
		return true
	})
	if _, err := os.Stat(filepath.Join(dir, testTailnetHost+".pem")); err != nil {
		t.Fatalf("the current pair went too: %v", err)
	}
}
