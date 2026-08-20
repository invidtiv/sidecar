package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/reveal"
)

// The toast column (design frames 1b and 1h). Three things live here and they
// are deliberately kept apart from the drawing in toast_view.go:
//
//   - which stacks are on screen this frame (notify.StackToasts decides;
//     this file only adds the re-show slot, which is presentation-only state
//     the store knows nothing about),
//   - each stack's reveal animation, keyed by source so a block does not
//     restart its motion when a second notification joins it,
//   - the guard that keeps both tiers off the screen while a pane rail is
//     being dragged.

const (
	// toastGapY is the blank row between blocks in the column. Two rounded
	// borders sharing an edge read as one malformed box.
	toastGapY = 1
)

// toastReveal is one block's motion plus the block it was last drawn from.
// The rendered text is kept because a retraction has to paint rows of a block
// whose notifications have already left the store: "bottom-up" is the top N
// rows of what was there, not a re-render of nothing.
type toastReveal struct {
	state *reveal.State
	block string
	// stack is the block this state was last synced from. It is kept because
	// the reveal machine, not the store, is what the render, the read gate and
	// the dismiss key read: a block whose records have expired is still on
	// screen until its retraction finishes, and it has to know what it holds.
	stack notify.Stack
}

// revealTickMsg drives the reveal frames (reveal.Step). It is tagged like every other
// timer in the app, so a burst of posts cannot leave two loops advancing the
// same states.
type revealTickMsg struct{ seq int }

func revealTick(seq int) tea.Cmd {
	return tea.Tick(reveal.Step, func(time.Time) tea.Msg { return revealTickMsg{seq: seq} })
}

// toastStacks is the column as the store and the re-show slot describe it,
// newest lead on top. It is pure: two calls in the same frame agree.
func (m Model) toastStacks(now time.Time) []notify.Stack {
	layout := notify.StackToasts(m.notificationCache, now, notify.DefaultSlots)
	stacks := layout.Stacks
	// A re-show ("view details" from the centre) is the user asking for one
	// specific notification, which is a newer intent than anything the store
	// posted — so it takes the top slot outright. It is a copy, not a record,
	// so it cannot come out of StackToasts.
	if n, ok := m.reshownToast(now); ok {
		key := notify.StackKeyFor(n)
		out := []notify.Stack{{
			Key:     key,
			Source:  notify.SourceOf(n.Source).ID,
			Members: []notify.Notification{n},
			First:   n.CreatedAt,
		}}
		for _, s := range stacks {
			// One block per identity, always: the key is the collapse key, the
			// reveal key and the pointer target, so a re-show takes over its
			// own block rather than opening a second one beside it.
			if s.Key == key {
				continue
			}
			out = append(out, s)
		}
		if len(out) > notify.DefaultSlots {
			out = out[:notify.DefaultSlots]
		}
		return out
	}
	return stacks
}

