package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/uirequest"
)

// The plugin forms are validated before anything touches the bus, so a mistake
// is a message where the user typed it rather than a request nothing answers.
func TestOpenPluginValidation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		contains string
	}{
		{"collection without plugin", []string{"open", "--collection", "results"},
			"--collection needs the --plugin"},
		{"query without collection", []string{"open", "--plugin", "recall", "--query", "dex", "X-1"},
			"--query needs a --collection"},
		{"two instances", []string{"open", "--plugin", "recall", "--provider", "jira", "X-1"},
			"--plugin and --provider name different instances"},
		{"collection and diff", []string{"open", "--plugin", "recall", "--collection", "results", "--diff"},
			"--collection and --diff name different kinds"},
		{"collection and line", []string{"open", "--plugin", "recall", "--collection", "results", "--line", "4"},
			"--line does not apply to a plugin collection"},
		{"two rows", []string{"open", "--plugin", "recall", "--collection", "results", "a", "b"},
			"open accepts at most one target"},
		{"missing collection value", []string{"open", "--plugin", "recall", "--collection"},
			"--collection requires a collection id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != 2 {
				t.Fatalf("Run(%v) = handled %v, code %d; want true, 2", tt.args, handled, code)
			}
			combined := out.String() + errOut.String()
			if !strings.Contains(combined, tt.contains) {
				t.Fatalf("output for %v missing %q; got %q", tt.args, tt.contains, combined)
			}
		})
	}
}

// The request that reaches the bus is the collection tab, with its query, and
// with nothing a matcher would have had to claim.
func TestOpenPluginCollectionWritesACollectionTarget(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "active task")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")

	var out, errOut bytes.Buffer
	handled, code := Run([]string{
		"open", "--plugin", "recall", "--collection", "results", "--query", "dex",
		"--wait", "0", "--json",
	}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("open --plugin = handled %v, code %d: %s", handled, code, errOut.String())
	}
	req := lastRequestOnBus(t, stateHome)
	if req.Action != uirequest.ActionOpen {
		t.Fatalf("action = %q, want open", req.Action)
	}
	if req.Target.Kind != uirequest.TargetKindResource {
		t.Fatalf("target kind = %q, want resource", req.Target.Kind)
	}
	if req.Target.Provider != "recall" || req.Target.Collection != "results" || req.Target.Query != "dex" {
		t.Fatalf("target = %+v, want the collection and its query", req.Target)
	}
	if req.Target.Value != "" || req.Target.Matcher != "" {
		t.Fatalf("a collection target carried document fields: %+v", req.Target)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("open --json wrote no object: %q", out.String())
	}
}

// A positional beside a collection is the row inside it, not a locator, and it
// still consults no matcher.
func TestOpenPluginRowWritesAnItemTarget(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "active task")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")

	var out, errOut bytes.Buffer
	handled, code := Run([]string{
		"open", "--plugin", "ongoing", "--collection", "projects", "recall", "--wait", "0",
	}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("open --plugin row = handled %v, code %d: %s", handled, code, errOut.String())
	}
	req := lastRequestOnBus(t, stateHome)
	if req.Target.Collection != "projects" || req.Target.Value != "recall" || req.Target.Matcher != "" {
		t.Fatalf("target = %+v, want the row of that collection", req.Target)
	}
}

// --provider keeps meaning exactly what it meant: the matched locator form,
// with no collection anywhere in the request.
func TestOpenProviderStaysTheLocatorForm(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "active task")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--provider", "jira-work", "CASH-1245", "--wait", "0"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("open --provider = handled %v, code %d: %s", handled, code, errOut.String())
	}
	req := lastRequestOnBus(t, stateHome)
	if req.Target.Provider != "jira-work" || req.Target.Value != "CASH-1245" {
		t.Fatalf("target = %+v, want the provider's locator", req.Target)
	}
	if req.Target.Collection != "" {
		t.Fatalf("--provider grew a collection: %+v", req.Target)
	}
}

// `plugin changed` writes one record and nothing else. It reads no config and
// starts no plugin: only a running instance knows what is on screen.
func TestPluginChangedWritesOneRequest(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "active task")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"plugin", "changed", "dex", "--collection", "people", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("plugin changed = handled %v, code %d: %s", handled, code, errOut.String())
	}
	req := lastRequestOnBus(t, stateHome)
	if req.Action != uirequest.ActionPluginChanged {
		t.Fatalf("action = %q, want plugin-changed", req.Action)
	}
	payload, err := uirequest.DecodePluginChangedPayload(req.Payload)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.Instance != "dex" || payload.Collection != "people" {
		t.Fatalf("payload = %+v, want dex/people", payload)
	}
}

func TestPluginChangedRefusesNoPlugin(t *testing.T) {
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"plugin", "changed"}, &out, &errOut)
	if !handled || code != 2 {
		t.Fatalf("plugin changed with no id = handled %v, code %d; want true, 2", handled, code)
	}
	if !strings.Contains(out.String()+errOut.String(), "exactly one plugin id") {
		t.Fatalf("output did not say what was missing: %q", out.String()+errOut.String())
	}
}

// lastRequestOnBus reads the newest record the CLI wrote. The bus is one file
// per request under the state directory, so a test reads exactly what a running
// instance would.
func lastRequestOnBus(t *testing.T, stateHome string) uirequest.Request {
	t.Helper()
	dir := filepath.Join(stateHome, "sidecar", "requests")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read request bus: %v", err)
	}
	var newest uirequest.Request
	found := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var req uirequest.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			continue
		}
		if !found || req.CreatedAt.After(newest.CreatedAt) {
			newest, found = req, true
		}
	}
	if !found {
		t.Fatalf("no request was written to %s", dir)
	}
	return newest
}
