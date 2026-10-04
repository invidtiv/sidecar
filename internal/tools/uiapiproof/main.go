// Command uiapiproof drives one terminal round-trip over the UI API's
// /api/v0/terminal WebSocket for scripts/ui-api-proof.sh: hello, resolve,
// open, first frame, control, input, the echo in a frame, release, close.
// It prints one JSON summary with the latencies it observed.
//
// Run it by naming the package explicitly:
//
//	go run ./internal/tools/uiapiproof -url ws://127.0.0.1:7861/api/v0/terminal?ticket=T -origin http://proof.example -target SESSION
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/marcus/sidecar/internal/mobileproto"
)

type session struct {
	conn   *websocket.Conn
	number int
	holder *mobileproto.Holder
}

func (s *session) call(ctx context.Context, request mobileproto.Request) (mobileproto.Response, []mobileproto.Response, error) {
	s.number++
	request.Version = mobileproto.Version
	request.RequestID = "proof-" + strconv.Itoa(s.number)
	data, err := json.Marshal(request)
	if err != nil {
		return mobileproto.Response{}, nil, err
	}
	if err := s.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return mobileproto.Response{}, nil, err
	}
	var async []mobileproto.Response
	for {
		response, err := s.read(ctx)
		if err != nil {
			return mobileproto.Response{}, async, err
		}
		if response.RequestID == request.RequestID {
			if response.Type == mobileproto.ResponseError {
				return response, async, fmt.Errorf("%s refused: %s: %s", request.Type, response.Error.Code, response.Error.Message)
			}
			return response, async, nil
		}
		async = append(async, response)
	}
}

func (s *session) read(ctx context.Context) (mobileproto.Response, error) {
	kind, data, err := s.conn.Read(ctx)
	if err != nil {
		return mobileproto.Response{}, err
	}
	if kind != websocket.MessageText {
		return mobileproto.Response{}, errors.New("server sent a binary message")
	}
	var response mobileproto.Response
	err = json.Unmarshal(data, &response)
	if response.Holder != nil {
		s.holder = response.Holder
	}
	return response, err
}

func (s *session) frame(ctx context.Context, match func(mobileproto.Response) bool) (mobileproto.Response, error) {
	for {
		response, err := s.read(ctx)
		if err != nil {
			return response, err
		}
		if response.Type == mobileproto.ResponseFrame && (match == nil || match(response)) {
			return response, nil
		}
	}
}

func run() error {
	v1 := flag.Bool("v1", false, "exercise negotiated presence, frame flags, input takeover and paste")
	target := flag.String("target", "", "terminal target (a managed shell session)")
	wsURL := flag.String("url", "", "WebSocket URL of /api/v0/terminal")
	origin := flag.String("origin", "", "Origin header to send")
	bearer := flag.String("bearer", "", "send Authorization: Bearer with this token on the upgrade")
	socket := flag.String("socket", "", "dial this Unix socket instead of the URL's host")
	literalEcho := flag.Bool("literal-echo", false, "send literal marker bytes for the deterministic fixture echo terminal")
	marker := flag.String("marker", "UIAPI_PROOF", "text the shell must echo back")
	timeout := flag.Duration("timeout", 30*time.Second, "overall deadline")
	flag.Parse()
	if *target == "" || *wsURL == "" {
		return errors.New("-target and -url are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	options := &websocket.DialOptions{HTTPHeader: http.Header{}}
	if *v1 {
		options.CompressionMode = websocket.CompressionContextTakeover
	}
	if *bearer != "" {
		options.HTTPHeader.Set("Authorization", "Bearer "+*bearer)
	}
	if *origin != "" {
		options.HTTPHeader.Set("Origin", *origin)
	}
	if *socket != "" {
		path := *socket
		options.HTTPClient = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}}}
	}
	started := time.Now()
	conn, _, err := websocket.Dial(ctx, *wsURL, options)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	conn.SetReadLimit(mobileproto.MaxLineBytes)
	defer func() { _ = conn.CloseNow() }()
	s := &session{conn: conn}
	if *v1 {
		return runV1(ctx, s, *target, *marker, *literalEcho)
	}

	if _, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestHello}); err != nil {
		return err
	}
	resolved, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestResolve, Target: *target})
	if err != nil {
		return err
	}
	opened, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestOpen, TargetHandle: resolved.Target.Handle, AttachmentID: "ui-api-proof"})
	if err != nil {
		return err
	}
	first, err := s.frame(ctx, nil)
	if err != nil {
		return err
	}
	openMS := time.Since(started).Seconds() * 1000
	attachment := opened.AttachmentHandle
	reset, output := first.ResetGeneration, first.OutputSequence
	control, async, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestControl, AttachmentHandle: attachment, OperationSequence: 1,
		LastResetGeneration: reset, LastOutputSequence: output, Columns: first.Geometry.Columns, Rows: first.Geometry.Rows})
	if err != nil {
		return err
	}
	for _, f := range async {
		if f.Type == mobileproto.ResponseFrame && f.ResetGeneration == control.ResetGeneration {
			reset, output = f.ResetGeneration, f.OutputSequence
		}
	}
	if reset != control.ResetGeneration {
		latest, err := s.frame(ctx, func(r mobileproto.Response) bool { return r.ResetGeneration == control.ResetGeneration })
		if err != nil {
			return err
		}
		reset, output = latest.ResetGeneration, latest.OutputSequence
	}
	// The shell computes the marker, so it appears only in output, never in
	// the echoed command line.
	half := len(*marker) / 2
	command := fmt.Sprintf("printf '%%s%%s\\n' '%s' '%s'\r", (*marker)[:half], (*marker)[half:])
	if *literalEcho {
		command = *marker
	}
	inputStarted := time.Now()
	if _, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestInput, AttachmentHandle: attachment, OperationSequence: 2,
		LastResetGeneration: reset, LastOutputSequence: output, DataBase64: base64.StdEncoding.EncodeToString([]byte(command))}); err != nil {
		return err
	}
	echoed, err := s.frame(ctx, func(r mobileproto.Response) bool {
		vt, _ := base64.StdEncoding.DecodeString(r.RenderVTBase64)
		return bytes.Contains(vt, []byte(*marker))
	})
	if err != nil {
		return err
	}
	echoMS := time.Since(inputStarted).Seconds() * 1000
	if _, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestRelease, AttachmentHandle: attachment, OperationSequence: 3,
		LastResetGeneration: echoed.ResetGeneration, LastOutputSequence: echoed.OutputSequence}); err != nil {
		return err
	}
	if _, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestClose, AttachmentHandle: attachment}); err != nil {
		return err
	}
	if err := conn.Close(websocket.StatusNormalClosure, "proof complete"); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	summary := map[string]any{
		"target":     resolved.Target.Session,
		"pane":       resolved.Target.Pane,
		"geometry":   first.Geometry,
		"echo_found": true,
		"latency_ms": map[string]float64{"dial_hello_resolve_open_frame": round(openMS), "input_to_echo_frame": round(echoMS)},
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}

func round(v float64) float64 { return float64(int64(v*1000)) / 1000 }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "uiapiproof:", err)
		os.Exit(1)
	}
}
