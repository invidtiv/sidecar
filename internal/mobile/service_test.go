package mobile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func testTarget() targetState {
	resolved := ResolvedTarget{WorkspaceID: "demo", WorkspaceKind: "shell", Session: "mobile", Pane: "%7",
		ServerPID: 42, SessionID: "$3", SessionCreated: "1700000000", DurableSessionCreated: "2026-09-07T00:00:00Z", Width: 4, Height: 2}
	return targetState{resolved: resolved, wire: mobileproto.Target{Handle: "target_one", Session: resolved.Session, Pane: resolved.Pane}}
}

func testService(output *bytes.Buffer) *Service {
	target := testTarget()
	return &Service{
		out: &safeEncoder{enc: json.NewEncoder(output)}, targets: map[string]targetState{target.wire.Handle: target},
		attachments: map[string]*attachment{}, seenRequests: map[string]struct{}{},
		resolve: func(context.Context, string) (ResolvedTarget, error) { return target.resolved, nil },
	}
}

func decodeResponses(t *testing.T, output *bytes.Buffer) []mobileproto.Response {
	t.Helper()
	decoder := json.NewDecoder(output)
	var responses []mobileproto.Response
	for {
		var response mobileproto.Response
		if err := decoder.Decode(&response); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			break
		}
		responses = append(responses, response)
	}
	return responses
}

func TestDecodeRequestRejectsUnknownAndTrailingJSON(t *testing.T) {
	for _, line := range []string{
		`{"type":"hello","request_id":"one"}`,
		`{"version":0,"type":"hello","request_id":"one","mystery":true}`,
		`{"version":0,"type":"hello","request_id":"one"} {"second":true}`,
	} {
		if _, err := decodeRequest([]byte(line)); err == nil {
			t.Fatalf("decodeRequest(%q) accepted malformed envelope", line)
		}
	}
}

func TestSessionsUsesTheServiceCatalogAndCorrelatesResponse(t *testing.T) {
	now := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	s := testService(&output)
	s.instance, s.hubID, s.ownerHostID, s.configGeneration = "api", "hub", "local:test", "config"
	s.resolve = func(context.Context, string) (ResolvedTarget, error) {
		return ResolvedTarget{WorkspaceID: "demo", WorkspaceKind: "shell", ProjectRoot: "demo", Session: "mobile", Pane: "%7", ServerPID: 42, SessionID: "$3", SessionCreated: "1700000000", DurableSessionCreated: now.Add(-time.Hour).Format(time.RFC3339Nano), Width: 80, Height: 24}, nil
	}
	workspace := catalogShell("demo:shell:mobile", "Shell", "mobile", "%7", now)
	workspace.ProjectKey = "demo"
	s.catalog = func(context.Context) (CatalogInput, error) {
		return CatalogInput{ObservedAt: now, Hosts: []mobileproto.CatalogHost{{ID: "local:test", Name: "test", State: "online", Local: true}}, Projects: []CatalogProject{{Label: "Repo", Result: workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{workspace}}}}}, nil
	}
	s.sessions(context.Background(), mobileproto.Request{RequestID: "sessions", CatalogQuery: &mobileproto.CatalogQuery{Sort: "name"}})
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Type != mobileproto.ResponseSessions || responses[0].RequestID != "sessions" || responses[0].APIInstance != "api" || responses[0].Catalog == nil {
		t.Fatalf("responses = %+v", responses)
	}
	if responses[0].Catalog.Total != 1 || responses[0].Catalog.Sections[0].Rows[0].Target != "mobile" || responses[0].Catalog.Sections[0].Rows[0].ExpectedTarget == nil {
		t.Fatalf("catalog = %+v", responses[0].Catalog)
	}
}

func TestResolveRefusesCatalogIdentityChangedAfterListing(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	s.hubID, s.ownerHostID, s.configGeneration = "hub", "local:test", "config"
	expected := targetIdentity(CatalogIdentity{HubID: "hub", OwnerHostID: "local:test", OwnerConfigGeneration: "config"}, testTarget().resolved)
	expected.TargetGeneration = "stale-catalog-generation"
	before := len(s.targets)
	s.resolveTarget(context.Background(), mobileproto.Request{RequestID: "resolve-stale", Target: "mobile", ExpectedTarget: &expected})
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Error == nil || responses[0].Error.Code != mobileproto.ErrorIdentityChanged {
		t.Fatalf("responses = %+v", responses)
	}
	if len(s.targets) != before {
		t.Fatal("stale catalog selection created a target handle")
	}
}

