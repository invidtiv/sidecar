package overview

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// remoteResourceDescribeInterval is the bounded visible-row cadence for
// conditional content describe --if-revision. Hidden rows are silent.
const remoteResourceDescribeInterval = 5 * time.Second

type remoteMatcherKey struct {
	hostID      string
	incarnation uint64
}

type remoteMatcherEntry struct {
	fingerprint string
	matchers    []terminallink.ResourceMatcher
}

type remoteMatcherCache struct {
	mu        sync.Mutex
	byKey     map[remoteMatcherKey]remoteMatcherEntry
	inflight  map[string]bool
	immediate map[string]bool
	tickArmed bool
}

type remoteDescribeMsg struct {
	Generation  int
	WorkspaceID string
	HostID      string
	Incarnation uint64
	IfRevision  string
	Result      contentservice.DescribeResult
	Err         error
}

type remoteResourceDescribeTickMsg struct {
	Generation  int
	WorkspaceID string
	HostID      string
	Incarnation uint64
}

type remoteResourceKey struct {
	hostID      string
	incarnation uint64
	fingerprint string
	instance    string
	matcher     string
	locator     string
}

type remoteResourceEntry struct {
	doc     resource.Document
	expires time.Time
}

type remoteResourceCache struct {
	mu      sync.Mutex
	entries map[remoteResourceKey]remoteResourceEntry
}

// previewResourceMatchers is the scanner snapshot for the selected row.
// Local rows use the app matcher snapshot. Remote rows use only that host's
// described matchers; until describe succeeds they are empty, so
// resource-looking text stays ordinary.
func (m *Model) previewResourceMatchers() []terminallink.ResourceMatcher {
	ws, ok := m.SelectedWorkspace()
	if !ok || !ws.Remote() {
		return m.resourceMatchers
	}
	return m.remoteMatchersFor(ws)
}

func (m *Model) remoteMatchersFor(ws workspaceinventory.Workspace) []terminallink.ResourceMatcher {
	if ws.HostID == "" {
		return nil
	}
	key := remoteMatcherKey{hostID: ws.HostID, incarnation: m.hostIncarnationFor(ws.HostID)}
	m.remoteMatchers.mu.Lock()
	defer m.remoteMatchers.mu.Unlock()
	entry, ok := m.remoteMatchers.byKey[key]
	if !ok {
		return nil
	}
	return entry.matchers
}

func (m *Model) remoteDescriptorFingerprint(hostID string, incarnation uint64) string {
	key := remoteMatcherKey{hostID: hostID, incarnation: incarnation}
	m.remoteMatchers.mu.Lock()
	defer m.remoteMatchers.mu.Unlock()
	return m.remoteMatchers.byKey[key].fingerprint
}

func (m *Model) markRemoteDescribeImmediate(hostID string) {
	if hostID == "" {
		return
	}
	m.remoteMatchers.mu.Lock()
	defer m.remoteMatchers.mu.Unlock()
	if m.remoteMatchers.immediate == nil {
		m.remoteMatchers.immediate = map[string]bool{}
	}
	m.remoteMatchers.immediate[hostID] = true
}

func (m *Model) discardRemoteMatchers(hostID string) {
	m.remoteMatchers.mu.Lock()
	defer m.remoteMatchers.mu.Unlock()
	for key := range m.remoteMatchers.byKey {
		if key.hostID == hostID {
			delete(m.remoteMatchers.byKey, key)
		}
	}
	m.remoteResources.mu.Lock()
	for key := range m.remoteResources.entries {
		if key.hostID == hostID {
			delete(m.remoteResources.entries, key)
		}
	}
	m.remoteResources.mu.Unlock()
}

