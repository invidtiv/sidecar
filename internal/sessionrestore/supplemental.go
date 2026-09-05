package sessionrestore

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// SupplementalCandidates reads worktree-session and terminal-split identities.
// They live outside project shells.json so the workspace inventories do not
// render them as duplicate managed-shell rows.
func (c Collector) SupplementalCandidates(prior []Shell) ([]Shell, error) {
	path := workspaceops.RecoverySessionsPath(c.StateDir)
	defs, err := shellstate.ListAtPath(path)
	if err != nil {
		return nil, err
	}
	out := make([]Shell, 0, len(defs))
	seen := map[string]bool{}
	sessions, workspaces, err := c.persistedPaneLayouts()
	if err != nil {
		return nil, err
	}
	layouts := sessions
	if layouts == nil {
		layouts = map[string]*state.PaneLayoutJSON{}
	}
	for surface, layout := range workspaces {
		layouts[surface] = layout
	}
	openSplits := map[string]bool{}
	for _, layout := range layouts {
		if layout == nil || !layout.Open || (layout.HostID != "" && layout.HostID != "local") {
			continue
		}
		walkLayout(layout, func(leaf *state.PaneLayoutJSON) {
			if leaf.Kind == "shell" && strings.HasPrefix(leaf.Session, "sidecar-tp-") {
				openSplits[leaf.Session] = true
			}
		})
	}
	for _, def := range defs {
		if c.Namespace != "" && def.Namespace != c.Namespace {
			continue
		}
		if !strings.HasPrefix(def.TmuxName, workspaceops.WorktreeSessionPrefix) && !strings.HasPrefix(def.TmuxName, "sidecar-tp-") {
			continue
		}
		if strings.HasPrefix(def.TmuxName, "sidecar-tp-") && !openSplits[def.TmuxName] {
			continue
		}
		if def.Restore != nil && def.Restore.ServerLostAt.IsZero() {
			if lostAt := lossForServer(prior, def.Restore.LastSeenServer); !lostAt.IsZero() {
				copy := *def.Restore
				copy.ServerLostAt = lostAt
				def.Restore = &copy
			}
		}
		project := filepath.Base(filepath.Clean(def.WorkDir))
		out = append(out, Shell{
			Project: project, ProjectRoot: def.WorkDir,
			ManifestPath: path, Def: def,
		})
		seen[def.TmuxName] = true
	}
	for _, layout := range layouts {
		if layout == nil || !layout.Open || (layout.HostID != "" && layout.HostID != "local") {
			continue
		}
		server, createdAt, lostAt := priorServerForLayout(prior, layout)
		if server == "" {
			continue
		}
		if layout.WorkspaceKind == "worktree" && strings.TrimSpace(layout.Root) != "" {
			name := workspaceops.WorktreeSessionName(layout.Root, filepath.Base(layout.Root))
			appendLegacyCandidate(&out, seen, path, name, filepath.Base(layout.Root), layout.Root, layout.ProjectRoot, c.Namespace, server, createdAt, lostAt)
		}
		walkLayout(layout, func(leaf *state.PaneLayoutJSON) {
			if leaf.Kind == "shell" && strings.HasPrefix(leaf.Session, "sidecar-tp-") {
				appendLegacyCandidate(&out, seen, path, leaf.Session, leaf.Name, layout.Root, layout.ProjectRoot, c.Namespace, server, createdAt, lostAt)
			}
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Def.TmuxName < out[j].Def.TmuxName })
	return out, nil
}

func (c Collector) persistedPaneLayouts() (map[string]*state.PaneLayoutJSON, map[string]*state.PaneLayoutJSON, error) {
	if c.LayoutStateDir != "" {
		return state.ReadPaneLayoutsWithDir(c.LayoutStateDir)
	}
	return state.GetSessionsPaneLayouts(), state.GetWorkspacePaneLayouts(), nil
}

func lossForServer(prior []Shell, server string) time.Time {
	if server == "" {
		return time.Time{}
	}
	for _, sh := range prior {
		if sh.Def.Restore != nil && sh.Def.Restore.LastSeenServer == server && !sh.Def.Restore.ServerLostAt.IsZero() {
			return sh.Def.Restore.ServerLostAt
		}
	}
	return time.Time{}
}

func priorServerForLayout(prior []Shell, layout *state.PaneLayoutJSON) (string, time.Time, time.Time) {
	for _, sh := range prior {
		if sh.Def.Restore == nil || !sh.Def.Restore.Eligible || sh.Def.Restore.ServerLostAt.IsZero() {
			continue
		}
		if layout.ProjectRoot != "" && sh.ProjectRoot == layout.ProjectRoot {
			return sh.Def.Restore.LastSeenServer, sh.Def.CreatedAt, sh.Def.Restore.ServerLostAt
		}
	}
	return "", time.Time{}, time.Time{}
}

func appendLegacyCandidate(out *[]Shell, seen map[string]bool, manifestPath, session, name, workDir, projectRoot, namespace, server string, createdAt, lostAt time.Time) {
	if session == "" || seen[session] || workDir == "" {
		return
	}
	if name == "" {
		name = filepath.Base(workDir)
	}
	def := shellstate.Definition{TmuxName: session, DisplayName: name, Namespace: namespace, WorkDir: workDir, CreatedAt: createdAt,
		Restore: &shellstate.RestoreState{Eligible: true, LastSeenServer: server, LastSeenAliveAt: lostAt, ServerLostAt: lostAt}}
	*out = append(*out, Shell{Project: filepath.Base(projectRoot), ProjectRoot: projectRoot, ManifestPath: manifestPath, Def: def, Synthesized: true})
	seen[session] = true
}

func walkLayout(layout *state.PaneLayoutJSON, visit func(*state.PaneLayoutJSON)) {
	if layout == nil {
		return
	}
	if layout.Split == nil {
		visit(layout)
		return
	}
	walkLayout(layout.Split.A, visit)
	walkLayout(layout.Split.B, visit)
}
