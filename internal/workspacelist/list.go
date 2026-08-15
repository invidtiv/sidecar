// Package workspacelist is the presentation component shared by the project
// Workspaces sidebar and the global Workspaces browser.
//
// It owns the parts of a workspace list that both consumers must agree on —
// stable-ID selection, the filter input and its pure multi-field matcher, sort
// modes and their stable ordering, activity grouping, the viewport and its
// scrollbar, and the empty/no-match rows. It owns none of the parts that
// belong to a data owner: it cannot inspect tmux, read Git, switch projects,
// create worktrees, or attach to a terminal. Callers project their own records
// into Item and hand back a row renderer for source-specific metadata.
//
// The rule that keeps this honest: nothing in this package imports a plugin,
// the app, or the inventory. It receives resolved display fields only.
//
// Both consumers render through RenderSidebar. Global Model supplies sort
// sections and a sort action; project Workspaces supplies Shells/Workspaces
// sections and optional creation actions. The component lays out both without
// learning what those actions do.
package workspacelist

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Group is an item's presentation bucket. The buckets are a vertical
// projection of the shared Kanban semantics — they are assigned by the caller
// from resolved agentstatus, not derived here, so this package never becomes a
// second status reducer.
type Group string

const (
	GroupNeedsAttention Group = "Needs Attention"
	GroupWorking        Group = "Working"
	GroupDone           Group = "Done"
	GroupLive           Group = "Live"
	GroupIdle           Group = "Idle"
	GroupPaused         Group = "Paused"
	GroupNoSession      Group = "No Session"
)

// SectionTitle words a list section's heading. Both the project sidebar and the
// global browser render through it so a heading cannot gain a count on one
// surface and lose it on the other.
//
// The Kanban board words its lane headers itself: a column is as narrow as 17
// columns there, which "Needs Attention (0)" overruns, and its label and count
// carry different colours rather than one string.
func SectionTitle(name string, count int) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf("%s (%d)", name, count)
}

// activityOrder is the Activity sort's group order, straight from the plan:
// needs attention, working, done, live plain shells, idle, paused/unavailable,
// then everything with no live session.
var activityOrder = []Group{
	GroupNeedsAttention,
	GroupWorking,
	GroupDone,
	GroupLive,
	GroupIdle,
	GroupPaused,
	GroupNoSession,
}

func groupRank(g Group) int {
	for i, candidate := range activityOrder {
		if candidate == g {
			return i
		}
	}
	return len(activityOrder)
}

// Groups returns the activity group order. Callers render headings in this
// order so a group that gains its first item never appears in a new place.
func Groups() []Group { return append([]Group(nil), activityOrder...) }

// Sort is an explicit user-chosen ordering. Sorting is presentation only: it
// never changes identities, and it never triggers collection.
type Sort uint8

const (
	SortActivity Sort = iota
	SortProject
	SortRecent
	SortName
)

// SortModes is the cycle order behind `s`.
var SortModes = []Sort{SortActivity, SortProject, SortRecent, SortName}

func (s Sort) Label() string {
	switch s {
	case SortProject:
		return "Project"
	case SortRecent:
		return "Recent"
	case SortName:
		return "Name"
	default:
		return "Activity"
	}
}

// Next cycles to the following sort mode.
func (s Sort) Next() Sort {
	for i, mode := range SortModes {
		if mode == s {
			return SortModes[(i+1)%len(SortModes)]
		}
	}
	return SortActivity
}

// Item is one row's resolved display identity. Every field is presentation
// input: the component matches, sorts, groups, and renders from these values
// and reads nothing behind them. Data carries the caller's own record so a
// selection can be resolved back to its owner without this package knowing the
// type.
type Item struct {
	ID           string
	Name         string
	NameMeta     []RowField
	Project      string
	ProjectKey   string
	ProjectOrder int
	Branch       string
	Task         string
	Provider     string
	// TmuxName is an identity key, never rendered. It is searchable so two
	// shells sharing a display name inside one project can be told apart.
	TmuxName string
	Status   string
	Detail   string
	// Kind is a presentation string ("worktree" / "shell"), not a catalog type.
	Kind      string
	Marker    RowMarker
	Group     Group
	ChangedAt time.Time
	Data      any
}

// haystack is the exact field set the filter promises to search: workspace or
// shell name, project, branch, task, provider, tmux session name, and the
// semantic status label.
func (i Item) haystack() string {
	return strings.ToLower(strings.Join([]string{i.Name, i.Project, i.Branch, i.Task, i.Provider, i.TmuxName, i.Status, string(i.Group)}, "\x00"))
}

// Match reports whether an item satisfies a query. Matching is
// case-insensitive and space-separated: every token must appear somewhere in
// the item's promised fields, so "sidecar modal" narrows rather than widens.
// An empty or whitespace-only query matches everything.
func Match(item Item, query string) bool {
	return matchHaystack(item.haystack(), query)
}

