package notify

import (
	"sort"
	"time"
)

// Toast stacking, design frame 1b. Like everything else in this package these
// are state-free rules over a set of notifications: the app shell asks what
// belongs on screen and gets back an answer any surface could have computed.
//
// Two rules, and they compose:
//
//   - Same-source toasts collapse into one block carrying ×N and a peek line.
//     This is also where a source that repeats itself — a refusal the user is
//     leaning on a key for, an agent flapping — stops being a column of
//     near-identical blocks.
//   - At most DefaultSlots blocks are on screen. Anything beyond that queues,
//     macOS-style, and takes a slot when one frees.

// DefaultSlots is how many toast blocks may be on screen at once (1b).
const DefaultSlots = 3

// Stack is one toast block: the notification actually drawn, plus every other
// undismissed toastable notification from the same source hiding behind it.
type Stack struct {
	// Source is the stack's identity. Collapse is per source, so the source id
	// is also the stable key a host keys its reveal state by — a block does not
	// restart its animation because a second notification joined it.
	Source SourceID
	// Members are the stack's notifications, newest first. Never empty.
	Members []Notification
	// First is the arrival time of the oldest member: the stack's place in the
	// queue. A source already on screen keeps its slot when it gains a member.
	First time.Time
}

// Lead is the notification the block is drawn for: the newest in the stack.
func (s Stack) Lead() Notification {
	if len(s.Members) == 0 {
		return Notification{}
	}
	return s.Members[0]
}

// Count is the ×N on the block.
func (s Stack) Count() int { return len(s.Members) }

// Hidden is how many members the peek line offers to expand.
func (s Stack) Hidden() int { return max(0, len(s.Members)-1) }

// Layout is what a toast host draws this frame.
type Layout struct {
	// Stacks are the blocks on screen, newest lead first — the top of the
	// column is the thing that just happened (1b).
	Stacks []Stack
	// Queued are the stacks waiting for a slot, oldest first: the next one to
	// surface is Queued[0]. They are not painted, and a host must not mark
	// their notifications seen.
	Queued []Stack
}

// StackToasts resolves the whole toast column. slots <= 0 means DefaultSlots.
func StackToasts(all []Notification, now time.Time, slots int) Layout {
	if slots <= 0 {
		slots = DefaultSlots
	}
	stacks := collapse(Toastable(all, now))
	if len(stacks) == 0 {
		return Layout{}
	}
	// Admission is first-come-first-served, by the stack's oldest member: a
	// burst shows the first `slots` sources and the rest wait their turn, which
	// is what "queue and surface as slots free, oldest queued first" means. It
	// is deliberately not newest-first — that would let a chatty source shove a
	// block off the screen the instant before the user read it.
	sort.SliceStable(stacks, func(i, j int) bool {
		if !stacks[i].First.Equal(stacks[j].First) {
			return stacks[i].First.Before(stacks[j].First)
		}
		return stacks[i].Source < stacks[j].Source
	})
	var out Layout
	if len(stacks) > slots {
		out.Queued = append(out.Queued, stacks[slots:]...)
		stacks = stacks[:slots]
	}
	// Display order is the other axis: admitted by age, painted newest on top.
	sort.SliceStable(stacks, func(i, j int) bool {
		return louderStack(stacks[i], stacks[j])
	})
	out.Stacks = stacks
	return out
}

func louderStack(a, b Stack) bool {
	al, bl := a.Lead(), b.Lead()
	if !al.CreatedAt.Equal(bl.CreatedAt) {
		return al.CreatedAt.After(bl.CreatedAt)
	}
	return al.ID > bl.ID
}

// collapse groups toastable notifications by source, newest member first.
func collapse(toastable []Notification) []Stack {
	index := map[SourceID]int{}
	var stacks []Stack
	for _, n := range toastable {
		id := SourceOf(n.Source).ID
		at, ok := index[id]
		if !ok {
			index[id] = len(stacks)
			stacks = append(stacks, Stack{Source: id, Members: []Notification{n}, First: n.CreatedAt})
			continue
		}
		stacks[at].Members = append(stacks[at].Members, n)
		if n.CreatedAt.Before(stacks[at].First) {
			stacks[at].First = n.CreatedAt
		}
	}
	for i := range stacks {
		SortNewestFirst(stacks[i].Members)
	}
	return stacks
}
