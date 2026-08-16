// Package pathcomplete offers directory completion for a typed path prefix.
//
// It is deliberately incapable of browsing: there is no "list everything"
// entry point, because Sidecar's Add Project form must never enumerate a
// directory before the user has typed a path prefix. A caller with an empty
// input gets nothing back.
package pathcomplete

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultLimit is how many candidates a form shows before it stops listing.
const DefaultLimit = 8

// Directories returns directory candidates for a typed prefix, formatted the
// way the user is typing them: a path that began with ~ comes back with ~.
//
// The empty input returns nil. So does a bare "~", "~/" or "/" with nothing
// after it: the user has expressed a root, not a prefix to complete.
func Directories(input string, limit int) []string {
	if limit <= 0 {
		limit = DefaultLimit
	}
	typed := strings.TrimSpace(input)
	// A bare root is a place, not a prefix: "/", "//", "~" and "~/" all name a
	// directory the user has not started typing into, and completing them would
	// be the enumeration this package refuses to do. At least one character
	// beyond the root is required.
	if typed == "" || typed == "~" || typed == "~/" || strings.Trim(typed, "/") == "" {
		return nil
	}

	home, _ := os.UserHomeDir()
	tilde := strings.HasPrefix(typed, "~/")

	expanded := typed
	if tilde && home != "" {
		expanded = filepath.Join(home, typed[2:])
		// filepath.Join drops a trailing separator, which is the difference
		// between "complete inside this directory" and "complete this name".
		if strings.HasSuffix(typed, "/") {
			expanded += string(os.PathSeparator)
		}
	}
	if !filepath.IsAbs(expanded) && !tilde {
		// A relative fragment with no directory part would mean scanning the
		// working directory from one character, which is the enumeration the
		// design rules out. Require a rooted prefix.
		if !strings.Contains(expanded, string(os.PathSeparator)) {
			return nil
		}
	}

	dir, prefix := filepath.Split(expanded)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var matches []string
	lowerPrefix := strings.ToLower(prefix)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
			continue
		}
		matches = append(matches, filepath.Join(dir, name))
	}
	sort.Strings(matches)
	if len(matches) > limit {
		matches = matches[:limit]
	}

	if tilde && home != "" {
		for i, match := range matches {
			if rel, err := filepath.Rel(home, match); err == nil && !strings.HasPrefix(rel, "..") {
				matches[i] = filepath.Join("~", rel)
			}
		}
	}
	return matches
}
