package agentbroadcast

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/managedtarget"
)

type candidateIdent struct {
	host, project, kind, session, namespace string
}

func identOf(t managedtarget.Target) candidateIdent {
	return candidateIdent{t.Host, t.Project, t.Kind, t.Session, t.Namespace}
}

// Plan discovers managed shells in scope and records a would_send or skipped
// verdict per identified agent. It does not write to any pane.
func (s Service) Plan(ctx context.Context, req PlanRequest) (Plan, error) {
	if s.Candidates == nil {
		return Plan{}, fmt.Errorf("candidate source is required")
	}
	universe, err := s.Candidates(ctx, req)
	if err != nil {
		return Plan{}, err
	}

	selected, err := selectCandidates(universe, req)
	if err != nil {
		return Plan{}, err
	}

	wanted := req.Status
	if len(wanted) == 0 {
		wanted = agentcontrol.PromptableStates()
	}

	plan := Plan{
		Scope:         Scope{Kind: req.ScopeKind, Project: req.Project},
		Raw:           req.Raw,
		FromUser:      req.FromUser,
		SenderName:    req.SenderName,
		SenderProject: req.SenderProject,
	}

	for _, cand := range selected {
		target := controlTarget(cand)
		snap, state, err := s.observe(ctx, target)
		if err != nil || state.Kind == "" {
			// No identified provider: absent from the plan, same as agent list.
			plan.ShellsWithoutAgent++
			continue
		}
		target = snap.Target
		row := Recipient{Target: target, Agent: state, Outcome: OutcomeWouldSend}
		if req.SenderSession != "" && cand.Session == req.SenderSession && !req.IncludeSelf {
			row.Outcome = OutcomeSkipped
			row.Reason = &Reason{Code: reasonSender, Message: "calling shell is excluded unless --include-self"}
		} else if r := eligibility(snap, state); r != nil {
			row.Outcome = OutcomeSkipped
			row.Reason = r
		} else if !statusWanted(state.Status, wanted) {
			row.Outcome = OutcomeSkipped
			row.Reason = &Reason{
				Code:    reasonStatus,
				Message: fmt.Sprintf("agent status is %s; status filter does not include it", state.Status),
			}
		}
		plan.Recipients = append(plan.Recipients, row)
	}

	sort.Slice(plan.Recipients, func(i, j int) bool {
		a, b := plan.Recipients[i], plan.Recipients[j]
		if a.Target.Project != b.Target.Project {
			return a.Target.Project < b.Target.Project
		}
		if a.Target.Name != b.Target.Name {
			return a.Target.Name < b.Target.Name
		}
		return a.Target.Session < b.Target.Session
	})
	return plan, nil
}

func selectCandidates(universe []managedtarget.Target, req PlanRequest) ([]managedtarget.Target, error) {
	chosen := make(map[candidateIdent]managedtarget.Target)
	switch req.ScopeKind {
	case ScopeProject:
		for _, t := range universe {
			if t.Project == req.Project {
				chosen[identOf(t)] = t
			}
		}
	case ScopeAll:
		for _, t := range universe {
			chosen[identOf(t)] = t
		}
	}

	for _, name := range req.To {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		resolved, err := managedtarget.Resolve(universe, managedtarget.Query{Value: name})
		if err != nil {
			return nil, fmt.Errorf("--to %q: %w", name, err)
		}
		chosen[identOf(resolved)] = resolved
	}
	for _, name := range req.Exclude {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		resolved, err := managedtarget.Resolve(universe, managedtarget.Query{Value: name})
		if err != nil {
			return nil, fmt.Errorf("--exclude %q: %w", name, err)
		}
		delete(chosen, identOf(resolved))
	}

	out := make([]managedtarget.Target, 0, len(chosen))
	for _, t := range chosen {
		out = append(out, t)
	}
	return out, nil
}

func (s Service) observe(ctx context.Context, target agentcontrol.Target) (agentcontrol.Snapshot, agentcontrol.AgentState, error) {
	if s.Control.Terminal == nil {
		return agentcontrol.Snapshot{}, agentcontrol.AgentState{}, &agentcontrol.Error{Code: agentcontrol.ErrTransport, Message: "terminal adapter is unavailable"}
	}
	snap, err := s.Control.Terminal.Inspect(ctx, target)
	if err != nil {
		return agentcontrol.Snapshot{}, agentcontrol.AgentState{}, err
	}
	agent, err := s.Control.Get(ctx, target)
	if err != nil {
		return snap, agentcontrol.AgentState{}, err
	}
	return snap, agent.Agent, nil
}

// eligibility is the promptable check minus the no-provider branch: that case
// is omitted from the plan rather than listed as skipped.
func eligibility(snap agentcontrol.Snapshot, state agentcontrol.AgentState) *Reason {
	if r := paneBusy(snap); r != nil {
		return r
	}
	if state.Status == agentcontrol.StatusBlocked {
		return &Reason{Code: string(agentcontrol.ErrBlocked), Message: "agent is blocked; read the screen and answer it with agent send-keys"}
	}
	if state.Freshness != "current" {
		return &Reason{
			Code:    string(agentcontrol.ErrNotReady),
			Message: fmt.Sprintf("agent status is %s, not current; nothing is sent to a target Sidecar cannot vouch for", state.Freshness),
		}
	}
	for _, ok := range agentcontrol.PromptableStates() {
		if state.Status == ok {
			return nil
		}
	}
	return &Reason{
		Code:    string(agentcontrol.ErrNotReady),
		Message: fmt.Sprintf("agent status is %s; prompt accepts idle, done, or working", state.Status),
	}
}

func paneBusy(snap agentcontrol.Snapshot) *Reason {
	busy := func(message string) *Reason {
		return &Reason{Code: string(agentcontrol.ErrPaneBusy), Message: message}
	}
	if snap.PaneCount != 1 {
		return busy("managed session must contain exactly one pane")
	}
	if snap.Dead {
		return busy("managed pane is dead")
	}
	if snap.CopyMode {
		return busy("managed pane is in copy or another tmux mode; leave it before sending input")
	}
	if snap.PaneID == "" || snap.PanePID <= 0 || snap.ServerPID <= 0 {
		return busy("managed pane identity is incomplete")
	}
	return nil
}

func statusWanted(status agentcontrol.Status, wanted []agentcontrol.Status) bool {
	for _, w := range wanted {
		if status == w {
			return true
		}
	}
	return false
}

func reasonFromErr(err error) *Reason {
	var typed *agentcontrol.Error
	if agentcontrol.AsError(err, &typed) {
		return &Reason{Code: string(typed.Code), Message: typed.Message}
	}
	return &Reason{Code: string(agentcontrol.ErrTransport), Message: err.Error()}
}
