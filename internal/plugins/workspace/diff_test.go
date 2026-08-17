package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeBaseHashValidation(t *testing.T) {
	// Test the hash validation logic used in getDiffFromBase
	tests := []struct {
		name      string
		mbOutput  string
		shouldUse bool // Should use merge-base hash
	}{
		{
			name:      "valid sha",
			mbOutput:  "abc123def456789012345678901234567890abcd\n",
			shouldUse: true,
		},
		{
			name:      "valid sha no newline",
			mbOutput:  "abc123def456789012345678901234567890abcd",
			shouldUse: true,
		},
		{
			name:      "empty output",
			mbOutput:  "",
			shouldUse: false,
		},
		{
			name:      "too short",
			mbOutput:  "abc123\n",
			shouldUse: false,
		},
		{
			name:      "only whitespace",
			mbOutput:  "\n\n",
			shouldUse: false,
		},
		{
			name:      "exactly 40 chars",
			mbOutput:  "1234567890123456789012345678901234567890",
			shouldUse: true,
		},
		{
			name:      "39 chars",
			mbOutput:  "123456789012345678901234567890123456789",
			shouldUse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the validation logic from getDiffFromBase
			mbHash := strings.TrimSpace(tt.mbOutput)
			canUse := len(mbHash) >= 40

			if canUse != tt.shouldUse {
				t.Errorf("hash validation for %q: got canUse=%v, want %v", tt.mbOutput, canUse, tt.shouldUse)
			}
		})
	}
}

func TestGetUnpushedCommits_EmptyInputs(t *testing.T) {
	tests := []struct {
		name         string
		workdir      string
		remoteBranch string
	}{
		{"empty workdir", "", "origin/main"},
		{"empty remoteBranch", "/tmp/repo", ""},
		{"both empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getUnpushedCommits(tt.workdir, tt.remoteBranch)
			if result != nil {
				t.Errorf("expected nil, got %v", result)
			}
		})
	}
}

func TestGetUnpushedCommits_InvalidRemote(t *testing.T) {
	tmpDir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	result := getUnpushedCommits(tmpDir, "nonexistent/branch")
	if result != nil {
		t.Errorf("expected nil for invalid remote, got %v", result)
	}
}

func TestGetUnpushedCommits_Integration(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize git repo
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v failed: %v", args, err)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	// Create initial commit
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "test.txt")
	run("commit", "-m", "initial")

	// Create a "remote" branch pointing to current commit
	run("branch", "origin/main")

	// Create unpushed commits
	for i := 1; i <= 3; i++ {
		content := []byte(strings.Repeat("x", i))
		if err := os.WriteFile(testFile, content, 0644); err != nil {
			t.Fatal(err)
		}
		run("add", "test.txt")
		run("commit", "-m", "commit")
	}

	// Get unpushed commits
	unpushed := getUnpushedCommits(tmpDir, "origin/main")
	if unpushed == nil {
		t.Fatal("expected non-nil map")
	}
	if len(unpushed) != 3 {
		t.Errorf("expected 3 unpushed commits, got %d", len(unpushed))
	}
}

func TestGetUnpushedCommits_AllPushed(t *testing.T) {
	tmpDir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		_ = cmd.Run()
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	testFile := filepath.Join(tmpDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("content"), 0644)
	run("add", "test.txt")
	run("commit", "-m", "commit")

	// Remote branch points to HEAD (all pushed)
	run("branch", "origin/main")

	unpushed := getUnpushedCommits(tmpDir, "origin/main")
	if unpushed == nil {
		t.Fatal("expected empty map, got nil")
	}
	if len(unpushed) != 0 {
		t.Errorf("expected 0 unpushed commits, got %d", len(unpushed))
	}
}

func TestGetWorktreeCommits_Integration(t *testing.T) {
	tmpDir := t.TempDir()

	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		return cmd.Run()
	}

	_ = run("init")
	_ = run("config", "user.email", "test@test.com")
	_ = run("config", "user.name", "Test")

	// Create main branch with initial commit
	testFile := filepath.Join(tmpDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("initial"), 0644)
	_ = run("add", "test.txt")
	_ = run("commit", "-m", "initial")
	_ = run("branch", "-M", "main")

	// Create feature branch
	_ = run("checkout", "-b", "feature")

	// Add commits on feature branch
	for i := 1; i <= 2; i++ {
		_ = os.WriteFile(testFile, []byte(strings.Repeat("x", i)), 0644)
		_ = run("add", "test.txt")
		_ = run("commit", "-m", "feature commit")
	}

	// Test: get commits comparing to main
	commits, err := getWorktreeCommits(tmpDir, "main")
	if err != nil {
		t.Fatalf("getWorktreeCommits failed: %v", err)
	}

	if len(commits) != 2 {
		t.Errorf("expected 2 commits, got %d", len(commits))
	}

	// All commits should be marked as not pushed (no remote tracking)
	for _, c := range commits {
		if c.Pushed {
			t.Errorf("commit %s should not be marked as pushed", c.Hash)
		}
	}
}

