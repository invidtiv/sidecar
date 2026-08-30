package cli

import (
	"context"
	"io"

	"github.com/marcus/sidecar/internal/notifydelivery"
)

// Env holds the execution environment passed to a command handler.
type Env struct {
	Stdout               io.Writer
	Stderr               io.Writer
	StateDir             string
	Ctx                  context.Context
	NotificationDelivery notifydelivery.Coordinator
}

// Flag defines a command line flag.
type Flag struct {
	Name    string `json:"name"`
	Short   string `json:"short,omitempty"`
	Arg     string `json:"arg,omitempty"`
	Summary string `json:"summary"`
	Bool    bool   `json:"bool,omitempty"`
}

// ExitCode describes a command exit status.
type ExitCode struct {
	Code    int    `json:"code"`
	Summary string `json:"summary"`
}

// Example illustrates command usage.
type Example struct {
	Description string `json:"description,omitempty"`
	Command     string `json:"command"`
}

// TargetDoc describes target formats for commands like open.
type TargetDoc struct {
	Target  string `json:"target"`
	Summary string `json:"summary"`
}

// ArgSpec specifies positional argument requirements.
type ArgSpec struct {
	Min         int    `json:"min"`
	Max         int    `json:"max"` // -1 for unbounded
	Description string `json:"description,omitempty"`
}

// Command is a declarative command definition.
type Command struct {
	Name      string                  `json:"name"`
	Summary   string                  `json:"summary"`
	Usage     string                  `json:"usage"`
	Long      string                  `json:"long,omitempty"`
	Targets   []TargetDoc             `json:"targets,omitempty"`
	Flags     []Flag                  `json:"flags,omitempty"`
	Args      ArgSpec                 `json:"args,omitempty"`
	ExitCodes []ExitCode              `json:"exitCodes,omitempty"`
	Examples  []Example               `json:"examples,omitempty"`
	Sub       []*Command              `json:"subcommands,omitempty"`
	Run       func(Env, []string) int `json:"-"`

	// Mutates marks a command that changes state outside this process: a tmux
	// session, a git worktree, a manifest, the notification log.
	//
	// It is what makes SIDECAR_ISOLATED_STATE a guarantee rather than a claim.
	// A misconfigured proof run — the variable exported, the state tree still
	// the real one — is refused by Run before the handler is entered, so it
	// cannot get as far as `tmux new-session` or `git worktree add` and then
	// fail an assert on the write that follows. Per-write assertions still
	// exist and still fail closed; this only moves the refusal to before the
	// side effects instead of after the first of them (td-8d18de).
	//
	// Not published in the command tree: it is dispatch's business, not a
	// contract a caller reads.
	Mutates bool `json:"-"`

	// Launch is for a command that does not run non-interactively at all: it
	// records what the app should do and hands the process back to normal
	// startup by reporting handled=false. `sidecar setup` is the only one — it
	// starts Sidecar the ordinary way with Configuration open, rather than
	// printing a second settings interface into the terminal.
	Launch func(Env, []string) (handled bool, exitCode int) `json:"-"`

	// Agent is what `sidecar --agents` says about this command: the one line an
	// agent needs to decide whether to reach for it. Commands without one are
	// left out of that list, so it stays a short list of things worth doing
	// rather than a second copy of the help.
	Agent AgentDoc `json:"agent,omitempty"`
}

// AgentDoc is a command's agent-facing summary: an invocation and why it helps.
type AgentDoc struct {
	Invocation string `json:"invocation,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

// FindSubcommand looks up a direct child subcommand by name.
func (c *Command) FindSubcommand(name string) *Command {
	for _, sub := range c.Sub {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}
