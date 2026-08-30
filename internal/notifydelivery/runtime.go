package notifydelivery

import (
	"context"
	"os/exec"
	"time"
)

const providerTimeout = 10 * time.Second

type Runner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) error
}

type ExecRunner struct{}

func (ExecRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

type Timer interface{ Stop() bool }

type Clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) Timer
}

type RealClock struct{}

func (RealClock) Now() time.Time                             { return time.Now() }
func (RealClock) AfterFunc(d time.Duration, fn func()) Timer { return time.AfterFunc(d, fn) }
