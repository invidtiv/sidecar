package overview

import (
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// HostCatalogEntry is one registered machine's in-memory inventory as the
// project and worktree switchers will read it: live health, serve incarnation,
// and the project rows already folded in. It is a copy; callers must not
// mutate registry state through the result.
type HostCatalogEntry struct {
	ID          string
	Health      hosts.Health
	Incarnation uint64
	Projects    []HostCatalogProject
}

// HostCatalogProject is one host-side project. Key is the unscoped inventory
// key (canonical(root) on that machine). Persist and compare later via
// hosts.ScopedKey(HostID, Key) — hosts.ProjectResults already stores the
// scoped form, and leaving that in Key would double-scope.
type HostCatalogProject struct {
	Key        string
	Name       string
	Root       string
	Workspaces []workspaceinventory.Workspace
}

// HostCatalog returns every registered host's current catalog, in the same
// order HostConditions uses. It is empty when there is no registry — feature
// off, Overview off, or nothing registered — because in that case there is
// nothing to read without starting a second Sync loop.
func (m *Model) HostCatalog() []HostCatalogEntry {
	if m.hostRegistry == nil {
		return nil
	}
	order := m.hostOrder()
	if len(order) == 0 {
		return nil
	}
	out := make([]HostCatalogEntry, 0, len(order))
	for _, id := range order {
		out = append(out, HostCatalogEntry{
			ID:          id,
			Health:      m.hostHealth[id],
			Incarnation: hostIncarnation(m, id),
			Projects:    copyHostProjects(m.hostResults[id], m.hostProjects[id]),
		})
	}
	return out
}

func hostIncarnation(m *Model, id string) uint64 {
	if m.hostIncarnations != nil {
		if incarnation, ok := m.hostIncarnations[id]; ok {
			return incarnation
		}
	}
	if m.hostRegistry != nil {
		if incarnation, ok := m.hostRegistry.Incarnation(id); ok {
			return incarnation
		}
	}
	return 0
}

func copyHostProjects(results []workspaceinventory.ProjectResult, projects []Project) []HostCatalogProject {
	if len(results) > 0 {
		out := make([]HostCatalogProject, 0, len(results))
		for _, result := range results {
			out = append(out, HostCatalogProject{
				Key:        unscopedProjectKey(result.ProjectKey),
				Name:       result.ProjectName,
				Root:       result.ProjectRoot,
				Workspaces: append([]workspaceinventory.Workspace(nil), result.Workspaces...),
			})
		}
		return out
	}
	if len(projects) == 0 {
		return nil
	}
	out := make([]HostCatalogProject, 0, len(projects))
	for _, project := range projects {
		out = append(out, HostCatalogProject{
			Key:  unscopedProjectKey(project.Key),
			Name: project.Name,
			Root: project.Path,
		})
	}
	return out
}

// unscopedProjectKey returns the host-side inventory key. hosts.ProjectResults
// stores ScopedKey(hostID, key); Destination.ProjectKey and this accessor must
// hand callers the unscoped form so later slices persist via
// hosts.ScopedKey(HostID, ProjectKey) without double-scoping.
func unscopedProjectKey(key string) string {
	if _, rest, ok := hosts.SplitScopedKey(key); ok {
		return rest
	}
	return key
}
