package workspaceops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/tty"
)

var agentDefaults = map[string]string{
	"claude": "claude", "codex": "codex", "copilot": "copilot", "aider": "aider", "antigravity": "agy",
	"cursor": "cursor-agent", "opencode": "opencode", "pi": "pi", "amp": "amp", "grok": "grok",
}

var agentSkipFlags = map[string]string{
	"claude": "--dangerously-skip-permissions", "codex": "--dangerously-bypass-approvals-and-sandbox", "aider": "--yes",
	"antigravity": "--dangerously-skip-permissions", "cursor": "-f", "amp": "--dangerously-allow-all", "grok": "--always-approve",
}

// AgentSkipFlag returns the CLI flag that opts this agent into auto-approve,
// or "" if the agent has no such flag. Creation forms use this to decide
// whether to show the auto-approve checkbox; do not copy this map elsewhere.
func AgentSkipFlag(agentType string) string {
	return agentSkipFlags[agentType]
}

var openCodeRunPrefix = regexp.MustCompile(`^(\S+)\s+run(\s+.*)?$`)

func ResolveAgentCommand(worktreePath, agentType string, configured map[string]string, skipPerms bool) string {
	if strings.TrimSpace(agentType) == "" {
		return ""
	}
	command := readAgentStart(worktreePath)
	if command == "" {
		for _, key := range []string{agentType, "*", "default"} {
			if command = sanitizeAgentCommand(configured[key]); command != "" {
				break
			}
		}
	}
	if command == "" {
		command = agentDefaults[agentType]
	}
	if command == "" {
		command = agentDefaults["claude"]
	}
	if agentType == "opencode" {
		if match := openCodeRunPrefix.FindStringSubmatch(command); len(match) > 0 {
			command = strings.TrimSpace(match[1] + match[2])
		}
	}
	if skipPerms && agentSkipFlags[agentType] != "" {
		command += " " + agentSkipFlags[agentType]
	}
	return command
}

func WorktreeSessionName(path, name string) string {
	value := filepath.Base(path)
	if strings.TrimSpace(value) == "" || value == "." {
		value = name
	}
	var cleaned strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			cleaned.WriteRune(r)
		} else if cleaned.Len() > 0 && !strings.HasSuffix(cleaned.String(), "-") {
			cleaned.WriteByte('-')
		}
	}
	return "sidecar-ws-" + strings.Trim(cleaned.String(), "-")
}

func readAgentStart(worktreePath string) string {
	data, err := os.ReadFile(filepath.Join(worktreePath, ".sidecar-agent-start"))
	if err != nil {
		return ""
	}
	data = bytes.TrimSpace(data)
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return ""
	}
	return sanitizeAgentCommand(string(data))
}

func sanitizeAgentCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\r\n") || !utf8.ValidString(command) {
		return ""
	}
	var cleaned strings.Builder
	for _, r := range command {
		if r == '\uFFFD' || r == '\uFEFF' || unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
			continue
		}
		cleaned.WriteRune(r)
	}
	command = strings.TrimSpace(cleaned.String())
	if command == "" {
		return ""
	}
	return command
}

type TmuxRunner interface {
	Run(context.Context, ...string) ([]byte, error)
}

type ExecTmuxRunner struct{}

func (ExecTmuxRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
}

type AgentLaunchSpec struct {
	SessionName, WorkDir, AgentCommand, TaskID string
	Env                                        map[string]string
	StartAgent                                 bool
}

type AgentLaunchResult struct {
	SessionName, PaneID string
	Reconnected         bool
}

func LaunchWorktreeSession(ctx context.Context, spec AgentLaunchSpec) (AgentLaunchResult, error) {
	return launchWorktreeSession(ctx, spec, ExecTmuxRunner{}, tty.NewSession)
}

func LaunchWorktreeSessionWithRunner(ctx context.Context, spec AgentLaunchSpec, runner TmuxRunner) (AgentLaunchResult, error) {
	return launchWorktreeSession(ctx, spec, runner, func(args ...string) error {
		_, err := runner.Run(ctx, args...)
		return err
	})
}

