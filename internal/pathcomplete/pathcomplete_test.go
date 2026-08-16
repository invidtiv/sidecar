package pathcomplete

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoriesCompletesATypedPrefix(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"sidecar", "sidecar-notes", "other", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sidecar.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got := Directories(filepath.Join(root, "side"), 8)
	want := []string{filepath.Join(root, "sidecar"), filepath.Join(root, "sidecar-notes")}
	if len(got) != len(want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Everything inside a directory, once the user has typed the separator.
	if all := Directories(root+string(os.PathSeparator), 8); len(all) != 3 {
		t.Fatalf("listing a typed directory returned %#v", all)
	}

	// A dotted prefix is the only way to reach a hidden directory.
	if hidden := Directories(filepath.Join(root, ".hid"), 8); len(hidden) != 1 {
		t.Fatalf("dot prefix returned %#v", hidden)
	}

	if limited := Directories(filepath.Join(root, "s"), 1); len(limited) != 1 {
		t.Fatalf("limit ignored: %#v", limited)
	}
}

// Nothing is enumerated until the user has typed a path prefix.
func TestDirectoriesRefusesToBrowse(t *testing.T) {
	for _, input := range []string{"", "   ", "~", "s", "project"} {
		if got := Directories(input, 8); got != nil {
			t.Fatalf("input %q enumerated %#v", input, got)
		}
	}
}

func TestDirectoriesKeepsTildeNotation(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Skip("home directory is unreadable")
	}
	var name string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name()[0] != '.' {
			name = entry.Name()
			break
		}
	}
	if name == "" {
		t.Skip("no directory in home to complete")
	}
	got := Directories("~/"+name[:1], 20)
	if len(got) == 0 {
		t.Fatalf("completing ~/%s matched nothing", name[:1])
	}
	for _, candidate := range got {
		if candidate[0] != '~' {
			t.Fatalf("candidate %q dropped the ~ the user typed", candidate)
		}
	}
}

// A bare root is a place, not a prefix. Completing it would enumerate a whole
// directory the user has not typed into, which is the one thing this package
// promises never to do.
func TestBareRootDoesNotEnumerate(t *testing.T) {
	for _, input := range []string{"/", "//", " / ", "~", "~/"} {
		if got := Directories(input, 20); got != nil {
			t.Fatalf("input %q enumerated %d directories", input, len(got))
		}
	}
}
