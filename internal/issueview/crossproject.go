package issueview

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/tdroot"
)

// Cross-project resolution: when a `td-*` link misses in the current project,
// sidecar searches its other configured projects and opens the card from the
// store that owns it. This file is the state-free core of that search — no
// Bubble Tea, no host state — so a headless caller could adopt it unchanged.
//
// The rules it implements are settled product decisions (see
// docs/plans/active/cross-project-issue-links.md): candidates come from
// sidecar's own project registry resolved through tdroot, worktrees sharing a
// td database collapse to one candidate, stores without a database are pruned
// without spawning td, and the fan-out is concurrent with first success wins.

const (
	// maxCrossProjectWorkers caps concurrent `td show` subprocesses. Six
	// covers any realistic project registry without stampeding the machine.
	maxCrossProjectWorkers = 6
)

// crossProjectTimeout bounds the whole fan-out. A click must answer in a
// click's worth of patience even when several stores are slow or gone. It is
// a variable so tests can shrink it.
var crossProjectTimeout = 4 * time.Second

// ProjectRef is one searchable project: display name plus any directory in it.
// Hosts map their config's project list to these at click time; resolving them
// into candidates is this package's job.
type ProjectRef struct {
	Name string
	Root string
}

// SearchCandidate is a deduped, verified place to look for an issue: its root
// has been resolved through [tdroot.ResolveTDRoot], shown not to be the
// current project, and stat-checked for a td database.
type SearchCandidate struct {
	Name string
	Root string
}

// Owner names the project a cross-project result came from. A nil Owner on a
// fetch result means local — the ordinary path.
type Owner struct {
	Name string
	Root string
}

// ProjectRefsFromConfig maps sidecar's configured projects to searchable refs.
// All three hosts (workspace panes, the app preview modal, the global overview)
// call this at click time through their own [Model.FallbackRefs] handler; it is
// pure mapping over config, so it is safe on the update goroutine.
func ProjectRefsFromConfig(cfg *config.Config) []ProjectRef {
	if cfg == nil || len(cfg.Projects.List) == 0 {
		return nil
	}
	refs := make([]ProjectRef, 0, len(cfg.Projects.List))
	for _, project := range cfg.Projects.List {
		path := config.ExpandPath(project.Path)
		if path == "" {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		name := project.Name
		if name == "" {
			name = filepath.Base(path)
		}
		refs = append(refs, ProjectRef{Name: name, Root: path})
	}
	return refs
}

// hit pairs a winning candidate with its fetched issue.
type hit struct {
	Cand SearchCandidate
	Data *Data
}

// BuildCandidates resolves each ref through tdroot.ResolveTDRoot, skips the
// current project's resolved root (local-first is tried before this runs and
// must not be searched twice), dedupes shared roots with the first name
// winning, and drops candidates whose .todos/issues.db does not exist — a
// stat, free next to the git spawn ResolveTDRoot may need.
//
// Filesystem reads only; no td processes. Callers run it inside commands
// spawned by user action, never on the startup path.
func BuildCandidates(currentRoot string, refs []ProjectRef) []SearchCandidate {
	if len(refs) == 0 {
		return nil
	}
	current := ""
	if currentRoot != "" {
		if abs, err := filepath.Abs(currentRoot); err == nil {
			current = filepath.Clean(abs)
		} else {
			current = filepath.Clean(currentRoot)
		}
	}
	seen := make(map[string]bool)
	var out []SearchCandidate
	for _, ref := range refs {
		if ref.Name == "" || ref.Root == "" {
			continue
		}
		resolved := tdroot.ResolveTDRoot(ref.Root)
		if resolved == "" {
			continue
		}
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
		resolved = filepath.Clean(resolved)
		if current != "" && resolved == current {
			continue
		}
		if seen[resolved] {
			continue
		}
		if _, err := os.Stat(filepath.Join(resolved, tdroot.TodosDir, tdroot.DBFile)); err != nil {
			continue
		}
		seen[resolved] = true
		out = append(out, SearchCandidate{Name: ref.Name, Root: resolved})
	}
	return out
}

// findAcrossProjects fans out showIssue over the candidates concurrently,
// bounded by min(maxCrossProjectWorkers, len(cands)) workers and an overall
// timeout. The first success wins and cancels the rest; nil means every
// candidate missed or time ran out.
//
// Duplicate issue IDs across projects are accepted as extremely rare, and
// first completion wins is declared policy rather than an accident.
func findAcrossProjects(ctx context.Context, issueID string, cands []SearchCandidate) *hit {
	if len(cands) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, crossProjectTimeout)
	defer cancel()

	limit := min(maxCrossProjectWorkers, len(cands))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	results := make(chan *hit, len(cands))
	for _, cand := range cands {
		cand := cand
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			data, err := showIssueContext(ctx, cand.Root, issueID)
			if err != nil || data == nil || data.ID == "" {
				return
			}
			results <- &hit{Cand: cand, Data: data}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	for h := range results {
		// First completion wins; the deferred cancel above stops the losers'
		// subprocesses through their command contexts.
		return h
	}
	return nil
}
