package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/plugin"
)

// footerStatusPlugin is a plugin that suppresses its own footer and therefore
// depends on the host to keep one condition visible.
type footerStatusPlugin struct {
	routerTestPlugin
	status  string
	isError bool
}

func (p *footerStatusPlugin) FooterStatus() (string, bool) { return p.status, p.isError }

// The unified footer selects the plugin's highest-priority commands for the
// current context, in priority order, using the keys the plugin registered.
func TestUnifiedFooterSelectsPluginCommandsByPriority(t *testing.T) {
	p := newRouterPlugin()
	p.commands = []plugin.Command{
		{ID: "select-next", Name: "Select", Context: "tasks-list", Priority: 4},
		{ID: "focus-prompt", Name: "Ask", Context: "tasks-list", Priority: 1},
		{ID: "start-filter", Name: "Filter", Context: "tasks-list", Priority: 2},
		{ID: "modal-confirm", Name: "Confirm", Context: "tasks-modal", Priority: 1},
		{ID: "no-binding", Name: "Unbound", Context: "tasks-list", Priority: 1},
	}
	m := routerTestModel(t, p)
	for _, b := range []struct{ key, cmd string }{
		{"j", "select-next"},
		{"tab", "focus-prompt"},
		{"/", "start-filter"},
		{"enter", "modal-confirm"},
	} {
		m.keymap.RegisterPluginBinding(b.key, b.cmd, "tasks-list")
	}
	m.keymap.RegisterPluginBinding("enter", "modal-confirm", "tasks-modal")

	hints := m.pluginFooterHints(p, "tasks-list")

	var labels []string
	for _, hint := range hints {
		labels = append(labels, hint.keys+":"+hint.label)
	}
	want := []string{"tab:Ask", "/:Filter", "j:Select"}
	if len(labels) != len(want) {
		t.Fatalf("footer hints = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("footer hints = %v, want %v", labels, want)
		}
	}
}

// Hints for another context never leak into the footer: the tab's footer must
// describe the layer the keyboard is actually in.
func TestUnifiedFooterFollowsTheFocusContext(t *testing.T) {
	p := newRouterPlugin()
	p.context = "tasks-modal"
	p.commands = []plugin.Command{
		{ID: "select-next", Name: "Select", Context: "tasks-list", Priority: 1},
		{ID: "modal-confirm", Name: "Confirm", Context: "tasks-modal", Priority: 1},
	}
	m := routerTestModel(t, p)
	m.keymap.RegisterPluginBinding("j", "select-next", "tasks-list")
	m.keymap.RegisterPluginBinding("y", "modal-confirm", "tasks-modal")

	hints := m.pluginFooterHints(p, m.activeContext)

	if len(hints) != 1 || hints[0].label != "Confirm" {
		t.Fatalf("footer hints = %+v, want only the modal context's Confirm", hints)
	}
}

// The footer truncates rather than wrapping: a narrow terminal keeps the
// highest-priority hints and drops the rest.
func TestFooterHintsTruncateToWidth(t *testing.T) {
	hints := []footerHint{
		{keys: "tab", label: "Ask"},
		{keys: "/", label: "Filter"},
		{keys: "j", label: "Select"},
	}

	full := ansi.Strip(renderHintLineTruncated(hints, 200))
	if !strings.Contains(full, "Ask") || !strings.Contains(full, "Select") {
		t.Fatalf("a wide footer should show every hint, got %q", full)
	}

	narrow := ansi.Strip(renderHintLineTruncated(hints, len("tab Ask")+2))
	if !strings.Contains(narrow, "Ask") {
		t.Fatalf("the highest-priority hint must survive truncation, got %q", narrow)
	}
	if strings.Contains(narrow, "Select") {
		t.Fatalf("a narrow footer must drop low-priority hints, got %q", narrow)
	}
	if strings.Contains(narrow, "\n") {
		t.Fatalf("the footer must stay one line, got %q", narrow)
	}
	if got := renderHintLineTruncated(hints, 0); got != "" {
		t.Fatalf("no width means no hints, got %q", got)
	}
}

// A plugin that suppresses its own footer can still put a condition in the
// host's. This is what keeps the Tasks store-read failure visible once Tasks
// stops painting its own banner.
func TestPluginFooterStatusIsRendered(t *testing.T) {
	p := &footerStatusPlugin{status: "tasks: cannot read the task store", isError: true}
	p.context = "tasks-list"
	p.claims = map[string]bool{}
	m := routerTestModel(t, p)

	footer := ansi.Strip(m.renderFooter())
	if !strings.Contains(footer, "cannot read the task store") {
		t.Fatalf("the plugin's condition is missing from the footer:\n%s", footer)
	}
}

// A transient toast outranks a standing plugin condition, and gets the slot
// back when it expires.
//
// The plugin condition is true until someone fixes it, so it can wait a couple
// of seconds. A toast cannot: it is sidecar's only surface for something that
// just happened ("Update installed — restart required"), and ranking it below
// the plugin meant every toast raised on the Tasks tab was dropped silently for
// as long as the Tasks store stayed unreadable.
func TestAToastOutranksAPluginFooterStatus(t *testing.T) {
	p := &footerStatusPlugin{status: "tasks: cannot read the task store", isError: true}
	p.context = "tasks-list"
	p.claims = map[string]bool{}
	m := routerTestModel(t, p)
	m.ui.ToastMessage = "saved"
	m.ui.ToastExpiry = time.Now().Add(time.Minute)
	if !m.ui.HasToast() {
		t.Fatal("test setup: no live toast")
	}

	footer := ansi.Strip(m.renderFooter())
	if !strings.Contains(footer, "saved") {
		t.Fatalf("the plugin's standing condition swallowed a live toast:\n%s", footer)
	}

	// And the standing condition comes back the moment the toast expires.
	m.ui.ToastExpiry = time.Now().Add(-time.Minute)
	footer = ansi.Strip(m.renderFooter())
	if !strings.Contains(footer, "cannot read the task store") {
		t.Fatalf("the plugin's condition did not return after the toast expired:\n%s", footer)
	}
}

// A healthy plugin says nothing, and the footer behaves exactly as before.
func TestSilentPluginFooterStatusChangesNothing(t *testing.T) {
	p := &footerStatusPlugin{}
	p.context = "tasks-list"
	p.claims = map[string]bool{}
	m := routerTestModel(t, p)
	m.statusMsg = "committed"

	footer := ansi.Strip(m.renderFooter())
	if !strings.Contains(footer, "committed") {
		t.Fatalf("an empty plugin status must not displace the host's own:\n%s", footer)
	}
}
