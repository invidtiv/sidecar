package gitstatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStringToInt(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   int
		wantOK bool
	}{
		{"zero", "0", 0, true},
		{"single digit", "5", 5, true},
		{"multiple digits", "123", 123, true},
		{"large number", "999999", 999999, true},
		{"empty string", "", 0, true},
		{"non-digit", "abc", 0, false},
		{"mixed", "12a34", 0, false},
		{"negative sign", "-5", 0, false},
		{"decimal", "3.14", 0, false},
		{"spaces", "1 2", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var result int
			ok, _ := stringToInt(tc.input, &result)

			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && result != tc.want {
				t.Errorf("result = %d, want %d", result, tc.want)
			}
		})
	}
}

func TestStringToInt_Accumulates(t *testing.T) {
	// The function accumulates into the result pointer
	// Starting with non-zero value should work as documented
	var result int
	_, _ = stringToInt("12", &result)
	if result != 12 {
		t.Errorf("got %d, want 12", result)
	}
}

func TestGetNewFileDiff(t *testing.T) {
	// Create temp dir with a test file
	tmpDir := t.TempDir()
	testFile := "newfile.txt"
	content := "line1\nline2\nline3"
	err := os.WriteFile(filepath.Join(tmpDir, testFile), []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	diff, err := GetNewFileDiff(tmpDir, testFile)
	if err != nil {
		t.Fatalf("GetNewFileDiff failed: %v", err)
	}

	// Check diff header
	if !strings.Contains(diff, "diff --git") {
		t.Error("diff missing git header")
	}
	if !strings.Contains(diff, "new file mode") {
		t.Error("diff missing new file indicator")
	}
	if !strings.Contains(diff, "--- /dev/null") {
		t.Error("diff missing /dev/null source")
	}
	if !strings.Contains(diff, "+++ b/"+testFile) {
		t.Error("diff missing dest path")
	}
	if !strings.Contains(diff, "@@ -0,0") {
		t.Error("diff missing hunk header")
	}

	// Check all lines are additions
	lines := strings.Split(diff, "\n")
	var addCount int
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			addCount++
		}
	}
	if addCount != 3 {
		t.Errorf("expected 3 addition lines, got %d", addCount)
	}
}

func TestGetNewFileDiff_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := "empty.txt"
	err := os.WriteFile(filepath.Join(tmpDir, testFile), []byte(""), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	diff, err := GetNewFileDiff(tmpDir, testFile)
	if err != nil {
		t.Fatalf("GetNewFileDiff failed: %v", err)
	}

	if !strings.Contains(diff, "new file mode") {
		t.Error("diff missing new file indicator for empty file")
	}
}

func TestGetNewFileDiff_NotExists(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := GetNewFileDiff(tmpDir, "nonexistent.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestGetNewFileDiff_Binary(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := "image.png"
	// Content with null bytes to trigger binary detection
	content := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}
	if err := os.WriteFile(filepath.Join(tmpDir, testFile), content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	diff, err := GetNewFileDiff(tmpDir, testFile)
	if err != nil {
		t.Fatalf("GetNewFileDiff failed: %v", err)
	}

	if !strings.Contains(diff, "new file mode") {
		t.Error("binary diff should contain 'new file mode' header")
	}
	// Should use git's standard format: "Binary files ... differ"
	if !strings.Contains(diff, "Binary files") {
		t.Error("binary diff should contain 'Binary files' indicator")
	}
	if !strings.Contains(diff, "differ") {
		t.Error("binary diff should contain 'differ' suffix matching git format")
	}
}

func TestGetNewFileDiff_ParserCompatibility(t *testing.T) {
	// Verify that synthetic new-file diffs can be parsed by ParseMultiFileDiff
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "new.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	diff, err := GetNewFileDiff(tmpDir, "new.go")
	if err != nil {
		t.Fatalf("GetNewFileDiff failed: %v", err)
	}

	// Parse with the multi-file parser
	result := ParseMultiFileDiff(diff)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file diff, got %d", len(result.Files))
	}

	file := result.Files[0]
	if file.Diff.OldFile != "/dev/null" {
		t.Errorf("OldFile = %q, want /dev/null", file.Diff.OldFile)
	}
	if file.Diff.NewFile != "new.go" {
		t.Errorf("NewFile = %q, want new.go", file.Diff.NewFile)
	}
	if file.Additions < 3 {
		t.Errorf("expected at least 3 additions, got %d", file.Additions)
	}
	if file.Deletions != 0 {
		t.Errorf("expected 0 deletions for new file, got %d", file.Deletions)
	}
}

func TestParseMultiFileDiff_MixedTrackedAndNew(t *testing.T) {
	// Simulate a combined diff: one tracked file modification + one new file
	diff := `diff --git a/existing.go b/existing.go
--- a/existing.go
+++ b/existing.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 func main() {}
diff --git a/newfile.txt b/newfile.txt
new file mode 100644
--- /dev/null
+++ b/newfile.txt
@@ -0,0 +1,2 @@
+hello
+world`

	result := ParseMultiFileDiff(diff)
	if len(result.Files) != 2 {
		t.Fatalf("expected 2 file diffs, got %d", len(result.Files))
	}

	// First file: tracked modification
	if result.Files[0].Diff.NewFile != "existing.go" {
		t.Errorf("first file NewFile = %q, want existing.go", result.Files[0].Diff.NewFile)
	}
	if result.Files[0].Additions != 1 {
		t.Errorf("first file additions = %d, want 1", result.Files[0].Additions)
	}

	// Second file: new file
	if result.Files[1].Diff.NewFile != "newfile.txt" {
		t.Errorf("second file NewFile = %q, want newfile.txt", result.Files[1].Diff.NewFile)
	}
	if result.Files[1].Diff.OldFile != "/dev/null" {
		t.Errorf("second file OldFile = %q, want /dev/null", result.Files[1].Diff.OldFile)
	}
	if result.Files[1].Additions != 2 {
		t.Errorf("second file additions = %d, want 2", result.Files[1].Additions)
	}
}

func TestParseMultiFileDiff_BinaryNewFile(t *testing.T) {
	diff := `diff --git a/image.png b/image.png
new file mode 100644
Binary files /dev/null and b/image.png differ`

	result := ParseMultiFileDiff(diff)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file diff, got %d", len(result.Files))
	}

	if !result.Files[0].Diff.Binary {
		t.Error("binary new file should be marked as binary")
	}
}

func TestParseMultiFileDiff_EmptyDiffHasNoPhantomFile(t *testing.T) {
	for _, in := range []string{"", "\n", "   \n\n"} {
		result := ParseMultiFileDiff(in)
		if len(result.Files) != 0 {
			t.Errorf("ParseMultiFileDiff(%q) = %d files, want 0 (got %q)",
				in, len(result.Files), result.Files[0].FileName())
		}
	}
}

func TestParseMultiFileDiff_ModeOnlyChangeKeepsPath(t *testing.T) {
	// A chmod carries no ---/+++ pair; the path must come from the git header.
	diff := `diff --git a/scripts/run.sh b/scripts/run.sh
old mode 100644
new mode 100755`

	result := ParseMultiFileDiff(diff)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file diff, got %d", len(result.Files))
	}
	if got := result.Files[0].FileName(); got != "scripts/run.sh" {
		t.Errorf("FileName() = %q, want scripts/run.sh", got)
	}
}
