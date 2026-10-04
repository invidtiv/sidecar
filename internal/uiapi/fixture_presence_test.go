package uiapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// Each client consumes its real stream independently. Every frame updates the
// checkpoint echoed by the next operation; a correlated error does not advance
// the operation sequence. Readers have deadlines rather than readiness sleeps.
type fixturePresenceClient struct {
	t          *testing.T
	conn       *websocket.Conn
	name       string
	attachment string
	generation uint64
	operation  uint64
	request    int
	frame      mobileproto.Response
	holder     mobileproto.Holder
	resets     []mobileproto.Response
	reset      uint64
}

func (c *fixturePresenceClient) send(r mobileproto.Request) string {
	c.t.Helper()
	c.request++
	r.Version = mobileproto.Version
	r.RequestID = fmt.Sprintf("%s-%d", c.name, c.request)
	data, err := json.Marshal(r)
	if err != nil {
		c.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
	return r.RequestID
}

func (c *fixturePresenceClient) await(description string, match func(mobileproto.Response) bool) mobileproto.Response {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		kind, data, err := c.conn.Read(ctx)
		if err != nil {
			c.t.Fatalf("%s waiting for %s: %v (frame=%+v holder=%+v resets=%+v)", c.name, description, err, c.frame.Geometry, c.holder, c.resets)
		}
		if kind != websocket.MessageText {
			c.t.Fatal("terminal response is not a text message")
		}
		var r mobileproto.Response
		if err := json.Unmarshal(data, &r); err != nil {
			c.t.Fatal(err)
		}
		if r.Version != mobileproto.Version {
			c.t.Fatalf("unexpected envelope version %d", r.Version)
		}
		if c.attachment != "" && r.AttachmentHandle != "" && (r.AttachmentHandle != c.attachment || r.AttachmentGeneration != c.generation) {
			c.t.Fatalf("%s received foreign attachment: %+v", c.name, r)
		}
		switch r.Type {
		case mobileproto.ResponseReset:
			if r.ResetGeneration <= c.reset || !mobileproto.IsResetReason(r.Reason) {
				c.t.Fatalf("invalid reset: %+v", r)
			}
			c.reset = r.ResetGeneration
			c.resets = append(c.resets, r)
		case mobileproto.ResponseFrame:
			if r.Geometry == nil || r.Modes == nil || r.FrameKind != "full" || !r.ResetFree || !r.Coalesced || r.ResetGeneration != c.reset || r.OutputSequence <= c.frame.OutputSequence {
				c.t.Fatalf("invalid negotiated frame: %+v", r)
			}
			vt, err := base64.StdEncoding.Strict().DecodeString(r.RenderVTBase64)
			if err != nil || bytes.Contains(vt, []byte("\x1bc")) {
				c.t.Fatalf("invalid reset-free repaint: %v", err)
			}
			c.frame = r
		case mobileproto.ResponseHolder:
			if r.Holder == nil {
				c.t.Fatal("holder event omitted label")
			}
			c.holder = *r.Holder
		case mobileproto.ResponseError:
			if r.RequestID == "" {
				c.t.Fatalf("asynchronous terminal error: %+v", r.Error)
			}
		}
		if match(r) {
			return r
		}
	}
}

func (c *fixturePresenceClient) response(id string) mobileproto.Response {
	c.t.Helper()
	return c.await("response "+id, func(r mobileproto.Response) bool { return r.RequestID == id })
}

func (c *fixturePresenceClient) mutate(kind string, p *mobileproto.Presence, text string) mobileproto.Response {
	c.t.Helper()
	id := c.send(mobileproto.Request{Type: kind, AttachmentHandle: c.attachment, OperationSequence: c.operation + 1,
		LastResetGeneration: c.frame.ResetGeneration, LastOutputSequence: c.frame.OutputSequence, Presence: p,
		DataBase64: base64.StdEncoding.EncodeToString([]byte(text))})
	r := c.response(id)
	if r.Error != nil {
		c.t.Fatalf("%s %s refused: %+v", c.name, kind, r.Error)
	}
	c.operation++
	if r.OperationSequence != c.operation {
		c.t.Fatalf("operation acknowledgement lost sequence: %+v", r)
	}
	return r
}

