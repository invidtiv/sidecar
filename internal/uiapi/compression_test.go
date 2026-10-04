package uiapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/mobileproto"
)

func TestTerminalCompressionNegotiationAndDecompressedBound(t *testing.T) {
	for _, mode := range []websocket.CompressionMode{websocket.CompressionDisabled, websocket.CompressionContextTakeover} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			h := newHarness(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, response, err := websocket.Dial(ctx, "ws://sidecar.local"+terminalPath, &websocket.DialOptions{
				HTTPClient: unixClient(h.s.Endpoint().UnixSocket), CompressionMode: mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.CloseNow() }()
			negotiated := strings.Contains(response.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate")
			if negotiated != (mode != websocket.CompressionDisabled) {
				t.Fatalf("unexpected compression handshake: %v", response.Header)
			}
			// Two repeated messages exercise context takeover without changing bytes.
			message := `{"padding":"` + strings.Repeat("a", 4096) + `"}`
			for i := 0; i < 2; i++ {
				if err = conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
					t.Fatal(err)
				}
				if got := readText(t, conn); got != message {
					t.Fatal("compression changed decoded protocol bytes")
				}
			}
			// This compresses to a tiny payload but exceeds the decompressed bound.
			bomb := []byte(strings.Repeat("a", mobileproto.MaxLineBytes+1))
			if err = conn.Write(ctx, websocket.MessageText, bomb); err != nil {
				t.Fatal(err)
			}
			if code, _ := closeStatus(t, conn); code != websocket.StatusMessageTooBig {
				t.Fatalf("oversized decompressed message closed with %d", code)
			}
			select {
			case <-h.backend.eof:
			case <-ctx.Done():
				t.Fatal("oversized message retained its backend")
			}
		})
	}
}
