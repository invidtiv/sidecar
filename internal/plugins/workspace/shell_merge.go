package workspace

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// shellMergeInput is everything mergeShellState needs. It carries no plugin
// state so the merge rules stay testable without tmux or a Bubble Tea program.
type shellMergeInput struct {
	Existing  []*ShellSession // current in-memory shells (nil at startup)
	Manifest  []ShellDefinition
	Running   map[string]bool // live sessions in THIS instance's tmux namespace
	PaneID    func(string) string
	WorkDir   string
	Namespace string
	Now       func() time.Time
}

type shellMergeResult struct {
	Shells   []*ShellSession   // the union, in stable order
	Restored []ShellDefinition // must be written back into the manifest
	Dropped  []string          // tmux names to purge from caches
	// Revived are shells that just came back to life and had an Agent built
	// for them. The caller owns them now: mark them managed and poll them.
	Revived []*ShellSession
}

// mergeShellState reconciles a manifest with what this instance already knows
// and with what is actually alive on its tmux server. The manifest is a shared
// file: another instance — a sibling worktree, an isolated proof run — can
// rewrite it at any moment, so absence from it is weak evidence (td-8d18de).
//
// The rules:
//
//	(a) Every manifest definition this working directory could own yields a
//	    shell. An existing *ShellSession with the same tmux name is reused (so
//	    buffers and panes survive) with its display name, agent and skip-perms
//	    refreshed from the definition; otherwise a fresh session is built. A
//	    definition that is neither live here nor matchable by this workDir's
//	    discovery pattern is kept in the manifest but not displayed.
//	(b) An existing shell absent from the manifest but still Running here is
//	    KEPT — appended after the manifest-ordered shells — and reported in
//	    Restored so the caller can heal the file. This is the exact eviction
//	    the bug exercised: a foreign instance's narrower manifest must not
//	    unmount a session this instance can see alive.
//	(c) An existing shell absent from the manifest and not running is Dropped.
//	    That preserves propagation of explicit deletes, which are the only
//	    writers allowed to remove entries.
//	(d) A running session in neither the manifest nor Existing is adopted as a
//	    new shell and reported in Restored. Adoptions are processed in sorted
//	    order so results are deterministic.
func mergeShellState(in shellMergeInput) shellMergeResult {
	now := in.Now
	if now == nil {
		now = time.Now
	}
	paneID := in.PaneID
	if paneID == nil {
		paneID = func(string) string { return "" }
	}

	existing := make(map[string]*ShellSession, len(in.Existing))
	for _, shell := range in.Existing {
		existing[shell.TmuxName] = shell
	}

	result := shellMergeResult{}
	seen := make(map[string]bool, len(in.Manifest))

	// Visibility is a narrower question than survival. A definition survives in
	// the manifest whenever we cannot prove it died; it belongs on *this*
	// instance's screen only when its name is one this working directory could
	// have produced. Sibling worktrees share one shells.json, so without this
	// split every worktree would list its siblings' shells as offline rows that
	// can never be opened (td-8d18de).
	visible := shellDiscoveryPattern(in.WorkDir)

	for _, definition := range in.Manifest {
		seen[definition.TmuxName] = true
		running := in.Running[definition.TmuxName]
		if shell, ok := existing[definition.TmuxName]; ok {
			// Same visibility split as a fresh definition. A leaked sibling
			// pointer in Existing must not stay on this workDir's Shells list
			// as a fake offline row (td-4819be / td-8d18de).
			if !running && !visible.MatchString(definition.TmuxName) {
				continue
			}
			shell.Name = definition.DisplayName
			shell.ChosenAgent = definitionToAgentType(definition.AgentType)
			shell.SkipPerms = definition.SkipPerms
			shell.IsOrphaned = !running
			if definition.WorkDir != "" {
				shell.WorkDir = definition.WorkDir
			} else if shell.WorkDir == "" {
				shell.WorkDir = in.WorkDir
			}
			// A shell that comes back to life needs an Agent, or it renders as
			// live while every open path refuses it: enterInteractiveMode wants
			// an Agent, recreateOrphanedShell only handles orphans, and no
			// later sync would ever repair it (td-8d18de).
			if running && shell.Agent == nil {
				attachAgentToShell(shell, definition, paneID)
				result.Revived = append(result.Revived, shell)
			}
			result.Shells = append(result.Shells, shell)
			continue
		}
		if !running && !visible.MatchString(definition.TmuxName) {
			// Retained for persistence only: it belongs to a sibling worktree
			// or another tmux server. Keeping it in the manifest is the fix;
			// showing it as an offline shell we could never open is not.
			continue
		}
		result.Shells = append(result.Shells, shellSessionFromDefinition(definition, running, paneID))
	}

	for _, shell := range in.Existing {
		if seen[shell.TmuxName] {
			continue
		}
		if !in.Running[shell.TmuxName] {
			result.Dropped = append(result.Dropped, shell.TmuxName)
			continue
		}
		shell.IsOrphaned = false
		if shell.WorkDir == "" {
			shell.WorkDir = in.WorkDir
		}
		result.Shells = append(result.Shells, shell)
		definition := shellToDefinition(shell)
		definition.Namespace = in.Namespace
		if definition.WorkDir == "" {
			definition.WorkDir = in.WorkDir
		}
		result.Restored = append(result.Restored, definition)
	}

	adopted := make([]string, 0, len(in.Running))
	for name := range in.Running {
		if seen[name] || existing[name] != nil {
			continue
		}
		adopted = append(adopted, name)
	}
	sort.Strings(adopted)
	for _, name := range adopted {
		definition := ShellDefinition{
			TmuxName:    name,
			DisplayName: deriveShellDisplayName(in.WorkDir, name),
			Namespace:   in.Namespace,
			CreatedAt:   now(),
			WorkDir:     in.WorkDir,
		}
		result.Shells = append(result.Shells, shellSessionFromDefinition(definition, true, paneID))
		result.Restored = append(result.Restored, definition)
	}

	return result
}

func sameWorkDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// inferDefinitionWorkDir resolves a shell's parent worktree. An explicit
// WorkDir wins. Otherwise a unique basename match against known worktree
// paths, or — for old manifests — the current workDir when the session name
// could only have been produced there.
func inferDefinitionWorkDir(def ShellDefinition, worktreePaths []string, currentWorkDir string) string {
	if dir := strings.TrimSpace(def.WorkDir); dir != "" {
		return filepath.Clean(dir)
	}
	var matches []string
	seen := make(map[string]bool, len(worktreePaths))
	for _, path := range worktreePaths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		if shellDiscoveryPattern(path).MatchString(def.TmuxName) {
			matches = append(matches, clean)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	if currentWorkDir != "" && shellDiscoveryPattern(currentWorkDir).MatchString(def.TmuxName) {
		return filepath.Clean(currentWorkDir)
	}
	return ""
}

// groupManifestShellsByWorkDir is the nest projection of the full manifest.
// It does not decide what belongs in this workDir's Shells section.
func groupManifestShellsByWorkDir(
	defs []ShellDefinition,
	worktreePaths []string,
	currentWorkDir string,
	paneID func(string) string,
) map[string][]*ShellSession {
	if paneID == nil {
		paneID = func(string) string { return "" }
	}
	groups := make(map[string][]*ShellSession)
	for _, def := range defs {
		workDir := inferDefinitionWorkDir(def, worktreePaths, currentWorkDir)
		if workDir == "" {
			continue
		}
		// Sibling liveness is not this workDir's Running set. Give the row a
		// session target so attach is by TmuxName, never panesForPath.
		shell := shellSessionFromDefinition(def, true, paneID)
		shell.WorkDir = workDir
		shell.IsOrphaned = false
		groups[workDir] = append(groups[workDir], shell)
	}
	return groups
}
