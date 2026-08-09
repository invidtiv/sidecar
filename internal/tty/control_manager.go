package tty

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var controlPanePattern = regexp.MustCompile(`^%[0-9]+$`)

// ControlSnapshot is an authoritative rendered screen captured in-band over a
// tmux control connection after an output or layout notification.
type ControlSnapshot struct {
	Session       string
	Pane          string
	Output        string
	HistorySize   int
	CaptureBase   int
	HasHistory    bool
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	PaneHeight    int
	PaneWidth     int
	Generation    uint64

	// MouseReporting mirrors tmux's #{mouse_any_flag}: the app running in the
	// pane has enabled at least one mouse tracking mode. It is asked of tmux
	// rather than scanned out of the capture because `capture-pane -e` emits
	// rendering escapes only — DECSET mode sequences never survive it.
	MouseReporting bool
	// PaneTitle and CurrentCommand are captured in the same control-mode
	// metadata response as the screen. Semantic agent probes must never reuse
	// identity from an older ordinary poll while that polling path is suspended.
	PaneTitle      string
	CurrentCommand string
}

// ControlRequest describes one active/visible terminal consumer. Consumers
// retain their existing polling path and resume it from OnFallback whenever
// control mode cannot start or its client dies.
type ControlRequest struct {
	Session    string
	Pane       string
	Width      int
	Height     int
	Scrollback int
	Visible    bool
	Focused    bool
	OnSnapshot func(ControlSnapshot)
	OnFallback func(error)

	// OnModelFrame opts this subscription into the byte-fed screen model
	// (slice 1 of the td-64c916 spike). It is nil everywhere in production:
	// with it nil no model is built, no extra tmux command is issued, and the
	// %output payload is never even decoded, so the *content* of every delivered
	// ControlSnapshot is identical to the pre-slice-1 behavior — asserted as an
	// exact struct value by TestCaptureDeliveryUnchangedWhenModelPathOff.
	//
	// Its *concurrency* did change, for every subscription including this one.
	// Slice 1 moved every command-response callback — the capture path's
	// included — off the control reader goroutine and onto the client's single
	// ordered actor, which is what makes the seed cut exact. A consequence: an
	// OnSnapshot callback that blocks now backpressures the reader, so a slow
	// consumer can make tmux pause the pane or discard bytes for this client,
	// which was not possible before. No Sidecar consumer blocks in OnSnapshot,
	// but a future one must not.
	//
	// A frame is published only after a seed transaction and its post-seed
	// replay have both completed. Until then — and forever, in this slice —
	// capture/polling ownership is unchanged.
	OnModelFrame func(ModelFrame)
	// OnModelInvalid reports that the pane model needs a fresh seed or has
	// stopped. It never implies anything about the capture path.
	OnModelInvalid func(ModelInvalidation)
}

type managerControlSubscription struct {
	id         uint64
	generation uint64
	request    ControlRequest
}

// ControlSubscription is a handle to one manager subscription.
type ControlSubscription struct {
	manager *ControlManager
	id      uint64
	once    sync.Once
}

func (s *ControlSubscription) SetVisible(visible bool) {
	if s != nil && s.manager != nil {
		s.manager.setVisible(s.id, visible)
	}
}

func (s *ControlSubscription) SetFocused(focused bool) {
	if s != nil && s.manager != nil {
		s.manager.setFocused(s.id, focused)
	}
}

func (s *ControlSubscription) Resize(width, height int) {
	if s != nil && s.manager != nil {
		s.manager.resize(s.id, width, height)
	}
}

func (s *ControlSubscription) UsingControl() bool {
	return s != nil && s.manager != nil && s.manager.usingControl(s.id)
}

func (s *ControlSubscription) Close() {
	if s == nil || s.manager == nil {
		return
	}
	s.once.Do(func() { s.manager.unsubscribe(s.id) })
}

// ControlManager owns a small pool of control clients keyed by tmux session.
// tmux control clients are attached to exactly one session, so pooling by pane
// alone would silently miss notifications from Sidecar's other sessions.
type ControlManager struct {
	mu         sync.Mutex
	factory    controlChannelFactory
	coalesce   time.Duration
	nextID     atomic.Uint64
	subs       map[uint64]*managerControlSubscription
	clients    map[string]*sessionControlClient
	starting   map[string]bool
	stopped    bool
}

