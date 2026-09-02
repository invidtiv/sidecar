// Package hostproto defines the wire contract between a Sidecar viewer and a
// `sidecar host serve` process running on a remote machine.
//
// The transport is newline-delimited JSON over an SSH stdio pipe. Both ends of
// this protocol are this repository, which is the whole reason it can be small:
// there is no third-party pace to track, the protocol integer is bumped here,
// and errors are structured here.
//
// Two rules shape everything below.
//
// The protocol integer is checked before anything else is trusted. A viewer
// reads Hello.Proto, compares it against Version, and renders an actionable
// row state ("sidecar too old on <host>: proto 1, need 2") rather than
// attempting to parse a stream it does not understand. The version *string* is
// display-only and must never gate behaviour.
//
// Every message is a delta or a full snapshot of read-only observation. There
// is no request direction in v1 at all — not a disabled one, an absent one.
// Serve is read-only through Phase B by construction rather than by flag, so
// the type that would carry a mutation does not exist yet.
package hostproto

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Version is the protocol integer. Bump it for any change a viewer of the
// previous version could misread; adding an optional field a decoder ignores
// is not such a change.
//
// v2 added KindNotify. It is a bump rather than an ignorable addition on
// purpose: a v1 viewer would silently discard every notification a v2 host
// sends, and a user who enabled managed-host notifications and heard nothing
// would have no way to learn why. Failing the handshake with the existing
// actionable message says which machine to update.
const Version = 2

// MaxLineBytes bounds one encoded message. A remote host that somehow produces
// a larger line is a bug or an attack, and either way the viewer must fail
// with a named error rather than buffering without limit. Preview text is the
// only unbounded-looking field and it is capped far below this at the source.
const MaxLineBytes = 4 << 20

// Kind names a message type. It is a string rather than an integer so a
// human reading the raw JSONL stream — which is how this protocol will
// actually be debugged — can see what is happening.
type Kind string

const (
	KindHello    Kind = "hello"
	KindSnapshot Kind = "snapshot"
	KindEvent    Kind = "event"
	KindError    Kind = "error"
	// KindNotify is one settled agent transition the host considers worth a
	// notification. It is deliberately separate from KindEvent: an event is a
	// row's state changing, which a viewer folds into its snapshot, while this
	// is a thing that happened once and is gone. Folding one into state, or
	// deriving one from state, is exactly how a reconnect would replay an old
	// prompt onto someone's desktop.
	KindNotify Kind = "notify"
	// KindUIRequest is one host-side open/layout request file the connected
	// viewer may apply. It is not a protocol bump: a v2 decoder that does not
	// know the kind still parses the rest of the stream, and an old viewer
	// never writes a presence file, so the host CLI fast-refuses rather than
	// claiming success. It is not KindNotify: that payload is a toast the user
	// can ignore, while this is a command the agent is blocked on.
	KindUIRequest Kind = "uirequest"
)

// Message is the envelope. Exactly one of the payload pointers is set, chosen
// by Kind.
type Message struct {
	Proto int       `json:"proto"`
	Kind  Kind      `json:"kind"`
	Seq   uint64    `json:"seq"`
	At    time.Time `json:"at"`

	Hello     *Hello       `json:"hello,omitempty"`
	Snapshot  *Snapshot    `json:"snapshot,omitempty"`
	Event     *Event       `json:"event,omitempty"`
	Error     *Error       `json:"error,omitempty"`
	Notify    *NotifyEvent `json:"notify,omitempty"`
	UIRequest *UIRequest   `json:"uiRequest,omitempty"`
}

