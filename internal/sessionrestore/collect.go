package sessionrestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/workspaceops"
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
	// LayoutStateDir contains state.json. Setting it lets a headless caller
	// inspect saved layouts without initializing the UI state singleton.
	LayoutStateDir string
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
	ServerStatus      func(context.Context) (tmuxserver.Status, error)
	CandidateFinder   interface {
		Find(agentsession.CandidateQuery) (agentsession.Candidate, bool, error)
	}
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
	if c.ServerStatus == nil {
		c.ServerStatus = tmuxserver.Inspect
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
	serverStatus, err := c.ServerStatus(ctx)
	if err != nil {
		return Input{}, err
	}
	liveNames := map[string]bool{}
	if serverStatus.State != tmuxserver.StateExitPending {
		liveNames, err = c.LiveSessions(ctx)
	}
	if err != nil {
		return Input{}, err
	}

	live := map[string]LiveState{}
	currentServer := c.ServerID()
	now := time.Now().UTC()
	for i := range shells {
		sh := &shells[i]
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
	// Candidate discovery is only meaningful for shells positively absent from
	// a dead or replaced server. A status read derives the loss instant in
	// memory; an executing restore persists the same transition once.
	if serverStatus.State != tmuxserver.StateExitPending {
		for i := range shells {
			def := &shells[i].Def
			if live[def.TmuxName] != LiveAbsent || def.Restore == nil || !def.Restore.Eligible {
				continue
			}
			lost := currentServer == "" || (def.Restore.LastSeenServer != "" && def.Restore.LastSeenServer != currentServer)
			if !lost {
				continue
			}
			if def.Restore.ServerLostAt.IsZero() {
				updated, _ := shellstate.RecordServerLoss(*def, now, def.Restore.LastAgentActivity)
				*def = updated
				if req.RecordCandidates {
					state := shellstate.ServerGone()
					if currentServer != "" {
						state = shellstate.ServerRunning(currentServer)
					}
					_, _ = shellstate.ForgetOrPreserveAtPath(shells[i].ManifestPath, shellstate.Identity{TmuxName: def.TmuxName, Namespace: def.Namespace}, now, state, def.Restore.LastAgentActivity)
				}
			}
		}
		if err := c.discoverCandidates(shells, req.RecordCandidates); err != nil {
			return Input{}, err
		}
	}

	return Input{
		Config:            cfg,
		CurrentServer:     currentServer,
		Live:              live,
		Shells:            shells,
		DirExists:         c.DirExists,
		ProviderAvailable: c.ProviderAvailable,
		Request:           req,
		ServerStatus:      &serverStatus,
	}, nil
}

func (c Collector) discoverCandidates(shells []Shell, persist bool) error {
	finder := c.CandidateFinder
	if finder == nil {
		finder = agentsession.NewCandidateFinder()
	}
	claimed := make(map[string][]agentsession.Ref)
	for _, sh := range shells {
		if sh.Def.Agent != nil && sh.Def.Agent.Session != nil && !sh.Def.Agent.Session.Empty() {
			kind, _ := shellstate.AgentKindOf(sh.Def)
			claimed[kind] = append(claimed[kind], *sh.Def.Agent.Session)
		}
	}
	for i := range shells {
		def := &shells[i].Def
		if persist && shells[i].Synthesized {
			if err := shellstate.AddAtPath(shells[i].ManifestPath, *def); err != nil {
				return err
			}
			shells[i].Synthesized = false
		}
		if (def.Agent != nil && def.Agent.Session != nil && !def.Agent.Session.Empty()) || def.Restore == nil {
			continue
		}
		kind, conflict := shellstate.AgentKindOf(*def)
		if conflict != "" {
			continue
		}
		var candidate agentsession.Candidate
		var ok bool
		var err error
		if kind == "" && (strings.HasPrefix(def.TmuxName, workspaceops.WorktreeSessionPrefix) || strings.HasPrefix(def.TmuxName, "sidecar-tp-")) {
			matches := 0
			inferenceReadable := true
			for _, possible := range []string{"claude", "grok", "antigravity", "opencode"} {
				found, foundOK, findErr := finder.Find(agentsession.CandidateQuery{WorkDir: def.WorkDir, AgentKind: possible, CreatedAt: def.CreatedAt, ServerLostAt: def.Restore.ServerLostAt, Claimed: claimed[possible]})
				if findErr != nil {
					inferenceReadable = false
					continue
				}
				if foundOK {
					matches++
					kind, candidate, ok = possible, found, true
				}
			}
			if !inferenceReadable || matches != 1 {
				continue
			}
			candidate.Reason += "; provider inferred because exactly one supported store matched this recovered pane"
			candidate.AgentKind = kind
			def.AgentType = kind
			def.Agent = &shellstate.AgentBinding{Kind: kind}
		} else if kind != "" {
			candidate, ok, err = finder.Find(agentsession.CandidateQuery{WorkDir: def.WorkDir, AgentKind: kind, CreatedAt: def.CreatedAt, ServerLostAt: def.Restore.ServerLostAt, Claimed: claimed[kind]})
		} else {
			continue
		}
		if err != nil {
			// A provider-store read failure cannot authorize a stale persisted
			// suggestion. Suppress it for this plan, but keep it on disk so a
			// transient error does not destroy the last useful evidence.
			if def.Agent != nil && def.Agent.Candidate != nil {
				clone := *def.Agent
				clone.Candidate = nil
				def.Agent = &clone
			}
			continue
		}
		if !ok {
			if def.Agent != nil && def.Agent.Candidate != nil {
				clone := *def.Agent
				clone.Candidate = nil
				def.Agent = &clone
				if persist {
					if _, err := shellstate.RecordCandidateAtPath(shells[i].ManifestPath, shellstate.Identity{TmuxName: def.TmuxName, Namespace: def.Namespace}, nil); err != nil {
						return err
					}
				}
			}
			continue
		}
		clone := shellstate.AgentBinding{Kind: kind}
		if def.Agent != nil {
			clone = *def.Agent
		}
		clone.Candidate = &candidate
		def.Agent = &clone
		if !candidate.Picker && !candidate.Ref.Empty() {
			claimed[kind] = append(claimed[kind], candidate.Ref)
		}
		if persist {
			if _, err := shellstate.RecordCandidateAtPath(shells[i].ManifestPath, shellstate.Identity{TmuxName: def.TmuxName, Namespace: def.Namespace}, &candidate); err != nil {
				return err
			}
		}
	}
	return nil
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
		if !os.IsNotExist(err) {
			return nil, err
		}
		entries = nil
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
	supplemental, err := c.SupplementalCandidates(out)
	if err != nil {
		return nil, err
	}
	out = append(out, supplemental...)
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
	// The manifest path is unexported and so does not survive a JSON round trip.
	// A plan that has been serialized and read back is a fine thing to display
	// but is not a thing to execute, and saying so plainly is better than
	// letting an empty path turn into a confusing read error — or, worse, into
	// a silent decision not to resume.
	if strings.TrimSpace(step.ManifestPath()) == "" {
		return agentsession.ResumePlan{}, errors.New(
			"this step has no manifest path, so it was not built by this process; rebuild the plan before executing it")
	}
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

// PrefillPlanFor re-reads either the official binding or the discovered
// candidate. Candidate construction is accepted only on this no-Enter path.
func PrefillPlanFor(step Step, namespace string) (agentsession.ResumePlan, error) {
	if strings.TrimSpace(step.ManifestPath()) == "" {
		return agentsession.ResumePlan{}, errors.New("this step has no manifest path; rebuild the plan before executing it")
	}
	defs, err := shellstate.ListAtPath(step.ManifestPath())
	if err != nil {
		return agentsession.ResumePlan{}, err
	}
	for _, def := range defs {
		if def.TmuxName != step.Session || (namespace != "" && def.Namespace != "" && def.Namespace != namespace) {
			continue
		}
		kind, conflict := shellstate.AgentKindOf(def)
		if conflict != "" {
			return agentsession.ResumePlan{}, fmt.Errorf("shell record names both %s and %s", kind, conflict)
		}
		if def.Agent != nil && def.Agent.Session != nil && !def.Agent.Session.Empty() {
			return agentsession.PlanResume(kind, *def.Agent.Session)
		}
		if def.Agent != nil && def.Agent.Candidate != nil {
			if kind == "" {
				kind = def.Agent.Candidate.AgentKind
			}
			if kind == "" {
				return agentsession.ResumePlan{}, ErrNoBinding
			}
			return agentsession.PlanCandidatePrefill(kind, *def.Agent.Candidate)
		}
		return agentsession.ResumePlan{}, ErrNoBinding
	}
	return agentsession.ResumePlan{}, ErrNoBinding
}
