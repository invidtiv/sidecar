package uiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func (b *fakeBackend) Projects(context.Context, string) (workspacewire.Projects, error) {
	return workspacewire.Projects{Projects: []workspacewire.Project{{Key: "fixture-project", Name: "Fixture"}}}, nil
}
func (b *fakeBackend) Workspace(context.Context, string, string, mobileproto.CatalogQuery) (workspacewire.Workspace, error) {
	return workspacewire.Workspace{Project: workspacewire.Project{Key: "fixture-project"}, Shells: []workspacewire.ShellRecord{}, Catalog: b.snapshot}, nil
}
func (*fakeBackend) WorkspaceOperation(_ context.Context, project string, c WorkspaceCommand) (json.RawMessage, int, error) {
	if c.Target == "missing" {
		return nil, 3, &OperationError{Code: "agent_not_found", Message: "Target disappeared; refresh the workspace.", ExitCode: 3}
	}
	data, _ := json.Marshal(map[string]any{"project": project, "operation": c.Operation, "command": c})
	return data, 0, nil
}
func TestWorkspaceRoutesScopeGuardsAndValidation(t *testing.T) {
	h := newHarness(t)
	response, body := h.localDo(req{method: "GET", path: "/api/v0/projects"})
	expectWorkspaceStatus(t, response, body, 200)
	for operation := range workspaceOperationFields {
		response, body = h.localDo(req{method: "GET", path: "/api/v0/projects/fixture-project/" + operation})
		expectWorkspaceStatus(t, response, body, 405)
	}
	cases := []struct {
		op, body string
		status   int
	}{
		{"shells/create", `{"name":"New"}`, 200},
		{"shells/start", `{"target":"managed"}`, 200},
		{"shells/start", `{}`, 400},
		{"worktrees/start", `{"target":"/checkout"}`, 200},
		{"worktrees/start", `{}`, 400},
		{"worktrees/start", `{"target":"/checkout","name":"feature"}`, 400},
		{"shells/create", `{} {}`, 400},
		{"shells/create", `null`, 400},
		{"shells/rename", `{"target":"managed","name":"Review"}`, 200},
		{"shells/delete", `{"target":"missing"}`, 404},
		{"worktrees/create", `{"name":"feature"}`, 400},
		{"worktrees/create", `{"name":"feature","confirm":true,"expect_source_oid":"abc"}`, 200},
		{"worktrees/delete", `{"target":"/checkout","confirm":true}`, 400},
		{"worktrees/delete", `{"target":"/checkout","confirm":true,"expect_head_oid":"abc","expect_branch":"topic","expect_delete_state":"abc"}`, 200},
		{"agents/start", `{"target":"managed","kind":"codex","args":["--model","test"]}`, 200},
		{"agents/prompt", `{"target":"managed","text":"continue","wait":true}`, 400},
		{"agents/prompt", `{"target":"managed","text":"continue","wait":true,"timeout":"10s"}`, 200},
		{"agents/prompt", `{"target":"managed","text":"-"}`, 200},
		{"shells/restore", `{"target":"-session"}`, 200},
		{"agents/start", `{"target":"-session","kind":"codex"}`, 200},
		{"shells/delete", `{"target":"managed","args":["--project","elsewhere"]}`, 400},
	}
	for _, tc := range cases {
		response, body = h.localDo(req{method: "POST", path: "/api/v0/projects/fixture-project/" + tc.op, body: tc.body})
		expectWorkspaceStatus(t, response, body, tc.status)
	}
	token, _, err := h.s.origins.pair("http://widget.example", []string{"readonly"}, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	response, body = h.browserDo(req{method: "POST", path: "/api/v0/projects/fixture-project/shells/create", body: `{}`, header: map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json", mutationHeader: "1"}})
	expectWorkspaceStatus(t, response, body, 403)
	if !strings.Contains(string(body), "scope_refused") {
		t.Fatal(string(body))
	}
	response, body = h.tailnetDo(req{method: "POST", path: "/api/v0/projects/fixture-project/shells/create", body: `{}`, header: mutationHeaders("http://widget.example", map[string]string{tailscaleLoginHead: testTailnetLogin, "Authorization": "Bearer " + token})})
	expectWorkspaceStatus(t, response, body, 403)
	token, _, err = h.s.origins.pair("http://widget.example", []string{ScopeWorkspaceWrite}, h.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	response, body = h.browserDo(req{method: "POST", path: "/api/v0/projects/fixture-project/shells/create", body: `{}`, header: map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json", mutationHeader: "1"}})
	expectWorkspaceStatus(t, response, body, 200)
	response, body = h.tailnetDo(req{method: "POST", path: "/api/v0/projects/fixture-project/shells/create", body: `{}`, header: mutationHeaders("http://widget.example", map[string]string{tailscaleLoginHead: testTailnetLogin, "Authorization": "Bearer " + token})})
	expectWorkspaceStatus(t, response, body, 200)
	response, body = h.browserDo(req{method: "POST", path: "/api/v0/projects/fixture-project/shells/create", body: `{}`, header: map[string]string{"Authorization": "Bearer " + token}})
	expectWorkspaceStatus(t, response, body, 403)

}

func expectWorkspaceStatus(t *testing.T, r *http.Response, b []byte, status int) {
	t.Helper()
	if r.StatusCode != status {
		t.Fatalf("status %d want %d: %s", r.StatusCode, status, b)
	}
}

type workspaceEventBackend struct{ *eventBackend }

func (b *workspaceEventBackend) WorkspaceInvalidation(ctx context.Context) (workspacewire.WorkspaceEvent, error) {
	projects, err := b.Projects(ctx, "")
	return workspacewire.WorkspaceEvent{Projects: projects, Workspaces: []workspacewire.WorkspaceRef{{Project: "fixture-project"}, {Project: "owner-project", Host: "remote"}}}, err
}
func TestWorkspaceEventsShareWatchersAndCoalesce(t *testing.T) {
	base := &eventBackend{fakeBackend: newFakeBackend(), changes: make(chan struct{}, 1)}
	b := &workspaceEventBackend{base}
	h := newHarness(t, func(o *Options) { o.Backend = b })
	conn := dialEvents(t, h, "", nil, true)
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	readWorkspace := func() EventMessage {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var m EventMessage
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if m.Type == "workspace" {
				return m
			}
		}
	}
	initial := readWorkspace()
	if len(initial.Workspace.Workspaces) != 2 {
		t.Fatal(initial)
	}
	// A watcher signal invalidates workspace/tombstone state even if the terminal
	// catalog generation is unchanged. No separate catalog polling occurs.
	notify(base.changes)
	next := readWorkspace()
	if next.Seq <= initial.Seq {
		t.Fatal(next)
	}
	p := newEventPending()
	for i := 0; i < 1000; i++ {
		p.put(EventMessage{Type: "workspace", Workspace: initial.Workspace})
	}
	if got := p.take(); len(got) != 1 || got[0].Type != "workspace" {
		t.Fatalf("not coalesced: %v", got)
	}
}
func TestWorkspaceSpecIsDeterministic(t *testing.T) {
	first, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		next, err := Spec()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, next) {
			_ = os.WriteFile("/tmp/u2b-spec-first.json", first, 0600)
			_ = os.WriteFile("/tmp/u2b-spec-next.json", next, 0600)
			t.Fatal("nondeterministic workspace spec")
		}
	}
}

func TestWorkspaceEncodedOwnerProjectPath(t *testing.T) {
	h := newHarness(t)
	response, body := h.localDo(req{method: "POST", path: "/api/v0/projects/%2Fremote%2Frepo/shells/create", body: `{}`})
	expectWorkspaceStatus(t, response, body, 200)
	var result struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Project != "/remote/repo" {
		t.Fatalf("%s %v", body, err)
	}
}
func TestFixtureWorkspaceRetainsRecoverableRecords(t *testing.T) {
	b, err := LoadFixtures(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := b.Workspace(context.Background(), "fixture-project", "", mobileproto.CatalogQuery{Search: "absent"})
	if err != nil || w.Catalog.Total != 0 || len(w.Shells) != 2 || w.Shells[1].Status != "forgotten" {
		t.Fatalf("%+v %v", w, err)
	}
}

func TestEveryWorkspacePostEnforcesOriginMutationAndScopes(t *testing.T) {
	h := newHarness(t)
	body := func(op string) string {
		fields := map[string]any{"host": "owner"}
		values := map[string]any{"target": "managed", "name": "Review", "base": "main", "confirm": true, "expect_source_oid": "abc", "expect_head_oid": "abc", "expect_branch": "topic", "expect_delete_state": "abc", "kind": "codex", "args": []string{"--model", "test"}, "text": "--host=elsewhere; $(touch /tmp/forbidden)", "wait": false, "timeout": "", "delete_local_branch": false, "delete_remote_branch": false}
		for _, field := range workspaceOperationFields[op] {
			fields[field] = values[field]
		}
		data, _ := json.Marshal(fields)
		return string(data)
	}
	for _, scope := range []string{ScopeFull, ScopeWorkspaceWrite, "content:read", "sessions:read"} {
		token, _, err := h.s.origins.pair("http://widget.example", []string{scope}, h.clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		for op := range workspaceOperationFields {
			for _, listener := range []string{"browser", "tailnet"} {
				t.Run(scope+"/"+listener+"/"+op, func(t *testing.T) {
					do := h.browserDo
					origin := h.s.BrowserURL()
					if listener == "tailnet" {
						do = h.tailnetDo
						origin = "https://" + testTailnetHost
					}
					_ = origin
					headers := mutationHeaders("http://widget.example", map[string]string{"Authorization": "Bearer " + token, tailscaleLoginHead: testTailnetLogin})
					want := 200
					if scope != ScopeFull && scope != ScopeWorkspaceWrite {
						want = 403
					}
					r, b := do(req{method: "POST", path: "/api/v0/projects/proof/" + op, body: body(op), header: headers})
					expectWorkspaceStatus(t, r, b, want)
					for _, attack := range []string{"foreign origin", "null origin", "simple form", "missing header", "wrong bearer origin", "no origin"} {
						bad := map[string]string{}
						for k, v := range headers {
							bad[k] = v
						}
						switch attack {
						case "foreign origin":
							bad["Origin"] = "https://evil.example"
						case "null origin":
							bad["Origin"] = "null"
						case "simple form":
							bad["Content-Type"] = "application/x-www-form-urlencoded"
						case "missing header":
							delete(bad, mutationHeader)
						case "wrong bearer origin":
							if listener == "browser" {
								bad["Origin"] = h.s.BrowserURL()
							} else {
								bad["Origin"] = "http://localhost:5173"
							}
						case "no origin":
							delete(bad, "Origin")
						}
						expected := 403
						if attack == "no origin" && listener == "browser" {
							expected = want
						}
						r, b := do(req{method: "POST", path: "/api/v0/projects/%2Fowner%2Frepo/" + op, body: body(op), header: bad})
						expectWorkspaceStatus(t, r, b, expected)
					}
				})
			}
		}
	}
}

type failingWorkspaceBackend struct {
	*fakeBackend
	exit         int
	operationErr error
	result       json.RawMessage
}

func (b *failingWorkspaceBackend) WorkspaceOperation(context.Context, string, WorkspaceCommand) (json.RawMessage, int, error) {
	return b.result, b.exit, b.operationErr
}
func TestWorkspaceFailureNeverReportsHTTPSuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exit   int
		err    error
		result string
	}{
		{"decoder failure", 0, &OperationError{Code: "backend", Message: "invalid owner result", ExitCode: 0}, ""},
		{"partial failure", 1, &OperationError{Code: "backend", Message: "setup failed", ExitCode: 1}, `{"path":"/owner/created"}`},
		{"nonzero without error", 1, nil, `{"path":"/owner/created"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &failingWorkspaceBackend{fakeBackend: newFakeBackend(), exit: tc.exit, operationErr: tc.err, result: json.RawMessage(tc.result)}
			h := newHarness(t, func(o *Options) { o.Backend = b })
			r, data := h.localDo(req{method: "POST", path: "/api/v0/projects/proof/shells/create", body: `{}`})
			expectWorkspaceStatus(t, r, data, 503)
			if tc.result != "" && !strings.Contains(string(data), "/owner/created") {
				t.Fatalf("lost partial receipt: %s", data)
			}
		})
	}
}
