package tdmonitor

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTDMonitorDoesNotImportSidecarMarkdown pins the ownership boundary: the
// embedded td tab renders through td's own palette adapter
// (styles.CreateTD*Renderer plus monitor.SetTheme), not through Sidecar's
// Markdown renderer. Sidecar must not become the owner of td's rendering.
func TestTDMonitorDoesNotImportSidecarMarkdown(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		checked++
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if path == "github.com/marcus/sidecar/internal/markdown" {
				t.Errorf("%s imports internal/markdown; the td tab must keep using td's own renderer adapter", e.Name())
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Go files found to check")
	}
}
