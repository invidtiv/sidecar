package tasks

import (
	"strings"
	"sync"

	tasksui "github.com/marcus/tasks/pkg/tui"
)

// This file derives sidecar's key-routing metadata for the Tasks tab from
// Tasks' own exported registry. Nothing here is a second copy of the Tasks
// key table: every set below is computed from ExportContexts/ExportBindings,
// so a context or binding added in a later Tasks release routes correctly
// without a matching edit here.

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
// Filtering hostOwnedCommands already covers today's registry; this is the
// belt to that pair of braces. If a future Tasks release binds `q` in the list
// context to something new, sidecar's quit flow still wins there, because a
// key that can end the session is not something a tab may quietly take over.
var hostReservedKeys = map[string]bool{
	"ctrl+c": true,
	"q":      true,
	"?":      true,
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

	// A context is "root" for sidecar's purposes when `q` there means nothing
	// to Tasks except its own quit — that is where the host's quit flow may
	// take the key. A context that binds `q` to something of its own (a modal
	// dismissal, say) is an overlay, and the host must not quit out from under
	// it.
	usesQuitKey := map[string]bool{}
	for _, binding := range tasksui.ExportBindings() {
		context := string(binding.Context)
		table.known[context] = true
		if hostOwnedCommands[binding.CommandID] {
			continue
		}
		if binding.Key == "q" {
			usesQuitKey[context] = true
		}
		keys := table.byContext[context]
		if keys == nil {
			keys = map[string][]string{}
			table.byContext[context] = keys
		}
		keys[binding.Key] = append(keys[binding.Key], binding.CommandID)
	}

	for context := range table.known {
		if !table.textInput[context] && !usesQuitKey[context] {
			table.root[context] = true
		}
	}
	return table
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
// quit flow rather than the tab.
func IsRootContext(context string) bool {
	return routingData().root[context]
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
