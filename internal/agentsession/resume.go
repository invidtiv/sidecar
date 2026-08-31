package agentsession

import (
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/agentcatalog"
)

// Policy is a per-shell restore policy.
type Policy string

const (
	// PolicyInherit follows the configured machine default.
	PolicyInherit Policy = "inherit"
	// PolicyShell recreates the shell but never resumes the agent.
	PolicyShell Policy = "shell"
	// PolicyResume recreates the shell and resumes the exact conversation.
	PolicyResume Policy = "resume"
	// PolicyNever leaves the shell alone entirely.
	PolicyNever Policy = "never"
)

// Policies lists the vocabulary in a stable order.
func Policies() []Policy {
	return []Policy{PolicyInherit, PolicyShell, PolicyResume, PolicyNever}
}

func (p Policy) valid() bool {
	for _, known := range Policies() {
		if p == known {
			return true
		}
	}
	return false
}

// ParsePolicy accepts a policy name from a caller. An empty value is Inherit,
// which is what an older record without the field means.
func ParsePolicy(value string) (Policy, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return PolicyInherit, nil
	}
	policy := Policy(trimmed)
	if !policy.valid() {
		return "", fmt.Errorf("unknown restore policy %q; use one of: inherit, shell, resume, never", value)
	}
	return policy, nil
}

// ResumePlan is a resume expressed as an argument vector.
//
// There is no command-string field, deliberately. A string here would be the one
// place a session identifier could reach a shell, and every consumer — the
// Conversations UI, the CLI, and the restore coordinator — would then be one
// convenience away from interpolating it.
type ResumePlan struct {
	// Kind is the catalog family id the plan resumes.
	Kind string `json:"kind"`
	// Argv is the exact command to run.
	Argv []string `json:"argv"`
	// Ref is the reference being resumed.
	Ref Ref `json:"ref"`
}

// PlanResume builds the structured resume for a bound conversation.
//
// It refuses an unreported reference: only an official integration's report is
// exact enough to resume from, and a candidate discovered by matching working
// directories is a suggestion for a human, not a command to run.
func PlanResume(kind string, ref Ref) (ResumePlan, error) {
	if ref.Empty() {
		return ResumePlan{}, fmt.Errorf("%w: there is no session reference to resume", ErrInvalidRef)
	}
	if !ref.Reported {
		return ResumePlan{}, fmt.Errorf("%w: %q is not an official Sidecar integration, so its reference is not auto-resumable",
			ErrUntrustedSource, ref.Source)
	}
	family, ok := agentcatalog.Lookup(kind)
	if !ok {
		return ResumePlan{}, fmt.Errorf("unknown agent kind %q", kind)
	}
	if !family.CanResume() {
		return ResumePlan{}, fmt.Errorf("%w: %s has no native resume command", ErrUnsupportedKind, family.Name)
	}
	if !family.ResumesKind(string(ref.Kind)) {
		return ResumePlan{}, fmt.Errorf("%w: %s cannot resume from a %q reference", ErrUnsupportedKind, family.Name, ref.Kind)
	}
	argv, err := family.ResumeArgv(string(ref.Kind), ref.Value, nil)
	if err != nil {
		return ResumePlan{}, err
	}
	return ResumePlan{Kind: family.ID, Argv: argv, Ref: ref}, nil
}

// CanResumeKind reports whether a catalog family has a native resume at all.
// Surfaces use it to show or hide a resume affordance without building a plan.
func CanResumeKind(kind string) bool {
	family, ok := agentcatalog.Lookup(kind)
	return ok && family.CanResume()
}