func NewControlManager() *ControlManager {
	return newControlManager(newProcessControlChannel, 12*time.Millisecond)
}

func newControlManager(factory controlChannelFactory, coalesce time.Duration) *ControlManager {
	if coalesce < 0 {
		coalesce = 0
	}
	return &ControlManager{
		factory:    factory,
		coalesce:   coalesce,
		subs:       make(map[uint64]*managerControlSubscription),
		clients:    make(map[string]*sessionControlClient),
		starting:   make(map[string]bool),
	}
}

func (m *ControlManager) Subscribe(request ControlRequest) (*ControlSubscription, error) {
	if request.Session == "" {
		return nil, fmt.Errorf("tmux control: empty session")
	}
	if !controlPanePattern.MatchString(request.Pane) {
		return nil, fmt.Errorf("tmux control: invalid pane %q", request.Pane)
	}
	if request.Scrollback <= 0 {
		request.Scrollback = DefaultScrollbackLines
	}
	id := m.nextID.Add(1)
	sub := &managerControlSubscription{id: id, generation: 1, request: request}

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return nil, fmt.Errorf("tmux control: manager stopped")
	}
	m.subs[id] = sub
	m.mu.Unlock()

	handle := &ControlSubscription{manager: m, id: id}
	if request.Visible {
		m.activate(id)
	}
	return handle, nil
}

func (m *ControlManager) SetAppFocused(focused bool) {
	// Geometry arbitration needs this bit so an unfocused Sidecar never resizes
	// a shared tmux pane (td-ee222a). Output capture has a different ownership
	// rule: a visible subscription must stay current even while the host terminal
	// is blurred, so application focus does not gate control-mode snapshots.
	SetAppFocused(focused)
}

