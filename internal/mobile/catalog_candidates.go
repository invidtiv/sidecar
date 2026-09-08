package mobile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const candidateSelectorPrefix = "candidate_v0_"

// WorkspaceCandidates projects the current source-owned pane set into opaque,
// deterministic selectors. ExpectedTarget remains empty until the owning
// resolver revalidates the exact pane and terminal incarnation.
func WorkspaceCandidates(workspace workspaceinventory.Workspace, ownerHostID string) (string, []mobileproto.CatalogCandidate, error) {
	if workspace.ID == "" || workspace.Kind != workspaceinventory.KindWorktree || ownerHostID == "" {
		return "", nil, fmt.Errorf("mobile candidates: incomplete worktree identity")
	}
	panes := append([]workspaceinventory.TerminalCandidate(nil), workspace.TerminalCandidates...)
	sort.Slice(panes, func(i, j int) bool {
		if panes[i].Session != panes[j].Session {
			return panes[i].Session < panes[j].Session
		}
		return panes[i].Pane < panes[j].Pane
	})
	if len(panes) == 0 {
		return "", nil, nil
	}
	if len(panes) > mobileproto.MaxCandidatesPerRow {
		return "", nil, &ResolveError{Code: mobileproto.ErrorOverflow, Message: fmt.Sprintf("workspace has more than %d terminal candidates", mobileproto.MaxCandidatesPerRow)}
	}
	parts := []string{ownerHostID, workspace.ID, canonicalCatalogPath(workspace.ProjectKey), canonicalCatalogPath(workspace.Path), string(workspace.Kind)}
	seen := make(map[string]bool, len(panes))
	for _, pane := range panes {
		if pane.Session == "" || pane.Pane == "" {
			return "", nil, fmt.Errorf("mobile candidates: incomplete pane identity")
		}
		key := pane.Session + "\x00" + pane.Pane
		if seen[key] {
			return "", nil, &ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "workspace contains duplicate terminal candidate identity"}
		}
		seen[key] = true
		parts = append(parts, pane.Session, pane.Pane)
	}
	setSum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	setGeneration := hex.EncodeToString(setSum[:16])
	candidates := make([]mobileproto.CatalogCandidate, 0, len(panes))
	for _, pane := range panes {
		selectorSum := sha256.Sum256([]byte(strings.Join([]string{setGeneration, ownerHostID, workspace.ID, pane.Session, pane.Pane}, "\x00")))
		label := strings.TrimSpace(pane.Title)
		if label == "" {
			label = strings.TrimSpace(pane.Command)
		}
		if label == "" {
			label = pane.Session + " " + pane.Pane
		}
		candidates = append(candidates, mobileproto.CatalogCandidate{
			Selector: candidateSelectorPrefix + hex.EncodeToString(selectorSum[:16]), DisplayName: label,
			OwnerHostID: ownerHostID, WorkspaceID: workspace.ID, WorkspaceKind: string(workspace.Kind),
			Session: pane.Session, Pane: pane.Pane,
		})
	}
	return setGeneration, candidates, nil
}

func IsCandidateSelector(value string) bool {
	if !strings.HasPrefix(value, candidateSelectorPrefix) || len(value) != len(candidateSelectorPrefix)+32 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, candidateSelectorPrefix))
	return err == nil
}

type CandidateInspector func(context.Context, string) (tty.HeadlessTargetIdentity, error)
type CandidateWorkspaceProvider func(context.Context, ResolvedTarget) (workspaceinventory.Workspace, error)