func (m *Model) visibleRemoteRow() (workspaceinventory.Workspace, bool) {
	if !m.preview.visible {
		return workspaceinventory.Workspace{}, false
	}
	ws, ok := m.SelectedWorkspace()
	if !ok || !ws.Remote() || !m.hostShows(ws.HostID) {
		return workspaceinventory.Workspace{}, false
	}
	if !m.hostVerbs(ws.HostID).ContentReadV1 {
		return workspaceinventory.Workspace{}, false
	}
	return ws, true
}

func (m *Model) ensureRemoteResourceDescribe() tea.Cmd {
	ws, ok := m.visibleRemoteRow()
	if !ok {
		return nil
	}
	incarnation := m.hostIncarnationFor(ws.HostID)
	key := remoteMatcherKey{hostID: ws.HostID, incarnation: incarnation}
	m.remoteMatchers.mu.Lock()
	if m.remoteMatchers.inflight[ws.HostID] {
		m.remoteMatchers.mu.Unlock()
		return nil
	}
	_, have := m.remoteMatchers.byKey[key]
	immediate := m.remoteMatchers.immediate[ws.HostID]
	if immediate {
		delete(m.remoteMatchers.immediate, ws.HostID)
	}
	if have && !immediate {
		m.remoteMatchers.mu.Unlock()
		return nil
	}
	ifRevision := ""
	if have {
		ifRevision = m.remoteMatchers.byKey[key].fingerprint
	}
	if m.remoteMatchers.inflight == nil {
		m.remoteMatchers.inflight = map[string]bool{}
	}
	m.remoteMatchers.inflight[ws.HostID] = true
	generation := m.preview.generation
	workspaceID := m.preview.workspaceID
	m.remoteMatchers.mu.Unlock()
	return m.remoteDescribeCmd(ws.HostID, workspaceID, generation, incarnation, ifRevision)
}

func (m *Model) remoteDescribeCmd(hostID, workspaceID string, generation int, incarnation uint64, ifRevision string) tea.Cmd {
	src := m.documentSource(contentpanes.SurfaceContext{
		Source: contentpanes.SourceContext{HostID: hostID, HostIncarnation: incarnation, WorkspaceID: workspaceID},
	})
	return func() tea.Msg {
		result, err := src.Describe(context.Background(), ifRevision)
		return remoteDescribeMsg{
			Generation:  generation,
			WorkspaceID: workspaceID,
			HostID:      hostID,
			Incarnation: incarnation,
			IfRevision:  ifRevision,
			Result:      result,
			Err:         err,
		}
	}
}

func (m *Model) applyRemoteDescribe(msg remoteDescribeMsg) tea.Cmd {
	m.remoteMatchers.mu.Lock()
	if m.remoteMatchers.inflight != nil {
		delete(m.remoteMatchers.inflight, msg.HostID)
	}
	m.remoteMatchers.mu.Unlock()
	if msg.Generation != m.preview.generation || msg.WorkspaceID != m.preview.workspaceID {
		return nil
	}
	if m.hostIncarnationFor(msg.HostID) != msg.Incarnation {
		m.discardRemoteMatchers(msg.HostID)
		return nil
	}
	if msg.Err != nil {
		return nil
	}
	key := remoteMatcherKey{hostID: msg.HostID, incarnation: msg.Incarnation}
	if msg.Result.NotModified {
		return nil
	}
	fp := msg.Result.Fingerprint
	if fp == "" {
		return nil
	}
	if got := contentservice.FingerprintDescriptors(msg.Result.Descriptors); got != fp {
		return nil
	}
	matchers, err := contentservice.TerminalMatchersFrom(msg.Result.Descriptors)
	if err != nil {
		return nil
	}
	m.remoteMatchers.mu.Lock()
	if m.remoteMatchers.byKey == nil {
		m.remoteMatchers.byKey = map[remoteMatcherKey]remoteMatcherEntry{}
	}
	prev, had := m.remoteMatchers.byKey[key]
	m.remoteMatchers.byKey[key] = remoteMatcherEntry{fingerprint: fp, matchers: matchers}
	m.remoteMatchers.mu.Unlock()
	if !had || prev.fingerprint != fp {
		m.linkMatcherGeneration++
		m.pruneRemoteResources(msg.HostID, msg.Incarnation, fp)
	}
	return nil
}

