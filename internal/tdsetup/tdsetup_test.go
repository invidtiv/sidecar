package tdsetup

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tdroot"
)

func TestStatusDistinguishesMissingReadyAndConflictingTodos(t *testing.T) {
	root := t.TempDir()
	if err := Status(root); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("missing status = %v, want ErrNotInitialized", err)
	}
	if err := os.Mkdir(filepath.Join(root, tdroot.TodosDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Status(root); err != nil {
		t.Fatalf("ready status = %v", err)
	}
	if err := os.Remove(filepath.Join(root, tdroot.TodosDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, tdroot.TodosDir), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Status(root); !errors.Is(err, tdroot.ErrTodosIsFile) {
		t.Fatalf("conflict status = %v, want ErrTodosIsFile", err)
	} else if !strings.Contains(err.Error(), "mv .todos .todos.bak") {
		t.Fatalf("conflict is not actionable: %v", err)
	}
}

func TestInitializeUsesTDCLIAndDeclinesAgentInstructions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake td executable uses a POSIX shell")
	}
	root := t.TempDir()
	agents := filepath.Join(root, "AGENTS.md")
	claude := filepath.Join(root, "CLAUDE.md")
	beforeAgents := []byte("# Project instructions\nkeep me\n")
	beforeClaude := []byte("# Claude instructions\nkeep me too\n")
	if err := os.WriteFile(agents, beforeAgents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, beforeClaude, 0o644); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	script := filepath.Join(bin, "td")
	body := `#!/bin/sh
if [ "$1" != "-w" ] || [ "$2" != "` + root + `" ] || [ "$3" != "init" ]; then
  exit 12
fi
IFS= read -r answer || true
if [ -n "$answer" ]; then
  echo "accepted instructions" >&2
  exit 13
fi
mkdir -p "$2/.todos"
printf '%s' "$answer" > "$2/.todos/answer"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := Initialize(root); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	for path, want := range map[string][]byte{agents: beforeAgents, claude: beforeClaude} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s changed:\n%s", filepath.Base(path), got)
		}
	}
	answer, err := os.ReadFile(filepath.Join(root, ".todos", "answer"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(answer)) != "" {
		t.Fatalf("td prompt answer = %q, want blank", answer)
	}
}

func TestInitializeReportsConflictBeforeRunningTD(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".todos"), []byte("conflict"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(root); !errors.Is(err, tdroot.ErrTodosIsFile) {
		t.Fatalf("Initialize error = %v, want ErrTodosIsFile", err)
	}
}
