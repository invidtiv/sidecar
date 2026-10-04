package workspacediff

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Exercise the actual git entry points, including the snapshot shared by both
// desktop diff surfaces. A transport encoder cap is too late for any of these.
func TestDiffLoadersBoundOversizedGitPatches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "large.txt")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(path, []byte(strings.Repeat("changed line\n", 180000)), 0600); err != nil {
		t.Fatal(err)
	}
	check := func(name, raw string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Includes a small allowance for the desktop's visible truncation notice.
		if len(raw) > (768<<10)+256 {
			t.Fatalf("%s loaded %d bytes; want bounded prefix", name, len(raw))
		}
		if !strings.Contains(raw, "truncated") {
			t.Fatalf("%s did not report truncation", name)
		}
	}
	raw, err := LoadWorkingTreeFileDiff(ctx, root, "large.txt")
	check("working-tree file", raw, err)
	snap, err := LoadSnapshot(ctx, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	check("snapshot", snap.WorkingTree, nil)
	if !snap.Truncated || snap.State != LoadStateTruncated {
		t.Fatalf("snapshot state=%v truncated=%v", snap.State, snap.Truncated)
	}
	git("add", "large.txt")
	git("commit", "-m", "large")
	head := git("rev-parse", "HEAD")
	raw, err = LoadRangeDiff(ctx, root, Target{Kind: TargetRange, A: base, B: head, Dots: ".."})
	check("range", raw, err)
	raw, err = LoadCommitFileDiff(ctx, root, head, "large.txt", "")
	check("commit file", raw, err)
	raw, err = LoadCommitFileDiff(ctx, root, head, "large.txt", base)
	check("commit against parent", raw, err)
}

