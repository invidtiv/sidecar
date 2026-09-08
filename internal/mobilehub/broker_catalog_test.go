package mobilehub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

type fakeOwnerDirectory struct {
	mu       sync.Mutex
	snapshot DirectorySnapshot
	err      error
	delay    time.Duration
}

func (d *fakeOwnerDirectory) Snapshot(ctx context.Context) (DirectorySnapshot, error) {
	if d.delay > 0 {
		select {
		case <-time.After(d.delay):
		case <-ctx.Done():
			return DirectorySnapshot{}, ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshot, d.err
}

type fakeCatalogLineStream struct {
	mu       sync.Mutex
	catalog  mobileproto.CatalogSnapshot
	lines    chan []byte
	closed   bool
	writes   int
	writeErr error
	hang     bool
}

type blockingWriteCatalogStream struct {
	closed chan struct{}
	once   sync.Once
}

func (s *blockingWriteCatalogStream) WriteLine([]byte) error {
	<-s.closed
	return io.ErrClosedPipe
}
func (s *blockingWriteCatalogStream) ReadLine(context.Context) ([]byte, error) {
	return nil, errors.New("read reached after blocked write")
}
func (s *blockingWriteCatalogStream) Close() { s.once.Do(func() { close(s.closed) }) }

func newFakeCatalogLineStream(catalog mobileproto.CatalogSnapshot) *fakeCatalogLineStream {
	return &fakeCatalogLineStream{catalog: catalog, lines: make(chan []byte, 1)}
}

func (s *fakeCatalogLineStream) WriteLine(line []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	var request mobileproto.Request
	if err := json.Unmarshal(line, &request); err != nil {
		return err
	}
	if request.Type != mobileproto.RequestSessions || request.CatalogQuery == nil || request.CatalogQuery.Sort != "project" {
		return errors.New("unexpected owner request")
	}
	s.writes++
	if s.hang {
		return nil
	}
	response := mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseSessions, RequestID: request.RequestID, Catalog: &s.catalog}
	data, _ := json.Marshal(response)
	s.lines <- data
	return nil
}

func (s *fakeCatalogLineStream) ReadLine(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case line := <-s.lines:
		return line, nil
	}
}

func (s *fakeCatalogLineStream) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func TestCatalogRouterQueriesOwnersSequentiallyAndComposesOneSharedView(t *testing.T) {
	now := time.Now().UTC()
	localRaw := rawOwnerCatalog("local-owner", "same", "local")
	localRaw.ObservedAt = now.Format(time.RFC3339Nano)
	remoteRaw := rawOwnerCatalog("remote-owner", "same", "remote")
	remoteRaw.ObservedAt = now.Add(time.Second).Format(time.RFC3339Nano)
	localStream, remoteStream := newFakeCatalogLineStream(localRaw), newFakeCatalogLineStream(remoteRaw)
	directory := newFakeRouterDirectory(localStream, remoteStream)
	router, err := NewCatalogRouter(directory)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := router.Query(context.Background(), mobileproto.CatalogQuery{Sort: "project", Hosts: []string{"book"}})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Total != 1 || len(snapshot.Sections) != 1 || snapshot.Sections[0].Rows[0].OwnerHostID != "book" || snapshot.Sections[0].Rows[0].ExpectedTarget.HubID != "hub" {
		t.Fatalf("composed routed catalog = %+v", snapshot)
	}
	if localStream.writes != 1 || remoteStream.writes != 1 || !localStream.closed || !remoteStream.closed {
		t.Fatalf("owner streams local=%+v remote=%+v", localStream, remoteStream)
	}
}

