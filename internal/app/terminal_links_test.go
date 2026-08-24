package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/termpreview"
)

func TestTerminalLinkResolverFreshRejectsRetargetedRoot(t *testing.T) {
	parent := t.TempDir()
	rootA := filepath.Join(parent, "a")
	rootB := filepath.Join(parent, "b")
	for _, root := range []string{rootA, rootB} {
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(root), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(parent, "current")
	if err := os.Symlink(rootA, alias); err != nil {
		t.Fatal(err)
	}
	canonicalA, err := filepath.EvalSymlinks(alias)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &terminalLinkResolver{}
	request := termpreview.FreshLinkRequest{
		Root: canonicalA, RawRoot: alias,
		Candidate: contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"},
	}
	if ref, found := resolver.ResolveFresh(request); !found || ref.Value != "README.md" {
		t.Fatalf("fresh file before retarget = (%+v, %v)", ref, found)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rootB, alias); err != nil {
		t.Fatal(err)
	}
	if ref, found := resolver.ResolveFresh(request); found || ref != (contentlink.Ref{}) {
		t.Fatalf("retargeted root was accepted: (%+v, %v)", ref, found)
	}
}
