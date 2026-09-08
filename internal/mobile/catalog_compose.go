package mobile

import (
	"fmt"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// CatalogSource is one owning service's authoritative, unfiltered project
// snapshot after the hub has remapped its public identities. Source snapshots
// use project order so composition retains each owner's configured order.
type CatalogSource struct {
	OwnerHostID string
	Snapshot    mobileproto.CatalogSnapshot
}

// ComposeCatalog projects already-authorized owner snapshots through the same
// query, ordering and grouping rules as QueryCatalog. It performs no target
// resolution. Callers must obtain each snapshot from the current owning mobile
// service and remap its public host identity before calling it.
func ComposeCatalog(query mobileproto.CatalogQuery, identity CatalogIdentity, observedAt time.Time, catalogHosts []mobileproto.CatalogHost, sources []CatalogSource, extraFailures []mobileproto.CatalogFailure) (mobileproto.CatalogSnapshot, error) {
	query, mode, err := normalizeCatalogQuery(query)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	if identity.HubID == "" || identity.OwnerHostID == "" || identity.OwnerConfigGeneration == "" {
		return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog authority identity is incomplete"}
	}
	hostStates, err := validateComposedHosts(catalogHosts, identity.OwnerHostID)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	if len(sources) > mobileproto.MaxCatalogHosts {
		return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "combined mobile catalog exceeds source limit"}
	}
	rows := make(map[string]mobileproto.CatalogRow)
	items := make([]workspacelist.Item, 0)
	failures := append([]mobileproto.CatalogFailure(nil), extraFailures...)
	if len(failures) > mobileproto.MaxCatalogFailures {
		return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "combined mobile catalog exceeds failure limit"}
	}
	seenSources := make(map[string]bool, len(sources))
	for sourceOrder, source := range sources {
		if source.OwnerHostID == "" || seenSources[source.OwnerHostID] || hostStates[source.OwnerHostID] != "online" {
			return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog source is duplicated, absent or offline"}
		}
		seenSources[source.OwnerHostID] = true
		if err := validateCompositionSource(identity.HubID, source); err != nil {
			return mobileproto.CatalogSnapshot{}, err
		}
		for _, failure := range source.Snapshot.Failures {
			if len(failures) >= mobileproto.MaxCatalogFailures {
				return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "combined mobile catalog exceeds failure limit"}
			}
			failure.ID = hosts.ScopedKey(source.OwnerHostID, failure.ID)
			failures = append(failures, failure)
		}
		for projectOrder, section := range source.Snapshot.Sections {
			var sectionProject string
			for _, row := range section.Rows {
				if len(items) >= mobileproto.MaxCatalogRows {
					return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "combined mobile catalog exceeds row limit"}
				}
				if row.ID == "" || row.ProjectID == "" || row.OwnerHostID != source.OwnerHostID || row.WorkspaceKind == "" || row.AttachState == "" {
					return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog contains incomplete row identity"}
				}
				if sectionProject == "" {
					sectionProject = row.ProjectID
				} else if row.ProjectID != sectionProject {
					return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog project section contains several projects"}
				}
				if _, exists := rows[row.ID]; exists {
					return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "combined mobile catalog contains duplicate row identity"}
				}
				if err := validateComposedRow(identity.HubID, hostStates, row); err != nil {
					return mobileproto.CatalogSnapshot{}, err
				}
				changedAt, err := parseCatalogTime(row.ChangedAt)
				if err != nil {
					return mobileproto.CatalogSnapshot{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: fmt.Sprintf("combined mobile catalog row %q has invalid changed_at", row.ID)}
				}
				items = append(items, workspacelist.Item{ID: row.ID, Name: row.DisplayName, Project: row.ProjectName,
					ProjectKey: hosts.ScopedKey(source.OwnerHostID, row.ProjectID), ProjectOrder: sourceOrder*mobileproto.MaxCatalogRows + projectOrder,
					Branch: row.Branch, Task: row.Task, Provider: row.Provider, TmuxName: row.Session, Host: row.OwnerHostID,
					Status: row.Status, Kind: row.WorkspaceKind, Group: workspacelist.Group(row.Group), ChangedAt: changedAt})
				rows[row.ID] = row
			}
		}
	}
	return projectCatalog(query, mode, identity, observedAt, catalogHosts, items, rows, failures)
}

func validateComposedHosts(catalogHosts []mobileproto.CatalogHost, ownerHostID string) (map[string]string, error) {
	if len(catalogHosts) > mobileproto.MaxCatalogHosts {
		return nil, &ResolveError{Code: mobileproto.ErrorOverflow, Message: "combined mobile catalog exceeds host limit"}
	}
	states := make(map[string]string, len(catalogHosts))
	for _, host := range catalogHosts {
		if host.ID == "" || host.State == "" || len(host.ID) > mobileproto.MaxTargetBytes {
			return nil, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog contains incomplete host identity"}
		}
		if _, exists := states[host.ID]; exists {
			return nil, &ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "combined mobile catalog contains duplicate host identity"}
		}
		states[host.ID] = strings.ToLower(host.State)
	}
	if _, ok := states[ownerHostID]; !ok {
		return nil, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog omits its hub owner identity"}
	}
	return states, nil
}

func validateCompositionSource(hubID string, source CatalogSource) error {
	snapshot := source.Snapshot
	if snapshot.HubID != hubID || snapshot.OwnerHostID != source.OwnerHostID || snapshot.OwnerConfigGeneration == "" ||
		strings.ToLower(snapshot.Query.Sort) != "project" || snapshot.Query.Search != "" || len(snapshot.Query.Hosts) != 0 || len(snapshot.Query.Providers) != 0 || len(snapshot.Query.States) != 0 ||
		(snapshot.Query.ShowIdleSessions != nil && !*snapshot.Query.ShowIdleSessions) {
		return &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog source is not an authoritative unfiltered project snapshot"}
	}
	return nil
}

func validateComposedRow(hubID string, hostStates map[string]string, row mobileproto.CatalogRow) error {
	if row.AttachmentReady {
		if row.AttachState != AttachReady || !row.Live || row.Stale || row.Ambiguous || row.Target == "" || row.ExpectedTarget == nil ||
			hostStates[row.OwnerHostID] != "online" || !catalogRowMatchesExpectedHub(hubID, row) {
			return &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog contains inconsistent attachment authority"}
		}
	} else if row.ExpectedTarget != nil || row.Target != "" && row.AttachState != AttachCandidate {
		return &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "combined mobile catalog contains authority on a non-ready row"}
	}
	return nil
}

func catalogRowMatchesExpectedHub(hubID string, row mobileproto.CatalogRow) bool {
	expected := row.ExpectedTarget
	return expected.HubID == hubID && expected.OwnerHostID == row.OwnerHostID && expected.OwnerConfigGeneration != "" &&
		expected.WorkspaceID == row.WorkspaceID && expected.WorkspaceKind == row.WorkspaceKind && expected.Session == row.Session &&
		expected.Pane == row.Pane && expected.ServerIncarnation != "" && expected.TargetGeneration != ""
}

func parseCatalogTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}
