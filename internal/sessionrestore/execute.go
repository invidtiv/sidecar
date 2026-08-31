package sessionrestore

import (
	"context"
	"errors"
	"fmt"

	"github.com/marcus/sidecar/internal/agentsession"
)

// Executing a plan.
//
// The plan is a decision taken from a snapshot. By the time a step runs, the
// snapshot can be wrong in ways that matter: a user can have recreated the shell
// by hand, another Sidecar can have run a restore, a previous run of this same
// restore can have been killed halfway through, or the tmux server can have been
// replaced again. So every mutation rechecks its own preconditions immediately
// before performing it, and the tmux session name is the key every recheck is
// made against.
//
// That key is what makes the whole thing converge instead of duplicating. A
// crash after the session was created but before the agent was resumed leaves a
// live session under a name the next run recognises, so the next run resumes
// into it rather than creating a second shell. The same is true of a crash after
// the plan was built, after layout attachment, and during the readiness wait:
// none of those states is distinguishable from "someone else already did this
// part", and the executor is written so that it does not need to distinguish
// them.

// Status is what execution did to one shell.
type Status string

const (
	// StatusReattached means the session was already live and nothing was done.
	StatusReattached Status = "reattached"
	// StatusRestored means a shell was created under its own name and cwd.
	StatusRestored Status = "restored"
	// StatusResumed means the shell was restored and its exact conversation
	// resumed. It is the only status that implies an agent process was run.
	StatusResumed Status = "resumed"
	// StatusConverged means the work was already done — by a previous
	// interrupted run, another Sidecar, or the user. Nothing was created.
	StatusConverged Status = "converged"
	// StatusSkipped means the plan did not call for action here.
	StatusSkipped Status = "skipped"
	// StatusRefused means acting would have required something unsafe.
	StatusRefused Status = "refused"
	// StatusFailed means the action was attempted and did not succeed. The shell
	// record is untouched and the step is retryable.
	StatusFailed Status = "failed"
)

// Outcome is the executed result for one shell.
type Outcome struct {
	Step   Step   `json:"-"`
	Status Status `json:"status"`
	Reason Reason `json:"reason"`
	Detail string `json:"detail"`
	// Err is the underlying failure, if any. It is never a reason to remove a
	// shell record: a failed restore leaves exactly the state a retry needs.
	Err error `json:"-"`
}

// Result is the whole execution.
type Result struct {
	Plan     Plan
	Outcomes []Outcome
}

// Counts summarises the result for a grouped summary line.
func (r Result) Counts() map[Status]int {
	out := map[Status]int{}
	for _, o := range r.Outcomes {
		out[o.Status]++
	}
	return out
}

// Failed reports the retryable failures.
func (r Result) Failed() []Outcome {
	var out []Outcome
	for _, o := range r.Outcomes {
		if o.Status == StatusFailed {
			out = append(out, o)
		}
	}
	return out
}

// Deps are the effects the executor is allowed to have. Every one of them is
// injected so the crash-and-retry matrix can be driven without a tmux server.
type Deps struct {
	// Live reports what currently holds a tmux session name. It is called again
	// immediately before every mutation, never read from the plan.
	Live func(session string) LiveState
	// CurrentServer returns the running tmux server id, empty when none. The
	// executor rechecks it before each mutation: if the server has been replaced
	// mid-run, everything already created is gone and continuing would build on
	// a foundation that no longer exists.
	CurrentServer func() string
	// CreateShell recreates one managed shell under its recorded name and
	// working directory. It must be idempotent on the session name.
	CreateShell func(context.Context, Step) error
	// ResumePlanFor re-reads the shell's exact session binding from the manifest
	// and builds the resume. Re-reading rather than trusting the plan is the
	// binding recheck: the reference can have been rotated or cleared by the
	// provider's own integration since the plan was built.
	ResumePlanFor func(Step) (agentsession.ResumePlan, error)
	// ResumeAgent runs the resume in the shell and returns once the provider is
	// identified and ready.
	ResumeAgent func(context.Context, Step, agentsession.ResumePlan) error
}

// ErrServerReplaced aborts a run whose tmux server changed underneath it.
var ErrServerReplaced = errors.New("the tmux server was replaced while restoring")

// Execute performs a plan, rechecking every precondition at the moment of use.
//
// It never kills a live session to take its name, never deletes or rewrites a
// shell record on failure, and stops the run rather than continuing across a
// tmux server replacement.
func Execute(ctx context.Context, plan Plan, deps Deps) Result {
	result := Result{Plan: plan}
	if deps.Live == nil {
		deps.Live = func(string) LiveState { return LiveAbsent }
	}
	if deps.CurrentServer == nil {
		deps.CurrentServer = func() string { return "" }
	}
	// The run's server is adopted rather than required up front.
	//
	// A cold restore usually begins with no tmux server at all — that is the
	// situation it exists for — and creating the first shell is what starts one.
	// Treating "" -> "pid=N" as a replacement would abort every real cold
	// restore after its first shell, which is exactly what it did the first time
	// this ran against a real tmux. What must abort the run is the server
	// changing out from under work already done: a different pid, or the server
	// disappearing again.
	run := &serverPin{id: deps.CurrentServer()}

	aborted := false
	for _, step := range plan.Steps {
		if aborted {
			result.Outcomes = append(result.Outcomes, Outcome{
				Step: step, Status: StatusSkipped, Reason: step.Reason,
				Detail: "not attempted: the tmux server was replaced mid-restore",
			})
			continue
		}
		out := executeStep(ctx, step, deps, run)
		result.Outcomes = append(result.Outcomes, out)
		if errors.Is(out.Err, ErrServerReplaced) {
			aborted = true
		}
	}
	return result
}

