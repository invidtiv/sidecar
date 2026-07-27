package tty

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveEditorPrecedence(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	if got := ResolveEditor(); got != "vim" {
		t.Fatalf("default editor = %q, want vim", got)
	}
	t.Setenv("VISUAL", "nano")
	if got := ResolveEditor(); got != "nano" {
		t.Fatalf("VISUAL editor = %q, want nano", got)
	}
	t.Setenv("EDITOR", "nvim")
	if got := ResolveEditor(); got != "nvim" {
		t.Fatalf("EDITOR editor = %q, want nvim", got)
	}
}

func TestNormalizeEditorName(t *testing.T) {
	tests := map[string]string{
		"/usr/bin/nvim":   "vim",
		"neovim":          "vim",
		"vi":              "vim",
		"hx":              "helix",
		"kak":             "kakoune",
		"emacsclient.exe": "emacs",
		"custom":          "custom",
	}
	for input, want := range tests {
		if got := NormalizeEditorName(input); got != want {
			t.Errorf("NormalizeEditorName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEditorSaveQuitKeys(t *testing.T) {
	tests := []struct {
		editor string
		want   []string
	}{
		{"nvim", []string{"Escape", ":wq", "Enter"}},
		{"nano", []string{"C-o", "Enter", "C-x"}},
		{"emacsclient", []string{"C-x", "C-s", "C-x", "C-c"}},
		{"kak", []string{"Escape", ":write-quit", "Enter"}},
	}
	for _, tt := range tests {
		got, ok := editorSaveQuitKeys(tt.editor)
		if !ok {
			t.Fatalf("editorSaveQuitKeys(%q) not recognized", tt.editor)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("editorSaveQuitKeys(%q) = %v, want %v", tt.editor, got, tt.want)
		}
	}
	if _, ok := editorSaveQuitKeys("unknown"); ok {
		t.Fatal("unknown editor unexpectedly recognized")
	}
}

func TestEditorSessionEmptyLifecycleIsSafe(t *testing.T) {
	var session EditorSession
	if session.IsAlive() {
		t.Fatal("empty editor session reported alive")
	}
	session.Kill()
	if cmd := session.MouseCmd(MessageScope{}, 0, 1, 1, false); cmd != nil {
		t.Fatal("empty editor session returned mouse command")
	}
}

func TestStartEditorSessionPreparesHistoryAndBuildsEditorCommand(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("TERM", "")
	session, err := StartEditorSession(EditorSessionOptions{
		NamePrefix:  "test-editor-",
		Editor:      "nvim",
		Path:        "/tmp/file with spaces.md",
		Line:        3,
		Width:       100,
		Height:      40,
		CursorAtEnd: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(session.Name, "test-editor-") || session.Editor != "nvim" {
		t.Fatalf("session = %+v", session)
	}

	log := strings.Join(fakeTmuxCalls(t, logPath), "\n")
	history := strings.Index(log, "show-options -gv history-limit")
	create := strings.Index(log, "new-session -d -s "+session.Name+" -x 100 -y 40")
	if history < 0 || create < 0 || history > create {
		t.Fatalf("history was not prepared before session creation:\n%s", log)
	}
	if !strings.Contains(log, "-e TERM=xterm-256color nvim +4 /tmp/file with spaces.md") {
		t.Fatalf("editor argv missing line/path/TERM:\n%s", log)
	}
	if !strings.Contains(log, "send-keys -t "+session.Name+" G") ||
		!strings.Contains(log, "send-keys -t "+session.Name+" $") {
		t.Fatalf("cursor-to-end keys missing:\n%s", log)
	}
}

func TestEditorSessionMouseCmdUsesScopedForwarding(t *testing.T) {
	logPath := installFakeTmux(t)
	session := EditorSession{Name: "editor"}
	cmd := session.MouseCmd(MessageScope{Owner: 7, Target: "notes"}, 32, 7, 9, false)
	if msg := cmd(); msg != nil {
		t.Fatalf("mouse command returned %T, want nil", msg)
	}
	log := strings.Join(fakeTmuxCalls(t, logPath), "\n")
	if !strings.Contains(log, "send-keys -t editor -H") {
		t.Fatalf("SGR mouse command missing from fake tmux log:\n%s", log)
	}
}

func TestEditorSessionMouseCmdReportsScopedDeadSession(t *testing.T) {
	installFakeTmux(t)
	t.Setenv("TMUX_FAKE_SEND_ERROR", "can't find pane: editor")
	scope := MessageScope{Owner: 9, Target: "notes", Generation: 4}
	cmd := (EditorSession{Name: "editor"}).MouseCmd(scope, 0, 2, 3, false)
	result := cmd()
	msg, ok := result.(SessionDeadMsg)
	if !ok {
		t.Fatalf("mouse failure returned %T, want SessionDeadMsg", result)
	}
	if msg.Scope != scope {
		t.Fatalf("dead-session scope = %+v, want %+v", msg.Scope, scope)
	}
}