func TestBoundedGitStopsProducerAndSeparatesFailures(t *testing.T) {
	root := t.TempDir()
	script := `#!/bin/sh
case "$1" in
 emit) if head -c "$2" /dev/zero; then printf finished > "$3"; else exec tail -f /dev/null; fi ;;
 error) printf 'git diagnostic' >&2; exit 128 ;;
 cancel) exec tail -f /dev/null ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := GitOutputBounded(ctx, root, -1, "emit"); err == nil {
		t.Fatal("negative output cap accepted")
	}
	const limit = 1024
	marker := filepath.Join(root, "finished")
	patch, err := GitOutputBounded(ctx, root, limit, "emit", "67108864", marker)
	if err != nil {
		t.Fatal(err)
	}
	if !patch.Truncated || len(patch.Raw) != limit {
		t.Fatalf("patch len=%d truncated=%v", len(patch.Raw), patch.Truncated)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("producer reached its completion marker: %v", err)
	}
	patch, err = GitOutputBounded(ctx, root, limit, "emit", "1024", marker)
	if err != nil || patch.Truncated || len(patch.Raw) != limit {
		t.Fatalf("exact cap: len=%d truncated=%v err=%v", len(patch.Raw), patch.Truncated, err)
	}
	patch, err = GitOutputBounded(ctx, root, limit, "error")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 128 || !strings.Contains(err.Error(), "git diagnostic") || patch.Truncated {
		t.Fatalf("failure became truncation: %+v err=%v", patch, err)
	}
	cancelCtx, stop := context.WithCancel(context.Background())
	stop()
	patch, err = GitOutputBounded(cancelCtx, root, limit, "cancel")
	if !errors.Is(err, context.Canceled) || patch.Truncated {
		t.Fatalf("cancellation became truncation: %+v err=%v", patch, err)
	}
}

func TestTruncatedDiffRemainsVisibleInDesktopViews(t *testing.T) {
	v := &View{Target: Target{Kind: TargetRange, A: "abcdef123", B: "fedcba123"}}
	v.ApplyRangeMsg(RangeMsg{Identity: v.Target.Identity(), Raw: "diff --git a/file b/file\n--- a/file\n+++ b/file\n@@ -1 +1 @@\n-old\n+new\n", Truncated: true})
	if v.State != LoadStateTruncated {
		t.Fatalf("range state=%v", v.State)
	}
	v.Focus = FocusDiff
	for _, mode := range []ViewMode{ViewUnified, ViewSideBySide, ViewFullFile} {
		v.ViewMode = mode
		got := v.Render(80, 12, RenderOpts{PaintFile: func(string, string, ViewMode, int, int, int, int) string { return "painted diff" }})
		if !strings.Contains(got, "truncated") {
			t.Fatalf("mode=%v silently truncates: %s", mode, got)
		}
	}
	v.Target = Target{Kind: TargetCommit, A: "abcdef123"}
	v.CommitDetail = &CommitDetail{Hash: "abcdef123", Files: []CommitFile{{Path: "file"}}}
	v.ApplyCommitFileDiff(CommitFileDiffMsg{Identity: v.Target.Identity(), CommitHash: "abcdef123", FilePath: "file", Raw: "patch", Truncated: true})
	if got := v.renderCommitFileDiffPane(80, 12, 0, 0, RenderOpts{}); !strings.Contains(got, "truncated") {
		t.Fatalf("commit silently truncates: %s", got)
	}
}

// A real git command may start a helper that inherits its pipes. Killing only
// git must neither trap the caller in ReadAll nor trap Wait in stderr draining.
func TestBoundedGitStopsWhenDescendantHoldsPipes(t *testing.T) {
	root := t.TempDir()
	script := `#!/bin/sh
tail -f /dev/null &
child=$!
printf '%s\n' "$child" > "$1"
if [ "$2" = overflow ]; then head -c 67108864 /dev/zero; fi
wait
`
	if err := os.WriteFile(filepath.Join(root, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, mode := range []string{"cancellation", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = watcher.Close() }()
			if err = watcher.Add(root); err != nil {
				t.Fatal(err)
			}
			ready := filepath.Join(root, mode+".pid")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				patch Patch
				err   error
			}
			done := make(chan result, 1)
			go func() { patch, err := GitOutputBounded(ctx, root, 1024, ready, mode); done <- result{patch, err} }()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			pid := 0
			for pid == 0 {
				select {
				case <-watcher.Events:
					data, _ := os.ReadFile(ready)
					pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
				case err := <-watcher.Errors:
					t.Fatal(err)
				case <-deadline.C:
					t.Fatal("git helper did not signal readiness")
				}
			}
			child, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Kill() }()
			if mode == "cancellation" {
				cancel()
			}
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case got := <-done:
				if mode == "cancellation" && !errors.Is(got.err, context.Canceled) {
					t.Fatalf("cancellation: %+v", got)
				}
				if mode == "overflow" && (got.err != nil || !got.patch.Truncated || len(got.patch.Raw) != 1024) {
					t.Fatalf("overflow: %+v", got)
				}
			case <-timer.C:
				t.Error("bounded git blocked on inherited pipes")
				_ = child.Kill()
				cancel()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("git reader still blocked after helper cleanup")
				}
			}
		})
	}
}

func TestBoundedGitPreservesCompletedFailureAfterOverflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Wait for the actual process to exit before interpreting its bounded
	// stdout. That ordering removes cancellation/exit timing from the assertion.
	cmd := exec.CommandContext(ctx, "sh", "-c", "head -c 2048 /dev/zero; printf 'known git failure' >&2; exit 128")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	waitErr := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ExitCode() != 128 {
		t.Fatalf("fixture process: %v", waitErr)
	}
	patch, err := boundedGitResult(ctx, "", []string{"show"}, stdout.Bytes(), 1024, nil, waitErr, stderr.String())
	if !errors.As(err, &exit) || exit.ExitCode() != 128 || !strings.Contains(err.Error(), "known git failure") || patch.Truncated {
		t.Fatalf("known failure hidden by overflowing stdout: len=%d truncated=%v err=%v", len(patch.Raw), patch.Truncated, err)
	}
}

func TestBoundedGitAcceptsInternalStopAfterOverflow(t *testing.T) {
	out := []byte(strings.Repeat("x", 2048))
	for _, stopErr := range []error{context.Canceled, exec.ErrWaitDelay} {
		patch, err := boundedGitResult(context.Background(), "", []string{"show"}, out, 1024, nil, stopErr, "")
		if err != nil || !patch.Truncated || len(patch.Raw) != 1024 {
			t.Errorf("internal output stop %v became a caller failure: len=%d truncated=%v err=%v", stopErr, len(patch.Raw), patch.Truncated, err)
		}
		caller, cancel := context.WithCancel(context.Background())
		cancel()
		patch, err = boundedGitResult(caller, "", []string{"show"}, out, 1024, nil, stopErr, "")
		if !errors.Is(err, context.Canceled) || patch.Truncated {
			t.Errorf("caller cancellation became a prefix success: %+v err=%v", patch, err)
		}
	}
}
