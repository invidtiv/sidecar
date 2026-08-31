package shellstate

import (
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
)

// SessionUpdate is one integration's fenced attempt to change a shell's session
// binding.
//
// It carries the already-validated reference rather than raw provider input:
// validation belongs to agentsession, persistence belongs here, and keeping the
// two apart is what stops this package from growing a second opinion about what
// a legal reference looks like.
type SessionUpdate struct {
	// Ref is the reference to store. A zero Ref with Clear set removes the
	// binding.
	Ref agentsession.Ref
	// Clear removes the stored reference instead of setting one.
	Clear bool
	// Kind is the catalog family id the report came from.
	Kind string
	// Live is the provider generation currently occupying the pane, resolved by
	// the caller from live tmux — never from the report.
	Live string
	// Now stamps the acceptance. Nil means time.Now.
	Now func() time.Time
}

// SessionOutcome is what a fenced update did.
type SessionOutcome struct {
	// Decision is recorded, rotated, cleared, unchanged, or ignored.
	Decision agentsession.Decision
	// Ref is the binding in force after the update.
	Ref *agentsession.Ref
	// Kind is the provider recorded on the shell.
	Kind string
}

// BindSessionAtPath applies a fenced session report to one managed shell.
//
// The whole operation happens inside the manifest lock, and that is the point:
// the generation fence is a read-decide-write, so evaluating it outside the lock
// would let two concurrent hook processes both read the same prior state and
// both believe they won. Reading the prior reference under the same lock that
// writes the new one makes the fence actually decisive.
//
// A losing report is not an error to the caller's process — it returns
// DecisionIgnored with the refusal reason so a hook can exit 0 and stay out of
// the provider's way, which is the fail-open rule every reporting surface here
// follows.
func BindSessionAtPath(path string, id Identity, update SessionUpdate) (SessionOutcome, error) {
	if strings.TrimSpace(id.TmuxName) == "" {
		return SessionOutcome{}, &Error{Kind: KindValidation, Msg: "shell session name is required"}
	}
	now := update.Now
	if now == nil {
		now = time.Now
	}

	var out SessionOutcome
	err := mutateManifest(path, func(m *manifest) error {
		idx := -1
		for i := range m.Shells {
			if m.Shells[i].TmuxName == id.TmuxName && sameNamespace(m.Shells[i].Namespace, id.Namespace) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return &Error{Kind: KindNotFound, Msg: "no managed shell named " + id.TmuxName + " is recorded in this project"}
		}
		def := m.Shells[idx]

		var prior *agentsession.Ref
		if def.Agent != nil && def.Agent.Session != nil && !def.Agent.Session.Empty() {
			prior = def.Agent.Session
		}

		var (
			decision agentsession.Decision
			fenceErr error
		)
		if update.Clear {
			decision, fenceErr = agentsession.FenceClear(prior, update.Ref.Generation, update.Live)
		} else {
			decision, fenceErr = agentsession.Fence(prior, update.Ref, update.Live)
		}
		out.Decision = decision
		if fenceErr != nil {
			// The fence refused. Leave the record exactly as it was and report
			// why; a stale writer must change nothing at all, not even a
			// timestamp, or "ignored" would still be a write.
			out.Ref = prior
			out.Kind = def.AgentType
			return errSessionFenced{err: fenceErr}
		}
		if decision == agentsession.DecisionUnchanged {
			out.Ref = prior
			out.Kind = def.AgentType
			return errSessionUnchanged{}
		}

		if def.Agent == nil {
			def.Agent = &AgentBinding{}
		} else {
			clone := *def.Agent
			def.Agent = &clone
		}
		if kind := strings.TrimSpace(update.Kind); kind != "" {
			def.Agent.Kind = kind
			if def.AgentType == "" {
				def.AgentType = kind
			}
		}
		if update.Clear {
			def.Agent.Session = nil
		} else {
			ref := update.Ref
			if ref.ReportedAt.IsZero() {
				ref.ReportedAt = now().UTC()
			}
			def.Agent.Session = &ref
		}
		if def.Agent.Kind == "" && def.Agent.Session == nil && len(def.Agent.LaunchArgv) == 0 {
			def.Agent = nil
		}
		m.Shells[idx] = def
		out.Ref = nil
		if def.Agent != nil {
			out.Ref = def.Agent.Session
			out.Kind = def.Agent.Kind
		}
		if out.Kind == "" {
			out.Kind = def.AgentType
		}
		return nil
	})

	switch e := err.(type) {
	case errSessionUnchanged:
		return out, nil
	case errSessionFenced:
		return out, e.err
	}
	if err != nil {
		return SessionOutcome{}, err
	}
	return out, nil
}

