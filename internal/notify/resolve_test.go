package notify

import (
	"testing"
	"time"
)

func at(d time.Duration) *time.Time {
	t := time.Now().UTC().Add(d)
	return &t
}

func TestUnreadCountAndActive(t *testing.T) {
	now := time.Now().UTC()
	all := []Notification{
		{ID: "a", Source: SourceAgent, CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "b", Source: SourceAgent, CreatedAt: now.Add(-2 * time.Minute), ReadAt: at(-time.Minute)},
		{ID: "c", Source: SourceTD, CreatedAt: now.Add(-time.Minute), DismissedAt: at(-30 * time.Second)},
	}
	if got := UnreadCount(all); got != 1 {
		t.Fatalf("UnreadCount = %d, want 1", got)
	}
	active := Active(all)
	if len(active) != 2 {
		t.Fatalf("Active = %d, want 2 (dismissed excluded)", len(active))
	}
	if active[0].ID != "b" {
		t.Fatalf("Active must be newest first, got %s", active[0].ID)
	}
	if unread := Unread(all); len(unread) != 1 || unread[0].ID != "a" {
		t.Fatalf("Unread = %+v", unread)
	}
}

func TestLoudestPrefersSeverityThenSourcePriority(t *testing.T) {
	now := time.Now().UTC()
	all := []Notification{
		{ID: "sys", Source: SourceSystem, Severity: SeverityInfo, CreatedAt: now},
		{ID: "wait", Source: SourceWaiting, Severity: SeverityInfo, CreatedAt: now.Add(-time.Minute)},
	}
	loud, ok := Loudest(all)
	if !ok || loud.ID != "wait" {
		t.Fatalf("waiting outranks system by priority, got %+v (%v)", loud, ok)
	}
	hue, ok := LoudestHue(all)
	if !ok || hue != HueWarning {
		t.Fatalf("LoudestHue = %v, %v; want warning", hue, ok)
	}

	all = append(all, Notification{ID: "boom", Source: SourceTasks, Severity: SeverityError, CreatedAt: now.Add(-time.Hour)})
	loud, _ = Loudest(all)
	if loud.ID != "boom" {
		t.Fatalf("an error outranks everything, got %s", loud.ID)
	}
	if hue, _ := LoudestHue(all); hue != HueError {
		t.Fatalf("LoudestHue = %v; want error", hue)
	}

	if _, ok := Loudest([]Notification{{ID: "x", ReadAt: at(-time.Second)}}); ok {
		t.Fatalf("a read notification is not loud")
	}
	if hue, ok := LoudestHue(nil); ok || hue != HueMuted {
		t.Fatalf("empty LoudestHue = %v, %v; want muted, false", hue, ok)
	}
}

func TestMayToast(t *testing.T) {
	now := time.Now().UTC()
	fresh := Notification{ID: "fresh", Source: SourceAgent, CreatedAt: now, ExpiresAt: at(5 * time.Second)}
	stale := Notification{ID: "stale", Source: SourceAgent, CreatedAt: now, ExpiresAt: at(-time.Second)}
	sticky := Notification{ID: "sticky", Source: SourceWaiting, CreatedAt: now, Sticky: true}
	read := Notification{ID: "read", Source: SourceAgent, CreatedAt: now, ExpiresAt: at(5 * time.Second), ReadAt: at(0)}

	for _, tc := range []struct {
		n    Notification
		want bool
	}{{fresh, true}, {stale, false}, {sticky, true}, {read, false}} {
		if got := MayToast(tc.n, now); got != tc.want {
			t.Errorf("MayToast(%s) = %v, want %v", tc.n.ID, got, tc.want)
		}
	}
	if !ToastExpired(stale, now) || ToastExpired(sticky, now) {
		t.Fatalf("ToastExpired disagrees with MayToast")
	}
	toastable := Toastable([]Notification{fresh, stale, sticky, read}, now)
	if len(toastable) != 2 {
		t.Fatalf("Toastable = %d, want 2", len(toastable))
	}
}

func TestNormalizeAppliesSourceDefaults(t *testing.T) {
	now := time.Now().UTC()
	n := Normalize(Notification{Source: SourceAgent, Title: "t"}, now)
	if n.ID == "" || n.Severity != SeverityInfo || n.ExpiresAt == nil || n.Sticky {
		t.Fatalf("agent default: %+v", n)
	}
	w := Normalize(Notification{Source: SourceWaiting, Title: "t"}, now)
	if !w.Sticky || w.ExpiresAt != nil {
		t.Fatalf("waiting has no countdown: %+v", w)
	}
	unknown := Normalize(Notification{Source: "nobody", Title: "t"}, now)
	if unknown.SourceInfo().ID != SourceSystem {
		t.Fatalf("an unknown source falls back to system, got %+v", unknown.SourceInfo())
	}
	if ValidSource("nobody") || !ValidSource(SourceTD) {
		t.Fatalf("ValidSource is wrong")
	}
}

func TestOriginMatchingGovernsDismissal(t *testing.T) {
	shell := Origin{TmuxSession: "sidecar-3", WorkDir: "/repo"}
	other := Origin{TmuxSession: "sidecar-4", WorkDir: "/repo"}
	byDir := Origin{WorkDir: "/repo"}

	mine := Notification{ID: "m", Origin: shell}
	theirs := Notification{ID: "t", Origin: other}
	inApp := Notification{ID: "a"}

	if !MayDismiss(mine, shell) {
		t.Fatalf("a caller may dismiss its own")
	}
	if MayDismiss(theirs, shell) {
		t.Fatalf("a caller may not dismiss another shell's")
	}
	if MayDismiss(inApp, shell) || MayDismiss(mine, Origin{}) {
		t.Fatalf("a zero origin never matches")
	}
	if MayDismiss(mine, byDir) {
		t.Fatalf("a tmux-identified notification is not matched by workdir alone")
	}
	if !MayDismiss(Notification{Origin: byDir}, byDir) {
		t.Fatalf("workdir identity should match when neither side has a session")
	}
}

func TestGroupBySourceFollowsRegistryOrder(t *testing.T) {
	now := time.Now().UTC()
	groups := GroupBySource([]Notification{
		{ID: "1", Source: SourceTasks, CreatedAt: now},
		{ID: "2", Source: SourceWaiting, CreatedAt: now},
		{ID: "3", Source: SourceTasks, CreatedAt: now.Add(time.Second)},
	})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].Source.ID != SourceWaiting {
		t.Fatalf("waiting sorts first, got %s", groups[0].Source.ID)
	}
	if len(groups[1].Items) != 2 || groups[1].Items[0].ID != "3" {
		t.Fatalf("group items must be newest first: %+v", groups[1].Items)
	}
	if groups[1].Unread() != 2 {
		t.Fatalf("group unread = %d, want 2", groups[1].Unread())
	}
}

func TestSourceRegistryHasGlyphAndHueForEverySource(t *testing.T) {
	for _, s := range Sources() {
		if s.Glyph == "" || s.Label == "" || s.Hue == "" || s.Priority == 0 {
			t.Errorf("source %s is incompletely registered: %+v", s.ID, s)
		}
		if ResolveHue(s.Hue) == nil {
			t.Errorf("source %s has an unresolvable hue %q", s.ID, s.Hue)
		}
	}
	if len(SourceIDs()) != len(Sources()) {
		t.Fatalf("SourceIDs and Sources disagree")
	}
}
