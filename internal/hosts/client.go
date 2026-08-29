package hosts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
)

// State is a host's health, as a row in the Sessions browser shows it.
//
// Every value below is something a user can act on, and each has exactly one
// fix. That is the whole design rule: "offline" with no reason is a state that
// makes someone go and investigate, which is the work this feature is supposed
// to remove.
type State string

const (
	// StateConnecting is the first attempt, before anything is known. It is
	// distinct from unreachable because a host that is merely slow must not be
	// reported as broken.
	StateConnecting State = "connecting"
	// StateOnline means the stream is live and the data is current.
	StateOnline State = "online"
	// StateStale means the connection is up but no snapshot has arrived
	// recently. The rows are still shown, marked stale, because last-known
	// state is more useful than a blank host — as long as it says so.
	StateStale State = "stale"
	// StateUnreachable means ssh could not connect.
	StateUnreachable State = "unreachable"
	// StateNoSidecar means ssh connected but no sidecar binary ran.
	StateNoSidecar State = "no-sidecar"
	// StateProtocol means both ends ran but speak different protocol versions.
	StateProtocol State = "protocol-mismatch"
	// StateNoTmux means sidecar is there but tmux is not, so the host has no
	// sessions to observe.
	StateNoTmux State = "no-tmux"
	// StateNotProtocol means something came back that is not the protocol —
	// almost always a login shell writing to stdout.
	StateNotProtocol State = "not-protocol"
	// StateDisabled is a registered host the user has switched off.
	StateDisabled State = "disabled"
)

// Healthy reports whether rows from this state should be trusted as current.
func (s State) Healthy() bool { return s == StateOnline }

// Shows reports whether rows should be displayed at all. A stale host still
// shows its last-known rows; a host that never connected has none to show.
func (s State) Shows() bool { return s == StateOnline || s == StateStale }

// Fix names what to do about a state, in the imperative. Empty for the states
// that need nothing done.
func (s State) Fix() string {
	switch s {
	case StateUnreachable:
		return "check the machine is on and `ssh <target>` works from here"
	case StateNoSidecar:
		return "install Sidecar on that machine, or set its `binary` path"
	case StateProtocol:
		return "update Sidecar on whichever machine is older"
	case StateNoTmux:
		return "install tmux on that machine"
	case StateNotProtocol:
		return "that machine's login shell prints to stdout; send it to stderr or guard it with a non-interactive check"
	case StateStale:
		return "the connection is up but quiet; it will recover on its own, or remove the host to stop trying"
	case StateDisabled:
		return "set `disabled: false` for this host to connect to it"
	default:
		return ""
	}
}

// Health is everything a row needs to render a host's condition.
type Health struct {
	State  State
	Detail string
	// Since is when the current state began, so a row can say how long a host
	// has been unreachable rather than only that it is.
	Since time.Time
	// Hello is the last successful handshake, retained across a drop so a
	// disconnected host can still say which Sidecar it was running.
	Hello *hostproto.Hello
	// Attempts counts consecutive failed connections. It drives the backoff
	// and tells a reader whether this is a blip or a habit.
	Attempts int
}

// Fix is the actionable line for this health.
func (h Health) Fix() string { return h.State.Fix() }

// Update is one change to a host's observable state.
type Update struct {
	HostID   string
	Health   Health
	Snapshot *hostproto.Snapshot
}

// Conn is one open connection to a host's serve process.
type Conn struct {
	Stdout io.Reader
	// Stderr is read for diagnosis when the stream fails. It is a function
	// rather than a reader because it is only consulted after the fact.
	Stderr func() string
	Close  func()
}

// Dialer opens a connection to a host. Injectable so the client's reconnect,
// backoff, event application and health transitions can all be tested without
// ssh, a network, or a second machine.
type Dialer func(ctx context.Context) (*Conn, error)

// Client keeps one host's serve stream alive and exposes the current picture.
//
// It owns the reconnect loop, so callers never see a connection — they see a
// host that is online or not, with rows that are current or stale. That
// division is deliberate: "degrade the host, not the app" is only achievable
// if the thing that can fail is entirely behind this boundary.
type Client struct {
	host    Host
	dial    Dialer
	now     func() time.Time
	updates chan Update

	// StaleAfter bounds how long a connected host may go without a snapshot
	// before its rows are marked stale. It must exceed the serve loop's
	// slowest cadence (30s idle) with room for a slow link, or an idle host
	// would flap between online and stale.
	staleAfter time.Duration
	// backoff bounds are for the reconnect loop.
	minBackoff, maxBackoff time.Duration

	mu       sync.RWMutex
	health   Health
	snapshot *hostproto.Snapshot
	lastData time.Time

	closeOnce sync.Once
	done      chan struct{}

	// controlDir is where this host's ssh ControlMaster socket lives, shared
	// by the serve stream and every pane channel.
	controlDir string
}

