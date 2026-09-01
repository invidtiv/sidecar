// Package managedtarget resolves a caller-facing shell name into one
// host-shaped Sidecar-owned target. It is state-free: callers supply the
// candidates they are authorized to expose, so local and future remote hosts
// share ambiguity and precedence rules without sharing storage.
package managedtarget

import (
	"fmt"
	"strings"
)

type Target struct {
	Host, Project, ProjectRoot, Kind, Session, Name, Namespace, WorkDir, WorktreeRoot, ManifestPath string
	Priority                                                                                        int
}

type Query struct{ Host, Project, Namespace, Value string }

type ErrorKind string

const (
	NotFound  ErrorKind = "not_found"
	Ambiguous ErrorKind = "ambiguous"
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

// Resolve prefers exact durable session identity over display name. Every
// filter is exact; an empty field means the caller has not scoped that axis.
func Resolve(candidates []Target, q Query) (Target, error) {
	filter := func(t Target) bool {
		return (q.Host == "" || t.Host == q.Host) && (q.Project == "" || t.Project == q.Project) && (q.Namespace == "" || t.Namespace == "" || t.Namespace == q.Namespace)
	}
	var exact, named []Target
	for _, t := range candidates {
		if !filter(t) {
			continue
		}
		if t.Session == q.Value {
			exact = append(exact, t)
		} else if t.Name == q.Value {
			named = append(named, t)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = named
	}
	matches = dedupeEquivalent(matches)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return Target{}, &Error{Kind: Ambiguous, Message: ambiguousMessage(q.Value, matches)}
	}
	return Target{}, &Error{Kind: NotFound, Message: fmt.Sprintf("no Sidecar-managed target named %q", strings.TrimSpace(q.Value))}
}

// ambiguousMessage names the projects a value matched in and the flag that
// picks one. The count alone was the whole message once, and a caller reading
// "ambiguous across 3 Sidecar sessions" had no way to tell which selector
// would resolve it — the flag that did was named only by a later, different
// refusal.
func ambiguousMessage(value string, matches []Target) string {
	projects := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		key := m.Project
		if key == "" {
			key = "(unknown project)"
		}
		if !seen[key] {
			seen[key] = true
			projects = append(projects, key)
		}
	}
	if len(projects) > 1 {
		return fmt.Sprintf("managed target %q is ambiguous across %d Sidecar projects (%s); pass --project NAME to pick one, or --shell NAME to resolve through that shell's project",
			value, len(projects), strings.Join(projects, ", "))
	}
	return fmt.Sprintf("managed target %q matches %d Sidecar sessions in project %q; address it by tmux session name",
		value, len(matches), projects[0])
}

// dedupeEquivalent applies registered/discovered precedence only to two
// records describing the same owned target. Priority must never resolve a
// collision across projects or between a shell and worktree: those are
// genuinely distinct targets and remain ambiguous to a global caller.
func dedupeEquivalent(matches []Target) []Target {
	type identity struct{ host, project, kind, session, namespace string }
	best := make(map[identity][]Target, len(matches))
	order := make([]identity, 0, len(matches))
	for _, match := range matches {
		key := identity{match.Host, match.Project, match.Kind, match.Session, match.Namespace}
		current, ok := best[key]
		if !ok {
			best[key] = []Target{match}
			order = append(order, key)
			continue
		}
		switch {
		case match.Priority < current[0].Priority:
			best[key] = []Target{match}
		case match.Priority == current[0].Priority:
			best[key] = append(current, match)
		}
	}
	out := make([]Target, 0, len(order))
	for _, key := range order {
		out = append(out, best[key]...)
	}
	return out
}
