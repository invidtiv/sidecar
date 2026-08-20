package notify

import (
	"sort"
	"strings"
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

// StackKey is a block's identity: the collapse key, the reveal key, and the
// pointer target, one string.
//
// It was the source id through Phase 3, which read correctly against the six
// registered sources but wrongly against real use: nearly everything an agent
// or the CLI posts is `agent`, so three unrelated notifications a second apart
// collapsed into one block and each new title *replaced* the last. Design 1b's
// collapse exists to stop a source repeating *itself* — "a refusal the user is
// leaning on a key for" — which is a repeat of the same message, not any two
// messages that share a source. So identity is source **and** title: repeats
// still dedupe to `×N`, and distinct notifications stack, queue and retract as
// separate blocks the way the spec describes.
type StackKey string

// StackKeyFor is the block identity a notification belongs to.
func StackKeyFor(n Notification) StackKey {
	title := strings.ToLower(strings.Join(strings.Fields(n.Title), " "))
	return StackKey(string(SourceOf(n.Source).ID) + "\x00" + title)
}

// Stack is one toast block: the notification actually drawn, plus every other
// undismissed toastable notification from the same source hiding behind it.
type Stack struct {
	// Key is the stack's identity: the collapse key, the reveal key and the
	// pointer target. A block does not restart its animation because another
	// copy of the same message joined it.
	Key StackKey
	// Source is what the block looks like — hue, glyph, section — not what it
	// is. Two blocks can share a source.
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
		return stacks[i].Key < stacks[j].Key
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

// collapse groups toastable notifications by block identity (source + title),
// newest member first.
func collapse(toastable []Notification) []Stack {
	index := map[StackKey]int{}
	var stacks []Stack
	for _, n := range toastable {
		key := StackKeyFor(n)
		at, ok := index[key]
		if !ok {
			index[key] = len(stacks)
			stacks = append(stacks, Stack{
				Key:     key,
				Source:  SourceOf(n.Source).ID,
				Members: []Notification{n},
				First:   n.CreatedAt,
			})
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
