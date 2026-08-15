package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCLIDocDrift fails when docs/reference/cli.md falls behind the registry.
// This is how you fix it:
//
//	REGEN_CLI_DOC=1 go test ./internal/cli/ -run TestRegenerateCLIDoc
func TestRegenerateCLIDoc(t *testing.T) {
	if os.Getenv("REGEN_CLI_DOC") == "" {
		t.Skip("set REGEN_CLI_DOC=1 to regenerate docs/reference/cli.md")
	}
	path := filepath.Join("..", "..", "docs", "reference", "cli.md")
	if err := os.WriteFile(path, []byte(RenderMarkdownDoc(RootCommand())), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