// Hello is the first message on every connection. A viewer that does not
// receive one before its handshake deadline treats the host as unreachable.
type Hello struct {
	// Proto duplicates Message.Proto so a viewer that logs only the payload
	// still records the number that decided compatibility.
	Proto int `json:"proto"`
	// Version is the remote sidecar's build identity. Display only.
	Version string `json:"version"`

	Host string `json:"host"`
	OS   string `json:"os"`
	Arch string `json:"arch"`

	// TmuxPresent is false when no tmux binary resolved at all, which is a
	// different and more actionable state than "a server that is not running".
	TmuxPresent bool   `json:"tmuxPresent"`
	TmuxVersion string `json:"tmuxVersion,omitempty"`
	// ServerRunning distinguishes "tmux installed, nothing running" from
	// "tmux installed and serving sessions". Both are healthy; only the second
	// can produce workspaces.
	ServerRunning bool `json:"serverRunning"`

	// Projects is how many projects the remote config lists. A viewer shows it
	// before the first snapshot arrives so a slow host still says something
	// true immediately.
	Projects int `json:"projects"`

	Capabilities Capabilities `json:"capabilities"`
}

// Capabilities lets a viewer render honest confidence per host instead of
// assuming every host resolves agent identity as well as the local machine
// does. This answers the plan's open question in the affirmative: it is cheap,
// and without it a Linux host's degraded process identity is invisible.
type Capabilities struct {
	// ProcessIdentity is true when the host can disambiguate a shared-runtime
	// pane (node, bun, python) by argv0. It is false on platforms where
	// process_identity is a stub, and a viewer showing a `?` provider on such
	// a host is reporting a host limitation, not an unknown agent.
	ProcessIdentity bool `json:"processIdentity"`
	// IsolatedState is true when the serve process is running under
	// SIDECAR_ISOLATED_STATE. A proof run must see this; a real session must
	// not.
	IsolatedState bool `json:"isolatedState"`
	// StateDir is the resolved state root, echoed so an isolation failure is
	// visible in the transcript rather than inferred from behaviour.
	StateDir string `json:"stateDir,omitempty"`

	// Verbs is what the host's CLI accepts, for the arguments a viewer cannot
	// otherwise find out before sending them.
	Verbs VerbCapabilities `json:"verbs"`
}

// VerbCapabilities is what a host says it understands about the one-shot
// `sidecar <verb> --json` invocations a viewer makes of it.
//
// Deliberately minimal, and deliberately not a negotiation framework: one flat
// set of booleans, added to when a verb gains an option a viewer must know
// about before sending it. Anything more elaborate would be inventing a
// mechanism for a protocol whose two ends are both this repository.
//
// It is additive, so Version stays where it is: a decoder ignores unknown
// fields, and a viewer talking to a host that predates a field reads the zero
// value — which is the correct answer, "that host does not have it". Version
// strings cannot answer this. Dev builds carry git revisions rather than
// releases, so comparing them decides nothing.
type VerbCapabilities struct {
	// CreateShellAgent is `sidecar create shell --agent <family>`, which records
	// the agent family in the host's own shells.json as the shell is created, so
	// HasAgent() is true from that moment rather than from the first successful
	// screen identification. A host that advertises it also accepts --agent
	// alongside --run, which is how a viewer says it owns the launch.
	//
	// A host that predates the flag answers `unknown option "--agent"` and exits
	// 2, so a viewer that sent it unconditionally did not degrade — it broke
	// remote agent-shell creation outright against any machine updated later
	// than the viewer's. Mixed versions are the normal state: nobody updates two
	// machines at the same moment. A viewer that reads false here falls back to
	// creating the shell and then starting the agent with `shell send --run`,
	// which is what it did before the flag existed.
	CreateShellAgent bool `json:"createShellAgent,omitempty"`

	// ContentReadV1 is `sidecar content resolve|read --json`, the read-only
	// file contract a viewer uses to load Document panes from a host. A host
	// that predates the verbs is read as false; the viewer must refuse rather
	// than guess, and must not infer ordering from version strings.
	ContentReadV1 bool `json:"contentReadV1,omitempty"`

	// ContentTreeV1 is `sidecar content tree --json`, the read-only directory
	// listing a viewer's file tree is built from. A host that predates the verb
	// is read as false and the viewer refuses, naming the host, rather than
	// falling back to a tree of its own disk — a same-named checkout is a
	// different project, and showing it under a remote label is the failure the
	// remote-project work exists to prevent. Never inferred from a version
	// string.
	ContentTreeV1 bool `json:"contentTreeV1,omitempty"`

	// UIRequestRelayV1 is serve observing host `uirequest` files and announcing
	// them as KindUIRequest. A host that predates it is read as false; the
	// viewer must not expect announcements. Serve still does not apply the
	// request or write an ack.
	UIRequestRelayV1 bool `json:"uiRequestRelayV1,omitempty"`
}