// errSessionUnchanged aborts the mutation without an error reaching the caller.
//
// mutateManifest writes the file whenever apply returns nil, so "nothing
// changed" has to come back as an error to avoid rewriting shells.json on every
// replayed hook report. The plan is explicit that records are written on
// meaningful transitions, not on every capture.
type errSessionUnchanged struct{}

func (errSessionUnchanged) Error() string { return "session reference unchanged" }

// errSessionFenced aborts the mutation and carries the fence's reason out.
type errSessionFenced struct{ err error }

func (e errSessionFenced) Error() string { return e.err.Error() }
func (e errSessionFenced) Unwrap() error { return e.err }

// SessionHoldersAtPath lists the session claims recorded in one manifest.
//
// Deduplication is a global per-host question, so a caller collects these across
// every registered project and hands the whole set to agentsession.Dedup.
func SessionHoldersAtPath(path, project string) ([]agentsession.Holder, error) {
	defs, err := ListAtPath(path)
	if err != nil {
		return nil, err
	}
	var out []agentsession.Holder
	for _, def := range defs {
		if def.Agent == nil || def.Agent.Session == nil || def.Agent.Session.Empty() {
			continue
		}
		kind := def.Agent.Kind
		if kind == "" {
			kind = def.AgentType
		}
		out = append(out, agentsession.Holder{
			Project: project,
			Session: def.TmuxName,
			Name:    def.DisplayName,
			Kind:    kind,
			Ref:     *def.Agent.Session,
		})
	}
	return out, nil
}

// SessionRefAtPath returns the exact session reference bound to one shell.
//
// It is the read half of BindSessionAtPath and the only supported way to get a
// reference back out: the transcript reader and the restore planner both go
// through it rather than unmarshalling shells.json themselves.
func SessionRefAtPath(path string, id Identity) (agentsession.Ref, string, bool, error) {
	defs, err := ListAtPath(path)
	if err != nil {
		return agentsession.Ref{}, "", false, err
	}
	for _, def := range defs {
		if def.TmuxName != id.TmuxName || !sameNamespace(def.Namespace, id.Namespace) {
			continue
		}
		kind := ""
		if def.Agent != nil {
			kind = def.Agent.Kind
		}
		if kind == "" {
			kind = def.AgentType
		}
		if def.Agent == nil || def.Agent.Session == nil || def.Agent.Session.Empty() {
			return agentsession.Ref{}, kind, false, nil
		}
		return *def.Agent.Session, kind, true, nil
	}
	return agentsession.Ref{}, "", false, &Error{Kind: KindNotFound, Msg: "no managed shell named " + id.TmuxName + " is recorded in this project"}
}

// CarryForward returns next with every field it does not model taken from prior.
//
// It exists because shells.json has a second serializer. The workspace plugin
// builds a Definition from its own in-memory ShellSession, which models the v2
// fields and nothing else, and then replaces the stored record wholesale. Before
// v3 that was lossless, because the two structs described the same thing. With
// v3 it silently destroyed the session binding -- and it did so on the shell
// revival path, which is exactly the cold-restore moment the binding exists to
// serve.
//
// The honest fix is one serializer. Until the two locking implementations can be
// unified, this keeps the rule in the package that owns the schema rather than
// in the caller that keeps forgetting it: a writer that does not model a field
// must carry it, not drop it. A field added to Definition in a later schema
// version needs a line here, and the test that pins this is the reminder.
func CarryForward(prior, next Definition) Definition {
	if next.Agent == nil {
		next.Agent = prior.Agent
	}
	if next.Restore == nil {
		next.Restore = prior.Restore
	}
	return next
}
