package workspacediff

import (
	"strings"
	"testing"
)

func TestParseFilesGitQuotedPaths(t *testing.T) {
	for _, tc := range []struct {
		name, header, markers, path string
	}{
		{"quote", `"a/docs/\"><img src=x>.md" "b/docs/\"><img src=x>.md"`, "", `docs/"><img src=x>.md`},
		{"octal UTF8", `"a/caf\303\251.md" "b/caf\303\251.md"`, "", "café.md"},
		{"controls", `"a/a\tline\n.md" "b/a\tline\n.md"`, "", "a\tline\n.md"},
		{"backslash", `"a/a\\b.md" "b/a\\b.md"`, "", `a\b.md`},
		{"space", "a/space name.md b/space name.md", "", "space name.md"},
		{"quoted destination", `a/old name.md "b/new\"name.md"`, "", `new"name.md`},
		{"quoted source", `"a/old\"name.md" b/new name.md`, "", "new name.md"},
		{"ambiguous header", "a/a b/file.md b/a b/file.md", "--- a/a b/file.md\n+++ b/a b/file.md\n", "a b/file.md"},
		{"binary ambiguous header", "a/a b/file.md b/a b/file.md", "Binary files a/a b/file.md and b/a b/file.md differ\n", "a b/file.md"},
		{"rename", "a/old.md b/new b/name.md", "rename from old.md\nrename to new b/name.md\n", "new b/name.md"},
		{"deleted", `"a/a\t.md" "b/a\t.md"`, "--- \"a/a\\t.md\"\n+++ /dev/null\n", "a\t.md"},
		{"malformed quote", `"a/bad\q" "b/bad\q"`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "diff --git " + tc.header + "\n" + tc.markers + "@@ -1 +1 @@\n-old\n+new\n"
			files := ParseFiles(raw)
			if tc.path == "" {
				if len(files) != 0 {
					t.Fatalf("malformed header returned %+v", files)
				}
				return
			}
			if len(files) != 1 || files[0].Path != tc.path {
				t.Fatalf("files = %+v, want path %q", files, tc.path)
			}
			if strings.TrimSuffix(files[0].Raw, "\n") != strings.TrimSuffix(raw, "\n") || files[0].Additions != 1 || files[0].Deletions != 1 {
				t.Fatalf("patch or counts changed: %+v", files[0])
			}
		})
	}
}
