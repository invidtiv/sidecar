package mobilehub

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/marcus/sidecar/internal/mobileproto"
)

// ownerLineQueue bounds the transport seam. Negotiated full repaint frames
// can replace only the last queued frame; every other message is a barrier.
type ownerLineQueue struct {
	mu        sync.Mutex
	lines     [][]byte
	ready     chan struct{}
	closed    bool
	coalesced bool
}

func newOwnerLineQueue(ctx context.Context) *ownerLineQueue {
	opts, _ := ctx.Value(ownerHelloKey{}).(ownerHelloOptions)
	return &ownerLineQueue{ready: make(chan struct{}, 1), coalesced: opts.capabilities != nil && opts.capabilities.CoalescedFrames}
}

func (q *ownerLineQueue) push(line []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if q.coalesced && len(q.lines) > 0 && replaceableOwnerFrames(q.lines[len(q.lines)-1], line) {
		q.lines[len(q.lines)-1] = line
		return true
	}
	if len(q.lines) == mobileproto.OutboundQueueDepth {
		return false
	}
	q.lines = append(q.lines, line)
	q.signal()
	return true
}

func replaceableOwnerFrames(previous, next []byte) bool {
	// Decode only the fence metadata, avoiding a second allocation of the
	// potentially multi-megabyte VT payload at every transport hop.
	type frameFence struct {
		Type                 string `json:"type"`
		RequestID            string `json:"request_id"`
		FrameKind            string `json:"frame_kind"`
		Coalesced            bool   `json:"coalesced"`
		AttachmentHandle     string `json:"attachment_handle"`
		AttachmentGeneration uint64 `json:"attachment_generation"`
		ResetGeneration      uint64 `json:"reset_generation"`
	}
	var a, b frameFence
	if json.Unmarshal(previous, &a) != nil || json.Unmarshal(next, &b) != nil {
		return false
	}
	return a.Type == mobileproto.ResponseFrame && b.Type == mobileproto.ResponseFrame && a.RequestID == "" && b.RequestID == "" &&
		a.FrameKind == "full" && b.FrameKind == "full" && a.Coalesced && b.Coalesced &&
		a.AttachmentHandle == b.AttachmentHandle && a.AttachmentGeneration == b.AttachmentGeneration && a.ResetGeneration == b.ResetGeneration
}

func (q *ownerLineQueue) read(ctx context.Context) ([]byte, error) {
	for {
		q.mu.Lock()
		if len(q.lines) > 0 {
			line := q.lines[0]
			q.lines[0] = nil
			q.lines = q.lines[1:]
			q.mu.Unlock()
			return line, nil
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return nil, io.EOF
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-q.ready:
		}
	}
}

func (q *ownerLineQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.signal()
}

func (q *ownerLineQueue) signal() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
