package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/activitystore"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const maxMobileCatalogProjects = 128

func mobileCommand() *Command {
	status := &Command{
		Name: "status", Summary: "Report the local mobile terminal protocol", Usage: "sidecar mobile status --json",
		Flags:     []Flag{{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}},
		ExitCodes: []ExitCode{{Code: 0, Summary: "success"}, {Code: 2, Summary: "usage error"}}, Run: runMobileStatus,
		Examples: []Example{{Command: "sidecar mobile status --json"}},
	}
	serve := &Command{
		Name: "serve", Summary: "Serve one bounded mobile terminal protocol stream", Usage: "sidecar mobile serve --stdio [--owner-only]",
		Long:      "Read versioned JSONL requests from stdin and write JSONL responses and terminal frames to stdout. With remote hosts enabled, the hub routes each selected terminal to its owning Sidecar. --owner-only is the internal registered-host boundary: it serves only this machine and prevents recursive hubs. Candidate selectors require their paired expected_target identity. Multi-pane candidates resize and verify the exact selected pane; layouts that cannot accept the requested pane geometry are refused.",
		Flags:     []Flag{{Name: "--stdio", Summary: "Use stdin and stdout for the protocol", Bool: true}, {Name: "--owner-only", Summary: "Serve only terminals owned by this machine", Bool: true}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}},
		ExitCodes: []ExitCode{{Code: 0, Summary: "stream closed normally"}, {Code: 1, Summary: "service failed"}, {Code: 2, Summary: "usage error"}},
		Examples:  []Example{{Command: "sidecar mobile serve --stdio"}},
		Mutates:   true, Run: runMobileServe,
	}
	sessions := &Command{
		Name: "sessions", Summary: "Query the mobile Sessions catalog", Usage: "sidecar mobile sessions --json [--sort MODE] [--search TEXT] [--host ID] [--provider NAME] [--state STATE]",
		Long: "Collect the same workspace inventory from this hub and each available registered owner, then apply the same Activity, Project, Recent, or Name ordering as the Sessions browser. A newly started one-shot query waits briefly for initial remote health and returns connecting or unavailable hosts as explicit partial failures. Filters are repeatable and server-applied. Worktrees expose zero, one, or several exact server-owned terminal candidates; several choices keep the parent row ambiguous until the client echoes one candidate selector with its expected identity.",
		Flags: []Flag{{Name: "--json", Summary: "Write one structured catalog snapshot to stdout", Bool: true},
			{Name: "--sort", Arg: "MODE", Summary: "activity, project, recent, or name"}, {Name: "--search", Arg: "TEXT", Summary: "Match Sessions fields"},
			{Name: "--host", Arg: "ID", Summary: "Include one owning host (repeatable)"}, {Name: "--provider", Arg: "NAME", Summary: "Include one provider (repeatable)"},
			{Name: "--state", Arg: "STATE", Summary: "Include one status, group, or attachment state (repeatable)"}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}},
		ExitCodes: []ExitCode{{Code: 0, Summary: "success"}, {Code: 1, Summary: "catalog unavailable"}, {Code: 2, Summary: "usage error"}},
		Examples:  []Example{{Command: "sidecar mobile sessions --json --sort activity"}, {Command: "sidecar mobile sessions --json --search sidecar --state working"}},
		Agent:     AgentDoc{Invocation: "sidecar mobile sessions --json", Summary: "Query the ordered Sessions catalog and attachment readiness"}, Run: runMobileSessions,
	}
	return &Command{Name: "mobile", Summary: "Serve the native mobile terminal client", Usage: "sidecar mobile <command>", Sub: []*Command{serve, sessions, status}, Run: runMobileRoot}
}

func runMobileRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("mobile")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub == nil || sub.Run == nil {
		cliErrf(env.Stderr, "unknown mobile command %q\n\n%s", args[0], RenderHelp(cmd))
		return 2
	}
	return sub.Run(env, args[1:])
}

func runMobileStatus(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("mobile").FindSubcommand("status")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	if len(args) != 1 || args[0] != "--json" {
		cliErrf(env.Stderr, "--json is required\n\n%s", RenderHelp(cmd))
		return 2
	}
	host, _ := os.Hostname()
	result := struct {
		Version      int                      `json:"version"`
		HubID        string                   `json:"hub_id"`
		Capabilities mobileproto.Capabilities `json:"capabilities"`
	}{mobileproto.Version, host, mobileproto.DefaultCapabilities()}
	if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}

