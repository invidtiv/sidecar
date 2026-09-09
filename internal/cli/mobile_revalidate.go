package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxformat"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Only the known read-only pane listing uses the existing control actor; Git
// inventory keeps its owning adapter and remains fresh before and after the
// pane check. This avoids adding a separate mobile candidate-selection path.
type mobilePaneInventoryRunner struct {
	manager  *tty.ControlManager
	session  string
	fallback workspaceinventory.Runner
}

func (r mobilePaneInventoryRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" {
		if !slices.Equal(args, tmuxformat.ClientArgs("list-panes", "-a", "-F", tty.PaneInventoryFormat)) {
			return nil, fmt.Errorf("mobile target: unsupported in-band inventory command")
		}
		panes, err := r.manager.HeadlessPaneInventory(ctx, r.session)
		if err != nil {
			return nil, &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate authority is unavailable: " + err.Error()}
		}
		return panes, nil
	}
	return r.fallback.Output(ctx, name, args...)
}

// A resolved shell is bound to its durable session, not its mutable display
// name. Re-read its source manifests on every operation without discovering
// unrelated Git worktrees, which otherwise spawns one Git process per project
// for every key. Both source reads are fresh; no cached authority grants input.
func revalidateMobileShell(ctx context.Context, stateDir string, target mobile.ResolvedTarget, inspect mobile.CandidateInspector) (mobile.ResolvedTarget, error) {
	if target.WorkspaceKind != "shell" || target.Session == "" || target.Pane == "" || target.DurableSessionCreated == "" {
		return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "managed shell authority is incomplete"}
	}
	if _, err := mobileShellSource(ctx, stateDir, target); err != nil {
		return mobile.ResolvedTarget{}, err
	}
	identity, err := inspect(ctx, target.Pane)
	if err != nil {
		return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "managed shell authority is unavailable: " + err.Error()}
	}
	current, err := mobileShellSource(ctx, stateDir, target)
	if err != nil {
		return mobile.ResolvedTarget{}, err
	}
	applyMobilePaneIdentity(&current, identity)
	return current, nil
}

func mobileShellSource(ctx context.Context, stateDir string, target mobile.ResolvedTarget) (mobile.ResolvedTarget, error) {
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		return mobile.ResolvedTarget{}, err
	}
	var found *mobile.ResolvedTarget
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return mobile.ResolvedTarget{}, err
		}
		shells, err := shellstate.ListAtPath(filepath.Join(project.Dir, "shells.json"))
		if err != nil {
			return mobile.ResolvedTarget{}, err
		}
		for _, shell := range shells {
			if shell.TmuxName != target.Session || (shell.Namespace != "" && shell.Namespace != tmuxenv.Namespace()) {
				continue
			}
			if found != nil {
				return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "managed shell has duplicate source identities"}
			}
			if project.Key != target.WorkspaceID || project.Path != target.ProjectRoot || shell.CreatedAt.IsZero() || shell.CreatedAt.UTC().Format(time.RFC3339Nano) != target.DurableSessionCreated {
				return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "managed shell source identity changed"}
			}
			current := target
			current.DisplayName = shell.DisplayName
			found = &current
		}
	}
	if found == nil {
		return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: fmt.Sprintf("managed shell %q is no longer registered", target.Session)}
	}
	return *found, nil
}

func applyMobilePaneIdentity(target *mobile.ResolvedTarget, identity tty.HeadlessTargetIdentity) {
	target.Session, target.Pane = identity.Session, identity.Pane
	target.ServerPID, target.SessionID, target.SessionCreated = identity.ServerPID, identity.SessionID, identity.SessionCreated
	target.Width, target.Height, target.PaneCount = identity.Width, identity.Height, identity.PaneCount
}