func TestSessionsRefusesToBlockAnActiveTerminalStream(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	s.attachments["active"] = &attachment{handle: "active"}
	s.sessions(context.Background(), mobileproto.Request{RequestID: "sessions-active"})
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Error == nil || responses[0].Error.Code != mobileproto.ErrorUnsupported || !responses[0].Error.Retry {
		t.Fatalf("responses = %+v", responses)
	}
}

func TestRequestIDDedupIsBoundedAndRolling(t *testing.T) {
	s := testService(&bytes.Buffer{})
	for i := 0; i <= recentRequestIDs; i++ {
		if err := s.validateRequestID(string(rune(0x1000 + i))); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.seenRequests) != recentRequestIDs || len(s.requestOrder) != recentRequestIDs {
		t.Fatalf("dedup bounds = %d/%d", len(s.seenRequests), len(s.requestOrder))
	}
	if err := s.validateRequestID(string(rune(0x1000))); err != nil {
		t.Fatalf("evicted request id remained permanently exhausted: %v", err)
	}
	if err := s.validateRequestID(string(rune(0x1100))); err == nil {
		t.Fatal("recent duplicate request id was accepted")
	}
}

func TestAttachmentOwnerDoesNotAliasCallerSuppliedID(t *testing.T) {
	one := mobileOwnerID("attachment_one", 1, 1)
	two := mobileOwnerID("attachment_two", 1, 1)
	if one == two {
		t.Fatalf("opaque attachment handles aliased owner %q", one)
	}
	if one == mobileOwnerID("attachment_one", 1, 2) {
		t.Fatal("reacquired control reused a prior owner epoch")
	}
}

func TestOperationSequenceAndResetGenerationFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  mobileproto.Request
	}{
		{name: "sequence gap", req: mobileproto.Request{AttachmentHandle: "attachment", RequestID: "gap", OperationSequence: 4, LastResetGeneration: 3, LastOutputSequence: 2}},
		{name: "stale reset", req: mobileproto.Request{AttachmentHandle: "attachment", RequestID: "reset", OperationSequence: 3, LastResetGeneration: 2, LastOutputSequence: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			s := testService(&output)
			a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), control: true, operationSequence: 2, resetGeneration: 3, outputSequence: 2, firstOutputForReset: 2}
			s.attachments[a.handle] = a
			if got, ok := s.operationAttachment(context.Background(), tc.req, false); ok || got != nil {
				t.Fatal("invalid operation crossed the mutation boundary")
			}
			if a.control {
				t.Fatal("invalid operation retained control")
			}
			responses := decodeResponses(t, &output)
			if len(responses) != 1 || responses[0].Error == nil || responses[0].Error.Code != mobileproto.ErrorOperationOrder {
				t.Fatalf("responses = %#v", responses)
			}
		})
	}
}

func TestOperationRequiresAFrameEmittedForCurrentReset(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), control: true,
		operationSequence: 1, resetGeneration: 2, outputSequence: 7, firstOutputForReset: 0}
	s.attachments[a.handle] = a
	request := mobileproto.Request{AttachmentHandle: a.handle, RequestID: "before-frame", OperationSequence: 2,
		LastResetGeneration: 2, LastOutputSequence: 7}
	if got, ok := s.operationAttachment(context.Background(), request, false); ok || got != nil {
		t.Fatal("operation was accepted before a replacement frame")
	}
	if a.control {
		t.Fatal("pre-frame operation retained control")
	}
}

func TestRevalidationRejectsChangedDurableShellRecord(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	target := testTarget()
	s.resolve = func(context.Context, string) (ResolvedTarget, error) {
		changed := target.resolved
		changed.DurableSessionCreated = "2026-09-08T00:00:00Z"
		return changed, nil
	}
	if err := s.revalidate(context.Background(), target); err == nil {
		t.Fatal("unchanged tmux process with replaced durable shell record was accepted")
	}
}

func TestTargetGenerationIsStableAcrossAPIProcesses(t *testing.T) {
	target := testTarget().resolved
	one := testService(&bytes.Buffer{})
	two := testService(&bytes.Buffer{})
	one.instance, two.instance = "api_one", "api_two"
	one.hubID, two.hubID = "aerie", "aerie"
	first := one.wireTarget("handle_one", target)
	second := two.wireTarget("handle_two", target)
	if first.HubInstance == second.HubInstance || first.Handle == second.Handle {
		t.Fatal("test did not vary process-scoped identity")
	}
	if first.TargetGeneration != second.TargetGeneration || first.Identity() != second.Identity() {
		t.Fatalf("stable reconnect identity changed across API processes:\n%+v\n%+v", first, second)
	}
}

