package uiapi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/viewerlayout"
)

func attachment(session, pane, selector string) *state.PaneAttachmentJSON {
	return &state.PaneAttachmentJSON{Selector: selector, ExpectedTarget: mobileproto.TargetIdentity{HubID: "hub", OwnerHostID: "host", OwnerConfigGeneration: "config", WorkspaceID: "workspace", WorkspaceKind: "shell", Session: session, Pane: pane, ServerIncarnation: "server", TargetGeneration: "target"}}
}
func attachmentTree() *state.PaneLayoutJSON {
	return &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{Axis: "cols", Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: "terminal", Session: "sidecar-sh-content-1", Attachment: attachment("sidecar-sh-content-1", "%1", "opaque-primary")},
		B: &state.PaneLayoutJSON{Kind: "shell", Session: "sidecar-sh-content-1", Attachment: attachment("sidecar-sh-content-1", "%2", "opaque-shell")}}}
}

func TestViewerAttachmentSessionFirstAndAmbiguity(t *testing.T) {
	cases := []struct {
		name, session, pane string
		mutate              func(*state.PaneLayoutJSON)
		want                int
		refusal             string
	}{
		{name: "primary candidate", session: "sidecar-sh-content-1", pane: "%1", want: 2},
		{name: "shell candidate", session: "sidecar-sh-content-1", pane: "%2", want: 3},
		{name: "no pane", session: "sidecar-sh-content-1", refusal: "ambiguous"},
		{name: "unknown pane", session: "sidecar-sh-content-1", pane: "%3", refusal: "ambiguous"},
		{name: "duplicate identity", session: "sidecar-sh-content-1", pane: "%1", mutate: func(j *state.PaneLayoutJSON) { j.Split.B.Attachment.ExpectedTarget.Pane = "%1" }, refusal: "ambiguous"},
		{name: "no hints", session: "sidecar-sh-content-1", pane: "%1", mutate: func(j *state.PaneLayoutJSON) { j.Split.A.Attachment = nil; j.Split.B.Attachment = nil }, refusal: "ambiguous"},
		{name: "hint grants no session authority", session: "forged", pane: "%1", mutate: func(j *state.PaneLayoutJSON) { j.Split.A.Attachment.ExpectedTarget.Session = "forged" }, refusal: "not on screen"},
		{name: "hint session mismatch", session: "sidecar-sh-content-1", pane: "%1", mutate: func(j *state.PaneLayoutJSON) { j.Split.A.Attachment.ExpectedTarget.Session = "forged" }, refusal: "ambiguous"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := attachmentTree()
			if tc.mutate != nil {
				tc.mutate(j)
			}
			host := &viewerLayoutHost{}
			host.restore(j)
			got, err := host.originLeaf(uirequest.Origin{TmuxSession: tc.session, TmuxPane: tc.pane})
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("got %d %v", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %d %v want %d", got, err, tc.want)
			}
		})
	}
	host := &viewerLayoutHost{}
	host.restore(&state.PaneLayoutJSON{Kind: "terminal", Session: "legacy"})
	if got, err := host.originLeaf(uirequest.Origin{TmuxSession: "legacy"}); err != nil || got != 1 {
		t.Fatalf("legacy: %d %v", got, err)
	}
}

func TestViewerAttachmentHTTPAndSpecCarry(t *testing.T) {
	h, root := viewerHarness(t)
	path := "/api/v0/projects/content/layout"
	doc := LayoutDocument{Layout: attachmentTree()}
	_, etag := layoutRead(t, h, path)
	raw, _ := json.Marshal(doc)
	r, b := h.localDo(req{method: "PUT", path: path, body: string(raw), header: map[string]string{"If-Match": etag}})
	expect(t, r, b, 200, "")
	got, etag := layoutRead(t, h, path)
	if !reflect.DeepEqual(got, doc) {
		t.Fatalf("GET changed attachment: %+v", got)
	}
	r, b = h.localDo(req{method: "HEAD", path: path})
	expect(t, r, b, 200, "")
	if len(b) != 0 || r.Header.Get("ETag") != etag {
		t.Fatal("HEAD lost layout revision/body semantics")
	}
	v := &apiScreen{id: "test", caller: caller{client: "local"}, ws: contentservice.Workspace{Root: root}, presence: ViewerPresenceRequest{Project: "content", Session: "sidecar-sh-content-1", Viewport: Viewport{Width: 1200, Height: 800}}}
	payload, _ := json.Marshal(uirequest.LayoutPayload{Mode: "apply", Columns: json.RawMessage(`[{"panes":[{"kind":"shell","session":"sidecar-sh-content-1"}]},{"panes":[{"kind":"primary"}]}]`)})
	request := uirequest.Request{ID: "test", CreatedAt: time.Now(), Action: uirequest.ActionLayout, Origin: uirequest.Origin{TmuxSession: "sidecar-sh-content-1", TmuxPane: "%2"}, Payload: payload}
	plan, err := h.s.planViewerRequest(context.Background(), v, request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.event.OriginPane != 3 {
		t.Fatalf("origin pane = %d", plan.event.OriginPane)
	}
	leaves := savedLeaves(plan.event.Document.Layout)
	if leaves[0].Kind != "shell" || !reflect.DeepEqual(leaves[0].Attachment, doc.Layout.Split.B.Attachment) || !reflect.DeepEqual(leaves[1].Attachment, doc.Layout.Split.A.Attachment) {
		t.Fatalf("spec lost carried attachments: %+v", leaves)
	}
	host := &viewerLayoutHost{v: v}
	host.restore(plan.event.Document.Layout)
	var report layoutreport.Report
	if err := json.Unmarshal(host.report(), &report); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Grid.Columns[0].Panes[0].Attachment, leaves[0].Attachment) || !reflect.DeepEqual(report.Grid.Columns[1].Panes[0].Attachment, leaves[1].Attachment) {
		t.Fatal("CLI grid report lost attachments")
	}
	// Persisted proposals use the same conditional store as HTTP acknowledgements.
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	if _, _, err := store.Put("local", root, etag, plan.event.Document); err != nil {
		t.Fatal(err)
	}
	got, _ = layoutRead(t, h, path)
	if !reflect.DeepEqual(got, plan.event.Document) {
		t.Fatal("saved proposal changed attachments")
	}
}

func TestViewerAttachmentRejectsMalformedUTF8(t *testing.T) {
	h, _ := viewerHarness(t)
	path := "/api/v0/projects/content/layout"
	_, etag := layoutRead(t, h, path)
	raw, _ := json.Marshal(LayoutDocument{Layout: attachmentTree()})
	// encoding/json otherwise silently repairs this byte to U+FFFD before
	// attachment validation, changing the opaque selector rather than refusing.
	body := strings.Replace(string(raw), "opaque-primary", "opaque-\xff", 1)
	r, b := h.localDo(req{method: "PUT", path: path, body: body, header: map[string]string{"If-Match": etag}})
	expect(t, r, b, 400, CodeInvalidRequest)
	doc, gotETag := layoutRead(t, h, path)
	if doc.Layout != nil || gotETag != etag {
		t.Fatal("malformed attachment changed layout")
	}
}