func TestCatalogRouterLookupReconstructsAcrossFreshHubProcesses(t *testing.T) {
	raw := rawOwnerCatalog("remote-owner", "repo", "owner-selector")
	directory := newFakeRouterDirectory(newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local")), newFakeCatalogLineStream(raw))
	router, _ := NewCatalogRouter(directory)
	full, err := router.Query(context.Background(), mobileproto.CatalogQuery{Sort: "project", Hosts: []string{"book"}})
	if err != nil {
		t.Fatal(err)
	}
	row := full.Sections[0].Rows[0]
	// A new router and new owner process reconstruct the same public binding
	// from current stable registration and raw owner identity.
	directory = newFakeRouterDirectory(newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local")), newFakeCatalogLineStream(raw))
	fresh, _ := NewCatalogRouter(directory)
	owner, stream, binding, err := fresh.Lookup(context.Background(), row.Target, *row.ExpectedTarget)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if owner.Authority.OwnerHostID != "book" || binding.PublicSelector != row.Target || binding.PublicExpected != *row.ExpectedTarget || binding.OwnerSelector != "owner-selector" || binding.OwnerExpected.OwnerHostID != "remote-owner" {
		t.Fatalf("fresh binding = %+v owner=%+v", binding, owner.Authority)
	}
	if !owner.Capabilities.CatalogSnapshots || owner.Capabilities.HeartbeatIntervalMS == 0 || owner.Capabilities.PresenceTimeoutMS == 0 {
		t.Fatalf("lookup discarded negotiated owner capabilities: %+v", owner.Capabilities)
	}
}

func TestCatalogRouterLookupRetainsARealLocalOwnerAfterCatalogDeadlineEnds(t *testing.T) {
	now := time.Date(2026, 9, 8, 19, 0, 0, 0, time.UTC)
	resolved := mobile.ResolvedTarget{WorkspaceID: "workspace", WorkspaceKind: string(workspaceinventory.KindShell), ProjectRoot: "/repo",
		Session: "session", Pane: "%1", ServerPID: 42, SessionID: "$1", SessionCreated: "1700000000",
		DurableSessionCreated: now.Add(-time.Hour).Format(time.RFC3339Nano), Width: 80, Height: 24}
	workspace := workspaceinventory.Workspace{ID: resolved.WorkspaceID, ProjectKey: "/repo", ProjectName: "Repo", ProjectRoot: "/repo",
		Kind: workspaceinventory.KindShell, Name: "Terminal", TmuxName: resolved.Session, PaneID: resolved.Pane, Live: true,
		CreatedAt: now.Add(-time.Hour), ObservedAt: now}
	factory := func(input io.Reader, output io.Writer) (*mobile.Service, error) {
		return mobile.New(mobile.Config{Input: input, Output: output, HubID: "owner-hub", OwnerHostID: "owner-local", OwnerConfigGeneration: "owner-cfg",
			Resolver: func(context.Context, string) (mobile.ResolvedTarget, error) { return resolved, nil },
			Catalog: func(context.Context) (mobile.CatalogInput, error) {
				return mobile.CatalogInput{ObservedAt: now, Hosts: []mobileproto.CatalogHost{{ID: "owner-local", State: "online", Local: true}},
					Projects: []mobile.CatalogProject{{Label: "Repo", Result: workspaceinventory.ProjectResult{ProjectKey: "/repo", ProjectName: "Repo", Workspaces: []workspaceinventory.Workspace{workspace}}}}}, nil
			}})
	}
	endpoint := OwnerEndpoint{Host: mobileproto.CatalogHost{ID: "book", State: "online"}, Bind: func(context.Context) (BoundOwner, error) {
		return BoundOwner{Authority: CatalogAuthority{OwnerHostID: "book", RegistrationFingerprint: "registration"},
			Start:    func(ctx context.Context) (LineStream, mobileproto.Response, error) { return StartLocal(ctx, factory) },
			Validate: func(context.Context) error { return nil }}, nil
	}}
	directory := &fakeOwnerDirectory{snapshot: DirectorySnapshot{
		Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "hub-cfg"},
		Hosts:    []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}, {ID: "book", State: "online"}}, Endpoints: []OwnerEndpoint{endpoint}, Validate: func(context.Context) error { return nil },
	}}
	router, _ := NewCatalogRouter(directory)
	catalog, err := router.Query(context.Background(), mobileproto.CatalogQuery{Sort: "project"})
	if err != nil {
		t.Fatal(err)
	}
	row := catalog.Sections[0].Rows[0]
	_, stream, _, err := router.Lookup(context.Background(), row.Target, *row.ExpectedTarget)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	request, _ := json.Marshal(mobileproto.Request{Version: mobileproto.Version, Type: mobileproto.RequestStatus, RequestID: "post-lookup-status"})
	if err := stream.WriteLine(request); err != nil {
		t.Fatalf("retained local stream write: %v", err)
	}
	readCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	line, err := stream.ReadLine(readCtx)
	if err != nil {
		t.Fatalf("retained local stream read: %v", err)
	}
	var response mobileproto.Response
	if err := json.Unmarshal(line, &response); err != nil || response.Type != mobileproto.ResponseStatus || response.RequestID != "post-lookup-status" {
		t.Fatalf("post-lookup response=%+v err=%v", response, err)
	}
}

