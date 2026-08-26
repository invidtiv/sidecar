// Package parityscan is test support for the surface-parity contract around
// targetactivation plans.
//
// Each activating surface declares which plan kinds it handles
// (`…HandlesPlanKind`) and separately dispatches them in a switch. The
// declaration is what the parity pair asserts against, so on its own it proves
// an intention rather than a behaviour: a kind added to the declaration but
// never given a branch would pass while a click on it did nothing. This package
// reads both out of the source and lets a surface require that they name
// exactly the same kinds, which is the cheapest way to make the two impossible
// to drift apart.
package parityscan

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// StructFields returns the written field types of one named struct. It lets a
// parity test hold a shared-state seam in place without importing either host
// or duplicating runtime behavior in test support.
func StructFields(t *testing.T, file, typeName string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != typeName {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s: %s is not a struct", file, typeName)
			}
			fields := make(map[string]string)
			for _, field := range structure.Fields.List {
				var rendered bytes.Buffer
				if err := printer.Fprint(&rendered, fset, field.Type); err != nil {
					t.Fatalf("print %s.%s field type: %v", file, typeName, err)
				}
				for _, name := range field.Names {
					fields[name.Name] = rendered.String()
				}
			}
			return fields
		}
	}
	t.Fatalf("%s: no struct %s — the parity scan is reading the wrong source", file, typeName)
	return nil
}

// ReceiverMethods returns the method names declared on typeName in dir, from
// non-test files. Spread methods are the usual Go case; parsing one file is
// how a codec would hide in a neighbour.
func ReceiverMethods(t *testing.T, dir, typeName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name == nil || len(fn.Recv.List) == 0 {
				continue
			}
			if receiverTypeName(fn.Recv.List[0].Type) != typeName {
				continue
			}
			seen[fn.Name.Name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	default:
		return ""
	}
}

// RequireNoPrivatePaneLayoutCodec fails when a host still encodes or decodes
// PaneLayoutJSON itself rather than through panecodec.
func RequireNoPrivatePaneLayoutCodec(t *testing.T, surface string, methods []string) {
	t.Helper()
	for _, name := range methods {
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "encode") && !strings.Contains(lower, "decode") {
			continue
		}
		if strings.Contains(lower, "pane") || strings.Contains(lower, "leaf") || strings.Contains(lower, "layout") {
			t.Fatalf("%s still declares private PaneLayoutJSON codec method %s", surface, name)
		}
	}
}

// RequireFieldType holds one shared ownership seam and its legacy exclusions.
func RequireFieldType(t *testing.T, surface string, fields map[string]string, field, wantType string, forbidden ...string) {
	t.Helper()
	if got := fields[field]; got != wantType {
		t.Fatalf("%s %s type = %q, want %q", surface, field, got, wantType)
	}
	for _, name := range forbidden {
		if got, ok := fields[name]; ok {
			t.Fatalf("%s regained host-owned terminal field %s (%s)", surface, name, got)
		}
	}
}

// HandledKinds returns the targetactivation.Plan* constants named by the case
// clauses of the switch inside fn, as it is written in file. The file path is
// relative to the package directory, which is where `go test` runs.
func HandledKinds(t *testing.T, file, fn string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var decl *ast.FuncDecl
	for _, d := range parsed.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == fn {
			decl = fd
			break
		}
	}
	if decl == nil {
		t.Fatalf("%s: no function %s — the parity scan is reading the wrong source", file, fn)
	}
	seen := make(map[string]struct{})
	ast.Inspect(decl, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			sel, ok := expr.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "targetactivation" {
				continue
			}
			if len(sel.Sel.Name) > 4 && sel.Sel.Name[:4] == "Plan" {
				seen[sel.Sel.Name] = struct{}{}
			}
		}
		return true
	})
	kinds := make([]string, 0, len(seen))
	for kind := range seen {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	if len(kinds) == 0 {
		t.Fatalf("%s: %s names no plan kinds — the parity scan found nothing to compare", file, fn)
	}
	return kinds
}

// RequireSameKinds fails when a surface declares a kind it does not dispatch,
// or dispatches one it does not declare.
func RequireSameKinds(t *testing.T, surface string, declared, dispatched []string) {
	t.Helper()
	in := func(kinds []string, want string) bool {
		for _, kind := range kinds {
			if kind == want {
				return true
			}
		}
		return false
	}
	for _, kind := range declared {
		if !in(dispatched, kind) {
			t.Fatalf("%s declares %s handled but never dispatches it", surface, kind)
		}
	}
	for _, kind := range dispatched {
		if !in(declared, kind) {
			t.Fatalf("%s dispatches %s but does not declare it handled", surface, kind)
		}
	}
}
