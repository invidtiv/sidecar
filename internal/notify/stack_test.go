package notify

import (
	"testing"
	"time"
)

func stackable(source SourceID, title string, created time.Time, life time.Duration) Notification {
	n := Notification{ID: title, Source: source, Title: title, CreatedAt: created}
	if life > 0 {
		expires := created.Add(life)
		n.ExpiresAt = &expires
	} else {
		n.Sticky = true
	}
	return n
}

// Design 1b: a source repeating *itself* is one block carrying ×N, with the
// newest as its lead — the refusal a user is leaning on a key for stops being
// a column of near-identical blocks.
func TestRepeatedMessageCollapsesIntoOneBlock(t *testing.T) {
	now := time.Now().UTC()
	all := []Notification{
		repeat(SourceWaiting, "Agent is waiting", "a", now.Add(-3*time.Second)),
		repeat(SourceWaiting, "Agent is waiting", "b", now.Add(-2*time.Second)),
		repeat(SourceWaiting, "Agent is waiting", "c", now.Add(-time.Second)),
	}
	layout := StackToasts(all, now, DefaultSlots)
	if len(layout.Stacks) != 1 {
		t.Fatalf("three copies of one message drew %d blocks, want 1", len(layout.Stacks))
	}
	s := layout.Stacks[0]
	if s.Count() != 3 || s.Hidden() != 2 {
		t.Fatalf("count=%d hidden=%d, want 3/2", s.Count(), s.Hidden())
	}
	if s.Lead().ID != "c" {
		t.Fatalf("lead = %q, want the newest member", s.Lead().ID)
	}
}

func repeat(source SourceID, title, id string, created time.Time) Notification {
	return Notification{ID: id, Source: source, Title: title, CreatedAt: created, Sticky: true}
}

// Live-use regression: three *different* notifications a second apart from the
// same source are three blocks. They collapsed to one before, so each arrival
// replaced the last instead of stacking — which is what agents and the CLI
// actually post, since nearly everything they send is `agent`.
func TestDistinctMessagesFromOneSourceStack(t *testing.T) {
	now := time.Now().UTC()
	all := []Notification{
		stackable(SourceAgent, "first", now.Add(-3*time.Second), time.Minute),
		stackable(SourceAgent, "second", now.Add(-2*time.Second), time.Minute),
		stackable(SourceAgent, "third", now.Add(-time.Second), time.Minute),
	}
	layout := StackToasts(all, now, DefaultSlots)
	if len(layout.Stacks) != 3 || len(layout.Queued) != 0 {
		t.Fatalf("on screen=%d queued=%d, want 3/0", len(layout.Stacks), len(layout.Queued))
	}
	if layout.Stacks[0].Lead().Title != "third" {
		t.Fatalf("top block = %q, want the newest", layout.Stacks[0].Lead().Title)
	}
	for _, s := range layout.Stacks {
		if s.Count() != 1 {
			t.Fatalf("block %q collapsed %d notifications, want 1", s.Key, s.Count())
		}
	}
}

// A fourth distinct message from the same source queues rather than shoving a
// block off the screen.
func TestAFourthMessageFromOneSourceQueues(t *testing.T) {
	now := time.Now().UTC()
	all := []Notification{
		stackable(SourceAgent, "first", now.Add(-4*time.Second), time.Minute),
		stackable(SourceAgent, "second", now.Add(-3*time.Second), time.Minute),
		stackable(SourceAgent, "third", now.Add(-2*time.Second), time.Minute),
		stackable(SourceAgent, "fourth", now.Add(-time.Second), time.Minute),
	}
	layout := StackToasts(all, now, DefaultSlots)
	if len(layout.Stacks) != DefaultSlots || len(layout.Queued) != 1 {
		t.Fatalf("on screen=%d queued=%d, want 3/1", len(layout.Stacks), len(layout.Queued))
	}
	if layout.Queued[0].Lead().Title != "fourth" {
		t.Fatalf("queued %q, want the newest — admission is first-come-first-served", layout.Queued[0].Lead().Title)
	}
}

// At most three blocks are on screen; the rest queue and take a slot when one
// frees, oldest queued first.
func TestBeyondThreeSourcesQueue(t *testing.T) {
	now := time.Now().UTC()
	all := []Notification{
		stackable(SourceAgent, "agent", now.Add(-5*time.Second), time.Minute),
		stackable(SourceSession, "session", now.Add(-4*time.Second), time.Minute),
		stackable(SourceTD, "td", now.Add(-3*time.Second), time.Minute),
		stackable(SourceTasks, "tasks", now.Add(-2*time.Second), time.Minute),
	}
	layout := StackToasts(all, now, DefaultSlots)
	if len(layout.Stacks) != DefaultSlots || len(layout.Queued) != 1 {
		t.Fatalf("on screen=%d queued=%d, want 3/1", len(layout.Stacks), len(layout.Queued))
	}
	if layout.Queued[0].Source != SourceTasks {
		t.Fatalf("queued the wrong stack: %s — admission is first-come-first-served", layout.Queued[0].Source)
	}
	// The newest admitted block is on top (1b), even though admission was by age.
	if layout.Stacks[0].Source != SourceTD {
		t.Fatalf("top block = %s, want the newest admitted", layout.Stacks[0].Source)
	}

	// The oldest admitted block expires; the queued one takes its slot.
	all[0].Sticky = false
	expired := now.Add(-time.Second)
	all[0].ExpiresAt = &expired
	layout = StackToasts(all, now, DefaultSlots)
	if len(layout.Queued) != 0 {
		t.Fatalf("a freed slot left %d queued", len(layout.Queued))
	}
	found := false
	for _, s := range layout.Stacks {
		if s.Source == SourceTasks {
			found = true
		}
	}
	if !found {
		t.Fatal("the queued stack did not surface when a slot freed")
	}
}

// A stack keeps its place in the queue when it gains a member: a chatty source
// must not be able to shove itself up the admission order.
func TestAStackKeepsItsSlotWhenItGrows(t *testing.T) {
	now := time.Now().UTC()
	all := []Notification{
		stackable(SourceAgent, "agent old", now.Add(-9*time.Second), time.Minute),
		stackable(SourceAgent, "agent new", now, time.Minute),
		stackable(SourceSession, "session", now.Add(-5*time.Second), time.Minute),
	}
	layout := StackToasts(all, now, 1)
	if len(layout.Stacks) != 1 || layout.Stacks[0].Source != SourceAgent {
		t.Fatalf("admitted %v, want the agent stack that arrived first", layout.Stacks)
	}
}

// Dismissed, read, and expired notifications are not on screen, so they are not
// in any block.
func TestStackToastsOnlyDrawsToastables(t *testing.T) {
	now := time.Now().UTC()
	read := stackable(SourceAgent, "read", now, time.Minute)
	read.ReadAt = &now
	all := []Notification{read, stackable(SourceAgent, "live", now, time.Minute)}
	layout := StackToasts(all, now, DefaultSlots)
	if len(layout.Stacks) != 1 || layout.Stacks[0].Count() != 1 {
		t.Fatalf("a read notification joined a block: %v", layout.Stacks)
	}
}
