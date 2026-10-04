package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentremote"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/uiapi"
	"github.com/marcus/sidecar/internal/workspacewire"
)

// The existing CLI adapters bind explicit targets to workspaceops/agentcontrol.
// HTTP uses them unchanged: no second mutation sequence or response formatter.
func (b *mobileBackend) Projects(ctx context.Context, host string) (workspacewire.Projects, error) {
	if host != "" && host != "local" {
		var result workspacewire.Projects
		err := b.workspaceRemote(ctx, host, []string{"project", "list", "--json"}, &result)
		// Shell/Visible describe the invoking CLI only, never the API process.
		result.Shell = nil
		result.Visible = nil
		result.Aligned = false
		return result, err
	}
	cfg, err := config.Load()
	if err != nil {
		return workspacewire.Projects{}, err
	}
	result := workspacewire.Projects{Projects: make([]workspacewire.Project, 0, len(cfg.Projects.List))}
	for _, p := range cfg.Projects.List {
		result.Projects = append(result.Projects, *makeProjectJSONItem(b.env.StateDir, p))
	}
	return result, nil
}
func (b *mobileBackend) Workspace(ctx context.Context, project, host string, q mobileproto.CatalogQuery) (workspacewire.Workspace, error) {
	if host != "" && host != "local" {
		args := []string{"workspace", "list", "--project", project, "--json"}
		args = appendCatalogArgs(args, q)
		var result workspacewire.Workspace
		err := b.workspaceRemote(ctx, host, args, &result)
		return result, err
	}
	projects, err := loadRegisteredProjects(b.env.StateDir)
	if err != nil {
		return workspacewire.Workspace{}, err
	}
	proj, err := matchProject(b.env.StateDir, projects, project, resolveProjectOnly)
	if err != nil {
		return workspacewire.Workspace{}, &uiapi.OperationError{Code: "project", Message: err.Error(), ExitCode: 3}
	}
	configured, err := b.Projects(ctx, "")
	if err != nil {
		return workspacewire.Workspace{}, err
	}
	var item workspacewire.Project
	for _, p := range configured.Projects {
		if p.Key == proj.Key {
			item = p
			break
		}
	}
	if item.Key == "" {
		return workspacewire.Workspace{}, &uiapi.OperationError{Code: "not_found", Message: "This project is no longer configured; refresh the projects list.", ExitCode: 3}
	}
	provider := mobileCatalogProviderForProjects(b.env, func() ([]hostserve.Project, error) {
		return []hostserve.Project{{Name: item.Name, Path: proj.Path}}, nil
	})
	snapshot, err := queryLocalMobileCatalogFrom(ctx, b.env, q, provider)
	if err != nil {
		return workspacewire.Workspace{}, err
	}
	snapshot.Total = 0
	sections := make([]mobileproto.CatalogSection, 0)
	for _, section := range snapshot.Sections {
		rows := make([]mobileproto.CatalogRow, 0)
		for _, row := range section.Rows {
			if row.ProjectID == canonicalMobileSourcePath(proj.Path) {
				rows = append(rows, row)
			}
		}
		if len(rows) > 0 {
			section.Rows = rows
			sections = append(sections, section)
			snapshot.Total += len(rows)
		}
	}
	snapshot.Sections = sections
	failures := make([]mobileproto.CatalogFailure, 0)
	for _, f := range snapshot.Failures {
		if f.ID == canonicalMobileSourcePath(proj.Path) {
			failures = append(failures, f)
		}
	}
	snapshot.Failures = failures
	localEnv := b.env
	localEnv.Ctx = ctx
	records, exit, err := workspaceInvoke(localEnv, runShellList, []string{"--project", proj.Key, "--json"})
	if err != nil {
		return workspacewire.Workspace{}, workspaceInvocationError(exit, err)
	}
	var shells workspacewire.ShellList
	if err := json.Unmarshal(records, &shells); err != nil {
		return workspacewire.Workspace{}, err
	}
	result := workspacewire.Workspace{Project: item, Catalog: snapshot, Shells: shells.Shells}
	// Workspace generation includes tombstones, unlike the terminal catalog.
	stable := result
	stable.Catalog.Generation = ""
	stable.Catalog.ObservedAt = ""
	stable.Catalog.Sections = append([]mobileproto.CatalogSection(nil), stable.Catalog.Sections...)
	for i := range stable.Catalog.Sections {
		stable.Catalog.Sections[i].Rows = append([]mobileproto.CatalogRow(nil), stable.Catalog.Sections[i].Rows...)
		for j := range stable.Catalog.Sections[i].Rows {
			stable.Catalog.Sections[i].Rows[j].ObservedAt = ""
		}
	}
	data, _ := json.Marshal(stable)
	result.Catalog.Generation = fmt.Sprintf("%x", sha256.Sum256(data))
	return result, nil
}
func appendCatalogArgs(args []string, q mobileproto.CatalogQuery) []string {
	if q.Sort != "" {
		args = append(args, "--sort", q.Sort)
	}
	if q.Search != "" {
		args = append(args, "--search", q.Search)
	}
	for _, v := range q.Providers {
		args = append(args, "--provider", v)
	}
	for _, v := range q.States {
		args = append(args, "--state", v)
	}
	if q.ShowIdleSessions != nil {
		args = append(args, "--show-idle-sessions", strconv.FormatBool(*q.ShowIdleSessions))
	}
	return args
}
func (b *mobileBackend) workspaceRemote(ctx context.Context, host string, args []string, out any) error {
	// Keep the already-connected owner transport when available.
	if b.registry != nil {
		return b.registry.RunSidecar(ctx, host, args, out)
	}
	runner, err := newRemoteRunner(b.env, host)
	if err != nil {
		return err
	}
	return runner(ctx, host, args, out)
}
func (b *mobileBackend) WorkspaceOperation(ctx context.Context, project string, c uiapi.WorkspaceCommand) (json.RawMessage, int, error) {
	args, handler, err := workspaceCommandArgs(project, c)
	if err != nil {
		return nil, 2, &uiapi.OperationError{Code: "invalid_request", Message: err.Error(), ExitCode: 2}
	}
	if c.Host != "" && c.Host != "local" {
		if c.Operation == "agents/prompt" {
			client := agentremote.Client{HostID: c.Host, Project: project, ExactTargets: true, Run: func(ctx context.Context, host string, args []string, out any) error {
				wire := workspaceRemoteResult{Operation: "agents/prompt"}
				if err := b.workspaceRemote(ctx, host, args, &wire); err != nil {
					return err
				}
				if !wire.ValidRemoteResult() {
					return fmt.Errorf("owner returned no valid prompt receipt")
				}
				return json.Unmarshal(wire.Data, out)
			}}
			timeout, _ := time.ParseDuration(c.Timeout)
			result, err := client.Prompt(ctx, c.Target, c.Text, c.Wait, nil, timeout)
			if err != nil {
				var ae *agentcontrol.Error
				if agentcontrol.AsError(err, &ae) {
					exit := agentErrorExitCode(ae.Code)
					return agentcontrol.MarshalError(err), exit, &uiapi.OperationError{Code: string(ae.Code), Message: ae.Message, ExitCode: exit}
				}
				return nil, 1, workspaceInvocationError(1, err)
			}
			data, err := json.Marshal(result)
			return data, 0, err
		}
		result := workspaceRemoteResult{Operation: c.Operation}
		if err := b.workspaceRemote(ctx, c.Host, args, &result); err != nil {
			var remote *hosts.RunError
			if errors.As(err, &remote) {
				exit := remote.ExitCode
				var receipt json.RawMessage
				if result.ValidRemoteResult() {
					receipt = result.Data
				}
				if len(receipt) == 0 && json.Valid([]byte(remote.Stderr)) {
					receipt = []byte(remote.Stderr)
				}
				invocationErr := error(remote)
				if strings.TrimSpace(remote.Stderr) != "" {
					invocationErr = errors.New(remote.Stderr)
				}
				return receipt, exit, workspaceInvocationError(exit, invocationErr)
			}
			var ae *agentcontrol.Error
			if agentcontrol.AsError(err, &ae) {
				exit := agentErrorExitCode(ae.Code)
				return agentcontrol.MarshalError(err), exit, &uiapi.OperationError{Code: string(ae.Code), Message: ae.Message, ExitCode: exit}
			}
			return nil, 1, workspaceInvocationError(1, err)
		}
		return result.Data, 0, nil
	}
	env := b.env
	env.Ctx = ctx
	result, exit, err := workspaceInvoke(env, handler, args[2:])
	return result, exit, workspaceInvocationError(exit, err)
}
func workspaceCommandArgs(project string, c uiapi.WorkspaceCommand) ([]string, func(Env, []string) int, error) {
	var args []string
	var run func(Env, []string) int
	switch c.Operation {
	case "shells/create":
		args = []string{"create", "shell", "--tab", "--wait", "0"}
		run = runCreateShell
	case "shells/rename", "worktrees/rename":
		args = []string{"shell", "rename", "--exact-target", "--target", c.Target}
		run = runShellRename
	case "shells/delete":
		args = []string{"shell", "delete", "--exact-target", "--target", c.Target}
		run = runShellDelete
	case "shells/restore":
		args = []string{"shell", "restore"}
		run = runShellRestore
	case "worktrees/plan", "worktrees/create":
		args = []string{"create", "worktree", "--wait", "0"}
		run = runCreateWorktree
	case "worktrees/delete-plan", "worktrees/delete":
		args = []string{"worktree", "delete"}
		run = runWorktreeDelete
	case "agents/start":
		args = []string{"agent", "start", "--kind", c.Kind, "--target", c.Target}
		run = runAgentStart
	case "agents/prompt":
		args = []string{"agent", "prompt", "--exact-target"}
		run = runAgentPrompt
	default:
		return nil, nil, fmt.Errorf("unknown workspace operation %q", c.Operation)
	}
	args = append(args, "--project", project, "--json")
	switch c.Operation {
	case "shells/create":
		if c.Name != "" {
			args = append(args, "--name", c.Name)
		}
	case "shells/rename", "worktrees/rename":
		args = append(args, "--", c.Name)
	case "shells/restore":
		args = append(args, "--", c.Target)
	case "worktrees/plan", "worktrees/create":
		if c.Base != "" {
			args = append(args, "--base", c.Base)
		}
		if c.Operation == "worktrees/plan" {
			args = append(args, "--plan")
		} else {
			args = append(args, "--expect-source-oid", c.ExpectSourceOID)
		}
		args = append(args, "--", c.Name)
	case "worktrees/delete-plan", "worktrees/delete":
		if c.DeleteLocalBranch {
			args = append(args, "--delete-local-branch")
		}
		if c.DeleteRemoteBranch {
			args = append(args, "--delete-remote-branch")
		}
		if c.Operation == "worktrees/delete-plan" {
			args = append(args, "--plan")
		} else {
			args = append(args, "--yes", "--expect-head-oid", c.ExpectHeadOID, "--expect-branch", c.ExpectBranch, "--expect-delete-state", c.ExpectDeleteState)
		}
		args = append(args, "--", c.Target)
	case "agents/start":
		if len(c.Args) > 0 {
			args = append(args, "--")
			args = append(args, c.Args...)
		}
	case "agents/prompt":
		if c.Wait {
			args = append(args, "--wait")
		}
		if c.Timeout != "" {
			args = append(args, "--timeout", c.Timeout)
		}
		args = append(args, "--", c.Target, c.Text)
	}
	return args, run, nil
}
func workspaceInvoke(env Env, run func(Env, []string) int, args []string) (json.RawMessage, int, error) {
	var out, errOut bytes.Buffer
	env.Stdout = &out
	env.Stderr = &errOut
	env.Stdin = strings.NewReader("")
	exit := run(env, args)
	data := bytes.TrimSpace(out.Bytes())
	if len(data) > 0 && !json.Valid(data) {
		return nil, 1, fmt.Errorf("command returned an invalid JSON result")
	}
	if exit != 0 {
		if len(data) == 0 && json.Valid(bytes.TrimSpace(errOut.Bytes())) {
			data = bytes.TrimSpace(errOut.Bytes())
		}
		return data, exit, errors.New(strings.TrimSpace(errOut.String()))
	}
	if len(data) == 0 {
		return nil, 1, fmt.Errorf("command returned no JSON result")
	}
	return data, exit, nil
}
func workspaceInvocationError(exit int, err error) error {
	if err == nil {
		return nil
	}
	var body uiapi.ErrorBody
	if json.Unmarshal([]byte(err.Error()), &body) == nil && body.Error.Code != "" {
		return &uiapi.OperationError{Code: body.Error.Code, Message: body.Error.Message, ExitCode: exit}
	}
	code := map[int]string{1: "backend", 2: "invalid_request", 3: "not_found", 4: "refused", 5: "rejected"}[exit]
	if code == "" {
		code = "backend"
	}
	return &uiapi.OperationError{Code: code, Message: err.Error(), ExitCode: exit}
}

