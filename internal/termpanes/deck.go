// Package termpanes owns the host-independent state and lifecycle of Sidecar's
// live terminal leaves.
//
// A Deck deliberately does not render or persist itself. Hosts own their pane
// trees and target policy; the deck only keys terminal state by tree leaf ID.
// Potentially expensive terminal operations are returned as tea.Cmd.
package termpanes

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// Target is the currently attached terminal identity and geometry. Source and
// SourceID are host vocabulary used to distinguish projections of the same tmux
// target without coupling the deck to a particular surface.
type Target struct {
	Session string
	Pane    string
	// Host names the machine the session lives on; empty is this machine. It
	// is stored rather than derived because the surface compares its stored
	// target against a desired one to decide "am I already showing this?" —
	// and a comparison that always dropped Host could never answer yes for a
	// remote pane, so every poll tore down its ssh connection and reseeded.
	Host     string
	Width    int
	Height   int
	Source   string
	SourceID string
}

// Leaf is all mutable state belonging to one live terminal leaf.
type Leaf struct {
	ID int
	// Requested is host reconciliation policy: the keyed state should have a
	// live tree leaf. It is distinct from merely preparing detached state.
	Requested bool

	Terminal  *tty.Model
	Target    Target
	Buffer    *tty.OutputBuffer
	Scroll    int
	Freeze    tty.WindowFreeze
	FreezeDoc bool
	History   tty.HistoryReach

	LinkState   termpreview.LinkState
	LinkContext any
	RowAnalyzer *termpreview.RowAnalyzer

	// Interaction state belongs to the leaf where the gesture or input mode
	// began. HostState retains host-specific adapters without teaching this
	// presentation-neutral collection either host's private interface.
	Selection   ui.SelectionState
	Pointer     tty.Pointer
	Wheel       tty.WheelBurst
	Interactive bool
	HostState   any

	Session string
	PaneID  string
	Name    string
}

// NewLeaf constructs detached leaf state and its durable presentation helpers
// without I/O. Hosts must not patch shared leaf invariants after construction.
func NewLeaf(id int, terminal *tty.Model) *Leaf {
	return &Leaf{ID: id, Terminal: terminal, RowAnalyzer: &termpreview.RowAnalyzer{}}
}

// Decode reconstructs detached state without opening a terminal.
func Decode(id int, session, paneID string, terminal *tty.Model) *Leaf {
	leaf := NewLeaf(id, terminal)
	leaf.Session, leaf.PaneID = session, paneID
	return leaf
}

// Open attaches the terminal model to target asynchronously.
func (l *Leaf) Open(target Target) tea.Cmd {
	if l == nil || l.Terminal == nil {
		return nil
	}
	l.Target = target
	l.Terminal.Width, l.Terminal.Height = target.Width, target.Height
	return l.Terminal.Open(tty.Target{Session: target.Session, Pane: target.Pane})
}

// Close releases the model synchronously and clears its target.
func (l *Leaf) Close() {
	if l == nil {
		return
	}
	if l.Terminal != nil {
		l.Terminal.Close()
	}
	l.Target = Target{}
}

// Resize asks the attached model to adopt a new geometry.
func (l *Leaf) Resize(width, height int) tea.Cmd {
	if l == nil || l.Terminal == nil {
		return nil
	}
	l.Target.Width, l.Target.Height = width, height
	return l.Terminal.Resize(width, height)
}

// ReadHistory performs a caller-supplied history read as a command. The deck
// owns the reach state while the host owns target resolution and result policy.
func (l *Leaf) ReadHistory(read func() tea.Msg) tea.Cmd {
	if l == nil || read == nil {
		return nil
	}
	return read
}

// Deck is the keyed collection of live leaf state for one pane tree.
type Deck struct{ leaves map[int]*Leaf }

// New constructs an empty collection without I/O.
func New() *Deck { return &Deck{leaves: make(map[int]*Leaf)} }

// Attach installs leaf state at its pane-tree ID.
func (d *Deck) Attach(leaf *Leaf) bool {
	if d == nil || leaf == nil || leaf.ID < 0 {
		return false
	}
	if d.leaves == nil {
		d.leaves = make(map[int]*Leaf)
	}
	d.leaves[leaf.ID] = leaf
	return true
}

// Rekey moves detached state onto the pane-tree ID that made it live.
func (d *Deck) Rekey(from, to int) *Leaf {
	if d == nil || to < 0 {
		return nil
	}
	leaf := d.leaves[from]
	if leaf == nil {
		return nil
	}
	delete(d.leaves, from)
	leaf.ID = to
	d.leaves[to] = leaf
	return leaf
}

// Release removes and closes a leaf.
func (d *Deck) Release(id int) *Leaf {
	if d == nil {
		return nil
	}
	leaf := d.leaves[id]
	if leaf != nil {
		leaf.Close()
		delete(d.leaves, id)
	}
	return leaf
}

// Leaf returns state for a pane-tree leaf ID.
func (d *Deck) Leaf(id int) *Leaf {
	if d == nil {
		return nil
	}
	return d.leaves[id]
}

// Range visits attached leaves until visit returns false.
func (d *Deck) Range(visit func(int, *Leaf) bool) {
	if d == nil || visit == nil {
		return
	}
	for id, leaf := range d.leaves {
		if !visit(id, leaf) {
			return
		}
	}
}

// LiveLeafCount answers the cap from the pane tree, which remains the source
// of truth for which leaves are live.
func (d *Deck) LiveLeafCount(root *panelayout.Node) int {
	return panelayout.LiveLeafCount(root)
}
