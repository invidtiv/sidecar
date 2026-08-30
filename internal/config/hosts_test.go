package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hostsFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)
	return path
}

// An entry with no name of its own answers to its target, because that is the
// defaulting hosts.FromConfig does when it builds the running registry.
func TestHostIDDefaultsToTheTarget(t *testing.T) {
	host := NormalizeHost(HostConfig{Target: "  marcusbook  "})
	if host.ID != "marcusbook" || host.Target != "marcusbook" {
		t.Fatalf("normalized = %+v, want the target as both id and target", host)
	}
	if got := HostIDFor(HostConfig{Target: "book"}); got != "book" {
		t.Fatalf("HostIDFor = %q, want the target", got)
	}
}

func TestValidateHostRefusals(t *testing.T) {
	existing := []HostConfig{{ID: "book", Target: "marcusbook"}, {Target: "proof-host"}}
	for _, tt := range []struct {
		name string
		host HostConfig
		skip int
		want string
	}{
		{"empty target", HostConfig{ID: "book"}, -1, "SSH target"},
		{"duplicate name", HostConfig{ID: "book", Target: "elsewhere"}, -1, "already registered"},
		{"duplicate name by case", HostConfig{ID: "BOOK", Target: "elsewhere"}, -1, "already registered"},
		{"duplicate defaulted name", HostConfig{Target: "proof-host"}, -1, "already registered"},
		{"name with a space", HostConfig{ID: "my book", Target: "elsewhere"}, -1, "spaces"},
		{"malformed env", HostConfig{Target: "elsewhere", Env: []string{"NOPE"}}, -1, "KEY=VALUE"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			message := ValidateHost(existing, tt.host, tt.skip)
			if !strings.Contains(message, tt.want) {
				t.Fatalf("ValidateHost = %q, want it to mention %q", message, tt.want)
			}
		})
	}

	// An edit must not collide with the entry being edited.
	if message := ValidateHost(existing, HostConfig{ID: "book", Target: "marcusbook.local"}, 0); message != "" {
		t.Fatalf("editing a host collided with itself: %q", message)
	}
}

// The registry survives a round trip through the writer. Until hosts became a
// managed key, Save carried the section forward as an unknown key and silently
// dropped anything a caller had changed.
func TestHostsRoundTripThroughSave(t *testing.T) {
	path := hostsFixture(t)

	added, err := AddHost(HostConfig{Target: "marcusbook", ID: "book", Env: []string{"TMUX_TMPDIR=/tmp/proof"}})
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if added.ID != "book" {
		t.Fatalf("added = %+v", added)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts.List) != 1 || cfg.Hosts.List[0].Target != "marcusbook" {
		t.Fatalf("reloaded hosts = %+v", cfg.Hosts.List)
	}

	if _, err := AddHost(HostConfig{Target: "elsewhere", ID: "book"}); err == nil {
		t.Fatal("a duplicate name was accepted")
	} else if !IsHostValueRejection(err) {
		t.Fatalf("duplicate name reported as %v, want a value rejection", err)
	}

	if _, err := UpdateHost("book", func(host *HostConfig) { host.Disabled = true }); err != nil {
		t.Fatalf("UpdateHost: %v", err)
	}
	cfg, _ = Load()
	if !cfg.Hosts.List[0].Disabled {
		t.Fatal("the disabled switch did not survive the save")
	}
	if len(cfg.Hosts.List[0].Env) != 1 {
		t.Fatalf("env was lost by an unrelated edit: %+v", cfg.Hosts.List[0])
	}

	if _, err := UpdateHost("nobody", func(*HostConfig) {}); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("UpdateHost on an unknown host = %v, want ErrHostNotFound", err)
	}
	if _, err := RemoveHost("nobody"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("RemoveHost on an unknown host = %v, want ErrHostNotFound", err)
	}

	if _, err := RemoveHost("book"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	// The key goes with the last entry. Leaving it behind would let Save's
	// unknown-key preservation resurrect a host the user just unregistered.
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["hosts"]; present {
		t.Fatalf("removing the last host left a hosts key behind:\n%s", data)
	}
}

// A save that is about something else must not disturb a hand-written registry.
func TestUnrelatedSavePreservesHosts(t *testing.T) {
	hostsFixture(t)
	if _, err := AddHost(HostConfig{Target: "marcusbook"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveUI(func(ui *UIConfig) { ui.ShowClock = true }); err != nil {
		t.Fatalf("SaveUI: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts.List) != 1 {
		t.Fatalf("an unrelated save changed the registry: %+v", cfg.Hosts.List)
	}
}