// Snapshot is the complete observable state of the host at one instant. Serve
// emits one after the hello and again whenever a full collection cycle
// completes; Events carry the cheaper in-between transitions.
type Snapshot struct {
	Generation uint64    `json:"generation"`
	ObservedAt time.Time `json:"observedAt"`
	// ServerIncarnation changes when the remote tmux server restarts. A viewer
	// uses the transition to mark rows dead. It must never use it to delete
	// anything: the shells-wipe incident (td-8d18de) is what that rule is for.
	ServerIncarnation uint64    `json:"serverIncarnation"`
	Projects          []Project `json:"projects"`
}

// Project mirrors workspaceinventory.ProjectResult across the wire. Err is a
// string because an error value does not survive JSON and a viewer only ever
// displays it.
type Project struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Root  string `json:"root"`
	Err   string `json:"err,omitempty"`
	Items []Item `json:"items"`
}

// Item is one remote workspace row. It is deliberately a projection of
// workspaceinventory.Item rather than that type re-exported: the wire type has
// to stay stable across refactors of the collector's internals, and it carries
// two fields the local type has no reason to (HostID, Preview).
type Item struct {
	// ID is the collector's workspace ID. HostID scopes it: two hosts can
	// legitimately produce the same ID, and a viewer keying rows on ID alone
	// would collide them.
	ID     string `json:"id"`
	HostID string `json:"hostId"`

	ProjectKey  string `json:"projectKey"`
	ProjectName string `json:"projectName"`
	ProjectRoot string `json:"projectRoot"`

	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	TaskID string `json:"taskId,omitempty"`

	Provider string `json:"provider,omitempty"`
	PaneID   string `json:"paneId,omitempty"`
	// Session is the tmux session this row's pane lives in. The viewer needs
	// it to open a proxied control channel, and it is the honest name for what
	// workspaceinventory calls TmuxName — which the plan notes should stop
	// leaking upward locally too.
	Session string `json:"session,omitempty"`

	Live      bool `json:"live"`
	Ambiguous bool `json:"ambiguous"`
	IsMain    bool `json:"isMain,omitempty"`

	Agent      *Presentation `json:"agent,omitempty"`
	ObservedAt time.Time     `json:"observedAt"`

	// Preview is the capture text the status pass already took, so a viewer's
	// preview cell needs no control channel. Bounded at the source by the same
	// byte cap the local capture path uses.
	Preview string `json:"preview,omitempty"`
}

// Presentation mirrors agentstatus.Presentation. Serving the resolved
// presentation rather than the raw activity tracker is the design choice that
// makes remote status identical to local status: the same Resolve ran, on the
// host, over the host's own captures.
type Presentation struct {
	Lane       string    `json:"lane"`
	Icon       string    `json:"icon,omitempty"`
	Label      string    `json:"label,omitempty"`
	Attention  bool      `json:"attention,omitempty"`
	Evidence   string    `json:"evidence,omitempty"`
	ChangedAt  time.Time `json:"changedAt,omitzero"`
	CapturedAt time.Time `json:"capturedAt,omitzero"`
	Health     bool      `json:"health,omitempty"`
	Semantic   bool      `json:"semantic,omitempty"`
	Freshness  string    `json:"freshness,omitempty"`
	Inferred   bool      `json:"inferred,omitempty"`
}

// EventKind names what changed.
type EventKind string