func TestCatalogRouterLookupBudgetIncludesDirectoryAndOwnerBind(t *testing.T) {
	directory := &fakeOwnerDirectory{delay: 20 * time.Millisecond, snapshot: DirectorySnapshot{
		Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "hub-cfg"},
		Hosts:    []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}, {ID: "book", State: "online"}},
		Endpoints: []OwnerEndpoint{{Host: mobileproto.CatalogHost{ID: "book", State: "online"}, Bind: func(ctx context.Context) (BoundOwner, error) {
			<-ctx.Done()
			return BoundOwner{}, ctx.Err()
		}}},
		Validate: func(context.Context) error { return nil },
	}}
	router, _ := NewCatalogRouter(directory)
	router.ownerTimeout = 100 * time.Millisecond
	router.queryTimeout = 60 * time.Millisecond
	router.finalReserve = 10 * time.Millisecond
	started := time.Now()
	_, stream, _, err := router.Lookup(context.Background(), "selector", mobileproto.TargetIdentity{HubID: "hub", OwnerHostID: "book"})
	elapsed := time.Since(started)
	if err == nil || stream != nil {
		t.Fatalf("delayed owner bind accepted stream=%v err=%v", stream, err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("lookup exceeded scaled client budget: %s", elapsed)
	}
}

func TestCatalogRouterLookupCancelsDelayedStartWithinTheOperationBudget(t *testing.T) {
	directory := &fakeOwnerDirectory{delay: 15 * time.Millisecond, snapshot: DirectorySnapshot{
		Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "hub-cfg"},
		Hosts:    []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}, {ID: "book", State: "online"}},
		Endpoints: []OwnerEndpoint{{Host: mobileproto.CatalogHost{ID: "book", State: "online"}, Bind: func(ctx context.Context) (BoundOwner, error) {
			select {
			case <-time.After(15 * time.Millisecond):
			case <-ctx.Done():
				return BoundOwner{}, ctx.Err()
			}
			return BoundOwner{Authority: CatalogAuthority{OwnerHostID: "book", RegistrationFingerprint: "registration"},
				Start: func(startCtx context.Context) (LineStream, mobileproto.Response, error) {
					<-startCtx.Done()
					return nil, mobileproto.Response{}, startCtx.Err()
				}, Validate: func(context.Context) error { return nil }}, nil
		}}},
		Validate: func(context.Context) error { return nil },
	}}
	router, _ := NewCatalogRouter(directory)
	router.ownerTimeout = 100 * time.Millisecond
	router.queryTimeout = 60 * time.Millisecond
	router.finalReserve = 10 * time.Millisecond
	started := time.Now()
	_, stream, _, err := router.Lookup(context.Background(), "selector", mobileproto.TargetIdentity{HubID: "hub", OwnerHostID: "book"})
	elapsed := time.Since(started)
	if err == nil || stream != nil {
		t.Fatalf("delayed owner start accepted stream=%v err=%v", stream, err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("delayed owner start exceeded scaled client budget: %s", elapsed)
	}
}

func TestCatalogRouterLookupClosesABlockedOwnerWriteAtTheOperationDeadline(t *testing.T) {
	blocked := &blockingWriteCatalogStream{closed: make(chan struct{})}
	directory := &fakeOwnerDirectory{delay: 10 * time.Millisecond, snapshot: DirectorySnapshot{
		Identity: mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "hub-cfg"},
		Hosts:    []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}, {ID: "book", State: "online"}},
		Endpoints: []OwnerEndpoint{{Host: mobileproto.CatalogHost{ID: "book", State: "online"}, Bind: func(context.Context) (BoundOwner, error) {
			return BoundOwner{Authority: CatalogAuthority{OwnerHostID: "book", RegistrationFingerprint: "registration"},
				Start: func(context.Context) (LineStream, mobileproto.Response, error) {
					caps := mobileproto.DefaultCapabilities()
					return blocked, mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseHello, RequestID: "hub-owner-hello", APIInstance: "owner", Capabilities: &caps}, nil
				}, Validate: func(context.Context) error { return nil }}, nil
		}}},
		Validate: func(context.Context) error { return nil },
	}}
	router, _ := NewCatalogRouter(directory)
	router.ownerTimeout = 100 * time.Millisecond
	router.queryTimeout = 60 * time.Millisecond
	router.finalReserve = 10 * time.Millisecond
	started := time.Now()
	_, stream, _, err := router.Lookup(context.Background(), "selector", mobileproto.TargetIdentity{HubID: "hub", OwnerHostID: "book"})
	elapsed := time.Since(started)
	if err == nil || stream != nil {
		t.Fatalf("blocked owner write accepted stream=%v err=%v", stream, err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("blocked owner write exceeded scaled client budget: %s", elapsed)
	}
	select {
	case <-blocked.closed:
	default:
		t.Fatal("operation deadline did not close blocked owner stream")
	}
}