func TestSnapshotOfferNeverBlocksAndCoalescesNewestCompleteFrame(t *testing.T) {
	a := &attachment{snapshots: make(chan queuedSnapshot, 1), resetGeneration: 1}
	a.snapshots <- queuedSnapshot{snapshot: tty.ControlSnapshot{Output: "old"}, resetGeneration: 1}
	done := make(chan struct{})
	go func() { a.offerSnapshot(tty.ControlSnapshot{Output: "new"}); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("capture callback blocked")
	}
	queued := <-a.snapshots
	if queued.discontinuity != "" || queued.snapshot.Output != "new" {
		t.Fatalf("ordinary coalescing = %+v, want newest frame without discontinuity", queued)
	}
}

func TestSnapshotOfferReplacesPreResetCaptureWithoutOverflow(t *testing.T) {
	a := &attachment{snapshots: make(chan queuedSnapshot, 1), resetGeneration: 1}
	a.offerSnapshot(tty.ControlSnapshot{Output: "stale"})
	a.mu.Lock()
	a.resetGeneration = 2
	a.mu.Unlock()
	a.offerSnapshot(tty.ControlSnapshot{Output: "fresh"})
	queued := <-a.snapshots
	if queued.discontinuity != "" || queued.resetGeneration != 2 || queued.snapshot.Output != "fresh" {
		t.Fatalf("replacement = %+v", queued)
	}
}

func TestSkippedGeometryOrAlternateScreenDiscontinuityStillResets(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		dropped      tty.ControlSnapshot
	}{
		{name: "geometry", reason: "geometry_changed", dropped: testSnapshot("B    \n     ", false, 5, 2)},
		{name: "alternate", reason: "alternate_screen", dropped: testSnapshot("B   \n    ", true, 4, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			s := testService(&output)
			baseline := testSnapshot("A   \n    ", false, 4, 2)
			a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 1, control: true,
				latest: baseline, snapshots: make(chan queuedSnapshot, 1)}
			a.offerSnapshot(tc.dropped)
			a.offerSnapshot(testSnapshot("C   \n    ", false, 4, 2))
			queued := <-a.snapshots
			if queued.discontinuity != tc.reason {
				t.Fatalf("skipped discontinuity = %q, want %q", queued.discontinuity, tc.reason)
			}
			a.publishQueued(queued)
			responses := decodeResponses(t, &output)
			if len(responses) != 2 || responses[0].Type != mobileproto.ResponseReset || responses[0].Reason != tc.reason ||
				responses[1].Type != mobileproto.ResponseFrame || responses[1].ResetGeneration != 2 {
				t.Fatalf("responses = %#v", responses)
			}
			if a.control {
				t.Fatal("skipped discontinuity retained mutation authority")
			}
		})
	}
}

func TestSkippedIdentityDiscontinuityStillFailsClosed(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	baseline := testSnapshot("A   \n    ", false, 4, 2)
	changed := baseline
	changed.ServerPID++
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 1, control: true,
		latest: baseline, snapshots: make(chan queuedSnapshot, 1)}
	a.offerSnapshot(testSnapshot("B    \n     ", false, 5, 2))
	a.offerSnapshot(changed)
	a.offerSnapshot(testSnapshot("C   \n    ", false, 4, 2))
	queued := <-a.snapshots
	if queued.discontinuity != "identity_changed" {
		t.Fatalf("skipped discontinuity = %q, want identity_changed", queued.discontinuity)
	}
	a.publishQueued(queued)
	responses := decodeResponses(t, &output)
	if len(responses) != 2 || responses[0].Type != mobileproto.ResponseReset || responses[0].Reason != "identity_changed" ||
		responses[1].Type != mobileproto.ResponseError || responses[1].Error == nil || responses[1].Error.Code != mobileproto.ErrorIdentityChanged {
		t.Fatalf("responses = %#v", responses)
	}
	if a.control {
		t.Fatal("skipped identity discontinuity retained mutation authority")
	}
}

