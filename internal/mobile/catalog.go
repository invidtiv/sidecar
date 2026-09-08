package mobile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspacecatalog"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

const (
	AttachReady       = "ready"
	AttachCandidate   = "candidate"
	AttachUnavailable = "unavailable"
	AttachAmbiguous   = "ambiguous"
	AttachStale       = "stale"
	AttachUnknown     = "unknown"
	AttachUnsupported = "unsupported"

	// The stdio service adds its response envelope, request correlation and a
	// trailing newline around a catalog snapshot. Keeping a fixed reserve makes
	// every snapshot accepted by the shared CLI/service path fit MaxLineBytes.
	catalogEnvelopeReserve = 64 << 10
)

type CatalogProject struct {
	Result workspaceinventory.ProjectResult
	Label  string
	Order  int
	Stale  bool
}

type CatalogInput struct {
	ObservedAt time.Time
	Hosts      []mobileproto.CatalogHost
	Projects   []CatalogProject
	// CandidateResolver authorizes selectors against this exact inventory
	// observation. It avoids rebuilding the global catalog for every candidate;
	// target open and every later operation still perform fresh source checks.
	CandidateResolver Resolver
}

type CatalogProvider func(context.Context) (CatalogInput, error)

type CatalogIdentity struct {
	HubID, OwnerHostID, OwnerConfigGeneration string
}

// QueryCatalog collects and projects one bounded catalog snapshot. It is the
// common path for the JSON CLI and stdio protocol.
func QueryCatalog(ctx context.Context, provider CatalogProvider, resolver Resolver, query mobileproto.CatalogQuery, identity CatalogIdentity) (mobileproto.CatalogSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if provider == nil {
		return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorUnsupported, Message: "mobile catalog is unavailable"}
	}
	query, mode, err := normalizeCatalogQuery(query)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	input, err := provider(ctx)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = time.Now()
	}
	if identity.HubID == "" || identity.OwnerHostID == "" || identity.OwnerConfigGeneration == "" {
		return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "mobile catalog authority identity is incomplete"}
	}
	if len(input.Hosts) > mobileproto.MaxCatalogHosts {
		return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds host limit"}
	}
	seenHosts := make(map[string]bool, len(input.Hosts))
	ownerPresent := false
	for _, host := range input.Hosts {
		if host.ID == "" || host.State == "" || len(host.ID) > mobileproto.MaxTargetBytes || seenHosts[host.ID] {
			return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "mobile catalog contains invalid or duplicate host identity"}
		}
		seenHosts[host.ID] = true
		ownerPresent = ownerPresent || host.ID == identity.OwnerHostID
	}
	if !ownerPresent {
		return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "mobile catalog omits its owning host identity"}
	}
	items := make([]workspacelist.Item, 0)
	rows := make(map[string]mobileproto.CatalogRow)
	failures := make([]mobileproto.CatalogFailure, 0)
	for _, project := range input.Projects {
		if project.Result.Err != nil {
			if len(failures) >= mobileproto.MaxCatalogFailures {
				return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds failure limit"}
			}
			failures = append(failures, mobileproto.CatalogFailure{Scope: "project", ID: project.Result.ProjectKey, Name: project.Result.ProjectName, State: "unavailable", Detail: project.Result.Err.Error()})
		}
		for _, workspace := range project.Result.Workspaces {
			if len(items) >= mobileproto.MaxCatalogRows {
				return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds row limit"}
			}
			item := workspacecatalog.ProjectItem(workspace.Item(), project.Label, project.Order, project.Stale)
			if workspace.ID == "" || workspace.ProjectKey == "" || workspace.Kind == "" {
				return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "mobile catalog contains incomplete workspace identity"}
			}
			if item.Host == "" {
				item.Host = identity.OwnerHostID
			}
			if workspace.ObservedAt.IsZero() {
				workspace.ObservedAt = input.ObservedAt
			}
			if _, exists := rows[item.ID]; exists {
				return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "mobile catalog contains duplicate workspace identity"}
			}
			items = append(items, item)
			rows[item.ID] = catalogRow(workspace, item, project.Stale, identity.OwnerHostID)
		}
	}
	totalCandidates := 0
	for _, row := range rows {
		totalCandidates += len(row.Candidates)
		if totalCandidates > mobileproto.MaxCatalogCandidates {
			return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds terminal candidate limit"}
		}
	}
	refuseDuplicateTargets(rows)
	candidateResolver := input.CandidateResolver
	if candidateResolver == nil {
		candidateResolver = resolver
	}
	if err := authorizeCatalogRows(ctx, rows, resolver, candidateResolver, identity); err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	return projectCatalog(query, mode, identity, input.ObservedAt, input.Hosts, items, rows, failures)
}