func TestCatalogRouterLookupRefusesRetargetedRegistrationAndNeverFallsBack(t *testing.T) {
	raw := rawOwnerCatalog("remote-owner", "repo", "shared")
	directory := newFakeRouterDirectory(newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "repo", "shared")), newFakeCatalogLineStream(raw))
	router, _ := NewCatalogRouter(directory)
	full, err := router.Query(context.Background(), mobileproto.CatalogQuery{Sort: "project", Hosts: []string{"book"}})
	if err != nil {
		t.Fatal(err)
	}
	row := full.Sections[0].Rows[0]
	changed := newFakeRouterDirectory(newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "repo", "shared")), newFakeCatalogLineStream(raw))
	changed.snapshot.Endpoints[1] = fakeCatalogEndpoint("book", "changed-registration", newFakeCatalogLineStream(raw))
	fresh, _ := NewCatalogRouter(changed)
	if _, stream, _, err := fresh.Lookup(context.Background(), row.Target, *row.ExpectedTarget); err == nil || stream != nil {
		t.Fatalf("retargeted registration accepted stream=%v err=%v", stream, err)
	}
}

func TestCatalogRouterLookupRequiresSuccessfulFinalOwnerComposition(t *testing.T) {
	raw := rawOwnerCatalog("remote-owner", "repo", "remote")
	directory := newFakeRouterDirectory(newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local")), newFakeCatalogLineStream(raw))
	router, _ := NewCatalogRouter(directory)
	full, err := router.Query(context.Background(), mobileproto.CatalogQuery{Hosts: []string{"book"}})
	if err != nil {
		t.Fatal(err)
	}
	row := full.Sections[0].Rows[0]
	raw.Sections[0].Rows[0].Stale = true
	changed := newFakeRouterDirectory(newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local")), newFakeCatalogLineStream(raw))
	fresh, _ := NewCatalogRouter(changed)
	if _, stream, _, err := fresh.Lookup(context.Background(), row.Target, *row.ExpectedTarget); err == nil || stream != nil {
		t.Fatalf("contradictory owner row accepted stream=%v err=%v", stream, err)
	}
}

func TestCatalogRouterDiscardsCatalogWhenDirectoryChangesAfterOwnerReply(t *testing.T) {
	directory := newFakeRouterDirectory(newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local")), newFakeCatalogLineStream(rawOwnerCatalog("remote-owner", "repo", "remote")))
	directory.snapshot.Validate = func(context.Context) error { return errors.New("hub config changed") }
	router, _ := NewCatalogRouter(directory)
	if _, err := router.Query(context.Background(), mobileproto.CatalogQuery{}); err == nil || err.Error() != "hub config changed" {
		t.Fatalf("directory change error = %v", err)
	}
}

func TestCatalogRouterKeepsSuccessfulRowsWhenAnotherOwnerTimesOut(t *testing.T) {
	local := newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local"))
	hung := newFakeCatalogLineStream(rawOwnerCatalog("remote-owner", "repo", "remote"))
	hung.hang = true
	directory := newFakeRouterDirectory(local, hung)
	router, _ := NewCatalogRouter(directory)
	router.ownerTimeout = 10 * time.Millisecond
	router.queryTimeout = 50 * time.Millisecond
	router.finalReserve = 5 * time.Millisecond
	snapshot, err := router.Query(context.Background(), mobileproto.CatalogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Total != 1 || snapshot.Sections[0].Rows[0].OwnerHostID != "local:hub" || len(snapshot.Failures) != 1 || snapshot.Failures[0].ID != "book" {
		t.Fatalf("partial bounded catalog = %+v", snapshot)
	}
}

func TestCatalogRouterOverallBudgetIncludesDirectoryAndLeavesFinalValidationTime(t *testing.T) {
	if CatalogQueryTimeout >= 15*time.Second {
		t.Fatalf("production catalog budget %s must remain below the native request deadline", CatalogQueryTimeout)
	}
	local := newFakeCatalogLineStream(rawOwnerCatalog("local-owner", "local", "local"))
	hung := newFakeCatalogLineStream(rawOwnerCatalog("remote-owner", "repo", "remote"))
	hung.hang = true
	directory := newFakeRouterDirectory(local, hung)
	directory.delay = 20 * time.Millisecond
	router, _ := NewCatalogRouter(directory)
	router.ownerTimeout = 100 * time.Millisecond
	router.queryTimeout = 60 * time.Millisecond
	router.finalReserve = 10 * time.Millisecond
	started := time.Now()
	snapshot, err := router.Query(context.Background(), mobileproto.CatalogQuery{})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("catalog exceeded scaled client budget: %s", elapsed)
	}
	if snapshot.Total != 1 || len(snapshot.Failures) != 1 || snapshot.Failures[0].ID != "book" {
		t.Fatalf("bounded partial catalog = %+v", snapshot)
	}
}

func TestCatalogRouterBoundsSourcesBeforeRetainingTheNextOwner(t *testing.T) {
	first := rawOwnerCatalog("local-owner", "local", "local")
	first.Sections[0].Rows = repeatedRawRows(first.Sections[0].Rows[0], mobileproto.MaxCatalogRows/2+1)
	first.Total = len(first.Sections[0].Rows)
	second := rawOwnerCatalog("remote-owner", "remote", "remote")
	second.Sections[0].Rows = repeatedRawRows(second.Sections[0].Rows[0], mobileproto.MaxCatalogRows/2+1)
	second.Total = len(second.Sections[0].Rows)
	directory := newFakeRouterDirectory(newFakeCatalogLineStream(first), newFakeCatalogLineStream(second))
	router, _ := NewCatalogRouter(directory)
	if snapshot, err := router.Query(context.Background(), mobileproto.CatalogQuery{}); err == nil {
		t.Fatalf("oversized aggregate retained: %+v", snapshot)
	}
}

func newFakeRouterDirectory(local, remote *fakeCatalogLineStream) *fakeOwnerDirectory {
	return &fakeOwnerDirectory{snapshot: DirectorySnapshot{
		Identity:  mobile.CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "hub-cfg"},
		Hosts:     []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}, {ID: "book", State: "online"}},
		Endpoints: []OwnerEndpoint{fakeCatalogEndpoint("local:hub", "local-registration", local), fakeCatalogEndpoint("book", "book-registration", remote)},
		Validate:  func(context.Context) error { return nil },
	}}
}

