package tty

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// nastyName is the filename every injection assertion here uses: spaces, both
// quote styles, a command substitution, a backtick substitution, a glob, and a
// leading dash that an argument parser could mistake for a flag.
const nastyName = `-we ird'$(touch pwned-sub)` + "`touch pwned-tick`" + `"*.md`

func TestEditorArgvUsesLoginShell(t *testing.T) {
	t.Setenv("EDITOR", "nvim")
	t.Setenv("VISUAL", "")
	t.Setenv("SHELL", "/bin/sh")

	argv, viaProfile := EditorArgv("nvim", 12, "/tmp/notes.md")
	if !viaProfile {
		t.Fatalf("EditorArgv did not take the profile route: %q", argv)
	}
	want := []string{"/bin/sh", "-l", "-i", "-c", `exec "$@"`, "sidecar-editor", "nvim", "+12", "/tmp/notes.md"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
}

func TestEditorArgvOmitsLineWhenUnset(t *testing.T) {
	t.Setenv("EDITOR", "nvim")
	t.Setenv("SHELL", "/bin/sh")
	argv, _ := EditorArgv("nvim", 0, "/tmp/notes.md")
	for _, arg := range argv {
		if strings.HasPrefix(arg, "+") {
			t.Fatalf("line argument survived a zero line: %q", argv)
		}
	}
}

// The whole point of loading the profile is that the profile may be the only
// place the user names an editor.
func TestEditorArgvLetsProfileResolveEditor(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("SHELL", "/bin/sh")

	argv, viaProfile := EditorArgv(ResolveEditor(), 0, "/tmp/notes.md")
	if !viaProfile {
		t.Fatal("EditorArgv did not take the profile route")
	}
	want := []string{"/bin/sh", "-l", "-i", "-c", `exec "${EDITOR:-${VISUAL:-vim}}" "$@"`, "sidecar-editor", "/tmp/notes.md"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
}

// An editor the caller chose explicitly outranks whatever the profile exports.
func TestEditorArgvKeepsExplicitEditorOverProfile(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("SHELL", "/bin/sh")

	argv, _ := EditorArgv("helix", 0, "/tmp/notes.md")
	if !containsArg(argv, "helix") {
		t.Fatalf("explicit editor lost: %q", argv)
	}
	if containsArg(argv, `exec "${EDITOR:-${VISUAL:-vim}}" "$@"`) {
		t.Fatalf("explicit editor was handed to the profile to resolve: %q", argv)
	}
}

func TestEditorArgvFallsBackToDirectExec(t *testing.T) {
	t.Setenv("EDITOR", "nvim")
	for name, shell := range map[string]string{
		"unset":       "",
		"missing":     filepath.Join(t.TempDir(), "no-such-shell"),
		"unknown-cli": "/usr/bin/tcsh",
		"nologin":     "/usr/sbin/nologin",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHELL", shell)
			argv, viaProfile := EditorArgv("nvim", 3, "/tmp/notes.md")
			if viaProfile {
				t.Fatalf("shell %q was trusted with the profile route: %q", shell, argv)
			}
			want := []string{"nvim", "+3", "/tmp/notes.md"}
			if !reflect.DeepEqual(argv, want) {
				t.Fatalf("argv = %q, want %q", argv, want)
			}
		})
	}
}

func TestDirectEditorArgvIsTheFallbackShape(t *testing.T) {
	if got, want := DirectEditorArgv("vim", 0, "/a b"), []string{"vim", "/a b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if got, want := DirectEditorArgv("vim", 9, "/a b"), []string{"vim", "+9", "/a b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

// The path is data. It reaches the editor byte for byte, and nothing inside it
// is ever executed, expanded, or split — proved by running the real shell.
func TestEditorArgvDoesNotInjectThroughTheFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, nastyName)
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Skipf("filesystem rejected the hostile name: %v", err)
	}
	// A stand-in "editor" that records the single argument it was handed.
	recorded := filepath.Join(dir, "recorded")
	editor := filepath.Join(dir, "fake-editor")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > " + shellQuote(recorded) + "\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(filepath.Base(shell), func(t *testing.T) {
			t.Setenv("EDITOR", editor)
			t.Setenv("SHELL", shell)
			argv, viaProfile := EditorArgv(editor, 0, path)
			if !viaProfile {
				t.Fatalf("%s was not used for the launch", shell)
			}
			// The hostile text must never appear inside the script the shell
			// parses; it may only appear as an operand after it.
			if strings.Contains(argv[4], nastyName) {
				t.Fatalf("filename was spliced into shell text: %q", argv[4])
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			// Run where the tripwires would land if anything in the name were
			// executed, so a breach is visible rather than scattered.
			cmd.Dir = dir
			cmd.Stdin = strings.NewReader("")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("launch failed: %v\n%s", err, out)
			}
			got, err := os.ReadFile(recorded)
			if err != nil {
				t.Fatalf("editor did not run: %v", err)
			}
			if string(got) != path {
				t.Fatalf("editor received %q, want %q", got, path)
			}
			for _, tripwire := range []string{"pwned-sub", "pwned-tick"} {
				if _, err := os.Stat(filepath.Join(dir, tripwire)); err == nil {
					t.Fatalf("substitution in the filename executed (%s)", tripwire)
				}
			}
		})
	}
}

func containsArg(argv []string, want string) bool {
	for _, arg := range argv {
		if arg == want {
			return true
		}
	}
	return false
}

// shellQuote is test-local: only the fixture script below needs it, and the
// production path deliberately has no such helper because it never builds
// shell text out of user data.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
