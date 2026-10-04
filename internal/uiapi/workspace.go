package uiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspacewire"
)

const ScopeWorkspaceWrite = "workspace:write"

// WorkspaceBackend is the application seam for the workspace HTTP adapter.
// Operations use the CLI's JSON result and exit status, including partial results.
type WorkspaceBackend interface {
	Projects(context.Context, string) (workspacewire.Projects, error)
	Workspace(context.Context, string, string, mobileproto.CatalogQuery) (workspacewire.Workspace, error)
	WorkspaceOperation(context.Context, string, WorkspaceCommand) (json.RawMessage, int, error)
}

// WorkspaceCommand is input to the shared command adapter, never raw CLI argv.
type WorkspaceCommand struct {
	Operation          string   `json:"-"`
	Host               string   `json:"host,omitempty"`
	Target             string   `json:"target,omitempty"`
	Name               string   `json:"name,omitempty"`
	Base               string   `json:"base,omitempty"`
	Confirm            bool     `json:"confirm,omitempty"`
	ExpectSourceOID    string   `json:"expect_source_oid,omitempty"`
	ExpectHeadOID      string   `json:"expect_head_oid,omitempty"`
	ExpectBranch       string   `json:"expect_branch,omitempty"`
	DeleteLocalBranch  bool     `json:"delete_local_branch,omitempty"`
	DeleteRemoteBranch bool     `json:"delete_remote_branch,omitempty"`
	Kind               string   `json:"kind,omitempty"`
	Args               []string `json:"args,omitempty"`
	Text               string   `json:"text,omitempty"`
	Wait               bool     `json:"wait,omitempty"`
	Timeout            string   `json:"timeout,omitempty"`
}

// OperationError retains the CLI refusal and status across HTTP and remote calls.
type OperationError struct {
	Code, Message string
	ExitCode      int
}

func (e *OperationError) Error() string { return e.Message }

var workspaceOperationFields = map[string][]string{
	"shells/create":         {"name"},
	"shells/rename":         {"target", "name"},
	"shells/delete":         {"target"},
	"shells/restore":        {"target"},
	"worktrees/plan":        {"name", "base"},
	"worktrees/create":      {"name", "base", "confirm", "expect_source_oid"},
	"worktrees/rename":      {"target", "name"},
	"worktrees/delete-plan": {"target", "delete_local_branch", "delete_remote_branch"},
	"worktrees/delete":      {"target", "confirm", "expect_head_oid", "expect_branch", "delete_local_branch", "delete_remote_branch"},
	"agents/start":          {"target", "kind", "args"},
	"agents/prompt":         {"target", "text", "wait", "timeout"},
}

