package mobile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mobileproto"
)

// resetReasonConstants reads the Reset* constants declared in mobileproto, by
// name, so this test can resolve a selector to the string it puts on the wire.
func resetReasonConstants(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "mobileproto", "protocol.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	constants := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "Reset") || i >= len(spec.Values) {
				continue
			}
			if literal, ok := spec.Values[i].(*ast.BasicLit); ok && literal.Kind == token.STRING {
				value, _ := strconv.Unquote(literal.Value)
				constants[name.Name] = value
			}
		}
		return true
	})
	return constants
}

// Every reason the service can put on a reset event must be a documented
// mobileproto.Reset* constant. Clients branch on these strings, so an
// undocumented one is a contract break. The check is on source: every place a
// reason is chosen (a reset's Reason field, advanceResetLocked, failLocked, a
// queued discontinuity, and strongerDiscontinuity's candidate) must name a
// constant in mobileproto.ResetReasons, never a literal. Values that only
// carry an already chosen reason onward (a variable) pass through.
func TestServiceEmitsOnlyDocumentedResetReasons(t *testing.T) {
	constants := resetReasonConstants(t)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	used := map[string]bool{}
	sites := 0
	check := func(expr ast.Expr) {
		sites++
		position := fset.Position(expr.Pos())
		switch e := expr.(type) {
		case *ast.BasicLit:
			t.Errorf("%s: reset reason %s is a literal; declare it as a mobileproto.Reset constant, add it to ResetReasons and document it", position, e.Value)
		case *ast.SelectorExpr:
			pkg, ok := e.X.(*ast.Ident)
			if !ok || pkg.Name != "mobileproto" {
				return // a field carrying a chosen reason, such as observed.discontinuity
			}
			value, ok := constants[e.Sel.Name]
			if !ok {
				t.Errorf("%s: reset reason mobileproto.%s is not a Reset* string constant", position, e.Sel.Name)
				return
			}
			if !mobileproto.IsResetReason(value) {
				t.Errorf("%s: reset reason %q is missing from mobileproto.ResetReasons and its documentation", position, value)
			}
			used[value] = true
		}
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.KeyValueExpr:
				if key, ok := n.Key.(*ast.Ident); ok && (key.Name == "Reason" || key.Name == "discontinuity") {
					check(n.Value)
				}
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if selector, ok := lhs.(*ast.SelectorExpr); ok && selector.Sel.Name == "discontinuity" && i < len(n.Rhs) {
						check(n.Rhs[i])
					}
				}
			case *ast.CallExpr:
				name := ""
				switch fn := n.Fun.(type) {
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				case *ast.Ident:
					name = fn.Name
				}
				switch name {
				case "advanceResetLocked", "failLocked":
					if len(n.Args) > 0 {
						check(n.Args[0])
					}
				case "strongerDiscontinuity":
					if len(n.Args) > 1 {
						check(n.Args[1])
					}
				}
			}
			return true
		})
	}
	if sites < 10 {
		t.Fatalf("found only %d reset reason sites; the test no longer sees how the service chooses reasons", sites)
	}
	for _, reason := range mobileproto.ResetReasons {
		if !used[reason] {
			t.Errorf("documented reset reason %q is never emitted; remove it only if no client depends on it", reason)
		}
	}
}
