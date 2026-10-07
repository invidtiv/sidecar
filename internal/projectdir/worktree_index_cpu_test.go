//go:build darwin || linux

package projectdir

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Production had hundreds of obsolete registrations. Authorizing each current
// configured root repeatedly resolved every stale registration's missing
// ancestors. Measure process CPU, including that syscall work, in a fixed
// window; a timer/request-count assertion would miss this catalog hot path.
func TestWorktreeIndexStaleRegistrationCPUWindow(t *testing.T) {
	if base := os.Getenv("SIDECAR_WORKTREE_INDEX_CPU_BASE"); base != "" {
		cpu := func() time.Duration {
			var usage syscall.Rusage
			if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
				panic(err)
			}
			return time.Duration(usage.Utime.Sec+usage.Stime.Sec)*time.Second + time.Duration(usage.Utime.Usec+usage.Stime.Usec)*time.Microsecond
		}
		before := cpu()
		done := make(chan struct{})
		go func() {
			index := NewWorktreeIndex(base)
			for i := range 100 {
				root := filepath.Join(base, "missing", strings.Repeat("deep/", 12), fmt.Sprintf("configured-%03d", i))
				index.Lookup(root, filepath.Join(base, "missing-worktree"))
			}
			close(done)
		}()
		time.Sleep(2 * time.Second)
		completed := false
		select {
		case <-done:
			completed = true
		default:
		}
		fmt.Printf("%d %t\n", cpu()-before, completed)
		os.Exit(0)
	}
	base := t.TempDir()
	for i := range 400 {
		dir := filepath.Join(base, "projects", fmt.Sprintf("stale-%03d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(base, "missing", strings.Repeat("deep/", 12), fmt.Sprintf("retired-%03d", i))
		data, _ := json.Marshal(projectMeta{Path: root})
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorktreeIndexStaleRegistrationCPUWindow$")
	command.Env = append(os.Environ(), "SIDECAR_WORKTREE_INDEX_CPU_BASE="+base)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("CPU helper: %v, %s", err, data)
	}
	var ns int64
	var completed bool
	if _, err := fmt.Sscanf(string(data), "%d %t", &ns, &completed); err != nil {
		t.Fatalf("CPU helper output %q: %v", data, err)
	}
	used := time.Duration(ns)
	t.Logf("400 stale deep registrations, 100 root lookups: CPU=%s / fixed 2s, completed=%v", used, completed)
	if !completed || used > 400*time.Millisecond {
		t.Fatalf("catalog index consumed %s CPU / 2s or failed to finish its scan; maximum 400ms", used)
	}
}
