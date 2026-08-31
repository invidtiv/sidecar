// Package sessionrestore decides what a cold restore would do, and says why.
//
// The decision is a pure function. Everything the planner needs — which shells
// were confirmed live in the previous tmux server, which sessions exist now,
// whether each recorded working directory still exists, what the policy says,
// which providers can actually resume, and which conversations are claimed
// twice — arrives as data in an Input, and a Plan comes back. There is no tmux
// call, no filesystem walk, and no provider spawn in this package.
//
// That shape is deliberate rather than aesthetic. A restore is the one moment
// when Sidecar recreates state it cannot see, after an event that destroyed the
// evidence, and the failure the plan is most concerned with is not a crash but a
// confident wrong answer: resuming a conversation into the wrong repository,
// starting a second copy of an agent that is already running, or replaying a
// command nobody asked to replay. Those are all decisions, not effects, so they
// are all testable here without a terminal.
//
// The executor in this package's sibling file performs the plan, and rechecks
// every precondition again at the moment of mutation. The plan is what should
// happen; the recheck is what makes it safe to act on a plan built from a
// snapshot that may already be stale.
package sessionrestore

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/shellstate"
)

// Action is the verdict for one managed shell. The vocabulary is the plan's:
// reattach, recreate-shell, resume-agent, manual, skip, refuse.
type Action string

const (
	// ActionReattach means the tmux session is still live and the existing pane
	// layout will find it by name. Nothing is created.
	ActionReattach Action = "reattach"
	// ActionRecreateShell means a fresh shell will be created under the same
	// tmux session name and working directory, with no agent started.
	ActionRecreateShell Action = "recreate-shell"
	// ActionResumeAgent means the shell will be recreated and the exact bound
	// conversation resumed in it. This is the only action that runs an agent.
	ActionResumeAgent Action = "resume-agent"
	// ActionManual means the record is kept and recoverable, but Sidecar will not
	// act on it automatically. The user can recreate or resume it deliberately.
	ActionManual Action = "manual"
	// ActionSkip means policy or configuration says to leave this shell alone.
	ActionSkip Action = "skip"
	// ActionRefuse means restoring would require doing something unsafe — using a
	// directory that no longer exists, or taking a session name something else
	// currently holds. The record is kept and the reason is shown.
	ActionRefuse Action = "refuse"
)

// Reason is the stable machine code explaining an Action.
//
// These are codes, not sentences, for the same reason agentcontrol's error codes
// are: a caller that has to match on prose is a caller that breaks when the
// prose improves. The human sentence travels alongside in Detail.
type Reason string