func (m *ControlManager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	clients := make([]*sessionControlClient, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.clients = make(map[string]*sessionControlClient)
	m.subs = make(map[uint64]*managerControlSubscription)
	m.mu.Unlock()
	for _, client := range clients {
		client.close()
	}
}

func (m *ControlManager) activate(id uint64) {
	m.mu.Lock()
	sub := m.subs[id]
	if sub == nil || !sub.request.Visible || m.stopped {
		m.mu.Unlock()
		return
	}
	client := m.clients[sub.request.Session]
	if client != nil {
		copy := *sub
		client.add(copy)
		m.mu.Unlock()
		return
	}
	session := sub.request.Session
	if m.starting[session] {
		m.mu.Unlock()
		return
	}
	m.starting[session] = true
	m.mu.Unlock()
	go m.startClient(session)
}

// startClient performs process creation off the Bubble Tea update path. Starting
// tmux can be expensive on machines with endpoint security, and subscription
// activation must never hold up a frame.
func (m *ControlManager) startClient(session string) {
	channel, err := m.factory(session)

	m.mu.Lock()
	delete(m.starting, session)
	if m.stopped {
		m.mu.Unlock()
		if channel != nil {
			_ = channel.Close()
		}
		return
	}
	if err != nil {
		var fallbacks []func(error)
		for _, sub := range m.subs {
			if sub.request.Session == session && sub.request.Visible {
				fallbacks = append(fallbacks, sub.request.OnFallback)
			}
		}
		m.mu.Unlock()
		for _, fallback := range fallbacks {
			callFallback(fallback, err)
		}
		return
	}

	var active []managerControlSubscription
	for _, sub := range m.subs {
		if sub.request.Session == session && sub.request.Visible {
			active = append(active, *sub)
		}
	}
	if len(active) == 0 {
		m.mu.Unlock()
		_ = channel.Close()
		return
	}
	client := newSessionControlClient(m, session, channel, m.coalesce)
	m.clients[session] = client
	for _, sub := range active {
		client.add(sub)
	}
	m.mu.Unlock()
}

func (m *ControlManager) setVisible(id uint64, visible bool) {
	m.mu.Lock()
	sub := m.subs[id]
	if sub == nil || sub.request.Visible == visible {
		m.mu.Unlock()
		return
	}
	sub.request.Visible = visible
	sub.generation++
	client := m.clients[sub.request.Session]
	m.mu.Unlock()
	if visible {
		m.activate(id)
		return
	}
	if client != nil {
		client.remove(id)
		m.removeClientIfUnused(client)
	}
}

func (m *ControlManager) setFocused(id uint64, focused bool) {
	m.mu.Lock()
	sub := m.subs[id]
	if sub == nil || sub.request.Focused == focused {
		m.mu.Unlock()
		return
	}
	sub.request.Focused = focused
	client := m.clients[sub.request.Session]
	generation := sub.generation
	m.mu.Unlock()
	if client != nil {
		client.setFocused(id, generation, focused)
	}
}

func (m *ControlManager) resize(id uint64, width, height int) {
	m.mu.Lock()
	sub := m.subs[id]
	if sub == nil {
		m.mu.Unlock()
		return
	}
	sub.request.Width = width
	sub.request.Height = height
	client := m.clients[sub.request.Session]
	generation := sub.generation
	m.mu.Unlock()
	if client != nil {
		client.resize(id, generation, width, height)
	}
}

func (m *ControlManager) usingControl(id uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	sub := m.subs[id]
	if sub == nil || !sub.request.Visible {
		return false
	}
	client := m.clients[sub.request.Session]
	return client != nil && client.has(id, sub.generation)
}

func (m *ControlManager) unsubscribe(id uint64) {
	m.mu.Lock()
	sub := m.subs[id]
	if sub == nil {
		m.mu.Unlock()
		return
	}
	delete(m.subs, id)
	client := m.clients[sub.request.Session]
	m.mu.Unlock()
	if client != nil {
		client.remove(id)
		m.removeClientIfUnused(client)
	}
}

func (m *ControlManager) removeClientIfUnused(client *sessionControlClient) {
	if client.subscriberCount() != 0 {
		return
	}
	m.mu.Lock()
	if m.clients[client.session] == client && client.subscriberCount() == 0 {
		delete(m.clients, client.session)
		m.mu.Unlock()
		client.close()
		return
	}
	m.mu.Unlock()
}

func (m *ControlManager) clientFailed(client *sessionControlClient, err error) {
	m.mu.Lock()
	if m.clients[client.session] != client {
		m.mu.Unlock()
		return
	}
	delete(m.clients, client.session)
	var fallbacks []func(error)
	for _, sub := range m.subs {
		if sub.request.Session == client.session && sub.request.Visible {
			fallbacks = append(fallbacks, sub.request.OnFallback)
		}
	}
	m.mu.Unlock()
	client.close()
	for _, fallback := range fallbacks {
		callFallback(fallback, err)
	}
}

func callFallback(callback func(error), err error) {
	if callback != nil {
		callback(err)
	}
}

type sessionSubscriber struct {
	id         uint64
	generation uint64
	request    ControlRequest
	delivery   *subscriberDeliveryGate
}

// subscriberDeliveryGate makes subscription invalidation a callback barrier.
// Deactivation prevents new callbacks; waiting then drains any callback that
// already began. The client mutex still protects membership and generation
// checks; this gate closes the small race between that check and external code.
type subscriberDeliveryGate struct {
	mu      sync.Mutex
	cond    *sync.Cond
	active  bool
	running int
}

func newSubscriberDeliveryGate() *subscriberDeliveryGate {
	gate := &subscriberDeliveryGate{active: true}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}

func (g *subscriberDeliveryGate) invoke(callback func()) bool {
	if g == nil || callback == nil {
		return false
	}
	g.mu.Lock()
	if !g.active {
		g.mu.Unlock()
		return false
	}
	g.running++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.running--
		if g.running == 0 {
			g.cond.Broadcast()
		}
		g.mu.Unlock()
	}()
	callback()
	return true
}

func (g *subscriberDeliveryGate) deactivate() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.active = false
	g.mu.Unlock()
}

func (g *subscriberDeliveryGate) wait() {
	if g == nil {
		return
	}
	g.mu.Lock()
	for g.running != 0 {
		g.cond.Wait()
	}
	g.mu.Unlock()
}

