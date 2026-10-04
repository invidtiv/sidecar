package uiapi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
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
)

const (
	terminalWriteTimeout = 15 * time.Second
	terminalDrainTimeout = 2 * time.Second
	terminalStopTimeout  = 10 * time.Second
	maxCloseReasonBytes  = 123
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
	h.s.runTerminal(conn, c)
}

func (h *listenerHandler) authorizeTerminal(r *http.Request) (caller, websocket.StatusCode, string) {
	c := caller{listener: h.kind}
	if h.kind == ListenerLocal {
		c.auth = "local"
		return c, 0, ""
	}
	origin := r.Header.Get("Origin")
	if !h.originAllowed(origin) {
		return c, CloseOriginRefused, "This origin may not open terminals; pair it with sidecar api pair --origin URL."
	}
	c.origin = origin
	if h.kind == ListenerTailnet {
		login, code, message := h.tailnetLogin(r)
		if code != "" {
			return c, CloseUnauthenticated, message
		}
		c.auth, c.login = "tailnet", login
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
		c.auth = "ticket"
		return c, 0, ""
	}
	if token, present := bearerToken(r); present {
		record, ok := h.s.origins.lookupToken(token)
		if !ok {
			return c, CloseUnauthenticated, "This bearer token is not paired; pair the origin again."
		}
		if record.Origin != origin {
			return c, CloseOriginRefused, "This bearer token was issued to another origin."
		}
		c.auth = "bearer"
		return c, 0, ""
	}
	if h.ownOrigin(origin) {
		if cookie, err := r.Cookie(h.cookieName()); err == nil && h.s.auth.validSession(cookie.Value) {
			c.auth = "cookie"
			return c, 0, ""
		}
	}
	return c, CloseUnauthenticated, "Pair this browser with sidecar api open, or connect with a ticket from POST /api/v0/ws-tickets."
}

type inboundResult struct {
	violation string
	err       error
}

// runTerminal bridges one WebSocket to one protocol stream: each text message
// is one JSONL request line and each response line is one text message. The
// socket closing is the stream's EOF, so the backend releases its lease
// exactly as `sidecar mobile serve --stdio` does on stdin EOF.
func (s *Server) runTerminal(conn *websocket.Conn, c caller) {
	conn.SetReadLimit(mobileproto.MaxLineBytes)
	client := s.clients.add("terminal", c)
	defer s.clients.remove(client)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
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
	go func() { readerDone <- pumpRequests(conn, requestWriter) }()

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
func pumpRequests(conn *websocket.Conn, requests *io.PipeWriter) inboundResult {
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
		if _, err := requests.Write(append(data, '\n')); err != nil {
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
