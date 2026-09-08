package mobilehub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

const publicTargetPrefix = "hub_target_v0_"

// CatalogAuthority identifies the hub configuration and stable owner
// registration that authorize one public catalog projection.
type CatalogAuthority struct {
	HubID, HubConfigGeneration, OwnerHostID, RegistrationFingerprint string
}

// TargetBinding retains the raw owner identity behind one public opaque
// selector. It is process-local acceleration only. A fresh hub process
// reconstructs the same binding from a current owner catalog and exact-matches
// PublicExpected before forwarding reconnect.
type TargetBinding struct {
	PublicSelector string
	PublicExpected mobileproto.TargetIdentity
	OwnerSelector  string
	OwnerExpected  mobileproto.TargetIdentity
}

// RemappedCatalog is one owner source ready for shared hub composition.
type RemappedCatalog struct {
	Source   mobile.CatalogSource
	Bindings map[string]TargetBinding
}

// RemapOwnerCatalog converts one current owner's unfiltered project snapshot
// into stable hub-public identities. Raw owner handles and identities never
// leave this boundary.
func RemapOwnerCatalog(authority CatalogAuthority, snapshot mobileproto.CatalogSnapshot) (RemappedCatalog, error) {
	if authority.HubID == "" || authority.HubConfigGeneration == "" || authority.OwnerHostID == "" || authority.RegistrationFingerprint == "" ||
		snapshot.HubID == "" || snapshot.OwnerHostID == "" || snapshot.OwnerConfigGeneration == "" {
		return RemappedCatalog{}, fmt.Errorf("mobile hub: incomplete catalog authority")
	}
	if strings.ToLower(snapshot.Query.Sort) != "project" || snapshot.Query.Search != "" || len(snapshot.Query.Hosts) != 0 || len(snapshot.Query.Providers) != 0 || len(snapshot.Query.States) != 0 ||
		(snapshot.Query.ShowIdleSessions != nil && !*snapshot.Query.ShowIdleSessions) {
		return RemappedCatalog{}, fmt.Errorf("mobile hub: owner catalog must be an unfiltered project snapshot")
	}
	if err := validateRawOwnerCatalog(snapshot); err != nil {
		return RemappedCatalog{}, err
	}
	if err := mobileproto.ValidateCatalogCandidates(snapshot); err != nil {
		return RemappedCatalog{}, fmt.Errorf("mobile hub: invalid owner candidates: %w", err)
	}
	public := snapshot
	public.HubID = authority.HubID
	public.OwnerHostID = authority.OwnerHostID
	public.Hosts = []mobileproto.CatalogHost{{ID: authority.OwnerHostID, State: "online"}}
	public.Sections = append([]mobileproto.CatalogSection(nil), snapshot.Sections...)
	bindings := make(map[string]TargetBinding)
	seenRows := make(map[string]bool)
	for sectionIndex := range public.Sections {
		public.Sections[sectionIndex].Rows = append([]mobileproto.CatalogRow(nil), snapshot.Sections[sectionIndex].Rows...)
		for rowIndex := range public.Sections[sectionIndex].Rows {
			raw := snapshot.Sections[sectionIndex].Rows[rowIndex]
			row, rowBindings, err := remapCatalogRow(authority, snapshot.OwnerHostID, raw)
			if err != nil {
				return RemappedCatalog{}, err
			}
			if seenRows[row.ID] {
				return RemappedCatalog{}, fmt.Errorf("mobile hub: remapped owner catalog has duplicate row %q", row.ID)
			}
			seenRows[row.ID] = true
			for _, binding := range rowBindings {
				if _, exists := bindings[binding.PublicSelector]; exists {
					return RemappedCatalog{}, fmt.Errorf("mobile hub: remapped owner catalog has duplicate selector")
				}
				bindings[binding.PublicSelector] = binding
			}
			public.Sections[sectionIndex].Rows[rowIndex] = row
		}
	}
	if err := mobileproto.ValidateCatalogCandidates(public); err != nil {
		return RemappedCatalog{}, fmt.Errorf("mobile hub: invalid public candidates: %w", err)
	}
	return RemappedCatalog{Source: mobile.CatalogSource{OwnerHostID: authority.OwnerHostID, Snapshot: public}, Bindings: bindings}, nil
}

