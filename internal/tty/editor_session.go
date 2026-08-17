package tty

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	xterm "golang.org/x/term"
)

const (
	defaultEditorWidth  = 80
	defaultEditorHeight = 24
)

// EditorSession owns the tmux-specific lifecycle and editor conventions shared
// by inline editor consumers. Domain state (files, tabs, notes, persistence)
// remains with the calling plugin.
type EditorSession struct {
	Name   string
	Editor string
}

// EditorSessionOptions describes a detached editor session.
type EditorSessionOptions struct {
	NamePrefix  string
	Editor      string
	Path        string
	Line        int // zero-indexed; values <= 0 omit the editor line argument
	Width       int
	Height      int
	CursorAtEnd bool
}

// fallbackEditor is what the precedence chain lands on when the environment
// names none. The shell wrapper recognises it and lets the profile resolve the
// editor instead; see editor_launch.go.
const fallbackEditor = "vim"

// ResolveEditor returns the configured editor, preserving the established
// EDITOR > VISUAL > vim precedence.
func ResolveEditor() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}
	return fallbackEditor
}

// EditorAvailable reports whether tmux can host an embedded editor.
func EditorAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// StartEditorSession creates a detached tmux editor with consistent TERM,
// dimensions, and history setup.
func StartEditorSession(opts EditorSessionOptions) (EditorSession, error) {
	editor := opts.Editor
	if editor == "" {
		editor = ResolveEditor()
	}
	prefix := opts.NamePrefix
	if prefix == "" {
		prefix = "sidecar-edit-"
	}
	width, height := editorDimensions(opts.Width, opts.Height)
	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm-256color"
	}
	session := EditorSession{
		Name:   fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()),
		Editor: editor,
	}
	// The pane runs the editor through the user's profile, exactly as the
	// suspend-into-$EDITOR path does, so the in-pane editor and the external
	// one are provably the same program with the same configuration.
	line := 0
	if opts.Line > 0 {
		line = opts.Line + 1
	}
	editorArgs, _ := EditorArgv(editor, line, opts.Path)
	tmuxArgs := []string{
		"new-session", "-d", "-s", session.Name,
		"-x", strconv.Itoa(width), "-y", strconv.Itoa(height),
		"-e", "TERM=" + term,
	}
	tmuxArgs = append(tmuxArgs, editorArgs...)
	if err := NewSession(tmuxArgs...); err != nil {
		return EditorSession{}, err
	}
	if opts.CursorAtEnd {
		session.CursorToEnd()
	}
	return session, nil
}

func editorDimensions(width, height int) (int, int) {
	if width > 0 && height > 0 {
		return width, height
	}
	if w, h, err := xterm.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return defaultEditorWidth, defaultEditorHeight
}

// IsAlive reports whether the session still exists.
func (s EditorSession) IsAlive() bool {
	if s.Name == "" {
		return false
	}
	return exec.Command("tmux", "has-session", "-t", s.Name).Run() == nil
}

// Kill terminates the session. A missing/already-dead session is harmless.
func (s EditorSession) Kill() {
	if s.Name != "" {
		_ = exec.Command("tmux", "kill-session", "-t", s.Name).Run()
	}
}

// KillCmd defers session cleanup to Bubble Tea's command runner.
func (s EditorSession) KillCmd() tea.Cmd {
	if s.Name == "" {
		return nil
	}
	return func() tea.Msg {
		s.Kill()
		return nil
	}
}

// NormalizeEditorName maps common aliases to their editor family.
func NormalizeEditorName(editor string) string {
	base := strings.TrimSuffix(filepath.Base(editor), ".exe")
	switch base {
	case "nvim", "neovim", "vi":
		return "vim"
	case "hx":
		return "helix"
	case "kak":
		return "kakoune"
	case "emacsclient":
		return "emacs"
	default:
		return base
	}
}

// SaveAndQuit sends the editor-specific save-and-exit sequence. It reports
// whether the editor is known, independently of command delivery success.
func (s EditorSession) SaveAndQuit() bool {
	keys, ok := editorSaveQuitKeys(s.Editor)
	if !ok {
		return false
	}
	_ = SendKeys(s.Name, keySpecs(keys)...)
	return true
}

// CursorToEnd positions supported editors at the end of their buffer.
func (s EditorSession) CursorToEnd() bool {
	keys, ok := editorCursorEndKeys(s.Editor)
	if !ok {
		return false
	}
	_ = SendKeys(s.Name, keySpecs(keys)...)
	return true
}

func keySpecs(keys []string) []KeySpec {
	specs := make([]KeySpec, len(keys))
	for i, key := range keys {
		specs[i] = KeySpec{Value: key}
	}
	return specs
}

func editorSaveQuitKeys(editor string) ([]string, bool) {
	switch NormalizeEditorName(editor) {
	case "vim", "helix", "amp":
		return []string{"Escape", ":wq", "Enter"}, true
	case "nano":
		return []string{"C-o", "Enter", "C-x"}, true
	case "emacs":
		return []string{"C-x", "C-s", "C-x", "C-c"}, true
	case "micro":
		return []string{"C-s", "C-q"}, true
	case "kakoune":
		return []string{"Escape", ":write-quit", "Enter"}, true
	case "joe":
		return []string{"C-k", "x"}, true
	case "ne":
		return []string{"Escape", "Escape", ":s", "Enter", ":q", "Enter"}, true
	default:
		return nil, false
	}
}

func editorCursorEndKeys(editor string) ([]string, bool) {
	switch NormalizeEditorName(editor) {
	case "vim":
		return []string{"G", "$"}, true
	case "nano":
		return []string{"M-/"}, true
	case "emacs":
		return []string{"M->"}, true
	case "helix", "kakoune":
		return []string{"g", "e"}, true
	case "micro":
		return []string{"C-End"}, true
	default:
		return nil, false
	}
}

// MouseCmd forwards one SGR mouse event and reports a dead session through the
// owning tty model's scope.
func (s EditorSession) MouseCmd(scope MessageScope, button, col, row int, release bool) tea.Cmd {
	if s.Name == "" {
		return nil
	}
	return func() tea.Msg {
		if err := SendSGRMouse(s.Name, button, col, row, release); err != nil && IsSessionDeadError(err) {
			return SessionDeadMsg{Scope: scope}
		}
		return nil
	}
}
