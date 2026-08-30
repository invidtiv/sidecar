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
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Version is the protocol integer. Bump it for any change a viewer of the
// previous version could misread; adding an optional field a decoder ignores
// is not such a change.
const Version = 1

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
)

// Message is the envelope. Exactly one of the payload pointers is set, chosen
// by Kind.
type Message struct {
	Proto int       `json:"proto"`
	Kind  Kind      `json:"kind"`
	Seq   uint64    `json:"seq"`
	At    time.Time `json:"at"`

	Hello    *Hello    `json:"hello,omitempty"`
	Snapshot *Snapshot `json:"snapshot,omitempty"`
	Event    *Event    `json:"event,omitempty"`
	Error    *Error    `json:"error,omitempty"`
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
	// screen identification.
	//
	// A host that predates the flag answers `unknown option "--agent"` and exits
	// 2, so a viewer that sent it unconditionally did not degrade — it broke
	// remote agent-shell creation outright against any machine updated later
	// than the viewer's. Mixed versions are the normal state: nobody updates two
	// machines at the same moment. A viewer that reads false here falls back to
	// creating the shell and then starting the agent with `shell send --run`,
	// which is what it did before the flag existed.
	CreateShellAgent bool `json:"createShellAgent,omitempty"`
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

// Encode stamps and writes one message.
func (e *Encoder) Encode(msg Message) error {
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

// Decoder reads a JSONL message stream with a bounded line length.
type Decoder struct {
	scanner *bufio.Scanner
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
// condition, and it must not look like a protocol violation. Anything that is
// non-blank and not valid JSON is a genuine error — most often a shell banner
// or an ssh warning on the same pipe — and the message says so, because that
// is the failure a first-time user of this feature will actually hit.
func (d *Decoder) Next() (Message, error) {
	for {
		if !d.scanner.Scan() {
			if err := d.scanner.Err(); err != nil {
				if err == bufio.ErrTooLong {
					return Message{}, fmt.Errorf("hostproto: message exceeds %d bytes", MaxLineBytes)
				}
				return Message{}, err
			}
			return Message{}, io.EOF
		}
		line := d.scanner.Bytes()
		if len(trimSpace(line)) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return Message{}, fmt.Errorf("hostproto: unparseable line (remote output is not the protocol; a shell banner on stdout is the usual cause): %w", err)
		}
		return msg, nil
	}
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