func matchHaystack(haystack, query string) bool {
	tokens := strings.FieldsFunc(strings.ToLower(query), unicode.IsSpace)
	for _, token := range tokens {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

// MatchFields is the same matcher for callers that have not built an Item —
// the project sidebar filters its own worktree and shell records this way, so
// both consumers share one definition of "matches".
func MatchFields(query string, fields ...string) bool {
	return matchHaystack(strings.ToLower(strings.Join(fields, "\x00")), query)
}

// Filtered returns the items matching query, preserving input order.
func Filtered(items []Item, query string) []Item {
	if strings.TrimSpace(query) == "" {
		return append([]Item(nil), items...)
	}
	out := make([]Item, 0, len(items))
	for _, item := range items {
		if Match(item, query) {
			out = append(out, item)
		}
	}
	return out
}

// Sorted orders items for presentation. Every mode is stable: equal keys keep
// the caller's order, so an unchanged poll cannot churn the list.
func Sorted(items []Item, mode Sort) []Item {
	out := append([]Item(nil), items...)
	switch mode {
	case SortProject:
		sort.SliceStable(out, func(a, b int) bool {
			if out[a].ProjectOrder != out[b].ProjectOrder {
				return out[a].ProjectOrder < out[b].ProjectOrder
			}
			return false
		})
	case SortRecent:
		sort.SliceStable(out, func(a, b int) bool {
			return out[a].ChangedAt.After(out[b].ChangedAt)
		})
	case SortName:
		sort.SliceStable(out, func(a, b int) bool {
			left, right := strings.ToLower(out[a].Name), strings.ToLower(out[b].Name)
			if left != right {
				return left < right
			}
			return out[a].ProjectOrder < out[b].ProjectOrder
		})
	default:
		sort.SliceStable(out, func(a, b int) bool {
			left, right := groupRank(out[a].Group), groupRank(out[b].Group)
			if left != right {
				return left < right
			}
			if !out[a].ChangedAt.Equal(out[b].ChangedAt) {
				return out[a].ChangedAt.After(out[b].ChangedAt)
			}
			return out[a].ProjectOrder < out[b].ProjectOrder
		})
	}
	return out
}

// Recent time-bucket headings. Empty buckets are omitted.
const (
	RecentNew      = "New"
	RecentToday    = "Today"
	RecentThisWeek = "This week"
	RecentOlder    = "Older"
)

var recentBucketOrder = []string{RecentNew, RecentToday, RecentThisWeek, RecentOlder}

// Section is a rendered group of items with its heading. Title is the heading
// without a count; empty Title means an unheaded run. Group is the Activity
// bucket when that sort is in play.
type Section struct {
	Title string
	Group Group
	Items []Item
}

// Grouped splits sorted items into headed sections. Activity uses the Kanban
// projection; Project one heading per project; Recent the time buckets; Name
// stays a single unheaded run.
func Grouped(items []Item, mode Sort) []Section {
	return GroupedAt(items, mode, time.Time{}, nil)
}

// GroupedAt is Grouped with a clock for Recent buckets and an optional pinned
// identity list. Pinned items that still exist sit in a headed "Pinned"
// section first and are not re-bucketed below. Gone pin IDs are dropped.
func GroupedAt(items []Item, mode Sort, now time.Time, pinnedIDs []string) []Section {
	if now.IsZero() {
		now = time.Now()
	}
	pinned, rest := splitPinned(items, pinnedIDs)
	var sections []Section
	switch mode {
	case SortProject:
		sections = groupByProject(rest)
	case SortRecent:
		sections = groupByRecent(rest, now)
	case SortName:
		if len(rest) > 0 {
			sections = []Section{{Items: rest}}
		}
	default:
		sections = groupByActivity(rest)
	}
	if len(pinned) > 0 {
		return append([]Section{{Title: "Pinned", Items: pinned}}, sections...)
	}
	return sections
}

func splitPinned(items []Item, pinnedIDs []string) (pinned, rest []Item) {
	if len(pinnedIDs) == 0 {
		return nil, items
	}
	index := make(map[string]Item, len(items))
	for _, item := range items {
		index[item.ID] = item
	}
	seen := make(map[string]bool, len(pinnedIDs))
	for _, id := range pinnedIDs {
		item, ok := index[id]
		if !ok || seen[id] {
			continue
		}
		pinned = append(pinned, item)
		seen[id] = true
	}
	if len(pinned) == 0 {
		return nil, items
	}
	rest = make([]Item, 0, len(items)-len(pinned))
	for _, item := range items {
		if !seen[item.ID] {
			rest = append(rest, item)
		}
	}
	return pinned, rest
}

func groupByActivity(items []Item) []Section {
	var sections []Section
	for _, group := range activityOrder {
		var members []Item
		for _, item := range items {
			if item.Group == group {
				members = append(members, item)
			}
		}
		if len(members) > 0 {
			sections = append(sections, Section{Title: string(group), Group: group, Items: members})
		}
	}
	// An item whose caller invented a group still has to appear somewhere.
	var others []Item
	for _, item := range items {
		if groupRank(item.Group) == len(activityOrder) {
			others = append(others, item)
		}
	}
	if len(others) > 0 {
		sections = append(sections, Section{Group: Group(""), Items: others})
	}
	return sections
}

func groupByProject(items []Item) []Section {
	var sections []Section
	var current int
	var key string
	started := false
	for _, item := range items {
		itemKey := item.ProjectKey
		if itemKey == "" {
			itemKey = item.Project
		}
		if !started || itemKey != key {
			title := item.Project
			if title == "" {
				title = item.ProjectKey
			}
			sections = append(sections, Section{Title: title})
			current = len(sections) - 1
			key = itemKey
			started = true
		}
		sections[current].Items = append(sections[current].Items, item)
	}
	return sections
}

func groupByRecent(items []Item, now time.Time) []Section {
	buckets := make(map[string][]Item, len(recentBucketOrder))
	for _, item := range items {
		name := recentBucket(item.ChangedAt, now)
		buckets[name] = append(buckets[name], item)
	}
	var sections []Section
	for _, name := range recentBucketOrder {
		members := buckets[name]
		if len(members) == 0 {
			continue
		}
		sections = append(sections, Section{Title: name, Items: members})
	}
	return sections
}

func recentBucket(changedAt, now time.Time) string {
	if changedAt.IsZero() {
		return RecentOlder
	}
	age := now.Sub(changedAt)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Hour:
		return RecentNew
	case age < 24*time.Hour:
		return RecentToday
	case age < 7*24*time.Hour:
		return RecentThisWeek
	default:
		return RecentOlder
	}
}