// Client defaults. StaleAfter is deliberately generous: the serve loop drops
// to a 30-second cadence on a fully idle host, and marking such a host stale
// would be wrong — nothing is wrong with it.
const (
	DefaultStaleAfter = 90 * time.Second
	DefaultMinBackoff = 2 * time.Second
	DefaultMaxBackoff = 60 * time.Second
)

// ClientOptions configures a Client. Every field is optional.
type ClientOptions struct {
	Dial       Dialer
	Now        func() time.Time
	StaleAfter time.Duration
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// ControlDir is where the ssh ControlMaster socket lives when the default
	// dialer is used.
	ControlDir string
}

// NewClient builds a client for one host. It does not connect; call Run.
func NewClient(host Host, opts ClientOptions) *Client {
	client := &Client{
		host:       host,
		dial:       opts.Dial,
		now:        opts.Now,
		updates:    make(chan Update, 16),
		staleAfter: opts.StaleAfter,
		minBackoff: opts.MinBackoff,
		maxBackoff: opts.MaxBackoff,
		done:       make(chan struct{}),
	}
	if client.now == nil {
		client.now = time.Now
	}
	if client.staleAfter <= 0 {
		client.staleAfter = DefaultStaleAfter
	}
	if client.minBackoff <= 0 {
		client.minBackoff = DefaultMinBackoff
	}
	if client.maxBackoff <= 0 {
		client.maxBackoff = DefaultMaxBackoff
	}
	client.controlDir = opts.ControlDir
	if client.controlDir == "" {
		if dir, err := os.MkdirTemp("", "sidecar-host-"); err == nil {
			client.controlDir = dir
		}
	}
	if client.dial == nil {
		client.dial = sshDialer(host, client.controlDir)
	}
	client.health = Health{State: StateConnecting, Since: client.now()}
	return client
}

// Updates yields a value whenever the host's health or data changes.
//
// The channel is buffered and lossy by design: a consumer that falls behind
// gets the newest state on its next read, not a backlog of superseded ones.
// Nothing downstream benefits from replaying a health transition that has
// already been overtaken.
func (c *Client) Updates() <-chan Update { return c.updates }

// Health returns the current health.
func (c *Client) Health() Health {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// Snapshot returns the most recent snapshot, and whether there is one.
// A copy: callers render from it on another goroutine.
func (c *Client) Snapshot() (hostproto.Snapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.snapshot == nil {
		return hostproto.Snapshot{}, false
	}
	return *c.snapshot, true
}

// Host returns the host this client serves.
func (c *Client) Host() Host { return c.host }

// ControlCommand builds the ssh invocation that carries one remote tmux
// session's control channel — channel 1, the pane bytes.
//
// It rides the same multiplexed master the serve stream uses, so opening a
// pane costs a round trip rather than a connection. Returning an *exec.Cmd
// rather than an opened channel keeps process lifetime with the caller, which
// is what tty.ControlSpawner expects.
func (c *Client) ControlCommand(ctx context.Context, session string) *exec.Cmd {
	transport, err := NewTransport(c.host, c.controlDir)
	if err != nil {
		return nil
	}
	return transport.Command(ctx, transport.ControlCommand(session))
}

// Close stops the client. Safe to call more than once.
func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.done) })
}

// Run drives connect, consume, and reconnect until ctx is cancelled or Close
// is called. It returns only when stopping, and never returns an error: a host
// that cannot be reached is a state to render, not a failure to propagate.
func (c *Client) Run(ctx context.Context) {
	defer close(c.updates)

	attempts := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
		}

		state, detail := c.session(ctx)
		if state == StateOnline {
			// A connection that carried data and then ended is an ordinary
			// disconnect, not a fault: reset the backoff so a long-lived host
			// that drops once reconnects immediately.
			attempts = 0
			state, detail = StateUnreachable, "the connection to the host ended"
		} else {
			attempts++
		}
		c.setHealth(state, detail, attempts)

		wait := c.backoff(attempts)
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-time.After(wait):
		}
	}
}

// backoff grows the retry interval with consecutive failures, capped. A host
// that is off for the weekend must not be probed every two seconds for two
// days.
func (c *Client) backoff(attempts int) time.Duration {
	wait := c.minBackoff
	for i := 1; i < attempts && wait < c.maxBackoff; i++ {
		wait *= 2
	}
	if wait > c.maxBackoff {
		wait = c.maxBackoff
	}
	return wait
}

