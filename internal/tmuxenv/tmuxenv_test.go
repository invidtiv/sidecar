package tmuxenv

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSocketPathHonorsTmuxTmpdir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_TMPDIR", dir)

	want := filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
	if got := SocketPath(); got != want {
		t.Fatalf("SocketPath() = %q, want %q", got, want)
	}
}

func TestSocketPathFallsBackToTmp(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", "")

	want := filepath.Join("/tmp", "tmux-"+strconv.Itoa(os.Getuid()), "default")
	if got := SocketPath(); got != want {
		t.Fatalf("SocketPath() = %q, want %q", got, want)
	}
}

func TestSocketPathTracksEnvChanges(t *testing.T) {
	first := t.TempDir()
	t.Setenv("TMUX_TMPDIR", first)
	before := SocketPath()

	second := t.TempDir()
	t.Setenv("TMUX_TMPDIR", second)
	after := SocketPath()

	if before == after {
		t.Fatalf("SocketPath() did not follow TMUX_TMPDIR change (both %q)", before)
	}
}

// The namespace is the socket path and nothing else: a hostname component would
// change under a macOS network rename and orphan every manifest entry.
func TestNamespaceIsTheSocketPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_TMPDIR", dir)

	if namespace, socket := Namespace(), SocketPath(); namespace != socket {
		t.Fatalf("Namespace() = %q, want %q", namespace, socket)
	}
	if strings.Contains(Namespace(), ":") {
		t.Fatalf("Namespace() = %q, want no host component", Namespace())
	}
}

func TestNamespaceDiffersAcrossSockets(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	first := Namespace()
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	if second := Namespace(); first == second {
		t.Fatalf("Namespace() identical across sockets: %q", first)
	}
}
