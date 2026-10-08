package uiapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Admission on the direct-mode Tailnet listener. Identity is the device: the
// WireGuard source address of the connection, which only tailscaled can vouch
// for. Everything here refuses when that identity cannot be established.

const (
	tailnetRecheckInterval = time.Minute
	tailnetLogSummaryEvery = 10 * time.Minute
	tailnetRefusalLogEvery = time.Minute
	tailnetLogMapLimit     = 1024
	taggedDevicesLogin     = "tagged-devices"
)

// tailnetSourcePrefixes are the only source ranges Tailscale assigns: the
// CGNAT block for IPv4 and Tailscale's ULA block for IPv6. Anything else did
// not arrive over the tailnet (a LAN host, a local process on another
// interface) and is refused before whois.
var tailnetSourcePrefixes = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

type tailnetPeerKey struct{}
type tailnetConnKey struct{}

// tailnetAdmission is what admit leaves on the request context.
type tailnetAdmission struct {
	peer TailnetPeer
	addr netip.Addr
}

func tailnetPeerFrom(ctx context.Context) (TailnetPeer, bool) {
	admission, ok := ctx.Value(tailnetPeerKey{}).(tailnetAdmission)
	return admission.peer, ok
}

// withTailnetPeer carries the admitted device on the caller, so streams can
// be re-admitted later and approvals can record which device acted.
func withTailnetPeer(r *http.Request, c caller) caller {
	if admission, ok := r.Context().Value(tailnetPeerKey{}).(tailnetAdmission); ok {
		c.device, c.deviceID, c.peerAddr = admission.peer.Device, admission.peer.NodeID, admission.addr
	}
	return c
}

type tailnetRefusal struct {
	status  int
	code    string
	message string // what the peer is told
	detail  string // what the log says
}

const unidentifiedMessage = "This listener admits only identified tailnet devices; Tailscale could not identify this one."

// check decides whether the device at addr may use the listener now.
func (d *tailnetDirect) check(ctx context.Context, addr netip.Addr) (TailnetPeer, *tailnetRefusal) {
	inTailnet := false
	for _, prefix := range d.peerPrefixes {
		if prefix.Contains(addr) {
			inTailnet = true
			break
		}
	}
	if !inTailnet {
		return TailnetPeer{}, &tailnetRefusal{http.StatusUnauthorized, CodeUnauthenticated, unidentifiedMessage, "source address is not a tailnet address"}
	}
	trust := d.s.tailnetTrustSnapshot()
	if trust.own[addr] {
		return TailnetPeer{}, &tailnetRefusal{http.StatusForbidden, CodeLoginRefused,
			"Connections from this machine's own tailnet address are refused. On this machine use the Local socket (the sidecar CLI) or the Browser listener (`sidecar api open`).", "this node's own address"}
	}
	peer, err := d.whois.get(ctx, addr)
	if err != nil {
		return TailnetPeer{}, &tailnetRefusal{http.StatusUnauthorized, CodeUnauthenticated, unidentifiedMessage, fmt.Sprintf("whois failed: %v", err)}
	}
	refuse := func(message, detail string) (TailnetPeer, *tailnetRefusal) {
		return peer, &tailnetRefusal{http.StatusForbidden, CodeLoginRefused, message, detail}
	}
	login := strings.ToLower(peer.Login)
	switch {
	case (trust.selfID != "" && peer.NodeID == trust.selfID) || (peer.Name != "" && peer.Name == trust.host):
		return refuse("Connections from this machine are refused. On this machine use the Local socket (the sidecar CLI) or the Browser listener (`sidecar api open`).", "whois named this node")
	case len(peer.Tags) > 0 || login == taggedDevicesLogin:
		return refuse(fmt.Sprintf("Device %s is tagged and belongs to no person; only an allowed login's untagged devices may use this listener.", peer.Device),
			fmt.Sprintf("tagged device (%s)", strings.Join(peer.Tags, ", ")))
	case peer.SharedIn || trust.suffix == "" || !strings.HasSuffix(peer.Name, "."+trust.suffix):
		return refuse(fmt.Sprintf("Device %s is shared in from another tailnet; only this tailnet's own devices may use this listener.", peer.Device), "device shared in from another tailnet")
	case trust.ownerID != 0 && peer.UserID != trust.ownerID:
		return refuse(fmt.Sprintf("Tailnet login %q (device %s) is not this node's owner; add it to api.tailnetLogins in the Sidecar config.", peer.Login, peer.Device), "login is not the node owner")
	case !trust.logins[login]:
		return refuse(fmt.Sprintf("Tailnet login %q (device %s) is not allowed; add it to api.tailnetLogins in the Sidecar config.", peer.Login, peer.Device), "login not allowed")
	}
	return peer, nil
}