// serverPin is the tmux server this run is bound to, adopted on first sight.
type serverPin struct{ id string }

// changed reports that the server is no longer the one this run is working in.
//
// An empty observation before anything has been adopted is not a change: it is
// the ordinary starting state of a cold restore. Once a server has been adopted,
// both a different pid and a disappearance are changes, because either one means
// the sessions this run already created are gone.
func (p *serverPin) changed(now string) bool {
	if p.id == "" {
		p.id = now
		return false
	}
	return now != p.id
}

func executeStep(ctx context.Context, step Step, deps Deps, run *serverPin) Outcome {
	switch step.Action {
	case ActionReattach:
		return Outcome{Step: step, Status: StatusReattached, Reason: step.Reason, Detail: step.Detail}
	case ActionSkip, ActionManual:
		return Outcome{Step: step, Status: StatusSkipped, Reason: step.Reason, Detail: step.Detail}
	case ActionRefuse:
		return Outcome{Step: step, Status: StatusRefused, Reason: step.Reason, Detail: step.Detail}
	}

	// Recheck the server before touching anything. A run that continued across a
	// server replacement would be creating shells in a server the plan was not
	// built for, and every session it had already created would be gone.
	if run.changed(deps.CurrentServer()) {
		return Outcome{
			Step: step, Status: StatusFailed, Reason: step.Reason,
			Detail: "the tmux server was replaced while restoring; nothing further was attempted",
			Err:    ErrServerReplaced,
		}
	}

	// Recheck the name at the instant of the mutation, not from the plan.
	status := StatusRestored
	switch deps.Live(step.Session) {
	case LiveForeign:
		// The single most important refusal in the executor. Taking the name
		// would mean terminating a process someone is using, to satisfy a
		// bookkeeping preference. The record stays, the reason is shown.
		return Outcome{
			Step: step, Status: StatusRefused, Reason: ReasonNameCollision,
			Detail: "another live tmux session already holds this name; it was left running and nothing was created",
		}
	case LiveManaged:
		// Already there. Either a previous interrupted run created it, or the
		// user did. Converge on it rather than creating a second shell.
		status = StatusConverged
	default:
		if deps.CreateShell == nil {
			return Outcome{Step: step, Status: StatusFailed, Reason: step.Reason, Detail: "no shell creator is configured"}
		}
		if err := deps.CreateShell(ctx, step); err != nil {
			return Outcome{
				Step: step, Status: StatusFailed, Reason: step.Reason,
				Detail: fmt.Sprintf("could not recreate the shell: %v", err), Err: err,
			}
		}
	}

	if step.Action != ActionResumeAgent {
		return Outcome{Step: step, Status: status, Reason: step.Reason, Detail: step.Detail}
	}

	// The shell exists. Everything below decides whether to run an agent in it,
	// and each check is made again here rather than inherited from the plan.
	if run.changed(deps.CurrentServer()) {
		return Outcome{
			Step: step, Status: status, Reason: step.Reason,
			Detail: "the shell was restored; the tmux server was replaced before the conversation could be resumed",
			Err:    ErrServerReplaced,
		}
	}
	if deps.Live(step.Session) != LiveManaged {
		// The shell we just made is not there. Report the shell result honestly
		// and do not resume into whatever is.
		return Outcome{
			Step: step, Status: StatusFailed, Reason: step.Reason,
			Detail: "the restored shell was gone again before its conversation could be resumed",
		}
	}
	if deps.ResumePlanFor == nil || deps.ResumeAgent == nil {
		return Outcome{Step: step, Status: status, Reason: ReasonResumeOff, Detail: "no resume path is configured"}
	}

	// Re-read the binding. Between planning and now, the provider's own
	// integration can have rotated or cleared it, and resuming a reference that
	// is no longer the shell's is the exact mistake exact binding exists to
	// prevent.
	resumePlan, err := deps.ResumePlanFor(step)
	if err != nil {
		return Outcome{
			Step: step, Status: status, Reason: resumeRefusal(err),
			Detail: fmt.Sprintf("the shell was restored; its conversation was not resumed: %v", err), Err: err,
		}
	}
	if err := deps.ResumeAgent(ctx, step, resumePlan); err != nil {
		return Outcome{
			Step: step, Status: status, Reason: step.Reason,
			Detail: fmt.Sprintf("the shell was restored; resuming its conversation failed: %v", err), Err: err,
		}
	}
	return Outcome{Step: step, Status: StatusResumed, Reason: ReasonPolicyResume, Detail: "restored the shell and resumed its exact conversation"}
}
