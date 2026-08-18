package keymap_test

import (
	"testing"

	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/resourceview"
)

// The Resource leaf is one component bound by two surfaces. These guards exist
// because the two host bindings were written independently and each invented
// its own command IDs for the same keys — one chose "cycle-tab", the other
// "resource-{/}" — which the footer resolves through the keymap, so neither
// surface would have shown a hint. resourceview now declares the IDs; this is
// what stops a host quietly reintroducing its own.
const (
	projectResourceContext = "workspace-resource"
	globalResourceContext  = "global-workspaces-resource"
)

func bindingsFor(t *testing.T, context string) map[string]string {
	t.Helper()
	byCommand := map[string]string{}
	for _, b := range keymap.DefaultBindings() {
		if b.Context == context {
			byCommand[b.Command] = b.Key
		}
	}
	if len(byCommand) == 0 {
		t.Fatalf("context %q has no bindings at all", context)
	}
	return byCommand
}

func TestResourceCommandsAreBoundOnBothSurfaces(t *testing.T) {
	project := bindingsFor(t, projectResourceContext)
	global := bindingsFor(t, globalResourceContext)

	for _, cmd := range resourceview.Commands() {
		projectKey, okProject := project[cmd.ID]
		if !okProject {
			t.Errorf("%s: command %q has no binding, so its footer hint renders nothing",
				projectResourceContext, cmd.ID)
		}
		globalKey, okGlobal := global[cmd.ID]
		if !okGlobal {
			t.Errorf("%s: command %q has no binding, so its footer hint renders nothing",
				globalResourceContext, cmd.ID)
		}
		if okProject && okGlobal && projectKey != globalKey {
			t.Errorf("command %q is %q on the project surface and %q on the global one",
				cmd.ID, projectKey, globalKey)
		}
		if okProject && projectKey != cmd.Key {
			t.Errorf("command %q documents key %q but is bound to %q on the project surface",
				cmd.ID, cmd.Key, projectKey)
		}
	}
}

// Leaving a Resource pane is each surface's own close/hide rule, but both must
// offer a way out or the pane is a trap.
func TestBothResourceSurfacesCanBeLeft(t *testing.T) {
	for _, context := range []string{projectResourceContext, globalResourceContext} {
		byCommand := bindingsFor(t, context)
		if _, ok := byCommand["close"]; !ok {
			t.Errorf("%s: no close binding, so the pane cannot be left", context)
		}
	}
}
