package tasks

import (
	"sort"
	"strings"
	"sync"

	"github.com/marcus/sidecar/internal/keymap"

	tasksui "github.com/marcus/tasks/pkg/tui"
)

// This file derives sidecar's key-routing metadata for the Tasks tab from
// Tasks' own exported registry. Nothing here is a second copy of the Tasks key
// table: the per-context key map is computed from ExportContexts and
// ExportBindings, so a context or binding added in a later Tasks release routes
// without a matching edit here.
//
// The two things that are stated rather than derived are policy, not data:
// which sidecar globals a Tasks binding may shadow (shadowableGlobals — a
// decision, taken in the plan's conflict table) and which contexts are safe to
// treat as non-overlay (rootContexts — a judgement Tasks does not export, made
// in the direction that fails safe). Both say so, and both are guarded by tests
// that fail if Tasks moves underneath them.

// hostOwnedCommands are Tasks commands sidecar deliberately does not adopt.
//
// Quit belongs to the host: an embedded Tasks must never end the sidecar
// session, and Tasks is built with SuppressQuit for the same reason. Help
// belongs to the host too — `?` opens sidecar's merged contextual palette,
// which lists every Tasks command as well, so Tasks' own shortcut sheet would
// be a second, narrower help surface bound to the same key.
//
// They are filtered out of binding registration, the palette projection, and
// key claiming alike, so the three surfaces cannot disagree about them.
var hostOwnedCommands = map[string]bool{
	"quit":                       true,
	"quit-confirmation-reminder": true,
	"open-help":                  true,
}

// hostReservedKeys are never claimed by the plugin from a non-overlay context,
// whatever Tasks binds them to.
//
// The guarantee itself lives in the host: pluginClaimsKey refuses
// keymap.HostReservedKeys at precedence level 3 regardless of what any router
// claims, because a non-negotiable enforced only by plugin goodwill is not
// enforced. This alias is defence in depth on top of that, and being the same
// variable is what keeps the two layers from drifting apart.
//
// Filtering hostOwnedCommands already covers today's registry; this is the
// belt to that pair of braces. If a future Tasks release binds `q` in the list
// context to something new, sidecar's quit flow still wins there, because a
// key that can end the session is not something a tab may quietly take over.
var hostReservedKeys = keymap.HostReservedKeys

// shadowableGlobals are the sidecar globals a Tasks binding is allowed to take
// over. They are exactly the keys the plan's conflict table decided
// (docs/plans/active/tasks-in-sidecar.md § 1.4): `?`, `@`, `1`-`6`, `q`, `tab`,
// `M`, `A` — of which `?`, `@`, `1`-`6` and `q` are the ones sidecar also binds.
// `?` and `q` are resolved the other way in that table (and are host-reserved
// besides), so what is left for Tasks is `@` and the view keys.
//
// The rule, and the reason for it: a Tasks binding may shadow a sidecar global
// only if that collision was decided. Every other collision is an accident of
// two independently grown key tables, and sidecar wins it — `K`, `W` and `#`
// are the live examples. They were never in the conflict table, and letting
// Tasks take them meant the theme switcher (`#`) became delete-selected the
// moment a task was selected. A destructive command hiding behind a key whose
// meaning depends on the selection is not a mapping anyone chose.
//
// The Tasks commands behind those keys stay reachable through `?` and the
// palette, which is what the merged help is for.
var shadowableGlobals = map[string]bool{
	"@": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true,
}

// rootContexts are the Tasks contexts where sidecar's global keys may fire and
// `q` may reach the host quit flow: the ordinary browsing layers with nothing
// overlaid on them.
//
// This is an allow-list on purpose. The previous version inferred it — "does
// not consume text input and does not bind `q`" — which fails dangerous: a
// future Tasks overlay that is neither (a read-only preview, a diff viewer, a
// y/n confirmation that uses `esc` rather than `q`) would be classified as root,
// so sidecar globals would fire underneath a visible overlay and `q` would pop
// the quit confirmation on top of it.
//
// Deriving it from something Tasks asserts would be better, but pkg/tui cannot
// express it today: ContextMetadata carries only Name and ConsumesTextInput,
// with no "is an overlay" bit. Until it does, the conservative direction is the
// one that costs a keystroke rather than the session, so anything unknown or
// ambiguous — including every context a future Tasks release adds — is treated
// as an overlay. The failure mode of a wrong guess is then "a sidecar global
// did not fire in a Tasks view", which is visible and harmless, instead of
// "sidecar quit out from under an open overlay".
//
// contextsAreKnown() checks these names still exist in Tasks' exported
// contexts, so a rename shows up as a test failure rather than as four contexts
// silently demoted to overlays.
var rootContexts = map[string]bool{
	string(tasksui.FocusList):           true,
	string(tasksui.FocusDetail):         true,
	string(tasksui.FocusResponse):       true,
	string(tasksui.FocusResponseDetail): true,
}

