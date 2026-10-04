package uiapi

import (
	"strings"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/workspaceops"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func workspaceSpecTypes(values map[string]any) {
	for name, value := range map[string]any{"Projects": workspacewire.Projects{}, "Workspace": workspacewire.Workspace{}, "WorkspaceEvent": workspacewire.WorkspaceEvent{}, "WorkspaceCommand": WorkspaceCommand{}, "ShellCreated": workspacewire.ShellCreated{}, "ShellRenamed": shellstate.RenameResult{}, "ShellDeleted": workspacewire.ShellDeleted{}, "ShellRestored": workspacewire.ShellRestored{}, "WorktreePlan": workspaceops.WorktreePlan{}, "WorktreeCreated": workspacewire.WorktreeCreated{}, "WorktreeDeleted": workspacewire.WorktreeDeleted{}, "Agent": agentcontrol.Agent{}, "PromptResult": agentcontrol.PromptResult{}, "AgentError": agentcontrol.ErrorEnvelope{}} {
		values[name] = value
	}
}
func workspaceSpec(schemas, paths map[string]any, add func(string, string, string, string, []string, bool)) {
	all := []string{"local", "browser", "tailnet"}
	add("/api/v0/projects", "get", "", "Projects", all, false)
	add("/api/v0/projects/{project}/workspace", "get", "", "Workspace", all, false)
	projectParam := map[string]any{"name": "project", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Configured owning project key or root path, encoded as one path segment; never a hub-scoped catalog project_id."}
	hostParam := map[string]any{"name": "host", "in": "query", "schema": map[string]any{"type": "string"}, "description": "Registered owning host; omit or use local for this machine."}
	paths["/api/v0/projects"].(map[string]any)["get"].(map[string]any)["x-required-scope"] = ScopeWorkspaceWrite
	paths["/api/v0/projects/{project}/workspace"].(map[string]any)["get"].(map[string]any)["x-required-scope"] = ScopeWorkspaceWrite
	paths["/api/v0/projects"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{hostParam}
	query := paths["/api/v0/sessions"].(map[string]any)["get"].(map[string]any)["parameters"].([]any)
	params := []any{projectParam}
	for _, p := range query {
		if p.(map[string]any)["name"] != "host" {
			params = append(params, p)
		}
	}
	params = append(params, hostParam)
	paths["/api/v0/projects/{project}/workspace"].(map[string]any)["get"].(map[string]any)["parameters"] = params
	responses := map[string]string{"shells/create": "ShellCreated", "shells/rename": "ShellRenamed", "shells/delete": "ShellDeleted", "shells/restore": "ShellRestored", "worktrees/plan": "WorktreePlan", "worktrees/create": "WorktreeCreated", "worktrees/rename": "ShellRenamed", "worktrees/delete-plan": "WorktreeDeleted", "worktrees/delete": "WorktreeDeleted", "agents/start": "Agent", "agents/prompt": "PromptResult"}
	command := schemas["WorkspaceCommand"].(map[string]any)
	properties := command["properties"].(map[string]any)
	for op, fields := range workspaceOperationFields {
		name := strings.ReplaceAll(op, "/", "_") + "Request"
		props := map[string]any{"host": properties["host"]}
		for _, field := range fields {
			props[field] = properties[field]
		}
		required := []string{}
		for _, field := range fields {
			if field == "target" || field == "kind" || field == "text" || (field == "name" && op != "shells/create") || field == "confirm" || strings.HasPrefix(field, "expect_") {
				required = append(required, field)
			}
		}
		for _, field := range required {
			prop := map[string]any{}
			for k, v := range props[field].(map[string]any) {
				prop[k] = v
			}
			if field == "confirm" {
				prop["const"] = true
			} else {
				prop["minLength"] = 1
			}
			props[field] = prop
		}
		schemas[name] = map[string]any{"type": "object", "properties": props, "additionalProperties": false, "required": required}
		path := "/api/v0/projects/{project}/" + op
		add(path, "post", name, responses[op], all, false)
		operation := paths[path].(map[string]any)["post"].(map[string]any)
		operation["parameters"] = append(operation["parameters"].([]any), projectParam)
		operation["x-scope"] = ScopeWorkspaceWrite
		operation["x-required-scope"] = ScopeWorkspaceWrite
		refs := []any{map[string]any{"$ref": "#/components/schemas/ErrorBody"}}
		if strings.HasPrefix(op, "agents/") {
			refs = append(refs, map[string]any{"$ref": "#/components/schemas/AgentError"})
		}
		if op == "worktrees/create" || op == "shells/create" {
			refs = append(refs, map[string]any{"$ref": "#/components/schemas/" + responses[op]})
		}
		responses := operation["responses"].(map[string]any)
		responses["default"].(map[string]any)["content"] = map[string]any{"application/json": map[string]any{"schema": map[string]any{"anyOf": refs}}}
		for _, response := range responses {
			response.(map[string]any)["headers"] = map[string]any{"X-Sidecar-Exit-Code": map[string]any{"description": "Original owning CLI exit status; absent for HTTP admission refusals.", "schema": map[string]any{"type": "integer"}}}
		}
		operation["requestBody"].(map[string]any)["required"] = true
		operation["description"] = "Calls the owning CLI's shared workspace operation. X-Sidecar-Exit-Code retains its exit status. A failed create can return its partial creation result; a failed prompt retains the CLI error receipt. Do not replay uncertain writes."
	}
}