func TestExpectedResizeCoalescesToNewestMatchingFrame(t *testing.T) {
	for _, tc := range []struct {
		name          string
		columns, rows int
	}{
		{name: "changed size", columns: 5, rows: 3},
		{name: "same size", columns: 4, rows: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			s := testService(&output)
			baseline := testSnapshot("A   \n    ", false, 4, 2)
			a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 2,
				latest: baseline, snapshots: make(chan queuedSnapshot, 1), expectedColumns: tc.columns, expectedRows: tc.rows}
			a.offerSnapshot(baseline)
			content := strings.Repeat("B", tc.columns)
			lines := make([]string, tc.rows)
			for index := range lines {
				lines[index] = content
			}
			a.offerSnapshot(testSnapshot(strings.Join(lines, "\n"), false, tc.columns, tc.rows))
			queued := <-a.snapshots
			if queued.discontinuity != "" {
				t.Fatalf("expected resize marked %q", queued.discontinuity)
			}
			a.publishQueued(queued)
			responses := decodeResponses(t, &output)
			if len(responses) != 1 || responses[0].Type != mobileproto.ResponseFrame || responses[0].ResetGeneration != 2 ||
				responses[0].Geometry == nil || responses[0].Geometry.Columns != tc.columns || responses[0].Geometry.Rows != tc.rows {
				t.Fatalf("responses = %#v", responses)
			}
			if a.expectedColumns != 0 || a.expectedRows != 0 {
				t.Fatalf("accepted geometry remained pending: %dx%d", a.expectedColumns, a.expectedRows)
			}
		})
	}
}

func TestExpectedResizeFrameSupersededByWrongGeometryStillResets(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	baseline := testSnapshot("A   \n    ", false, 4, 2)
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 2, control: true,
		latest: baseline, snapshots: make(chan queuedSnapshot, 1), expectedColumns: 5, expectedRows: 3}
	a.offerSnapshot(testSnapshot("BBBBB\nBBBBB\nBBBBB", false, 5, 3))
	a.offerSnapshot(testSnapshot("C   \n    ", false, 4, 2))
	queued := <-a.snapshots
	if queued.discontinuity != "geometry_changed" {
		t.Fatalf("superseded expected geometry marked %q", queued.discontinuity)
	}
	a.publishQueued(queued)
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Type != mobileproto.ResponseReset || responses[0].Reason != "geometry_changed" || responses[0].ResetGeneration != 3 {
		t.Fatalf("responses = %#v", responses)
	}
	if a.control || a.expectedColumns != 0 || a.expectedRows != 0 || a.outputSequence != 0 {
		t.Fatalf("superseded geometry retained authority: control=%t expected=%dx%d output=%d", a.control, a.expectedColumns, a.expectedRows, a.outputSequence)
	}
}

func TestFullCaptureCoalescingBehindOperationDoesNotRevokeControl(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	a := newAttachment(s, "attachment", "client", 1, testTarget())
	a.mu.Lock()
	a.control = true
	a.latest = testSnapshot("0   \n    ", false, 4, 2)
	a.mu.Unlock()
	a.opMu.Lock() // Heartbeat holds this gate until its tmux acknowledgment.
	close(a.ready)
	a.offerSnapshot(testSnapshot("A   \n    ", false, 4, 2))
	deadline := time.Now().Add(time.Second)
	for len(a.snapshots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(a.snapshots) != 0 {
		a.opMu.Unlock()
		a.stopAttachment()
		t.Fatal("publisher did not reach operation gate")
	}
	a.offerSnapshot(testSnapshot("B   \n    ", false, 4, 2))
	a.offerSnapshot(testSnapshot("C   \n    ", false, 4, 2))
	a.opMu.Unlock()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		count := a.outputSequence
		a.mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	a.mu.Lock()
	controlled, sequence, reset := a.control, a.outputSequence, a.resetGeneration
	a.mu.Unlock()
	a.stopAttachment()
	responses := decodeResponses(t, &output)
	if !controlled || sequence != 2 || reset != 1 {
		t.Fatalf("ordinary full-capture coalescing changed authority: control=%t sequence=%d reset=%d responses=%+v", controlled, sequence, reset, responses)
	}
	for _, response := range responses {
		if response.Type == mobileproto.ResponseReset {
			t.Fatalf("ordinary full captures emitted reset: %+v", response)
		}
	}
}

func TestUnexpectedGeometryOrAlternateScreenTransitionResets(t *testing.T) {
	for _, tc := range []struct {
		name string
		next tty.ControlSnapshot
	}{
		{name: "geometry", next: testSnapshot("A    \n     ", false, 5, 2)},
		{name: "alternate", next: testSnapshot("A   \n    ", true, 4, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			s := testService(&output)
			a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 1, control: true,
				latest: testSnapshot("A   \n    ", false, 4, 2)}
			a.publish(tc.next)
			responses := decodeResponses(t, &output)
			if len(responses) != 2 || responses[0].Type != mobileproto.ResponseReset || responses[1].ResetGeneration != 2 {
				t.Fatalf("responses = %#v", responses)
			}
			if a.control {
				t.Fatal("unannounced terminal reset retained control")
			}
		})
	}
}

