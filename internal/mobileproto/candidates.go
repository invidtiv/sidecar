package mobileproto

import "fmt"

// ValidateCatalogCandidates checks the cross-surface candidate contract on a
// complete snapshot. It does not authorize a selector; only the owning
// resolver can do that against current source state.
func ValidateCatalogCandidates(snapshot CatalogSnapshot) error {
	total := 0
	selectors := make(map[string]struct{})
	hosts := make(map[string]string, len(snapshot.Hosts))
	for _, host := range snapshot.Hosts {
		hosts[host.ID] = host.State
	}
	for _, section := range snapshot.Sections {
		for _, row := range section.Rows {
			if err := validateCandidateRow(snapshot.HubID, row, hosts, selectors); err != nil {
				return fmt.Errorf("catalog row %q: %w", row.ID, err)
			}
			total += len(row.Candidates)
			if total > MaxCatalogCandidates {
				return fmt.Errorf("catalog has more than %d terminal candidates", MaxCatalogCandidates)
			}
		}
	}
	return nil
}

func validateCandidateRow(hubID string, row CatalogRow, hosts map[string]string, selectors map[string]struct{}) error {
	if len(row.Candidates) == 0 {
		return nil
	}
	if len(row.Candidates) > MaxCandidatesPerRow {
		return fmt.Errorf("has more than %d terminal candidates", MaxCandidatesPerRow)
	}
	if hubID == "" || row.CandidateGeneration == "" || row.OwnerHostID == "" || row.WorkspaceID == "" || row.WorkspaceKind == "" || !row.Live || row.Stale {
		return fmt.Errorf("candidate parent identity is incomplete")
	}
	if hosts[row.OwnerHostID] != "online" {
		return fmt.Errorf("candidate owner is absent or not online in the catalog host set")
	}
	for _, candidate := range row.Candidates {
		if candidate.Selector == "" || len(candidate.Selector) > MaxTargetBytes || candidate.DisplayName == "" || candidate.Session == "" || candidate.Pane == "" {
			return fmt.Errorf("candidate identity is incomplete or unbounded")
		}
		if _, exists := selectors[candidate.Selector]; exists {
			return fmt.Errorf("candidate selector %q is duplicated", candidate.Selector)
		}
		selectors[candidate.Selector] = struct{}{}
		if candidate.OwnerHostID != row.OwnerHostID || candidate.WorkspaceID != row.WorkspaceID || candidate.WorkspaceKind != row.WorkspaceKind {
			return fmt.Errorf("candidate parent identity does not match its row")
		}
		expected := candidate.ExpectedTarget
		if expected.HubID != hubID || expected.OwnerHostID != candidate.OwnerHostID || expected.OwnerConfigGeneration == "" ||
			expected.WorkspaceID != candidate.WorkspaceID || expected.WorkspaceKind != candidate.WorkspaceKind ||
			expected.Session != candidate.Session || expected.Pane != candidate.Pane || expected.ServerIncarnation == "" || expected.TargetGeneration == "" {
			return fmt.Errorf("candidate expected target does not match its display identity")
		}
	}
	if len(row.Candidates) == 1 {
		candidate := row.Candidates[0]
		if !row.AttachmentReady || row.AttachState != "ready" || row.Ambiguous || row.Target != candidate.Selector || row.ExpectedTarget == nil || *row.ExpectedTarget != candidate.ExpectedTarget ||
			row.Session != candidate.Session || row.Pane != candidate.Pane {
			return fmt.Errorf("single candidate is not the row's exact direct authority")
		}
		return nil
	}
	if row.AttachmentReady || row.AttachState != "ambiguous" || !row.Ambiguous || row.Target != "" || row.ExpectedTarget != nil {
		return fmt.Errorf("multiple candidates must leave the parent row non-authoritative")
	}
	return nil
}