func runMobileServe(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("mobile").FindSubcommand("serve")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	stdio, ownerOnly := false, false
	for _, arg := range args {
		switch arg {
		case "--stdio":
			stdio = true
		case "--owner-only":
			ownerOnly = true
		default:
			cliErrf(env.Stderr, "unknown mobile serve flag %q\n\n%s", arg, RenderHelp(cmd))
			return 2
		}
	}
	if !stdio {
		cliErrf(env.Stderr, "--stdio is required\n\n%s", RenderHelp(cmd))
		return 2
	}
	// CLI dispatch precedes main's tmux cleanup. A remote shell commonly
	// inherits both variables; allowing them through would address its hosting
	// server instead of the configured Sidecar namespace.
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	var err error
	if ownerOnly {
		err = runMobileOwnerService(env)
	} else {
		err = runMobileHubOrOwner(env)
	}
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}

func runMobileOwnerService(env Env) error {
	service, err := newMobileOwnerService(env, env.Stdin, env.Stdout)
	if err != nil {
		return err
	}
	return service.Run(env.Ctx)
}

func newMobileOwnerService(env Env, input io.Reader, output io.Writer) (*mobile.Service, error) {
	host, _ := os.Hostname()
	return mobile.New(mobile.Config{
		Input: input, Output: output, HubID: host, OwnerHostID: "local:" + host,
		OwnerConfigGeneration: mobileConfigGeneration(), Resolver: mobileResolver(env), Revalidator: mobileTargetRevalidator(env), Catalog: mobileCatalogProvider(env),
		OwnerConfigGenerationProvider: currentMobileConfigGeneration,
	})
}

func runMobileSessions(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("mobile").FindSubcommand("sessions")
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	query, code := parseMobileCatalogArgs(env, args, RenderHelp(cmd))
	if code != 0 {
		return code
	}
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	snapshot, err := queryMobileCatalog(env, query)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if err := json.NewEncoder(env.Stdout).Encode(snapshot); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}

func parseMobileCatalogArgs(env Env, args []string, help string) (mobileproto.CatalogQuery, int) {
	query := mobileproto.CatalogQuery{}
	jsonOutput := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if !hasValue {
			if i+1 >= len(args) {
				cliErrf(env.Stderr, "%s requires a value\n\n%s", name, help)
				return query, 2
			}
			i++
			value = args[i]
		}
		switch name {
		case "--sort":
			query.Sort = value
		case "--search":
			query.Search = value
		case "--host":
			query.Hosts = append(query.Hosts, value)
		case "--provider":
			query.Providers = append(query.Providers, value)
		case "--state":
			query.States = append(query.States, value)
		default:
			cliErrf(env.Stderr, "unknown mobile sessions flag %q\n\n%s", name, help)
			return query, 2
		}
	}
	if !jsonOutput {
		cliErrf(env.Stderr, "--json is required\n\n%s", help)
		return query, 2
	}
	return query, 0
}

func mobileCatalogProvider(env Env) mobile.CatalogProvider {
	seed := activitystore.Load(filepath.Join(env.StateDir, activitystore.FileName), time.Now())
	collector := workspaceinventory.Collector{}.WithDefaults()
	collector = collector.SeedTrackers(seed)
	return newMobileCatalogProvider(env, configuredProjects, collector, seed, time.Now)
}

