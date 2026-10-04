package uiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacewire"
)

const eventsPath = "/api/v0/events"
const eventsDebounce = 250 * time.Millisecond
const maxPendingAttentionBytes = 1 << 20

// CatalogChangeSource supplies real invalidations, not a catalog polling clock.
// One subscription lives for the API process. A buffered signal may coalesce
// any number of changes; Sessions must always return the current snapshot.
type CatalogChangeSource interface {
	WatchCatalog(context.Context) (<-chan struct{}, error)
}

// GeometryHolderSource observes only sessions with open API attachments.
// The holder is presentation data, never a lease token or instance identity.
type GeometryHolderSource interface {
	GeometryHolder(context.Context, TerminalInfo) *GeometryHolder
}

type GeometryHolder struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type EventTerminal struct {
	ClientID    string          `json:"client_id"`
	OwnerHostID string          `json:"owner_host_id,omitempty"`
	Session     string          `json:"session"`
	Pane        string          `json:"pane"`
	DisplayName string          `json:"display_name,omitempty"`
	Holder      *GeometryHolder `json:"holder" jsonschema:"nullable"`
}

type AttentionEvent struct {
	Kind      string    `json:"kind" jsonschema:"enum=needs_input,enum=finished"`
	CatalogID string    `json:"catalog_id"`
	Title     string    `json:"title"`
	Time      time.Time `json:"time"`
}

// EventMessage is one text frame (or one JSONL line on the CLI bridge).
// Seq starts at 1 with hello, increases on delivery, and resets on reconnect.
type EventMessage struct {
	Viewer        *ViewerIdentity               `json:"viewer,omitempty"`
	UIRequest     *UIRequestEvent               `json:"ui_request,omitempty"`
	Type          string                        `json:"type" jsonschema:"enum=hello,enum=catalog,enum=attention,enum=terminals,enum=workspace,enum=content,enum=viewer,enum=ui_request,enum=error,enum=shutdown"`
	Seq           uint64                        `json:"seq" jsonschema:"minimum=1"`
	APIVersion    int                           `json:"api_version" jsonschema:"enum=0"`
	APIInstance   string                        `json:"api_instance,omitempty"`
	ServerVersion string                        `json:"server_version,omitempty"`
	Capabilities  []string                      `json:"capabilities,omitempty"`
	Catalog       *mobileproto.CatalogSnapshot  `json:"catalog,omitempty"`
	Attention     *AttentionEvent               `json:"attention,omitempty"`
	Terminals     *[]EventTerminal              `json:"terminals,omitempty"`
	Error         *ErrorDetail                  `json:"error,omitempty"`
	Workspace     *workspacewire.WorkspaceEvent `json:"workspace,omitempty"`
	Content       *ContentEvent                 `json:"content,omitempty"`
	Reason        string                        `json:"reason,omitempty"`
}

// eventSignals fans one backend observation stream out to bounded per-client
// invalidations. A pending signal already means "read the latest state".
type eventSignals struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (b *eventSignals) subscribe() (<-chan struct{}, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = make(map[chan struct{}]struct{})
	}
	ch := make(chan struct{}, 1)
	b.subs[ch] = struct{}{}
	return ch, func() { b.mu.Lock(); delete(b.subs, ch); b.mu.Unlock() }
}

func (b *eventSignals) signal() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Server) startCatalogEvents() {
	s.eventOnce.Do(func() {
		source, ok := s.opts.Backend.(CatalogChangeSource)
		if !ok {
			return
		}
		changes, err := source.WatchCatalog(s.ctx)
		if err != nil {
			s.opts.Logf("catalog watch: %v", err)
			s.eventErr = err
			return
		}
		go func() {
			for {
				select {
				case <-s.ctx.Done():
					return
				case _, ok := <-changes:
					if !ok {
						return
					}
					s.catalogEvents.signal()
				}
			}
		}()
	})
}

func eventQuery(values url.Values) (mobileproto.CatalogQuery, error) {
	copy := make(url.Values, len(values))
	for key, list := range values {
		copy[key] = append([]string(nil), list...)
	}
	copy.Del("ticket")
	copy.Del("content")
	copy.Del("viewer")
	if len(values["viewer"]) > 1 || values.Has("viewer") && values.Get("viewer") != uirequest.APIViewerRelay {
		return mobileproto.CatalogQuery{}, fmt.Errorf("viewer must be uiRequestRelayV1")
	}
	if len(values["ticket"]) > 1 {
		return mobileproto.CatalogQuery{}, fmt.Errorf("ticket takes one value")
	}
	query, err := ParseCatalogQuery(copy)
	if err == nil {
		err = mobile.ValidateCatalogQuery(query)
	}
	return query, err
}

