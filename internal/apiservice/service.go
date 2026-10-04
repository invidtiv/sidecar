// Package apiservice manages the per-user UI API process, never tmux.
package apiservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const Label = "com.marcus.sidecar.api"
const Unit = "sidecar-api.service"

// Exit is the last termination reported by the manager. Nil means unknown.
type Exit struct {
	Code   int    `json:"code"`
	Signal string `json:"signal,omitempty"`
}

type Status struct {
	Manager   string `json:"manager"`
	Label     string `json:"label"`
	File      string `json:"file"`
	Installed bool   `json:"installed"`
	Loaded    bool   `json:"loaded"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	Version   string `json:"version"`
	LastExit  *Exit  `json:"last_exit"`
	Log       string `json:"log"`
	Message   string `json:"message"`
}

// Manager is the service-manager seam. Tests and headless callers inject it.
type Manager interface {
	Install(context.Context) error
	Uninstall(context.Context) error
	Status(context.Context) (Status, error)
}

// Runner is the only process boundary; it never runs a shell.
type Runner func(context.Context, string, ...string) ([]byte, error)

// Options pins the user's paths and the stable executable link into the job.
type Options struct {
	OS         string
	Home       string
	ConfigHome string
	StateDir   string
	ConfigPath string
	Executable string
	Path       string
	UID        int
	Run        Runner
}

type Native struct {
	options          Options
	file, label, log string
}

func New(options Options) (*Native, error) {
	if options.OS == "" {
		options.OS = runtime.GOOS
	}
	if options.OS != "darwin" && options.OS != "linux" {
		return nil, fmt.Errorf("API services are unavailable on %s; run `sidecar api serve` in the foreground", options.OS)
	}
	if strings.ContainsAny(options.Path, "\x00\r\n") {
		return nil, errors.New("PATH contains a line break; set a normal PATH and retry service install")
	}
	for name, path := range map[string]string{"home": options.Home, "state": options.StateDir, "config": options.ConfigPath, "executable": options.Executable} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return nil, fmt.Errorf("%s must be an absolute path without line breaks; reinstall with `sidecar api service install` from a normal user shell", name)
		}
	}
	if options.Run == nil {
		options.Run = func(ctx context.Context, command string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, command, args...).CombinedOutput()
		}
	}
	n := &Native{options: options, label: Label, log: filepath.Join(options.StateDir, "api", "service.log")}
	if options.OS == "darwin" {
		n.file = filepath.Join(options.Home, "Library", "LaunchAgents", Label+".plist")
	} else {
		base := options.ConfigHome
		if base == "" {
			base = filepath.Join(options.Home, ".config")
		}
		if !filepath.IsAbs(base) {
			return nil, errors.New("XDG_CONFIG_HOME must be absolute; unset it and retry `sidecar api service install`")
		}
		n.file = filepath.Join(base, "systemd", "user", Unit)
		n.label = Unit
		n.log = "journalctl --user -u " + Unit
	}
	return n, nil
}

func (n *Native) command(ctx context.Context, args ...string) ([]byte, error) {
	command := "systemctl"
	if n.options.OS == "darwin" {
		command = "launchctl"
	}
	output, err := n.options.Run(ctx, command, args...)
	if err != nil {
		return output, fmt.Errorf("%s %s: %w (%s); inspect %s, then retry `sidecar api service install` (Linux needs a running systemd user manager)", command, strings.Join(args, " "), err, strings.TrimSpace(string(output)), n.log)
	}
	return output, nil
}

func (n *Native) Install(ctx context.Context) error {
	status, err := n.Status(ctx)
	if err != nil {
		return err
	}
	if status.Loaded { // stop only this job, before replacing its definition
		if err := n.unload(ctx); err != nil {
			return err
		}
	}
	data := n.definition()
	if err := os.MkdirAll(filepath.Dir(n.file), 0o700); err != nil {
		return fmt.Errorf("create service directory: %w; check permissions on %s", err, filepath.Dir(n.file))
	}
	if err := os.MkdirAll(filepath.Join(n.options.StateDir, "api"), 0o700); err != nil {
		return fmt.Errorf("create API log directory: %w; check permissions on %s", err, n.options.StateDir)
	}
	if err := writeFile(n.file, data); err != nil {
		return fmt.Errorf("write service: %w; check permissions on %s and retry install", err, n.file)
	}
	if n.options.OS == "darwin" {
		if _, err := n.command(ctx, "enable", n.target()); err != nil {
			return err
		}
		_, err = n.command(ctx, "bootstrap", n.domain(), n.file)
	} else {
		if _, err := n.command(ctx, "--user", "daemon-reload"); err != nil {
			return err
		}
		_, err = n.command(ctx, "--user", "enable", "--now", Unit)
	}
	return err
}

func (n *Native) domain() string { return fmt.Sprintf("gui/%d", n.options.UID) }
func (n *Native) target() string { return n.domain() + "/" + Label }
func (n *Native) unload(ctx context.Context) error {
	if n.options.OS == "darwin" {
		_, err := n.command(ctx, "bootout", n.target())
		return err
	}
	_, err := n.command(ctx, "--user", "disable", "--now", Unit)
	return err
}

func (n *Native) Uninstall(ctx context.Context) error {
	status, err := n.Status(ctx)
	if err != nil {
		return err
	}
	if status.Loaded || (n.options.OS == "linux" && status.Installed) {
		if err := n.unload(ctx); err != nil {
			return err
		}
	}
	if err := os.Remove(n.file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove service: %w; check permissions on %s and retry uninstall", err, n.file)
	}
	if n.options.OS == "linux" && status.Installed {
		_, err = n.command(ctx, "--user", "daemon-reload")
	}
	return err
}

func writeFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".sidecar-service-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

// StableExecutable preserves an argv/PATH link only when it currently names
// this exact executable. Following that link covers managed and brew upgrades.
func StableExecutable(executable, invoked, path string) string {
	own, err := os.Stat(executable)
	if err != nil {
		return executable
	}
	candidates := []string{}
	if strings.ContainsRune(invoked, os.PathSeparator) {
		candidates = append(candidates, invoked)
	} else {
		for _, dir := range filepath.SplitList(path) {
			if dir != "" {
				candidates = append(candidates, filepath.Join(dir, invoked))
			}
		}
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && os.SameFile(own, info) {
			absolute, err := filepath.Abs(candidate)
			if err == nil {
				return absolute
			}
		}
	}
	return executable
}