func (m *Model) remoteResourceDescribeCmd() tea.Cmd {
	ws, ok := m.visibleRemoteRow()
	if !ok {
		m.remoteMatchers.mu.Lock()
		m.remoteMatchers.tickArmed = false
		m.remoteMatchers.mu.Unlock()
		return nil
	}
	m.remoteMatchers.mu.Lock()
	if m.remoteMatchers.tickArmed {
		m.remoteMatchers.mu.Unlock()
		return nil
	}
	m.remoteMatchers.tickArmed = true
	generation := m.preview.generation
	workspaceID := m.preview.workspaceID
	hostID := ws.HostID
	incarnation := m.hostIncarnationFor(ws.HostID)
	m.remoteMatchers.mu.Unlock()
	return tea.Tick(remoteResourceDescribeInterval, func(time.Time) tea.Msg {
		return remoteResourceDescribeTickMsg{
			Generation:  generation,
			WorkspaceID: workspaceID,
			HostID:      hostID,
			Incarnation: incarnation,
		}
	})
}

func (m *Model) applyRemoteResourceDescribeTick(msg remoteResourceDescribeTickMsg) tea.Cmd {
	m.remoteMatchers.mu.Lock()
	m.remoteMatchers.tickArmed = false
	m.remoteMatchers.mu.Unlock()
	if msg.Generation != m.preview.generation || msg.WorkspaceID != m.preview.workspaceID {
		return m.remoteResourceDescribeCmd()
	}
	if m.hostIncarnationFor(msg.HostID) != msg.Incarnation {
		m.discardRemoteMatchers(msg.HostID)
		return m.ensureRemoteResourceDescribe()
	}
	ws, ok := m.visibleRemoteRow()
	if !ok || ws.HostID != msg.HostID {
		return nil
	}
	m.markRemoteDescribeImmediate(msg.HostID)
	return tea.Batch(m.ensureRemoteResourceDescribe(), m.remoteResourceDescribeCmd())
}

func (m *Model) pruneRemoteResources(hostID string, incarnation uint64, fingerprint string) {
	m.remoteResources.mu.Lock()
	defer m.remoteResources.mu.Unlock()
	for key := range m.remoteResources.entries {
		if key.hostID == hostID && (key.incarnation != incarnation || key.fingerprint != fingerprint) {
			delete(m.remoteResources.entries, key)
		}
	}
}

func (m *Model) remoteResourceCacheKey(src contentpanes.SourceContext, ref resource.Reference) remoteResourceKey {
	return remoteResourceKey{
		hostID:      src.HostID,
		incarnation: src.HostIncarnation,
		fingerprint: m.remoteDescriptorFingerprint(src.HostID, src.HostIncarnation),
		instance:    ref.Instance,
		matcher:     ref.Matcher,
		locator:     ref.Locator,
	}
}

func (m *Model) cachedRemoteResource(key remoteResourceKey) (resource.Document, bool) {
	m.remoteResources.mu.Lock()
	defer m.remoteResources.mu.Unlock()
	entry, ok := m.remoteResources.entries[key]
	if !ok || !entry.expires.After(m.now()) {
		return resource.Document{}, false
	}
	return entry.doc, true
}

func (m *Model) storeRemoteResource(key remoteResourceKey, doc resource.Document) {
	m.remoteResources.mu.Lock()
	defer m.remoteResources.mu.Unlock()
	if m.remoteResources.entries == nil {
		m.remoteResources.entries = map[remoteResourceKey]remoteResourceEntry{}
	}
	m.remoteResources.entries[key] = remoteResourceEntry{doc: doc, expires: contentservice.DocumentFreshUntil(doc, m.now())}
}