func (h *listenerHandler) serveStream(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == eventsPath {
		h.serveEvents(w, r)
	} else {
		h.serveTerminal(w, r)
	}
}

func (h *listenerHandler) serveEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, CodeMethod, "Open the events stream with GET.")
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writeError(w, http.StatusUpgradeRequired, CodeUpgradeRequired, "Open /api/v0/events as a WebSocket.")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	c, code, reason := h.authorizeTerminal(r)
	if code != 0 {
		_ = conn.Close(code, reason)
		return
	}
	query, err := eventQuery(r.URL.Query())
	if err != nil {
		_ = conn.Close(CloseProtocolViolation, closeReason(err.Error()))
		return
	}
	if !h.s.beginStream() {
		_ = conn.Close(CloseShuttingDown, "The Sidecar API is shutting down; reconnect when it is back.")
		return
	}
	defer h.s.streams.Done()
	h.s.credentialMu.Lock()
	if !h.s.callerLive(c) {
		h.s.credentialMu.Unlock()
		_ = conn.Close(CloseUnauthenticated, revokedSessionReason)
		return
	}
	client, ok := h.s.clients.add("events", c)
	h.s.credentialMu.Unlock()
	if !ok {
		_ = conn.Close(CloseTooManyTerminals, "This client has 16 events streams; close one first.")
		return
	}
	defer h.s.clients.remove(client)
	refs, err := parseContentRefs(r.URL.Query())
	if err != nil {
		_ = conn.Close(CloseProtocolViolation, closeReason(err.Error()))
		return
	}
	if len(refs) > 0 && !h.s.hasScope(c, ScopeContentRead) {
		_ = conn.Close(CloseOriginRefused, "This credential needs content:read to watch content.")
		return
	}
	h.s.runEvents(conn, client, c, query, refs, r.URL.Query().Has("viewer"))
}

// eventPending separates collection from socket writes. State is latest-wins;
// attention coalesces by row and kind with an explicit fixed maximum. The
// writer assigns seq, so replacing pending data never creates sequence gaps.
type eventPending struct {
	mu             sync.Mutex
	wake           chan struct{}
	catalog        *EventMessage
	terminals      *EventMessage
	workspace      *EventMessage
	attention      map[string]EventMessage
	attentionSizes map[string]int
	attentionBytes int
	content        map[string]ContentRef
	err            *EventMessage
}

func newEventPending() *eventPending {
	return &eventPending{wake: make(chan struct{}, 1), attention: make(map[string]EventMessage), attentionSizes: make(map[string]int)}
}
func (p *eventPending) put(m EventMessage) {
	p.mu.Lock()
	switch m.Type {
	case "catalog":
		p.catalog = &m
	case "terminals":
		p.terminals = &m
	case "workspace":
		p.workspace = &m
	case "attention":
		key := m.Attention.CatalogID + "\x00" + m.Attention.Kind
		encoded, _ := json.Marshal(m)
		nextBytes := p.attentionBytes - p.attentionSizes[key] + len(encoded)
		_, exists := p.attention[key]
		if (exists || len(p.attention) < mobileproto.MaxCatalogRows*2) && nextBytes <= maxPendingAttentionBytes {
			p.attention[key] = m
			p.attentionSizes[key] = len(encoded)
			p.attentionBytes = nextBytes
		} else {
			// A changing row population can outgrow even the maximum catalog.
			// Surface that loss explicitly; never silently discard an alert.
			p.err = &EventMessage{Type: "error", Error: &ErrorDetail{Code: mobileproto.ErrorOverflow, Message: "Pending attention exceeded the stream bound; use the latest catalog to reconcile attention."}}
		}
	case "content":
		if p.content == nil {
			p.content = make(map[string]ContentRef)
		}
		for _, ref := range m.Content.Resources {
			key, _ := json.Marshal(ref)
			p.content[string(key)] = ref
		}
	case "error":
		p.err = &m
	}
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *eventPending) take() []EventMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]EventMessage, 0, 3+len(p.attention))
	if p.catalog != nil {
		out = append(out, *p.catalog)
		p.catalog = nil
	}
	keys := make([]string, 0, len(p.attention))
	for key := range p.attention {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, p.attention[key])
		delete(p.attention, key)
		delete(p.attentionSizes, key)
	}
	p.attentionBytes = 0
	if p.terminals != nil {
		out = append(out, *p.terminals)
		p.terminals = nil
	}
	if p.workspace != nil {
		out = append(out, *p.workspace)
		p.workspace = nil
	}
	if len(p.content) != 0 {
		keys := make([]string, 0, len(p.content))
		for key := range p.content {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		refs := make([]ContentRef, 0, len(keys))
		for _, key := range keys {
			refs = append(refs, p.content[key])
		}
		p.content = nil
		out = append(out, EventMessage{Type: "content", Content: &ContentEvent{Resources: refs}})
	}
	if p.err != nil {
		out = append(out, *p.err)
		p.err = nil
	}
	return out
}

