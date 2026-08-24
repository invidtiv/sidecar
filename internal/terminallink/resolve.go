package terminallink

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/marcus/sidecar/internal/terminalperf"
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
	terminalperf.Record(terminalperf.SynchronousResolverCall)
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

// ResolveCommit reports whether rev names a commit in workdir.
func ResolveCommit(workdir, rev string) (oid string, ok bool) {
	if workdir == "" || rev == "" || containsControl(rev) || strings.ContainsAny(rev, " \t\n") {
		return "", false
	}
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	oid = strings.TrimSpace(string(out))
	return oid, oid != ""
}

// ResolveGitSpec existence-gates a scanner token: a lowercase hex rev,
// "commit <rev>", or A..B / A...B. HEAD and branch names are refused here
// even if git would accept them — those are CLI-only.
func ResolveGitSpec(workdir, raw string) (value string, extra Extra, ok bool) {
	terminalperf.Record(terminalperf.SynchronousResolverCall)
	extra = Extra{Raw: raw}
	a, b, parsed := parseGitSpecToken(raw)
	if !parsed {
		return "", extra, false
	}
	if _, ok := ResolveCommit(workdir, a); !ok {
		return "", extra, false
	}
	if b != "" {
		if _, ok := ResolveCommit(workdir, b); !ok {
			return "", extra, false
		}
	}
	return raw, extra, true
}

func parseGitSpecToken(raw string) (a, b string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if a, b, ok := splitDottedRevs(raw); ok {
		return a, b, true
	}
	if rest, found := strings.CutPrefix(raw, "commit"); found {
		rev := strings.TrimSpace(rest)
		if gitRevExact(rev) {
			return rev, "", true
		}
		return "", "", false
	}
	if gitRevExact(raw) {
		return raw, "", true
	}
	return "", "", false
}

func splitDottedRevs(raw string) (a, b string, ok bool) {
	for _, dots := range []string{"...", ".."} {
		i := strings.Index(raw, dots)
		if i <= 0 {
			continue
		}
		left, right := raw[:i], raw[i+len(dots):]
		if gitRevExact(left) && gitRevExact(right) {
			return left, right, true
		}
	}
	return "", "", false
}

func gitRevExact(s string) bool {
	n := len(s)
	if n < 7 || n > 64 {
		return false
	}
	for i := 0; i < n; i++ {
		c := s[i]
		if c < '0' || (c > '9' && (c < 'a' || c > 'f')) {
			return false
		}
	}
	return true
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
