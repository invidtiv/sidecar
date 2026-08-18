package resourceview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/resource"
)

// Ref is the resource reference a tab points at. It is aliased rather than
// redeclared so a reference built by the scanner, restored from disk, or
// parsed from a CLI request is the same type everywhere.
type Ref = resource.Reference

// State is what the view is showing right now.
type State int

const (
	// StateArmed is a reference that has never been resolved: a restored tab
	// that has not been selected, or one whose provider is not ready yet. It
	// is not an error and must never be pruned as though it were.
	StateArmed State = iota
	// StateLoading is a resolve in flight with nothing to show behind it.
	StateLoading
	// StateReady is a resolved document.
	StateReady
	// StateError is a typed failure with no document behind it.
	StateError
)

// Resolver is how the view asks the host to resolve a reference. The host owns
// the manager, the process, the timeout and the cancellation; the view owns
// only when to ask and what to do with the answer.
//
// refresh bypasses cache freshness. The returned command must eventually
// produce a ResolvedMsg carrying the same identity fields it was given.
//
// epoch is the host's own scoping value, passed through so the model stamps it
// rather than every host wrapping the resolver to do the same thing.
type Resolver func(modelID int, generation, epoch uint64, ref resource.Reference, refresh bool) tea.Cmd

// ResolvedMsg is the result of one resolve. Its identity fields are what stop
// a late answer from landing in a closed, retargeted, or foreign tab: the host
// adds its own surface and epoch scoping on top.
type ResolvedMsg struct {
	ModelID    int
	Generation uint64
	// Epoch is the host's own scoping value, carried through untouched.
	Epoch uint64
	// Ref is the reference that was requested, before any canonical re-key.
	Ref      resource.Reference
	Document resource.Document
	Err      error
	// Refresh marks a result produced by Refresh rather than the first load,
	// so a failure can leave the last good document on screen.
	Refresh bool
}

// GetEpoch lets hosts run their normal epoch check without unwrapping.
func (m ResolvedMsg) GetEpoch() uint64 { return m.Epoch }

// Model is one resource reference in one content box.
type Model struct {
	renderer *markdown.Renderer
	resolve  Resolver

	modelID    int
	generation uint64
	epoch      uint64

	ref resource.Reference

	state State
	doc   resource.Document
	// hasDoc records that doc is a real resolved document, so a refresh
	// failure can keep showing it while reporting the error.
	hasDoc bool
	err    *resource.Error
	// refreshing is a resolve in flight over an existing document.
	refreshing bool

	width  int
	height int
	scroll int

	// pendingScroll is a restored scroll offset waiting for content to exist.
	pendingScroll    int
	hasPendingScroll bool

	// body is the rendered, sanitized body for the current width.
	body       []string
	bodyForW   int
	bodyForGen uint64
}

// New creates a view bound to a resolver. A nil renderer takes the default.
func New(renderer *markdown.Renderer, resolve Resolver) *Model {
	if renderer == nil {
		renderer, _ = markdown.NewRenderer()
	}
	return &Model{renderer: renderer, resolve: resolve, bodyForW: -1}
}

// Reference is what this tab points at.
func (m *Model) Reference() resource.Reference { return m.ref }

// State reports what the view is showing.
func (m *Model) State() State { return m.state }

// Document returns the resolved document and whether there is one.
func (m *Model) Document() (resource.Document, bool) { return m.doc, m.hasDoc }

// Err returns the current typed error, or nil.
func (m *Model) Err() *resource.Error { return m.err }

// Refreshing reports an in-flight resolve over an existing document.
func (m *Model) Refreshing() bool { return m.refreshing }

// Arm points the model at a reference without resolving it. A restored tab
// starts here so that opening Sidecar does not fan out one process per tab.
func (m *Model) Arm(modelID int, ref resource.Reference, epoch uint64) {
	m.modelID = modelID
	m.epoch = epoch
	m.ref = ref
	m.state = StateArmed
	m.hasDoc = false
	m.doc = resource.Document{}
	m.err = nil
	m.refreshing = false
	m.invalidateBody()
}

// SetPendingScroll records a restored scroll offset to apply once there is
// something to scroll.
func (m *Model) SetPendingScroll(scroll int) {
	if scroll <= 0 {
		return
	}
	m.pendingScroll = scroll
	m.hasPendingScroll = true
}