const (
	// ReasonAlreadyLive — the tmux session survived; there is nothing to restore.
	ReasonAlreadyLive Reason = "already_live"
	// ReasonNotPriorLive — the shell was not confirmed live in the previous tmux
	// server incarnation, so it is not a cold-restore candidate.
	ReasonNotPriorLive Reason = "not_prior_live"
	// ReasonDiedInThisServer — the shell's last confirmed server is the one
	// running now, so it exited on its own rather than dying with the server.
	// Recreating it would be reviving something the user or a program closed.
	ReasonDiedInThisServer Reason = "died_in_this_server"
	// ReasonPolicyNever — this shell's per-shell policy is never.
	ReasonPolicyNever Reason = "policy_never"
	// ReasonRecreateDisabled — recreateShells is off in configuration.
	ReasonRecreateDisabled Reason = "recreate_disabled"
	// ReasonMissingWorkDir — the recorded working directory does not exist. This
	// is a refusal and never a fallback to some other directory.
	ReasonMissingWorkDir Reason = "missing_workdir"
	// ReasonNoWorkDir — the record predates working-directory capture, so there
	// is no directory to recreate it in.
	ReasonNoWorkDir Reason = "no_workdir"
	// ReasonNameCollision — something that is not this managed shell already
	// holds the tmux session name. Never resolved by killing it.
	ReasonNameCollision Reason = "name_collision"
	// ReasonNotSelected — a --shell target was given and this is not it.
	ReasonNotSelected Reason = "not_selected"
	// ReasonRecreatable — the shell will be recreated; used on the agent verdict
	// when the shell part succeeds but no agent is involved.
	ReasonRecreatable Reason = "recreatable"

	// ReasonNoSessionRef — no official integration ever reported a conversation
	// for this shell, so there is nothing exact to resume.
	ReasonNoSessionRef Reason = "no_session_ref"
	// ReasonUnreportedRef — a reference exists but no official integration
	// vouched for it. A same-cwd guess is a suggestion, not a command.
	ReasonUnreportedRef Reason = "unreported_ref"
	// ReasonProviderNoResume — the provider has no native resume command.
	ReasonProviderNoResume Reason = "provider_no_resume"
	// ReasonProviderUnavailable — the provider's binary is not installed here.
	ReasonProviderUnavailable Reason = "provider_unavailable"
	// ReasonProviderRejectedRef — the provider cannot resume from this kind of
	// reference, or the catalog refused to build the argv.
	ReasonProviderRejectedRef Reason = "provider_rejected_ref"
	// ReasonDuplicateRef — another managed shell claims the same conversation and
	// won deduplication. This one restores as a plain shell.
	ReasonDuplicateRef Reason = "duplicate_ref"
	// ReasonResumeOff — resumeAgents is off, or the per-shell policy is shell.
	ReasonResumeOff Reason = "resume_off"
	// ReasonNeedsConfirmation — the effective policy is ask and no confirmation
	// has been given. Nothing is executed until it is.
	ReasonNeedsConfirmation Reason = "needs_confirmation"
	// ReasonAgentsNotRequested — the caller did not ask for agent resumes.
	ReasonAgentsNotRequested Reason = "agents_not_requested"
	// ReasonPolicyResume — the resume is authorized and will run.
	ReasonPolicyResume Reason = "policy_resume"
	// ReasonKindDisagreement — the record names two different providers, so
	// which CLI would be handed the conversation is not knowable from it. The
	// shell is recreated; the conversation is not resumed by either.
	//
	// This is a refusal rather than a preference on purpose. A reader that picks
	// a field is a reader that will eventually run `claude --resume` on a grok
	// conversation id, which is the exact defect that produced these records.
	ReasonKindDisagreement Reason = "kind_disagreement"
)

// ResumeMode is the machine-wide resumeAgents setting.
type ResumeMode string

const (
	// ResumeOff never resumes a conversation automatically.
	ResumeOff ResumeMode = "off"
	// ResumeAsk restores shells and layout, then asks once before running any
	// agent. It is the default, because a resume can cost money and can mutate a
	// repository, and neither should follow from a reboot alone.
	ResumeAsk ResumeMode = "ask"
	// ResumeAuto is explicit standing authorization for exact, officially
	// reported references only.
	ResumeAuto ResumeMode = "auto"
)

// ResumeModes lists the vocabulary in a stable order.
func ResumeModes() []ResumeMode { return []ResumeMode{ResumeOff, ResumeAsk, ResumeAuto} }

// ParseResumeMode accepts a mode name. Empty means the default, ask.
func ParseResumeMode(value string) (ResumeMode, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ResumeAsk, nil
	}
	for _, known := range ResumeModes() {
		if ResumeMode(trimmed) == known {
			return known, nil
		}
	}
	return "", &ConfigError{Value: value}
}

// ConfigError reports an unusable resumeAgents value.
type ConfigError struct{ Value string }

func (e *ConfigError) Error() string {
	return fmt.Sprintf("unknown resumeAgents value %q; use one of: off, ask, auto", e.Value)
}

// Config is the machine default restore policy.
type Config struct {
	// RecreateShells recreates interactive managed shells that were confirmed
	// live in the previous tmux server. It defaults true.
	RecreateShells bool
	// ResumeAgents is off | ask | auto and defaults ask.
	ResumeAgents ResumeMode
}

