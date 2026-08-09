package screenmodel

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// vtModule is the emulator dependency this package owns exclusively.
const vtModule = "github.com/charmbracelet/x/vt"

// thisPackage is the one import path allowed to reference it.
const thisPackage = "internal/tty/screenmodel"

// TestOnlyThisPackageImportsVT enforces the package doc's central invariant as a
// test rather than a comment. The whole point of the adapter is that the
// emulator can be replaced or deleted without touching workspace code, and that
// property is only true while exactly one package imports it. Slices 1-5 add
// consumers right at this boundary, so the guard has to fail the build the
// moment one of them reaches past the adapter.
//
// TestImports/XTestImports are included: a test file in another package that
// imports x/vt to "just check something" is the same leak.
func TestOnlyThisPackageImportsVT(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := moduleRoot(t)

	const format = `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}` +
		`{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}`
	cmd := exec.Command("go", "list", "-e", "-f", format, "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v: %s", err, stderr.String())
	}

	var offenders []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pkg := fields[0]
		if strings.HasSuffix(pkg, thisPackage) {
			continue
		}
		for _, imp := range fields[1:] {
			if imp == vtModule || strings.HasPrefix(imp, vtModule+"/") {
				offenders = append(offenders, pkg+" -> "+imp)
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("only %s may import %s; found:\n  %s",
			thisPackage, vtModule, strings.Join(offenders, "\n  "))
	}
}

// TestGuardSeesThisPackagesOwnImport proves the guard is looking at real data:
// screenmodel itself must show up as importing x/vt, so a listing that silently
// returned nothing could not pass as "no offenders".
func TestGuardSeesThisPackagesOwnImport(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	cmd := exec.Command("go", "list", "-f", `{{range .Imports}}{{.}}
{{end}}`, ".")
	// The test's working directory is this package's directory.
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	if !strings.Contains(string(out), vtModule) {
		t.Fatalf("screenmodel no longer imports %s; the guard test would pass vacuously", vtModule)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("locate module root: %v", err)
	}
	return strings.TrimSpace(string(out))
}
