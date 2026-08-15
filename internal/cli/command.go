package cli

import (
	"context"
	"io"
)

// Env holds the execution environment passed to a command handler.
type Env struct {
	Stdout   io.Writer
	Stderr   io.Writer
	StateDir string
	Ctx      context.Context
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
