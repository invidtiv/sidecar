package uiapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func projectsFromCatalog(c mobileproto.CatalogSnapshot) workspacewire.Projects {
	result := workspacewire.Projects{Projects: []workspacewire.Project{}}
	seen := map[string]bool{}
	for _, section := range c.Sections {
		for _, row := range section.Rows {
			if !seen[row.ProjectID] {
				result.Projects = append(result.Projects, workspacewire.Project{Key: row.ProjectID, Name: row.ProjectName, Path: row.Path})
				seen[row.ProjectID] = true
			}
		}
	}
	return result
}
func fixtureWorkspace(c mobileproto.CatalogSnapshot, project string) (workspacewire.Workspace, error) {
	result := workspacewire.Workspace{Shells: []workspacewire.ShellRecord{}}
	for _, p := range projectsFromCatalog(c).Projects {
		if p.Key == project {
			result.Project = p
			break
		}
	}
	if result.Project.Key == "" {
		return result, &OperationError{Code: "project", Message: fmt.Sprintf("No project %q exists in these fixtures.", project), ExitCode: 3}
	}
	result.Catalog = c
	result.Catalog.Sections = []mobileproto.CatalogSection{}
	result.Catalog.Total = 0
	for _, section := range c.Sections {
		section.Rows = append([]mobileproto.CatalogRow(nil), section.Rows...)
		rows := section.Rows[:0]
		for _, row := range section.Rows {
			if row.ProjectID == project {
				rows = append(rows, row)
			}
		}
		if len(rows) > 0 {
			section.Rows = rows
			result.Catalog.Sections = append(result.Catalog.Sections, section)
			result.Catalog.Total += len(rows)
		}
	}
	return result, nil
}
func (b *FixtureBackend) Projects(_ context.Context, host string) (workspacewire.Projects, error) {
	if host != "" && host != "local" {
		return workspacewire.Projects{}, &OperationError{Code: "host_unavailable", Message: "Fixture workspaces are local; omit host.", ExitCode: 3}
	}
	projects := projectsFromCatalog(b.catalog)
	if b.workspace != nil {
		for i, p := range projects.Projects {
			if p.Key == b.workspace.Project.Key {
				projects.Projects[i] = b.workspace.Project
				return projects, nil
			}
		}
		projects.Projects = append(projects.Projects, b.workspace.Project)
	}
	return projects, nil
}
func (b *FixtureBackend) Workspace(ctx context.Context, project, host string, q mobileproto.CatalogQuery) (workspacewire.Workspace, error) {
	if _, err := b.Projects(ctx, host); err != nil {
		return workspacewire.Workspace{}, err
	}
	c, err := b.Sessions(ctx, q)
	if err != nil {
		return workspacewire.Workspace{}, err
	}
	// Resolve a configured project independently of filters, even if its rows are empty.
	result, err := fixtureWorkspace(b.catalog, project)
	if err != nil {
		return result, err
	}
	filtered, _ := fixtureWorkspace(c, project)
	result.Catalog = filtered.Catalog
	if result.Catalog.HubID == "" {
		result.Catalog = c
		result.Catalog.Sections = []mobileproto.CatalogSection{}
		result.Catalog.Total = 0
	}
	if b.workspace != nil && b.workspace.Project.Key == project {
		result.Project = b.workspace.Project
		result.Shells = append([]workspacewire.ShellRecord{}, b.workspace.Shells...)
	}
	return result, nil
}
func (*FixtureBackend) WorkspaceOperation(context.Context, string, WorkspaceCommand) (json.RawMessage, int, error) {
	return nil, 5, &OperationError{Code: "unsupported", Message: "Fixture workspaces are read-only; run the isolated workspace proof for writes.", ExitCode: 5}
}

func (b *FixtureBackend) WorkspaceInvalidation(ctx context.Context) (workspacewire.WorkspaceEvent, error) {
	projects, err := b.Projects(ctx, "")
	event := workspacewire.WorkspaceEvent{Projects: projects, Workspaces: []workspacewire.WorkspaceRef{}}
	for _, p := range projects.Projects {
		event.Workspaces = append(event.Workspaces, workspacewire.WorkspaceRef{Project: p.Key})
	}
	return event, err
}
