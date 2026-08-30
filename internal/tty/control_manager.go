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

// ControlSnapshot is a rendered screen captured in-band over a tmux control
// connection. It supplies bootstrap/recovery presentation and independent
// semantic or diagnostic evidence after an output or layout notification.
type ControlSnapshot struct {
	Session     string
	Pane        string
	Output      string
	HistorySize int
	CaptureBase int
	// HistoryRows and PaneRows split Output into the scrolled-off rows above the
	// pane and the pane's own grid. They are counted while the capture is still
	// a line slice: once Output is joined, a blank final pane row is
	// indistinguishable from a trailing terminator, and a consumer that
	// re-derives the split loses that row and places the cursor one row too high
	// (td-d29821).
	HistoryRows   int
	PaneRows      int
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
	// ModelPresentation permits a live byte-fed model to suppress capture for
	// ordinary output bursts. It is separate from OnModelFrame because the
	// diagnostic comparison oracle deliberately retains independent captures.
	ModelPresentation bool

	// OnModelFrame receives permanent model-backed presentation frames. Command
	// response callbacks, including capture recovery callbacks, run on the
	// client's single
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
	mu           sync.Mutex
	factory      controlChannelFactory
	coalesce     time.Duration
	modelCadence modelCadenceConfig
	nextID       atomic.Uint64
	subs         map[uint64]*managerControlSubscription
	clients      map[string]*sessionControlClient
	starting     map[string]bool
	stopped      bool
}

func NewControlManager() *ControlManager {
	return newControlManager(newProcessControlChannel, 12*time.Millisecond)
}

