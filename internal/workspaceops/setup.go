package workspaceops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type SetupOutcome struct {
	Kind, Action string
	Required     bool
	Err          error
}

// RunConfiguredSetup performs only repository-artifact setup. Durable Sidecar
// metadata and task/agent presentation stay with the calling application host.
func RunConfiguredSetup(ctx context.Context, plan *WorktreePlan) []SetupOutcome {
	if plan == nil {
		return []SetupOutcome{{Kind: "identity", Action: "read plan", Required: true, Err: fmt.Errorf("missing worktree plan")}}
	}
	var outcomes []SetupOutcome
	if plan.CopyEnv {
		for _, rel := range plan.EnvFiles {
			source, err := OpenContainedRegularFile(plan.MainWorktree, rel)
			if err == nil {
				err = CopyOpenFile(source, filepath.Join(plan.Path, rel))
				_ = source.Close()
			}
			outcomes = append(outcomes, SetupOutcome{Kind: "env-copy", Action: "copy " + rel, Err: err})
		}
	}
	if plan.RunHook {
		outcomes = append(outcomes, SetupOutcome{Kind: "setup-hook", Action: "run " + plan.HookPath, Required: plan.HookRequired, Err: RunSetupHookWithHook(ctx, plan, nil)})
	}
	return outcomes
}

func RunSetupHookWithHook(ctx context.Context, plan *WorktreePlan, beforeOpen func()) error {
	hook, err := OpenContainedRegularFileWithHook(plan.MainWorktree, plan.HookPath, beforeOpen)
	if err != nil {
		return fmt.Errorf("validate setup hook: %w", err)
	}
	defer hook.Close()
	cmd := exec.CommandContext(ctx, "bash", "/dev/fd/3")
	cmd.ExtraFiles = []*os.File{hook}
	cmd.Dir = plan.Path
	cmd.Env = applySetupEnv(os.Environ(), map[string]string{"GOWORK": "off", "GOFLAGS": "", "NODE_OPTIONS": "", "NODE_PATH": "", "PYTHONPATH": "", "VIRTUAL_ENV": ""})
	cmd.Env = append(cmd.Env, "MAIN_WORKTREE="+plan.MainWorktree, "SOURCE_WORKTREE="+plan.SourceWorktree, "WORKTREE_PATH="+plan.Path, "WORKTREE_BRANCH="+plan.Branch)
	// Hook output may contain secrets and never crosses this boundary.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("setup hook exited unsuccessfully: %w", err)
	}
	return nil
}

func applySetupEnv(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base))
	for _, entry := range base {
		if idx := strings.Index(entry, "="); idx >= 0 {
			values[entry[:idx]] = entry[idx+1:]
		}
	}
	for key, value := range overrides {
		if value == "" {
			delete(values, key)
		} else {
			values[key] = value
		}
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}
