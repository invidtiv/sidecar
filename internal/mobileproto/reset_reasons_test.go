package mobileproto

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The reset reason table in mobile-protocol.md is the contract clients read.
// It must list exactly ResetReasons: a reason the code knows and the document
// does not is undiscoverable, and a documented one the code lacks is a lie.
func TestResetReasonsAreExactlyTheDocumentedSet(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "mobile-protocol.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(doc), "\n## Reset reasons\n")
	if !found {
		t.Fatal("mobile-protocol.md has no \"## Reset reasons\" section")
	}
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")
	documented := map[string]bool{}
	for _, match := range row.FindAllStringSubmatch(section, -1) {
		if documented[match[1]] {
			t.Errorf("reset reason %q is documented twice", match[1])
		}
		documented[match[1]] = true
	}
	known := map[string]bool{}
	for _, reason := range ResetReasons {
		if known[reason] {
			t.Errorf("ResetReasons lists %q twice", reason)
		}
		known[reason] = true
		if !documented[reason] {
			t.Errorf("reset reason %q is not in the mobile-protocol.md table", reason)
		}
	}
	for reason := range documented {
		if !known[reason] {
			t.Errorf("mobile-protocol.md documents reset reason %q, which is not in ResetReasons", reason)
		}
	}
	if !IsResetReason(ResetResize) || IsResetReason("") || IsResetReason("reset") {
		t.Fatal("IsResetReason disagrees with ResetReasons")
	}
}

// Fixtures are client test vectors, so every reset reason in them must be one
// the contract names.
func TestFixtureResetReasonsAreDocumented(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "mobile-protocol", "v0")
	reason := regexp.MustCompile(`"type"\s*:\s*"reset"[^{}]*?"reason"\s*:\s*"([^"]*)"|"reason"\s*:\s*"([^"]*)"[^{}]*?"type"\s*:\s*"reset"`)
	var seen []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range reason.FindAllStringSubmatch(string(data), -1) {
			value := match[1] + match[2]
			seen = append(seen, value)
			if !IsResetReason(value) {
				t.Errorf("%s: reset reason %q is not in ResetReasons", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("no reset events found in the fixtures; the scan no longer matches their shape")
	}
}