const (
	// EventStatus is a lane or presentation transition on one existing row.
	EventStatus EventKind = "status"
	// EventAppear is a row the previous snapshot did not have.
	EventAppear EventKind = "appear"
	// EventDisappear is a row that is gone from the inventory. It is an
	// observation, never an instruction to delete remote state.
	EventDisappear EventKind = "disappear"
	// EventServer reports a tmux server incarnation change: the remote server
	// died and/or came back. Every row's liveness is suspect until the next
	// snapshot.
	EventServer EventKind = "server"
)

// Event is an incremental change between snapshots.
type Event struct {
	Kind       EventKind `json:"kind"`
	Generation uint64    `json:"generation"`
	ProjectKey string    `json:"projectKey,omitempty"`
	ItemID     string    `json:"itemId,omitempty"`

	// From and To are the lanes on a status transition, present so a viewer
	// can drive notifications without diffing snapshots itself.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`

	Item *Item `json:"item,omitempty"`

	// ServerIncarnation is set on EventServer.
	ServerIncarnation uint64 `json:"serverIncarnation,omitempty"`
}

// NotifyClass names the settled agent transition a notification is about. The
// vocabulary is the viewer's own transition classes, spelled here as plain
// strings so this package stays free of the notification model.
type NotifyClass string

const (
	NotifyWaiting NotifyClass = "waiting"
	NotifyDone    NotifyClass = "done"
	NotifyFailure NotifyClass = "failure"
)

// Notify field bounds. They are small on purpose. Everything here is going to
// be handed to a local desktop notification service, so the wire schema states
// what may cross and how much of it; a host that exceeds a bound is refused at
// decode rather than truncated, because a payload that does not fit the
// contract is not a payload to guess at.
const (
	MaxNotifyKeyBytes   = 128
	MaxNotifyTitleBytes = 200
	MaxNotifyBodyBytes  = 800
	// MaxNotifyOriginBytes is generous because a workspace path is a real path
	// on a real machine, and PATH_MAX is 1024 on macOS. It is a bound against
	// a hostile peer, not a budget anyone should have to fit inside.
	MaxNotifyOriginBytes = 1024
)

// NotifyOrigin is the stable remote identity of the workspace a notification
// is about, in the same vocabulary the viewer already keys remote rows on.
//
// It is deliberately the whole of what crosses about *where* the event
// happened. There is no pane ID, TTY path, environment, bundle identifier, or
// command: nothing here can select something for the local machine to run.
type NotifyOrigin struct {
	// ItemID is the remote collector's workspace ID, the same one Item.ID
	// carries, so a viewer can line an event up with the row it belongs to.
	ItemID     string `json:"itemId"`
	ProjectKey string `json:"projectKey,omitempty"`
	Session    string `json:"session,omitempty"`
	Path       string `json:"path,omitempty"`
}

func (o NotifyOrigin) empty() bool {
	return o.ItemID == "" && o.ProjectKey == "" && o.Session == "" && o.Path == ""
}

// NotifyEvent is one live remote transition, or the withdrawal of one.
//
// The host performs no delivery of its own: it does not claim, does not write
// a receipt, and never invokes a desktop or audio service. This message is the
// entire remote half of the feature.
type NotifyEvent struct {
	// Key identifies this transition independently of the connection that
	// carried it. See NotifyKey for how it is derived and why.
	Key string `json:"key,omitempty"`
	// OccurredAt is when the host settled the transition, not when it was
	// encoded. A viewer compares it against its own live-event window, so a
	// message that queued behind a slow link does not arrive as fresh news.
	OccurredAt time.Time `json:"occurredAt,omitzero"`

	Class    NotifyClass `json:"class,omitempty"`
	Source   string      `json:"source,omitempty"`
	Severity string      `json:"severity,omitempty"`
	Title    string      `json:"title,omitempty"`
	Body     string      `json:"body,omitempty"`
	Sticky   bool        `json:"sticky,omitempty"`

	Origin NotifyOrigin `json:"origin,omitzero"`

	// Withdraws names an earlier event key this message answers: the agent has
	// left the lane that was waiting for input. A withdrawal carries no text
	// and no origin — it is an answer to something the viewer already has, and
	// a message that both announced and withdrew would have two meanings.
	Withdraws string `json:"withdraws,omitempty"`

	// WithdrawsTransition retires whatever wait is outstanding for this event's
	// Origin and Class without naming an event key.
	//
	// It exists because a serve process cannot always name the key. The key is
	// a function of the occurrence time, and a process that reconnected or
	// restarted never saw the original event — but the viewer still holds the
	// sticky record, and a wait that outlives its answer is worse than no wait
	// at all. Origin and Class are both derivable from what the process is
	// observing right now, on either side, so this form survives a reconnect
	// where the key form cannot.
	//
	// It still carries no text: it can only retire a record, never create one.
	WithdrawsTransition bool `json:"withdrawsTransition,omitempty"`
}