type paneCaptureState struct {
	dirty    bool
	inFlight bool
	timer    *time.Timer
}

type sessionControlClient struct {
	manager  *ControlManager
	session  string
	channel  controlChannel
	coalesce time.Duration

	mu         sync.Mutex
	subs       map[uint64]sessionSubscriber
	deliveries map[uint64]*subscriberDeliveryGate
	panes      map[string]*paneCaptureState
	closed     bool
	closeOnce  sync.Once

	// actions, models, modelTick, modelTimer and discardArmed belong to the
	// ordered actor goroutine (run). Lifecycle calls arriving on other
	// goroutines are funnelled through actions so that model state and pane
	// bytes are only ever touched in one place, in receive order.
	actions     chan func()
	quit        chan struct{}
	models      map[uint64]*paneModelFeed
	modelTick   chan struct{}
	modelTimer  bool
	discardWait bool
}

// discardProbeInterval is how often a client with at least one live pane model
// asks tmux for its client_discarded counter. It is a cadence, not a per-burst
// command: growth means tmux dropped output for this client, which invalidates
// every model on it. tmux's pause-after flow control normally pauses a pane
// before discarding, so this is the backstop for the case where it does not.
const discardProbeInterval = time.Second

func newSessionControlClient(manager *ControlManager, session string, channel controlChannel, coalesce time.Duration) *sessionControlClient {
	client := &sessionControlClient{
		manager:    manager,
		session:    session,
		channel:    channel,
		coalesce:   coalesce,
		subs:       make(map[uint64]sessionSubscriber),
		deliveries: make(map[uint64]*subscriberDeliveryGate),
		panes:      make(map[string]*paneCaptureState),
		actions:    make(chan func(), 256),
		quit:       make(chan struct{}),
		models:     make(map[uint64]*paneModelFeed),
		modelTick:  make(chan struct{}, 1),
	}
	go client.run()
	// Feature-detect flow control: older tmux versions return an error, which is
	// harmless; supported versions emit extended-output and pause notifications.
	_ = channel.Send("refresh-client -f pause-after=5", func(controlResponse) {})
	return client
}

// post hands work to the ordered actor. It never blocks past client shutdown.
func (c *sessionControlClient) post(action func()) {
	select {
	case c.actions <- action:
	case <-c.quit:
	}
}

// run is the single ordered actor. Command responses reach it on the same
// stream as %output notifications and carry their FIFO callback, so a seed
// capture response is processed at exactly its position in the byte stream.
func (c *sessionControlClient) run() {
	for {
		select {
		case action := <-c.actions:
			action()
		case event := <-c.channel.Events():
			c.handleEvent(event)
		case <-c.modelTick:
			c.publishModelFrames()
		case err := <-c.channel.Done():
			if err == nil {
				err = fmt.Errorf("tmux control exited")
			}
			c.failAllModels(ResyncReconnect, err)
			c.manager.clientFailed(c, err)
			return
		}
	}
}

func (c *sessionControlClient) handleEvent(event controlEvent) {
	switch event.Kind {
	case controlEventResponse:
		// Invoked here rather than on the reader goroutine: this is the
		// ordering barrier. Callbacks must not defer their work back onto the
		// action queue or the barrier is lost.
		if event.Callback != nil {
			event.Callback(event.Response)
		}
	case controlEventOutput:
		c.feedModels(event)
		c.markDirty(event.Pane)
	case controlEventLayout:
		if event.Pane != "" {
			c.requestSeedForPane(event.Pane, ResyncLayout)
			c.markDirty(event.Pane)
		} else {
			c.requestSeedForAll(ResyncLayout)
			c.markAllDirty()
		}
	case controlEventPause:
		if controlPanePattern.MatchString(event.Pane) {
			// The pane target must be quoted. tmux's command parser treats a
			// bare leading '%' as the start of a conditional directive, so
			// `refresh-client -A %7:continue` is a parse error and the pane
			// stays paused forever. Verified against tmux 3.6b.
			_ = c.channel.Send("refresh-client -A '"+event.Pane+":continue'", func(controlResponse) {})
			// tmux drops the pane's buffered output while it is paused, so
			// byte continuity is broken across the pause regardless of how it
			// is resumed.
			c.requestSeedForPane(event.Pane, ResyncPause)
		}
	case controlEventContinue:
		if controlPanePattern.MatchString(event.Pane) {
			c.requestSeedForPane(event.Pane, ResyncPause)
		}
	case controlEventExit:
		err := fmt.Errorf("tmux control exit notification")
		c.failAllModels(ResyncReconnect, err)
		c.manager.clientFailed(c, err)
	}
}