func validateRawOwnerCatalog(snapshot mobileproto.CatalogSnapshot) error {
	seenHosts := make(map[string]bool, len(snapshot.Hosts))
	ownerOnline := false
	for _, host := range snapshot.Hosts {
		if host.ID == "" || seenHosts[host.ID] {
			return fmt.Errorf("mobile hub: owner catalog contains invalid or duplicate host identity")
		}
		seenHosts[host.ID] = true
		if host.ID == snapshot.OwnerHostID && strings.ToLower(host.State) == "online" {
			ownerOnline = true
		}
	}
	if !ownerOnline {
		return fmt.Errorf("mobile hub: catalog owner is absent or not online")
	}
	for _, section := range snapshot.Sections {
		for _, row := range section.Rows {
			if row.OwnerHostID != snapshot.OwnerHostID {
				return fmt.Errorf("mobile hub: raw catalog row belongs to another owner")
			}
			if row.ExpectedTarget != nil && !rawTargetMatchesSnapshot(snapshot, *row.ExpectedTarget) {
				return fmt.Errorf("mobile hub: raw row target disagrees with owner catalog authority")
			}
			for _, candidate := range row.Candidates {
				if !rawTargetMatchesSnapshot(snapshot, candidate.ExpectedTarget) {
					return fmt.Errorf("mobile hub: raw candidate target disagrees with owner catalog authority")
				}
			}
		}
	}
	return nil
}

func rawTargetMatchesSnapshot(snapshot mobileproto.CatalogSnapshot, target mobileproto.TargetIdentity) bool {
	return target.HubID == snapshot.HubID && target.OwnerHostID == snapshot.OwnerHostID &&
		target.OwnerConfigGeneration == snapshot.OwnerConfigGeneration
}

