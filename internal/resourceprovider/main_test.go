package resourceprovider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixtureBin is the built path of testdata/fixtureprovider. Building it once in
// TestMain keeps every case in this package pointed at a real executable
// without paying a compile per test.
var fixtureBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sidecar-fixtureprovider-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "resourceprovider tests: temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	fixtureBin = filepath.Join(dir, "fixtureprovider")
	build := exec.Command("go", "build", "-o", fixtureBin, "./testdata/fixtureprovider")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "resourceprovider tests: building the fixture provider:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newFixtureProvider builds a CommandProvider over the fixture executable with
// the given extra argv.
func newFixtureProvider(t *testing.T, instance string, args ...string) *CommandProvider {
	t.Helper()
	p, err := NewCommandProvider(CommandConfig{
		Instance: instance,
		Argv:     append([]string{fixtureBin}, args...),
		Dir:      t.TempDir(),
		HostEnv:  os.Environ(),
		Host:     HostInfo{Name: "sidecar", Version: "test"},
	})
	if err != nil {
		t.Fatalf("NewCommandProvider: %v", err)
	}
	return p
}