// syncToastReveal reconciles the reveal states against the current column and
// returns the tick that keeps the motion running, or nil once it has settled.
// Every path that can change the column calls it — a post, a dismissal, the
// heartbeat's expiry sweep, and the reveal tick itself — so a block can never
// be on screen without a state, and a state can never outlive its block.
func (m *Model) syncToastReveal(now time.Time) tea.Cmd {
	width := m.toastBlockWidth()
	if m.toastReveals == nil {
		m.toastReveals = map[notify.StackKey]*toastReveal{}
	}
	live := map[notify.StackKey]bool{}
	var order []notify.StackKey
	// The column stops where the content region does. A block the renderer
	// would have to clip off the bottom is not on screen, so it gets no reveal
	// state — which is also how the read gate learns it was never painted.
	budget := m.contentHeight() - toastMarginY
	if width > 0 && !m.overlaysSuppressed() {
		for _, s := range m.toastStacks(now) {
			block := renderToastBlock(s, width, now, m.toastExpanded)
			if block == "" {
				continue
			}
			height := lipgloss.Height(block)
			if height > budget {
				break
			}
			budget -= height + toastGapY
			live[s.Key] = true
			order = append(order, s.Key)
			r, ok := m.toastReveals[s.Key]
			if !ok {
				r = &toastReveal{state: reveal.New(height)}
				m.toastReveals[s.Key] = r
			} else if r.state.Phase() == reveal.Leaving || r.state.Phase() == reveal.Gone {
				// The block came back before its retraction finished, so it
				// arrives again rather than snapping back to full height.
				r.state = reveal.New(height)
			} else {
				r.state.Resize(height)
			}
			r.block = block
			r.stack = s
		}
	}
	// Anything no longer admitted retracts, and keeps its place in the column
	// while it does: a block must never blink out and be re-painted just to
	// play its exit. Its cached block is what "bottom-up" retracts — the
	// records behind it may already have left the store.
	//
	// "Its place" is literal — it is re-inserted at the index it held in the
	// previous column, not appended. Appending was invisible for an expiry
	// (the oldest block is already the bottom one) and wrong for every other
	// way a block leaves: dismissing the top block with `d` or a click made it
	// jump to the bottom of the column and retract there while the blocks
	// below it slid up past it.
	for i, key := range m.toastColumn {
		if live[key] {
			continue
		}
		r, ok := m.toastReveals[key]
		if !ok {
			continue
		}
		r.state.Leave()
		if r.state.Phase() == reveal.Gone {
			continue
		}
		at := min(i, len(order))
		order = append(order, "")
		copy(order[at+1:], order[at:])
		order[at] = key
	}
	var animating bool
	for key, r := range m.toastReveals {
		if !live[key] {
			// A state the previous column never listed (a resize dropped it, or
			// it was created before this column existed) still has to leave.
			r.state.Leave()
		}
		if r.state.Phase() == reveal.Gone {
			delete(m.toastReveals, key)
			continue
		}
		if r.state.Animating() {
			animating = true
		}
	}
	m.toastColumn = nil
	for _, key := range order {
		if _, ok := m.toastReveals[key]; ok {
			m.toastColumn = append(m.toastColumn, key)
		}
	}
	if !animating {
		return nil
	}
	m.toastRevealSeq++
	return revealTick(m.toastRevealSeq)
}

// toastColumnBlocks is what is on screen this frame, top to bottom: the reveal
// machine's answer, not the store's. Every render, hit-test, read-gate and
// dismiss path reads it, so a record set change can only ever feed the machine
// — it can never make a block appear before its entry animation or vanish
// before its exit.
func (m Model) toastColumnBlocks() []*toastReveal {
	out := make([]*toastReveal, 0, len(m.toastColumn))
	for _, key := range m.toastColumn {
		if r, ok := m.toastReveals[key]; ok {
			out = append(out, r)
		}
	}
	return out
}

// advanceToastReveal handles one reveal frame.
func (m *Model) advanceToastReveal(tick revealTickMsg) tea.Cmd {
	if tick.seq != m.toastRevealSeq {
		return nil
	}
	for _, r := range m.toastReveals {
		r.state.Advance()
	}
	return m.syncToastReveal(time.Now())
}

// toggleToastExpand answers the expand key on a collapsed stack (design 1b's
// peek line). The design's key is `tab`, which Phase 2 gave to the focus cycle
// whenever the centre is open — one key cannot mean both — so the expand
// affordance is `alt+e`, in the same family as `alt+n` for the centre: a key no
// context rebinds, available on every tab. It reports whether anything on
// screen had something to expand, so the key falls through untouched otherwise.
func (m *Model) toggleToastExpand() bool {
	if m.overlaysSuppressed() {
		return false
	}
	if !m.toastExpanded {
		expandable := false
		for _, r := range m.toastColumnBlocks() {
			if r.stack.Hidden() > 0 {
				expandable = true
				break
			}
		}
		if !expandable {
			return false
		}
	}
	m.toastExpanded = !m.toastExpanded
	return true
}

// overlaysSuppressed is the suppress-while-resizing guard (design 1g). While a
// resize rail is under the pointer every drag frame re-lays out the whole
// content region; painting a floating block on top of that both flickers and
// adds a composite to the frame that is already the expensive one. Nothing is
// lost: the notification is in the store, in the centre, and in the header
// count, and the toast paints on the frame after the drop.
//
// It is also why the read gate is safe here — a toast the guard kept off the
// screen was never painted, so its expiry cannot mark it read.
func (m Model) overlaysSuppressed() bool {
	if m.notificationCentreDragging() {
		return true
	}
	if h := m.currentContentDeck(); h != nil && h.mouse.IsDragging() && h.dragSplit != 0 {
		return true
	}
	if p, ok := m.ActivePlugin().(plugin.ResizeDragReporter); ok && p != nil {
		return p.ResizeDragActive()
	}
	if m.inGlobalScope() && m.overview != nil {
		return m.overview.ResizeDragActive()
	}
	return false
}
