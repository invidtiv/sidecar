package app

import (
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/tasks"
)

// GlobalDescriptors is the ordered list of global-scope plugin descriptors the
// app shell hosts. The shell builds one globalPluginHost per enabled entry, and
// the header row follows the same order.
//
// It is here rather than read from internal/plugins/assembly because the plugin
// packages import this one, so an import of the assembly would be a cycle. The
// descriptor values themselves are the single source of truth — this list and
// assembly's catalog both call the same tasks.Descriptor() — and
// TestGlobalDescriptorsMatchTheAssemblyCatalog in internal/plugins/assembly
// fails if the two lists ever disagree.
func GlobalDescriptors() []plugin.Descriptor {
	return []plugin.Descriptor{tasks.Descriptor()}
}
