package terminallink

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// userHomeDir is os.UserHomeDir, overridden in tests.
var userHomeDir = os.UserHomeDir

// ResolveFile maps a printed path onto a regular file on this machine.
//
// It tries the selected root first, then an absolute or ~/ path. The file
// must exist, be regular, and survive EvalSymlinks. It does not have to sit
// inside the selected worktree. Display is a root-relative slash path when
// the file is inside base, otherwise the resolved absolute path.
func ResolveFile(base, raw string) (display, absolute string, ok bool) {
	if raw == "" || containsControl(raw) {
		return "", "", false
	}
	expanded := expandHome(raw)

	baseResolved := ""
	if strings.TrimSpace(base) != "" {
		if resolved, err := filepath.EvalSymlinks(base); err == nil {
			baseResolved = filepath.Clean(resolved)
		}
	}

	if baseResolved != "" && !isHomeToken(raw) {
		if display, absolute, ok = acceptRegularFile(filepath.Join(baseResolved, expanded), baseResolved); ok {
			return display, absolute, true
		}
	}
	if isHomeToken(raw) || filepath.IsAbs(expanded) {
		return acceptRegularFile(expanded, baseResolved)
	}
	return "", "", false
}

// OpenRegular opens the EvalSymlinks result of absolute when it is a regular
// file. Callers own the returned handle.
func OpenRegular(absolute string) (*os.File, error) {
	if absolute == "" || containsControl(absolute) {
		return nil, fmt.Errorf("not a regular file")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	return os.Open(resolved)
}

func acceptRegularFile(target, baseResolved string) (display, absolute string, ok bool) {
	target = filepath.Clean(target)
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", false
	}
	if baseResolved != "" {
		if rel, err := filepath.Rel(baseResolved, resolved); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel), resolved, true
		}
	}
	return resolved, resolved, true
}

func expandHome(raw string) string {
	if !isHomeToken(raw) {
		return raw
	}
	home, err := userHomeDir()
	if err != nil || home == "" {
		return raw
	}
	if raw == "~" {
		return home
	}
	return filepath.Join(home, raw[2:])
}

func isHomeToken(raw string) bool {
	return raw == "~" || strings.HasPrefix(raw, "~/")
}

func containsControl(raw string) bool {
	for _, r := range raw {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Markdown reports whether path should open as rendered markdown.
func Markdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}
