package workspacelist

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPureProjectionSortsOnlyUnpinnedRemainder(t *testing.T) {
	for _, tc := range []struct {
		mode Sort
		want string
	}{
		{SortActivity, "b,c,a,d"},
		{SortProject, "b,a,c,d"},
		{SortRecent, "b,a,c,d"},
		{SortName, "b,a,d,c"},
		{SortManual, "b,a,c,d"},
	} {
		t.Run(tc.mode.Label(), func(t *testing.T) {
			var ids []string
			for _, item := range Projected(items(), "", tc.mode, []string{"b"}) {
				ids = append(ids, item.ID)
			}
			if got := strings.Join(ids, ","); got != tc.want {
				t.Fatalf("order = %s, want %s", got, tc.want)
			}
		})
	}
}

// A headless consumer has to get the same filtered pin order and section
// membership as the list without constructing a terminal UI model.
func TestPureProjectionMatchesModelAndKeepsFilteredPinsOut(t *testing.T) {
	rows := items()
	pins := []string{"d", "b", "a", "b", "missing"}
	before := append([]Item(nil), rows...)
	for _, mode := range SortModes {
		t.Run(mode.Label(), func(t *testing.T) {
			got := Projected(rows, "SIDECAR", mode, pins)
			if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
				t.Fatalf("projection = %+v, want matching pins b,a exactly once", got)
			}
			var m Model
			m.SetSort(mode)
			m.SetPinned(pins)
			m.Filter().SetQuery("SIDECAR")
			m.SetItems(rows)
			if !reflect.DeepEqual(m.Visible(), got) {
				t.Fatalf("model = %+v, pure projection = %+v", m.Visible(), got)
			}
			sections := SectionsAt(got, mode, rows[0].ChangedAt, pins)
			if len(sections) != 1 || sections[0].Title != "Pinned" || !reflect.DeepEqual(sections[0].Items, got) {
				t.Fatalf("sections = %+v, want only the matching pinned rows", sections)
			}
		})
	}
	if !reflect.DeepEqual(rows, before) || !reflect.DeepEqual(pins, []string{"d", "b", "a", "b", "missing"}) {
		t.Fatal("projection changed the caller's catalog or pins")
	}
	projected := Projected(rows, "sidecar", SortName, pins)
	projected[0].Name = "caller edit"
	if !reflect.DeepEqual(rows, before) {
		t.Fatal("projection returned the caller's mutable item slice")
	}
}

func TestPurePinsDoNotMutateCallerAndPruneOnlyKnownIdentity(t *testing.T) {
	pins := []string{"a", "b", "c"}
	for _, tc := range []struct {
		name string
		got  []string
		want []string
	}{
		{"remove middle", TogglePinned(pins, "b"), []string{"a", "c"}},
		{"append new", TogglePinned(pins, "d"), []string{"a", "b", "c", "d"}},
		{"empty target", TogglePinned(pins, ""), pins},
		{"normalize", NormalizePins([]string{"a", "", "b", "a", "missing"}), []string{"a", "b", "missing"}},
		{"retain order", RetainPins(pins, []string{"c", "a"}), []string{"a", "c"}},
		{"empty catalog", RetainPins(pins, nil), []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Fatalf("pins = %v, want %v", tc.got, tc.want)
			}
			if len(tc.got) > 0 {
				tc.got[0] = "changed"
			}
			if !reflect.DeepEqual(pins, []string{"a", "b", "c"}) {
				t.Fatal("pin operation modified caller's input")
			}
		})
	}
}

func TestPureVisibilityPreservesRevealAndRemoteHomeRules(t *testing.T) {
	for _, tc := range []struct {
		name       string
		group      Group
		visibility Visibility
		wantHidden bool
	}{
		{"idle hidden", GroupNoSession, Visibility{}, true},
		{"idle shown", GroupNoSession, Visibility{ShowIdleWorktrees: true}, false},
		{"agent idle stays", GroupIdle, Visibility{}, false},
		{"plain live stays", GroupLive, Visibility{}, false},
		{"remote home hidden even live", GroupLive, Visibility{ShowIdleWorktrees: true, RemoteMainWithoutAgent: true}, true},
		{"revealed idle stays", GroupNoSession, Visibility{Revealed: true}, false},
		{"revealed remote home stays", GroupNoSession, Visibility{Revealed: true, RemoteMainWithoutAgent: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Hidden(tc.group, tc.visibility); got != tc.wantHidden {
				t.Fatalf("hidden = %v, want %v", got, tc.wantHidden)
			}
		})
	}
}

func TestPureGroupingUsesOnlySuppliedClock(t *testing.T) {
	rows := []Item{{ID: "old", ChangedAt: time.Time{}.Add(2 * time.Hour)}, {ID: "unknown"}}
	sections := SectionsAt(rows, SortRecent, time.Time{}, nil)
	if len(sections) != 2 || sections[0].Title != RecentNew || sections[0].Items[0].ID != "old" || sections[1].Title != RecentOlder {
		t.Fatalf("zero observation time fell back to wall clock: %+v", sections)
	}
	observed := rows[0].ChangedAt.Add(48 * time.Hour)
	sections = SectionsAt(rows, SortRecent, observed, nil)
	if len(sections) != 2 || sections[0].Title != RecentThisWeek || sections[1].Title != RecentOlder {
		t.Fatalf("grouping ignored explicit observation time: %+v", sections)
	}
}
