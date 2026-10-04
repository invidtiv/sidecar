package mobile

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

// Frames may be replaced only behind the last control/reset barrier. The
// writer removes an item before encoding, so an in-flight frame never changes.
type safeEncoder struct {
	mu      sync.Mutex
	enc     *json.Encoder
	pending []mobileproto.Response
	wake    chan struct{}
	err     error
	done    chan struct{}
	closed  bool
	once    sync.Once
	onError func(error)
}

func (e *safeEncoder) write(r mobileproto.Response) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.wake == nil {
		return e.enc.Encode(r)
	}
	if e.err != nil {
		return e.err
	}
	if e.closed {
		return io.ErrClosedPipe
	}
	if r.Type == mobileproto.ResponseFrame && r.Coalesced {
		for i := len(e.pending) - 1; i >= 0; i-- {
			old := e.pending[i]
			if old.Type != mobileproto.ResponseFrame {
				break
			}
			if old.AttachmentHandle == r.AttachmentHandle && old.AttachmentGeneration == r.AttachmentGeneration && old.ResetGeneration == r.ResetGeneration {
				e.pending[i] = r
				return nil
			}
		}
	}
	limit := mobileproto.OutboundQueueDepth
	if r.Coalesced {
		limit = mobileproto.OutboundQueueDepth * (maxAttachments + 1)
	}
	// In negotiated mode, frame slots do not consume control-response capacity.
	controls := 0
	coalesced := r.Coalesced
	for _, old := range e.pending {
		if old.Coalesced {
			coalesced = true
		}
		if old.Type != mobileproto.ResponseFrame {
			controls++
		}
	}
	if (!coalesced && len(e.pending) >= limit) || (r.Type != mobileproto.ResponseFrame && controls >= mobileproto.OutboundQueueDepth) || len(e.pending) >= mobileproto.OutboundQueueDepth*(maxAttachments+1) {
		return fmt.Errorf("mobile service: outbound queue overflow")
	}
	e.pending = append(e.pending, r)
	select {
	case e.wake <- struct{}{}:
	default:
	}
	return nil
}

func newSafeEncoder(output io.Writer) *safeEncoder {
	e := &safeEncoder{enc: json.NewEncoder(output), wake: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		defer close(e.done)
		for {
			<-e.wake
			for {
				e.mu.Lock()
				if len(e.pending) == 0 {
					closed := e.closed
					e.mu.Unlock()
					if closed {
						return
					}
					break
				}
				r := e.pending[0]
				e.pending[0] = mobileproto.Response{}
				e.pending = e.pending[1:]
				e.mu.Unlock()
				if err := e.enc.Encode(r); err != nil {
					e.mu.Lock()
					e.err = err
					callback := e.onError
					e.mu.Unlock()
					if callback != nil {
						callback(err)
					}
					return
				}
			}
		}
	}()
	return e
}

func (e *safeEncoder) close() {
	if e.wake == nil {
		return
	}
	e.once.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.mu.Unlock()
		select {
		case e.wake <- struct{}{}:
		default:
		}
	})
	select {
	case <-e.done:
	case <-time.After(500 * time.Millisecond):
	}
}
