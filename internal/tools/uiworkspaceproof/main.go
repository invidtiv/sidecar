// Command uiworkspaceproof exercises real workspace HTTP operations in the
// caller's isolated API server. It owns no server and never addresses tmux.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uiapi"
	"github.com/marcus/sidecar/internal/workspaceops"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func run() error {
	state := flag.String("state", "", "isolated state root")
	project := flag.String("project", "", "configured project key")
	binary := flag.String("sidecar", "", "isolated binary")
	cfg := flag.String("config", "", "isolated config")
	flag.Parse()
	if *state == "" || *project == "" || *binary == "" || *cfg == "" {
		return errors.New("-state -project -sidecar -config are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := uiapi.NewLocalClient(*state)
	if err != nil {
		return err
	}
	defer client.HTTPClient().CloseIdleConnections()
	request := func(op string, body any, want int, out any) error {
		data, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, "POST", client.URL("/api/v0/projects/"+*project+"/"+op), bytes.NewReader(data))
		if err != nil {
			return err
		}
		response, err := client.HTTPClient().Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		result, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		if response.StatusCode != want {
			return fmt.Errorf("%s status=%d want=%d: %s", op, response.StatusCode, want, result)
		}
		if out != nil {
			if err := json.Unmarshal(result, out); err != nil {
				return err
			}
		}
		if op == "shells/create" && want == 409 && response.Header.Get("X-Sidecar-Exit-Code") != "5" {
			return fmt.Errorf("shell creation lost named refusal exit status: %s", response.Header.Get("X-Sidecar-Exit-Code"))
		}
		if response.Header.Get("X-Sidecar-Exit-Code") == "" && want != 400 {
			return fmt.Errorf("%s lost CLI exit status", op)
		}
		return nil
	}
	var projects workspacewire.Projects
	if err := client.Do(ctx, "GET", "/api/v0/projects", nil, &projects); err != nil {
		return err
	}
	if len(projects.Projects) != 1 || projects.Projects[0].Key != *project {
		return fmt.Errorf("projects: %+v", projects)
	}
	conn, _, err := websocket.Dial(ctx, strings.Replace(client.URL("/api/v0/events"), "http:", "ws:", 1), &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseNow() }()
	waitWorkspace := func() error {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return err
			}
			var m uiapi.EventMessage
			if err := json.Unmarshal(data, &m); err != nil {
				return err
			}
			if m.Type == "workspace" && m.Workspace != nil {
				for _, ref := range m.Workspace.Workspaces {
					if ref.Project == *project {
						return nil
					}
				}
			}
		}
	}
	if err := waitWorkspace(); err != nil {
		return err
	}
	var created workspacewire.ShellCreated
	if err := request("shells/create", map[string]any{"name": "API workspace shell"}, 200, &created); err != nil {
		return err
	}
	if created.Shell.Session == "" || created.Project != *project {
		return fmt.Errorf("created shell: %+v", created)
	}
	for _, tc := range []struct{ name, code string }{
		{"API workspace shell", "shell_name_in_use"},
		{strings.Repeat("x", shellstate.MaxNameBytes+1), "shell_name_invalid"},
	} {
		var refusal uiapi.ErrorBody
		if err := request("shells/create", map[string]any{"name": tc.name}, 409, &refusal); err != nil {
			return err
		}
		if refusal.Error.Code != tc.code || refusal.Error.Message == "" {
			return fmt.Errorf("shell creation lost named refusal: %+v", refusal)
		}
	}
	if err := waitWorkspace(); err != nil {
		return err
	}
	target := created.Shell.Session
	if err := request("shells/rename", map[string]any{"target": target, "name": "API renamed shell"}, 200, nil); err != nil {
		return err
	}
	var workspace workspacewire.Workspace
	if err := client.Do(ctx, "GET", "/api/v0/projects/"+*project+"/workspace?sort=name", nil, &workspace); err != nil {
		return err
	}
	found := false
	mainCheckout := false
	for _, section := range workspace.Catalog.Sections {
		for _, row := range section.Rows {
			if row.MainCheckout != nil && *row.MainCheckout {
				if row.WorkspaceKind != "worktree" || row.Path != workspace.Project.Path {
					return fmt.Errorf("main checkout paths disagree: row=%+v project=%+v", row, workspace.Project)
				}
				mainCheckout = true
			}
			if row.Session == target && row.DisplayName == "API renamed shell" {
				found = true
			}
		}
	}
	if !found {
		return errors.New("renamed shell absent from workspace")
	}
	if !mainCheckout {
		return errors.New("workspace has no explicit main checkout")
	}
	cli, err := exec.CommandContext(ctx, *binary, "-config", *cfg, "workspace", "list", "--project", *project, "--sort", "name", "--json").Output()
	if err != nil {
		return err
	}
	var fromCLI workspacewire.Workspace
	if err := json.Unmarshal(cli, &fromCLI); err != nil {
		return err
	}
	clean := func(w workspacewire.Workspace) []byte {
		w.Catalog.Generation = ""
		w.Catalog.ObservedAt = ""
		for i := range w.Catalog.Sections {
			for j := range w.Catalog.Sections[i].Rows {
				w.Catalog.Sections[i].Rows[j].ObservedAt = ""
			}
		}
		data, _ := json.Marshal(w)
		return data
	}
	if !bytes.Equal(clean(workspace), clean(fromCLI)) {
		return errors.New("CLI and HTTP workspace differ")
	}
	if err := request("agents/prompt", map[string]any{"target": target, "text": "-"}, 409, nil); err != nil {
		return err
	}
	if err := request("agents/start", map[string]any{"target": target, "kind": "codex"}, 409, nil); err != nil {
		return err
	}
	if err := request("shells/delete", map[string]any{"target": target}, 200, nil); err != nil {
		return err
	}
	if err := request("shells/restore", map[string]any{"target": target}, 200, nil); err != nil {
		return err
	}
	// Tombstone restore is record-only; deleting an already-gone tmux session
	// still re-tombstones that record, with no server restart.
	if err := request("shells/delete", map[string]any{"target": target}, 200, nil); err != nil {
		return err
	}
	// The deleted session's name is now another live shell's display name.
	// Neither HTTP nor the actual CLI may reinterpret that stale identity.
	var collision workspacewire.ShellCreated
	if err := request("shells/create", map[string]any{"name": target}, 200, &collision); err != nil {
		return err
	}
	// Ordinary bare display names remain a supported CLI target. Restore the
	// session-shaped display name afterwards for the stale-identity proof.
	if err := request("shells/rename", map[string]any{"target": collision.Shell.Session, "name": "rev U3-c"}, 200, nil); err != nil {
		return err
	}
	args := []string{"-config", *cfg, "shell", "rename", "--target", "rev U3-c", "--project", *project, "--json", "--", target}
	if output, err := exec.CommandContext(ctx, *binary, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("CLI bare display-name rename: %v %s", err, output)
	}
	for _, op := range []string{"shells/rename", "shells/delete"} {
		body := map[string]any{"target": target}
		if op == "shells/rename" {
			body["name"] = "wrong shell"
		}
		if err := request(op, body, 404, nil); err != nil {
			return err
		}
	}
	for _, verb := range []string{"rename", "delete"} {
		args := []string{"-config", *cfg, "shell", verb, "--target", target, "--project", *project, "--json"}
		if verb == "rename" {
			args = append(args, "--", "wrong shell")
		}
		output, err := exec.CommandContext(ctx, *binary, args...).CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 3 {
			return fmt.Errorf("CLI stale target %s: %v %s", verb, err, output)
		}
	}
	var restored workspacewire.ShellRestored
	if err := request("shells/restore", map[string]any{"target": target}, 200, &restored); err != nil {
		return err
	}
	if restored.Shell != target || restored.Name != "API renamed shell" {
		return fmt.Errorf("restore selected display-name collision: %+v", restored)
	}
	if err := client.Do(ctx, "GET", "/api/v0/projects/"+*project+"/workspace", nil, &workspace); err != nil {
		return err
	}
	preserved := false
	for _, shell := range workspace.Shells {
		preserved = preserved || (shell.Shell == collision.Shell.Session && shell.Name == target && shell.Status == "live")
	}
	if !preserved {
		return errors.New("stale target operations changed the live display-name collision")
	}
	for _, session := range []string{target, collision.Shell.Session} {
		if err := request("shells/delete", map[string]any{"target": session}, 200, nil); err != nil {
			return err
		}
	}
	var plan workspaceops.WorktreePlan
	if err := request("worktrees/plan", map[string]any{"name": "API feature"}, 200, &plan); err != nil {
		return err
	}
	if _, err := os.Stat(plan.Path); !os.IsNotExist(err) {
		return errors.New("planning created a worktree")
	}
	if err := request("worktrees/create", map[string]any{"name": "API feature", "confirm": true, "expect_source_oid": strings.Repeat("0", 40)}, 409, nil); err != nil {
		return err
	}
	if _, err := os.Stat(plan.Path); !os.IsNotExist(err) {
		return errors.New("stale source OID created a worktree")
	}
	var wt workspacewire.WorktreeCreated
	if err := request("worktrees/create", map[string]any{"name": "API feature", "confirm": true, "expect_source_oid": plan.SourceOID}, 200, &wt); err != nil {
		return err
	}
	if err := request("worktrees/rename", map[string]any{"target": wt.Shell.Session, "name": "API review branch"}, 200, nil); err != nil {
		return err
	}
	if err := os.WriteFile(wt.Path+"/dirty.txt", []byte("must be explicitly confirmed\n"), 0600); err != nil {
		return err
	}
	var deletion workspacewire.WorktreeDeleted
	if err := request("worktrees/delete-plan", map[string]any{"target": wt.Path}, 200, &deletion); err != nil {
		return err
	}
	if !strings.Contains(deletion.Plan.Dirtiness, "dirty") {
		return fmt.Errorf("dirty probe: %+v", deletion)
	}
	if err := request("worktrees/delete", map[string]any{"target": wt.Path}, 400, nil); err != nil {
		return err
	}
	if _, err := os.Stat(wt.Path); err != nil {
		return errors.New("unconfirmed delete removed worktree")
	}
	if err := request("worktrees/delete", map[string]any{"target": wt.Path, "confirm": true, "expect_delete_state": deletion.Plan.DeleteState, "expect_branch": deletion.Plan.Branch, "expect_head_oid": strings.Repeat("0", 40)}, 409, nil); err != nil {
		return err
	}
	if err := request("worktrees/delete", map[string]any{"target": wt.Path, "confirm": true, "expect_delete_state": deletion.Plan.DeleteState, "expect_branch": deletion.Plan.Branch, "expect_head_oid": deletion.Plan.HeadOID}, 200, nil); err != nil {
		return err
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		return errors.New("confirmed delete retained worktree")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]bool{"workspace_cli_parity": true, "shell_lifecycle": true, "workspace_push": true, "worktree_source_guard": true, "dirty_delete_confirmation": true, "delete_identity_guard": true, "agent_feature_refusals": true, "stale_session_collision_refused": true, "main_checkout_metadata": true, "literal_dash_prompt_validated": true, "bare_display_name_cli_compatible": true})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "uiworkspaceproof:", err)
		os.Exit(1)
	}
}
