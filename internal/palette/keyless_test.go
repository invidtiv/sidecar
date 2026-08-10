package palette

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

// stubPlugin is the minimum Plugin the palette needs: a list of commands.
type stubPlugin struct {
	id       string
	commands []plugin.Command
}

func (p *stubPlugin) ID() string                              { return p.id }
func (p *stubPlugin) Name() string                            { return p.id }
func (p *stubPlugin) Icon() string                            { return "S" }
func (p *stubPlugin) Init(*plugin.Context) error              { return nil }
func (p *stubPlugin) Start() tea.Cmd                          { return nil }
func (p *stubPlugin) Stop()                                   {}
func (p *stubPlugin) Update(tea.Msg) (plugin.Plugin, tea.Cmd) { return p, nil }
func (p *stubPlugin) View(width, height int) string           { return "" }
func (p *stubPlugin) IsFocused() bool                         { return true }
func (p *stubPlugin) SetFocused(bool)                         {}
func (p *stubPlugin) Commands() []plugin.Command              { return p.commands }
func (p *stubPlugin) FocusContext() string                    { return "demo" }
func noopHandler() func() tea.Cmd                             { return func() tea.Cmd { return nil } }

// A command sidecar's key ladder refused a binding for still reaches the
// palette, keyless, with its own name and description.
func TestKeylessCommandBecomesAPaletteEntry(t *testing.T) {
	km := keymap.NewRegistry()
	km.RegisterPluginBinding("j", "select-next", "demo")

	p := &stubPlugin{id: "demo", commands: []plugin.Command{
		{ID: "select-next", Name: "Select", Description: "move down", Context: "demo", Handler: noopHandler()},
		{ID: "view-inbox", Name: "Jump", Description: "jump to Inbox view", Context: "demo", Handler: noopHandler()},
	}}

	entries := BuildEntries(km, []plugin.Plugin{p}, "demo", "demo")

	byID := map[string]PaletteEntry{}
	for _, e := range entries {
		if _, dup := byID[e.CommandID]; dup {
			t.Fatalf("duplicate entry for %q", e.CommandID)
		}
		byID[e.CommandID] = e
	}

	keyed, ok := byID["select-next"]
	if !ok {
		t.Fatal("the bound command lost its entry")
	}
	if keyed.Key != "j" {
		t.Errorf("bound command key = %q, want j", keyed.Key)
	}

	keyless, ok := byID["view-inbox"]
	if !ok {
		t.Fatal("the unbound command produced no entry")
	}
	if keyless.Key != "" {
		t.Errorf("unbound command key = %q, want empty", keyless.Key)
	}
	if keyless.Name != "Jump" || keyless.Description != "jump to Inbox view" {
		t.Errorf("keyless entry lost its metadata: %+v", keyless)
	}
	if keyless.Context != "demo" {
		t.Errorf("keyless entry context = %q, want demo", keyless.Context)
	}
	if keyless.Layer != LayerCurrentMode {
		t.Errorf("keyless entry layer = %v, want CurrentMode", keyless.Layer)
	}
	if keyless.Category == "" {
		t.Error("keyless entry has no category, so it will not group")
	}
}

// A command that has a binding must not also appear as a keyless entry.
func TestBoundCommandIsNotListedTwice(t *testing.T) {
	km := keymap.NewRegistry()
	km.RegisterPluginBinding("d", "delete", "demo")
	km.RegisterPluginBinding("x", "delete", "demo")

	p := &stubPlugin{id: "demo", commands: []plugin.Command{
		{ID: "delete", Name: "Delete", Context: "demo", Handler: noopHandler()},
	}}

	entries := BuildEntries(km, []plugin.Plugin{p}, "demo", "demo")
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (dedup is by commandID:context)", len(entries))
	}
	if entries[0].Key != "d" {
		t.Errorf("entry key = %q, want the first registered binding", entries[0].Key)
	}
}

// The same command ID in two contexts is two entries, and each is deduped
// independently — the keyless path must not collapse them.
func TestKeylessDedupIsPerContext(t *testing.T) {
	km := keymap.NewRegistry()
	km.RegisterPluginBinding("d", "delete", "demo-list")

	p := &stubPlugin{id: "demo", commands: []plugin.Command{
		{ID: "delete", Name: "Delete", Context: "demo-list", Handler: noopHandler()},
		{ID: "delete", Name: "Delete", Context: "demo-detail", Handler: noopHandler()},
	}}

	entries := BuildEntries(km, []plugin.Plugin{p}, "demo-list", "demo")
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want one per context", len(entries))
	}
	keys := map[string]string{}
	for _, e := range entries {
		keys[e.Context] = e.Key
	}
	if keys["demo-list"] != "d" || keys["demo-detail"] != "" {
		t.Errorf("keys by context = %v, want demo-list:d demo-detail:(none)", keys)
	}
}

// A command with no binding and no way to run it is not an entry: it would be a
// row that does nothing when selected.
func TestUninvocableKeylessCommandIsSkipped(t *testing.T) {
	km := keymap.NewRegistry()
	p := &stubPlugin{id: "demo", commands: []plugin.Command{
		{ID: "documented-only", Name: "Doc", Context: "demo"},
	}}

	if entries := BuildEntries(km, []plugin.Plugin{p}, "demo", "demo"); len(entries) != 0 {
		t.Fatalf("entries = %+v, want none", entries)
	}

	// ...unless the keymap knows how to run it.
	km.RegisterCommand(keymap.Command{ID: "documented-only", Handler: func() tea.Cmd { return nil }})
	if entries := BuildEntries(km, []plugin.Plugin{p}, "demo", "demo"); len(entries) != 1 {
		t.Fatalf("entries = %+v, want the now-runnable command", entries)
	}
}

// A keyless entry has to be findable and legible: searchable by name and
// description, and rendered with a blank key column rather than an empty chip.
func TestKeylessEntryFiltersAndRenders(t *testing.T) {
	entries := []PaletteEntry{
		{CommandID: "view-inbox", Name: "Jump", Description: "jump to Inbox view", Context: "demo"},
		{Key: "j", CommandID: "select-next", Name: "Select", Description: "move down", Context: "demo"},
	}

	for _, query := range []string{"Jump", "Inbox"} {
		got := FilterEntries(entries, query)
		if len(got) == 0 || got[0].CommandID != "view-inbox" {
			t.Errorf("search %q did not find the keyless entry: %+v", query, got)
		}
	}

	m := New()
	m.width = 80
	m.filtered = entries

	keyless := m.renderEntry(entries[0], false, 76)
	keyed := m.renderEntry(entries[1], false, 76)
	if strings.Contains(stripANSI(keyless), "Jump") == false {
		t.Errorf("keyless entry did not render its name: %q", keyless)
	}
	// Both lines put the name in the same column, so the list stays aligned.
	keylessCol := strings.Index(stripANSI(keyless), "Jump")
	keyedCol := strings.Index(stripANSI(keyed), "Select")
	if keylessCol != keyedCol {
		t.Errorf("keyless entry misaligned: name at %d vs %d\n%q\n%q",
			keylessCol, keyedCol, stripANSI(keyless), stripANSI(keyed))
	}

	// Selection still works with a keyless entry under the cursor.
	m.cursor = 0
	if sel := m.SelectedEntry(); sel == nil || sel.CommandID != "view-inbox" {
		t.Errorf("keyless entry cannot be selected: %+v", sel)
	}
}

// stripANSI removes escape sequences so a rendered line can be inspected as
// text.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && (r == 'm' || r == 'K'):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}
