package notes

import (
	"os"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

func setNotesDefaultEditor(p *Plugin, editor string) {
	cfg := config.Default()
	cfg.Plugins.Notes.DefaultEditor = editor
	p.ctx.Config = cfg
}

func inlineStartFrom(t *testing.T, cmd tea.Cmd) InlineEditStartedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("editor gesture returned no command")
	}
	started, ok := cmd().(InlineEditStartedMsg)
	if !ok {
		t.Fatalf("editor gesture returned %T, want InlineEditStartedMsg", cmd())
	}
	return started
}

func TestDefaultEditorPreferenceDrivesEnterAndBodyClick(t *testing.T) {
	installNotesFakeTmux(t)

	for _, gesture := range []string{"list enter", "preview enter", "body click"} {
		t.Run(gesture, func(t *testing.T) {
			p, noteID := newNotesEditorHarness(t)
			setNotesDefaultEditor(p, config.NotesEditorPane)
			p.editorNote = &p.notes[0]
			p.previewMode = true

			var cmd tea.Cmd
			switch gesture {
			case "list enter":
				p.activePane = PaneList
				_, cmd = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			case "preview enter":
				p.activePane = PaneEditor
				_, cmd = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			case "body click":
				_ = p.View(p.width, p.height)
				_, cmd = p.handleMouseClick(mouse.MouseAction{
					X: 40, Y: 3,
					Region: &mouse.Region{ID: regionEditorPane, Rect: mouse.Rect{X: 30, Y: 1, W: 40, H: 20}},
				})
			}
			if started := inlineStartFrom(t, cmd); started.NoteID != noteID {
				t.Fatalf("started note %q, want %q", started.NoteID, noteID)
			}
		})
	}
}

func TestExplicitEditorCommandsIgnoreDefaultPreference(t *testing.T) {
	installNotesFakeTmux(t)

	p, _ := newNotesEditorHarness(t)
	setNotesDefaultEditor(p, config.NotesEditorPane)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if p.previewMode || p.activePane != PaneEditor || p.edit.Active {
		t.Fatalf("i did not enter built-in edit: preview=%v pane=%v inline=%v", p.previewMode, p.activePane, p.edit.Active)
	}

	p, noteID := newNotesEditorHarness(t)
	setNotesDefaultEditor(p, config.NotesEditorBuiltin)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if started := inlineStartFrom(t, cmd); started.NoteID != noteID {
		t.Fatalf("e started note %q, want %q", started.NoteID, noteID)
	}

	p, _ = newNotesEditorHarness(t)
	setNotesDefaultEditor(p, config.NotesEditorPane)
	_, cmd = p.Update(tea.KeyPressMsg{Code: 'E', Text: "E"})
	_ = prepareExternalEditor(t, p, cmd)
}

func TestPaneDefaultExplicitBuiltInThenRenderedLineClickStaysBuiltIn(t *testing.T) {
	installNotesFakeTmux(t)
	p, _ := newNotesEditorHarness(t)
	setNotesDefaultEditor(p, config.NotesEditorPane)

	// Enter the built-in editor through its explicit production key, then use
	// a hit region created by the real edit renderer. The default preference
	// must not be consulted again after this explicit ownership choice.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if p.previewMode || p.activePane != PaneEditor || p.edit.Active {
		t.Fatalf("i did not enter built-in edit: preview=%v pane=%v inline=%v", p.previewMode, p.activePane, p.edit.Active)
	}
	_ = p.View(p.width, p.height)
	var line *mouse.Region
	for _, candidate := range p.mouseHandler.HitMap.Regions() {
		if candidate.ID == regionEditorLine {
			copy := candidate
			line = &copy
			break
		}
	}
	if line == nil {
		t.Fatal("built-in edit render registered no editor-line region")
	}
	clickX := line.Rect.X + min(5, line.Rect.W-1)
	clickY := line.Rect.Y
	wantLine, wantCol := p.clickToSource(clickX, clickY)

	_, cmd := p.Update(tea.MouseClickMsg(tea.Mouse{X: clickX, Y: clickY, Button: tea.MouseLeft}))
	if cmd != nil {
		if started, ok := cmd().(InlineEditStartedMsg); ok {
			t.Fatalf("built-in body click launched pane editor for %s", started.NoteID)
		}
	}
	if p.previewMode || p.edit.Active {
		t.Fatalf("body click left built-in ownership: preview=%v inline=%v", p.previewMode, p.edit.Active)
	}
	if gotLine, gotCol := p.editorTextarea.Line(), p.editorTextarea.Column(); gotLine != wantLine || gotCol != wantCol {
		t.Fatalf("built-in caret = %d:%d, want clicked source %d:%d", gotLine, gotCol, wantLine, wantCol)
	}
}

