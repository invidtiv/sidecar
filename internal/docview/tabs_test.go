package docview

import "testing"

func TestTabsIndexOfNormalizesSlashAndClean(t *testing.T) {
	a := New(nil)
	a.path = "docs/README.md"
	b := New(nil)
	b.path = "main.go"
	tabs := Tabs{Items: []Item{{View: a}, {View: b}}, Active: 1}

	if got := tabs.IndexOf("docs/README.md"); got != 0 {
		t.Fatalf("IndexOf README = %d", got)
	}
	if got := tabs.IndexOf("docs//README.md"); got != 0 {
		t.Fatalf("IndexOf cleaned README = %d", got)
	}
	if got := tabs.IndexOf("missing.go"); got != -1 {
		t.Fatalf("IndexOf missing = %d", got)
	}
	if tabs.ActiveView() != b {
		t.Fatal("ActiveView was not the selected tab")
	}
}

func TestTabsAppendSelectCloseAndCycle(t *testing.T) {
	tabs := Tabs{}
	one, two, three := New(nil), New(nil), New(nil)
	one.path, two.path, three.path = "one.md", "two.md", "three.md"

	tabs.Append(one)
	tabs.Append(two)
	tabs.Append(three)
	if tabs.Active != 2 || tabs.ActiveView() != three {
		t.Fatalf("append active = %d", tabs.Active)
	}

	tabs.Select(0)
	tabs.Cycle(1)
	if tabs.ActiveView() != two {
		t.Fatalf("cycle forward = %q", tabs.ActiveView().Title())
	}
	tabs.Cycle(-1)
	if tabs.ActiveView() != one {
		t.Fatalf("cycle back = %q", tabs.ActiveView().Title())
	}
	tabs.Cycle(-1)
	if tabs.ActiveView() != three {
		t.Fatalf("cycle wrap = %q", tabs.ActiveView().Title())
	}

	tabs.Select(1)
	tabs.CloseActive()
	if len(tabs.Items) != 2 || tabs.ActiveView() != three {
		t.Fatalf("close middle left %#v active=%d", tabTitles(tabs), tabs.Active)
	}
	tabs.CloseActive()
	tabs.CloseActive()
	if len(tabs.Items) != 0 || tabs.Active != 0 {
		t.Fatalf("close last left %d items active=%d", len(tabs.Items), tabs.Active)
	}
}

func TestVisibleTabRangeKeepsActiveAndMarksOverflow(t *testing.T) {
	if start, end, left, right := VisibleTabRange(nil, 0, 20); start != 0 || end != -1 || left || right {
		t.Fatalf("empty = %d %d %v %v", start, end, left, right)
	}

	one := []int{10}
	start, end, left, right := VisibleTabRange(one, 0, 8)
	if start != 0 || end != 0 || left || right {
		t.Fatalf("one tab = %d %d %v %v", start, end, left, right)
	}

	two := []int{8, 8}
	start, end, left, right = VisibleTabRange(two, 1, 20)
	if start != 0 || end != 1 || left || right {
		t.Fatalf("two fitting = %d %d %v %v", start, end, left, right)
	}

	many := []int{10, 10, 10, 10, 10}
	tabs := Tabs{Active: 4}
	start, end, left, right = tabs.VisibleRange(many, 24)
	if start != 3 || end != 4 || !left || right {
		t.Fatalf("overflow around last = %d %d %v %v", start, end, left, right)
	}
	start, end, left, right = VisibleTabRange(many, 0, 24)
	if start != 0 || end != 1 || left || !right {
		t.Fatalf("overflow around first = %d %d %v %v", start, end, left, right)
	}
}

func tabTitles(tabs Tabs) []string {
	out := make([]string, 0, len(tabs.Items))
	for _, item := range tabs.Items {
		if item.View != nil {
			out = append(out, item.View.Title())
		}
	}
	return out
}
