package uiapi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/marcus/sidecar/internal/mobileproto"
)

// Close codes for /api/v0/terminal.
const (
	CloseProtocolViolation websocket.StatusCode = 4400
	CloseUnauthenticated   websocket.StatusCode = 4401
	CloseOriginRefused     websocket.StatusCode = 4403
	CloseShuttingDown      websocket.StatusCode = 4409
	// CloseTooManyTerminals is the WebSocket form of too_many_outstanding.
	CloseTooManyTerminals websocket.StatusCode = 4429
)

const (
	defaultKeepaliveInterval = 30 * time.Second
	defaultKeepaliveTimeout  = 15 * time.Second
)

// keepalive pings the peer on an interval and drops a connection whose pong
// misses the deadline. A half-open socket (a laptop that slept, a proxy that
// lost the peer) then ends as EOF and releases its lease, instead of holding
// a terminal until the next write happens to fail.
//
// A pong is only seen while pumpRequests is reading the socket. When the
// pump is blocked handing a request to a backend that has stopped reading,
// a missed pong is the server's stall, not the peer's, so it is excused. A
// peer may disappear after causing that stall, so the exemption is bounded
// from the last successful pong (or connection start).
func (s *Server) keepalive(ctx context.Context, conn *websocket.Conn, inbound *inboundGate) {
	interval, timeout := s.opts.KeepaliveInterval, s.opts.KeepaliveTimeout
	if interval <= 0 {
		interval = defaultKeepaliveInterval
	}
	if timeout <= 0 {
		timeout = defaultKeepaliveTimeout
	}
	stallTimeout := s.opts.KeepaliveStallTimeout
	if stallTimeout <= 0 {
		stallTimeout = time.Minute
	}
	responsive := inbound.now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if inbound.now()-responsive >= stallTimeout {
				_ = conn.CloseNow()
				return
			}
			if inbound.blocked() {
				// Nothing is reading the socket, so no pong could be seen.
				continue
			}
			sent := inbound.now()
			// Not derived from ctx: canceling a write context closes the socket,
			// which would pre-empt the 4409 close on shutdown.
			pingCtx, cancel := context.WithTimeout(context.Background(), timeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if errors.Is(err, context.DeadlineExceeded) && inbound.blockedSince(sent) {
					continue
				}
				_ = conn.CloseNow()
				return
			}
			responsive = inbound.now()
		}
	}
}

// inboundGate records when pumpRequests is blocked writing a request to the
// backend. The backend handles requests one at a time, so a slow one can
// stop it reading for longer than the pong deadline.
type inboundGate struct {
	origin   time.Time
	writing  atomic.Bool
	finished atomic.Int64 // when the last write finished, as an offset from origin
}

func newInboundGate() *inboundGate { return &inboundGate{origin: time.Now()} }

// now is a monotonic offset from the gate's creation.
func (g *inboundGate) now() time.Duration { return time.Since(g.origin) }

func (g *inboundGate) begin() { g.writing.Store(true) }

func (g *inboundGate) end() {
	g.finished.Store(int64(g.now()))
	g.writing.Store(false)
}

func (g *inboundGate) blocked() bool { return g.writing.Load() }

// blockedSince reports whether the pump was blocked at any point since t: it
// is blocked now, or a write that held it finished after t, so a pong may
// have waited unread behind it.
func (g *inboundGate) blockedSince(t time.Duration) bool {
	return g.writing.Load() || time.Duration(g.finished.Load()) >= t
}

const (
	terminalWriteTimeout = 15 * time.Second
	terminalDrainTimeout = 2 * time.Second
	terminalStopTimeout  = 10 * time.Second
	maxCloseReasonBytes  = 123
	revokedSessionReason = "This credential was revoked; pair again with sidecar api open or sidecar api pair --origin URL."
)