func newMobileCatalogProvider(env Env, loadProjects func() ([]hostserve.Project, error), collector workspaceinventory.Collector, seed map[string]agentactivity.Tracker, now func() time.Time) mobile.CatalogProvider {
	if now == nil {
		now = time.Now
	}
	knownActivity := make(map[string]struct{}, len(seed))
	for id := range seed {
		knownActivity[id] = struct{}{}
	}
	unknownActivity := make(map[string]activityObservation)
	return func(ctx context.Context) (mobile.CatalogInput, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		projects, err := loadProjects()
		if err != nil {
			return mobile.CatalogInput{}, err
		}
		if len(projects) > maxMobileCatalogProjects {
			return mobile.CatalogInput{}, &mobile.ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds project limit"}
		}
		panes, err := collector.ListPanes(ctx)
		if err != nil {
			return mobile.CatalogInput{}, fmt.Errorf("collect mobile terminal panes: %w", err)
		}
		if len(panes) > mobileproto.MaxCatalogRows {
			return mobile.CatalogInput{}, &mobile.ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds pane limit"}
		}
		roots := make([]string, 0, len(projects))
		results := make([]workspaceinventory.ProjectResult, 0, len(projects))
		for _, project := range projects {
			roots = append(roots, project.Path)
			results = append(results, collector.CollectProjectInventory(ctx, project.Name, project.Path))
		}
		refresh := collector.ForRefresh(4, workspaceinventory.BuildShellClaims(results))
		catalogProjects := make([]mobile.CatalogProject, 0, len(results))
		for index, result := range results {
			result = refresh.RefreshProjectStatus(ctx, result, roots, panes)
			markUnknownActivityAges(result.Workspaces, refresh.TrackerSnapshot(), knownActivity, unknownActivity)
			catalogProjects = append(catalogProjects, mobile.CatalogProject{Result: result, Label: projects[index].Name, Order: index})
		}
		refresh.CommitTrackers()
		observedAt := now()
		host, _ := os.Hostname()
		hostID := "local:" + host
		input := mobile.CatalogInput{ObservedAt: observedAt, Hosts: []mobileproto.CatalogHost{{ID: hostID, Name: host, State: "online", Local: true}}, Projects: catalogProjects}
		input.CandidateResolver = func(ctx context.Context, selector string) (mobile.ResolvedTarget, error) {
			return mobile.ResolveCatalogCandidate(ctx, input, hostID, selector, tty.InspectHeadlessPane)
		}
		return input, nil
	}
}

type activityObservation struct {
	state    agentactivity.State
	evidence string
}

// markUnknownActivityAges keeps a read-only catalog honest when the shared
// desktop activity history has no entry for a live agent. The first capture
// proves the current state, but it cannot prove when that state began. Keeping
// ChangedAt empty until this provider observes a real transition avoids
// inventing a fresh age and content generation on every independent CLI run.
func markUnknownActivityAges(workspaces []workspaceinventory.Workspace, trackers map[string]agentactivity.Tracker, known map[string]struct{}, unknown map[string]activityObservation) {
	for index := range workspaces {
		workspace := &workspaces[index]
		if _, ok := known[workspace.ID]; ok {
			continue
		}
		tracker, ok := trackers[workspace.ID]
		if !ok {
			continue
		}
		current := activityObservation{state: tracker.State, evidence: tracker.Evidence}
		first, seen := unknown[workspace.ID]
		if !seen {
			unknown[workspace.ID] = current
			workspace.Presentation.ChangedAt = time.Time{}
			continue
		}
		if first == current {
			workspace.Presentation.ChangedAt = time.Time{}
			continue
		}
		delete(unknown, workspace.ID)
		known[workspace.ID] = struct{}{}
	}
}

func mobileResolver(env Env) mobile.Resolver {
	return func(ctx context.Context, value string) (mobile.ResolvedTarget, error) {
		value = strings.TrimSpace(value)
		if mobile.IsCandidateSelector(value) {
			input, err := mobileCatalogProvider(env)(ctx)
			if err != nil {
				return mobile.ResolvedTarget{}, err
			}
			host, _ := os.Hostname()
			return mobile.ResolveCatalogCandidate(ctx, input, "local:"+host, value, tty.InspectHeadlessPane)
		}
		target, code, err := findShellTarget(env, value, "", "", true, tmuxenv.Namespace())
		if err != nil {
			kind := mobileproto.ErrorAmbiguous
			if code == shellTargetUnregistered {
				kind = mobileproto.ErrorNotFound
			}
			return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: kind, Message: err.Error()}
		}
		if target.Kind != shellTargetKindShell || target.CreatedAt == "" {
			return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorUnsupported, Message: "mobile M0 requires a managed shell with durable creation identity"}
		}
		identity, err := tty.InspectHeadlessTarget(ctx, target.Session)
		if err != nil {
			return mobile.ResolvedTarget{}, &mobile.ResolveError{Code: mobileproto.ErrorUnsupported, Message: err.Error()}
		}
		return mobile.ResolvedTarget{WorkspaceID: target.Project.Key, WorkspaceKind: target.Kind, ProjectRoot: target.Project.Path, Selector: value, Session: identity.Session,
			Pane: identity.Pane, DisplayName: target.DisplayName, ServerPID: identity.ServerPID, SessionID: identity.SessionID,
			SessionCreated: identity.SessionCreated, DurableSessionCreated: target.CreatedAt, Width: identity.Width, Height: identity.Height, PaneCount: identity.PaneCount}, nil
	}
}