func attentionChanges(before, after *mobileproto.CatalogSnapshot, now time.Time) []AttentionEvent {
	if before == nil {
		return nil
	} // Initial state is not a new alert.
	previous := make(map[string]mobileproto.CatalogRow)
	for _, section := range before.Sections {
		for _, row := range section.Rows {
			previous[row.ID] = row
		}
	}
	var out []AttentionEvent
	for _, section := range after.Sections {
		for _, row := range section.Rows {
			old, exists := previous[row.ID]
			if !exists || row.Stale || !row.Live {
				continue
			}
			kind := ""
			if !old.Attention && row.Attention {
				kind = "needs_input"
			} else if old.Status == "working" && row.Status == "done" {
				kind = "finished"
			}
			if kind != "" {
				out = append(out, AttentionEvent{Kind: kind, CatalogID: row.ID, Title: row.DisplayName, Time: now.UTC()})
			}
		}
	}
	return out
}

func (s *Server) runEvents(conn *websocket.Conn, client *trackedClient, c caller, query mobileproto.CatalogQuery, refs []ContentRef, viewer bool) {
	s.startCatalogEvents()
	catalogChanges, unsubscribe := s.catalogEvents.subscribe()
	defer unsubscribe()
	terminalChanges, unwatch := s.clients.changes.subscribe()
	defer unwatch()
	ctx, cancel := context.WithCancel(s.ctx)
	// Revocation must not wait behind a blocked data write or an attention
	// batch. Close independently; its control-write deadline also bounds the
	// lifetime of a revoked socket whose peer no longer reads.
	revocationDone := make(chan struct{})
	go func() {
		defer close(revocationDone)
		select {
		case <-client.revoked:
			_ = conn.Close(CloseUnauthenticated, revokedSessionReason)
		case <-ctx.Done():
		}
	}()
	defer func() { cancel(); <-revocationDone }()
	conn.SetReadLimit(1024)
	// The stream is server-to-client; still read to process pongs and EOF.
	readDone := make(chan error, 1)
	go func() { _, _, err := conn.Read(context.Background()); readDone <- err }()
	go s.keepalive(ctx, conn, newInboundGate())
	seq := uint64(0)
	write := func(m EventMessage) error {
		select {
		case <-client.revoked:
			_ = conn.Close(CloseUnauthenticated, revokedSessionReason)
			return fmt.Errorf("events credential revoked")
		default:
		}
		seq++
		m.Seq = seq
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		writeCtx, done := context.WithTimeout(context.Background(), terminalWriteTimeout)
		defer done()
		return conn.Write(writeCtx, websocket.MessageText, data)
	}
	pending := newEventPending()
	stop, err := s.startContentWatches(ctx, c, refs, pending)
	if err != nil {
		code := CloseProtocolViolation
		if errors.Is(err, errWatchBudget) {
			code = CloseTooManyTerminals
		}
		_ = conn.Close(code, closeReason(err.Error()))
		return
	}
	defer stop()
	if err := write(EventMessage{Type: "hello", APIInstance: s.instance, ServerVersion: s.opts.Version, Capabilities: []string{"catalog", "attention", "terminals", "workspace", "content", "uiRequestRelayV1", "shutdown"}}); err != nil {
		return
	}
	if s.eventErr != nil {
		_ = write(EventMessage{Type: "error", Error: &ErrorDetail{Code: CodeBackend, Message: s.eventErr.Error()}})
		_ = conn.Close(websocket.StatusInternalError, "The catalog watcher could not start; restart sidecar api serve.")
		return
	}
	var viewerEvents <-chan EventMessage
	if viewer {
		v, err := s.registerViewer(c)
		if err != nil {
			_ = conn.Close(CloseOriginRefused, closeReason(err.Error()))
			return
		}
		defer s.removeViewer(v)
		viewerEvents = v.out
		if write(EventMessage{Type: "viewer", Viewer: &ViewerIdentity{ID: v.id, Capability: uirequest.APIViewerRelay}}) != nil {
			return
		}
	}
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		s.collectEvents(ctx, c, query, catalogChanges, terminalChanges, pending)
	}()
	defer func() { cancel(); <-producerDone }()
	for {
		select {
		case <-client.revoked:
			_ = conn.Close(CloseUnauthenticated, revokedSessionReason)
			return
		case <-s.ctx.Done():
			_ = write(EventMessage{Type: "shutdown", Reason: "The Sidecar API is restarting or stopping; reconnect when it is back."})
			_ = conn.Close(CloseShuttingDown, "The Sidecar API is shutting down; reconnect when it is back.")
			return
		case err := <-readDone:
			if err == nil {
				_ = conn.Close(CloseProtocolViolation, "The events stream accepts no client messages.")
			}
			return
		case m := <-viewerEvents:
			if write(m) != nil {
				return
			}
		case <-pending.wake:
			for _, m := range pending.take() {
				if write(m) != nil {
					return
				}
			}
		}
	}
}