// admit establishes the connection's tailnet identity before anything else
// is read from the request, and refuses (closing the connection) when it
// cannot.
func (d *tailnetDirect) admit(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	addr, ok := connPeerAddr(r)
	if !ok {
		w.Header().Set("Connection", "close")
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, unidentifiedMessage)
		return r, false
	}
	peer, refusal := d.check(r.Context(), addr)
	if refusal != nil {
		d.logs.refused(addr, peer, refusal.detail)
		w.Header().Set("Connection", "close")
		writeError(w, refusal.status, refusal.code, refusal.message)
		return r, false
	}
	d.logs.admitted(addr, peer)
	return r.WithContext(context.WithValue(r.Context(), tailnetPeerKey{}, tailnetAdmission{peer: peer, addr: addr})), true
}

func connPeerAddr(r *http.Request) (netip.Addr, bool) {
	addr, ok := r.Context().Value(tailnetConnKey{}).(net.Addr)
	if !ok || addr == nil {
		return netip.Addr{}, false
	}
	addrPort, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.Addr{}, false
	}
	return addrPort.Addr().Unmap(), true
}

// recheckStreams re-admits every open stream on the listener: one whose
// device no longer qualifies (tagged, removed, login no longer allowed) or
// whose login changed is closed with 4401.
func (d *tailnetDirect) recheckStreams(ctx context.Context) {
	for _, client := range d.s.clients.tailnetStreams() {
		peer, refusal := d.check(ctx, client.peerAddr)
		if ctx.Err() != nil {
			return
		}
		if refusal != nil || !strings.EqualFold(peer.Login, client.info.Login) {
			detail := "login changed"
			if refusal != nil {
				detail = refusal.detail
			}
			d.s.opts.Logf("tailnet listener: closing %s stream %s from %s: %s", client.info.Kind, client.info.ID, client.peerAddr, detail)
			client.revoke()
		}
	}
}

// supervise re-admits open streams and writes the log summary.
func (d *tailnetDirect) supervise() {
	defer d.workers.Done()
	recheck := time.NewTicker(d.recheckInterval)
	defer recheck.Stop()
	summary := time.NewTicker(tailnetLogSummaryEvery)
	defer summary.Stop()
	for {
		select {
		case <-d.s.ctx.Done():
			return
		case <-recheck.C:
			d.recheckStreams(d.s.ctx)
		case <-summary.C:
			d.logs.summary()
		}
	}
}

// tailnetLogs keeps the service log useful without letting any reachable
// peer fill the disk: TLS handshake failures are counted and summarized,
// other server errors are rate limited, refusals are logged at most once a
// minute per address, and an admitted device is logged once per process.
type tailnetLogs struct {
	logf       func(string, ...any)
	now        func() time.Time
	handshakes atomic.Int64
	suppressed atomic.Int64

	mu        sync.Mutex
	lastOther time.Time
	refusals  map[netip.Addr]time.Time
	seen      map[string]bool
}

func newTailnetLogs(logf func(string, ...any)) *tailnetLogs {
	return &tailnetLogs{logf: logf, now: time.Now, refusals: map[netip.Addr]time.Time{}, seen: map[string]bool{}}
}

// Write receives http.Server's ErrorLog lines.
func (l *tailnetLogs) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if strings.Contains(line, "TLS handshake error") {
		l.handshakes.Add(1)
		return len(p), nil
	}
	l.mu.Lock()
	now := l.now()
	allowed := now.Sub(l.lastOther) >= 10*time.Second
	if allowed {
		l.lastOther = now
	}
	l.mu.Unlock()
	if allowed {
		l.logf("tailnet listener: %s", line)
	} else {
		l.suppressed.Add(1)
	}
	return len(p), nil
}

func (l *tailnetLogs) summary() {
	handshakes, suppressed := l.handshakes.Swap(0), l.suppressed.Swap(0)
	if handshakes > 0 || suppressed > 0 {
		l.logf("tailnet listener: in the last %s, %d TLS handshakes failed and %d further server errors were not logged", tailnetLogSummaryEvery, handshakes, suppressed)
	}
}

