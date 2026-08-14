package workspace

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestWorkspaceDoesNotImportFilebrowser(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			if strings.Trim(spec.Path.Value, `"`) == "github.com/marcus/sidecar/internal/plugins/filebrowser" {
				t.Errorf("%s imports filebrowser; path actions belong in docview", name)
			}
		}
	}
}
