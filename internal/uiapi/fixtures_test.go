package uiapi

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	notification "github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func fixtureDir() string { return filepath.Join("..", "..", "testdata", "ui-api", "v0") }
func TestUIAPIFixtureCorpus(t *testing.T) {
	dir := fixtureDir()
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	identity := mobile.FixtureIdentity("fixture", "local:fixture", "fixture-config", "fixture-project", "fixture-echo", "%1")
	shellMainCheckout := false
	row := mobileproto.CatalogRow{MainCheckout: &shellMainCheckout, ID: "fixture-shell", OwnerHostID: identity.OwnerHostID, ProjectID: "fixture-project", ProjectName: "Fixture project", WorkspaceID: identity.WorkspaceID, WorkspaceKind: "shell", DisplayName: "Echo terminal", Path: "/workspace/fixture", Provider: "codex", Status: "working", Group: "Working", Session: identity.Session, Pane: identity.Pane, Target: identity.Session, ExpectedTarget: &identity, AttachState: "ready", ObservedAt: now.Format(time.RFC3339), ChangedAt: now.Format(time.RFC3339), Live: true, SemanticStatus: true, AttachmentReady: true}
	catalog := mobileproto.CatalogSnapshot{Generation: "fixture-generation", ObservedAt: row.ObservedAt, HubID: identity.HubID, OwnerHostID: identity.OwnerHostID, OwnerConfigGeneration: identity.OwnerConfigGeneration, Query: mobileproto.CatalogQuery{Sort: "project"}, Hosts: []mobileproto.CatalogHost{{ID: identity.OwnerHostID, Name: "Fixture host", State: "online", Local: true}}, Sections: []mobileproto.CatalogSection{{Key: "fixture-project", Title: "Fixture project", Rows: []mobileproto.CatalogRow{row}}}, Failures: []mobileproto.CatalogFailure{}, Total: 1}
	for _, kind := range []string{"shell", "worktree"} {
		catalog.Sections[0].Rows = append(catalog.Sections[0].Rows, mobileproto.CatalogRow{
			ID: "fixture-feature-" + kind, OwnerHostID: identity.OwnerHostID, ProjectID: row.ProjectID, ProjectName: row.ProjectName,
			MainCheckout:  &shellMainCheckout,
			WorkspaceKind: kind, DisplayName: "Feature " + kind, Path: "/workspace/feature",
			ContentWorkspaceID: "/workspace/fixture:worktree:/workspace/feature",
			Status:             "idle", Group: "No Session", AttachState: "unavailable", ObservedAt: row.ObservedAt,
		})
	}
	catalog.Total = 3
	feature := &catalog.Sections[0].Rows[2]
	feature.WorkspaceID = feature.ContentWorkspaceID
	feature.Live, feature.Ambiguous = true, true
	feature.AttachState, feature.CandidateGeneration = "ambiguous", "fixture-candidate-set"
	for i, pane := range []string{"%2", "%3"} {
		feature.Candidates = append(feature.Candidates, mobileproto.CatalogCandidate{
			Selector: fmt.Sprintf("opaque-fixture-feature-%d", i+1), DisplayName: fmt.Sprintf("Feature terminal %d", i+1),
			ContentWorkspaceID: feature.ContentWorkspaceID, OwnerHostID: identity.OwnerHostID, WorkspaceID: feature.WorkspaceID,
			WorkspaceKind: "worktree", Session: "fixture-feature", Pane: pane,
			ExpectedTarget: mobile.FixtureWorkspaceIdentity(identity.HubID, identity.OwnerHostID, identity.OwnerConfigGeneration, feature.WorkspaceID, "worktree", "fixture-feature", pane),
		})
	}
	values := map[string]any{
		"notifications-exchange.json": []any{NotificationSnapshot{Notifications: []notification.Notification{{ID: "ntf-fixture", Source: notification.SourceAgent, Title: "Build finished", CreatedAt: now, ExpiresAt: timePointer(now.Add(12 * time.Second)), Targets: []notification.Target{{Kind: notification.TargetFile, Value: "README.md", Line: 40, Project: "fixture-project"}}}}, Unread: 1, ToastIDs: []string{"ntf-fixture"}, DeliveryIDs: []string{}, Delivery: map[string]notification.DeliveryDecision{}}, NotificationReceiptRequest{ID: "ntf-fixture", Channel: "toast"}, NotificationClaimResponse{Claimed: true}, NotificationReceiptResponse{Delivered: true}},
		"viewer-exchange.json": []any{
			EventMessage{Type: "hello", Seq: 1, APIInstance: "api_fixture", ServerVersion: "fixture", Capabilities: []string{"catalog", "attention", "terminals", "workspace", "content", "uiRequestRelayV1", "notifications", "shutdown"}},
			EventMessage{Type: "viewer", Seq: 2, Viewer: &ViewerIdentity{ID: "api-viewer-fixture", Capability: "uiRequestRelayV1"}},
			ViewerPresenceRequest{ViewerID: "api-viewer-fixture", Focused: true, Visible: true, Project: "fixture-project", Session: "fixture-echo", Viewport: Viewport{Width: 1200, Height: 800}, FocusedPane: 1},
			ViewerPresenceResponse{Holder: true},
			EventMessage{Type: "ui_request", Seq: 3, UIRequest: &UIRequestEvent{ID: "fixture-request", Action: uirequest.ActionOpen, Project: "fixture-project", Request: uirequest.Request{Viewer: "api-viewer-fixture", Version: 1, ID: "fixture-request", CreatedAt: now, TTLMs: 15000, Action: uirequest.ActionOpen, Origin: uirequest.Origin{WorkDir: "/workspace/fixture", TmuxSession: "fixture-echo", TmuxPane: "%1"}, Target: uirequest.Target{Kind: "file", Value: "README.md"}}, OriginPane: 1, Document: LayoutDocument{Layout: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{Axis: "cols", Ratio: 50, A: &state.PaneLayoutJSON{Kind: "terminal", Session: "fixture-echo", Attachment: &state.PaneAttachmentJSON{Selector: "opaque-fixture-selector", ExpectedTarget: identity}}, B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "README.md"}}}}}}, ETag: `"synthetic-layout-revision"`, ExpiresAt: now.Add(15 * time.Second)}},
			ViewerAckRequest{ViewerID: "api-viewer-fixture", ID: "fixture-request", Status: uirequest.StatusOpened},
		},
		"session-proof.json": []any{
			map[string]any{"method": "POST", "path": "/api/v0/pairing/session-proof", "listener": "browser", "origin": "http://127.0.0.1:7861", "request": SessionProofChallengeRequest{RegistrationID: browserRegistrationID("http://127.0.0.1:7861", fixtureBrowserPublicKey())}, "response": SessionProofChallenge{Nonce: "synthetic-nonce", Timestamp: now.UnixMilli(), ExpiresAt: now.Add(pairingCodeTTL)}},
			map[string]any{"method": "POST", "path": "/api/v0/pairing/session-proof/verify", "listener": "browser", "origin": "http://127.0.0.1:7861", "request": SessionProofRequest{RegistrationID: browserRegistrationID("http://127.0.0.1:7861", fixtureBrowserPublicKey()), Nonce: "synthetic-nonce", Timestamp: now.UnixMilli(), Signature: fixtureBrowserSignature}, "response": SessionToken{Token: "synthetic-memory-token", ExpiresAt: now.Add(browserBearerTTL)}},
		},
		"hello.json":         Hello{APIVersion: 0, APIInstance: "api_fixture", ServerVersion: "fixture", Capabilities: []string{"sessions", "status", "terminal", "ws_tickets", "events", "projects", "workspace", "workspace_operations", "content", "layouts", "uiRequestRelayV1", "notifications", "file_search", "notifications_batch"}, Terminal: TerminalProtocol{Protocol: "mobile", Version: 0}},
		"sessions.json":      catalog,
		"file-search.json":   contentservice.FileSearchResult{Root: "/workspace/fixture", Query: "read", Results: []contentservice.FileSearchMatch{{Path: "README.md", Positions: []int{0, 1, 2, 3}, Score: 134}}},
		"content-file.json":  contentservice.ReadResult{Kind: "file", Operation: "document", Workspace: "fixture-project", Display: "README.md", Path: "/workspace/fixture/README.md", Revision: "fixture-file-v1", Content: "# Fixture project\n\nA Markdown pane.\n"},
		"content-issue.json": contentservice.ReadResult{Kind: "issue", Operation: "card", Workspace: "fixture-project", Target: "td-123456", Revision: "fixture-issue-v1", Issue: &contentservice.IssueDTO{ID: "td-123456", Title: "Fixture issue", Status: "open"}},
		"content-note.json":  contentservice.ReadResult{Kind: "note", Operation: "note", Workspace: "fixture-project", Target: "nt-123456", Revision: "fixture-note-v1", Note: &contentservice.NoteDTO{ID: "nt-123456", Title: "Fixture note", Content: "A note pane."}},
		"content-diff.json":  contentservice.ReadResult{Kind: "diff", Operation: "working-tree", Workspace: "fixture-project", Target: "working-tree", Revision: "fixture-diff-v1", Diff: &contentservice.DiffDTO{Target: "working-tree", Snapshot: &contentservice.DiffSnapshotDTO{Files: []contentservice.DiffFileRowDTO{{Path: "README.md", Raw: "@@ -1 +1 @@\n-old\n+new\n"}}}}},
		"content-tree.json":  contentservice.TreeResult{Kind: "tree", Workspace: "fixture-project", Dirs: []contentservice.TreeDir{{Path: "", Entries: []contentservice.TreeEntry{{Name: "README.md"}}}}},
		"layout.json":        LayoutDocument{Layout: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{Axis: "cols", Ratio: 50, A: &state.PaneLayoutJSON{Kind: "terminal", Session: "fixture-echo", Attachment: &state.PaneAttachmentJSON{Selector: "opaque-fixture-selector", ExpectedTarget: identity}}, B: &state.PaneLayoutJSON{Kind: "doc", Tabs: []state.PaneDocTabJSON{{Path: "README.md", Mode: "rendered"}}}}}},
		"content-event.json": EventMessage{Type: "content", Seq: 4, Content: &ContentEvent{Resources: []ContentRef{{Project: "fixture-project", Kind: "file", Target: "README.md"}}}},
		"status.json":        Status{APIVersion: 0, APIInstance: "api_fixture", ServerVersion: "fixture", PID: 4242, StartedAt: now, Listeners: []ListenerInfo{{Name: ListenerLocal, Network: "unix", Address: "/tmp/fixture/api.sock"}, {Name: ListenerBrowser, Network: "tcp", Address: "127.0.0.1:7861"}}, Clients: []ClientInfo{}, Terminals: []TerminalInfo{}},
		"error.json":         ErrorBody{Error: ErrorDetail{Code: CodeUnauthenticated, Message: "Pair this browser with sidecar api open."}},
		"pairing.json":       []any{map[string]any{"method": "POST", "path": "/api/v0/pairing/codes", "listener": "local", "request": PairingCodeRequest{Next: "/s/fixture"}, "response": PairingCode{Code: "synthetic-code", URL: "http://127.0.0.1:7861/pair#code=synthetic-code&next=%2Fs%2Ffixture", ExpiresAt: now.Add(time.Minute)}}, map[string]any{"method": "POST", "path": "/api/v0/pairing/exchange", "listener": "browser", "origin": "http://127.0.0.1:7861", "request": PairingExchangeRequest{Code: "synthetic-code", Next: "/s/fixture", PublicKey: fixtureBrowserPublicKey()}, "response": PairingExchange{RegistrationID: browserRegistrationID("http://127.0.0.1:7861", fixtureBrowserPublicKey()), Token: "synthetic-memory-token", ExpiresAt: now.Add(browserBearerTTL), Next: "/s/fixture"}}},
	}
	// The same layout document is read/written at the scope carried by presence
	// and the relay proposal. These examples are synthetic, not live authority.
	workspace := "/workspace/fixture:worktree:/workspace/feature"
	exchange := values["viewer-exchange.json"].([]any)
	worktreeExchange := append([]any(nil), exchange...)
	presence := worktreeExchange[2].(ViewerPresenceRequest)
	presence.Workspace = workspace
	worktreeExchange[2] = presence
	proposal := worktreeExchange[4].(EventMessage)
	uiRequest := *proposal.UIRequest
	uiRequest.Workspace = workspace
	uiRequest.Request.Origin.WorkDir = "/workspace/feature"
	proposal.UIRequest = &uiRequest
	worktreeExchange[4] = proposal
	values["viewer-worktree-exchange.json"] = worktreeExchange
	values["layout-workspace-exchange.json"] = []any{
		map[string]any{"method": "GET", "path": "/api/v0/projects/fixture-project/layout", "query": map[string]string{"workspace": workspace}, "response": LayoutDocument{}, "etag": `"synthetic-empty-layout"`},
		map[string]any{"method": "PUT", "path": "/api/v0/projects/fixture-project/layout", "query": map[string]string{"workspace": workspace}, "if_match": `"synthetic-empty-layout"`, "request": uiRequest.Document, "response": uiRequest.Document, "etag": `"synthetic-worktree-layout"`},
	}
	secondIdentity := identity
	secondIdentity.Pane = "%2"
	firstHint := &state.PaneAttachmentJSON{Selector: "opaque-fixture-candidate-one", ExpectedTarget: identity}
	secondHint := &state.PaneAttachmentJSON{Selector: "opaque-fixture-candidate-two", ExpectedTarget: secondIdentity}
	candidates := LayoutDocument{Layout: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal", Session: identity.Session, Attachment: firstHint},
		B: &state.PaneLayoutJSON{Kind: "shell", Session: identity.Session, Attachment: secondHint}}}}
	values["layout-candidates.json"] = candidates
	values["viewer-candidates-exchange.json"] = []any{
		map[string]any{"method": "PUT", "path": "/api/v0/projects/fixture-project/layout", "request": candidates, "response": candidates, "etag": `"synthetic-candidate-layout"`},
		EventMessage{Type: "ui_request", Seq: 3, UIRequest: &UIRequestEvent{ID: "fixture-candidate-request", Action: uirequest.ActionLayout, Project: "fixture-project", OriginPane: 3,
			Request: uirequest.Request{Version: 1, ID: "fixture-candidate-request", CreatedAt: now, TTLMs: 15000, Action: uirequest.ActionLayout,
				Origin: uirequest.Origin{TmuxSession: identity.Session, TmuxPane: secondIdentity.Pane, WorkDir: "/workspace/fixture"}, Payload: json.RawMessage(`{"mode":"get"}`)},
			Document: candidates, ETag: `"synthetic-candidate-layout"`, ExpiresAt: now.Add(15 * time.Second)}},
		ViewerAckRequest{ViewerID: "api-viewer-fixture", ID: "fixture-candidate-request", Status: uirequest.StatusOpened},
		ViewerAckResponse{Document: candidates, ETag: `"synthetic-candidate-layout"`},
	}
	project := workspacewire.Project{Key: "fixture-project", Name: "Fixture project", Path: "/workspace/fixture"}
	mainCheckout := true
	mainRow := mobileproto.CatalogRow{ID: "fixture-main-checkout", OwnerHostID: identity.OwnerHostID, ProjectID: project.Key, ProjectName: project.Name, WorkspaceKind: "worktree", DisplayName: "Main checkout", Path: project.Path, MainCheckout: &mainCheckout, Branch: "main", Status: "no session", Group: "No Session", AttachState: "unavailable", ObservedAt: row.ObservedAt}
	mainCatalog := catalog
	mainCatalog.Total = 1
	mainCatalog.Sections = []mobileproto.CatalogSection{{Key: project.Key, Title: project.Name, Rows: []mobileproto.CatalogRow{mainRow}}}
	values["workspace-main-checkout.json"] = workspacewire.Workspace{Project: project, Catalog: mainCatalog, Shells: []workspacewire.ShellRecord{}}
	values["projects.json"] = workspacewire.Projects{Projects: []workspacewire.Project{project}}
	values["workspace.json"] = workspacewire.Workspace{Project: project, Catalog: catalog, Shells: []workspacewire.ShellRecord{{Shell: "fixture-echo", Name: row.DisplayName, WorkDir: project.Path, Status: "live"}, {Shell: "fixture-forgotten", Name: "Recoverable shell", Status: "forgotten", DeletedAt: &now}}}
	values["workspace-event.json"] = EventMessage{Type: "workspace", Seq: 4, Workspace: &workspacewire.WorkspaceEvent{Projects: workspacewire.Projects{Projects: []workspacewire.Project{project}}, Workspaces: []workspacewire.WorkspaceRef{{Project: project.Key}}}}
	plan := workspaceops.WorktreePlan{SourceWorktree: project.Path, MainWorktree: project.Path, SourceRef: "refs/heads/main", SourceOID: strings.Repeat("a", 40), Branch: "feature", Path: "/workspace/feature", DisplayName: "Feature", RemotePolicy: "local"}
	values["workspace-operations.json"] = []any{
		map[string]any{"path": "/api/v0/projects/fixture-project/shells/create", "request": WorkspaceCommand{Name: "New shell"}, "response": workspacewire.ShellCreated{Project: project.Key, Shell: workspacewire.ShellInfo{DisplayName: "New shell", Session: "fixture-new", WorkDir: project.Path}, Placement: "workspace"}},
		map[string]any{"path": "/api/v0/projects/fixture-project/shells/start", "request": WorkspaceCommand{Target: "fixture-new"}, "response": workspacewire.SessionStarted{Project: project.Key, Shell: workspacewire.ShellInfo{DisplayName: "New shell", Session: "fixture-new", WorkDir: project.Path}, Status: "started"}},
		map[string]any{"path": "/api/v0/projects/fixture-project/worktrees/start", "request": WorkspaceCommand{Target: plan.Path}, "response": workspacewire.SessionStarted{Project: project.Key, Shell: workspacewire.ShellInfo{DisplayName: plan.DisplayName, Session: "fixture-worktree", WorkDir: plan.Path}, Status: "started"}},
		map[string]any{"path": "/api/v0/projects/fixture-project/shells/rename", "request": WorkspaceCommand{Target: "fixture-new", Name: "Review"}, "response": shellstate.RenameResult{Shell: "fixture-new", OldName: "New shell", Name: "Review", Changed: true}},
		map[string]any{"path": "/api/v0/projects/fixture-project/shells/delete", "request": WorkspaceCommand{Target: "fixture-new"}, "response": workspacewire.ShellDeleted{Shell: "fixture-new", Name: "Review", Status: "deleted", Deleted: true}},
		map[string]any{"path": "/api/v0/projects/fixture-project/shells/restore", "request": WorkspaceCommand{Target: "fixture-new"}, "response": workspacewire.ShellRestored{Shell: "fixture-new", Name: "Review", Status: "restored"}},
		map[string]any{"path": "/api/v0/projects/fixture-project/worktrees/plan", "request": WorkspaceCommand{Name: "Feature", Base: "main"}, "response": plan},
		map[string]any{"path": "/api/v0/projects/fixture-project/worktrees/create", "request": WorkspaceCommand{Name: "Feature", Base: "main", Confirm: true, ExpectSourceOID: plan.SourceOID}, "response": workspacewire.WorktreeCreated{Project: project.Key, Path: plan.Path, Branch: plan.Branch, Shell: workspacewire.ShellInfo{DisplayName: plan.DisplayName, Session: "fixture-worktree", WorkDir: plan.Path}, Setup: []workspacewire.SetupOutcome{}, Placement: "workspace"}},
	}

	deletion := workspacewire.WorktreeDeletePlan{Project: project.Key, Name: plan.DisplayName, Path: plan.Path, Branch: plan.Branch, HeadOID: plan.SourceOID, DeleteState: strings.Repeat("b", 64), BranchOID: plan.SourceOID, Dirtiness: "dirty"}
	deletion.ManagedShells = []workspacewire.WorktreeDeleteShell{{ProjectRoot: "/workspace/another-project", Session: "fixture-cross-project", DisplayName: "Cross-project shell", WorkDir: plan.Path, CanClose: true}}
	target := agentcontrol.Target{Host: "local", Project: project.Key, Session: "fixture-new", PaneID: "%1"}
	state := agentcontrol.AgentState{Kind: "codex", Status: agentcontrol.StatusIdle, Freshness: "fresh", InteractiveReady: true, CapturedAt: now}
	exchanges := values["workspace-operations.json"].([]any)
	values["workspace-operations.json"] = append(exchanges,
		map[string]any{"path": "/api/v0/projects/fixture-project/worktrees/rename", "request": WorkspaceCommand{Target: "fixture-worktree", Name: "Review branch"}, "response": shellstate.RenameResult{Shell: "fixture-worktree", OldName: "Feature", Name: "Review branch", Changed: true}},
		map[string]any{"path": "/api/v0/projects/fixture-project/worktrees/delete-plan", "request": WorkspaceCommand{Target: plan.Path}, "response": workspacewire.WorktreeDeleted{Status: "planned", Plan: deletion}},
		map[string]any{"path": "/api/v0/projects/fixture-project/worktrees/delete", "request": WorkspaceCommand{Target: plan.Path, Confirm: true, ExpectHeadOID: plan.SourceOID, ExpectBranch: plan.Branch, ExpectDeleteState: deletion.DeleteState}, "response": workspacewire.WorktreeDeleted{Status: "deleted", Deleted: true, Plan: deletion}},
		map[string]any{"path": "/api/v0/projects/fixture-project/agents/start", "request": WorkspaceCommand{Target: target.Session, Kind: "codex"}, "response": agentcontrol.Agent{Target: target, Agent: state}},
		map[string]any{"path": "/api/v0/projects/fixture-project/agents/prompt", "request": WorkspaceCommand{Target: target.Session, Text: "-"}, "response": agentcontrol.PromptResult{Target: target, Agent: state, Receipt: agentcontrol.PromptReceipt{Target: target, Submission: agentcontrol.SubmissionSubmitted, Wait: agentcontrol.PromptWaitNotRequested}}},
		map[string]any{"path": "/api/v0/projects/fixture-project/agents/prompt", "request": WorkspaceCommand{Target: target.Session, Text: "continue"}, "status": 409, "exit_code": 5, "response": agentcontrol.ErrorEnvelope{Error: &agentcontrol.Error{Code: agentcontrol.ErrFeatureDisabled, Message: "Enable agent_control before submitting input.", Receipt: &agentcontrol.PromptReceipt{Target: target, Submission: agentcontrol.SubmissionNotSubmitted, Wait: agentcontrol.PromptWaitNotRequested}}}},
	)

	if os.Getenv("UPDATE_UI_API_FIXTURES") == "1" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, value := range values {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, value := range values {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(append(data, '\n')) {
			t.Fatalf("stale fixture %s: run ./scripts/update-ui-api-contract.sh", name)
		}
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.json*"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), filepath.Base(name))
	}
	sumPath := filepath.Join(dir, "SHA256SUMS")
	if os.Getenv("UPDATE_UI_API_FIXTURES") == "1" {
		if err := os.WriteFile(sumPath, []byte(sums.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(sumPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sums.String() {
		t.Fatal("UI API SHA256SUMS is stale: run ./scripts/update-ui-api-contract.sh")
	}
	if _, err := LoadFixtures(dir); err != nil {
		t.Fatal(err)
	}
}

func TestFixtureServerTerminalUsesRealOrderingAndGuards(t *testing.T) {
	backend, err := LoadFixtures(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "u1b-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	s, err := Start(Options{StateDir: root, Port: 0, Backend: backend, FixtureStatus: &backend.Status})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := NewLocalClientForSocket(s.Endpoint())
	conn, _, err := websocket.Dial(ctx, strings.Replace(s.BrowserURL(), "http://", "ws://", 1)+terminalPath, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{s.BrowserURL()}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != CloseUnauthenticated {
		t.Fatalf("fixture auth bypass: %v", err)
	}
	_ = conn.CloseNow()
	conn, _, err = websocket.Dial(ctx, strings.Replace(client.URL(terminalPath), "http://", "ws://", 1), &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	var transcript strings.Builder
	record := func(direction string, message any) {
		data, _ := json.Marshal(map[string]any{"direction": direction, "message": message})
		transcript.Write(data)
		transcript.WriteByte('\n')
	}
	send := func(req mobileproto.Request) {
		t.Helper()
		req.Version = mobileproto.Version
		data, _ := json.Marshal(req)
		record("client", req)
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
	}
	read := func() mobileproto.Response {
		t.Helper()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var response mobileproto.Response
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		record("server", response)
		return response
	}
	send(mobileproto.Request{Type: "hello", RequestID: "hello"})
	if r := read(); r.Type != "hello" {
		t.Fatal(r)
	}
	send(mobileproto.Request{Type: "sessions", RequestID: "sessions", CatalogQuery: &mobileproto.CatalogQuery{Sort: "name", Search: "Echo"}})
	if r := read(); r.Type != "sessions" || r.Catalog == nil || r.Catalog.Total != 1 {
		t.Fatal(r)
	}
	expected := backend.targets["fixture-echo"]
	send(mobileproto.Request{Type: "resolve", RequestID: "resolve", Target: "fixture-echo", ExpectedTarget: &expected})
	resolved := read()
	if resolved.Target == nil {
		t.Fatal(resolved)
	}
	send(mobileproto.Request{Type: "open", RequestID: "open", TargetHandle: resolved.Target.Handle, AttachmentID: "fixture-client"})
	opened := read()
	if opened.Type != "opened" {
		t.Fatal(opened)
	}
	frame := read()
	if frame.Type != "frame" || frame.OutputSequence != 1 || frame.ResetGeneration != 1 {
		t.Fatal(frame)
	}
	mutate := func(kind, id string, seq uint64, cols, rows int, data string) {
		send(mobileproto.Request{Type: kind, RequestID: id, AttachmentHandle: opened.AttachmentHandle, OperationSequence: seq, LastResetGeneration: frame.ResetGeneration, LastOutputSequence: frame.OutputSequence, Columns: cols, Rows: rows, DataBase64: base64.StdEncoding.EncodeToString([]byte(data))})
	}
	mutate("control", "control", 1, 40, 10, "")
	control := read()
	if control.Type != "control" || control.ResetGeneration != 2 {
		t.Fatal(control)
	}
	if r := read(); r.Type != "reset" {
		t.Fatal(r)
	}
	frame = read()
	if frame.Geometry.Columns != 40 || frame.Type != "frame" {
		t.Fatal(frame)
	}
	mutate("input", "input", 2, 0, 0, "FIXTURE_ECHO")
	accepted := read()
	if accepted.Type != "accepted" {
		t.Fatal(accepted)
	}
	frame = read()
	vt, err := base64.StdEncoding.DecodeString(frame.RenderVTBase64)
	if err != nil || !strings.Contains(string(vt), "FIXTURE_ECHO") {
		t.Fatalf("echo missing: %q %v", vt, err)
	}
	mutate("input", "gap", 4, 0, 0, "MUST_NOT_ECHO")
	gap := read()
	if gap.Error == nil || gap.Error.Code != mobileproto.ErrorOperationOrder {
		t.Fatal(gap)
	}
	// A refused operation does not consume sequence 3; re-control with 3 works.
	mutate("control", "reclaim", 3, 40, 10, "")
	if r := read(); r.Type != "control" {
		t.Fatal(r)
	}
	mutate("release", "release", 4, 0, 0, "")
	if r := read(); r.Type != "released" {
		t.Fatal(r)
	}
	send(mobileproto.Request{Type: "close", RequestID: "close", AttachmentHandle: opened.AttachmentHandle})
	if r := read(); r.Type != "closed" {
		t.Fatal(r)
	}
	// A fresh connection reconstructs the catalog identity and never replays input.
	if err := conn.Close(websocket.StatusNormalClosure, "reconnect proof"); err != nil {
		t.Fatal(err)
	}
	conn, _, err = websocket.Dial(ctx, strings.Replace(client.URL(terminalPath), "http://", "ws://", 1), &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	send(mobileproto.Request{Type: "hello", RequestID: "hello"})
	if r := read(); r.Type != "hello" {
		t.Fatal(r)
	}
	send(mobileproto.Request{Type: "reconnect", RequestID: "reconnect", Target: "fixture-echo", ExpectedTarget: &expected, PreviousAttachmentGeneration: 1, AttachmentID: "fixture-client"})
	reconnected := read()
	if reconnected.Type != "reconnected" || reconnected.AttachmentGeneration != 2 {
		t.Fatal(reconnected)
	}
	frame = read()
	if frame.Type != "frame" || frame.OutputSequence != 1 || frame.ResetGeneration != 1 || frame.Control {
		t.Fatal(frame)
	}
	send(mobileproto.Request{Type: "close", RequestID: "close", AttachmentHandle: reconnected.AttachmentHandle})
	if r := read(); r.Type != "closed" {
		t.Fatal(r)
	}
	normalized := regexp.MustCompile(`(api|target|attachment)_[a-f0-9]{32}`).ReplaceAllString(transcript.String(), "${1}_fixture")
	path := filepath.Join(fixtureDir(), "terminal.jsonl")
	if os.Getenv("UPDATE_UI_API_FIXTURES") == "1" {
		if err := os.WriteFile(path, []byte(normalized), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != normalized {
		t.Fatal("terminal transcript differs from the real Service; run ./scripts/update-ui-api-contract.sh")
	}
}

func TestFixtureCatalogUsesSharedQueryAndRejectsMalformedAuthority(t *testing.T) {
	backend, err := LoadFixtures(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query mobileproto.CatalogQuery
		total int
	}{
		{mobileproto.CatalogQuery{Sort: "name", Search: "echo"}, 1},
		{mobileproto.CatalogQuery{Providers: []string{"claude"}}, 0},
		{mobileproto.CatalogQuery{States: []string{"ready"}}, 1},
		{mobileproto.CatalogQuery{Hosts: []string{"absent"}}, 0},
	} {
		got, err := backend.Sessions(context.Background(), tc.query)
		if err != nil || got.Total != tc.total {
			t.Fatalf("%+v: %+v %v", tc.query, got, err)
		}
	}
	if _, err := backend.Sessions(context.Background(), mobileproto.CatalogQuery{Sort: "unknown"}); err == nil {
		t.Fatal("invalid sort accepted")
	}
	dir := t.TempDir()
	for _, name := range []string{"sessions.json", "status.json"} {
		data, err := os.ReadFile(filepath.Join(fixtureDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var catalog mobileproto.CatalogSnapshot
	data, err := os.ReadFile(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Sections[0].Rows[0].ExpectedTarget.TargetGeneration = "arbitrary-real-target"
	data, err = json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixtures(dir); err == nil {
		t.Fatal("non-fixture identity accepted")
	}
}

func TestFixtureLoaderRejectsDocumentBeyondByteBound(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sessions.json", "status.json"} {
		data, err := os.ReadFile(filepath.Join(fixtureDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "status.json" {
			// The decoder sees a valid first document and whitespace until
			// the limit, hiding the second document beyond its artificial EOF.
			data = append(data, []byte(strings.Repeat(" ", mobileproto.MaxLineBytes))...)
			data = append(data, []byte(`{"pid":999}`)...)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadFixtures(dir); err == nil {
		t.Fatal("oversized fixture with hidden second JSON document accepted")
	}
}

func fixtureBrowserPublicKey() BrowserPublicKey {
	curve := elliptic.P256().Params()
	return BrowserPublicKey{Kty: "EC", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(curve.Gx.FillBytes(make([]byte, 32))), Y: base64.RawURLEncoding.EncodeToString(curve.Gy.FillBytes(make([]byte, 32)))}
}

// Fixed valid P-256 vector for the synthetic generator-point public key and
// the fixed nonce/timestamp above. This is example material, never a credential.
const fixtureBrowserSignature = "axfR8uEsQkf4vOblY6RA8ncDfYEt6zOg9KE5RdiYwpYaO0QJAFWXamzPiK7epCFH5TubNJwuXLt-rtW_T7JfBw"

func timePointer(t time.Time) *time.Time { return &t }
