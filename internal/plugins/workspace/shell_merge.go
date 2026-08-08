package workspace

import (
	"sort"
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
}

// mergeShellState reconciles a manifest with what this instance already knows
// and with what is actually alive on its tmux server. The manifest is a shared
// file: another instance — a sibling worktree, an isolated proof run — can
// rewrite it at any moment, so absence from it is weak evidence (td-8d18de).
//
// The rules:
//
//	(a) Every manifest definition yields a shell. An existing *ShellSession
//	    with the same tmux name is reused (so buffers and panes survive) with
//	    its display name, agent and skip-perms refreshed from the definition;
//	    otherwise a fresh session is built.
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

	for _, definition := range in.Manifest {
		seen[definition.TmuxName] = true
		running := in.Running[definition.TmuxName]
		if shell, ok := existing[definition.TmuxName]; ok {
			shell.Name = definition.DisplayName
			shell.ChosenAgent = definitionToAgentType(definition.AgentType)
			shell.SkipPerms = definition.SkipPerms
			shell.IsOrphaned = !running
			result.Shells = append(result.Shells, shell)
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
		result.Shells = append(result.Shells, shell)
		definition := shellToDefinition(shell)
		definition.Namespace = in.Namespace
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
		}
		result.Shells = append(result.Shells, shellSessionFromDefinition(definition, true, paneID))
		result.Restored = append(result.Restored, definition)
	}

	return result
}
