package agentremote

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// packageImports returns every path this package's non-test source imports.
//
// It parses rather than greps, because the first version of this guard matched
// the word "shellstate" in the package doc comment explaining that shellstate
// is exactly what this package must not reach — a guard that fails on the
// sentence describing the property it protects is a guard nobody keeps.
func packageImports(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Clean(name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			paths = append(paths, strings.Trim(spec.Path.Value, `"`))
		}
	}
	if len(paths) == 0 {
		t.Fatal("no imports were read; the guard would pass vacuously")
	}
	return paths
}
