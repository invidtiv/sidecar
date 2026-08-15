package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryCompleteness(t *testing.T) {
	root := RootCommand()
	var check func(cmd *Command, path string)
	check = func(cmd *Command, path string) {
		full := path + " " + cmd.Name
		if cmd.Name == "" {
			t.Errorf("command at %s missing Name", path)
		}
		if cmd.Summary == "" {
			t.Errorf("command %s missing Summary", full)
		}
		if cmd.Usage == "" {
			t.Errorf("command %s missing Usage", full)
		}
		if len(cmd.Sub) == 0 {
			if len(cmd.ExitCodes) == 0 {
				t.Errorf("leaf command %s missing ExitCodes", full)
			}
			if len(cmd.Examples) == 0 {
				t.Errorf("leaf command %s missing Examples", full)
			}
		}
		for _, sub := range cmd.Sub {
			check(sub, full)
		}
	}

	for _, sub := range root.Sub {
		check(sub, "sidecar")
	}
}

func TestHelpJSONTree(t *testing.T) {
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"help", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("Run(help --json) = handled %v, code %d, stderr %q", handled, code, errOut.String())
	}
	var data map[string]any
	if err := json.Unmarshal(out.Bytes(), &data); err != nil {
		t.Fatalf("failed to parse JSON help output: %v\nOutput: %s", err, out.String())
	}
	if data["name"] != "sidecar" {
		t.Errorf("expected name 'sidecar', got %v", data["name"])
	}
	subcommands, ok := data["subcommands"].([]any)
	if !ok || len(subcommands) == 0 {
		t.Fatalf("expected subcommands in JSON output, got %v", data["subcommands"])
	}
}

func TestHelpDispatches(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		code     int
		contains string
	}{
		{"root help arg", []string{"help"}, 0, "Usage: sidecar <command>"},
		{"root help flag", []string{"--help"}, 0, "Usage: sidecar <command>"},
		{"root short help flag", []string{"-h"}, 0, "Usage: sidecar <command>"},
		{"subcommand help via help verb", []string{"help", "open"}, 0, "Usage: sidecar open [options] [<target>]"},
		{"subcommand help via flag", []string{"open", "--help"}, 0, "Usage: sidecar open [options] [<target>]"},
		{"nested subcommand help via help verb", []string{"help", "shell", "rename"}, 0, "Usage: sidecar shell rename"},
		{"nested subcommand help via flag", []string{"shell", "rename", "-h"}, 0, "Usage: sidecar shell rename"},
		{"unknown command via help", []string{"help", "nonexistent"}, 2, "unknown command \"nonexistent\""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != tt.code {
				t.Fatalf("Run(%v) = %v, %d; want true, %d", tt.args, handled, code, tt.code)
			}
			combined := out.String() + errOut.String()
			if !strings.Contains(combined, tt.contains) {
				t.Fatalf("output for %v missing %q; got %q", tt.args, tt.contains, combined)
			}
		})
	}
}

func TestCLIDocDrift(t *testing.T) {
	root := RootCommand()
	generated := RenderMarkdownDoc(root)

	docPath := filepath.Join("..", "..", "docs", "reference", "cli.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read docs/reference/cli.md: %v", err)
	}

	if string(content) != generated {
		t.Errorf("docs/reference/cli.md does not match generated doc from registry!\nExpected:\n%s\nGot:\n%s", generated, string(content))
	}
}
