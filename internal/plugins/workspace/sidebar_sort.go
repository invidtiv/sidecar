package workspace

import (
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/workspacelist"
)

// Sorting the project sidebar means answering a question the list has never had
// to answer: what happens to a shell that lives inside a worktree?
//
// The structural view draws it as a child, which is true and useful while the
// order is structural. It stops being either as soon as the order is computed.
// "Most recent first" over a tree has no honest reading — a parent cannot be
// both above its children and in its own recency position — and the case the
// Activity order exists to serve is precisely a blocked shell buried three rows
// under an idle worktree. Keeping it nested would hide the row the sort was
// asked to surface.
//
// So a computed order flattens: every shell and every worktree becomes a peer,
// and a nested shell carries its worktree the way a global row carries its
// project. Nesting returns with Manual. This is also what makes the two
// sidebars converge — under a computed sort the project list has the same shape
// as the global one, which is the whole point of sorting them alike.

// projectSortModes is what the project sidebar offers. Project is absent
// because there is only one project here; Manual is present because this list
// owns a real structural order, which global inventory does not.
var projectSortModes = []workspacelist.Sort{
	workspacelist.SortManual,
	workspacelist.SortActivity,
	workspacelist.SortRecent,
	workspacelist.SortName,
}

// navSortKey is the resolved ordering identity of one row. Every field is
// already-resolved presentation state; nothing here reads tmux or Git.
type navSortKey struct {
	item      sidebarNavItem
	group     workspacelist.Group
	changedAt time.Time
	name      string
	// order is the row's position in the structural list, and is the tiebreak
	// for every mode. Equal keys therefore never churn between polls.
	order int
}

// navSortKeyFor resolves one item's ordering identity.
func (p *Plugin) navSortKeyFor(item sidebarNavItem, order int) navSortKey {
	key := navSortKey{item: item, order: order}
	switch item.kind {
	case navKindShell:
		shell := p.shells[item.shellIdx]
		key.name = shell.Name
		key.group = workspacelist.GroupForLane(string(shellAgentStatusPresentation(shell).Lane))
		key.changedAt = shellChangedAt(shell)
		if shell.Agent == nil {
			// A shell with no session is not idle, it is absent. Grouping it
			// with live-but-quiet agents would put a dead row above a working
			// one under the Activity order.
			key.group = workspacelist.GroupNoSession
		}
	case navKindNestedShell:
		key.name = item.shell.Name
		key.group = workspacelist.GroupForLane(string(shellAgentStatusPresentation(item.shell).Lane))
		key.changedAt = shellChangedAt(item.shell)
		if item.shell.Agent == nil {
			key.group = workspacelist.GroupNoSession
		}
	default:
		wt := p.worktrees[item.worktreeIdx]
		key.name = wt.Name
		key.changedAt = worktreeChangedAt(wt)
		if wt.Agent == nil {
			key.group = workspacelist.GroupNoSession
		} else {
			key.group = workspacelist.GroupForLane(string(agentStatusPresentation(wt).Lane))
		}
	}
	return key
}

// flatNavKeys is every visible row as a peer, in structural order, with its
// ordering identity resolved.
func (p *Plugin) flatNavKeys() []navSortKey {
	keys := make([]navSortKey, 0, len(p.shells)+len(p.worktrees)+p.nestedShellTotal())
	for _, section := range p.manualNavSections() {
		for _, item := range section.items {
			keys = append(keys, p.navSortKeyFor(item, len(keys)))
		}
	}
	return keys
}

// sortedNavSections builds the computed orders. The section headings match the
// global list's for the same mode, so a user who has learned one has learned
// the other.
func (p *Plugin) sortedNavSections() []sidebarNavSection {
	keys := p.flatNavKeys()
	if len(keys) == 0 {
		return nil
	}
	switch p.listSort {
	case workspacelist.SortName:
		sort.SliceStable(keys, func(a, b int) bool {
			return lessByName(keys[a], keys[b])
		})
		return []sidebarNavSection{{items: navItems(keys)}}
	case workspacelist.SortRecent:
		sort.SliceStable(keys, func(a, b int) bool {
			return lessByRecent(keys[a], keys[b])
		})
		return recentNavSections(keys, time.Now())
	default:
		sort.SliceStable(keys, func(a, b int) bool {
			return lessByActivity(keys[a], keys[b])
		})
		return activityNavSections(keys)
	}
}

func lessByName(a, b navSortKey) bool {
	if left, right := strings.ToLower(a.name), strings.ToLower(b.name); left != right {
		return left < right
	}
	return a.order < b.order
}

func lessByRecent(a, b navSortKey) bool {
	// A row with no observed time has nothing to be recent about, so it sinks
	// rather than claiming the top of the list by way of the zero value.
	if a.changedAt.IsZero() != b.changedAt.IsZero() {
		return !a.changedAt.IsZero()
	}
	if !a.changedAt.Equal(b.changedAt) {
		return a.changedAt.After(b.changedAt)
	}
	return a.order < b.order
}

func lessByActivity(a, b navSortKey) bool {
	if left, right := groupRank(a.group), groupRank(b.group); left != right {
		return left < right
	}
	if !a.changedAt.Equal(b.changedAt) {
		return a.changedAt.After(b.changedAt)
	}
	return a.order < b.order
}

// groupRank mirrors workspacelist's activity order so the project sidebar
// cannot rank the same lanes differently from the global list.
func groupRank(g workspacelist.Group) int {
	for i, candidate := range workspacelist.Groups() {
		if candidate == g {
			return i
		}
	}
	return len(workspacelist.Groups())
}

func activityNavSections(keys []navSortKey) []sidebarNavSection {
	sections := make([]sidebarNavSection, 0, len(workspacelist.Groups()))
	for _, group := range workspacelist.Groups() {
		var members []sidebarNavItem
		for _, key := range keys {
			if key.group == group {
				members = append(members, key.item)
			}
		}
		if len(members) > 0 {
			sections = append(sections, sidebarNavSection{title: string(group), items: members})
		}
	}
	return sections
}

func recentNavSections(keys []navSortKey, now time.Time) []sidebarNavSection {
	buckets := map[string][]sidebarNavItem{}
	for _, key := range keys {
		buckets[recentBucketName(key.changedAt, now)] = append(buckets[recentBucketName(key.changedAt, now)], key.item)
	}
	order := []string{workspacelist.RecentNew, workspacelist.RecentToday, workspacelist.RecentThisWeek, workspacelist.RecentOlder}
	sections := make([]sidebarNavSection, 0, len(order))
	for _, name := range order {
		if members := buckets[name]; len(members) > 0 {
			sections = append(sections, sidebarNavSection{title: name, items: members})
		}
	}
	return sections
}

// recentBucketName matches the global list's time buckets exactly.
func recentBucketName(changedAt, now time.Time) string {
	if changedAt.IsZero() {
		return workspacelist.RecentOlder
	}
	age := now.Sub(changedAt)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Hour:
		return workspacelist.RecentNew
	case age < 24*time.Hour:
		return workspacelist.RecentToday
	case age < 7*24*time.Hour:
		return workspacelist.RecentThisWeek
	default:
		return workspacelist.RecentOlder
	}
}

func navItems(keys []navSortKey) []sidebarNavItem {
	items := make([]sidebarNavItem, 0, len(keys))
	for _, key := range keys {
		items = append(items, key.item)
	}
	return items
}
