package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/marcus/sidecar/internal/mobileproto"
)

func runV1(ctx context.Context, s *session, target, marker string) error {
	caps := mobileproto.ClientCapabilities{Presence: true, ResetFreeFrames: true, CoalescedFrames: true, ServerPaste: true, HolderLabels: true}
	hello, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestHello, Capabilities: &caps, Viewer: &mobileproto.Viewer{Kind: "browser", Label: "U1-d proof"}})
	if err != nil {
		return err
	}
	if hello.Capabilities == nil || !hello.Capabilities.Presence || !hello.Capabilities.ServerPaste {
		return fmt.Errorf("server did not advertise v1")
	}
	resolved, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestResolve, Target: target})
	if err != nil {
		return err
	}
	opened, _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestOpen, TargetHandle: resolved.Target.Handle, AttachmentID: "u1d-proof"})
	if err != nil {
		return err
	}
	frame, err := s.frame(ctx, nil)
	if err != nil {
		return err
	}
	vt, _ := base64.StdEncoding.DecodeString(frame.RenderVTBase64)
	if !frame.ResetFree || !frame.Coalesced || bytes.Contains(vt, []byte("\x1bc")) {
		return fmt.Errorf("v1 frame retained reset or omitted flags")
	}
	fitted := mobileproto.Presence{Focused: true, Visible: true, Columns: frame.Geometry.Columns - 2, Rows: frame.Geometry.Rows - 1}
	op := uint64(0)
	call := func(kind string, p *mobileproto.Presence, data []byte) (mobileproto.Response, error) {
		op++
		request := mobileproto.Request{Type: kind, AttachmentHandle: opened.AttachmentHandle, OperationSequence: op, LastOutputSequence: frame.OutputSequence, LastResetGeneration: frame.ResetGeneration, Presence: p, DataBase64: base64.StdEncoding.EncodeToString(data)}
		if kind == mobileproto.RequestControl || kind == mobileproto.RequestResize {
			request.Columns = frame.Geometry.Columns
			request.Rows = frame.Geometry.Rows
		}
		r, _, e := s.call(ctx, request)
		if e != nil {
			return r, e
		}
		if r.ResetGeneration != frame.ResetGeneration {
			frame, e = s.frame(ctx, func(f mobileproto.Response) bool { return f.ResetGeneration == r.ResetGeneration })
		}
		return r, e
	}
	if _, err = call(mobileproto.RequestControl, nil, nil); err != nil {
		return err
	}
	fitted.Focused = false
	if r, e := call(mobileproto.RequestPresence, &fitted, nil); e != nil {
		return e
	} else if r.Control {
		return fmt.Errorf("explicit control survived blur")
	}
	fitted.Focused = true
	claimed, err := call(mobileproto.RequestPresence, &fitted, nil)
	if err != nil {
		return err
	}
	if !claimed.Control {
		return fmt.Errorf("presence did not claim unowned pane")
	}
	if s.holder == nil || s.holder.Kind != "browser" || s.holder.Label != "U1-d proof" {
		return fmt.Errorf("holder label not received")
	}
	if frame.Geometry.Columns != fitted.Columns || frame.Geometry.Rows != fitted.Rows {
		return fmt.Errorf("presence did not fit geometry")
	}
	fitted.Focused = false
	blurred, err := call(mobileproto.RequestPresence, &fitted, nil)
	if err != nil {
		return err
	}
	if blurred.Control {
		return fmt.Errorf("blur retained control")
	}
	// View-only input must take the size and deliver without an explicit control.
	half := len(marker) / 2
	command := fmt.Sprintf("printf '%%s%%s\\n' '%s' '%s'", marker[:half], marker[half:])
	pasted, err := call(mobileproto.RequestPaste, nil, []byte(command))
	if err != nil {
		return err
	}
	if !pasted.Control {
		return fmt.Errorf("paste did not claim")
	}
	if _, err = call(mobileproto.RequestInput, nil, []byte("\r")); err != nil {
		return err
	}
	frame, err = s.frame(ctx, func(f mobileproto.Response) bool {
		v, _ := base64.StdEncoding.DecodeString(f.RenderVTBase64)
		return bytes.Contains(v, []byte(marker))
	})
	if err != nil {
		return err
	}
	fitted.Focused = true
	if _, err = call(mobileproto.RequestHeartbeat, &fitted, nil); err != nil {
		return err
	}
	fitted.Visible = false
	if _, err = call(mobileproto.RequestPresence, &fitted, nil); err != nil {
		return err
	}
	if _, _, err = s.call(ctx, mobileproto.Request{Type: mobileproto.RequestClose, AttachmentHandle: opened.AttachmentHandle}); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"holder_label": true, "explicit_control_blur": true, "v1": true, "presence_fit": true, "blur_released": true, "paste_takeover_echo": true, "reset_free": true, "coalesced": true})
}