func (c *sessionControlClient) add(sub managerControlSubscription) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		callFallback(sub.request.OnFallback, fmt.Errorf("tmux control client closed"))
		return
	}
	delivery := newSubscriberDeliveryGate()
	c.subs[sub.id] = sessionSubscriber{
		id: sub.id, generation: sub.generation, request: sub.request,
		delivery: delivery,
	}
	c.deliveries[sub.id] = delivery
	c.ensurePaneLocked(sub.request.Pane).dirty = true
	c.mu.Unlock()
	c.configureSize(sub.request.Width, sub.request.Height)
	c.scheduleIfEligible(sub.request.Pane)
	if sub.request.OnModelFrame != nil {
		joined := sub
		c.post(func() { c.startModelFeed(joined) })
	}
}

func (c *sessionControlClient) remove(id uint64) {
	c.mu.Lock()
	sub := c.subs[id]
	sub.delivery.deactivate()
	delete(c.subs, id)
	c.mu.Unlock()
	sub.delivery.wait()
	c.mu.Lock()
	if c.deliveries[id] == sub.delivery {
		delete(c.deliveries, id)
	}
	c.mu.Unlock()
	c.post(func() { c.stopModelFeed(id) })
}

func (c *sessionControlClient) has(id, generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	sub, ok := c.subs[id]
	return ok && sub.generation == generation && !c.closed
}

func (c *sessionControlClient) subscriberCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subs)
}

func (c *sessionControlClient) setFocused(id, generation uint64, focused bool) {
	c.mu.Lock()
	sub, ok := c.subs[id]
	if !ok || sub.generation != generation {
		c.mu.Unlock()
		return
	}
	sub.request.Focused = focused
	c.subs[id] = sub
	pane := sub.request.Pane
	c.mu.Unlock()
	if focused {
		c.scheduleIfEligible(pane)
	}
}

func (c *sessionControlClient) resize(id, generation uint64, width, height int) {
	c.mu.Lock()
	sub, ok := c.subs[id]
	if !ok || sub.generation != generation {
		c.mu.Unlock()
		return
	}
	sub.request.Width = width
	sub.request.Height = height
	c.subs[id] = sub
	c.ensurePaneLocked(sub.request.Pane).dirty = true
	pane := sub.request.Pane
	c.mu.Unlock()
	c.configureSize(width, height)
	c.scheduleIfEligible(pane)
	// Conservative resize: tmux is resized first and the reseed reads back the
	// authoritative resulting geometry. There is no in-model resize in this
	// slice. tmux's own %layout-change for the applied size triggers a second
	// reseed, which is what makes the final geometry authoritative.
	c.post(func() { c.requestSeed(id, ResyncResize) })
}

func (c *sessionControlClient) configureSize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	command := "refresh-client -C " + strconv.Itoa(width) + "x" + strconv.Itoa(height)
	_ = c.channel.Send(command, func(controlResponse) {})
}

func (c *sessionControlClient) markDirty(pane string) {
	if !controlPanePattern.MatchString(pane) {
		return
	}
	c.mu.Lock()
	c.ensurePaneLocked(pane).dirty = true
	c.mu.Unlock()
	c.scheduleIfEligible(pane)
}

func (c *sessionControlClient) markAllDirty() {
	c.mu.Lock()
	panes := make(map[string]struct{})
	for _, sub := range c.subs {
		c.ensurePaneLocked(sub.request.Pane).dirty = true
		panes[sub.request.Pane] = struct{}{}
	}
	c.mu.Unlock()
	for pane := range panes {
		c.scheduleIfEligible(pane)
	}
}

