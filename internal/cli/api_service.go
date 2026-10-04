package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/marcus/sidecar/internal/apiservice"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uiapi"
)

// The factory is replaced by tests so no test loads a real service manager.
var apiServiceManager = func(env Env) (apiservice.Manager, error) {
	if config.IsolationAsserted() {
		return nil, fmt.Errorf("service-manager access is disabled for isolated proofs; run `sidecar api serve --port 0` in the foreground")
	}
	if os.Getuid() == 0 {
		return nil, fmt.Errorf("API services are per-user; run `sidecar api service` as your login user without sudo")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home: %w; set HOME and retry", err)
	}
	executable, err := apiExecutablePath()
	if err != nil {
		return nil, err
	}
	return apiservice.New(apiservice.Options{OS: runtime.GOOS, Home: home, ConfigHome: os.Getenv("XDG_CONFIG_HOME"), StateDir: env.StateDir,
		ConfigPath: config.ConfigPath(), Executable: executable, Path: os.Getenv("PATH"), UID: os.Getuid()})
}

func apiExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find executable: %w; retry from an installed Sidecar binary", err)
	}
	return apiservice.StableExecutable(executable, os.Args[0], os.Getenv("PATH")), nil
}

func apiServiceCommand() *Command {
	command := &Command{Name: "service", Summary: "Manage the per-user UI API service", Usage: "sidecar api service <install|uninstall|status> [--json]",
		Long: "Use launchd on macOS or a systemd user unit on Linux. install starts the API at login, uninstall stops only the API service and removes its definition. No command changes tmux. The server reads api.uiDir from config on every start. Use either this command or brew services to manage the service, not both.", Run: runAPIService}
	for _, name := range []string{"install", "uninstall", "status"} {
		summary := map[string]string{"install": "Install and start the API service", "uninstall": "Stop and remove the API service", "status": "Inspect the API service manager"}[name]
		command.Sub = append(command.Sub, &Command{Name: name, Summary: summary, Usage: "sidecar api service " + name + " [--json]",
			Flags:     []Flag{{Name: "--json", Summary: "Write installed, loaded, running, PID, version and last exit as JSON", Bool: true}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}},
			ExitCodes: []ExitCode{{Code: 0, Summary: "success (status succeeds even when not installed or stopped)"}, {Code: 1, Summary: "manager or service operation failed; follow the message"}, {Code: 2, Summary: "usage error"}},
			Examples:  []Example{{Command: "sidecar api service " + name + " --json"}},
			Agent:     AgentDoc{Invocation: "sidecar api service " + name + " --json", Summary: summary}, Mutates: name != "status"})
	}
	return command
}

func runAPIService(env Env, args []string) int {
	cmd := apiSubcommand("service")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub == nil {
		cliErrf(env.Stderr, "unknown service command %q\n\n%s", args[0], RenderHelp(cmd))
		return 2
	}
	if len(args) == 2 && isHelp(args[1]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(sub))
		return 0
	}
	flags, err := parseAPIFlags(args[1:], []string{"--json"}, nil)
	if err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, RenderHelp(sub))
		return 2
	}
	manager, err := apiServiceManager(env)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	switch args[0] {
	case "install":
		// Refuse to spawn a restart loop next to a foreground server. The same
		// manager's running PID is safe to replace; no other process is stopped.
		status, statusErr := manager.Status(ctx)
		if statusErr != nil {
			cliErrln(env.Stderr, statusErr)
			return 1
		}
		if endpoint, endpointErr := uiapi.ReadEndpoint(env.StateDir); endpointErr == nil && (!status.Running || status.PID != endpoint.PID) {
			cliErrf(env.Stderr, "API server pid %d is already running outside this service; stop that API process, then retry `sidecar api service install`\n", endpoint.PID)
			return 1
		}
		err = manager.Install(ctx)
	case "uninstall":
		err = manager.Uninstall(ctx)
	}
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	status, err := manager.Status(ctx)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	apiServiceVersion(ctx, env, &status)
	if flags.bools["--json"] {
		return writeCLIJSON(env, status)
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s\n%s: installed=%t loaded=%t running=%t pid=%d version=%s\n", status.Message, status.Manager, status.Installed, status.Loaded, status.Running, status.PID, status.Version)
	if status.LastExit != nil {
		_, _ = fmt.Fprintf(env.Stdout, "Last exit: code=%d signal=%s\n", status.LastExit.Code, status.LastExit.Signal)
	}
	return 0
}

func apiServiceVersion(ctx context.Context, env Env, status *apiservice.Status) {
	if !status.Running {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := uiapi.NewLocalClient(env.StateDir)
	if err != nil {
		return
	}
	observed, err := client.Status(ctx)
	if err == nil && observed.PID == status.PID {
		status.Version = observed.ServerVersion
	}
}
