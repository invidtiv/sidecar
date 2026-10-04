package agentresolve

import "github.com/marcus/sidecar/internal/agentcontrol"

// TargetQuery describes how a caller addresses a managed agent. Target is the
// supplied name, or the caller's own shell for an omitted target. Callers
// supply that context explicitly; resolution never reads the environment or
// the focused UI row.
type TargetQuery struct {
	Target   string
	Shell    string
	Project  string
	Explicit bool
}

// TargetLookup resolves a value within the caller's authorized universe. The
// globalExplicit argument requests a search across registered projects. A
// lookup may retain a scan for one operation (for example, prompt's two
// lookups), but must not cache it for the lifetime of a server.
//
// Adapters return agentcontrol.Error for semantic refusals. Other failures are
// wrapped as transport failures by ResolveTarget.
type TargetLookup func(target, shell, project string, globalExplicit bool) (agentcontrol.Target, error)

// ResolveTarget turns a caller's query into a managed agent identity before
// agentcontrol pins its physical pane. CLI, API and UI callers use the same
// omitted-target refusal and explicit-target scoping rule.
func ResolveTarget(q TargetQuery, lookup TargetLookup) (agentcontrol.Target, error) {
	if q.Target == "" {
		return agentcontrol.Target{}, &agentcontrol.Error{Code: agentcontrol.ErrNotFound, Message: "target is required outside a managed shell"}
	}
	if lookup == nil {
		return agentcontrol.Target{}, &agentcontrol.Error{Code: agentcontrol.ErrTransport, Message: "agent target lookup is unavailable"}
	}
	target, err := lookup(q.Target, q.Shell, q.Project, q.Explicit && q.Shell == "" && q.Project == "")
	if err != nil {
		var typed *agentcontrol.Error
		if agentcontrol.AsError(err, &typed) {
			return agentcontrol.Target{}, err
		}
		return agentcontrol.Target{}, &agentcontrol.Error{Code: agentcontrol.ErrTransport, Message: err.Error(), Err: err}
	}
	return target, nil
}
