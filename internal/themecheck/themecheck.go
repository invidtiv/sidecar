// Package themecheck detects styles that are frozen to the default theme.
//
// internal/styles exposes the palette as package-level *variables* that
// ApplyTheme reassigns at runtime. Anything that reads one of those variables
// while building a value in a package-level `var` block — or in an `init()` —
// is evaluated once, at program init, before any theme has been applied. It
// therefore keeps the default theme's colours forever, and the bug is
// invisible to anyone running the default theme.
//
// This package walks the module's source and reports every such site, so a
// test can fail when the pattern reappears.
package themecheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// StylesPkgPath is the import path of the package whose exported variables are
// reassigned when the theme changes.
const StylesPkgPath = "github.com/marcus/sidecar/internal/styles"

// Finding is one package-level value that captures a theme colour at init.
type Finding struct {
	File   string // path relative to the scanned root
	Line   int
	Name   string // the package-level identifier being initialised
	Symbol string // the styles.* symbol it froze
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: package-level %s captures styles.%s at init; "+
		"it will keep the default theme's colour after ApplyTheme. "+
		"Make it a function that reads styles.%s at render time.",
		f.File, f.Line, f.Name, f.Symbol, f.Symbol)
}

// Scan walks root, finds the theme-mutable symbols exported by the styles
// package, and reports every package-level value elsewhere in the module that
// is initialised from one of them.
//
// If root contains no styles package (as in the analyzer's own fixtures), the
// caller-supplied mutable set is used instead; see ScanWith.
func Scan(root string) ([]Finding, error) {
	mutable, stylesDir, err := mutableSymbols(root)
	if err != nil {
		return nil, err
	}
	if len(mutable) == 0 {
		return nil, fmt.Errorf("themecheck: found no theme-mutable symbols under %s", root)
	}
	return ScanWith(root, mutable, stylesDir)
}

// MutableSymbols is Scan's first step, exposed so tests can assert the set is
// non-trivial and contains the palette variables it is meant to cover.
func MutableSymbols(root string) (map[string]bool, error) {
	m, _, err := mutableSymbols(root)
	return m, err
}

// ScanWith is Scan with an explicit set of theme-mutable symbol names, and an
// optional directory to skip (the styles package itself, which legitimately
// owns and rebuilds those variables).
func ScanWith(root string, mutable map[string]bool, skipDir string) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirName(d.Name()) {
				return fs.SkipDir
			}
			if skipDir != "" && sameDir(path, skipDir) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		found, err := scanFile(root, path, mutable)
		if err != nil {
			return err
		}
		findings = append(findings, found...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

func skipDirName(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "testdata", "dist", "build":
		return true
	}
	return false
}

func sameDir(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return aa == bb
}

// mutableSymbols returns the exported identifiers of the styles package that
// are reassigned after init — the palette variables ApplyThemeColors sets and
// the styles rebuildStyles recreates. Anything not in this set (a const, or a
// var never assigned again) is safe to capture at init.
func mutableSymbols(root string) (map[string]bool, string, error) {
	stylesDir := ""
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "styles" && filepath.Base(filepath.Dir(path)) == "internal" {
			stylesDir = path
			return fs.SkipDir
		}
		if d.IsDir() && skipDirName(d.Name()) {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if stylesDir == "" {
		return nil, "", fmt.Errorf("themecheck: no internal/styles directory under %s", root)
	}

	entries, err := os.ReadDir(stylesDir)
	if err != nil {
		return nil, "", err
	}

	declared := map[string]bool{}
	assigned := map[string]bool{}
	funcBodies := map[string]*ast.BlockStmt{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(stylesDir, e.Name()), nil, 0)
		if err != nil {
			return nil, "", err
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if ok && gen.Tok == token.VAR {
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, n := range vs.Names {
						declared[n.Name] = true
					}
				}
				continue
			}
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Recv == nil {
				funcBodies[fn.Name.Name] = fn.Body
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || as.Tok != token.ASSIGN {
					return true
				}
				for _, lhs := range as.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						assigned[id.Name] = true
					}
				}
				return true
			})
		}
	}

	// Theme-mutable state: package-level vars that are reassigned after init.
	themeState := map[string]bool{}
	for name := range declared {
		if assigned[name] {
			themeState[name] = true
		}
	}

	// A styles function is theme-dependent if it reads that state, directly or
	// through another styles function. Calling one of those from a
	// package-level var freezes the result exactly as reading a colour does.
	dependent := map[string]bool{}
	for {
		grew := false
		for name, body := range funcBodies {
			if dependent[name] {
				continue
			}
			reads := false
			ast.Inspect(body, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || reads {
					return !reads
				}
				if themeState[id.Name] || (id.Name != name && dependent[id.Name]) {
					reads = true
					return false
				}
				return true
			})
			if reads {
				dependent[name] = true
				grew = true
			}
		}
		if !grew {
			break
		}
	}

	mutable := map[string]bool{}
	for name := range themeState {
		if isExported(name) {
			mutable[name] = true
		}
	}
	for name := range dependent {
		if isExported(name) {
			mutable[name] = true
		}
	}
	return mutable, stylesDir, nil
}

