package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxformat"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceops"
)

func mobileShellRevalidationFixture(t *testing.T) (string, mobile.ResolvedTarget, func(string, string)) {
	t.Helper()
	state := t.TempDir()
	created := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	target := mobile.ResolvedTarget{WorkspaceID: "repo", WorkspaceKind: "shell", ProjectRoot: "/fixture/repo", Session: "sidecar-sh-perf", Pane: "%7", DurableSessionCreated: created.Format(time.RFC3339Nano)}
	write := func(project, name string) {
		t.Helper()
		dir := filepath.Join(state, "projects", project)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		meta, _ := json.Marshal(map[string]string{"path": target.ProjectRoot})
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o600); err != nil {
			t.Fatal(err)
		}
		manifest, _ := json.Marshal(map[string]any{"version": 1, "shells": []shellstate.Definition{{TmuxName: target.Session, DisplayName: name, Namespace: tmuxenv.Namespace(), CreatedAt: created}}})
		if err := os.WriteFile(filepath.Join(dir, "shells.json"), manifest, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("repo", "Original")
	return state, target, write
}

func TestMobileShellRevalidationUsesFreshDurableSourceWithoutGitDiscovery(t *testing.T) {
	state, target, write := mobileShellRevalidationFixture(t)
	// Any attempted Git discovery or tmux subprocess would now fail. The
	// injected inspector represents the attachment's existing control actor.
	t.Setenv("PATH", t.TempDir())
	write("repo", "Renamed")
	got, err := revalidateMobileShell(context.Background(), state, target, func(_ context.Context, pane string) (tty.HeadlessTargetIdentity, error) {
		if pane != "%7" {
			t.Fatalf("inspected another pane %q", pane)
		}
		return tty.HeadlessTargetIdentity{Session: target.Session, Pane: pane, ServerPID: 42, SessionID: "$1", SessionCreated: "123", Width: 80, Height: 24, PaneCount: 1}, nil
	})
	if err != nil || got.DisplayName != "Renamed" || got.ServerPID != 42 || got.DurableSessionCreated != target.DurableSessionCreated {
		t.Fatalf("revalidation = %+v, %v", got, err)
	}
}

func TestMobileShellRevalidationRefusesChangedOrDuplicateSource(t *testing.T) {
	for _, change := range []string{"removed", "creation changed", "project changed", "duplicate", "changed during inspection"} {
		t.Run(change, func(t *testing.T) {
			state, target, write := mobileShellRevalidationFixture(t)
			manifest := filepath.Join(state, "projects", "repo", "shells.json")
			switch change {
			case "removed":
				if err := os.Remove(manifest); err != nil {
					t.Fatal(err)
				}
			case "creation changed":
				target.DurableSessionCreated = "different"
			case "project changed":
				target.ProjectRoot = "/different"
			case "duplicate":
				write("duplicate", "Other")
			}
			_, err := revalidateMobileShell(context.Background(), state, target, func(_ context.Context, pane string) (tty.HeadlessTargetIdentity, error) {
				if change == "changed during inspection" {
					if err := os.Remove(manifest); err != nil {
						t.Fatal(err)
					}
				}
				return tty.HeadlessTargetIdentity{Session: target.Session, Pane: pane}, nil
			})
			if err == nil {
				t.Fatal("changed source accepted")
			}
		})
	}
}

func TestMobileLegacyShellRevalidationRetainsWorktreeCollisionRefusal(t *testing.T) {
	state, target, _ := mobileShellRevalidationFixture(t)
	target.Session = workspaceops.WorktreeSessionName(target.ProjectRoot, "")
	target.Selector = target.Session
	created, _ := time.Parse(time.RFC3339Nano, target.DurableSessionCreated)
	manifest, _ := json.Marshal(map[string]any{"version": 1, "shells": []shellstate.Definition{{TmuxName: target.Session, Namespace: tmuxenv.Namespace(), CreatedAt: created}}})
	if err := os.WriteFile(filepath.Join(state, "projects", "repo", "shells.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := tty.NewControlManager()
	t.Cleanup(manager.Stop)
	_, err := mobileTargetRevalidator(Env{StateDir: state, Ctx: context.Background()}, manager)(context.Background(), target)
	var refused *mobile.ResolveError
	if !errors.As(err, &refused) || refused.Code != mobileproto.ErrorAmbiguous {
		t.Fatalf("legacy shell/worktree collision = %v", err)
	}
}

func TestMobileShellRevalidationUnavailablePaneRevokesIdentity(t *testing.T) {
	state, target, _ := mobileShellRevalidationFixture(t)
	_, err := revalidateMobileShell(context.Background(), state, target, func(context.Context, string) (tty.HeadlessTargetIdentity, error) {
		return tty.HeadlessTargetIdentity{}, errors.New("owning control connection is unavailable")
	})
	var refused *mobile.ResolveError
	if !errors.As(err, &refused) || refused.Code != mobileproto.ErrorIdentityChanged {
		t.Fatalf("unavailable pane authority = %v", err)
	}
}

func TestMobileCandidateUnavailableControlRevokesIdentity(t *testing.T) {
	manager := tty.NewControlManager()
	manager.Stop()
	runner := mobilePaneInventoryRunner{manager: manager, session: "gone"}
	_, err := runner.Output(context.Background(), "tmux", tmuxformat.ClientArgs("list-panes", "-a", "-F", tty.PaneInventoryFormat)...)
	var refused *mobile.ResolveError
	if !errors.As(err, &refused) || refused.Code != mobileproto.ErrorIdentityChanged {
		t.Fatalf("unavailable candidate authority = %v", err)
	}
}