func (l *tailnetLogs) refused(addr netip.Addr, peer TailnetPeer, detail string) {
	l.mu.Lock()
	now := l.now()
	if last, ok := l.refusals[addr]; ok && now.Sub(last) < tailnetRefusalLogEvery {
		l.mu.Unlock()
		return
	}
	if len(l.refusals) >= tailnetLogMapLimit {
		l.refusals = map[netip.Addr]time.Time{}
	}
	l.refusals[addr] = now
	l.mu.Unlock()
	who := ""
	if peer.Login != "" {
		who = fmt.Sprintf(" (%s on %s)", peer.Login, peer.Device)
	}
	l.logf("tailnet listener: refused %s%s: %s", addr, who, detail)
}

func (l *tailnetLogs) admitted(addr netip.Addr, peer TailnetPeer) {
	key := strings.Join([]string{peer.Login, peer.NodeID, peer.Device, addr.String()}, "|")
	l.mu.Lock()
	if l.seen[key] || len(l.seen) >= tailnetLogMapLimit {
		l.mu.Unlock()
		return
	}
	l.seen[key] = true
	l.mu.Unlock()
	l.logf("tailnet listener: admitted %s on %s (%s)", peer.Login, peer.Device, addr)
}

// whoisCache answers whois by address for a short time, with one lookup in
// flight per address and a bound on concurrent lookups, so a burst of
// connections costs one subprocess. Each lookup runs on the server's context,
// not the request's, so a client hanging up cannot cancel it: the answer is
// always cached, and every waiter gets the real answer or gives up on its own
// context alone. Failures are cached briefly so a refused peer cannot turn
// reconnects into a subprocess storm.
type whoisCache struct {
	base    context.Context
	lookup  TailnetWhoisFunc
	now     func() time.Time
	ttl     time.Duration
	failTTL time.Duration
	sem     chan struct{}

	mu      sync.Mutex
	entries map[netip.Addr]*whoisEntry
}

type whoisEntry struct {
	ready   chan struct{}
	peer    TailnetPeer
	err     error
	expires time.Time
}

func newWhoisCache(base context.Context, lookup TailnetWhoisFunc, now func() time.Time) *whoisCache {
	return &whoisCache{base: base, lookup: lookup, now: now, ttl: tailnetWhoisTTL, failTTL: tailnetWhoisNegativeTTL,
		sem: make(chan struct{}, tailnetWhoisConcurrency), entries: map[netip.Addr]*whoisEntry{}}
}

func (c *whoisCache) get(ctx context.Context, addr netip.Addr) (TailnetPeer, error) {
	c.mu.Lock()
	entry := c.entries[addr]
	fresh := false
	if entry != nil {
		select {
		case <-entry.ready:
			fresh = c.now().Before(entry.expires)
		default:
			fresh = true // in flight
		}
	}
	if !fresh {
		entry = &whoisEntry{ready: make(chan struct{})}
		c.sweepLocked()
		c.entries[addr] = entry
		go c.fill(entry, addr)
	}
	c.mu.Unlock()
	select {
	case <-entry.ready:
		return entry.peer, entry.err
	case <-ctx.Done():
		return TailnetPeer{}, ctx.Err()
	}
}

func (c *whoisCache) fill(entry *whoisEntry, addr netip.Addr) {
	peer, err := c.resolve(addr)
	ttl := c.ttl
	if err != nil {
		ttl = c.failTTL
	}
	if c.base.Err() != nil {
		ttl = 0
	}
	entry.peer, entry.err, entry.expires = peer, err, c.now().Add(ttl)
	close(entry.ready)
}

func (c *whoisCache) resolve(addr netip.Addr) (TailnetPeer, error) {
	select {
	case c.sem <- struct{}{}:
	case <-c.base.Done():
		return TailnetPeer{}, c.base.Err()
	}
	defer func() { <-c.sem }()
	lookupCtx, cancel := context.WithTimeout(c.base, tailnetWhoisTimeout)
	defer cancel()
	peer, err := c.lookup(lookupCtx, addr)
	if err == nil && strings.TrimSpace(peer.Login) == "" {
		err = errors.New("whois named no login")
	}
	if err != nil {
		return TailnetPeer{}, err
	}
	peer.Tags = append([]string(nil), peer.Tags...)
	sort.Strings(peer.Tags)
	return peer, nil
}

func (c *whoisCache) sweepLocked() {
	if len(c.entries) < tailnetWhoisMaxEntries {
		return
	}
	now := c.now()
	for addr, entry := range c.entries {
		select {
		case <-entry.ready:
			if !now.Before(entry.expires) {
				delete(c.entries, addr)
			}
		default:
		}
	}
	if len(c.entries) < tailnetWhoisMaxEntries {
		return
	}
	for addr, entry := range c.entries {
		select {
		case <-entry.ready:
			delete(c.entries, addr)
		default:
		}
	}
}