func (c *sessionControlClient) scheduleIfEligible(pane string) {
	c.mu.Lock()
	state := c.ensurePaneLocked(pane)
	if c.closed || !state.dirty || state.inFlight || state.timer != nil || !c.paneEligibleLocked(pane) {
		c.mu.Unlock()
		return
	}
	state.timer = time.AfterFunc(c.coalesce, func() { c.startCapture(pane) })
	c.mu.Unlock()
}

func (c *sessionControlClient) startCapture(pane string) {
	c.mu.Lock()
	state := c.ensurePaneLocked(pane)
	state.timer = nil
	if c.closed || !state.dirty || state.inFlight || !c.paneEligibleLocked(pane) {
		c.mu.Unlock()
		return
	}
	scrollback := DefaultScrollbackLines
	for _, sub := range c.subs {
		if sub.request.Pane == pane && sub.request.Focused && sub.request.Scrollback > 0 {
			scrollback = sub.request.Scrollback
			break
		}
	}
	state.dirty = false
	state.inFlight = true
	c.mu.Unlock()

	metadataCommand, captureCommand, err := buildControlCaptureCommands(pane, scrollback)
	if err != nil {
		c.captureFinished(pane, scrollback, controlResponse{Err: err})
		return
	}
	var responseMu sync.Mutex
	var metadata controlResponse
	var finished sync.Once
	finish := func(response controlResponse) {
		finished.Do(func() { c.captureFinished(pane, scrollback, response) })
	}
	if err := c.channel.Send(metadataCommand, func(response controlResponse) {
		responseMu.Lock()
		metadata = response
		responseMu.Unlock()
		if response.Err != nil {
			finish(response)
		}
	}); err != nil {
		c.manager.clientFailed(c, fmt.Errorf("tmux control metadata write: %w", err))
		return
	}
	if err := c.channel.Send(captureCommand, func(response controlResponse) {
		responseMu.Lock()
		meta := metadata
		responseMu.Unlock()
		if meta.Err != nil {
			finish(meta)
			return
		}
		if len(meta.Lines) == 0 {
			finish(controlResponse{Err: errors.New("tmux control capture: missing cursor response")})
			return
		}
		response.Lines = append(append([]string(nil), meta.Lines...), response.Lines...)
		finish(response)
	}); err != nil {
		c.manager.clientFailed(c, fmt.Errorf("tmux control capture write: %w", err))
	}
}

func (c *sessionControlClient) captureFinished(pane string, scrollback int, response controlResponse) {
	if response.Err != nil {
		c.manager.clientFailed(c, response.Err)
		return
	}
	snapshot, err := parseControlSnapshot(c.session, pane, scrollback, response.Lines)
	if err != nil {
		c.manager.clientFailed(c, err)
		return
	}

	c.mu.Lock()
	state := c.ensurePaneLocked(pane)
	state.inFlight = false
	var deliveries []sessionSubscriber
	for _, sub := range c.subs {
		if sub.request.Pane == pane {
			deliveries = append(deliveries, sub)
		}
	}
	again := state.dirty
	c.mu.Unlock()

	for _, sub := range deliveries {
		// A prior callback may synchronously unsubscribe another consumer, and
		// Stop/Close may invalidate the whole client while a response is queued.
		// Revalidate immediately before each external callback rather than
		// trusting the delivery snapshot assembled above.
		c.mu.Lock()
		current, ok := c.subs[sub.id]
		valid := !c.closed && ok && current.generation == sub.generation
		callback := current.request.OnSnapshot
		gate := current.delivery
		c.mu.Unlock()
		if !valid || callback == nil {
			continue
		}
		copy := snapshot
		copy.Generation = current.generation
		gate.invoke(func() { callback(copy) })
	}
	if again {
		c.scheduleIfEligible(pane)
	}
}

func (c *sessionControlClient) paneEligibleLocked(pane string) bool {
	for _, sub := range c.subs {
		if sub.request.Pane == pane && sub.request.Visible && sub.request.Focused {
			return true
		}
	}
	return false
}

func (c *sessionControlClient) ensurePaneLocked(pane string) *paneCaptureState {
	state := c.panes[pane]
	if state == nil {
		state = &paneCaptureState{}
		c.panes[pane] = state
	}
	return state
}