type routingTable struct {
	known     map[string]bool
	textInput map[string]bool
	root      map[string]bool
	// byContext maps context -> key -> command IDs, excluding host-owned
	// commands.
	byContext map[string]map[string][]string
}

var (
	routingOnce sync.Once
	routing     routingTable
)

func routingData() routingTable {
	routingOnce.Do(func() {
		routing = buildRoutingTable()
	})
	return routing
}

func buildRoutingTable() routingTable {
	table := routingTable{
		known:     map[string]bool{},
		textInput: map[string]bool{},
		root:      map[string]bool{},
		byContext: map[string]map[string][]string{},
	}

	for _, meta := range tasksui.ExportContexts() {
		name := string(meta.Name)
		table.known[name] = true
		if meta.ConsumesTextInput {
			table.textInput[name] = true
		}
	}

	for _, binding := range tasksui.ExportBindings() {
		context := string(binding.Context)
		table.known[context] = true
		if hostOwnedCommands[binding.CommandID] {
			continue
		}
		keys := table.byContext[context]
		if keys == nil {
			keys = map[string][]string{}
			table.byContext[context] = keys
		}
		keys[binding.Key] = append(keys[binding.Key], binding.CommandID)
	}

	// Root-ness is an allow-list intersected with what Tasks actually exports:
	// a name that no longer exists is not a context anyone can be in, and a
	// text-input context can never be root whatever the list says.
	for context := range rootContexts {
		if table.known[context] && !table.textInput[context] {
			table.root[context] = true
		}
	}
	return table
}

// contextsAreKnown reports which of the named contexts Tasks still exports. It
// exists for the test that guards rootContexts against a Tasks-side rename.
func contextsAreKnown(names map[string]bool) (missing []string) {
	table := routingData()
	for name := range names {
		if !table.known[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// IsTasksContext reports whether a sidecar focus context belongs to the Tasks
// tab.
func IsTasksContext(context string) bool {
	return context == pluginID || strings.HasPrefix(context, pluginID+"-")
}

// IsTextInputContext reports whether a Tasks context routes printable keys into
// an editor widget. It is derived from Tasks' exported context metadata rather
// than from a list maintained here.
func IsTextInputContext(context string) bool {
	return routingData().textInput[context]
}

// IsRootContext reports whether `q` in a Tasks context should reach sidecar's
// quit flow rather than the tab. Unknown contexts are not root: see
// rootContexts.
func IsRootContext(context string) bool {
	return routingData().root[context]
}

// mayShadowGlobal reports whether Tasks is allowed to claim a key from
// sidecar's global switch at all. Keys sidecar does not bind globally are not
// its business, so they are always claimable.
func mayShadowGlobal(key string) bool {
	return !keymap.GlobalKeys[key] || shadowableGlobals[key]
}

// claimIsUnconditional reports whether a claimed key's availability must not be
// consulted.
//
// Availability-awareness is good where it lets an idle Tasks key fall through
// to sidecar — `r` claims nothing when there is no proposal to reject, so
// sidecar's refresh still works. It is bad on a key sidecar also binds: there,
// availability would make the SAME key in the SAME view mean the Tasks command
// or the sidecar global depending on whether something is selected. A claim on
// a sidecar global is therefore all-or-nothing per context, which is what makes
// the claimed set predictable.
func claimIsUnconditional(key string) bool {
	return keymap.GlobalKeys[key]
}

// commandsForKey returns the non-host-owned Tasks commands bound to a key in a
// context, in registry order. Tasks binds several conditional commands to one
// key (`r` is both reject-proposal and open-recur-popup); the caller decides
// with CommandAvailable which, if any, is live right now.
func commandsForKey(context, key string) []string {
	keys := routingData().byContext[context]
	if keys == nil {
		return nil
	}
	return keys[key]
}