// DefaultConfig is what a machine with no configuration gets.
func DefaultConfig() Config { return Config{RecreateShells: true, ResumeAgents: ResumeAsk} }

// LiveState is what the current tmux server says about one session name.
type LiveState string

const (
	// LiveAbsent means no session holds the name.
	LiveAbsent LiveState = "absent"
	// LiveManaged means the name is held by this managed shell — either it
	// survived, or a previous restore attempt already recreated it.
	LiveManaged LiveState = "managed"
	// LiveForeign means the name is held by something that is not this managed
	// shell. The restore refuses; it never kills a live session to take a name.
	LiveForeign LiveState = "foreign"
)

// Shell is one candidate record with the project it belongs to.
type Shell struct {
	// Project is the project key whose manifest holds the record.
	Project string
	// ProjectRoot is that project's working tree, which is where a recreated
	// shell's record already says it belongs. It is never used as a fallback
	// directory for a shell whose own WorkDir has gone: that case is a refusal.
	ProjectRoot string
	// ManifestPath is where the record lives, so the executor can write the
	// outcome back without re-deriving the path.
	ManifestPath string
	// Def is the stored record.
	Def shellstate.Definition
}

// Request is what the caller asked for, as distinct from what is configured.
type Request struct {
	// OnlyShell limits the plan to one tmux session name. Other shells are
	// reported as not_selected rather than omitted, so the output still accounts
	// for every record.
	OnlyShell string
	// Agents is the --agents flag: the caller is asking for eligible resumes.
	// Without it a CLI restore does shells only, whatever the policy allows.
	Agents bool
	// Confirmed records that a human confirmed — --yes, or a TUI confirmation.
	// It is what turns an ask-policy resume from planned into executable.
	Confirmed bool
	// Startup marks the automatic post-first-frame restore, which follows
	// configuration rather than an explicit request and so implies Agents.
	Startup bool
}

// Input is everything the planner is allowed to know.
type Input struct {
	Config Config
	// CurrentServer identifies the tmux server running now, in the same "pid=N"
	// form the manifest stores. Empty means no server is running.
	CurrentServer string
	// Live reports the current tmux inventory, keyed by tmux session name.
	// A name absent from the map is LiveAbsent.
	Live map[string]LiveState
	// Shells are the candidate records across every project.
	Shells []Shell
	// DirExists answers whether a recorded working directory is still there. It
	// is injected so the planner stays pure and so the test matrix can describe a
	// deleted worktree without deleting one.
	DirExists func(path string) bool
	// ProviderAvailable answers whether a provider's binary is installed. Nil
	// means "assume available", which is only correct in tests.
	ProviderAvailable func(kind string) bool
	// Request is the caller's ask.
	Request Request
}

// AgentStep is the resume verdict for one shell.
//
// It deliberately carries the reference *kind* and whether it was reported, but
// never the reference value. A plan is printed, logged, and pasted into issues;
// M3 established that a conversation identifier written into a log cannot be
// unwritten, and a plan is exactly the kind of output that ends up there.
type AgentStep struct {
	// Kind is the provider family id.
	Kind string `json:"kind"`
	// RefKind is "id" or "path", or empty when there is no reference.
	RefKind string `json:"refKind,omitempty"`
	// Reported is whether an official integration vouched for the reference.
	Reported bool `json:"reported"`
	// Resume is whether this plan would actually resume the conversation.
	Resume bool `json:"resume"`
	// Reason explains the verdict either way.
	Reason Reason `json:"reason"`
	// ConflictWith names the shell that won deduplication, when this one lost.
	ConflictWith string `json:"conflictWith,omitempty"`
}

