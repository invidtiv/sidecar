package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/contentservice"
)

func TestFilesFindCLI(t *testing.T) {
	root, workspace, cfg := setupContentCLI(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfg, "files", "find", "read", "--project", "demo", "--workspace", workspace, "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("%v %d %s", handled, code, errOut.String())
	}
	var result contentservice.FileSearchResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || result.Results[0].Path != "README.md" {
		t.Fatalf("%s", out.String())
	}
	for _, args := range [][]string{{"files", "find", "read"}, {"files", "find", "read", "--limit", "101"}, {"files", "nope"}} {
		out.Reset()
		errOut.Reset()
		handled, code = Run(args, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("%v: %v %d %s", args, handled, code, errOut.String())
		}
	}
}