func fakeCatalogEndpoint(host, registration string, stream *fakeCatalogLineStream) OwnerEndpoint {
	return OwnerEndpoint{Host: mobileproto.CatalogHost{ID: host, State: "online", Local: host == "local:hub"}, Bind: func(context.Context) (BoundOwner, error) {
		return BoundOwner{Authority: CatalogAuthority{OwnerHostID: host, RegistrationFingerprint: registration},
			Start: func(context.Context) (LineStream, mobileproto.Response, error) {
				caps := mobileproto.DefaultCapabilities()
				return stream, mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseHello, Capabilities: &caps}, nil
			}, Validate: func(context.Context) error { return nil }}, nil
	}}
}

func repeatedRawRows(template mobileproto.CatalogRow, count int) []mobileproto.CatalogRow {
	rows := make([]mobileproto.CatalogRow, count)
	for i := range rows {
		row := template
		row.ID = fmt.Sprintf("%s-%d", template.ID, i)
		row.WorkspaceID = fmt.Sprintf("%s-%d", template.WorkspaceID, i)
		row.Target = fmt.Sprintf("%s-%d", template.Target, i)
		expected := *template.ExpectedTarget
		expected.WorkspaceID = row.WorkspaceID
		expected.TargetGeneration = fmt.Sprintf("%s-%d", expected.TargetGeneration, i)
		row.ExpectedTarget = &expected
		rows[i] = row
	}
	return rows
}
