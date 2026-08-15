package workspaceops

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var DefaultEnvOverrides = map[string]string{
	"GOWORK": "off", "GOFLAGS": "", "NODE_OPTIONS": "", "NODE_PATH": "", "PYTHONPATH": "", "VIRTUAL_ENV": "",
}

func BuildEnvOverrides(mainRepoPath string) map[string]string {
	result := make(map[string]string, len(DefaultEnvOverrides))
	for key, value := range DefaultEnvOverrides {
		result[key] = value
	}
	file, err := os.Open(filepath.Join(mainRepoPath, ".worktree-env"))
	if err != nil {
		return result
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" {
			continue
		}
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		result[key] = value
	}
	return result
}

func GenerateSingleEnvCommand(overrides map[string]string) string {
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var commands []string
	for _, key := range keys {
		if value := overrides[key]; value == "" {
			commands = append(commands, "unset "+key)
		} else {
			commands = append(commands, "export "+key+"="+ShellQuote(value))
		}
	}
	return strings.Join(commands, "; ")
}

func ShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