// Step is the ordered verdict for one managed shell.
type Step struct {
	Project string `json:"project"`
	// Session is the tmux session name, which is also the idempotency key: the
	// executor's every recheck is keyed on it, so a crashed and retried restore
	// converges on one shell rather than creating a second.
	Session string `json:"session"`
	Name    string `json:"name,omitempty"`
	WorkDir string `json:"workDir,omitempty"`

	// ProjectRoot is the project's working tree, carried for the executor.
	ProjectRoot string `json:"projectRoot,omitempty"`

	Action Action `json:"action"`
	Reason Reason `json:"reason"`
	// Detail is the human sentence for the same verdict.
	Detail string `json:"detail"`
	// ExternalExecution is true when performing this step would run an agent
	// process — the thing that can cost money and mutate a repository. The plan
	// requires status and dry-run to say this per shell, so a reader can answer
	// "will this spend anything" without decoding the policy matrix themselves.
	ExternalExecution bool `json:"externalExecution"`
	// NeedsConfirmation is true when a resume is otherwise authorized and only a
	// human confirmation is missing.
	NeedsConfirmation bool `json:"needsConfirmation,omitempty"`
	// Agent is the resume verdict, present whenever the shell has agent metadata.
	Agent *AgentStep `json:"agent,omitempty"`

	// manifestPath is carried for the executor and not serialized: a plan is a
	// user-facing document and a state path in it is noise at best.
	manifestPath string
}

// ManifestPath is where the executor writes this step's outcome.
func (s Step) ManifestPath() string { return s.manifestPath }

// Plan is the ordered set of verdicts plus the server evidence behind them.
type Plan struct {
	// ServerChanged reports that the tmux server running now is not the one the
	// candidate shells were last confirmed alive in. It is what distinguishes a
	// cold restore from an ordinary Sidecar restart, and it is evidence rather
	// than a claim: it is derived from the recorded and observed server identity.
	ServerChanged bool `json:"serverChanged"`
	// CurrentServer is the running tmux server, empty when none is running.
	CurrentServer string `json:"currentServer,omitempty"`
	// PriorServers are the distinct servers the candidates were last seen in.
	PriorServers []string `json:"priorServers,omitempty"`
	// Steps are ordered by project then session name, so two runs over the same
	// state produce the same document.
	Steps []Step `json:"steps"`
}

// Executable reports the steps the executor would actually perform.
func (p Plan) Executable() []Step {
	var out []Step
	for _, s := range p.Steps {
		if s.Action == ActionRecreateShell || s.Action == ActionResumeAgent {
			out = append(out, s)
		}
	}
	return out
}

// PendingConfirmation reports the steps held only by a missing confirmation.
func (p Plan) PendingConfirmation() []Step {
	var out []Step
	for _, s := range p.Steps {
		if s.NeedsConfirmation {
			out = append(out, s)
		}
	}
	return out
}

// WouldExecuteAgents reports whether performing this plan runs any agent.
func (p Plan) WouldExecuteAgents() bool {
	for _, s := range p.Steps {
		if s.ExternalExecution {
			return true
		}
	}
	return false
}

// Build computes the plan. It performs no I/O.
func Build(in Input) Plan {
	dirExists := in.DirExists
	if dirExists == nil {
		dirExists = func(string) bool { return true }
	}
	providerAvailable := in.ProviderAvailable
	if providerAvailable == nil {
		providerAvailable = func(string) bool { return true }
	}
	if in.Config.ResumeAgents == "" {
		in.Config.ResumeAgents = ResumeAsk
	}

	shells := append([]Shell(nil), in.Shells...)
	sort.SliceStable(shells, func(i, j int) bool {
		if shells[i].Project != shells[j].Project {
			return shells[i].Project < shells[j].Project
		}
		return shells[i].Def.TmuxName < shells[j].Def.TmuxName
	})

	winners := dedupWinners(shells)

	plan := Plan{CurrentServer: in.CurrentServer}
	priorSeen := map[string]bool{}
	for _, sh := range shells {
		step := planShell(in, sh, dirExists, providerAvailable, winners)
		plan.Steps = append(plan.Steps, step)
		if r := sh.Def.Restore; r != nil && r.LastSeenServer != "" && !priorSeen[r.LastSeenServer] {
			priorSeen[r.LastSeenServer] = true
			plan.PriorServers = append(plan.PriorServers, r.LastSeenServer)
		}
	}
	sort.Strings(plan.PriorServers)
	for _, prior := range plan.PriorServers {
		if prior != in.CurrentServer {
			plan.ServerChanged = true
			break
		}
	}
	return plan
}