// serveTerminal upgrades first and refuses with a close code, because a
// browser cannot read the status of a failed handshake and the code is the
// only way to tell it what to do.
func (h *listenerHandler) serveTerminal(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeError(w, http.StatusUpgradeRequired, CodeUpgradeRequired, "Open /api/v0/terminal as a WebSocket.")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	c, code, reason := h.authorizeTerminal(r)
	if code != 0 {
		_ = conn.Close(code, reason)
		return
	}
	if !h.s.beginStream() {
		_ = conn.Close(CloseShuttingDown, "The Sidecar API server is shutting down; reconnect when it is back.")
		return
	}
	defer h.s.streams.Done()
	h.s.credentialMu.Lock()
	if !h.s.callerLive(c) {
		h.s.credentialMu.Unlock()
		_ = conn.Close(CloseUnauthenticated, revokedSessionReason)
		return
	}
	client, ok := h.s.clients.add("terminal", c)
	h.s.credentialMu.Unlock()
	if !ok {
		_ = conn.Close(CloseTooManyTerminals, closeReason(fmt.Sprintf("This client already has %d open terminals; close one first.", maxTerminalsPerClient)))
		return
	}
	defer h.s.clients.remove(client)
	h.s.runTerminal(conn, client)
}

func (h *listenerHandler) authorizeTerminal(r *http.Request) (caller, websocket.StatusCode, string) {
	c := caller{listener: h.kind}
	if h.kind == ListenerLocal {
		c.auth, c.client = "local", "local"
		return c, 0, ""
	}
	origin := r.Header.Get("Origin")
	// A non-browser client (Node, a native app) sends no Origin; it may still
	// connect with a bearer token, which is not ambient. A ticket alone is
	// origin-bound, so it still needs its Origin.
	bearerOnly := origin == "" && h.bearerWithoutOrigin(r) && r.URL.Query().Get("ticket") == ""
	if !bearerOnly && !h.originAllowed(origin) {
		return c, CloseOriginRefused, "This origin may not open terminals; pair it with sidecar api pair --origin URL."
	}
	c.origin = origin
	if h.kind == ListenerTailnet {
		login, code, message := h.tailnetLogin(r)
		if code != "" {
			return c, CloseUnauthenticated, message
		}
		c.auth, c.login, c.client = "tailnet", login, "tailnet:"+login
		return c, 0, ""
	}
	if ticket := r.URL.Query().Get("ticket"); ticket != "" {
		g, ok := h.s.auth.redeemTicket(ticket)
		if !ok || g.listener != h.kind {
			return c, CloseUnauthenticated, "This ticket is invalid, expired or already used; request a new one from POST /api/v0/ws-tickets."
		}
		if g.origin != origin {
			return c, CloseOriginRefused, "This ticket was issued to another origin."
		}
		c.auth, c.client, c.credential = "ticket", g.client, g.credential
		return c, 0, ""
	}
	// Non-browser clients may send their bearer token on the upgrade. The
	// Origin they send must be the one the token is bound to.
	if token, present := bearerToken(r); present {
		resolved, result := h.s.resolveBearer(token, origin)
		switch result {
		case bearerWrongOrigin:
			return c, CloseOriginRefused, "This bearer token was issued to another origin."
		case bearerUnknown:
			return c, CloseUnauthenticated, "This bearer token is not valid; pair again."
		}
		resolved.listener = h.kind
		return resolved, 0, ""
	}
	return c, CloseUnauthenticated, "Connect with a ticket from POST /api/v0/ws-tickets; pair this browser first with sidecar api open."
}

type inboundResult struct {
	violation string
	err       error
}