func (c *sessionControlClient) close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		// Invalidate queued delivery snapshots before releasing the close
		// barrier. A late control response can still run its FIFO callback, but
		// it can no longer reach a consumer after Close/Stop.
		gates := make([]*subscriberDeliveryGate, 0, len(c.deliveries))
		for _, gate := range c.deliveries {
			gates = append(gates, gate)
		}
		c.subs = make(map[uint64]sessionSubscriber)
		c.deliveries = make(map[uint64]*subscriberDeliveryGate)
		for _, pane := range c.panes {
			if pane.timer != nil {
				pane.timer.Stop()
				pane.timer = nil
			}
			pane.dirty = false
		}
		c.mu.Unlock()
		// Deactivate every subscriber before waiting for any running callback;
		// otherwise a callback queued behind the first one could start while
		// Close is waiting on that first callback.
		for _, gate := range gates {
			gate.deactivate()
		}
		for _, gate := range gates {
			gate.wait()
		}
		// Best effort: release emulators on the actor that owns them. If the
		// actor has already exited, its Done path did the same teardown.
		select {
		case c.actions <- func() { c.failAllModels(ResyncReconnect, errClientClosed) }:
		default:
		}
		close(c.quit)
		_ = c.channel.Close()
	})
}

func buildControlCaptureCommands(pane string, scrollback int) (metadata, capture string, err error) {
	if !controlPanePattern.MatchString(pane) {
		return "", "", fmt.Errorf("tmux control: invalid pane %q", pane)
	}
	if scrollback <= 0 {
		scrollback = DefaultScrollbackLines
	}
	metadata = "display-message -p -t " + pane +
		" '#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width},#{history_size},#{mouse_any_flag},#{pane_current_command},#{pane_title}'"
	capture = "capture-pane -p -e -S -" + strconv.Itoa(scrollback) + " -t " + pane
	return metadata, capture, nil
}

func parseControlSnapshot(session, pane string, scrollback int, lines []string) (ControlSnapshot, error) {
	if len(lines) == 0 {
		return ControlSnapshot{}, errors.New("tmux control capture: missing cursor metadata")
	}
	// SplitN preserves commas in pane titles. pane_current_command is a tmux
	// command name rather than arbitrary terminal content and cannot contain a
	// comma in ordinary tmux output.
	parts := strings.SplitN(strings.TrimSpace(lines[0]), ",", 9)
	// Fields past the sixth are optional so a metadata line produced before they
	// were added still parses.
	if len(parts) < 6 {
		return ControlSnapshot{}, fmt.Errorf("tmux control capture: invalid cursor metadata %q", lines[0])
	}
	col, errCol := strconv.Atoi(parts[0])
	row, errRow := strconv.Atoi(parts[1])
	height, errHeight := strconv.Atoi(parts[3])
	width, errWidth := strconv.Atoi(parts[4])
	historySize, errHistory := strconv.Atoi(parts[5])
	if errCol != nil || errRow != nil || errHeight != nil || errWidth != nil ||
		errHistory != nil || historySize < 0 {
		return ControlSnapshot{}, fmt.Errorf("tmux control capture: invalid cursor metadata %q", lines[0])
	}
	if scrollback <= 0 {
		scrollback = DefaultScrollbackLines
	}
	mouseReporting := len(parts) >= 7 && parts[6] != "0" && parts[6] != ""
	currentCommand := ""
	paneTitle := ""
	if len(parts) >= 8 {
		currentCommand = parts[7]
	}
	if len(parts) >= 9 {
		paneTitle = parts[8]
	}
	return ControlSnapshot{
		Session:        session,
		Pane:           pane,
		Output:         strings.Join(lines[1:], "\n"),
		HistorySize:    historySize,
		CaptureBase:    max(historySize-scrollback, 0),
		HasHistory:     true,
		CursorRow:      row,
		CursorCol:      col,
		CursorVisible:  parts[2] != "0",
		PaneHeight:     height,
		PaneWidth:      width,
		MouseReporting: mouseReporting,
		PaneTitle:      paneTitle,
		CurrentCommand: currentCommand,
	}, nil
}
