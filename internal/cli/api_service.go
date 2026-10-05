package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		Long: "Use launchd on macOS or a systemd user service/socket pair on Linux. install starts the API at login, uninstall stops only the API service and removes its definition. The manager holds the browser port across binary upgrades. No command changes tmux. install --ui DIR saves an absolute UI directory containing index.html as api.uiDir; --ui \"\" clears it. Omit --ui to keep the configured directory. The server reads api.uiDir on every start. On Linux use this command; Homebrew cannot generate socket units. On macOS use either this command or brew services to manage the service, not both.", Run: runAPIService}
	for _, name := range []string{"install", "uninstall", "status"} {
		summary := map[string]string{"install": "Install and start the API service", "uninstall": "Stop and remove the API service", "status": "Inspect the API service manager"}[name]
		command.Sub = append(command.Sub, &Command{Name: name, Summary: summary, Usage: "sidecar api service " + name + " [--json]",
			Flags:     []Flag{{Name: "--json", Summary: "Write service and socket state, PID, version and last exit as JSON", Bool: true}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}},
			ExitCodes: []ExitCode{{Code: 0, Summary: "success (status succeeds even when not installed or stopped)"}, {Code: 1, Summary: "manager or service operation failed; follow the message"}, {Code: 2, Summary: "usage error"}},
			Examples:  []Example{{Command: "sidecar api service " + name + " --json"}},
			Agent:     AgentDoc{Invocation: "sidecar api service " + name + " --json", Summary: summary}, Mutates: name != "status"})
		if name == "install" {
			sub := command.Sub[len(command.Sub)-1]
			sub.Usage = "sidecar api service install [--ui DIR] [--json]"
			sub.Long = command.Long
			sub.Flags = append(sub.Flags, Flag{Name: "--ui", Arg: "DIR", Summary: "Save the built UI directory (must contain index.html); an empty value clears it"})
			sub.Examples = append(sub.Examples, Example{Command: "sidecar api service install --ui ~/.local/share/sidecar/ui/current"})
		}
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
	var valueFlags []string
	if args[0] == "install" {
		valueFlags = []string{"--ui"}
	}
	flags, err := parseAPIFlags(args[1:], []string{"--json"}, valueFlags)
	if err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, RenderHelp(sub))
		return 2
	}
	uiDir, setUI := flags.values["--ui"]
	if setUI && uiDir != "" {
		uiDir, err = validateAPIUIDir(uiDir)
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
	}
	var configuredUI *string
	if args[0] == "install" {
		cfg, err := config.Load()
		if err != nil {
			cliErrf(env.Stderr, "load API config: %v; fix %s and retry\n", err, config.ConfigPath())
			return 1
		}
		configuredUI = &cfg.API.UIDir
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
		managedPID := 0
		if status.Running {
			managedPID = status.PID
		}
		if err := uiapi.CheckServiceInstall(env.StateDir, managedPID); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		if setUI {
			if err := config.SaveAPIUIDir(uiDir); err != nil {
				cliErrf(env.Stderr, "save UI directory: %v; check %s and retry\n", err, config.ConfigPath())
				return 1
			}
			configuredUI = &uiDir
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
	status.UIDir = configuredUI
	if args[0] == "status" {
		if cfg, err := config.Load(); err == nil {
			status.UIDir = &cfg.API.UIDir
		} else {
			status.UIConfigError = fmt.Sprintf("load API config: %v; fix %s and retry", err, config.ConfigPath())
		}
	}
	if flags.bools["--json"] {
		return writeCLIJSON(env, status)
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s\n%s: installed=%t loaded=%t running=%t pid=%d version=%s\n", status.Message, status.Manager, status.Installed, status.Loaded, status.Running, status.PID, status.Version)
	_, _ = fmt.Fprintf(env.Stdout, "Browser socket: installed=%t loaded=%t listening=%t\n", status.Socket.Installed, status.Socket.Loaded, status.Socket.Listening)
	if status.UIDir != nil {
		printAPIUIDir(env, *status.UIDir)
	} else {
		_, _ = fmt.Fprintln(env.Stdout, "UI directory: unknown")
	}
	if status.UIConfigError != "" {
		_, _ = fmt.Fprintln(env.Stdout, status.UIConfigError)
	}
	if status.LastExit != nil {
		_, _ = fmt.Fprintf(env.Stdout, "Last exit: code=%d signal=%s\n", status.LastExit.Code, status.LastExit.Signal)
	}
	return 0
}

func validateAPIUIDir(dir string) (string, error) {
	abs, err := filepath.Abs(config.ExpandPath(dir))
	if err == nil {
		var root *os.Root
		root, err = os.OpenRoot(abs)
		if err == nil {
			defer func() { _ = root.Close() }()
			var info os.FileInfo
			info, err = root.Stat("index.html")
			if err == nil && !info.Mode().IsRegular() {
				err = fmt.Errorf("index.html must be a regular file")
			}
		}
	}
	if err != nil {
		return "", fmt.Errorf("--ui %q must be a built UI directory containing index.html: %w; build your UI first, then run `sidecar api service install --ui DIR` with its output directory", dir, err)
	}
	return abs, nil
}

func printAPIUIDir(env Env, dir string) {
	if dir == "" {
		dir = "none (API only)"
	}
	_, _ = fmt.Fprintf(env.Stdout, "UI directory: %s\n", dir)
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