// projectCatalog is the single query, ordering, grouping and envelope path for
// both one-owner collection and already-authorized hub composition.
func projectCatalog(query mobileproto.CatalogQuery, mode workspacelist.Sort, identity CatalogIdentity, observedAt time.Time, catalogHosts []mobileproto.CatalogHost, items []workspacelist.Item, rows map[string]mobileproto.CatalogRow, failures []mobileproto.CatalogFailure) (mobileproto.CatalogSnapshot, error) {
	allRows := make([]mobileproto.CatalogRow, 0, len(rows))
	for _, row := range rows {
		allRows = append(allRows, row)
	}
	generation, err := catalogGeneration(identity, catalogHosts, allRows, failures)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	items = workspacelist.Filtered(items, query.Search)
	items = filterCatalogItems(items, rows, query)
	items = workspacelist.Sorted(items, mode)
	sections := workspacelist.GroupedAt(items, mode, observedAt, nil)
	wireSections := make([]mobileproto.CatalogSection, 0, len(sections))
	total := 0
	for _, section := range sections {
		wire := mobileproto.CatalogSection{Title: section.Title, Key: section.Key, Group: string(section.Group), Rows: make([]mobileproto.CatalogRow, 0, len(section.Items))}
		for _, item := range section.Items {
			wire.Rows = append(wire.Rows, rows[item.ID])
			total++
		}
		wireSections = append(wireSections, wire)
	}
	catalogHosts = append([]mobileproto.CatalogHost(nil), catalogHosts...)
	if catalogHosts == nil {
		catalogHosts = []mobileproto.CatalogHost{}
	}
	if failures == nil {
		failures = []mobileproto.CatalogFailure{}
	}
	snapshot := mobileproto.CatalogSnapshot{
		Generation: generation, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		HubID: identity.HubID, OwnerHostID: identity.OwnerHostID, OwnerConfigGeneration: identity.OwnerConfigGeneration,
		Query: query, Hosts: catalogHosts, Sections: wireSections, Failures: failures, Total: total,
	}
	if err := mobileproto.ValidateCatalogCandidates(snapshot); err != nil {
		return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: err.Error()}
	}
	if err := validateCatalogPayload(snapshot); err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	return snapshot, nil
}

