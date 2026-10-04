// threeviewer is the native SSH-stdio terminal peer used by three-viewer-proof.sh.
// Its JSONL control pipe is test plumbing; terminal traffic uses mobileproto.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

type command struct {
	Type     string                `json:"type"`
	Text     string                `json:"text"`
	Presence *mobileproto.Presence `json:"presence"`
}

type peer struct {
	in            io.Writer
	responses     chan mobileproto.Response
	errors        chan error
	number        int
	attachment    string
	operation     uint64
	reset, output uint64
	ready         bool
	geometry      *mobileproto.Geometry
	holder        *mobileproto.Holder
}

func (p *peer) apply(r mobileproto.Response) {
	if r.Type == mobileproto.ResponseReset {
		p.ready = false
	}
	if r.Type == mobileproto.ResponseFrame {
		p.reset, p.output, p.geometry, p.ready = r.ResetGeneration, r.OutputSequence, r.Geometry, true
	}
	if r.Holder != nil {
		p.holder = r.Holder
	}
}

func (p *peer) read(ctx context.Context) (mobileproto.Response, error) {
	select {
	case r := <-p.responses:
		p.apply(r)
		return r, nil
	case err := <-p.errors:
		return mobileproto.Response{}, err
	case <-ctx.Done():
		return mobileproto.Response{}, ctx.Err()
	}
}

func (p *peer) call(r mobileproto.Request) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// Apply all arrived frames before choosing the operation checkpoint.
	for {
		select {
		case next := <-p.responses:
			p.apply(next)
		default:
			goto drained
		}
	}
drained:
	if p.attachment != "" && r.Type != mobileproto.RequestClose {
		for !p.ready {
			if _, err := p.read(ctx); err != nil {
				return err
			}
		}
		r.AttachmentHandle = p.attachment
		r.OperationSequence, r.LastResetGeneration, r.LastOutputSequence = p.operation+1, p.reset, p.output
	}
	p.number++
	r.Version, r.RequestID = mobileproto.Version, fmt.Sprintf("ios-%d", p.number)
	if err := json.NewEncoder(p.in).Encode(r); err != nil {
		return err
	}
	for {
		next, err := p.read(ctx)
		if err != nil {
			return err
		}
		if next.RequestID != r.RequestID {
			continue
		}
		if next.Error != nil {
			return fmt.Errorf("%s: %s: %s", r.Type, next.Error.Code, next.Error.Message)
		}
		if next.Target != nil {
			p.attachment = next.Target.Handle
		}
		if r.Type == mobileproto.RequestResolve {
			return nil
		}
		if next.AttachmentHandle != "" {
			p.attachment = next.AttachmentHandle
		}
		if next.OperationSequence != 0 {
			p.operation = next.OperationSequence
		}
		if next.ResetGeneration != 0 && next.ResetGeneration != p.reset {
			p.ready = false
		}
		return nil
	}
}

func run() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: threeviewer SIDECAR CONFIG TARGET")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[1], "-config", os.Args[2], "mobile", "serve", "--stdio")
	child.Stderr = os.Stderr
	in, err := child.StdinPipe()
	if err != nil {
		return err
	}
	out, err := child.StdoutPipe()
	if err != nil {
		return err
	}
	if err = child.Start(); err != nil {
		return err
	}
	defer func() { _ = in.Close(); cancel(); _ = child.Wait() }()
	p := &peer{in: in, responses: make(chan mobileproto.Response, 128), errors: make(chan error, 1)}
	go func() {
		s := bufio.NewScanner(out)
		s.Buffer(make([]byte, 4096), mobileproto.MaxLineBytes)
		for s.Scan() {
			var r mobileproto.Response
			if e := json.Unmarshal(s.Bytes(), &r); e != nil {
				p.errors <- e
				return
			}
			select {
			case p.responses <- r:
			case <-ctx.Done():
				return
			}
		}
		e := s.Err()
		if e == nil {
			e = io.EOF
		}
		p.errors <- e
	}()
	if err = p.call(mobileproto.Request{Type: "hello", Capabilities: &mobileproto.ClientCapabilities{Presence: true, ResetFreeFrames: true, CoalescedFrames: true, HolderLabels: true}, Viewer: &mobileproto.Viewer{Kind: "ios", Label: "iPhone proof"}}); err != nil {
		return err
	}
	if err = p.call(mobileproto.Request{Type: "resolve", Target: os.Args[3]}); err != nil {
		return err
	}
	targetHandle := p.attachment
	p.attachment = ""
	if err = p.call(mobileproto.Request{Type: "open", TargetHandle: targetHandle, AttachmentID: "three-viewer-ios"}); err != nil {
		return err
	}
	commands := make(chan command)
	go func() {
		defer close(commands)
		s := bufio.NewScanner(os.Stdin)
		for s.Scan() {
			var c command
			if json.Unmarshal(s.Bytes(), &c) != nil {
				return
			}
			select {
			case commands <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	encoder := json.NewEncoder(os.Stdout)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	present := false
	for {
		select {
		case r := <-p.responses:
			p.apply(r)
		case err := <-p.errors:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if present {
				if err = p.call(mobileproto.Request{Type: "heartbeat"}); err != nil {
					return err
				}
			}
		case c, ok := <-commands:
			if !ok || c.Type == "close" {
				return nil
			}
			switch c.Type {
			case "presence":
				err = p.call(mobileproto.Request{Type: "presence", Presence: c.Presence})
				present = true
			case "input":
				err = p.call(mobileproto.Request{Type: "input", DataBase64: base64.StdEncoding.EncodeToString([]byte(c.Text))})
			case "state":
			default:
				return fmt.Errorf("unknown command %q", c.Type)
			}
			if err != nil {
				return err
			}
			if err = encoder.Encode(map[string]any{"geometry": p.geometry, "holder": p.holder, "ready": p.ready}); err != nil {
				return err
			}
		}
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "threeviewer:", err)
		os.Exit(1)
	}
}
