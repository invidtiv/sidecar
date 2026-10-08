package cli

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxformat"
)

// A capture/transport failure is only a wakeup. The owner must still prove
// that the same server is alive and the exact pane is gone or retained dead.
// PrepareServer already keeps even the last managed session's server alive.
// Server death/replacement and unreadable source metadata never tombstone.
func mobileTerminalEndObserver(env Env) mobile.TerminalEndObserver {
	return mobileTerminalEndObserverWithRead(env, func(ctx context.Context, args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "tmux", tmuxformat.ClientArgs(args...)...).CombinedOutput()
		return strings.TrimRight(string(out), "\r\n"), err
	})
}

func mobileTerminalEndObserverWithRead(env Env, readTmux func(context.Context, ...string) (string, error)) mobile.TerminalEndObserver {
	return func(ctx context.Context, target mobile.ResolvedTarget) (*mobile.TerminalEnd, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if target.ServerPID <= 0 || target.Session == "" || target.Pane == "" {
			return nil, nil
		}
		var manifest string
		var definition shellstate.Definition
		if target.WorkspaceKind == "shell" {
			projects, err := loadRegisteredProjects(env.StateDir)
			if err != nil {
				return nil, err
			}
			for _, project := range projects {
				snapshot, err := shellstate.SnapshotAtPath(filepath.Join(project.Dir, "shells.json"))
				if err != nil {
					return nil, err
				}
				defs := append([]shellstate.Definition(nil), snapshot.Shells...)
				for _, tomb := range snapshot.Tombstones {
					defs = append(defs, tomb.Definition)
				}
				for _, def := range defs {
					if def.TmuxName == target.Session && (def.Namespace == "" || def.Namespace == tmuxenv.Namespace()) {
						if manifest != "" || project.Key != target.WorkspaceID || project.Path != target.ProjectRoot || def.CreatedAt.UTC().Format(time.RFC3339Nano) != target.DurableSessionCreated {
							return nil, fmt.Errorf("terminal end: source identity changed")
						}
						manifest, definition = filepath.Join(project.Dir, "shells.json"), def
					}
				}
			}
			if manifest == "" {
				return nil, nil
			}
		}
		read := func(args ...string) (string, error) { return readTmux(ctx, args...) }
		sameServer := func() bool {
			out, err := read("display-message", "-p", "#{pid}")
			return err == nil && strings.TrimSpace(out) == strconv.Itoa(target.ServerPID)
		}
		if !sameServer() {
			return nil, nil
		}
		panes, err := read("list-panes", "-a", "-F", "#{session_name}\t#{session_id}\t#{session_created}\t#{pane_id}\t#{pane_dead}\t#{pane_dead_status}")
		// With exit-empty off, an empty server answers display-message but
		// list-panes may say no current target. Confirm a successful empty
		// session inventory instead of classifying the error message.
		if err != nil {
			sessions, listErr := read("list-sessions", "-F", "#{session_name}")
			if listErr != nil || strings.TrimSpace(sessions) != "" {
				return nil, nil
			}
		}
		sessionPresent := false
		if err != nil {
			panes = ""
		} // A successful empty session inventory is positive evidence.
		for _, line := range strings.Split(panes, "\n") {
			if line == "" {
				continue
			}
			fields := strings.Split(line, "\t")
			if len(fields) != 6 || fields[0] == "" || fields[1] == "" || fields[2] == "" || fields[3] == "" || (fields[4] != "0" && fields[4] != "1") {
				return nil, fmt.Errorf("terminal end: incomplete pane inventory")
			}
			if fields[0] != target.Session {
				continue
			}
			sessionPresent = true
			if fields[1] != target.SessionID || fields[2] != target.SessionCreated {
				return nil, nil
			}
			if fields[3] != target.Pane {
				continue
			}
			if fields[4] != "1" || !sameServer() {
				return nil, nil
			}
			// remain-on-exit: retain both the pane and its record for explicit recovery.
			end := &mobile.TerminalEnd{}
			if status, err := strconv.Atoi(fields[5]); err == nil && status >= 0 && status <= 255 {
				end.ExitStatus = &status
			}
			return end, nil
		}

		if !sameServer() {
			return nil, nil
		}
		if manifest != "" && !sessionPresent {
			created, err := time.Parse(time.RFC3339Nano, target.DurableSessionCreated)
			if err != nil || !created.Equal(definition.CreatedAt) {
				return nil, fmt.Errorf("terminal end: durable identity changed")
			}
			if err := shellstate.RemoveIfUnchangedAtPath(manifest, shellstate.Identity{TmuxName: target.Session, Namespace: definition.Namespace}, created); err != nil {
				return nil, err
			}
		}
		return &mobile.TerminalEnd{}, nil
	}
}