// dedupWinners maps each dedup key to the tmux session allowed to resume it.
//
// Deduplication is global per host, so the map is built once over every
// candidate across every project rather than per project: two projects can hold
// the same conversation reference, and resuming both would put one provider's
// session in two panes.
func dedupWinners(shells []Shell) map[string]agentsession.Holder {
	var holders []agentsession.Holder
	for _, sh := range shells {
		ref, kind, ok := binding(sh.Def)
		if !ok {
			continue
		}
		// A record that names two providers can never resume, so letting it
		// enter deduplication would only let it beat a healthy shell for a
		// conversation neither would then get.
		if kindConflict(sh.Def) != "" {
			continue
		}
		holders = append(holders, agentsession.Holder{
			Project: sh.Project,
			Session: sh.Def.TmuxName,
			Name:    sh.Def.DisplayName,
			Kind:    kind,
			Ref:     ref,
		})
	}
	out := map[string]agentsession.Holder{}
	for _, result := range agentsession.Dedup(holders) {
		out[result.Key] = result.Winner
	}
	return out
}

// binding returns the shell's exact session reference and provider kind.
func binding(def shellstate.Definition) (agentsession.Ref, string, bool) {
	if def.Agent == nil || def.Agent.Session == nil || def.Agent.Session.Empty() {
		return agentsession.Ref{}, agentKind(def), false
	}
	return *def.Agent.Session, agentKind(def), true
}

func agentKind(def shellstate.Definition) string {
	kind, _ := shellstate.AgentKindOf(def)
	return kind
}

// kindConflict names the second provider a self-contradicting record holds, and
// is empty for every record a current writer produces.
func kindConflict(def shellstate.Definition) string {
	_, conflict := shellstate.AgentKindOf(def)
	return conflict
}

func policyOf(def shellstate.Definition) agentsession.Policy {
	if def.Restore == nil || def.Restore.Policy == "" {
		return agentsession.PolicyInherit
	}
	return def.Restore.Policy
}

