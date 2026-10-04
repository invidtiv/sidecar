package cli

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

// validateImplicitCaller validates ambient identity before a resolver uses it.
// It must run before a fallback ladder: a contradiction is not a missing cue
// and must never become the unique instance or a project guessed from cwd.
func validateImplicitCaller(ctx context.Context, stateDir string) error {
	claimed := strings.TrimSpace(os.Getenv(shellstate.SessionEnv))
	if claimed == "" && (os.Getenv("TMUX") == "" || os.Getenv("TMUX_PANE") == "") {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	evidence := managedtarget.CallerEvidence{ClaimedSession: claimed, Cwd: canonicalOpenPath(cwd)}
	identity, identityErr := currentPaneIdentity(ctx)
	if identityErr == nil && claimed == "" && !strings.HasPrefix(identity.session, "sidecar-sh-") && !strings.HasPrefix(identity.session, "sidecar-ws-") {
		return nil // An ordinary tmux pane makes no managed Sidecar claim.
	}
	if identityErr == nil {
		evidence.PaneSession = identity.session
		evidence.PaneWorkDir = canonicalCallerPath(identity.path)
	}
	if claimed != "" {
		namespace := tmuxenv.Namespace()
		if identityErr == nil {
			namespace = identity.socket
		}
		origin, err := shellstate.LookupOrigin(stateDir, shellstate.Identity{TmuxName: claimed, Namespace: namespace})
		if err == nil {
			evidence.OriginVerified = true
			evidence.OriginWorkDir = canonicalCallerPath(origin.WorkDir)
		} else if strings.HasPrefix(claimed, "sidecar-ws-") && identityErr == nil && identity.session == claimed {
			if _, root, worktreeErr := currentManagedWorktree(ctx, stateDir, identity); worktreeErr == nil {
				evidence.OriginVerified = true
				evidence.OriginWorkDir = canonicalCallerPath(root)
			}
		} else if isShellStateError(err) {
			return err
		}
	}
	if evidence.PaneWorkDir == "" && evidence.OriginWorkDir != "" {
		projects, err := loadRegisteredProjects(stateDir)
		if err != nil {
			return err
		}
		if _, root, known := uniqueProjectContaining(projects, cwd); known {
			evidence.RegisteredCwdRoot = canonicalCallerPath(root)
		}
	}
	return managedtarget.ValidateCaller(evidence)
}

func canonicalCallerPath(path string) string {
	if path == "" {
		return ""
	}
	return canonicalOpenPath(path)
}

func isCallerConflict(err error) bool {
	var typed *managedtarget.Error
	return errors.As(err, &typed) && typed.Kind == managedtarget.CallerConflict
}