func isExported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

func scanFile(root, path string, mutable map[string]bool) ([]Finding, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		// Unparseable files are not our problem; ignore rather than fail the guard.
		return nil, nil //nolint:nilerr
	}

	alias := stylesAlias(f)
	if alias == "" {
		return nil, nil
	}

	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}

	// Package-level identifiers, so init() assignments to them can be flagged
	// too — moving the expression into init() freezes it just the same.
	pkgLevel := map[string]bool{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, n := range vs.Names {
					pkgLevel[n.Name] = true
				}
			}
		}
	}

	var findings []Finding
	report := func(name string, expr ast.Expr) {
		for _, sym := range mutableRefs(expr, alias, mutable) {
			findings = append(findings, Finding{
				File:   rel,
				Line:   fset.Position(expr.Pos()).Line,
				Name:   name,
				Symbol: sym,
			})
		}
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue
			}
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, v := range vs.Values {
					name := "value"
					if i < len(vs.Names) {
						name = vs.Names[i].Name
					} else if len(vs.Names) > 0 {
						name = vs.Names[0].Name
					}
					report(name, v)
				}
			}
		case *ast.FuncDecl:
			if d.Name.Name != "init" || d.Recv != nil || d.Body == nil {
				continue
			}
			ast.Inspect(d.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range as.Lhs {
					root := rootIdent(lhs)
					if root == "" || !pkgLevel[root] {
						continue
					}
					if i < len(as.Rhs) {
						report(root, as.Rhs[i])
					} else if len(as.Rhs) == 1 {
						report(root, as.Rhs[0])
					}
				}
				return true
			})
		}
	}
	return findings, nil
}

// rootIdent returns the base identifier of an assignment target, so that
// `m["k"] = ...` and `s.Field = ...` resolve to `m` and `s`.
func rootIdent(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return rootIdent(t.X)
	case *ast.SelectorExpr:
		return rootIdent(t.X)
	case *ast.StarExpr:
		return rootIdent(t.X)
	}
	return ""
}

// stylesAlias returns the local name the file imports the styles package
// under, or "" if it does not import it. A dot-import would defeat the check,
// so it is reported as unsupported by returning "" — the repo does not use one
// and the guard test asserts that separately.
func stylesAlias(f *ast.File) string {
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path != StylesPkgPath {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return ""
			}
			return imp.Name.Name
		}
		return "styles"
	}
	return ""
}

// HasDotImport reports whether any file under root dot-imports the styles
// package, which would hide theme references from the scanner.
func HasDotImport(root string) (string, bool) {
	found := ""
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirName(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil //nolint:nilerr
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == StylesPkgPath && imp.Name != nil && imp.Name.Name == "." {
				found = path
			}
		}
		return nil
	})
	return found, found != ""
}

// mutableRefs returns the theme-mutable styles symbols an expression reads.
// References inside a function literal are ignored: a closure body runs when
// it is called, not at init, so `var f = func() { ... styles.TextMuted ... }`
// is exactly the fix, not the bug.
func mutableRefs(expr ast.Expr, alias string, mutable map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(ast.Node) bool
	walk = func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != alias {
			return true
		}
		if mutable[sel.Sel.Name] && !seen[sel.Sel.Name] {
			seen[sel.Sel.Name] = true
			out = append(out, sel.Sel.Name)
		}
		return true
	}
	ast.Inspect(expr, walk)
	return out
}