// session runs one connection to exhaustion and reports how it ended.
func (c *Client) session(ctx context.Context) (State, string) {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Close must interrupt a blocked read, not merely be noticed afterwards.
	go func() {
		select {
		case <-c.done:
			cancel()
		case <-sessionCtx.Done():
		}
	}()

	conn, err := c.dial(sessionCtx)
	if err != nil {
		return StateUnreachable, err.Error()
	}
	defer conn.Close()

	stderr := func() string {
		if conn.Stderr == nil {
			return ""
		}
		return conn.Stderr()
	}

	decoder := hostproto.NewDecoder(conn.Stdout)
	sawData := false

	for {
		msg, err := decoder.Next()
		if err != nil {
			if sessionCtx.Err() != nil {
				return StateOnline, ""
			}
			if sawData {
				// Data arrived and then the stream ended. Report it as a
				// clean session so the caller resets its backoff.
				return StateOnline, ""
			}
			return classifyStreamFailure(err, stderr())
		}

		switch msg.Kind {
		case hostproto.KindHello:
			if !hostproto.Compatible(msg.Proto) {
				return StateProtocol, hostproto.IncompatibleMessage(c.host.ID, msg.Proto)
			}
			if msg.Hello != nil && !msg.Hello.TmuxPresent {
				return StateNoTmux, "Sidecar is installed there but tmux is not, so the machine has no sessions to show"
			}
			c.applyHello(msg.Hello)
		case hostproto.KindSnapshot:
			sawData = true
			c.applySnapshot(msg.Snapshot)
		case hostproto.KindEvent:
			sawData = true
			c.applyEvent(msg.Event)
		case hostproto.KindError:
			if msg.Error != nil && msg.Error.Fatal {
				return stateForErrorCode(msg.Error.Code), msg.Error.Message
			}
		}
	}
}

func stateForErrorCode(code string) State {
	switch code {
	case hostproto.ErrNoTmux:
		return StateNoTmux
	case hostproto.ErrNoConfig:
		return StateNotProtocol
	default:
		return StateUnreachable
	}
}

