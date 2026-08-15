package tabs

import (
	"strings"
	"testing"
)

func groupOf(keys ...string) Group[string] {
	var g Group[string]
	for _, key := range keys {
		g.Append(key, key)
	}
	return g
}

func groupValues(g Group[string]) []string {
	out := make([]string, len(g.Items))
	for i, item := range g.Items {
		out[i] = item.Value
	}
	return out
}

func TestGroupAppendAndOpenOrFocus(t *testing.T) {
	var g Group[string]
	g.Append("a", "A")
	g.Append("b", "B")
	if g.Active != 1 || len(g.Items) != 2 {
		t.Fatalf("append = active %d items %d", g.Active, len(g.Items))
	}

	idx, created := g.OpenOrFocus("a", "A-new")
	if created || idx != 0 || g.Active != 0 {
		t.Fatalf("focus existing = idx %d created %v active %d", idx, created, g.Active)
	}
	if g.Items[0].Value != "A" {
		t.Fatalf("OpenOrFocus replaced existing value: %q", g.Items[0].Value)
	}

	idx, created = g.OpenOrFocus("c", "C")
	if !created || idx != 2 || g.Active != 2 || g.Items[2].Value != "C" {
		t.Fatalf("append missing = idx %d created %v %#v", idx, created, groupValues(g))
	}
}

func TestGroupFindFirstDuplicateKey(t *testing.T) {
	var g Group[string]
	g.Append("a", "first")
	g.Append("b", "mid")
	g.Append("a", "second")

	if got := g.Find("a"); got != 0 {
		t.Fatalf("Find first duplicate = %d", got)
	}
	idx, created := g.OpenOrFocus("a", "third")
	if created || idx != 0 || len(g.Items) != 3 {
		t.Fatalf("OpenOrFocus duplicate created=%v idx=%d n=%d", created, idx, len(g.Items))
	}
	if g.Find("missing") != -1 {
		t.Fatal("Find missing should be -1")
	}
}

func TestGroupSelectCycleAndCloseActive(t *testing.T) {
	g := groupOf("one", "two", "three")
	g.Select(0)
	g.Cycle(1)
	if item, ok := g.ActiveItem(); !ok || item.Value != "two" {
		t.Fatalf("cycle forward = %#v ok=%v", item, ok)
	}
	g.Cycle(-1)
	if item, _ := g.ActiveItem(); item.Value != "one" {
		t.Fatalf("cycle back = %q", item.Value)
	}
	g.Cycle(-1)
	if item, _ := g.ActiveItem(); item.Value != "three" {
		t.Fatalf("cycle wrap = %q", item.Value)
	}

	g.Select(1)
	got := g.CloseActive()
	if got.Empty || !got.ActiveRemoved || len(got.Removed) != 1 || got.Removed[0].Value != "two" {
		t.Fatalf("close middle result = %#v", got)
	}
	if strings.Join(groupValues(g), ",") != "one,three" || g.Active != 1 {
		t.Fatalf("close middle left %#v active=%d", groupValues(g), g.Active)
	}

	g.CloseActive()
	last := g.CloseActive()
	if !last.Empty || len(g.Items) != 0 || g.Active != 0 {
		t.Fatalf("close last left n=%d active=%d empty=%v", len(g.Items), g.Active, last.Empty)
	}

	empty := g.CloseActive()
	if !empty.Empty || g.Active != 0 {
		t.Fatalf("close empty = %#v active=%d", empty, g.Active)
	}

	single := groupOf("only")
	single.Cycle(1)
	if single.Active != 0 {
		t.Fatalf("cycle single moved active to %d", single.Active)
	}
	single.Select(4)
	if single.Active != 0 {
		t.Fatalf("select out of range moved active to %d", single.Active)
	}
}

func TestGroupCloseMatchingBeforeAtAfterSeveralAndAll(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		g := groupOf("a", "b", "c", "d")
		g.Select(3)
		got := g.CloseMatching(func(item Item[string]) bool { return item.Key == "b" })
		if got.ActiveRemoved || got.Empty || strings.Join(groupValues(g), ",") != "a,c,d" || g.Active != 2 {
			t.Fatalf("before: %#v active=%d result=%#v", groupValues(g), g.Active, got)
		}
	})
	t.Run("active", func(t *testing.T) {
		g := groupOf("a", "b", "c", "d")
		g.Select(1)
		got := g.CloseMatching(func(item Item[string]) bool { return item.Key == "b" })
		if !got.ActiveRemoved || strings.Join(groupValues(g), ",") != "a,c,d" || g.Active != 1 {
			t.Fatalf("active: %#v active=%d result=%#v", groupValues(g), g.Active, got)
		}
	})
	t.Run("after", func(t *testing.T) {
		g := groupOf("a", "b", "c", "d")
		g.Select(0)
		got := g.CloseMatching(func(item Item[string]) bool { return item.Key == "d" })
		if got.ActiveRemoved || strings.Join(groupValues(g), ",") != "a,b,c" || g.Active != 0 {
			t.Fatalf("after: %#v active=%d result=%#v", groupValues(g), g.Active, got)
		}
	})
	t.Run("several before counted once", func(t *testing.T) {
		g := groupOf("a", "drop1", "drop2", "keep")
		g.Select(3)
		got := g.CloseMatching(func(item Item[string]) bool {
			return strings.HasPrefix(item.Key, "drop")
		})
		if got.ActiveRemoved || strings.Join(groupValues(g), ",") != "a,keep" || g.Active != 1 {
			t.Fatalf("several: %#v active=%d result=%#v", groupValues(g), g.Active, got)
		}
		if len(got.Removed) != 2 {
			t.Fatalf("removed = %d, want 2", len(got.Removed))
		}
	})
	t.Run("all", func(t *testing.T) {
		g := groupOf("a", "b")
		g.Select(1)
		got := g.CloseMatching(func(Item[string]) bool { return true })
		if !got.Empty || !got.ActiveRemoved || len(g.Items) != 0 || g.Active != 0 {
			t.Fatalf("all: n=%d active=%d result=%#v", len(g.Items), g.Active, got)
		}
	})
	t.Run("none", func(t *testing.T) {
		g := groupOf("a", "b")
		g.Select(1)
		got := g.CloseMatching(func(Item[string]) bool { return false })
		if got.ActiveRemoved || got.Empty || len(got.Removed) != 0 || g.Active != 1 {
			t.Fatalf("none: %#v", got)
		}
	})
}

func TestVisibleRangeKeepsActiveAndMarksOverflow(t *testing.T) {
	if start, end, left, right := VisibleRange(nil, 0, 20); start != 0 || end != -1 || left || right {
		t.Fatalf("empty = %d %d %v %v", start, end, left, right)
	}

	one := []int{10}
	start, end, left, right := VisibleRange(one, 0, 8)
	if start != 0 || end != 0 || left || right {
		t.Fatalf("one tab = %d %d %v %v", start, end, left, right)
	}

	two := []int{8, 8}
	start, end, left, right = VisibleRange(two, 1, 20)
	if start != 0 || end != 1 || left || right {
		t.Fatalf("two fitting = %d %d %v %v", start, end, left, right)
	}

	many := []int{10, 10, 10, 10, 10}
	g := Group[string]{Active: 4}
	start, end, left, right = g.VisibleRange(many, 24)
	if start != 3 || end != 4 || !left || right {
		t.Fatalf("overflow around last = %d %d %v %v", start, end, left, right)
	}
	start, end, left, right = VisibleRange(many, 0, 24)
	if start != 0 || end != 1 || left || !right {
		t.Fatalf("overflow around first = %d %d %v %v", start, end, left, right)
	}
}
