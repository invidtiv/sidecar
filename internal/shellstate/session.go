package shellstate

import (
	"errors"
	"fmt"
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

		// The shell already knows what provider it runs. A report that names a
		// different one is refused here, before the fence and before any write.
		//
		// This is the second of two independent gates on the same condition. The
		// caller checks the claim against the pane's live foreground process,
		// which is stronger evidence but is only available where a process
		// identity adapter exists and where the provider did not hide its hook in
		// a fresh process group. This one needs no process table at all: it
		// compares the claim against the kind the manifest recorded for this
		// shell, so wherever a kind was recorded, a grok shell refuses a claude
		// report on every platform, including the ones with no process identity
		// adapter.
		//
		// It covers only shells whose record names a kind, and that is the exact
		// limit of the claim. A shell created with `create shell --agent KIND` and
		// one whose provider was started by `agent start --kind KIND` both record
		// one (RecordAgentKindAtPath); a plain shell somebody launched an agent
		// inside by hand, and a pre-existing tmux session adopted at startup, do
		// not, and for those this gate abstains and the process-identity gate is
		// the only one standing.
		//
		// Enforcing it here is also what keeps AgentType and Agent.Kind from
		// disagreeing. The two are documented as the same value, and the previous
		// writer set Agent.Kind from the report while leaving AgentType alone
		// whenever it was already populated — so a mis-attributed report produced a
		// record that named two different providers and made every reader's answer
		// depend on which field it happened to consult.
		if kind := strings.TrimSpace(update.Kind); kind != "" && def.AgentType != "" && kind != def.AgentType {
			out.Ref = prior
			out.Kind = def.AgentType
			return errSessionKindMismatch{err: fmt.Errorf(
				"%w: this shell runs %s, but the report claims %s",
				agentsession.ErrKindMismatch, def.AgentType, kind)}
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
			// Both fields, always, and unconditionally: the mismatch gate above
			// has already established that AgentType is either empty or exactly
			// this kind, so writing both is what makes "the two are the same
			// value" true by construction rather than by the caller remembering.
			def.Agent.Kind = kind
			def.AgentType = kind
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
	case errSessionKindMismatch:
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

// errSessionKindMismatch aborts the mutation because the report named a
// different provider than the shell runs. It is separate from errSessionFenced
// so the two refusals cannot be confused: one is the right provider too late,
// the other is the wrong provider.
type errSessionKindMismatch struct{ err error }

func (e errSessionKindMismatch) Error() string { return e.err.Error() }
func (e errSessionKindMismatch) Unwrap() error { return e.err }

// AgentKindOf answers "what provider does this record name", and — because a
// record can name two — "does it disagree with itself".
//
// AgentType and Agent.Kind are documented as the same value and are written
// together by every writer here, but a record produced before that invariant
// existed can hold two different providers: a mis-attributed hook report set
// Agent.Kind and left AgentType alone. Readers preferring one field silently
// picked a side, which is how a shell created for grok came to offer
// `claude --resume` on a grok conversation id.
//
// So the disagreement is returned rather than resolved. This package will not
// guess which field is right; a caller about to act on the answer — running a
// provider CLI against a stored conversation id, above all — is expected to
// refuse instead. conflict is empty whenever the record is self-consistent,
// which is every record any current writer produces.
func AgentKindOf(def Definition) (kind, conflict string) {
	bound := ""
	if def.Agent != nil {
		bound = strings.TrimSpace(def.Agent.Kind)
	}
	recorded := strings.TrimSpace(def.AgentType)
	switch {
	case bound == "":
		return recorded, ""
	case recorded == "" || recorded == bound:
		return bound, ""
	default:
		return bound, recorded
	}
}

// RecordAgentKindAtPath records which provider family a managed shell is
// running.
//
// It exists because the bind-time kind gate above can only compare a report
// against a kind the record already names, and until this call there was
// exactly one moment that wrote one: shell creation with an explicit --agent.
// A shell created plain and then handed a provider by `sidecar agent start` —
// the sequence the coordinate-agents contract actually prescribes — recorded
// nothing, so the gate abstained for precisely the shells an agent drives.
//
// The caller must have already proved the provider occupies the pane. That is
// what makes overwriting a differing recorded kind correct rather than
// destructive: process-ownership evidence beats a stale creation-time
// preference, which is the opposite of the report path, where the kind is an
// unverified claim and a disagreement is a refusal.
//
// Both kind fields are written together, for the same reason BindSessionAtPath
// writes them together — a record that names two providers is a record whose
// meaning depends on which field a reader consults. And a changed kind drops
// the session binding: that reference is the *previous* provider's
// conversation, and keeping it is how a restore comes to offer one agent's
// conversation to another.
func RecordAgentKindAtPath(path string, id Identity, kind string) error {
	if strings.TrimSpace(id.TmuxName) == "" {
		return &Error{Kind: KindValidation, Msg: "shell session name is required"}
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return &Error{Kind: KindValidation, Msg: "agent kind is required"}
	}

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

		prior := def.AgentType
		if def.Agent != nil && strings.TrimSpace(def.Agent.Kind) != "" {
			prior = def.Agent.Kind
		}
		if prior == kind && def.AgentType == kind {
			// Already recorded, by creation or by an earlier start. Returning
			// without a write keeps a repeated start from rewriting shells.json
			// and waking every watcher for no change.
			return errSessionUnchanged{}
		}

		if def.Agent == nil {
			def.Agent = &AgentBinding{}
		} else {
			clone := *def.Agent
			def.Agent = &clone
		}
		if prior != "" && prior != kind {
			def.Agent.Session = nil
			def.Agent.LaunchArgv = nil
		}
		def.Agent.Kind = kind
		def.AgentType = kind
		m.Shells[idx] = def
		return nil
	})
	if _, ok := err.(errSessionUnchanged); ok {
		return nil
	}
	return err
}

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
		kind, _ := AgentKindOf(def)
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