func remapCatalogRow(authority CatalogAuthority, rawOwnerHostID string, raw mobileproto.CatalogRow) (mobileproto.CatalogRow, []TargetBinding, error) {
	if raw.ID == "" || raw.OwnerHostID != rawOwnerHostID || raw.ProjectID == "" || raw.WorkspaceKind == "" {
		return mobileproto.CatalogRow{}, nil, fmt.Errorf("mobile hub: incomplete raw catalog row identity")
	}
	row := raw
	row.ID = hosts.ScopedKey(authority.OwnerHostID, raw.ID)
	row.OwnerHostID = authority.OwnerHostID
	row.ProjectID = hosts.ScopedKey(authority.OwnerHostID, raw.ProjectID)
	if raw.WorkspaceID != "" {
		row.WorkspaceID = hosts.ScopedKey(authority.OwnerHostID, raw.WorkspaceID)
	}
	if raw.CandidateGeneration != "" {
		row.CandidateGeneration = publicDigest("candidate-set", authority, raw.CandidateGeneration)
	}
	row.Candidates = append([]mobileproto.CatalogCandidate(nil), raw.Candidates...)
	bindings := make([]TargetBinding, 0, len(raw.Candidates)+1)
	for i, candidate := range raw.Candidates {
		if candidate.OwnerHostID != rawOwnerHostID || candidate.WorkspaceID != raw.WorkspaceID || candidate.WorkspaceKind != raw.WorkspaceKind {
			return mobileproto.CatalogRow{}, nil, fmt.Errorf("mobile hub: candidate does not match its raw owner row")
		}
		publicExpected, err := publicTargetIdentity(authority, rawOwnerHostID, candidate.ExpectedTarget)
		if err != nil {
			return mobileproto.CatalogRow{}, nil, err
		}
		publicSelector := publicTargetSelector(authority, candidate.Selector, candidate.ExpectedTarget)
		row.Candidates[i].Selector = publicSelector
		row.Candidates[i].OwnerHostID = authority.OwnerHostID
		row.Candidates[i].WorkspaceID = hosts.ScopedKey(authority.OwnerHostID, candidate.WorkspaceID)
		row.Candidates[i].ExpectedTarget = publicExpected
		bindings = append(bindings, TargetBinding{PublicSelector: publicSelector, PublicExpected: publicExpected,
			OwnerSelector: candidate.Selector, OwnerExpected: candidate.ExpectedTarget})
	}
	if raw.AttachmentReady {
		if raw.ExpectedTarget == nil || raw.Target == "" {
			return mobileproto.CatalogRow{}, nil, fmt.Errorf("mobile hub: ready raw row has incomplete target authority")
		}
		publicExpected, err := publicTargetIdentity(authority, rawOwnerHostID, *raw.ExpectedTarget)
		if err != nil {
			return mobileproto.CatalogRow{}, nil, err
		}
		publicSelector := publicTargetSelector(authority, raw.Target, *raw.ExpectedTarget)
		row.Target, row.ExpectedTarget = publicSelector, &publicExpected
		if len(row.Candidates) == 1 {
			row.Target = row.Candidates[0].Selector
			row.ExpectedTarget = &row.Candidates[0].ExpectedTarget
		} else {
			bindings = append(bindings, TargetBinding{PublicSelector: publicSelector, PublicExpected: publicExpected,
				OwnerSelector: raw.Target, OwnerExpected: *raw.ExpectedTarget})
		}
	} else {
		row.Target, row.ExpectedTarget = "", nil
	}
	return row, bindings, nil
}

func publicTargetIdentity(authority CatalogAuthority, rawOwnerHostID string, raw mobileproto.TargetIdentity) (mobileproto.TargetIdentity, error) {
	if raw.HubID == "" || raw.OwnerHostID != rawOwnerHostID || raw.OwnerConfigGeneration == "" || raw.WorkspaceID == "" || raw.WorkspaceKind == "" ||
		raw.Session == "" || raw.Pane == "" || raw.ServerIncarnation == "" || raw.TargetGeneration == "" {
		return mobileproto.TargetIdentity{}, fmt.Errorf("mobile hub: incomplete raw target identity")
	}
	return mobileproto.TargetIdentity{
		HubID: authority.HubID, OwnerHostID: authority.OwnerHostID,
		OwnerConfigGeneration: publicDigest("owner-config", authority, raw.OwnerConfigGeneration),
		WorkspaceID:           hosts.ScopedKey(authority.OwnerHostID, raw.WorkspaceID),
		WorkspaceKind:         raw.WorkspaceKind, Session: raw.Session, Pane: raw.Pane,
		ServerIncarnation: raw.ServerIncarnation,
		TargetGeneration:  publicDigest("target", authority, targetIdentityDigestInput(raw)),
	}, nil
}

func publicTargetSelector(authority CatalogAuthority, rawSelector string, rawExpected mobileproto.TargetIdentity) string {
	return publicTargetPrefix + publicDigest("selector", authority, rawSelector, targetIdentityDigestInput(rawExpected))
}

func publicDigest(kind string, authority CatalogAuthority, values ...string) string {
	parts := []string{kind, authority.HubID, authority.HubConfigGeneration, authority.OwnerHostID, authority.RegistrationFingerprint}
	parts = append(parts, values...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func targetIdentityDigestInput(target mobileproto.TargetIdentity) string {
	return strings.Join([]string{target.HubID, target.OwnerHostID, target.OwnerConfigGeneration, target.WorkspaceID, target.WorkspaceKind,
		target.Session, target.Pane, target.ServerIncarnation, target.TargetGeneration}, "\x00")
}