func (c *fixturePresenceClient) observes(cols, rows int, holder mobileproto.Holder, from int, reason, text string) {
	c.t.Helper()
	c.await(fmt.Sprintf("%dx%d held by %s", cols, rows, holder.Label), func(mobileproto.Response) bool {
		if c.frame.Geometry == nil || c.frame.Geometry.Columns != cols || c.frame.Geometry.Rows != rows || c.holder != holder {
			return false
		}
		if reason != "" {
			found := false
			for _, reset := range c.resets[from:] {
				if reset.Reason == reason && reset.ResetGeneration == c.frame.ResetGeneration {
					found = true
				}
			}
			if !found {
				return false
			}
		}
		if text != "" {
			vt, _ := base64.StdEncoding.DecodeString(c.frame.RenderVTBase64)
			if !strings.Contains(string(vt), text) {
				return false
			}
		}
		return true
	})
}

func TestFixturePresenceSharesGeometryAcrossNegotiatedWebSockets(t *testing.T) {
	for _, takeover := range []string{mobileproto.RequestInput, mobileproto.RequestPaste} {
		t.Run(takeover, func(t *testing.T) {
			backend, err := LoadFixtures(fixtureDir())
			if err != nil {
				t.Fatal(err)
			}
			root, err := os.MkdirTemp("/tmp", "fixture-presence-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			server, err := Start(Options{StateDir: root, Port: 0, Backend: backend, FixtureStatus: &backend.Status})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			local := NewLocalClientForSocket(server.Endpoint())
			t.Cleanup(local.HTTPClient().CloseIdleConnections)
			expected := backend.targets["fixture-echo"]
			open := func(name string) *fixturePresenceClient {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				conn, _, err := websocket.Dial(ctx, strings.Replace(local.URL(terminalPath), "http://", "ws://", 1), &websocket.DialOptions{HTTPClient: local.HTTPClient()})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = conn.CloseNow() })
				c := &fixturePresenceClient{t: t, conn: conn, name: name}
				hello := c.response(c.send(mobileproto.Request{Type: mobileproto.RequestHello, Capabilities: &mobileproto.ClientCapabilities{Presence: true, ResetFreeFrames: true, CoalescedFrames: true, ServerPaste: true, HolderLabels: true}, Viewer: &mobileproto.Viewer{Kind: "browser", Label: name}}))
				if hello.Capabilities == nil || !hello.Capabilities.Presence || !hello.Capabilities.ServerPaste || !hello.Capabilities.HolderLabels {
					t.Fatal("fixture did not negotiate requested capabilities")
				}
				resolved := c.response(c.send(mobileproto.Request{Type: mobileproto.RequestResolve, Target: "fixture-echo", ExpectedTarget: &expected}))
				if resolved.Target == nil {
					t.Fatalf("resolve failed: %+v", resolved)
				}
				// Opening alone must not take geometry or claim a holder label.
				id := c.send(mobileproto.Request{Type: mobileproto.RequestOpen, TargetHandle: resolved.Target.Handle, AttachmentID: name})
				opened := c.await("opened", func(r mobileproto.Response) bool {
					if r.RequestID != id {
						return false
					}
					if r.Type != mobileproto.ResponseOpened || r.Control {
						t.Fatalf("opening claimed control: %+v", r)
					}
					c.attachment, c.generation, c.reset = r.AttachmentHandle, r.AttachmentGeneration, r.ResetGeneration
					return true
				})
				if opened.AttachmentHandle == "" || opened.ResetGeneration != 1 {
					t.Fatalf("invalid opening: %+v", opened)
				}
				c.await("initial full frame", func(r mobileproto.Response) bool { return r.Type == mobileproto.ResponseFrame })
				if c.frame.Geometry.Columns != 80 || c.frame.Geometry.Rows != 24 {
					t.Fatalf("unexpected initial geometry: %+v", c.frame.Geometry)
				}
				return c
			}
			a, b := open("Viewer A"), open("Viewer B")
			originalB := b.frame
			holderA, holderB := mobileproto.Holder{Kind: "browser", Label: a.name}, mobileproto.Holder{Kind: "browser", Label: b.name}
			fitA := &mobileproto.Presence{Focused: true, Visible: true, IdleMS: 0, Columns: 60, Rows: 20}
			claimed := a.mutate(mobileproto.RequestPresence, fitA, "")
			if !claimed.Control {
				t.Fatal("A presence did not claim geometry")
			}
			a.observes(60, 20, holderA, 0, mobileproto.ResetResize, "")
			b.observes(60, 20, holderA, 0, mobileproto.ResetGeometryChanged, "")
			// B must apply the cross-connection reset before mutation. Refusing this
			// stale checkpoint must leave operation sequence 1 available for presence.
			staleID := b.send(mobileproto.Request{Type: mobileproto.RequestPresence, AttachmentHandle: b.attachment, OperationSequence: 1, LastResetGeneration: originalB.ResetGeneration, LastOutputSequence: originalB.OutputSequence, Presence: &mobileproto.Presence{Focused: true, Visible: true, Columns: 40, Rows: 10}})
			stale := b.response(staleID)
			if stale.Error == nil || stale.Error.Code != mobileproto.ErrorOperationOrder {
				t.Fatalf("stale cross-viewer checkpoint accepted: %+v", stale)
			}
			fitB := &mobileproto.Presence{Focused: true, Visible: true, IdleMS: 0, Columns: 40, Rows: 10}
			fitted := b.mutate(mobileproto.RequestPresence, fitB, "")
			if fitted.Control {
				t.Fatal("fresh focused observer displaced active A without input")
			}
			aReset, bReset := len(a.resets), len(b.resets)
			text := "B_" + strings.ToUpper(takeover) + "_TAKEOVER"
			accepted := b.mutate(takeover, nil, text)
			if !accepted.Control {
				t.Fatal("input did not claim before delivery")
			}
			b.observes(40, 10, holderB, bReset, mobileproto.ResetResize, text)
			a.observes(40, 10, holderB, aReset, mobileproto.ResetGeometryChanged, text)
			// Both input and paste must remain usable with the same fitted viewport.
			follow := mobileproto.RequestPaste
			if takeover == mobileproto.RequestPaste {
				follow = mobileproto.RequestInput
			}
			b.mutate(follow, nil, "B_SECOND_DELIVERY")
			b.observes(40, 10, holderB, len(b.resets), "", "B_SECOND_DELIVERY")
			a.observes(40, 10, holderB, len(a.resets), "", "B_SECOND_DELIVERY")
			blur := *fitA
			blur.Focused = false
			if r := a.mutate(mobileproto.RequestPresence, &blur, ""); r.Control {
				t.Fatal("blurred A still controls geometry")
			}
			if r := b.mutate(mobileproto.RequestHeartbeat, nil, ""); !r.Control {
				t.Fatal("A blur released B's lease")
			}
			closed := b.response(b.send(mobileproto.Request{Type: mobileproto.RequestClose, AttachmentHandle: b.attachment}))
			if closed.Type != mobileproto.ResponseClosed {
				t.Fatalf("B close failed: %+v", closed)
			}
			a.await("B holder release", func(r mobileproto.Response) bool {
				return r.Type == mobileproto.ResponseHolder && a.holder == (mobileproto.Holder{})
			})
			afterClose := len(a.resets)
			if r := a.mutate(mobileproto.RequestPresence, fitA, ""); !r.Control {
				t.Fatal("A could not reacquire after B closed")
			}
			a.observes(60, 20, holderA, afterClose, mobileproto.ResetResize, "")
		})
	}
}
