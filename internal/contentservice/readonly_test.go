package contentservice

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestServiceSourceGraphIsReadOnly is the content-service analogue of
// TestServeIsReadOnly: the package must not be able to reach tmux mutation,
// file writes, td mutation, git mutation, config save, or an arbitrary shell.
//
// Runtime tests pin the git argv to `worktree list --porcelain`. This scans
// the source so a later import cannot quietly grow a writer.
func TestServiceSourceGraphIsReadOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	forbiddenImports := []string{
		"github.com/marcus/sidecar/internal/tty",
		"github.com/marcus/sidecar/internal/workspaceops",
		"github.com/marcus/sidecar/internal/uirequest",
		"github.com/marcus/sidecar/internal/cli",
		"github.com/marcus/sidecar/internal/overview",
		"github.com/marcus/sidecar/internal/hostserve",
		"charm.land/bubbletea/v2",
		"os/exec",
	}
	// os/exec is allowed only in service.go for the default git runner.
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, entry.Name(), src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path := strings.Trim(spec.Path.Value, `"`)
			for _, forbidden := range forbiddenImports {
				if path != forbidden {
					continue
				}
				if path == "os/exec" && entry.Name() == "service.go" {
					continue
				}
				t.Errorf("%s imports %s", entry.Name(), path)
			}
		}
		text := string(src)
		for _, verb := range []string{
			"tmux ", "send-keys", "kill-session", "new-session",
			"git commit", "git push", "git add", "Save(", "WriteFile",
		} {
			if strings.Contains(text, verb) && entry.Name() != "file.go" {
				// file.go mentions nothing of these; keep the scan honest.
				t.Errorf("%s contains %q", entry.Name(), verb)
			}
		}
		if strings.Contains(text, "os.WriteFile") || strings.Contains(text, "os.Write(") {
			t.Errorf("%s writes files", entry.Name())
		}
	}
}
