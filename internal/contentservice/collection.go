package contentservice

import (
	"context"

	"github.com/marcus/sidecar/internal/pluginhost"
	"github.com/marcus/sidecar/internal/resource"
)

// The two plugin-collection read operations, beside OpResource.
//
// They exist for one reason: a Resource leaf bound to a remote host must ask
// THAT host's plugins, not this machine's. `resolve` already works that way —
// the viewer runs `sidecar content read --kind resource` over the connection
// Sidecar holds open — and a collection tab that quietly listed the local
// machine's data while the pane said it was showing a remote workspace would be
// answering a question nobody asked.
const (
	// OpCollection lists one collection of one plugin.
	OpCollection = "collection"
	// OpItem expands one row of one collection into a document.
	OpItem = "item"
)

// WirePage is one list answer on the wire. It is the host-kept page — already
// sanitized, already bounded — never the plugin's raw stdout.
type WirePage struct {
	Outcome    string          `json:"outcome,omitempty"`
	Items      []WireItem      `json:"items,omitempty"`
	NextCursor string          `json:"nextCursor,omitempty"`
	Total      int             `json:"total,omitempty"`
	Notices    []WirePageNotic `json:"notices,omitempty"`
	Truncated  bool            `json:"truncated,omitempty"`
}

// WireItem is one row.
type WireItem struct {
	ID        string            `json:"id"`
	Cells     map[string]string `json:"cells,omitempty"`
	Status    *WireStatus       `json:"status,omitempty"`
	SourceURL string            `json:"sourceUrl,omitempty"`
}

// WireStatus is a row's optional pill.
type WireStatus struct {
	Label string `json:"label"`
	Tone  string `json:"tone,omitempty"`
}

// WirePageNotic is one notice row. The short name keeps the wire object's Go
// type beside the others in this file rather than in a package of its own.
type WirePageNotic struct {
	Tone string `json:"tone,omitempty"`
	Text string `json:"text"`
}

// CollectionParams is what a list call carries over the wire.
type CollectionParams struct {
	Collection string
	Query      string
	View       string
	Sort       string
	SortDir    string
	Cursor     string
	Limit      int
}

// ListCollection invokes the host manager's list and returns a wire page, or a
// typed resource error envelope. Credentials, commands and unsanitized cells
// never enter the result: the page has already been through the same bounds a
// pane would apply, which is what makes this safe to hand across a connection.
func (s *Service) ListCollection(ctx context.Context, workspaceID, instance string, params CollectionParams) (ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	ref := resource.Reference{Instance: instance, Collection: params.Collection, Query: params.Query,
		View: params.View, Sort: params.Sort}
	if !ref.Valid() {
		return ReadResult{}, Rejected("collection reference is empty or exceeds its bounds")
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ReadResult{}, err
	}
	mgr, err := s.resourceManager()
	if err != nil {
		return ReadResult{}, err
	}
	page, listErr := mgr.List(ctx, pluginhost.ListRequest{
		Instance: instance,
		Params: pluginhost.ListParams{
			Collection: params.Collection,
			Query:      params.Query,
			View:       params.View,
			Sort:       pluginhost.SortOrder{Key: params.Sort, Dir: params.SortDir},
			Cursor:     params.Cursor,
			Limit:      params.Limit,
		},
	})
	if listErr != nil {
		return collectionErrorResult(ws.ID, OpCollection, instance, params.Collection,
			pluginhost.AsResourceError(listErr)), nil
	}
	wire := wirePageFrom(page)
	return ReadResult{
		Kind:       KindResource,
		Operation:  OpCollection,
		Workspace:  ws.ID,
		Target:     params.Collection,
		Provider:   instance,
		Revision:   revisionForValue(wire),
		Collection: wire,
	}, nil
}

// GetCollectionItem expands one row into a wire document.
func (s *Service) GetCollectionItem(ctx context.Context, workspaceID, instance, collection, id string, refresh bool) (ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	ref := resource.Reference{Instance: instance, Collection: collection, Locator: id}
	if !ref.Valid() {
		return ReadResult{}, Rejected("collection item reference is empty or exceeds its bounds")
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ReadResult{}, err
	}
	mgr, err := s.resourceManager()
	if err != nil {
		return ReadResult{}, err
	}
	doc, getErr := mgr.Get(ctx, pluginhost.GetRequest{
		Instance: instance,
		Params:   pluginhost.GetParams{Collection: collection, ID: id},
		Refresh:  refresh,
	})
	if getErr != nil {
		return collectionErrorResult(ws.ID, OpItem, instance, collection,
			pluginhost.AsResourceError(getErr)), nil
	}
	wire := wireDocumentFrom(doc)
	return ReadResult{
		Kind:      KindResource,
		Operation: OpItem,
		Workspace: ws.ID,
		Target:    id,
		Display:   doc.Title,
		Provider:  instance,
		Revision:  revisionForValue(wire),
		Resource:  wire,
	}, nil
}

func collectionErrorResult(workspace, operation, instance, collection string, err *resource.Error) ReadResult {
	if err == nil {
		err = resource.Errorf(resource.CodeInternal, "plugin failed")
	}
	retryable := err.Retryable
	wire := &resource.WireError{
		Code:      string(err.Code),
		Message:   err.Message,
		Retryable: &retryable,
		SetupHint: err.SetupHint,
	}
	return ReadResult{
		Kind:          KindResource,
		Operation:     operation,
		Workspace:     workspace,
		Target:        collection,
		Provider:      instance,
		Revision:      revisionForValue(wire),
		ResourceError: wire,
	}
}

func wirePageFrom(page pluginhost.Page) *WirePage {
	out := &WirePage{
		Outcome:    string(page.Outcome),
		NextCursor: page.NextCursor,
		Total:      page.Total,
		Truncated:  page.Truncated,
		Items:      make([]WireItem, 0, len(page.Items)),
	}
	for _, item := range page.Items {
		row := WireItem{ID: item.ID, Cells: item.Cells, SourceURL: item.SourceURL}
		if item.Status != nil {
			row.Status = &WireStatus{Label: item.Status.Label, Tone: string(item.Status.Tone)}
		}
		out.Items = append(out.Items, row)
	}
	for _, notice := range page.Notices {
		out.Notices = append(out.Notices, WirePageNotic{Tone: string(notice.Tone), Text: notice.Text})
	}
	return out
}

// PageFromWire is the inverse, for the viewer side of the connection. Every
// string is re-coerced rather than trusted: the answer crossed a process
// boundary, and a host that sent an outcome this version does not know still
// gets read as "coverage may be incomplete" rather than as a guarantee.
func PageFromWire(wire *WirePage) pluginhost.Page {
	if wire == nil {
		return pluginhost.Page{Outcome: pluginhost.OutcomeAnswered}
	}
	out := pluginhost.Page{
		Outcome:    pluginhost.CoercePageOutcome(wire.Outcome),
		NextCursor: wire.NextCursor,
		Total:      wire.Total,
		Truncated:  wire.Truncated,
		Items:      make([]pluginhost.Item, 0, len(wire.Items)),
	}
	for _, row := range wire.Items {
		item := pluginhost.Item{ID: row.ID, Cells: row.Cells, SourceURL: row.SourceURL}
		if row.Status != nil {
			item.Status = &resource.Status{Label: row.Status.Label, Tone: resource.CoerceTone(row.Status.Tone)}
		}
		out.Items = append(out.Items, item)
	}
	for _, notice := range wire.Notices {
		out.Notices = append(out.Notices, pluginhost.Notice{
			Tone: resource.CoerceTone(notice.Tone), Text: notice.Text,
		})
	}
	return out
}
