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
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	PaneHeight    int
	PaneWidth     int
	Generation    uint64
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
	appFocused bool
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
		appFocused: true,
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
		request.Scrollback = 600
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
	m.mu.Lock()
	if m.appFocused == focused || m.stopped {
		m.mu.Unlock()
		return
	}
	m.appFocused = focused
	clients := make([]*sessionControlClient, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.mu.Unlock()
	for _, client := range clients {
		client.setAppFocused(focused)
	}
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
	client := newSessionControlClient(m, session, channel, m.coalesce, m.appFocused)
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
	panes      map[string]*paneCaptureState
	appFocused bool
	closed     bool
	closeOnce  sync.Once
}

func newSessionControlClient(manager *ControlManager, session string, channel controlChannel, coalesce time.Duration, appFocused bool) *sessionControlClient {
	client := &sessionControlClient{
		manager:    manager,
		session:    session,
		channel:    channel,
		coalesce:   coalesce,
		subs:       make(map[uint64]sessionSubscriber),
		panes:      make(map[string]*paneCaptureState),
		appFocused: appFocused,
	}
	go client.run()
	// Feature-detect flow control: older tmux versions return an error, which is
	// harmless; supported versions emit extended-output and pause notifications.
	_ = channel.Send("refresh-client -f pause-after=5", func(controlResponse) {})
	return client
}

func (c *sessionControlClient) run() {
	for {
		select {
		case event := <-c.channel.Events():
			c.handleEvent(event)
		case err := <-c.channel.Done():
			if err == nil {
				err = fmt.Errorf("tmux control exited")
			}
			c.manager.clientFailed(c, err)
			return
		}
	}
}

func (c *sessionControlClient) handleEvent(event controlEvent) {
	switch event.Kind {
	case controlEventOutput:
		c.markDirty(event.Pane)
	case controlEventLayout:
		if event.Pane != "" {
			c.markDirty(event.Pane)
		} else {
			c.markAllDirty()
		}
	case controlEventPause:
		if controlPanePattern.MatchString(event.Pane) {
			_ = c.channel.Send("refresh-client -A "+event.Pane+":continue", func(controlResponse) {})
		}
	case controlEventExit:
		c.manager.clientFailed(c, fmt.Errorf("tmux control exit notification"))
	}
}

func (c *sessionControlClient) add(sub managerControlSubscription) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		callFallback(sub.request.OnFallback, fmt.Errorf("tmux control client closed"))
		return
	}
	c.subs[sub.id] = sessionSubscriber{id: sub.id, generation: sub.generation, request: sub.request}
	c.ensurePaneLocked(sub.request.Pane).dirty = true
	c.mu.Unlock()
	c.configureSize(sub.request.Width, sub.request.Height)
	c.scheduleIfEligible(sub.request.Pane)
}

func (c *sessionControlClient) remove(id uint64) {
	c.mu.Lock()
	delete(c.subs, id)
	c.mu.Unlock()
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

func (c *sessionControlClient) setAppFocused(focused bool) {
	c.mu.Lock()
	c.appFocused = focused
	var panes []string
	if focused {
		for pane := range c.panes {
			panes = append(panes, pane)
		}
	}
	c.mu.Unlock()
	for _, pane := range panes {
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
	scrollback := 600
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
		c.captureFinished(pane, controlResponse{Err: err})
		return
	}
	var responseMu sync.Mutex
	var metadata controlResponse
	var finished sync.Once
	finish := func(response controlResponse) {
		finished.Do(func() { c.captureFinished(pane, response) })
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

func (c *sessionControlClient) captureFinished(pane string, response controlResponse) {
	if response.Err != nil {
		c.manager.clientFailed(c, response.Err)
		return
	}
	snapshot, err := parseControlSnapshot(c.session, pane, response.Lines)
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
		if sub.request.OnSnapshot != nil {
			copy := snapshot
			copy.Generation = sub.generation
			sub.request.OnSnapshot(copy)
		}
	}
	if again {
		c.scheduleIfEligible(pane)
	}
}

func (c *sessionControlClient) paneEligibleLocked(pane string) bool {
	if !c.appFocused {
		return false
	}
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
		for _, pane := range c.panes {
			if pane.timer != nil {
				pane.timer.Stop()
				pane.timer = nil
			}
		}
		c.mu.Unlock()
		_ = c.channel.Close()
	})
}

func buildControlCaptureCommands(pane string, scrollback int) (metadata, capture string, err error) {
	if !controlPanePattern.MatchString(pane) {
		return "", "", fmt.Errorf("tmux control: invalid pane %q", pane)
	}
	if scrollback <= 0 {
		scrollback = 600
	}
	metadata = "display-message -p -t " + pane +
		" '#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width}'"
	capture = "capture-pane -p -e -S -" + strconv.Itoa(scrollback) + " -t " + pane
	return metadata, capture, nil
}

func parseControlSnapshot(session, pane string, lines []string) (ControlSnapshot, error) {
	if len(lines) == 0 {
		return ControlSnapshot{}, errors.New("tmux control capture: missing cursor metadata")
	}
	parts := strings.Split(strings.TrimSpace(lines[0]), ",")
	if len(parts) != 5 {
		return ControlSnapshot{}, fmt.Errorf("tmux control capture: invalid cursor metadata %q", lines[0])
	}
	col, errCol := strconv.Atoi(parts[0])
	row, errRow := strconv.Atoi(parts[1])
	height, errHeight := strconv.Atoi(parts[3])
	width, errWidth := strconv.Atoi(parts[4])
	if errCol != nil || errRow != nil || errHeight != nil || errWidth != nil {
		return ControlSnapshot{}, fmt.Errorf("tmux control capture: invalid cursor metadata %q", lines[0])
	}
	return ControlSnapshot{
		Session:       session,
		Pane:          pane,
		Output:        strings.Join(lines[1:], "\n"),
		CursorRow:     row,
		CursorCol:     col,
		CursorVisible: parts[2] != "0",
		PaneHeight:    height,
		PaneWidth:     width,
	}, nil
}