func TestExpectedResizeDropsStalePreResizeCapture(t *testing.T) {
	var output bytes.Buffer
	s := testService(&output)
	old := testSnapshot("A   \n    ", false, 4, 2)
	a := &attachment{service: s, handle: "attachment", generation: 1, target: testTarget(), resetGeneration: 1,
		latest: old, snapshots: make(chan queuedSnapshot, 1)}

	a.offerSnapshot(old)
	a.mu.Lock()
	a.resetGeneration = 2
	a.expectedColumns, a.expectedRows = 4, 2
	a.mu.Unlock()
	stale := <-a.snapshots
	a.publishObserved(stale.snapshot, stale.resetGeneration)
	if output.Len() != 0 || a.firstOutputForReset != 0 {
		t.Fatalf("stale capture published into reset: output=%q first=%d", output.String(), a.firstOutputForReset)
	}
	a.offerSnapshot(testSnapshot("B   \n    ", false, 4, 2))
	fresh := <-a.snapshots
	a.publishObserved(fresh.snapshot, fresh.resetGeneration)
	responses := decodeResponses(t, &output)
	if len(responses) != 1 || responses[0].Type != mobileproto.ResponseFrame || responses[0].ResetGeneration != 2 ||
		responses[0].Geometry == nil || responses[0].Geometry.Columns != 4 || a.firstOutputForReset == 0 {
		t.Fatalf("replacement responses = %#v, first=%d", responses, a.firstOutputForReset)
	}
}

func testSnapshot(output string, alternate bool, width, height int) tty.ControlSnapshot {
	return tty.ControlSnapshot{Session: "mobile", Pane: "%7", Output: output, PaneRows: height, PaneWidth: width, PaneHeight: height,
		CursorVisible: true, Autowrap: true, InputModesKnown: true, AltScreen: alternate,
		ServerPID: 42, SessionID: "$3", SessionCreated: "1700000000"}
}

func TestInputModeRefusalIsSpecificToRequiredModes(t *testing.T) {
	snapshot := testSnapshot(strings.Repeat(" ", 4)+"\n"+strings.Repeat(" ", 4), false, 4, 2)
	if snapshot.InputModesKnown != true || snapshot.CursorShape != "" {
		t.Fatal("test setup invalid")
	}
	_, modes, err := normalizedFullFrame(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !modes.InputKnown || modes.CursorShape != "block" {
		t.Fatalf("cosmetic cursor gap changed input capability: %+v", modes)
	}
}

type blockingWriter struct{ started chan struct{} }

func (w *blockingWriter) Write([]byte) (int, error) {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
	select {}
}

func TestOutboundOverflowIsTerminalAndRevokesAttachments(t *testing.T) {
	w := &blockingWriter{started: make(chan struct{})}
	s, err := New(Config{Input: strings.NewReader(""), Output: w, Resolver: func(context.Context, string) (ResolvedTarget, error) { return ResolvedTarget{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	a := newAttachment(s, "attachment", "phone", 1, testTarget())
	close(a.ready)
	a.mu.Lock()
	a.control = true
	a.mu.Unlock()
	s.attachments[a.handle] = a
	if !s.emit(mobileproto.Response{Version: 0, Type: "blocked"}) {
		t.Fatal("first response did not enter writer")
	}
	<-w.started
	for i := 0; i < mobileproto.OutboundQueueDepth; i++ {
		if !s.emit(mobileproto.Response{Version: 0, Type: "queued"}) {
			t.Fatalf("queue filled early at %d", i)
		}
	}
	if s.emit(mobileproto.Response{Version: 0, Type: "overflow"}) {
		t.Fatal("overflow response was accepted")
	}
	select {
	case <-s.terminal:
	case <-time.After(time.Second):
		t.Fatal("outbound overflow did not terminate service")
	}
	deadline := time.Now().Add(time.Second)
	for {
		a.mu.Lock()
		controlled := a.control
		a.mu.Unlock()
		if !controlled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("outbound overflow retained attachment control")
		}
		time.Sleep(time.Millisecond)
	}
}