// runTerminal bridges one WebSocket to one protocol stream: each text message
// is one JSONL request line and each response line is one text message. The
// socket closing is the stream's EOF, so the backend releases its lease
// exactly as `sidecar mobile serve --stdio` does on stdin EOF.
func (s *Server) runTerminal(conn *websocket.Conn, client *trackedClient) {
	conn.SetReadLimit(mobileproto.MaxLineBytes)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	inbound := newInboundGate()
	keepaliveDone := make(chan struct{})
	go func() {
		s.keepalive(ctx, conn, inbound)
		close(keepaliveDone)
	}()
	requests, requestWriter := io.Pipe()
	responseReader, responses := io.Pipe()

	backendDone := make(chan error, 1)
	go func() {
		err := s.opts.Backend.ServeTerminal(ctx, requests, responses)
		_ = responses.Close()
		_ = requests.Close()
		backendDone <- err
	}()
	writerDone := make(chan error, 1)
	go func() { writerDone <- pumpResponses(conn, responseReader, client) }()
	readerDone := make(chan inboundResult, 1)
	go func() { readerDone <- pumpRequests(conn, requestWriter, inbound) }()

	waitBackend := func() (error, bool) {
		timer := time.NewTimer(terminalStopTimeout)
		defer timer.Stop()
		select {
		case err := <-backendDone:
			return err, true
		case <-timer.C:
			cancel()
			return nil, false
		}
	}

	select {
	case <-keepaliveDone:
		// The socket can close while the reader is stuck writing to the
		// backend. Break that write and cancel the backend directly.
		_ = requestWriter.Close()
		cancel()
		if _, ok := waitBackend(); !ok {
			<-backendDone
		}
	case result := <-readerDone:
		// End of stream: EOF on the request pipe, exactly like stdin EOF.
		_ = requestWriter.Close()
		if result.violation != "" {
			_ = conn.Close(CloseProtocolViolation, closeReason(result.violation))
		}
		if _, ok := waitBackend(); !ok {
			<-backendDone
		}
		_ = conn.CloseNow()
	case <-client.revoked:
		_ = requestWriter.Close()
		_ = conn.Close(CloseUnauthenticated, revokedSessionReason)
		if _, ok := waitBackend(); !ok {
			<-backendDone
		}
		_ = conn.CloseNow()
	case err := <-backendDone:
		drain := time.NewTimer(terminalDrainTimeout)
		select {
		case <-writerDone:
		case <-drain.C:
		}
		drain.Stop()
		code, reason := terminalCloseCode(err, client.finalErrorCode(), s.ctx.Err() != nil)
		_ = conn.Close(code, closeReason(reason))
		_ = requestWriter.Close()
	case <-s.ctx.Done():
		cancel()
		_ = requestWriter.Close()
		_ = conn.Close(CloseShuttingDown, "The Sidecar API server is shutting down; reconnect when it is back.")
		if _, ok := waitBackend(); !ok {
			<-backendDone
		}
	}
	cancel()
	_ = responseReader.CloseWithError(io.ErrClosedPipe)
}

func terminalCloseCode(err error, lastErrorCode string, shuttingDown bool) (websocket.StatusCode, string) {
	switch {
	case shuttingDown:
		return CloseShuttingDown, "The Sidecar API server is shutting down; reconnect when it is back."
	case lastErrorCode == mobileproto.ErrorInvalidRequest || lastErrorCode == mobileproto.ErrorProtocolMismatch || lastErrorCode == mobileproto.ErrorHandshake:
		return CloseProtocolViolation, "The terminal stream ended on a protocol violation; see the last error message."
	case err == nil || errors.Is(err, io.EOF):
		return websocket.StatusNormalClosure, "The terminal stream ended."
	default:
		return websocket.StatusInternalError, err.Error()
	}
}

func closeReason(reason string) string {
	if len(reason) <= maxCloseReasonBytes {
		return reason
	}
	cut := maxCloseReasonBytes
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut]
}

// pumpRequests forwards each text message as one request line. It reads
// without a context: canceling a read context would close the socket before
// the close code could be sent.
func pumpRequests(conn *websocket.Conn, requests *io.PipeWriter, inbound *inboundGate) inboundResult {
	for {
		kind, data, err := conn.Read(context.Background())
		if err != nil {
			return inboundResult{err: err}
		}
		if kind != websocket.MessageText {
			return inboundResult{violation: "Send protocol envelopes as text messages; binary messages are not accepted."}
		}
		if len(data) == 0 || bytes.ContainsAny(data, "\r\n") {
			return inboundResult{violation: "Send exactly one JSON envelope per text message, without newlines."}
		}
		inbound.begin()
		_, err = requests.Write(append(data, '\n'))
		inbound.end()
		if err != nil {
			return inboundResult{err: err}
		}
	}
}

// pumpResponses sends each response line as one text message without its
// newline. A peer that stops reading fails the write, which fails the
// backend's next write and ends its stream through its own bounded queue.
func pumpResponses(conn *websocket.Conn, responses *io.PipeReader, client *trackedClient) error {
	scanner := bufio.NewScanner(responses)
	scanner.Buffer(make([]byte, 64<<10), mobileproto.MaxLineBytes+1)
	for scanner.Scan() {
		line := scanner.Bytes()
		client.observe(line)
		ctx, cancel := context.WithTimeout(context.Background(), terminalWriteTimeout)
		err := conn.Write(ctx, websocket.MessageText, line)
		cancel()
		if err != nil {
			_ = responses.CloseWithError(err)
			return err
		}
	}
	err := scanner.Err()
	if err != nil {
		_ = responses.CloseWithError(err)
	}
	return err
}