// IsWithdrawal reports whether this message retires an earlier notification
// rather than announcing a new one.
func (e NotifyEvent) IsWithdrawal() bool { return e.Withdraws != "" || e.WithdrawsTransition }

// NotifyKeyResolution quantizes the occurrence time inside an event key.
//
// Two `sidecar host serve` processes watch one machine on independent poll
// clocks, so they cannot agree on the exact instant a transition settled. A
// key built from an exact time would therefore differ per connection, and two
// viewers of the same host would each store their own copy of one event —
// which is the duplicate this key exists to prevent. Rounding to the window
// the viewer's store already treats as one logical event makes them agree.
//
// A transition that straddles a bucket boundary still produces two keys. That
// case is caught by the second rule rather than this one: the viewer's store
// collapses two posts carrying the same logical dedupe key inside the same
// window. Widening this constant to close it would instead swallow an agent's
// genuine next turn, which is worse.
const NotifyKeyResolution = 15 * time.Second

// NotifyKey derives the stable identity of one remote transition from what
// both ends can agree on: where it happened, what kind of change it was, and
// roughly when.
//
// It must never involve Message.Seq or Snapshot.Generation. Those are facts
// about one connection, and a key built from them would make every reconnect —
// and every second viewer — a new event.
func NotifyKey(origin NotifyOrigin, class NotifyClass, occurredAt time.Time) string {
	bucket := occurredAt.UTC().Truncate(NotifyKeyResolution).UnixNano()
	raw := strings.Join([]string{
		origin.ItemID, origin.ProjectKey, origin.Session, origin.Path,
		string(class), strconv.FormatInt(bucket, 10),
	}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

// ErrInvalid marks a line that parsed as JSON but violates the protocol: a
// bound exceeded, a required field missing, a message that means two things at
// once. It is a named error so a viewer can report "that machine is not
// speaking this protocol correctly" rather than a decoder's phrasing, and so
// the failure is closed — nothing is stored or delivered from a message the
// contract does not cover.
var ErrInvalid = errors.New("hostproto: invalid message")

// Validate checks a decoded message against the parts of the contract JSON
// cannot express. It runs on every decoded line, on both ends.
func (m Message) Validate() error {
	switch m.Kind {
	case KindNotify:
		if m.Notify == nil {
			return fmt.Errorf("%w: notify message carried no payload", ErrInvalid)
		}
		return m.Notify.validate()
	case KindUIRequest:
		if m.UIRequest == nil {
			return fmt.Errorf("%w: ui request message carried no payload", ErrInvalid)
		}
		return m.UIRequest.validate()
	}
	return nil
}

func (e NotifyEvent) validate() error {
	if e.IsWithdrawal() {
		if len(e.Withdraws) > MaxNotifyKeyBytes {
			return fmt.Errorf("%w: withdrawal key exceeds %d bytes", ErrInvalid, MaxNotifyKeyBytes)
		}
		// The two forms are alternatives, not a spectrum. One names a key, the
		// other names an origin and class; a message carrying both would leave
		// a viewer to choose which identity it meant.
		if e.Withdraws != "" && e.WithdrawsTransition {
			return fmt.Errorf("%w: a withdrawal names a key or a transition, not both", ErrInvalid)
		}
		// A withdrawal that also carried text would be two messages in one, and
		// a viewer applying both would post the thing it was told to retire.
		// Every field is checked, not only the ones the viewer reads today, so
		// "a withdrawal carries no notification content" stays true of the wire
		// rather than only of the current reader.
		if e.Key != "" || e.Title != "" || e.Body != "" || e.Source != "" || e.Severity != "" || e.Sticky {
			return fmt.Errorf("%w: a withdrawal carries no notification content", ErrInvalid)
		}
		if e.WithdrawsTransition {
			// This form is identified by what it names, so those two fields are
			// required here exactly where the key form forbids them.
			if e.Origin.empty() {
				return fmt.Errorf("%w: a transition withdrawal has no origin", ErrInvalid)
			}
			switch e.Class {
			case NotifyWaiting:
			default:
				return fmt.Errorf("%w: only a wait can be withdrawn by transition", ErrInvalid)
			}
			return e.Origin.validateBounds()
		}
		if e.Class != "" || !e.Origin.empty() || !e.OccurredAt.IsZero() {
			return fmt.Errorf("%w: a withdrawal by key carries no transition identity", ErrInvalid)
		}
		return nil
	}
	switch {
	case e.Key == "":
		return fmt.Errorf("%w: notify event has no key", ErrInvalid)
	case len(e.Key) > MaxNotifyKeyBytes:
		return fmt.Errorf("%w: notify key exceeds %d bytes", ErrInvalid, MaxNotifyKeyBytes)
	case e.Title == "":
		return fmt.Errorf("%w: notify event has no title", ErrInvalid)
	case len(e.Title) > MaxNotifyTitleBytes:
		return fmt.Errorf("%w: notify title exceeds %d bytes", ErrInvalid, MaxNotifyTitleBytes)
	case len(e.Body) > MaxNotifyBodyBytes:
		return fmt.Errorf("%w: notify body exceeds %d bytes", ErrInvalid, MaxNotifyBodyBytes)
	case e.OccurredAt.IsZero():
		return fmt.Errorf("%w: notify event has no occurrence time", ErrInvalid)
	case e.Origin.empty():
		return fmt.Errorf("%w: notify event has no origin", ErrInvalid)
	}
	switch e.Class {
	case NotifyWaiting, NotifyDone, NotifyFailure:
	default:
		return fmt.Errorf("%w: unknown notify class %q", ErrInvalid, e.Class)
	}
	for _, field := range []string{e.Source, e.Severity} {
		if len(field) > MaxNotifyOriginBytes {
			return fmt.Errorf("%w: notify origin field exceeds %d bytes", ErrInvalid, MaxNotifyOriginBytes)
		}
	}
	return e.Origin.validateBounds()
}

// validateBounds enforces the per-field limits on an origin. It is separate so
// a transition withdrawal, which carries an origin and nothing else, is bounded
// by the same rule as a full event rather than by a second copy of it.
func (o NotifyOrigin) validateBounds() error {
	for _, field := range []string{o.ItemID, o.ProjectKey, o.Session, o.Path} {
		if len(field) > MaxNotifyOriginBytes {
			return fmt.Errorf("%w: notify origin field exceeds %d bytes", ErrInvalid, MaxNotifyOriginBytes)
		}
	}
	return nil
}

// BoundNotifyText sanitizes and truncates one text field for the wire.
//
// Control characters are removed at the source as well as at the viewer,
// because the bounded schema is what makes this text safe to hand to a desktop
// service later: a title carrying an escape sequence is not a title. The cut
// is on a rune boundary so a truncated field is still valid UTF-8.
func BoundNotifyText(s string, limit int) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			b.WriteByte(' ')
		case r == utf8.RuneError, unicode.IsControl(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if len(out) <= limit {
		return out
	}
	out = out[:limit]
	for len(out) > 0 && !utf8.ValidString(out) {
		out = out[:len(out)-1]
	}
	return strings.TrimSpace(out)
}

// Error is a structured failure the host can name. Fatal marks a condition the
// serve loop cannot continue past, so the viewer stops waiting for data
// instead of showing a host that is quietly stuck.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal,omitempty"`
}

// Error codes. They exist so a viewer can map a failure to a row state and a
// suggested fix without string-matching a message — the exact fragility the
// plan calls out in the alternative.
const (
	ErrNoTmux      = "no_tmux"
	ErrNoConfig    = "no_config"
	ErrCollect     = "collect"
	ErrProtoTooOld = "proto_too_old"
	ErrInternal    = "internal"
)

// Encoder writes messages as JSONL and stamps the envelope so no caller has to
// remember to. Sequence numbers start at 1.
type Encoder struct {
	w   io.Writer
	enc *json.Encoder
	seq uint64
	now func() time.Time
}

// NewEncoder wraps w. Callers that need a deterministic transcript for a test
// replace the clock with SetClock.
func NewEncoder(w io.Writer) *Encoder {
	enc := json.NewEncoder(w)
	// A JSONL line must not contain a raw newline, and Go's encoder escapes
	// nothing else that matters here. HTML escaping is off because pane
	// captures are full of angle brackets and ampersands and there is no
	// browser at the far end.
	enc.SetEscapeHTML(false)
	return &Encoder{w: w, enc: enc, now: time.Now}
}

// SetClock replaces the timestamp source.
func (e *Encoder) SetClock(now func() time.Time) { e.now = now }

// Encode stamps and writes one message. A message that violates the contract
// is refused here rather than written: the host is the first place that can
// tell, and a viewer's decode-time refusal would cost the connection.
func (e *Encoder) Encode(msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	e.seq++
	msg.Proto = Version
	msg.Seq = e.seq
	if msg.At.IsZero() {
		msg.At = e.now()
	}
	if err := e.enc.Encode(msg); err != nil {
		return fmt.Errorf("hostproto encode %s: %w", msg.Kind, err)
	}
	if flusher, ok := e.w.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

// MaxPreludeLines and MaxPreludeBytes bound the non-protocol output a decoder
// will skip before the first message.
//
// A login profile writes a banner, a version nag, or a motd, and it writes it
// once, at the top. Skipping it is not leniency about garbage: it is the
// difference between working on an ordinary machine and not. The bound is what
// keeps that from becoming "read anything forever" — a stream that is simply
// not this protocol has to fail, and fail saying so, rather than being
// consumed in silence.
const (
	MaxPreludeLines = 64
	MaxPreludeBytes = 64 << 10

	// maxPreludeQuoteBytes clips the line quoted back in the failure. Enough
	// to recognise a banner, short enough for one row of a health panel.
	maxPreludeQuoteBytes = 200
)

// Decoder reads a JSONL message stream with a bounded line length.
type Decoder struct {
	scanner *bufio.Scanner

	// started records that a protocol message has been decoded. Prelude
	// skipping applies only before it: a banner is something a shell prints on
	// the way in, so output that appears after the stream has proven itself is
	// a fault and is reported as one.
	started      bool
	preludeLines int
	preludeBytes int
	firstPrelude string
}

// NewDecoder wraps r.
func NewDecoder(r io.Reader) *Decoder {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), MaxLineBytes)
	return &Decoder{scanner: scanner}
}