func launchWorktreeSession(ctx context.Context, spec AgentLaunchSpec, runner TmuxRunner, newSession func(...string) error) (AgentLaunchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := AgentLaunchResult{SessionName: spec.SessionName}
	if spec.SessionName == "" || spec.WorkDir == "" {
		return result, fmt.Errorf("session name and worktree path are required")
	}
	if _, err := runner.Run(ctx, "has-session", "-t", spec.SessionName); err == nil {
		result.Reconnected = true
		result.PaneID = paneIDWithRunner(ctx, spec.SessionName, runner)
		return result, nil
	}
	if err := newSession("new-session", "-d", "-s", spec.SessionName, "-c", spec.WorkDir); err != nil {
		return result, fmt.Errorf("create session: %w", err)
	}
	failCreatedSession := func(err error) (AgentLaunchResult, error) {
		if _, cleanupErr := runner.Run(context.Background(), "kill-session", "-t", spec.SessionName); cleanupErr != nil {
			return result, fmt.Errorf("%w; cleanup session: %v", err, cleanupErr)
		}
		return result, err
	}
	send := func(command string) error {
		if command == "" {
			return nil
		}
		output, err := runner.Run(ctx, "send-keys", "-t", spec.SessionName, command, "Enter")
		if err != nil {
			return fmt.Errorf("send session command: %s: %w", strings.TrimSpace(string(output)), err)
		}
		return nil
	}
	if err := send("export TD_SESSION_ID=" + ShellQuote(spec.SessionName)); err != nil {
		return failCreatedSession(err)
	}
	if err := send(GenerateSingleEnvCommand(spec.Env)); err != nil {
		return failCreatedSession(err)
	}
	if spec.TaskID != "" {
		if err := send("td start " + ShellQuote(spec.TaskID)); err != nil {
			return failCreatedSession(err)
		}
	}
	if spec.StartAgent {
		if strings.TrimSpace(spec.AgentCommand) == "" {
			return failCreatedSession(fmt.Errorf("agent command is empty"))
		}
		time.Sleep(100 * time.Millisecond)
		if err := send(spec.AgentCommand); err != nil {
			return failCreatedSession(fmt.Errorf("start agent: %w", err))
		}
	}
	result.PaneID = paneIDWithRunner(ctx, spec.SessionName, runner)
	return result, nil
}

func StartAgentInShell(ctx context.Context, sessionName, command string) error {
	return StartAgentInShellWithRunner(ctx, sessionName, command, ExecTmuxRunner{})
}

func StartAgentInShellWithRunner(ctx context.Context, sessionName, command string, runner TmuxRunner) error {
	return sendKeysToShell(ctx, sessionName, command, true, runner)
}

// TypeInShell types command into the session without pressing Enter, so the
// user can review it. This is the send-keys core behind resume injection.
func TypeInShell(ctx context.Context, sessionName, command string) error {
	return TypeInShellWithRunner(ctx, sessionName, command, ExecTmuxRunner{})
}

func TypeInShellWithRunner(ctx context.Context, sessionName, command string, runner TmuxRunner) error {
	return sendKeysToShell(ctx, sessionName, command, false, runner)
}

func sendKeysToShell(ctx context.Context, sessionName, command string, execute bool, runner TmuxRunner) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("agent command is empty")
	}
	args := []string{"send-keys", "-t", sessionName, command}
	if execute {
		args = append(args, "Enter")
	}
	output, err := runner.Run(ctx, args...)
	if err != nil {
		verb := "type command"
		if execute {
			verb = "start agent"
		}
		return fmt.Errorf("%s: %s: %w", verb, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func paneIDWithRunner(ctx context.Context, sessionName string, runner TmuxRunner) string {
	output, err := runner.Run(ctx, "list-panes", "-t", sessionName, "-F", "#{pane_id}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
}