// ErrKindDisagreement reports a record whose two provider fields name different
// providers. It is a read-time refusal, not a repair: nothing here can tell
// which of the two is the mistake, and every way of choosing produces the
// failure the fields exist to prevent — one provider's CLI handed another
// provider's conversation.
var ErrKindDisagreement = errors.New("this shell's record names two different agent kinds")

// SessionRefAtPath returns the exact session reference bound to one shell.
//
// It is the read half of BindSessionAtPath and the only supported way to get a
// reference back out: the transcript reader and the restore planner both go
// through it rather than unmarshalling shells.json themselves.
//
// Being the one door is what lets it carry the reconciliation. A record poisoned
// before the bind-time kind gate existed still sits in users' manifests, and no
// migration can fix it — the healing report would have to come from a provider
// integration that does not ship. So the refusal happens on the way out, once,
// here, instead of in each of the callers that would otherwise each pick a field.
func SessionRefAtPath(path string, id Identity) (agentsession.Ref, string, bool, error) {
	defs, err := ListAtPath(path)
	if err != nil {
		return agentsession.Ref{}, "", false, err
	}
	for _, def := range defs {
		if def.TmuxName != id.TmuxName || !sameNamespace(def.Namespace, id.Namespace) {
			continue
		}
		kind, conflict := AgentKindOf(def)
		if conflict != "" {
			return agentsession.Ref{}, kind, false, fmt.Errorf(
				"%w: %s and %s", ErrKindDisagreement, conflict, kind)
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
	// AgentType is modeled by the second serializer, but only for a shell whose
	// in-memory session carries a chosen agent. A record written by another
	// surface — `create shell --agent`, or `agent start` — reaches the workspace
	// plugin as a shell it merely adopted, with no chosen agent, and a wholesale
	// replacement then cleared the kind. That silently disarmed the bind-time
	// kind gate, which can only refuse a mis-attributed report when the record
	// names a kind.
	//
	// Empty is not a value here: it means "this writer has nothing to say",
	// never "this shell runs no agent". Exiting an agent does not clear the
	// field either — the choice is creation-time truth, which is why the
	// workspace plugin keeps it in ChosenAgent across an agent's death.
	if strings.TrimSpace(next.AgentType) == "" {
		next.AgentType = prior.AgentType
	}
	return next
}