func (s *Server) workspaceRoutes(routes map[string]*route) {
	routes["/api/v0/projects"] = &route{methods: map[string]routeFunc{http.MethodGet: s.handleProjects}}
	routes["/api/v0/projects/{project}/workspace"] = &route{methods: map[string]routeFunc{http.MethodGet: s.handleWorkspace}}
	for operation := range workspaceOperationFields {
		op := operation
		routes["/api/v0/projects/{project}/"+op] = &route{methods: map[string]routeFunc{http.MethodPost: func(w http.ResponseWriter, r *http.Request, c caller) { s.handleWorkspaceOperation(w, r, c, op) }}}
	}
}
func workspaceRoute(path string) (string, string) {
	parts := strings.Split(path, "/")
	if len(parts) < 6 || strings.Join(parts[:4], "/") != "/api/v0/projects" || parts[4] == "" {
		return "", ""
	}
	project, err := url.PathUnescape(parts[4])
	if err != nil {
		return "", ""
	}
	return "/api/v0/projects/{project}/" + strings.Join(parts[5:], "/"), project
}
func (s *Server) workspaceBackend(w http.ResponseWriter) WorkspaceBackend {
	b, ok := s.opts.Backend.(WorkspaceBackend)
	if !ok {
		writeError(w, 503, "unsupported", "This backend does not serve workspaces; use a live API server.")
		return nil
	}
	return b
}
func workspaceQuery(w http.ResponseWriter, r *http.Request) (string, mobileproto.CatalogQuery, bool) {
	values := r.URL.Query()
	host := values.Get("host")
	if len(values["host"]) > 1 {
		writeError(w, 400, CodeInvalidRequest, "host takes one owning host.")
		return "", mobileproto.CatalogQuery{}, false
	}
	values.Del("host")
	q, err := ParseCatalogQuery(values)
	if err != nil {
		writeError(w, 400, CodeInvalidRequest, err.Error())
		return "", q, false
	}
	return host, q, true
}
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request, _ caller) {
	if err := projectsQuery(r.URL.Query()); err != nil {
		writeError(w, 400, CodeInvalidRequest, err.Error())
		return
	}
	host := r.URL.Query().Get("host")
	b := s.workspaceBackend(w)
	if b == nil {
		return
	}
	result, err := b.Projects(r.Context(), host)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request, _ caller) {
	host, q, ok := workspaceQuery(w, r)
	if !ok {
		return
	}
	_, project := workspaceRoute(r.URL.EscapedPath())
	b := s.workspaceBackend(w)
	if b == nil {
		return
	}
	result, err := b.Workspace(r.Context(), project, host, q)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) workspaceAuthorized(c caller) bool {
	if !s.callerLive(c) {
		return false
	}
	if !strings.HasPrefix(c.client, "origin:") {
		return true
	}
	record, ok := s.origins.lookupCredential(strings.TrimPrefix(c.client, "origin:"), c.credential)
	if !ok {
		return false
	}
	for _, scope := range record.Scopes {
		if scope == ScopeFull || scope == ScopeWorkspaceWrite {
			return true
		}
	}
	return false
}
func (s *Server) handleWorkspaceOperation(w http.ResponseWriter, r *http.Request, c caller, op string) {
	if !s.workspaceAuthorized(c) {
		writeError(w, 403, "scope_refused", "This credential needs workspace:write; pair it again with that scope or full.")
		return
	}
	var raw map[string]json.RawMessage
	if !decodeBody(w, r, &raw) {
		return
	}
	if raw == nil {
		writeError(w, 400, CodeInvalidRequest, "Send a JSON object for a workspace operation.")
		return
	}
	allowed := map[string]bool{"host": true}
	for _, field := range workspaceOperationFields[op] {
		allowed[field] = true
	}
	for field := range raw {
		if !allowed[field] {
			writeError(w, 400, CodeInvalidRequest, fmt.Sprintf("%s does not accept %s.", op, field))
			return
		}
	}
	data, _ := json.Marshal(raw)
	var command WorkspaceCommand
	if err := json.Unmarshal(data, &command); err != nil {
		writeError(w, 400, CodeInvalidRequest, err.Error())
		return
	}
	command.Operation = op
	if err := validateWorkspaceCommand(command); err != nil {
		writeError(w, 400, CodeInvalidRequest, err.Error())
		return
	}
	b := s.workspaceBackend(w)
	if b == nil {
		return
	}
	_, project := workspaceRoute(r.URL.EscapedPath())
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if !s.workspaceAuthorized(c) {
		writeError(w, 403, "scope_refused", "This credential was revoked; pair it again.")
		return
	}
	result, exit, err := b.WorkspaceOperation(ctx, project, command)
	// Partial failures can have created a shell or worktree too.
	s.catalogEvents.signal()
	w.Header().Set("X-Sidecar-Exit-Code", fmt.Sprint(exit))
	if err != nil {
		// CLI writes creation and prompt receipts even on failure; preserve them.
		if len(result) > 0 {
			writeJSON(w, workspaceStatus(exit), result)
		} else {
			writeWorkspaceError(w, err)
		}
		return
	}
	writeJSON(w, 200, result)
}
func validateWorkspaceCommand(c WorkspaceCommand) error {
	required := func(value, name string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
	if c.Operation != "shells/create" && c.Operation != "worktrees/plan" && c.Operation != "worktrees/create" {
		if err := required(c.Target, "target"); err != nil {
			return err
		}
	}
	switch c.Operation {
	case "shells/rename", "worktrees/rename", "worktrees/plan", "worktrees/create":
		if err := required(c.Name, "name"); err != nil {
			return err
		}
	}
	if c.Operation == "worktrees/create" && (!c.Confirm || c.ExpectSourceOID == "") {
		return fmt.Errorf("confirm and expect_source_oid from the plan are required")
	}
	if c.Operation == "worktrees/delete" && (!c.Confirm || c.ExpectHeadOID == "" || c.ExpectBranch == "") {
		return fmt.Errorf("confirm, expect_head_oid and expect_branch from delete-plan are required")
	}
	if c.Operation == "shells/restore" && strings.HasPrefix(c.Target, "-") {
		return fmt.Errorf("restore target must be a managed tmux session name")
	}
	if c.Operation == "agents/start" {
		if strings.HasPrefix(c.Target, "-") {
			return fmt.Errorf("agent start target must be a managed session name or path")
		}
		return required(c.Kind, "kind")
	}
	if c.Operation == "agents/prompt" {
		if err := required(c.Text, "text"); err != nil {
			return err
		}
		if c.Text == "-" {
			return fmt.Errorf("text must be literal prompt text, not the CLI stdin sentinel")
		}
		if !c.Wait && c.Timeout != "" {
			return fmt.Errorf("timeout requires wait")
		}
		if c.Wait && c.Timeout == "" {
			return fmt.Errorf("timeout is required when wait is true")
		}
		if c.Timeout != "" {
			d, err := time.ParseDuration(c.Timeout)
			if err != nil || d <= 0 || d > 2*time.Minute {
				return fmt.Errorf("timeout must be positive and no more than 2m")
			}
		}
	}
	return nil
}
func workspaceStatus(exit int) int {
	switch exit {
	case 2:
		return 400
	case 3:
		return 404
	case 4, 5:
		return 409
	case 0:
		return 200
	default:
		return 503
	}
}
func writeWorkspaceError(w http.ResponseWriter, err error) {
	if e, ok := err.(*OperationError); ok {
		w.Header().Set("X-Sidecar-Exit-Code", fmt.Sprint(e.ExitCode))
		writeError(w, workspaceStatus(e.ExitCode), e.Code, e.Message)
		return
	}
	writeError(w, 503, CodeBackend, err.Error())
}

// Projects query accepts only host. Kept separate from catalog filters.
func projectsQuery(values url.Values) error {
	for key, list := range values {
		if key != "host" || len(list) != 1 {
			return fmt.Errorf("projects accepts only a single host")
		}
	}
	return nil
}