// Validate the actual remote response, never accept login-profile JSON as success.
type workspaceRemoteResult struct {
	Operation string
	Data      json.RawMessage
}

func (r *workspaceRemoteResult) UnmarshalJSON(data []byte) error {
	r.Data = append(r.Data[:0], data...)
	return nil
}
func (r workspaceRemoteResult) ValidRemoteResult() bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(r.Data, &fields) != nil {
		return false
	}
	required := map[string][]string{"shells/create": {"shell", "placement"}, "shells/rename": {"shell", "name", "changed"}, "worktrees/rename": {"shell", "name", "changed"}, "shells/delete": {"shell", "status", "deleted"}, "shells/restore": {"shell", "status"}, "worktrees/plan": {"sourceOid", "branch", "path"}, "worktrees/create": {"path", "branch", "setup"}, "worktrees/delete-plan": {"status", "plan"}, "worktrees/delete": {"status", "deleted", "plan"}, "agents/start": {"target", "agent"}, "agents/prompt": {"target", "receipt"}}[r.Operation]
	if len(required) == 0 {
		return false
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return true
}

func (b *mobileBackend) WorkspaceInvalidation(ctx context.Context) (workspacewire.WorkspaceEvent, error) {
	projects, err := b.Projects(ctx, "")
	if err != nil {
		return workspacewire.WorkspaceEvent{}, err
	}
	event := workspacewire.WorkspaceEvent{Projects: projects, Workspaces: []workspacewire.WorkspaceRef{}}
	for _, p := range projects.Projects {
		event.Workspaces = append(event.Workspaces, workspacewire.WorkspaceRef{Project: p.Key})
	}
	if b.registry != nil {
		for _, client := range b.registry.Clients() {
			if snapshot, ok := client.Snapshot(); ok {
				for _, p := range snapshot.Projects {
					event.Workspaces = append(event.Workspaces, workspacewire.WorkspaceRef{Project: p.Key, Host: client.Host().ID})
				}
			}
		}
	}
	return event, nil
}