func TestGetUntrackedFileDiffs_Integration(t *testing.T) {
	tmpDir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v failed: %v", args, err)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	// Create initial commit so HEAD exists
	tracked := filepath.Join(tmpDir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("tracked content"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "initial")

	// Create untracked files
	if err := os.WriteFile(filepath.Join(tmpDir, "new1.txt"), []byte("hello\nworld"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "new2.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result := getUntrackedFileDiffs(tmpDir)

	// Should contain synthetic diffs for both files
	if !strings.Contains(result, "new file mode") {
		t.Error("result should contain 'new file mode' header")
	}
	if !strings.Contains(result, "new1.txt") {
		t.Error("result should contain new1.txt")
	}
	if !strings.Contains(result, "new2.go") {
		t.Error("result should contain new2.go")
	}
	if !strings.Contains(result, "+hello") {
		t.Error("result should contain '+hello' addition line")
	}
	if !strings.Contains(result, "+package main") {
		t.Error("result should contain '+package main' addition line")
	}
}

func TestGetUntrackedFileDiffs_RespectsGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v failed: %v", args, err)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	// Create .gitignore and initial commit
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "init.txt"), []byte("init"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	// Create files — one ignored, one not
	if err := os.WriteFile(filepath.Join(tmpDir, "debug.log"), []byte("log data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "visible.txt"), []byte("visible"), 0644); err != nil {
		t.Fatal(err)
	}

	result := getUntrackedFileDiffs(tmpDir)

	if strings.Contains(result, "debug.log") {
		t.Error("gitignored file debug.log should not appear in result")
	}
	if !strings.Contains(result, "visible.txt") {
		t.Error("non-ignored file visible.txt should appear in result")
	}
}

func TestGetUntrackedFileDiffs_NoUntrackedFiles(t *testing.T) {
	tmpDir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v failed: %v", args, err)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	result := getUntrackedFileDiffs(tmpDir)
	if result != "" {
		t.Errorf("expected empty result with no untracked files, got %q", result)
	}
}

func TestGetUntrackedFileDiff_LargeFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file larger than maxUntrackedFileSize (1MB)
	largeContent := strings.Repeat("x", maxUntrackedFileSize+100)
	if err := os.WriteFile(filepath.Join(tmpDir, "large.bin"), []byte(largeContent), 0644); err != nil {
		t.Fatal(err)
	}

	diff, err := getUntrackedFileDiff(tmpDir, "large.bin")
	if err != nil {
		t.Fatalf("getUntrackedFileDiff failed: %v", err)
	}

	if !strings.Contains(diff, "File too large to display") {
		t.Error("large file should show size warning")
	}
	if !strings.Contains(diff, "new file mode") {
		t.Error("large file diff should still have new file header")
	}
	// Should NOT contain the actual file content
	if strings.Contains(diff, strings.Repeat("x", 100)) {
		t.Error("large file diff should not contain actual file content")
	}
}

func TestGetUntrackedFileDiff_NormalFile(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("hello\nworld"), 0644); err != nil {
		t.Fatal(err)
	}

	diff, err := getUntrackedFileDiff(tmpDir, "hello.txt")
	if err != nil {
		t.Fatalf("getUntrackedFileDiff failed: %v", err)
	}

	if !strings.Contains(diff, "diff --git a/hello.txt b/hello.txt") {
		t.Error("diff should contain git header")
	}
	if !strings.Contains(diff, "--- /dev/null") {
		t.Error("diff should show /dev/null source")
	}
	if !strings.Contains(diff, "+hello") {
		t.Error("diff should contain addition lines")
	}
}

func TestGetUntrackedFileDiff_Nonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := getUntrackedFileDiff(tmpDir, "nonexistent.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestGetDiff_IncludesUntrackedFiles(t *testing.T) {
	tmpDir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v failed: %v", args, err)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	// Create initial tracked file
	tracked := filepath.Join(tmpDir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "initial")

	// Modify tracked file and create untracked file
	if err := os.WriteFile(tracked, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "untracked.txt"), []byte("new file"), 0644); err != nil {
		t.Fatal(err)
	}

	_, raw, err := getDiff(tmpDir)
	if err != nil {
		t.Fatalf("getDiff failed: %v", err)
	}

	// Should contain tracked file diff
	if !strings.Contains(raw, "tracked.txt") {
		t.Error("diff should contain tracked file changes")
	}
	// Should also contain untracked file synthetic diff
	if !strings.Contains(raw, "untracked.txt") {
		t.Error("diff should contain untracked file synthetic diff")
	}
	if !strings.Contains(raw, "new file mode") {
		t.Error("diff should contain 'new file mode' for untracked file")
	}
	if !strings.Contains(raw, "+new file") {
		t.Error("diff should contain addition lines for untracked file content")
	}
}

func TestGetWorktreeCommits_WithRemoteTracking(t *testing.T) {
	tmpDir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		_ = cmd.Run()
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	testFile := filepath.Join(tmpDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("initial"), 0644)
	run("add", "test.txt")
	run("commit", "-m", "initial")
	run("branch", "-M", "main")

	run("checkout", "-b", "feature")

	// Create commits
	_ = os.WriteFile(testFile, []byte("x"), 0644)
	run("add", "test.txt")
	run("commit", "-m", "commit1")

	_ = os.WriteFile(testFile, []byte("xx"), 0644)
	run("add", "test.txt")
	run("commit", "-m", "commit2")

	commits, err := getWorktreeCommits(tmpDir, "main")
	if err != nil {
		t.Fatalf("getWorktreeCommits failed: %v", err)
	}

	if len(commits) != 2 {
		t.Errorf("expected 2 commits, got %d", len(commits))
	}

	// Without remote tracking, all commits should be marked as not pushed
	for _, c := range commits {
		if c.Pushed {
			t.Errorf("commit %s should not be marked as pushed (no remote tracking)", c.Hash)
		}
	}
}