func planShell(in Input, sh Shell, dirExists func(string) bool, providerAvailable func(string) bool, winners map[string]agentsession.Holder) Step {
	def := sh.Def
	step := Step{
		Project:      sh.Project,
		ProjectRoot:  sh.ProjectRoot,
		Session:      def.TmuxName,
		Name:         def.DisplayName,
		WorkDir:      def.WorkDir,
		manifestPath: sh.ManifestPath,
	}

	live := in.Live[def.TmuxName]
	if live == "" {
		live = LiveAbsent
	}
	policy := policyOf(def)

	// A live managed session is the end of the story: the existing pane layout
	// finds it by name, and creating anything would be the duplicate the
	// idempotency key exists to prevent. This is checked before policy on
	// purpose — "never restore this shell" is not a reason to disturb one that
	// is already running.
	if live == LiveManaged {
		step.Action = ActionReattach
		step.Reason = ReasonAlreadyLive
		step.Detail = "still running; the saved layout reattaches to it by name"
		step.Agent = agentNoop(def, ReasonAlreadyLive)
		return step
	}

	if target := strings.TrimSpace(in.Request.OnlyShell); target != "" && target != def.TmuxName && target != def.DisplayName {
		step.Action = ActionSkip
		step.Reason = ReasonNotSelected
		step.Detail = "not the requested shell"
		step.Agent = agentNoop(def, ReasonNotSelected)
		return step
	}

	if policy == agentsession.PolicyNever {
		step.Action = ActionSkip
		step.Reason = ReasonPolicyNever
		step.Detail = "per-shell restore policy is never"
		step.Agent = agentNoop(def, ReasonPolicyNever)
		return step
	}

	// A foreign holder of the name is a refusal, never a kill. Taking the name
	// would mean terminating a live process to satisfy a bookkeeping preference,
	// which is the opposite of what a restore is for.
	if live == LiveForeign {
		step.Action = ActionRefuse
		step.Reason = ReasonNameCollision
		step.Detail = "another live tmux session already holds this name; Sidecar will not close it"
		step.Agent = agentNoop(def, ReasonNameCollision)
		return step
	}

	// Prior-live eligibility. This is the marker the reap path writes when the
	// server it was confirmed in goes away, and it is what keeps a restore from
	// resurrecting shells nobody had open.
	restore := def.Restore
	switch {
	case restore == nil || !restore.Eligible:
		step.Action = ActionManual
		step.Reason = ReasonNotPriorLive
		step.Detail = "was not confirmed live in the previous tmux server; recreate it deliberately if you want it back"
		step.Agent = agentNoop(def, ReasonNotPriorLive)
		return step
	case in.CurrentServer != "" && restore.LastSeenServer == in.CurrentServer:
		// The shell's last confirmed server is the one running now, so it did not
		// die with a server: it exited inside this one. Recreating it would revive
		// something that was closed on purpose.
		step.Action = ActionManual
		step.Reason = ReasonDiedInThisServer
		step.Detail = "exited inside the running tmux server rather than with a server restart"
		step.Agent = agentNoop(def, ReasonDiedInThisServer)
		return step
	}

	// Working directory. A missing directory is a refusal with a reason, never a
	// fallback: resuming into the wrong repository is worse than an offline row.
	switch {
	case strings.TrimSpace(def.WorkDir) == "":
		step.Action = ActionRefuse
		step.Reason = ReasonNoWorkDir
		step.Detail = "no working directory was recorded for this shell"
		step.Agent = agentNoop(def, ReasonNoWorkDir)
		return step
	case !dirExists(def.WorkDir):
		step.Action = ActionRefuse
		step.Reason = ReasonMissingWorkDir
		step.Detail = "its working directory no longer exists; Sidecar will not substitute another one"
		step.Agent = agentNoop(def, ReasonMissingWorkDir)
		return step
	}

	if !in.Config.RecreateShells {
		step.Action = ActionSkip
		step.Reason = ReasonRecreateDisabled
		step.Detail = "shell recreation is disabled in configuration"
		step.Agent = agentNoop(def, ReasonRecreateDisabled)
		return step
	}

	// The shell will be recreated. Everything from here decides only whether an
	// agent runs in it.
	step.Action = ActionRecreateShell
	step.Reason = ReasonRecreatable
	step.Detail = "recreate the shell under its own name and working directory"

	agent := decideAgent(in, sh, policy, providerAvailable, winners)
	step.Agent = agent
	switch {
	case agent == nil:
		return step
	case agent.Resume:
		step.Action = ActionResumeAgent
		step.Reason = ReasonPolicyResume
		step.Detail = "recreate the shell and resume its exact conversation"
		step.ExternalExecution = true
	case agent.Reason == ReasonKindDisagreement:
		// The shell part is unaffected and still happens. Naming both providers
		// here is the whole remediation an affected user gets: the record is not
		// rewritten by a restore, so the sentence has to be enough to fix it by
		// hand or to start the agent deliberately.
		step.Detail = fmt.Sprintf(
			"recreate the shell; its record names two providers (%s and %s), so Sidecar will not resume its conversation with either",
			agent.Kind, kindConflict(def))
	case agent.Reason == ReasonNeedsConfirmation:
		// The shell part is authorized; the agent part is waiting on a human.
		// ExternalExecution stays true because the question being asked is
		// precisely "may this run an agent", and answering it with "no external
		// execution would occur" would be answering the wrong question.
		step.NeedsConfirmation = true
		step.ExternalExecution = true
		step.Detail = "recreate the shell; resuming its conversation needs confirmation"
	}
	return step
}