func mobileTargetRevalidator(env Env) mobile.TargetRevalidator {
	resolve := mobileResolver(env)
	source := newMobileCandidateWorkspaceProvider(configuredProjects, workspaceinventory.Collector{}.WithDefaults())
	host, _ := os.Hostname()
	ownerHostID := "local:" + host
	return func(ctx context.Context, target mobile.ResolvedTarget) (mobile.ResolvedTarget, error) {
		if !mobile.IsCandidateSelector(target.Selector) {
			selector := target.Selector
			if selector == "" {
				selector = target.Session
			}
			return resolve(ctx, selector)
		}
		return mobile.RevalidateCatalogCandidate(ctx, target, ownerHostID, source, tty.InspectHeadlessPane)
	}
}

func newMobileCandidateWorkspaceProvider(loadProjects func() ([]hostserve.Project, error), collector workspaceinventory.Collector) mobile.CandidateWorkspaceProvider {
	return func(ctx context.Context, target mobile.ResolvedTarget) (workspaceinventory.Workspace, error) {
		projects, err := loadProjects()
		if err != nil {
			return workspaceinventory.Workspace{}, err
		}
		if len(projects) > maxMobileCatalogProjects {
			return workspaceinventory.Workspace{}, &mobile.ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds project limit"}
		}
		roots := make([]string, 0, len(projects))
		var selected *hostserve.Project
		for index := range projects {
			project := &projects[index]
			roots = append(roots, project.Path)
			if canonicalMobileSourcePath(project.Path) != target.SourceProjectKey {
				continue
			}
			if selected != nil {
				return workspaceinventory.Workspace{}, &mobile.ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "terminal candidate project source is duplicated"}
			}
			selected = project
		}
		if selected == nil {
			return workspaceinventory.Workspace{}, &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate project is no longer configured"}
		}
		result := collector.CollectProjectInventory(ctx, selected.Name, selected.Path)
		if result.Err != nil {
			return workspaceinventory.Workspace{}, &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: result.Err.Error()}
		}
		var found *workspaceinventory.Workspace
		for index := range result.Workspaces {
			workspace := &result.Workspaces[index]
			if workspace.Kind != workspaceinventory.KindWorktree || workspace.ID != target.WorkspaceID || canonicalMobileSourcePath(workspace.Path) != target.SourceWorkspacePath {
				continue
			}
			if found != nil {
				return workspaceinventory.Workspace{}, &mobile.ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "terminal candidate worktree source is duplicated"}
			}
			found = workspace
		}
		if found == nil {
			return workspaceinventory.Workspace{}, &mobile.ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate worktree is no longer registered"}
		}
		panes, err := collector.ListPanes(ctx)
		if err != nil {
			return workspaceinventory.Workspace{}, fmt.Errorf("refresh terminal candidate panes: %w", err)
		}
		refresh := collector.WithShellClaims(workspaceinventory.ConfiguredShellClaims(roots))
		return refresh.RefreshWorktreeTerminalCandidates(*found, roots, panes), nil
	}
}

func canonicalMobileSourcePath(path string) string {
	path = config.ExpandPath(strings.TrimSpace(path))
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		path = evaluated
	}
	return filepath.Clean(path)
}

func mobileConfigGeneration() string {
	generation, err := currentMobileConfigGeneration(context.Background())
	if err == nil {
		return generation
	}
	sum := sha256.Sum256([]byte(config.ConfigPath()))
	return hex.EncodeToString(sum[:16])
}

func currentMobileConfigGeneration(context.Context) (string, error) {
	b, err := os.ReadFile(config.ConfigPath())
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16]), nil
}