func TestArchivedEnterAndClickRemainReadOnlyWithPaneDefault(t *testing.T) {
	installNotesFakeTmux(t)
	p, _ := newNotesEditorHarness(t)
	setNotesDefaultEditor(p, config.NotesEditorPane)
	p.viewFilter = FilterArchived
	p.notes[0].Archived = true
	p.editorNote = &p.notes[0]
	p.previewMode = true

	p.activePane = PaneList
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || !p.previewMode || p.edit.Active {
		t.Fatalf("archived Enter edited: cmd=%T preview=%v inline=%v", cmd, p.previewMode, p.edit.Active)
	}
	_, cmd = p.handleMouseClick(mouse.MouseAction{
		X: 40, Y: 3,
		Region: &mouse.Region{ID: regionEditorPane, Rect: mouse.Rect{X: 30, Y: 1, W: 40, H: 20}},
	})
	if cmd != nil || !p.previewMode || p.edit.Active {
		t.Fatalf("archived click edited: cmd=%T preview=%v inline=%v", cmd, p.previewMode, p.edit.Active)
	}
}

func clipboardTextFromCopyCommand(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("copy returned no command")
	}
	sequence := reflect.ValueOf(cmd())
	if sequence.Kind() != reflect.Slice || sequence.Len() == 0 {
		t.Fatalf("copy command produced %T, want a command sequence", cmd())
	}
	first, ok := sequence.Index(0).Interface().(tea.Cmd)
	if !ok || first == nil {
		t.Fatalf("first copy step = %T, want tea.Cmd", sequence.Index(0).Interface())
	}
	msg := first()
	value := reflect.ValueOf(msg)
	if value.Kind() != reflect.String {
		t.Fatalf("clipboard message = %T, want string payload", msg)
	}
	return value.String()
}

func TestCtrlYCopiesNoteIDOnlyInListAndReadOnlyPreview(t *testing.T) {
	for _, pane := range []FocusPane{PaneList, PaneEditor} {
		t.Run(paneName(pane), func(t *testing.T) {
			p, noteID := newNotesEditorHarness(t)
			p.activePane = pane
			p.editorNote = &p.notes[0]
			p.previewMode = true
			_, cmd := p.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
			if got := clipboardTextFromCopyCommand(t, cmd); got != noteID {
				t.Fatalf("clipboard = %q, want note ID %q", got, noteID)
			}
		})
	}
}

func paneName(p FocusPane) string {
	if p == PaneEditor {
		return "preview"
	}
	return "list"
}

func TestCtrlYIsForwardedUntouchedToInlineEditor(t *testing.T) {
	logPath := installNotesFakeTmux(t)
	p := New()
	p.edit.Active = true
	p.edit.Name = "notes-editor-test"
	p.edit.Model.Enter(p.edit.Name, "")

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("inline editor swallowed ctrl+y")
	}
	_ = cmd()
	tty.WaitForPendingSends()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if !strings.Contains(log, "send-keys") || !strings.Contains(log, "C-y") {
		t.Fatalf("ctrl+y did not reach tmux unchanged:\n%s", log)
	}
}

func TestNotesCommandsAndBindingsAdvertiseDefaultExplicitAndContextualActions(t *testing.T) {
	p, _ := newNotesEditorHarness(t)
	cases := []struct {
		context string
		setup   func()
		want    map[string]string // key -> command
	}{
		{
			context: "notes-list",
			setup:   func() { p.activePane = PaneList },
			want: map[string]string{
				"enter": "open-note", "i": "edit-note", "e": "vim-edit", "E": "external-editor", "ctrl+y": "yank-id",
			},
		},
		{
			context: "notes-preview",
			setup: func() {
				p.activePane = PaneEditor
				p.editorNote = &p.notes[0]
				p.previewMode = true
			},
			want: map[string]string{
				"enter": "open-note", "i": "edit-note", "e": "vim-edit", "E": "external-editor", "ctrl+y": "yank-id",
			},
		},
		{
			context: "notes-editor",
			setup: func() {
				p.activePane = PaneEditor
				p.editorNote = &p.notes[0]
				p.previewMode = false
				p.historyForCurrent().redo = []editSnapshot{{content: p.editorTextarea.Value()}}
			},
			want: map[string]string{
				"alt+a": "select-all", "super+a": "select-all", "super+up": "note-start", "super+down": "note-end", "ctrl+y": "redo-edit",
			},
		},
	}
	bindings := keymap.DefaultBindings()
	for _, tc := range cases {
		t.Run(tc.context, func(t *testing.T) {
			tc.setup()
			commands := map[string]bool{}
			for _, command := range p.Commands() {
				if command.Context == tc.context {
					commands[command.ID] = true
				}
			}
			for key, command := range tc.want {
				bound := false
				for _, binding := range bindings {
					if binding.Context == tc.context && binding.Key == key && binding.Command == command {
						bound = true
						break
					}
				}
				if !bound {
					t.Errorf("%s has no %s -> %s binding", tc.context, key, command)
				}
				if !commands[command] {
					t.Errorf("%s binding %s -> %s has no advertised command", tc.context, key, command)
				}
			}
		})
	}

	selectAllKeys := []string{}
	for _, binding := range bindings {
		if binding.Context == "notes-editor" && binding.Command == "select-all" {
			selectAllKeys = append(selectAllKeys, binding.Key)
		}
	}
	if !reflect.DeepEqual(selectAllKeys, []string{"alt+a", "super+a"}) {
		t.Fatalf("select-all keys = %v, want portable alt+a advertised before super+a", selectAllKeys)
	}
}