// Next returns the next message. It returns io.EOF at a clean end of stream.
//
// Blank lines are skipped rather than treated as errors: a remote login shell
// that prints a stray newline before exec'ing sidecar is a real and common
// condition, and it must not look like a protocol violation.
//
// So is a banner. Before the first protocol message, non-protocol output is
// skipped up to MaxPreludeLines / MaxPreludeBytes — a motd, a version nag, a
// wrapper's log line — because a machine whose profile prints one line to
// stdout is an ordinary machine, not a broken one, and refusing it meant no
// snapshot, no rows, and an error naming the very cause it would not handle.
// JSON that is not a protocol message is prelude too: a profile logging
// `{"level":"info"}` parses cleanly and must not be mistaken for the stream
// starting (the same rule the run path applies to a --json result).
//
// After the first message the stream has proven itself, and anything
// unparseable is a genuine fault reported as one. Exhausting the prelude
// budget, or reaching end of stream having skipped output and never seen a
// message, is also a failure — and it quotes what the host actually wrote,
// because that line is the whole diagnosis.
func (d *Decoder) Next() (Message, error) {
	for {
		if !d.scanner.Scan() {
			if err := d.scanner.Err(); err != nil {
				if err == bufio.ErrTooLong {
					return Message{}, fmt.Errorf("hostproto: message exceeds %d bytes", MaxLineBytes)
				}
				return Message{}, err
			}
			if !d.started && d.preludeLines > 0 {
				return Message{}, d.preludeFailure()
			}
			return Message{}, io.EOF
		}
		line := d.scanner.Bytes()
		if len(trimSpace(line)) == 0 {
			continue
		}
		var msg Message
		err := json.Unmarshal(line, &msg)
		if err == nil && (d.started || isProtocolMessage(msg)) {
			d.started = true
			// Bounds are checked before the caller ever sees the payload, so a
			// message that violates the schema cannot be stored, rendered, or
			// delivered on the strength of having parsed.
			if err := msg.Validate(); err != nil {
				return Message{}, err
			}
			return msg, nil
		}
		if d.started {
			return Message{}, fmt.Errorf("hostproto: unparseable line (remote output is not the protocol; a shell banner on stdout is the usual cause): %w", err)
		}
		if skipped := d.skipPrelude(line); skipped != nil {
			return Message{}, skipped
		}
	}
}

