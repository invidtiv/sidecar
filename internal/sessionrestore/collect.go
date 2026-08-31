package sessionrestore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

// Collecting the planner's input from the real machine.
//
// This lives here rather than in the CLI because the plan requires the startup
// restore and the CLI to run the same planner and executor — "it is not a hidden
// TUI implementation". Two collectors would be two answers to "what is
// restorable", and the one the user read in `session status` would not be the
// one that ran.
//
// Everything that touches the world is an injected function with a real default,
// so the whole collection is drivable in a test without a tmux server.

// Collector reads the machine's restore candidates.
type Collector struct {
	// StateDir is Sidecar's state root; project manifests live under
	// <StateDir>/projects/<key>/shells.json.
	StateDir string
	// Namespace is the tmux socket path this restore is scoped to. Records from
	// another namespace belong to a different tmux server and are not this
	// restore's business.
	Namespace string

	// LiveSessions returns the tmux session names that currently exist.
	LiveSessions func(context.Context) (map[string]bool, error)
	// ManagedSession reports whether a live session is a Sidecar-managed shell
	// rather than something else that happens to hold the name.
	ManagedSession func(context.Context, string) bool
	// ServerID returns the running tmux server as a "pid=N" id, empty when none.
	ServerID func() string
	// DirExists answers whether a recorded working directory still exists.
	DirExists func(string) bool
	// ProviderAvailable answers whether a provider binary is installed.
	ProviderAvailable func(string) bool
}

func (c Collector) withDefaults() Collector {
	if c.LiveSessions == nil {
		c.LiveSessions = tmuxSessionNames
	}
	if c.ManagedSession == nil {
		c.ManagedSession = tmuxSessionIsManaged
	}
	if c.ServerID == nil {
		c.ServerID = liveServerID
	}
	if c.DirExists == nil {
		c.DirExists = dirExists
	}
	if c.ProviderAvailable == nil {
		c.ProviderAvailable = providerInstalled
	}
	return c
}

// Collect builds the planner input.
//
// A tmux listing that fails is reported as an error rather than as "nothing is
// live", because an empty inventory is what makes every shell look restorable,
// and acting on a listing that did not happen is how a restore would recreate
// shells that are running right now.
func (c Collector) Collect(ctx context.Context, cfg Config, req Request) (Input, error) {
	c = c.withDefaults()

	shells, err := c.candidates()
	if err != nil {
		return Input{}, err
	}
	liveNames, err := c.LiveSessions(ctx)
	if err != nil {
		return Input{}, err
	}

	live := map[string]LiveState{}
	for _, sh := range shells {
		name := sh.Def.TmuxName
		if !liveNames[name] {
			live[name] = LiveAbsent
			continue
		}
		if c.ManagedSession(ctx, name) {
			live[name] = LiveManaged
		} else {
			live[name] = LiveForeign
		}
	}

	return Input{
		Config:            cfg,
		CurrentServer:     c.ServerID(),
		Live:              live,
		Shells:            shells,
		DirExists:         c.DirExists,
		ProviderAvailable: c.ProviderAvailable,
		Request:           req,
	}, nil
}

// ManagedSessionOrDefault answers whether a live session is a Sidecar-managed
// shell, using the collector's override when one is set and the real tmux
// question otherwise. The executor's rechecks go through it so that a test can
// drive the collision refusal without a tmux server.
func (c Collector) ManagedSessionOrDefault(ctx context.Context, session string) bool {
	return c.withDefaults().ManagedSession(ctx, session)
}

// ServerIDOrDefault returns the running tmux server id, empty when none.
func (c Collector) ServerIDOrDefault() string { return c.withDefaults().ServerID() }

// candidates reads every registered project's manifest.
//
// It reads the files directly rather than through a project-registry type so
// that the same code serves the CLI and the app; a manifest that cannot be read
// is skipped rather than failing the whole collection, because one unreadable
// project must not hide every other project's restorable shells.
func (c Collector) candidates() ([]Shell, error) {
	root := filepath.Join(c.StateDir, "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var out []Shell
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projectDir := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(projectDir, "shells.json")
		defs, err := shellstate.ListAtPath(manifestPath)
		if err != nil {
			continue
		}
		projectRoot := projectRootOf(projectDir)
		for _, def := range defs {
			if c.Namespace != "" && def.Namespace != "" && def.Namespace != c.Namespace {
				// A record from another tmux server is not this restore's
				// business, and its absence here says nothing about it.
				continue
			}
			out = append(out, Shell{
				Project:      entry.Name(),
				ProjectRoot:  projectRoot,
				ManifestPath: manifestPath,
				Def:          def,
			})
		}
	}
	return out, nil
}

// projectRootOf reads a project's working tree from its state directory.
func projectRootOf(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, "meta.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Path
}

// tmuxSessionNames lists live session names, treating "no server" as an empty
// inventory rather than an error: no server running is a real, expected answer
// after a reboot, and it is precisely the state a cold restore serves.
func tmuxSessionNames(ctx context.Context) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		if noTmuxServer(err, out) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	names := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names[name] = true
		}
	}
	return names, nil
}

func noTmuxServer(err error, out []byte) bool {
	message := strings.ToLower(string(out))
	// tmux writes its real complaint to stderr, which is not in err.Error().
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message += " " + strings.ToLower(string(exitErr.Stderr))
	}
	return strings.Contains(message, "no server running") ||
		strings.Contains(message, "no sessions") ||
		strings.Contains(message, "error connecting to")
}

// tmuxSessionIsManaged asks the session itself whether Sidecar created it.
//
// The answer decides between converging on a session a previous restore made and
// refusing a name something else is holding, and the two must not be guessed at
// from the name: the refusal exists to stop Sidecar taking a live process's
// name, so it has to be based on evidence from the process's own environment
// rather than on a naming convention anyone can imitate.
func tmuxSessionIsManaged(ctx context.Context, session string) bool {
	out, err := exec.CommandContext(ctx, "tmux", "show-environment", "-t", session, shellstate.SessionEnv).Output()
	if err != nil {
		return false
	}
	value := strings.TrimSpace(string(out))
	return value == shellstate.SessionEnv+"="+session
}

func liveServerID() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pid}").Output()
	if err != nil {
		return ""
	}
	pid, ok := tmuxserver.ParsePID(strings.TrimSpace(string(out)))
	if !ok {
		return ""
	}
	return tmuxserver.Present(0, 0, pid).ServerID()
}

func dirExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func providerInstalled(kind string) bool {
	family, ok := agentcatalog.Lookup(kind)
	if !ok || strings.TrimSpace(family.Command) == "" {
		return false
	}
	_, err := exec.LookPath(family.Command)
	return err == nil
}

// ErrNoBinding reports that a shell has no exact session reference to resume.
var ErrNoBinding = errors.New("no session reference is bound to this shell")

// ResumePlanFor re-reads a step's exact session binding and builds the resume.
//
// It goes through shellstate rather than through the plan's own copy on purpose:
// this is the binding recheck the executor calls at the moment of resuming, and
// reading it from the manifest is what makes it a recheck rather than a
// restatement of what was already believed. An integration can have rotated or
// cleared the reference in between.
func ResumePlanFor(step Step, namespace string) (agentsession.ResumePlan, error) {
	ref, kind, bound, err := shellstate.SessionRefAtPath(step.ManifestPath(), shellstate.Identity{
		TmuxName:  step.Session,
		Namespace: namespace,
	})
	if err != nil {
		return agentsession.ResumePlan{}, err
	}
	if !bound {
		return agentsession.ResumePlan{}, ErrNoBinding
	}
	return agentsession.PlanResume(kind, ref)
}