// agentNoop describes the agent verdict for a shell whose shell-level decision
// already ended the story, so the reason is the same one.
func agentNoop(def shellstate.Definition, reason Reason) *AgentStep {
	ref, kind, ok := binding(def)
	if !ok && strings.TrimSpace(kind) == "" {
		return nil
	}
	out := &AgentStep{Kind: kind, Reason: reason}
	if ok {
		out.RefKind = string(ref.Kind)
		out.Reported = ref.Reported
	}
	return out
}

func decideAgent(in Input, sh Shell, policy agentsession.Policy, providerAvailable func(string) bool, winners map[string]agentsession.Holder) *AgentStep {
	def := sh.Def
	ref, kind, bound := binding(def)
	if !bound && strings.TrimSpace(kind) == "" {
		return nil
	}
	out := &AgentStep{Kind: kind}
	if bound {
		out.RefKind = string(ref.Kind)
		out.Reported = ref.Reported
	}

	// Before anything else the record says: does it agree with itself? A
	// mis-attributed hook report used to set Agent.Kind and leave AgentType
	// alone, and the resulting record names one provider's conversation and
	// another provider's CLI. Nothing downstream can tell which half is the
	// mistake, so this is checked ahead of the reference, deduplication, the
	// catalog and policy — every one of which would otherwise answer confidently
	// about a record that has no single answer.
	if conflict := kindConflict(def); conflict != "" {
		out.Reason = ReasonKindDisagreement
		return out
	}

	if !bound {
		out.Reason = ReasonNoSessionRef
		return out
	}
	if !ref.Reported {
		out.Reason = ReasonUnreportedRef
		return out
	}

	// Deduplication before capability: if another shell won this conversation,
	// the answer is the same whatever this provider can do, and naming the
	// winner is more useful than naming a capability.
	if winner, ok := winners[agentsession.DedupKey(kind, ref)]; ok && winner.Session != def.TmuxName {
		out.Reason = ReasonDuplicateRef
		out.ConflictWith = winner.Session
		return out
	}

	// Ask the catalog for a real resume rather than trusting a capability bit:
	// PlanResume is the same call the executor makes, so a plan that says
	// resume-agent is a plan whose argv has already been built once.
	if _, err := agentsession.PlanResume(kind, ref); err != nil {
		out.Reason = resumeRefusal(err)
		return out
	}
	if !providerAvailable(kind) {
		out.Reason = ReasonProviderUnavailable
		return out
	}

	// Policy last, so a shell that could not resume anyway reports the concrete
	// obstacle rather than a policy that was never the binding constraint.
	effective := in.Config.ResumeAgents
	switch policy {
	case agentsession.PolicyShell:
		out.Reason = ReasonResumeOff
		return out
	case agentsession.PolicyResume:
		// An explicit per-shell resume is the user's standing authorization for
		// this shell, the same way auto is for the machine.
		effective = ResumeAuto
	}

	if !in.Request.Agents && !in.Request.Startup {
		out.Reason = ReasonAgentsNotRequested
		return out
	}
	switch effective {
	case ResumeOff:
		out.Reason = ReasonResumeOff
		return out
	case ResumeAuto:
		out.Resume = true
		out.Reason = ReasonPolicyResume
		return out
	default: // ResumeAsk
		if in.Request.Confirmed {
			out.Resume = true
			out.Reason = ReasonPolicyResume
			return out
		}
		out.Reason = ReasonNeedsConfirmation
		return out
	}
}

// resumeRefusal maps agentsession's typed refusals onto plan reasons.
func resumeRefusal(err error) Reason {
	switch {
	case errors.Is(err, shellstate.ErrKindDisagreement):
		// The executor re-reads the record at the moment of mutation, so this is
		// the same refusal the plan already made, arriving through the other
		// door. Mapping it to the generic "provider rejected" reason would hide
		// a corrupt record behind a capability answer.
		return ReasonKindDisagreement
	case errors.Is(err, agentsession.ErrUntrustedSource):
		return ReasonUnreportedRef
	case errors.Is(err, agentsession.ErrUnsupportedKind):
		return ReasonProviderNoResume
	default:
		return ReasonProviderRejectedRef
	}
}