func (m *Model) resourceWorkspaceRemote(workspaceID string) bool {
	if workspaceID == "" {
		return false
	}
	if ws, ok := m.catalog[workspaceID]; ok {
		return ws.Remote()
	}
	if m.preview.deck != nil {
		ctx := m.preview.deck.Context()
		if ctx.Surface == workspaceID && ctx.Source.Remote() {
			return true
		}
	}
	if cached, ok := m.preview.paneCache[workspaceID]; ok && cached.deck != nil && cached.deck.Context().Source.Remote() {
		return true
	}
	if ws, ok := m.SelectedWorkspace(); ok && ws.ID == workspaceID {
		return ws.Remote()
	}
	_, _, scoped := hosts.SplitScopedKey(workspaceID)
	return scoped
}

func (m *Model) remoteResourceUnavailableCmd(workspaceID string, epoch uint64, modelID int, generation uint64, ref resourceview.Ref, refresh bool) tea.Cmd {
	return func() tea.Msg {
		return previewResourceResolvedMsg{
			ResolvedMsg: resourceview.ResolvedMsg{
				ModelID: modelID, Generation: generation, Epoch: epoch,
				Ref: ref, Refresh: refresh,
				Err: resource.Errorf(resource.CodeUnavailable, "that host is not available"),
			},
			WorkspaceID: workspaceID,
		}
	}
}

func (m *Model) remoteResourceResolveCmd(ctx contentpanes.SurfaceContext, workspaceID string, epoch uint64, modelID int, generation uint64, ref resourceview.Ref, refresh bool) tea.Cmd {
	src := m.documentSource(ctx)
	key := m.remoteResourceCacheKey(ctx.Source, ref)
	if !refresh {
		if doc, ok := m.cachedRemoteResource(key); ok {
			return func() tea.Msg {
				return previewResourceResolvedMsg{
					ResolvedMsg: resourceview.ResolvedMsg{
						ModelID: modelID, Generation: generation, Epoch: epoch,
						Ref: ref, Document: doc, Refresh: refresh,
					},
					WorkspaceID: workspaceID,
				}
			}
		}
	}
	return func() tea.Msg {
		msg := resourceview.ResolvedMsg{
			ModelID: modelID, Generation: generation, Epoch: epoch,
			Ref: ref, Refresh: refresh,
		}
		doc, err := src.ResolveResource(context.Background(), ctx.Source, ref, refresh)
		if err != nil {
			msg.Err = err
			return previewResourceResolvedMsg{ResolvedMsg: msg, WorkspaceID: workspaceID}
		}
		wire := wireFromDocument(doc)
		sanitized, err := contentservice.SanitizeWireDocument(wire)
		if err != nil {
			msg.Err = err
			return previewResourceResolvedMsg{ResolvedMsg: msg, WorkspaceID: workspaceID}
		}
		m.storeRemoteResource(key, sanitized)
		msg.Document = sanitized
		return previewResourceResolvedMsg{ResolvedMsg: msg, WorkspaceID: workspaceID}
	}
}

func wireFromDocument(doc resource.Document) *resource.WireDocument {
	w := &resource.WireDocument{
		Identity:        doc.Identity,
		Title:           doc.Title,
		Subtitle:        doc.Subtitle,
		SourceURL:       doc.SourceURL,
		FreshForSeconds: doc.FreshFor.Seconds(),
	}
	if !doc.UpdatedAt.IsZero() {
		w.UpdatedAt = doc.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if doc.Status != nil {
		w.Status = &resource.WireStatus{Label: doc.Status.Label, Tone: string(doc.Status.Tone)}
	}
	for _, f := range doc.Fields {
		w.Fields = append(w.Fields, resource.WireField{Label: f.Label, Value: f.Value, Kind: string(f.Kind)})
	}
	if doc.Body != nil {
		w.Body = &resource.WireBody{Format: string(doc.Body.Format), Text: doc.Body.Text}
	}
	return w
}