// classifyStreamFailure turns a dead stream into a named state.
//
// The distinctions are the ones observed against a real host during the Phase
// 0 spike, not imagined: a non-login ssh shell really does report a
// Homebrew-installed binary as "command not found", and a login profile with
// an interactive preexec hook really does put escape sequences on the same
// pipe as the protocol.
func classifyStreamFailure(err error, stderr string) (State, string) {
	detail := strings.TrimSpace(stderr)
	lowered := strings.ToLower(detail)
	switch {
	case strings.Contains(lowered, "command not found"),
		strings.Contains(lowered, "sidecar: no such file"),
		strings.Contains(lowered, "not found") && strings.Contains(lowered, "sidecar"):
		return StateNoSidecar, detail
	case strings.Contains(lowered, "permission denied"),
		strings.Contains(lowered, "could not resolve hostname"),
		strings.Contains(lowered, "connection refused"),
		strings.Contains(lowered, "connection timed out"),
		strings.Contains(lowered, "operation timed out"),
		strings.Contains(lowered, "host key verification failed"),
		strings.Contains(lowered, "no route to host"):
		return StateUnreachable, detail
	case err != nil && strings.Contains(err.Error(), "not the protocol"):
		return StateNotProtocol, err.Error()
	case detail != "":
		return StateUnreachable, detail
	case errors.Is(err, io.EOF):
		return StateUnreachable, "the host closed the connection without sending anything"
	default:
		return StateUnreachable, errText(err)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (c *Client) applyHello(hello *hostproto.Hello) {
	c.mu.Lock()
	c.health.Hello = hello
	c.mu.Unlock()
}

func (c *Client) applySnapshot(snapshot *hostproto.Snapshot) {
	if snapshot == nil {
		return
	}
	copied := *snapshot
	c.mu.Lock()
	c.snapshot = &copied
	c.lastData = c.now()
	c.health.State, c.health.Detail, c.health.Attempts = StateOnline, "", 0
	c.health.Since = c.lastData
	health := c.health
	c.mu.Unlock()
	c.publish(Update{HostID: c.host.ID, Health: health, Snapshot: &copied})
}

// applyEvent folds a delta into the retained snapshot, so a caller always
// reads a whole picture rather than having to replay events itself.
//
// An event for a row the snapshot does not have is not an error: it means the
// viewer connected mid-cycle, and the next periodic snapshot will resync. It
// is dropped rather than synthesised, because a half-populated row invented
// here would be indistinguishable from one the host actually reported.
func (c *Client) applyEvent(event *hostproto.Event) {
	if event == nil {
		return
	}
	c.mu.Lock()
	if c.snapshot == nil {
		c.mu.Unlock()
		return
	}
	updated := *c.snapshot
	updated.Projects = append([]hostproto.Project(nil), c.snapshot.Projects...)

	switch event.Kind {
	case hostproto.EventServer:
		updated.ServerIncarnation = event.ServerIncarnation
	case hostproto.EventDisappear:
		for i := range updated.Projects {
			updated.Projects[i] = removeItem(updated.Projects[i], event.ItemID)
		}
	case hostproto.EventAppear, hostproto.EventStatus:
		if event.Item != nil {
			updated.Projects = upsertItem(updated.Projects, *event.Item)
		}
	}
	updated.Generation = event.Generation
	c.snapshot = &updated
	c.lastData = c.now()
	c.health.State, c.health.Detail, c.health.Attempts = StateOnline, "", 0
	health := c.health
	c.mu.Unlock()
	c.publish(Update{HostID: c.host.ID, Health: health, Snapshot: &updated})
}

func removeItem(project hostproto.Project, id string) hostproto.Project {
	items := make([]hostproto.Item, 0, len(project.Items))
	for _, item := range project.Items {
		if item.ID != id {
			items = append(items, item)
		}
	}
	project.Items = items
	return project
}

func upsertItem(projects []hostproto.Project, item hostproto.Item) []hostproto.Project {
	for i := range projects {
		if projects[i].Key != item.ProjectKey {
			continue
		}
		items := append([]hostproto.Item(nil), projects[i].Items...)
		for j := range items {
			if items[j].ID == item.ID {
				items[j] = item
				projects[i].Items = items
				return projects
			}
		}
		projects[i].Items = append(items, item)
		return projects
	}
	// A row for a project the snapshot does not carry. Dropped: see applyEvent.
	return projects
}

func (c *Client) setHealth(state State, detail string, attempts int) {
	c.mu.Lock()
	if c.health.State != state || c.health.Detail != detail {
		c.health.Since = c.now()
	}
	c.health.State, c.health.Detail, c.health.Attempts = state, detail, attempts
	health := c.health
	snapshot := c.snapshot
	c.mu.Unlock()
	c.publish(Update{HostID: c.host.ID, Health: health, Snapshot: snapshot})
}

// MarkStaleIfQuiet moves a connected host to stale when no data has arrived
// within the window, and reports whether anything changed.
//
// It is a method the owner calls on a tick rather than an internal timer,
// because the owner is a Bubble Tea model that already has one: a second timer
// here would deliver transitions on a goroutine the model does not control.
func (c *Client) MarkStaleIfQuiet() bool {
	c.mu.Lock()
	if c.health.State != StateOnline || c.lastData.IsZero() ||
		c.now().Sub(c.lastData) < c.staleAfter {
		c.mu.Unlock()
		return false
	}
	c.health.State = StateStale
	c.health.Detail = fmt.Sprintf("no update for %s", c.now().Sub(c.lastData).Round(time.Second))
	c.health.Since = c.now()
	health := c.health
	snapshot := c.snapshot
	c.mu.Unlock()
	c.publish(Update{HostID: c.host.ID, Health: health, Snapshot: snapshot})
	return true
}

// publish sends an update without ever blocking the reader loop. A consumer
// that is behind will read the newest state next time; blocking here would
// stall the stream and turn a slow renderer into a stale host.
func (c *Client) publish(update Update) {
	select {
	case c.updates <- update:
	default:
	}
}

// sshDialer is the production dialer: one ssh child running the host's serve
// command over the multiplexed master connection.
func sshDialer(host Host, controlDir string) Dialer {
	return func(ctx context.Context) (*Conn, error) {
		dir := controlDir
		if dir == "" {
			var err error
			dir, err = os.MkdirTemp("", "sidecar-host-")
			if err != nil {
				return nil, fmt.Errorf("control dir: %w", err)
			}
		}
		transport, err := NewTransport(host, dir)
		if err != nil {
			return nil, err
		}
		cmd := transport.Command(ctx, transport.ServeCommand())
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		var stderr syncBuffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &Conn{
			Stdout: stdout,
			Stderr: stderr.String,
			Close: func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				_ = cmd.Wait()
				// The master connection is left up on purpose: the next
				// reconnect rides it instead of re-authenticating, and
				// ControlPersist retires it if nobody comes back.
			},
		}, nil
	}
}

// syncBuffer collects a child's stderr for later diagnosis. It is synchronised
// because os/exec writes to it on its own goroutine while the reader loop may
// be reading it to classify a failure.
type syncBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Bounded: a host that spews to stderr must not grow this without limit.
	if len(b.data) < 8<<10 {
		b.data = append(b.data, p...)
	}
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
