// Package agentbroadcast plans and delivers one prompt to a set of live
// managed agents. It is the application core behind sidecar agent broadcast
// and the Broadcast to agents modal; both are thin callers of Plan and Send.
package agentbroadcast

import (
	"context"
	"fmt"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/managedtarget"
)

const (
	ScopeProject = "project"
	ScopeAll     = "all"

	OutcomeWouldSend Outcome = "would_send"
	OutcomeSubmitted Outcome = "submitted"
	OutcomeSkipped   Outcome = "skipped"
	OutcomeUnknown   Outcome = "unknown"

	reasonSender = "sender"
	reasonStatus = "status"

	// ReasonExcluded is the verdict a row carries when the caller deselected
	// it: nothing was attempted and nothing refused it. The modal reads it to
	// keep a deselection out of the result notifications — the user already
	// knows, having just unchecked the box.
	ReasonExcluded = "excluded"
)

type Outcome string

type Service struct {
	Control agentcontrol.Service
	// Candidates returns the same enumeration agent list uses: every managed
	// shell/worktree the scope can see, one row per pane. Injected so this
	// package does not import internal/cli.
	Candidates func(ctx context.Context, req PlanRequest) ([]managedtarget.Target, error)
}

type PlanRequest struct {
	// ScopeKind is "project", "all", or empty when --to alone is the set.
	// Candidates still needs a search universe: empty/--to-only means all
	// registered projects, matching agent prompt TARGET resolution.
	ScopeKind     string // "project" | "all" | ""
	Project       string
	To            []string
	Exclude       []string
	Status        []agentcontrol.Status // empty => PromptableStates()
	IncludeSelf   bool
	SenderSession string // SIDECAR_SHELL tmux name; dropped unless IncludeSelf
	SenderName    string
	SenderProject string
	FromUser      bool // TUI: envelope says "the user"
	Raw           bool
}

type Scope struct {
	Kind    string `json:"kind,omitempty"`
	Project string `json:"project,omitempty"`
}

type Reason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Recipient struct {
	Target  agentcontrol.Target         `json:"target"`
	Agent   agentcontrol.AgentState     `json:"agent"`
	Outcome Outcome                     `json:"outcome"`
	Reason  *Reason                     `json:"reason,omitempty"`
	Receipt *agentcontrol.PromptReceipt `json:"receipt,omitempty"`
}

type Plan struct {
	Scope              Scope       `json:"scope"`
	Raw                bool        `json:"raw,omitempty"`
	FromUser           bool        `json:"-"`
	SenderName         string      `json:"-"`
	SenderProject      string      `json:"-"`
	Recipients         []Recipient `json:"recipients"`
	ShellsWithoutAgent int         `json:"shellsWithoutAgent"`
}

type Summary struct {
	Submitted          int `json:"submitted"`
	Skipped            int `json:"skipped"`
	Unknown            int `json:"unknown"`
	ShellsWithoutAgent int `json:"shellsWithoutAgent"`
}

type Result struct {
	Text       string      `json:"text"`
	Scope      Scope       `json:"scope"`
	Recipients []Recipient `json:"recipients"`
	Summary    Summary     `json:"summary"`
}

// Envelope prefixes delivered text so a receiving agent can tell a broadcast
// from its own user typing. sender is already framed: `"shell" in project`
// or `the user`.
func Envelope(sender, text string) string {
	return fmt.Sprintf("[Sidecar broadcast from %s] %s", sender, text)
}

func envelopeSender(fromUser bool, name, project string) string {
	if fromUser {
		return "the user"
	}
	return fmt.Sprintf("%q in %s", name, project)
}

func controlTarget(t managedtarget.Target) agentcontrol.Target {
	return agentcontrol.Target{Host: t.Host, Project: t.Project, Session: t.Session, Name: t.Name, Namespace: t.Namespace}
}
