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
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

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
