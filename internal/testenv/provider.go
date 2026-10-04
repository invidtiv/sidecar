package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ProviderHelp pins a provider's advertised capabilities without invoking the
// developer's installed agent. Any attempt to start this fixture fails.
func ProviderHelp(t *testing.T, command, help string) {
	t.Helper()
	dir := t.TempDir()
	quoted := "'" + strings.ReplaceAll(help, "'", "'\\''") + "'"
	script := "#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = --help ] || exit 90\nprintf '%s' " + quoted + "\n"
	if err := os.WriteFile(filepath.Join(dir, command), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