// ReArm returns a tab that is waiting on an answer nobody will deliver to the
// armed state, and reports whether it changed anything.
//
// A host discards a result whose surface, workspace row, or project epoch no
// longer matches. That is correct — but without this the tab it belonged to
// sits on a loading card forever, because the answer that would have cleared
// it was thrown away. Re-arming bumps the generation, so the discarded answer
// can never land afterwards either, and the card goes back to saying the tab
// is remembered and can be resolved with r.
func (m *Model) ReArm() bool {
	if m.state != StateLoading && !m.refreshing {
		return false
	}
	m.generation++
	m.refreshing = false
	if m.hasDoc {
		// A refresh that will never answer leaves the document it was
		// refreshing, which is what a failed refresh does too.
		m.state = StateReady
		return true
	}
	m.state = StateArmed
	return true
}

// Load resolves the reference, replacing whatever the model was showing. It
// bumps the generation, so any answer already in flight is stale on arrival.
func (m *Model) Load(modelID int, ref resource.Reference, epoch uint64) tea.Cmd {
	m.modelID = modelID
	m.epoch = epoch
	m.ref = ref
	m.generation++
	m.state = StateLoading
	m.hasDoc = false
	m.doc = resource.Document{}
	m.err = nil
	m.refreshing = false
	m.scroll = 0
	m.invalidateBody()
	return m.request(false)
}

// Resolve starts a resolve for an armed tab, and is a no-op for one that is
// already loading or resolved. Selecting a restored tab calls this.
func (m *Model) Resolve() tea.Cmd {
	if m.state != StateArmed {
		return nil
	}
	m.generation++
	m.state = StateLoading
	return m.request(false)
}

// Refresh re-resolves, bypassing freshness. Scroll and the last good document
// are preserved: a transient failure must not blank a card the user is
// reading.
func (m *Model) Refresh() tea.Cmd {
	if !m.ref.Valid() {
		return nil
	}
	m.generation++
	if m.hasDoc {
		m.refreshing = true
	} else {
		m.state = StateLoading
	}
	return m.request(true)
}

func (m *Model) request(refresh bool) tea.Cmd {
	if m.resolve == nil || !m.ref.Valid() {
		m.applyError(resource.Errorf(resource.CodeInvalidRequest,
			"this resource reference is not something Sidecar can resolve"))
		return nil
	}
	return m.resolve(m.modelID, m.generation, m.epoch, m.ref, refresh)
}

// Accepts reports whether a result belongs to this model's current request.
// A host still applies its own surface and epoch checks; this is the part
// every host would otherwise have written for itself.
func (m *Model) Accepts(msg ResolvedMsg) bool {
	return msg.ModelID == m.modelID && msg.Generation == m.generation
}

// Apply lands a resolve result. It returns false when the message does not
// belong to this model's current request, which is the normal outcome for a
// superseded refresh or a closed-and-reopened tab.
func (m *Model) Apply(msg ResolvedMsg) bool {
	if !m.Accepts(msg) {
		return false
	}
	m.refreshing = false
	if msg.Err != nil {
		// A failed refresh keeps the document it was refreshing.
		if msg.Refresh && m.hasDoc {
			m.err = asResourceError(msg.Err)
			return true
		}
		m.applyError(asResourceError(msg.Err))
		return true
	}
	m.doc = msg.Document
	m.hasDoc = true
	m.state = StateReady
	m.err = nil
	m.invalidateBody()
	m.applyPendingScroll()
	return true
}

// Rekey adopts the provider-stable identity a response supplied. The provider
// instance is never allowed to change: a response may say what a resource is
// called, not who owns it.
func (m *Model) Rekey(identity string) {
	if identity == "" || identity == m.ref.Locator {
		return
	}
	m.ref.Locator = identity
}

func (m *Model) applyError(err *resource.Error) {
	m.err = err
	m.state = StateError
	m.hasDoc = false
	m.doc = resource.Document{}
	m.invalidateBody()
}

func (m *Model) applyPendingScroll() {
	if !m.hasPendingScroll {
		return
	}
	m.hasPendingScroll = false
	m.scroll = m.pendingScroll
	m.pendingScroll = 0
	m.clampScroll()
}

// asResourceError makes any resolve failure displayable. A transport failure
// that is not already typed becomes internal, which is what the protocol says
// an unknown code maps to.
func asResourceError(err error) *resource.Error {
	if err == nil {
		return nil
	}
	if typed, ok := err.(*resource.Error); ok && typed != nil {
		return typed
	}
	return resource.Errorf(resource.CodeInternal, "%s", err.Error())
}