// authorizeCatalogRows resolves each candidate through the same target
// resolver used by open. The full target identity is returned with the row, so
// a later resolve can exact-match it and refuse a replacement created after
// listing. This precedes filtering and generation: both describe the final
// authoritative verdict rather than the optimistic inventory candidate.
func authorizeCatalogRows(ctx context.Context, rows map[string]mobileproto.CatalogRow, resolver, candidateResolver Resolver, identity CatalogIdentity) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for id, value := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(value.Candidates) > 0 {
			row := value
			if candidateResolver == nil {
				refuseCatalogRow(&row, AttachUnsupported, mobileproto.ErrorUnsupported, "mobile terminal resolver is unavailable")
				rows[id] = row
				continue
			}
			validated := append([]mobileproto.CatalogCandidate(nil), row.Candidates...)
			failed := false
			for index, candidate := range validated {
				resolved, err := candidateResolver(ctx, candidate.Selector)
				if err != nil {
					state, code := catalogResolveState(err)
					refuseCatalogRow(&row, state, code, err.Error())
					failed = true
					break
				}
				if resolved.Selector != candidate.Selector || resolved.SourceGeneration != row.CandidateGeneration ||
					resolved.WorkspaceID != candidate.WorkspaceID || resolved.WorkspaceKind != candidate.WorkspaceKind ||
					resolved.Session != candidate.Session || resolved.Pane != candidate.Pane {
					refuseCatalogRow(&row, AttachStale, mobileproto.ErrorIdentityChanged, "terminal candidate identity changed after catalog observation")
					failed = true
					break
				}
				validated[index].ExpectedTarget = targetIdentity(identity, resolved)
			}
			if failed {
				rows[id] = row
				continue
			}
			row.Candidates = validated
			row.Live, row.Stale = true, false
			if len(validated) == 1 {
				candidate := validated[0]
				row.Session, row.Pane, row.Target = candidate.Session, candidate.Pane, candidate.Selector
				row.ExpectedTarget = &candidate.ExpectedTarget
				row.AttachState, row.AttachmentReady, row.Ambiguous = AttachReady, true, false
				row.RefusalCode, row.Refusal = "", ""
			} else {
				row.Target, row.ExpectedTarget, row.AttachmentReady = "", nil, false
				row.AttachState, row.Ambiguous = AttachAmbiguous, true
				row.RefusalCode, row.Refusal = mobileproto.ErrorAmbiguous, fmt.Sprintf("choose one of %d terminal panes", len(validated))
			}
			rows[id] = row
			continue
		}
		if value.AttachState != AttachCandidate {
			continue
		}
		row := value
		if resolver == nil {
			refuseCatalogRow(&row, AttachUnsupported, mobileproto.ErrorUnsupported, "mobile terminal resolver is unavailable")
			rows[id] = row
			continue
		}
		resolved, err := resolver(ctx, row.Target)
		if err != nil {
			state, code := catalogResolveState(err)
			refuseCatalogRow(&row, state, code, err.Error())
			rows[id] = row
			continue
		}
		if row.CandidateGeneration == "" || row.CandidateGeneration != resolvedCandidateGeneration(resolved, row.OwnerHostID) ||
			row.WorkspaceKind != resolved.WorkspaceKind || row.Session != resolved.Session || row.Pane != resolved.Pane {
			refuseCatalogRow(&row, AttachStale, mobileproto.ErrorIdentityChanged, "terminal identity changed after catalog observation")
			rows[id] = row
			continue
		}
		expected := targetIdentity(identity, resolved)
		row.ExpectedTarget = &expected
		row.WorkspaceID = resolved.WorkspaceID
		row.AttachState, row.AttachmentReady = AttachReady, true
		rows[id] = row
	}
	return nil
}

func catalogResolveState(err error) (string, string) {
	code := mobileproto.ErrorBackend
	state := AttachUnavailable
	var resolveErr *ResolveError
	if errors.As(err, &resolveErr) {
		code = resolveErr.Code
		switch code {
		case mobileproto.ErrorAmbiguous:
			state = AttachAmbiguous
		case mobileproto.ErrorUnsupported, mobileproto.ErrorUnsupportedMode, mobileproto.ErrorOverflow:
			state = AttachUnsupported
		case mobileproto.ErrorIdentityChanged:
			state = AttachStale
		}
	}
	return state, code
}

func normalizeCatalogQuery(query mobileproto.CatalogQuery) (mobileproto.CatalogQuery, workspacelist.Sort, error) {
	if len(query.Hosts) > mobileproto.MaxCatalogFilters || len(query.Providers) > mobileproto.MaxCatalogFilters || len(query.States) > mobileproto.MaxCatalogFilters {
		return query, 0, &ResolveError{Code: mobileproto.ErrorInvalidRequest, Message: "catalog query exceeds protocol bounds"}
	}
	queryBytes := len(query.Search) + len(query.Sort)
	for _, values := range [][]string{query.Hosts, query.Providers, query.States} {
		for _, value := range values {
			queryBytes += len(value)
		}
	}
	if queryBytes > mobileproto.MaxCatalogQueryBytes {
		return query, 0, &ResolveError{Code: mobileproto.ErrorInvalidRequest, Message: "catalog query exceeds protocol bounds"}
	}
	label := strings.TrimSpace(query.Sort)
	if label == "" {
		label = workspacelist.SortActivity.Label()
	}
	mode, ok := workspacelist.SortFromLabel(label, workspacelist.SortModes)
	if !ok {
		return query, 0, &ResolveError{Code: mobileproto.ErrorInvalidRequest, Message: "catalog sort must be activity, project, recent, or name"}
	}
	query.Sort = strings.ToLower(mode.Label())
	query.Search = strings.TrimSpace(query.Search)
	query.Hosts = normalizeFilters(query.Hosts)
	query.Providers = normalizeFilters(query.Providers)
	query.States = normalizeFilters(query.States)
	return query, mode, nil
}

