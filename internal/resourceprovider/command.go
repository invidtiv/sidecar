package resourceprovider

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/marcus/sidecar/internal/resource"
)

// CommandProvider is the default Provider: a short-lived child process per
// request, one JSON object in, one JSON object out.
type CommandProvider struct {
	instance string
	argv     []string
	dir      string
	env      []string

	describeTimeout time.Duration
	resolveTimeout  time.Duration

	runner Runner
	host   HostInfo
	log    *slog.Logger
}

var _ Provider = (*CommandProvider)(nil)

// CommandConfig is everything a CommandProvider needs. It is resolved once, at
// construction, so no invocation reads configuration or the environment.
type CommandConfig struct {
	Instance string
	Argv     []string
	// Dir is the neutral working directory — a Sidecar config directory, never
	// a selected repository.
	Dir string
	// PassEnv names variables whose current values are inherited on top of the
	// documented base environment.
	PassEnv []string
	// HostEnv is the os.Environ()-shaped environment to draw from.
	HostEnv []string
	// ResolveTimeout is clamped; zero takes the default.
	ResolveTimeout time.Duration
	// Runner defaults to ExecRunner.
	Runner Runner
	// Host identifies Sidecar to the provider.
	Host HostInfo
	// Log receives metadata-only invocation records. Nil discards them.
	Log *slog.Logger
}

// NewCommandProvider builds a provider over a configured argv.
func NewCommandProvider(cfg CommandConfig) (*CommandProvider, error) {
	if cfg.Instance == "" {
		return nil, errors.New("resourceprovider: instance id is required")
	}
	if len(cfg.Argv) == 0 || cfg.Argv[0] == "" {
		return nil, errors.New("resourceprovider: command argv is required")
	}
	runner := cfg.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return &CommandProvider{
		instance:        cfg.Instance,
		argv:            append([]string(nil), cfg.Argv...),
		dir:             cfg.Dir,
		env:             BuildEnv(cfg.PassEnv, cfg.HostEnv),
		describeTimeout: resource.DescribeTimeout,
		resolveTimeout:  resource.ClampResolveTimeout(cfg.ResolveTimeout),
		runner:          runner,
		host:            cfg.Host,
		log:             cfg.Log,
	}, nil
}

// Instance reports the configured instance ID.
func (p *CommandProvider) Instance() string { return p.instance }

// Argv exposes the resolved command for diagnostics. It is a copy.
func (p *CommandProvider) Argv() []string { return append([]string(nil), p.argv...) }

// Env exposes the computed child environment for diagnostics. Values are real,
// so a caller that prints this must redact — `terminal-links check` reports
// names only.
func (p *CommandProvider) Env() []string { return append([]string(nil), p.env...) }

// ResolveTimeout reports the clamped per-invocation timeout.
func (p *CommandProvider) ResolveTimeout() time.Duration { return p.resolveTimeout }

// Describe runs the describe method and validates the result.
func (p *CommandProvider) Describe(ctx context.Context) (Description, error) {
	req := Request{
		Protocol: resource.Protocol,
		Method:   MethodDescribe,
		Instance: p.instance,
	}
	if p.host.Name != "" || p.host.Version != "" {
		host := p.host
		req.Host = &host
	}
	resp, err := p.invoke(ctx, MethodDescribe, req, p.describeTimeout)
	if err != nil {
		return Description{}, err
	}
	if resp.Error != nil {
		return Description{}, resource.SanitizeError(resp.Error)
	}
	return ValidateDescription(p.instance, resp.Provider, resp.Matchers)
}

// Resolve runs the resolve method and sanitizes the document.
func (p *CommandProvider) Resolve(ctx context.Context, ref resource.Reference) (resource.Document, error) {
	if !ref.Valid() || ref.Instance != p.instance {
		return resource.Document{}, &TransportError{
			Instance: p.instance,
			Method:   MethodResolve,
			Reason:   ReasonInvalidRequest,
			Detail:   "reference is not addressed to this instance or exceeds its bounds",
		}
	}
	req := Request{
		Protocol: resource.Protocol,
		Method:   MethodResolve,
		Instance: p.instance,
		Params:   &ResolveParams{Matcher: ref.Matcher, Locator: ref.Locator},
	}
	resp, err := p.invoke(ctx, MethodResolve, req, p.resolveTimeout)
	if err != nil {
		return resource.Document{}, err
	}
	if resp.Error != nil {
		return resource.Document{}, resource.SanitizeError(resp.Error)
	}
	doc, rerr := resource.SanitizeDocument(resp.Resource)
	if rerr != nil {
		return resource.Document{}, rerr
	}
	return doc, nil
}

// invoke is the whole process boundary: encode, run, decode, log. Everything it
// records is metadata — instance, method, duration, outcome, byte counts — and
// nothing else ever reaches a log line.
func (p *CommandProvider) invoke(ctx context.Context, method string, req Request, timeout time.Duration) (_ *Response, err error) {
	payload, marshalErr := json.Marshal(req)
	if marshalErr != nil {
		return nil, &TransportError{Instance: p.instance, Method: method, Reason: ReasonInvalidRequest, Detail: "request could not be encoded", Err: marshalErr}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, runErr := p.runner.Run(runCtx, RunSpec{
		Argv:      p.argv,
		Dir:       p.dir,
		Env:       p.env,
		Stdin:     payload,
		MaxStdout: resource.MaxResponseBytes,
	})

	defer func() { p.record(method, result, err) }()

	if runErr != nil {
		return nil, &TransportError{Instance: p.instance, Method: method, Reason: ReasonSpawn, Detail: "the provider command could not be run", Err: runErr}
	}
	if result.TimedOut {
		reason := ReasonTimeout
		if ctx.Err() != nil {
			reason = ReasonCanceled
		}
		return nil, &TransportError{Instance: p.instance, Method: method, Reason: reason, Detail: "the process group was killed"}
	}
	if result.StdoutTruncated {
		return nil, &TransportError{Instance: p.instance, Method: method, Reason: ReasonOversize, Detail: "stdout exceeded the response byte limit"}
	}
	if result.ExitCode != 0 {
		return nil, &TransportError{Instance: p.instance, Method: method, Reason: ReasonExit, Detail: "the provider exited non-zero"}
	}
	resp, reason, detail := decodeResponse(result.Stdout)
	if reason != "" {
		return nil, &TransportError{Instance: p.instance, Method: method, Reason: reason, Detail: detail}
	}
	return resp, nil
}

func (p *CommandProvider) record(method string, result RunResult, err error) {
	if p.log == nil {
		return
	}
	// Locator, title, body, URL, credentials, stdout and stderr are absent by
	// construction: none of them is in scope here.
	p.log.Debug("terminal resource provider invocation",
		"instance", p.instance,
		"method", method,
		"duration_ms", result.Duration.Milliseconds(),
		"outcome", OutcomeCode(err),
		"stdout_bytes", result.StdoutBytes,
		"stderr_bytes", result.StderrBytes,
		"exit_code", result.ExitCode,
	)
}