func newControlManager(factory controlChannelFactory, coalesce time.Duration) *ControlManager {
	if coalesce < 0 {
		coalesce = 0
	}
	return &ControlManager{
		factory:      factory,
		coalesce:     coalesce,
		modelCadence: realtimeModelCadence(),
		subs:         make(map[uint64]*managerControlSubscription),
		clients:      make(map[string]*sessionControlClient),
		starting:     make(map[string]bool),
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
	writeScreenCompareReport()
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
		client.startModel(copy)
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
	for _, sub := range active {
		client.startModel(sub)
	}
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

// clientForCommand returns the live session client, waiting through the short
// replacement window created by a resize reseed. It is called only from
// command goroutines (input's ordered send queue and history/geometry tea.Cmds),
// never from Bubble Tea's update loop.
func (m *ControlManager) clientForCommand(session string) (*sessionControlClient, error) {
	deadline := time.Now().Add(4 * time.Second)
	for {
		m.mu.Lock()
		client := m.clients[session]
		starting := m.starting[session]
		stopped := m.stopped
		m.mu.Unlock()
		if client != nil {
			return client, nil
		}
		if stopped {
			return nil, fmt.Errorf("tmux control: manager stopped")
		}
		if !starting || time.Now().After(deadline) {
			return nil, fmt.Errorf("tmux control: session %q is unavailable", session)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sendControlBatch writes commands onto one session's already-open control
// pipe. It deliberately does not wait for responses: the send queue is a FIFO
// of writes, not network round trips, so fast typing can fill the pipe at local
// speed while tmux executes those writes in order.
func (m *ControlManager) sendControlBatch(session string, commands ...string) error {
	if session == "" || len(commands) == 0 {
		return fmt.Errorf("tmux control: empty session or command batch")
	}
	client, err := m.clientForCommand(session)
	if err != nil {
		return err
	}
	callbacks := make([]func(controlResponse), len(commands))
	for i := range callbacks {
		callbacks[i] = func(controlResponse) {}
	}
	return client.channel.SendBatch(commands, callbacks)
}

// sendControlBarrier writes one command without waiting on the caller, while
// retaining the session client until tmux returns its response. It is for
// teardown writes whose execution must outlive the last pane subscription:
// waiting here would block Bubble Tea, but closing the control process as soon
// as that subscription disappears could discard the write before tmux runs it.
func (m *ControlManager) sendControlBarrier(session, command string) error {
	if session == "" || command == "" {
		return fmt.Errorf("tmux control: empty session or barrier command")
	}
	client, release, err := m.retainControlLifetime(session)
	if err != nil {
		return err
	}

	var once sync.Once
	finish := func() { once.Do(release) }
	// A dead or non-responsive transport must not retain a client forever. The
	// ordinary request path uses the same four-second response bound.
	timer := time.AfterFunc(4*time.Second, finish)
	callback := func(controlResponse) {
		timer.Stop()
		finish()
	}
	if err := client.channel.Send(command, callback); err != nil {
		timer.Stop()
		finish()
		return err
	}
	return nil
}

// retainControlLifetime prevents teardown after the final subscription while
// an asynchronous lifecycle operation is still reaching the remote server.
// Acquiring and releasing the reference touches only local mutexes; callers
// may therefore install it synchronously before returning to Bubble Tea.
func (m *ControlManager) retainControlLifetime(session string) (*sessionControlClient, func(), error) {
	m.mu.Lock()
	client := m.clients[session]
	if client == nil || !client.retainBarrier() {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("tmux control: session %q is unavailable", session)
	}
	m.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() { m.finishBarrier(client) })
	}
	return client, release, nil
}

// finishBarrier may run on the client's ordered actor. It therefore initiates
// shutdown without joining that actor; external close paths retain their
// stronger join through removeClientIfUnused.
func (m *ControlManager) finishBarrier(client *sessionControlClient) {
	m.mu.Lock()
	client.releaseBarrier()
	closeClient := m.clients[client.session] == client && !client.inUse()
	if closeClient {
		delete(m.clients, client.session)
	}
	m.mu.Unlock()
	if closeClient {
		client.beginClose()
	}
}

// requestControlBatch writes a response-bearing command group atomically and
// waits for all response blocks. History and geometry use this from tea.Cmds;
// input never does.
func (m *ControlManager) requestControlBatch(session string, commands ...string) ([]controlResponse, error) {
	if session == "" || len(commands) == 0 {
		return nil, fmt.Errorf("tmux control: empty session or command batch")
	}
	client, err := m.clientForCommand(session)
	if err != nil {
		return nil, err
	}
	responses := make([]controlResponse, len(commands))
	done := make(chan int, len(commands))
	callbacks := make([]func(controlResponse), len(commands))
	for i := range callbacks {
		i := i
		callbacks[i] = func(response controlResponse) {
			responses[i] = response
			done <- i
		}
	}
	if err := client.channel.SendBatch(commands, callbacks); err != nil {
		return nil, err
	}
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for range commands {
		select {
		case <-done:
		case <-timer.C:
			return nil, fmt.Errorf("tmux control: command response timeout")
		}
	}
	for _, response := range responses {
		if response.Err != nil {
			return nil, response.Err
		}
	}
	return responses, nil
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
	if client.inUse() {
		return
	}
	m.mu.Lock()
	if m.clients[client.session] == client && !client.inUse() {
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
	// Failure can be reported by the ordered actor itself. Initiate shutdown
	// without joining here; run's defer is the completion barrier, while Stop
	// and unused-client removal join through close from non-actor callers.
	client.beginClose()
	for _, fallback := range fallbacks {
		callFallback(fallback, err)
	}
}

func callFallback(callback func(error), err error) {
	if callback != nil {
		screenCompareStats.bump(&screenCompareStats.Fallbacks, 1)
		callback(err)
	}
}

type sessionSubscriber struct {
	id         uint64
	generation uint64
	request    ControlRequest
	delivery   *subscriberDeliveryGate
	// modelInvalidSent records that this subscription has already been told its
	// model is terminally invalid, so the ordered actor and beginClose cannot
	// both report the same death. Guarded by the client mutex, like the rest of
	// this struct's membership state.
	modelInvalidSent bool
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

// paneCompareState is shadow-comparison bookkeeping for one pane. Every field
// is owned by the ordered actor goroutine; it exists only when
// SIDECAR_TMUX_SCREEN_COMPARE is on.
type paneCompareState struct {
	// metaSeen/rawSinceMeta detect the capture path's own metadata race. Unlike
	// the seed transaction, the capture path writes its display-message and its
	// capture-pane as two separate writes, so pane bytes can land between the
	// two responses and the delivered cursor can describe an older moment than
	// the delivered screen. A cursor difference measured in that window says
	// nothing about the model, so the comparison declines to score it.
	metaSeen     bool
	rawSinceMeta int
	// pendingSince is when the first output arrived that this capture has not
	// yet delivered. It is the start of the capture path's output-to-frame
	// latency, measured against the identical start point on the model side.
	pendingSince time.Time
}

type sessionControlClient struct {
	manager      *ControlManager
	session      string
	channel      controlChannel
	coalesce     time.Duration
	modelCadence modelCadenceConfig

	mu         sync.Mutex
	subs       map[uint64]sessionSubscriber
	deliveries map[uint64]*subscriberDeliveryGate
	panes      map[string]*paneCaptureState
	closed     bool
	closeOnce  sync.Once
	barriers   int

	// actions, models, modelTick, modelTimer and discardArmed belong to the
	// ordered actor goroutine (run). Lifecycle calls arriving on other
	// goroutines are funnelled through actions so that model state and pane
	// bytes are only ever touched in one place, in receive order.
	actions              chan func()
	quit                 chan struct{}
	actorDone            chan struct{}
	models               map[uint64]*paneModelFeed
	modelTick            chan uint64
	modelTimer           modelCadenceTimer
	modelTimerDeadline   time.Time
	modelTimerGeneration uint64
	discardWait          bool
	// compare is nil unless shadow comparison is enabled.
	compare map[string]*paneCompareState
}

// comparePane returns the pane's shadow-comparison state, or nil when shadow
// comparison is off. Actor-only.
func (c *sessionControlClient) comparePane(pane string) *paneCompareState {
	if c.compare == nil {
		return nil
	}
	state := c.compare[pane]
	if state == nil {
		state = &paneCompareState{}
		c.compare[pane] = state
	}
	return state
}

// discardProbeInterval is how often a client with at least one live pane model
// asks tmux for its client_discarded counter. It is a cadence, not a per-burst
// command: growth means tmux dropped output for this client, which invalidates
// every model on it. tmux's pause-after flow control normally pauses a pane
// before discarding, so this is the backstop for the case where it does not.
const discardProbeInterval = time.Second

func newSessionControlClient(manager *ControlManager, session string, channel controlChannel, coalesce time.Duration) *sessionControlClient {
	client := &sessionControlClient{
		manager:      manager,
		session:      session,
		channel:      channel,
		coalesce:     coalesce,
		modelCadence: manager.modelCadence.normalized(),
		subs:         make(map[uint64]sessionSubscriber),
		deliveries:   make(map[uint64]*subscriberDeliveryGate),
		panes:        make(map[string]*paneCaptureState),
		actions:      make(chan func(), 256),
		quit:         make(chan struct{}),
		actorDone:    make(chan struct{}),
		models:       make(map[uint64]*paneModelFeed),
		modelTick:    make(chan uint64, 1),
	}
	if ScreenCompareEnabled() {
		client.compare = make(map[string]*paneCompareState)
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
	defer close(c.actorDone)
	for {
		select {
		case action := <-c.actions:
			action()
		case event := <-c.channel.Events():
			c.handleEvent(event)
		case generation := <-c.modelTick:
			c.publishModelFrames(generation)
		case <-c.quit:
			c.stopModelTimer()
			c.failAllModels(ResyncReconnect, errClientClosed)
			return
		case err := <-c.channel.Done():
			if err == nil {
				err = fmt.Errorf("tmux control exited")
			}
			c.stopModelTimer()
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
		if state := c.comparePane(event.Pane); state != nil {
			if state.metaSeen {
				state.rawSinceMeta++
			}
			if state.pendingSince.IsZero() {
				state.pendingSince = time.Now()
			}
		}
		c.feedModels(event)
		if !c.liveModelOwnsPresentation(event.Pane) {
			c.markDirty(event.Pane)
		}
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

// liveModelOwnsPresentation runs only on the ordered actor. A request alone is
// insufficient: capture remains the fallback until its model is fully seeded
// and live, and resumes automatically during every reseed or terminal fault.
func (c *sessionControlClient) liveModelOwnsPresentation(pane string) bool {
	// Diagnostic comparison deliberately retains capture as its independent
	// oracle while enabled.
	if ScreenCompareEnabled() {
		return false
	}
	modelOwns := false
	for id, feed := range c.models {
		if feed.pane != pane || feed.state != modelLive {
			continue
		}
		c.mu.Lock()
		sub, ok := c.subs[id]
		live := ok && !c.closed && sub.generation == feed.generation && sub.request.ModelPresentation
		c.mu.Unlock()
		if live {
			modelOwns = true
			break
		}
	}
	if !modelOwns {
		return false
	}
	// Capture is pane-scoped, not subscription-scoped. A visible primary
	// agent/shell may observe the same pane as the focused terminal surface; capture
	// delivery includes that subscriber even when it is unfocused, so it still
	// receives the capture it requested while the terminal ignores the snapshot.
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, sub := range c.subs {
		if sub.request.Pane == pane && sub.request.Visible && !sub.request.ModelPresentation {
			return false
		}
	}
	return true
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
}

// wantsModelFeed reports whether this subscription should run a byte-fed pane
// model. A presentation consumer always does; diagnostic comparison also runs
// an independent model for capture-only semantic subscribers.
func wantsModelFeed(request ControlRequest) bool {
	return request.OnModelFrame != nil || ScreenCompareEnabled()
}

// startModel posts the model-feed start. It is deliberately separate from add
// so the caller can release the ControlManager mutex first: post can block on a
// saturated action queue, and the actor takes that same manager mutex when tmux
// dies, which is a lock-order cycle (slice-1 evidence §9, item 8). Posting after
// the unlock removes the cycle. startModelFeed revalidates the subscription,
// which covers the small window this opens.
func (c *sessionControlClient) startModel(sub managerControlSubscription) {
	if !wantsModelFeed(sub.request) {
		return
	}
	c.post(func() { c.startModelFeed(sub) })
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

func (c *sessionControlClient) retainBarrier() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.barriers++
	return true
}

func (c *sessionControlClient) releaseBarrier() {
	c.mu.Lock()
	if c.barriers > 0 {
		c.barriers--
	}
	c.mu.Unlock()
}

func (c *sessionControlClient) inUse() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subs) != 0 || c.barriers != 0
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
		// On the ordered actor: opening the window in which pane bytes would
		// make this capture's metadata older than its screen.
		if state := c.comparePane(pane); state != nil {
			state.metaSeen = true
			state.rawSinceMeta = 0
			screenCompareStats.bump(&screenCompareStats.MetadataQueries, 1)
		}
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
	snapshot, extras, err := parseControlSnapshotLayout(c.session, pane, scrollback, response.Lines, ScreenCompareEnabled())
	if err != nil {
		c.manager.clientFailed(c, err)
		return
	}
	c.shadowCompare(pane, snapshot, extras)

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
	c.beginClose()
	<-c.actorDone
}

// pendingModelInvalidation is one death notice beginClose owes a subscriber
// whose model the ordered actor did not get to report on.
type pendingModelInvalidation struct {
	callback func(ModelInvalidation)
	gate     *subscriberDeliveryGate
	payload  ModelInvalidation
}

// beginClose is safe from the ordered actor: it signals termination but never
// waits for run to return. Joining belongs to close callers such as Stop and
// unused-client removal, which run outside the actor.
//
// It cannot hand the death notice to the actor and wait for it — not waiting is
// the invariant that lets the actor call this itself — so it reports any notice
// the actor has not already claimed.
func (c *sessionControlClient) beginClose() {
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
		// The death notice has to leave with us. Tearing c.subs down here used
		// to race the ordered actor's failAllModels: when this won, invalidate
		// found no subscription and dropped the terminal ModelInvalidation
		// silently, so a consumer was told to fall back (OnFallback is reported
		// outside this guard) and never told its model had died. Collect the
		// unsent notices while we still hold the state they need; they go out
		// below, before any gate is deactivated.
		pending := make([]pendingModelInvalidation, 0, len(c.subs))
		for id, sub := range c.subs {
			if sub.request.OnModelInvalid == nil || sub.modelInvalidSent {
				continue
			}
			sub.modelInvalidSent = true
			c.subs[id] = sub
			pending = append(pending, pendingModelInvalidation{
				callback: sub.request.OnModelInvalid,
				gate:     sub.delivery,
				payload: ModelInvalidation{
					Session:    sub.request.Session,
					Pane:       sub.request.Pane,
					Generation: sub.generation,
					Reason:     ResyncReconnect,
					Terminal:   true,
					Err:        errClientClosed,
				},
			})
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
		// Before deactivation, so these still pass the gate, and outside the
		// lock, because consumer callbacks never run under c.mu. The gate is
		// still the barrier: a Close that has already deactivated cannot be
		// reached, and gate.wait below drains whatever started here.
		for _, notice := range pending {
			notice.gate.invoke(func() { notice.callback(notice.payload) })
		}
		// Deactivate every subscriber before waiting for any running callback;
		// otherwise a callback queued behind the first one could start while
		// Close is waiting on that first callback.
		for _, gate := range gates {
			gate.deactivate()
		}
		for _, gate := range gates {
			gate.wait()
		}
		close(c.quit)
		_ = c.channel.Close()
	})
}

// captureMetadataFields is the ordinary capture metadata. It is unchanged from
// the pre-shadow behavior and is what every default (compare-off) run uses.
const captureMetadataFields = "#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width}," +
	"#{history_size},#{mouse_any_flag},#{pane_current_command},#{pane_title}"

// captureCompareMetadataFields adds the three fields the shadow comparison
// needs — alternate-screen state, the SGR mouse encoding flag, and the client's
// discarded-byte counter — to the same single display-message. Adding them to
// the existing query rather than issuing a fourth command is deliberate: the
// decision gate counts commands per output burst, and a diagnostic that
// inflated that count would corrupt the measurement it exists to produce.
//
// pane_current_command and pane_title stay last so the SplitN limit still
// preserves commas inside a pane title.
const captureCompareMetadataFields = "#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width}," +
	"#{history_size},#{mouse_any_flag},#{alternate_on},#{mouse_sgr_flag},#{client_discarded}," +
	"#{pane_current_command},#{pane_title}"

func buildControlCaptureCommands(pane string, scrollback int) (metadata, capture string, err error) {
	return buildControlCaptureCommandsLayout(pane, scrollback, ScreenCompareEnabled())
}

func buildControlCaptureCommandsLayout(pane string, scrollback int, extended bool) (metadata, capture string, err error) {
	if !controlPanePattern.MatchString(pane) {
		return "", "", fmt.Errorf("tmux control: invalid pane %q", pane)
	}
	if scrollback <= 0 {
		scrollback = DefaultScrollbackLines
	}
	fields := captureMetadataFields
	if extended {
		fields = captureCompareMetadataFields
	}
	metadata = "display-message -p -t " + pane + " '" + fields + "'"
	// -N keeps each row's trailing blanks; see CapturePaneOutput for why the
	// trimmed form is ambiguous.
	capture = "capture-pane -p -e -N -S -" + strconv.Itoa(scrollback) + " -t " + pane
	return metadata, capture, nil
}

// captureExtras is the shadow-comparison-only half of the capture metadata.
// Valid is false whenever the ordinary layout was used, in which case the
// comparison has no tmux-side authority for these fields and does not assert
// them.
type captureExtras struct {
	Valid     bool
	AltScreen bool
	MouseSGR  bool
	Discarded int64
}

func parseControlSnapshot(session, pane string, scrollback int, lines []string) (ControlSnapshot, error) {
	snapshot, _, err := parseControlSnapshotLayout(session, pane, scrollback, lines, false)
	return snapshot, err
}

// captureBaseFor is the absolute line number of the capture's first row.
//
// It is derived from the capture's own row count rather than from
// history_size - scrollback. A capture is the pane's scrollback followed by all
// paneHeight of its rows, trailing blanks included, so captureRows-paneHeight is
// exactly how much history the capture carried. The metadata is read by a
// separate write, so history_size can already be stale by the time the capture
// lands; subtracting the *requested* scrollback instead of the delivered rows
// makes the delivered base disagree with the delivered content, which is what
// left the cursor stranded rows above its line (td-d29821).
//
// Degenerate captures — no pane height, or fewer rows than the pane — carry no
// usable row count, so those fall back to the requested window.
func captureBaseFor(historySize, scrollback, captureRows, paneHeight int) int {
	if paneHeight <= 0 || captureRows < paneHeight {
		return max(historySize-scrollback, 0)
	}
	return max(historySize-(captureRows-paneHeight), 0)
}

func parseControlSnapshotLayout(session, pane string, scrollback int, lines []string, extended bool) (ControlSnapshot, captureExtras, error) {
	var extras captureExtras
	if len(lines) == 0 {
		return ControlSnapshot{}, extras, errors.New("tmux control capture: missing cursor metadata")
	}
	// SplitN preserves commas in pane titles. pane_current_command is a tmux
	// command name rather than arbitrary terminal content and cannot contain a
	// comma in ordinary tmux output.
	limit := 9
	if extended {
		limit = 12
	}
	parts := strings.SplitN(strings.TrimSpace(lines[0]), ",", limit)
	// Fields past the sixth are optional so a metadata line produced before they
	// were added still parses.
	if len(parts) < 6 {
		return ControlSnapshot{}, extras, fmt.Errorf("tmux control capture: invalid cursor metadata %q", lines[0])
	}
	col, errCol := strconv.Atoi(parts[0])
	row, errRow := strconv.Atoi(parts[1])
	height, errHeight := strconv.Atoi(parts[3])
	width, errWidth := strconv.Atoi(parts[4])
	historySize, errHistory := strconv.Atoi(parts[5])
	if errCol != nil || errRow != nil || errHeight != nil || errWidth != nil ||
		errHistory != nil || historySize < 0 {
		return ControlSnapshot{}, extras, fmt.Errorf("tmux control capture: invalid cursor metadata %q", lines[0])
	}
	if scrollback <= 0 {
		scrollback = DefaultScrollbackLines
	}
	mouseReporting := len(parts) >= 7 && parts[6] != "0" && parts[6] != ""
	commandIndex, titleIndex := 7, 8
	if extended {
		commandIndex, titleIndex = 10, 11
		if len(parts) >= 10 {
			extras.Valid = true
			extras.AltScreen = parts[7] != "" && parts[7] != "0"
			extras.MouseSGR = parts[8] != "" && parts[8] != "0"
			// client_discarded is empty outside a control client; zero, not a
			// parse failure.
			if parts[9] != "" {
				if value, err := strconv.ParseInt(parts[9], 10, 64); err == nil && value >= 0 {
					extras.Discarded = value
				}
			}
		}
	}
	currentCommand := ""
	paneTitle := ""
	if len(parts) > commandIndex {
		currentCommand = parts[commandIndex]
	}
	if len(parts) > titleIndex {
		paneTitle = parts[titleIndex]
	}
	captureRows := len(lines) - 1
	paneRows := min(max(height, 0), captureRows)
	return ControlSnapshot{
		Session:        session,
		Pane:           pane,
		Output:         strings.Join(lines[1:], "\n"),
		HistorySize:    historySize,
		CaptureBase:    captureBaseFor(historySize, scrollback, captureRows, height),
		HistoryRows:    captureRows - paneRows,
		PaneRows:       paneRows,
		HasHistory:     true,
		CursorRow:      row,
		CursorCol:      col,
		CursorVisible:  parts[2] != "0",
		PaneHeight:     height,
		PaneWidth:      width,
		MouseReporting: mouseReporting,
		PaneTitle:      paneTitle,
		CurrentCommand: currentCommand,
	}, extras, nil
}