func normalizeFilters(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func filterCatalogItems(items []workspacelist.Item, rows map[string]mobileproto.CatalogRow, query mobileproto.CatalogQuery) []workspacelist.Item {
	out := make([]workspacelist.Item, 0, len(items))
	for _, item := range items {
		row := rows[item.ID]
		if !filterIncludes(query.Hosts, row.OwnerHostID) || !filterIncludes(query.Providers, row.Provider) || !stateIncludes(query.States, row) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterIncludes(filter []string, value string) bool {
	if len(filter) == 0 {
		return true
	}
	value = strings.ToLower(value)
	for _, candidate := range filter {
		if candidate == value {
			return true
		}
	}
	return false
}

func stateIncludes(filter []string, row mobileproto.CatalogRow) bool {
	if len(filter) == 0 {
		return true
	}
	values := []string{strings.ToLower(row.Status), strings.ToLower(row.Group), strings.ToLower(row.AttachState)}
	for _, candidate := range filter {
		for _, value := range values {
			if candidate == value {
				return true
			}
		}
	}
	return false
}

func catalogRow(workspace workspaceinventory.Workspace, item workspacelist.Item, stale bool, localOwner string) mobileproto.CatalogRow {
	owner := localOwner
	if workspace.HostID != "" {
		owner = workspace.HostID
	}
	row := mobileproto.CatalogRow{
		ID: workspace.ID, OwnerHostID: owner, ProjectID: workspace.ProjectKey, ProjectName: item.Project,
		WorkspaceKind: string(workspace.Kind), DisplayName: workspace.Name,
		Branch: workspace.Branch, Task: workspace.TaskID, Provider: workspace.Provider, Status: item.Status,
		Group: string(item.Group), Session: workspace.TmuxName, Pane: workspace.PaneID,
		ObservedAt: workspace.ObservedAt.UTC().Format(time.RFC3339Nano), ChangedAt: formatCatalogTime(item.ChangedAt),
		Live: workspace.Live, Ambiguous: workspace.Ambiguous, Stale: stale,
	}
	if workspace.HasAgent() {
		row.SemanticStatus = workspace.Presentation.Semantic
		row.Attention = workspace.Presentation.Attention
	}
	switch {
	case stale:
		row.AttachState, row.RefusalCode, row.Refusal = AttachStale, mobileproto.ErrorIdentityChanged, "catalog observation is stale"
	case workspace.Kind == workspaceinventory.KindWorktree && len(workspace.TerminalCandidates) > 0:
		generation, candidates, err := WorkspaceCandidates(workspace, owner)
		if err != nil {
			code := mobileproto.ErrorBackend
			var resolveErr *ResolveError
			if errors.As(err, &resolveErr) {
				code = resolveErr.Code
			}
			row.AttachState, row.RefusalCode, row.Refusal = AttachUnsupported, code, err.Error()
			break
		}
		row.Live, row.WorkspaceID, row.CandidateGeneration, row.Candidates = true, workspace.ID, generation, candidates
		row.AttachState = AttachCandidate
	case workspace.Ambiguous:
		row.AttachState, row.RefusalCode, row.Refusal = AttachAmbiguous, mobileproto.ErrorAmbiguous, "workspace matches multiple terminal panes"
	case !workspace.Live || workspace.PaneID == "":
		row.AttachState, row.RefusalCode, row.Refusal = AttachUnavailable, mobileproto.ErrorNotFound, "workspace has no live terminal pane"
	case workspace.Kind != workspaceinventory.KindShell:
		row.AttachState, row.RefusalCode, row.Refusal = AttachUnsupported, mobileproto.ErrorUnsupported, "worktree terminal attachment is not available in this catalog slice"
	case workspace.CreatedAt.IsZero() || workspace.TmuxName == "":
		row.AttachState, row.RefusalCode, row.Refusal = AttachUnknown, mobileproto.ErrorIdentityChanged, "managed shell durable identity is unavailable"
	default:
		row.AttachState, row.Target = AttachCandidate, workspace.TmuxName
		row.CandidateGeneration = catalogCandidateGeneration(workspace, owner)
	}
	return row
}

func refuseCatalogRow(row *mobileproto.CatalogRow, state, code, reason string) {
	row.AttachState, row.RefusalCode, row.Refusal = state, code, reason
	row.Target, row.CandidateGeneration, row.Candidates, row.ExpectedTarget, row.AttachmentReady = "", "", nil, nil, false
}

func catalogCandidateGeneration(workspace workspaceinventory.Workspace, owner string) string {
	return candidateGeneration(canonicalCatalogPath(workspace.ProjectKey), string(workspace.Kind), workspace.TmuxName, workspace.PaneID, formatCatalogTime(workspace.CreatedAt), owner)
}

func resolvedCandidateGeneration(resolved ResolvedTarget, owner string) string {
	created := resolved.DurableSessionCreated
	if parsed, err := time.Parse(time.RFC3339Nano, created); err == nil {
		created = formatCatalogTime(parsed)
	}
	return candidateGeneration(canonicalCatalogPath(resolved.ProjectRoot), resolved.WorkspaceKind, resolved.Session, resolved.Pane, created, owner)
}

func canonicalCatalogPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

func candidateGeneration(workspaceID, workspaceKind, session, pane, created, owner string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{workspaceID, workspaceKind, session, pane, created, owner}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func refuseDuplicateTargets(rows map[string]mobileproto.CatalogRow) {
	byTarget := make(map[string][]string)
	for id, row := range rows {
		if row.AttachState == AttachCandidate && len(row.Candidates) == 0 {
			key := row.OwnerHostID + "\x00" + row.Target
			byTarget[key] = append(byTarget[key], id)
		}
	}
	for _, ids := range byTarget {
		if len(ids) < 2 {
			continue
		}
		for _, id := range ids {
			row := rows[id]
			refuseCatalogRow(&row, AttachAmbiguous, mobileproto.ErrorAmbiguous, "terminal target is claimed by multiple catalog rows")
			row.Ambiguous = true
			rows[id] = row
		}
	}
}

func formatCatalogTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func catalogGeneration(identity CatalogIdentity, hosts []mobileproto.CatalogHost, rows []mobileproto.CatalogRow, failures []mobileproto.CatalogFailure) (string, error) {
	hosts = append([]mobileproto.CatalogHost(nil), hosts...)
	failures = append([]mobileproto.CatalogFailure(nil), failures...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].ID < hosts[j].ID })
	sort.Slice(failures, func(i, j int) bool {
		if failures[i].Scope != failures[j].Scope {
			return failures[i].Scope < failures[j].Scope
		}
		return failures[i].ID < failures[j].ID
	})
	for i := range rows {
		// Observation time belongs to this poll's envelope. It must not churn
		// the content generation when every catalog fact is unchanged.
		rows[i].ObservedAt = ""
	}
	payload, err := json.Marshal(struct {
		Identity CatalogIdentity              `json:"identity"`
		Hosts    []mobileproto.CatalogHost    `json:"hosts"`
		Rows     []mobileproto.CatalogRow     `json:"rows"`
		Failures []mobileproto.CatalogFailure `json:"failures"`
	}{identity, hosts, rows, failures})
	if err != nil {
		return "", fmt.Errorf("mobile catalog generation: %w", err)
	}
	if len(payload) > mobileproto.MaxLineBytes-catalogEnvelopeReserve {
		return "", &ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds response byte limit"}
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:16]), nil
}

func validateCatalogPayload(snapshot mobileproto.CatalogSnapshot) error {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("mobile catalog response: %w", err)
	}
	if len(payload) > mobileproto.MaxLineBytes-catalogEnvelopeReserve {
		return &ResolveError{Code: mobileproto.ErrorOverflow, Message: "mobile catalog exceeds response byte limit"}
	}
	return nil
}