// isProtocolMessage reports that a decoded line is this protocol rather than
// something that merely happens to be JSON.
//
// Proto and Kind together are the test. Proto is stamped on every message the
// Encoder writes, so a zero one is not ours; a version this viewer refuses
// still has to reach the caller, which is why this does not compare it to
// Version. Kind is not checked against the known set for the same reason: a
// host one version ahead may name a kind this build has never heard of, and
// that is a compatibility answer to give, not a line to swallow.
func isProtocolMessage(msg Message) bool {
	return msg.Proto > 0 && msg.Kind != ""
}

// skipPrelude records one non-protocol line, or returns the failure when the
// budget is spent.
func (d *Decoder) skipPrelude(line []byte) error {
	if d.firstPrelude == "" {
		d.firstPrelude = quotePrelude(line)
	}
	d.preludeLines++
	d.preludeBytes += len(line)
	if d.preludeLines > MaxPreludeLines || d.preludeBytes > MaxPreludeBytes {
		return d.preludeFailure()
	}
	return nil
}

func (d *Decoder) preludeFailure() error {
	return fmt.Errorf("hostproto: %d lines of remote output are not the protocol "+
		"(a shell banner or a wrapper writing to stdout is the usual cause); the host wrote: %s",
		d.preludeLines, d.firstPrelude)
}

func quotePrelude(line []byte) string {
	text := string(trimSpace(line))
	if len(text) > maxPreludeQuoteBytes {
		text = text[:maxPreludeQuoteBytes] + "…"
	}
	return text
}

func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'
}

// Compatible reports whether a viewer speaking Version can talk to a host that
// announced hostProto. Equality is the v1 rule: there is no negotiation yet,
// and pretending otherwise would let a viewer half-read a stream.
func Compatible(hostProto int) bool { return hostProto == Version }

// IncompatibleMessage renders the actionable row text for a version mismatch.
// It is here rather than in the viewer so both ends agree on the wording, and
// so the direction of the mismatch is stated rather than implied.
func IncompatibleMessage(host string, hostProto int) string {
	switch {
	case hostProto < Version:
		return fmt.Sprintf("sidecar too old on %s (proto %d, need %d) — update sidecar there", host, hostProto, Version)
	case hostProto > Version:
		return fmt.Sprintf("sidecar too old here (proto %d, %s speaks %d) — update sidecar locally", Version, host, hostProto)
	default:
		return ""
	}
}
