package mobilehub

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// catalogOwnerPool keeps only catalog connections warm. Owner protocol
// connections have one negotiated viewer and one terminal attachment, so
// terminal lookup must continue to use a private connection.
type catalogOwnerPool struct {
	ctx      context.Context
	registry RouteRegistry
	mu       sync.Mutex
	entries  map[string]*catalogOwnerEntry
	closed   bool
}

type catalogOwnerEntry struct {
	authority    hosts.MobileRouteAuthority
	ctx          context.Context
	cancel       context.CancelFunc
	gate         chan struct{}
	mu           sync.Mutex
	stream       *OwnerStream
	streamCancel context.CancelFunc
	hello        mobileproto.Response
	number       atomic.Uint64
}

func newCatalogOwnerPool(ctx context.Context, registry RouteRegistry) *catalogOwnerPool {
	p := &catalogOwnerPool{ctx: ctx, registry: registry, entries: make(map[string]*catalogOwnerEntry)}
	context.AfterFunc(ctx, p.close)
	return p
}

func (p *catalogOwnerPool) acquire(ctx context.Context, authority hosts.MobileRouteAuthority) (LineStream, mobileproto.Response, error) {
	if err := p.registry.ValidateMobileRoute(authority); err != nil {
		return nil, mobileproto.Response{}, err
	}
	p.mu.Lock()
	if p.closed || p.ctx.Err() != nil {
		p.mu.Unlock()
		return nil, mobileproto.Response{}, ErrOwnerClosed
	}
	entry := p.entries[authority.HostID]
	var retired *catalogOwnerEntry
	if entry != nil && entry.authority != authority {
		retired, entry = entry, nil
	}
	if entry == nil {
		entryCtx, cancel := context.WithCancel(p.ctx)
		entry = &catalogOwnerEntry{authority: authority, ctx: entryCtx, cancel: cancel, gate: make(chan struct{}, 1)}
		entry.gate <- struct{}{}
		p.entries[authority.HostID] = entry
	}
	p.mu.Unlock()
	if retired != nil {
		retired.close()
	}
	select {
	case <-ctx.Done():
		return nil, mobileproto.Response{}, ctx.Err()
	case <-entry.ctx.Done():
		return nil, mobileproto.Response{}, ErrOwnerClosed
	case <-entry.gate:
	}
	fail := func(err error) (LineStream, mobileproto.Response, error) {
		entry.gate <- struct{}{}
		return nil, mobileproto.Response{}, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := p.registry.ValidateMobileRoute(authority); err != nil {
		return fail(err)
	}
	entry.mu.Lock()
	if entry.ctx.Err() != nil {
		entry.mu.Unlock()
		return fail(ErrOwnerClosed)
	}
	if entry.stream != nil && (entry.stream.terminalError() != nil || entry.stream.isClosing()) {
		entry.stream.Close()
		entry.streamCancel()
		entry.stream = nil
	}
	if entry.stream == nil {
		// Caller cancellation may stop startup, but must not become the
		// lifetime of a successfully established warm connection.
		startCtx, cancel := context.WithCancel(entry.ctx)
		stop := context.AfterFunc(ctx, cancel)
		stream, hello, err := StartOwner(startCtx, p.registry, authority)
		stopped := stop()
		if err != nil || !stopped || ctx.Err() != nil {
			cancel()
			if stream != nil {
				stream.Close()
			}
			entry.mu.Unlock()
			if err == nil {
				err = ctx.Err()
				if err == nil {
					err = context.Canceled
				}
			}
			return fail(err)
		}
		entry.stream, entry.hello, entry.streamCancel = stream, hello, cancel
	}
	stream, hello := entry.stream, entry.hello
	entry.mu.Unlock()
	return &catalogOwnerLease{entry: entry, stream: stream}, hello, nil
}

// reconcile retires removals, disabled owners and changed registrations even
// when their health prevents them appearing as a bindable endpoint.
func (p *catalogOwnerPool) reconcile(enabled []hosts.Host) {
	current := make(map[string]string, len(enabled))
	for _, host := range enabled {
		current[host.ID] = hosts.RegistrationFingerprint(host)
	}
	p.mu.Lock()
	var retired []*catalogOwnerEntry
	for id, entry := range p.entries {
		if current[id] != entry.authority.RegistrationFingerprint {
			retired = append(retired, entry)
			delete(p.entries, id)
		}
	}
	p.mu.Unlock()
	for _, entry := range retired {
		entry.close()
	}
}

func (p *catalogOwnerPool) close() {
	p.mu.Lock()
	p.closed = true
	entries := p.entries
	p.entries = make(map[string]*catalogOwnerEntry)
	p.mu.Unlock()
	for _, entry := range entries {
		entry.close()
	}
}

func (e *catalogOwnerEntry) close() {
	e.cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stream != nil {
		e.stream.Close()
		e.streamCancel()
	}
}

// A lease is exactly one request/reply. Its reader belongs to the pool, so a
// canceled catalog caller leaves the connection alive while its late reply is
// drained. The next caller cannot write until that reply has been discarded.
// No catalog snapshot is cached here; every query still reads current owners.
type catalogOwnerLease struct {
	entry  *catalogOwnerEntry
	stream *OwnerStream
	mu     sync.Mutex
	closed bool
	ready  chan struct{}
	line   []byte
	err    error
}

func (s *catalogOwnerLease) WriteLine(line []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ready != nil {
		return ErrOwnerClosed
	}
	var request mobileproto.Request
	if json.Unmarshal(line, &request) != nil || request.Type != mobileproto.RequestSessions {
		return fmt.Errorf("mobile hub: warm owner accepts only catalog requests")
	}
	publicID := request.RequestID
	request.RequestID = fmt.Sprintf("hub-warm-%d", s.entry.number.Add(1))
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	s.ready = make(chan struct{})
	go func() {
		readCtx, cancel := context.WithTimeout(s.entry.ctx, OwnerCatalogTimeout)
		defer cancel()
		stop := context.AfterFunc(readCtx, s.stream.Close)
		defer stop()
		err := s.stream.WriteLine(encoded)
		var data []byte
		if err == nil {
			data, err = s.stream.ReadLine(readCtx)
		}
		if err == nil {
			var response mobileproto.Response
			if json.Unmarshal(data, &response) != nil || response.Version != mobileproto.Version || response.RequestID != request.RequestID ||
				(response.Type != mobileproto.ResponseSessions && response.Type != mobileproto.ResponseError) {
				err = fmt.Errorf("mobile hub: warm owner returned an invalid catalog response")
			} else {
				response.RequestID = publicID
				data, err = json.Marshal(response)
			}
		}
		if err != nil {
			// A missing or mismatched reply is real protocol/transport loss;
			// retire it so a late line cannot become the next caller's reply.
			s.stream.Close()
		}
		s.line, s.err = data, err
		close(s.ready)
	}()
	return nil
}

func (s *catalogOwnerLease) ReadLine(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	ready, closed := s.ready, s.closed
	s.mu.Unlock()
	if ready == nil || closed {
		return nil, ErrOwnerClosed
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-ready:
		return s.line, s.err
	}
}

func (s *catalogOwnerLease) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	ready := s.ready
	s.mu.Unlock()
	if ready == nil {
		s.entry.gate <- struct{}{}
		return
	}
	select {
	case <-ready:
		s.entry.gate <- struct{}{}
	default:
		go func() {
			<-ready
			s.entry.gate <- struct{}{}
		}()
	}
}
