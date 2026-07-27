package tty

import (
	"reflect"
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
