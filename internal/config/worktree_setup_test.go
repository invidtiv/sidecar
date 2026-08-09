package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWorktreeSetupGlobalAndProjectOverride(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "repo")
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	data := `{
  "projects": {"list": [{"name":"repo","path":"` + project + `","worktreeSetup":{"copyEnvFiles":false,"envFiles":[],"runHook":false,"hookPath":"scripts/setup","hookRequired":false}}]},
  "plugins": {"workspace": {"worktreeSetup":{"copyEnvFiles":true,"envFiles":[".env.secret"],"runHook":true,"hookPath":"setup.sh","hookRequired":true}}}
}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.WorktreeSetupForProject(project); got.CopyEnvFiles || got.RunHook || got.HookPath != "scripts/setup" {
		t.Fatalf("project setup = %+v", got)
	}
	if got := cfg.WorktreeSetupForProject(filepath.Join(root, "other")); !got.CopyEnvFiles || !got.RunHook || got.HookPath != "setup.sh" || len(got.EnvFiles) != 1 {
		t.Fatalf("global setup = %+v", got)
	}
}
