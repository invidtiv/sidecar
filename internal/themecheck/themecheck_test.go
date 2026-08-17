package themecheck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/themecheck"
)

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test directory")
		}
		dir = parent
	}
}

// TestNoFrozenThemeStyles is the guard for td-f2d94f. internal/styles exposes
// the palette as variables that ApplyTheme reassigns, so anything built from
// one of them in a package-level var block is evaluated at init — before a
// theme is applied — and keeps the default theme's colours forever. That bug
// is invisible to anyone running the default theme, so it needs a check that
// does not depend on someone noticing.
func TestNoFrozenThemeStyles(t *testing.T) {
	findings, err := themecheck.Scan(repoRoot(t))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) > 0 {
		var b strings.Builder
		for _, f := range findings {
			b.WriteString("\n  " + f.String())
		}
		t.Fatalf("%d package-level value(s) freeze a theme colour at init:%s\n\n"+
			"Move the expression into a function so the colour is read at render time.",
			len(findings), b.String())
	}
}

// TestMutableSymbolsCoversPalette pins the scanner's notion of "theme-mutable"
// to reality. If ApplyThemeColors were refactored such that these were no
// longer detected as reassigned, TestNoFrozenThemeStyles would silently pass
// on everything.
func TestMutableSymbolsCoversPalette(t *testing.T) {
	mutable, err := themecheck.MutableSymbols(repoRoot(t))
	if err != nil {
		t.Fatalf("mutable symbols: %v", err)
	}
	for _, name := range []string{
		"TextMuted", "TextPrimary", "Primary", "Accent", "BgPrimary",
		"SearchMatch", "SearchMatchCurrent", "ListItemSelected", "Muted",
	} {
		if !mutable[name] {
			t.Errorf("styles.%s is reassigned by ApplyTheme but the scanner does not treat it as theme-mutable", name)
		}
	}
	// Constants are safe to capture at init and must not be flagged.
	if mutable["MinContrastRatio"] {
		t.Errorf("MinContrastRatio is a pure function; it should not be theme-mutable")
	}
}

// TestNoDotImportOfStyles: a dot-import would make theme references invisible
// to the scanner (they would parse as bare identifiers), quietly disarming the
// guard above.
func TestNoDotImportOfStyles(t *testing.T) {
	if path, ok := themecheck.HasDotImport(repoRoot(t)); ok {
		t.Fatalf("%s dot-imports the styles package, which hides theme references from the frozen-style guard", path)
	}
}

// fixture writes a miniature module: a styles package with one theme-mutable
// var and one theme-dependent function, plus a consumer package.
func fixture(t *testing.T, consumer string) string {
	t.Helper()
	root := t.TempDir()
	stylesDir := filepath.Join(root, "internal", "styles")
	if err := os.MkdirAll(stylesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stylesSrc := `package styles

var TextMuted = Color("#6B7280")
var Frozen = "const-like"

func Color(s string) string { return s }

func RenderChip(s string) string { return TextMuted + s }

func ApplyThemeColors(hex string) {
	TextMuted = Color(hex)
}
`
	if err := os.WriteFile(filepath.Join(stylesDir, "styles.go"), []byte(stylesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	consumerDir := filepath.Join(root, "internal", "consumer")
	if err := os.MkdirAll(consumerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumerDir, "consumer.go"), []byte(consumer), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const fixtureImport = "import \"" + themecheck.StylesPkgPath + "\"\n"

// TestScanDetectsFrozenStyles is the test of the test: it proves the guard
// actually fires on each shape of the bug rather than merely passing.
func TestScanDetectsFrozenStyles(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantSym string
	}{
		{
			name:    "var reads a palette colour",
			src:     "package consumer\n\n" + fixtureImport + "\nvar DimStyle = NewStyle(styles.TextMuted)\n\nfunc NewStyle(c string) string { return c }\n",
			wantSym: "TextMuted",
		},
		{
			name:    "var calls a theme-dependent styles function",
			src:     "package consumer\n\n" + fixtureImport + "\nvar Chip = styles.RenderChip(\"x\")\n",
			wantSym: "RenderChip",
		},
		{
			name:    "style stored in a map",
			src:     "package consumer\n\n" + fixtureImport + "\nvar byKind = map[string]string{\"a\": styles.TextMuted}\n",
			wantSym: "TextMuted",
		},
		{
			name:    "style field in a struct literal",
			src:     "package consumer\n\n" + fixtureImport + "\ntype T struct{ Fg string }\n\nvar t = T{Fg: styles.TextMuted}\n",
			wantSym: "TextMuted",
		},
		{
			name:    "assigned in init",
			src:     "package consumer\n\n" + fixtureImport + "\nvar dim string\n\nfunc init() { dim = styles.TextMuted }\n",
			wantSym: "TextMuted",
		},
		{
			name:    "method value on a theme style",
			src:     "package consumer\n\n" + fixtureImport + "\nvar render = styles.RenderChip\n",
			wantSym: "RenderChip",
		},
		{
			name:    "aliased import does not evade the check",
			src:     "package consumer\n\nimport st \"" + themecheck.StylesPkgPath + "\"\n\nvar dim = st.TextMuted\n",
			wantSym: "TextMuted",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := themecheck.Scan(fixture(t, tc.src))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(findings) == 0 {
				t.Fatalf("guard did not fire on a frozen style:\n%s", tc.src)
			}
			if findings[0].Symbol != tc.wantSym {
				t.Errorf("symbol = %q, want %q", findings[0].Symbol, tc.wantSym)
			}
		})
	}
}

// TestScanAllowsDeferredReads: the fix for the bug must not itself be flagged,
// and neither should values that read nothing theme-mutable.
func TestScanAllowsDeferredReads(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "function reading the colour at call time",
			src:  "package consumer\n\n" + fixtureImport + "\nfunc DimStyle() string { return styles.TextMuted }\n",
		},
		{
			name: "closure in a var defers the read",
			src:  "package consumer\n\n" + fixtureImport + "\nvar DimStyle = func() string { return styles.TextMuted }\n",
		},
		{
			name: "value that is not theme-mutable",
			src:  "package consumer\n\n" + fixtureImport + "\nvar x = styles.Frozen\n",
		},
		{
			name: "no styles import at all",
			src:  "package consumer\n\nvar x = 1\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := themecheck.Scan(fixture(t, tc.src))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(findings) != 0 {
				t.Fatalf("guard fired on safe code %q: %v", tc.src, findings)
			}
		})
	}
}