// ResolveCatalogCandidate re-enumerates the complete current source set and
// matches one deterministic selector. It never falls back to a residual
// Workspace PaneID/TmuxName or to another same-named pane.
func ResolveCatalogCandidate(ctx context.Context, input CatalogInput, ownerHostID, selector string, inspect CandidateInspector) (ResolvedTarget, error) {
	if !IsCandidateSelector(selector) || inspect == nil || ownerHostID == "" {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate selector is invalid"}
	}
	type match struct {
		workspace  workspaceinventory.Workspace
		candidate  mobileproto.CatalogCandidate
		generation string
	}
	var found *match
	for _, project := range input.Projects {
		if project.Stale || project.Result.Err != nil {
			continue
		}
		for _, workspace := range project.Result.Workspaces {
			if workspace.Kind != workspaceinventory.KindWorktree || len(workspace.TerminalCandidates) == 0 {
				continue
			}
			generation, candidates, err := WorkspaceCandidates(workspace, ownerHostID)
			if err != nil {
				return ResolvedTarget{}, err
			}
			for _, candidate := range candidates {
				if candidate.Selector != selector {
					continue
				}
				if found != nil {
					return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "terminal candidate selector matches multiple source workspaces"}
				}
				found = &match{workspace: workspace, candidate: candidate, generation: generation}
			}
		}
	}
	if found == nil {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate set changed"}
	}
	identity, err := inspect(ctx, found.candidate.Pane)
	if err != nil {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorUnsupported, Message: err.Error()}
	}
	if identity.Session != found.candidate.Session || identity.Pane != found.candidate.Pane {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate pane identity changed"}
	}
	root := found.workspace.Path
	if root == "" {
		root = found.workspace.ProjectRoot
	}
	return ResolvedTarget{WorkspaceID: found.workspace.ID, WorkspaceKind: string(found.workspace.Kind), ProjectRoot: canonicalCatalogPath(root),
		Selector: selector, SourceGeneration: found.generation, SourceProjectKey: canonicalCatalogPath(found.workspace.ProjectKey),
		SourceWorkspacePath: canonicalCatalogPath(root), Session: identity.Session, Pane: identity.Pane,
		DisplayName: found.candidate.DisplayName, ServerPID: identity.ServerPID, SessionID: identity.SessionID,
		SessionCreated: identity.SessionCreated, Width: identity.Width, Height: identity.Height, PaneCount: identity.PaneCount}, nil
}

// RevalidateCatalogCandidate checks a bound worktree source before and after
// exact-pane inspection. The provider is intentionally narrower than a full
// catalog refresh: it needs only current configured-project membership, one
// worktree inventory and the current tmux pane list.
func RevalidateCatalogCandidate(ctx context.Context, previous ResolvedTarget, ownerHostID string, source CandidateWorkspaceProvider, inspect CandidateInspector) (ResolvedTarget, error) {
	if !IsCandidateSelector(previous.Selector) || previous.SourceGeneration == "" || ownerHostID == "" || source == nil || inspect == nil {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate authority is incomplete"}
	}
	before, err := source(ctx, previous)
	if err != nil {
		return ResolvedTarget{}, err
	}
	beforeCandidate, err := boundCandidate(previous, before, ownerHostID)
	if err != nil {
		return ResolvedTarget{}, err
	}
	identity, err := inspect(ctx, beforeCandidate.Pane)
	if err != nil {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorUnsupported, Message: err.Error()}
	}
	if identity.Session != beforeCandidate.Session || identity.Pane != beforeCandidate.Pane {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate pane identity changed"}
	}
	after, err := source(ctx, previous)
	if err != nil {
		return ResolvedTarget{}, err
	}
	afterCandidate, err := boundCandidate(previous, after, ownerHostID)
	if err != nil {
		return ResolvedTarget{}, err
	}
	if beforeCandidate.Selector != afterCandidate.Selector || beforeCandidate.Session != afterCandidate.Session || beforeCandidate.Pane != afterCandidate.Pane {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate set changed during revalidation"}
	}
	current := previous
	current.DisplayName = afterCandidate.DisplayName
	current.Session, current.Pane = identity.Session, identity.Pane
	current.ServerPID, current.SessionID, current.SessionCreated = identity.ServerPID, identity.SessionID, identity.SessionCreated
	current.Width, current.Height, current.PaneCount = identity.Width, identity.Height, identity.PaneCount
	return current, nil
}

func boundCandidate(previous ResolvedTarget, workspace workspaceinventory.Workspace, ownerHostID string) (mobileproto.CatalogCandidate, error) {
	root := workspace.Path
	if root == "" {
		root = workspace.ProjectRoot
	}
	if workspace.ID != previous.WorkspaceID || string(workspace.Kind) != previous.WorkspaceKind ||
		canonicalCatalogPath(workspace.ProjectKey) != previous.SourceProjectKey || canonicalCatalogPath(root) != previous.SourceWorkspacePath {
		return mobileproto.CatalogCandidate{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate source identity changed"}
	}
	generation, candidates, err := WorkspaceCandidates(workspace, ownerHostID)
	if err != nil {
		return mobileproto.CatalogCandidate{}, err
	}
	if generation != previous.SourceGeneration {
		return mobileproto.CatalogCandidate{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate set changed"}
	}
	var found *mobileproto.CatalogCandidate
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.Selector != previous.Selector {
			continue
		}
		if found != nil {
			return mobileproto.CatalogCandidate{}, &ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "terminal candidate selector is duplicated"}
		}
		found = candidate
	}
	if found == nil || found.Session != previous.Session || found.Pane != previous.Pane {
		return mobileproto.CatalogCandidate{}, &ResolveError{Code: mobileproto.ErrorIdentityChanged, Message: "terminal candidate is no longer a member of its source workspace"}
	}
	return *found, nil
}