func (s *Server) collectEvents(ctx context.Context, c caller, query mobileproto.CatalogQuery, catalogs, terminals <-chan struct{}, pending *eventPending) {
	var previous *mobileproto.CatalogSnapshot
	var lastTerminals []EventTerminal
	refreshCatalog := func() {
		if !s.hasScope(c, ScopeFull) {
			return
		}
		queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		snapshot, err := s.opts.Backend.Sessions(queryCtx, query)
		if err != nil {
			pending.put(EventMessage{Type: "error", Error: &ErrorDetail{Code: CodeBackend, Message: err.Error()}})
			return
		}
		if previous != nil && snapshot.Generation == previous.Generation {
			return
		}
		pending.put(EventMessage{Type: "catalog", Catalog: &snapshot})
		for _, alert := range attentionChanges(previous, &snapshot, s.opts.Now()) {
			a := alert
			pending.put(EventMessage{Type: "attention", Attention: &a})
		}
		previous = &snapshot
	}
	refreshTerminals := func() {
		if !s.hasScope(c, ScopeFull) {
			return
		}
		current := s.eventTerminals(ctx, c)
		if reflect.DeepEqual(lastTerminals, current) {
			return
		}
		pending.put(EventMessage{Type: "terminals", Terminals: &current})
		lastTerminals = current
	}
	refreshWorkspace := func() {
		if s.hasScope(c, ScopeWorkspaceWrite) {
			s.refreshWorkspaceEvents(ctx, pending)
		}
	}
	refreshCatalog()
	refreshWorkspace()
	refreshTerminals()
	// Only the lease of an open attachment is observed on this clock. It
	// never collects a catalog, Git inventory, or a terminal screen.
	leaseTick := time.NewTicker(time.Second)
	defer leaseTick.Stop()
	var timer *time.Timer
	var debounce <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-catalogs:
			if debounce == nil {
				timer = time.NewTimer(eventsDebounce)
				debounce = timer.C
			}
		case <-debounce:
			debounce = nil
			refreshCatalog()
			refreshWorkspace()
		case <-terminals:
			refreshTerminals()
		case <-leaseTick.C:
			refreshTerminals()
		}
	}
}

func (s *Server) eventTerminals(ctx context.Context, c caller) []EventTerminal {
	terms := s.clients.terminalsFor(c.client)
	out := make([]EventTerminal, 0, len(terms))
	source, _ := s.opts.Backend.(GeometryHolderSource)
	for _, term := range terms {
		var holder *GeometryHolder
		if term.Holder != nil {
			// A negotiated holder observation includes explicit unowned state.
			// Do not replace it with a legacy inferred label or a live tmux read
			// in fixture mode.
			if term.Holder.Kind != "" || term.Holder.Label != "" {
				holder = &GeometryHolder{Kind: term.Holder.Kind, Label: term.Holder.Label}
			}
		} else if source != nil {
			holder = s.legacyHolder(ctx, source, term)
		}
		out = append(out, EventTerminal{ClientID: term.ClientID, OwnerHostID: term.OwnerHostID, Session: term.Session, Pane: term.Pane, DisplayName: term.DisplayName, Holder: holder})
	}
	return out
}
