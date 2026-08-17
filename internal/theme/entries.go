package theme

import (
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/styles"
)

// The theme library is one list, whatever surface is showing it: the `#` theme
// switcher modal, Configuration's Appearance page, and the inline picker in Add
// Project all build it here. Keeping the list, the filter, the swatch colors,
// and the preview/apply translation in one place is what lets those surfaces
// look and behave alike without any of them owning the others' rendering.

// Entry is one theme in the unified built-in + community list.
type Entry struct {
	Name          string // display name
	IsBuiltIn     bool
	ThemeKey      string // built-in: theme registry key; community: scheme name
	IsSeparator   bool   // non-selectable divider between the two libraries
	SeparatorText string // e.g. "Community Themes (453)"
}

// IsZero reports the empty entry, which is what "no theme chosen" looks like.
func (e Entry) IsZero() bool { return e.ThemeKey == "" && !e.IsSeparator }

// Same reports whether two entries name the same theme.
func (e Entry) Same(other Entry) bool {
	return e.IsBuiltIn == other.IsBuiltIn && e.ThemeKey == other.ThemeKey && !e.IsSeparator && !other.IsSeparator
}

// Counts is how many themes each library contributes, for the result count the
// design brief asks every theme list to show.
type Counts struct {
	BuiltIn   int
	Community int
}

// Total is every selectable theme.
func (c Counts) Total() int { return c.BuiltIn + c.Community }

// Summary is the "21 themes" line.
func (c Counts) Summary() string {
	return fmt.Sprintf("%d themes", c.Total())
}

// LibraryCounts reports the total theme count.
func LibraryCounts() Counts {
	return Counts{BuiltIn: len(styles.ListThemes()), Community: 0}
}

// List returns all themes in canonical order.
func List() []Entry {
	themes := styles.ListThemes()
	entries := make([]Entry, 0, len(themes))
	for _, name := range themes {
		displayName := name
		if t := styles.GetTheme(name); t.DisplayName != "" {
			displayName = t.DisplayName
		}
		entries = append(entries, Entry{Name: displayName, IsBuiltIn: true, ThemeKey: name})
	}
	return entries
}

// Filter narrows a list by case-insensitive substring on the display name.
func Filter(entries []Entry, query string) []Entry {
	query = strings.TrimSpace(query)
	if query == "" {
		return entries
	}
	lower := strings.ToLower(query)
	var matches []Entry
	for _, entry := range entries {
		if entry.IsSeparator {
			continue
		}
		if strings.Contains(strings.ToLower(entry.Name), lower) {
			matches = append(matches, entry)
		}
	}
	return matches
}

// CountSelectable counts the entries a cursor can land on.
func CountSelectable(entries []Entry) int {
	count := 0
	for _, entry := range entries {
		if !entry.IsSeparator {
			count++
		}
	}
	return count
}

// Swatch is the four colors that identify a theme at a glance: primary,
// success, secondary, and error. It returns nil for a separator or an unknown theme.
func Swatch(entry Entry) []string {
	if entry.IsSeparator || entry.ThemeKey == "" {
		return nil
	}
	t := styles.GetTheme(entry.ThemeKey)
	return []string{t.Colors.Primary, t.Colors.Success, t.Colors.Secondary, t.Colors.Error}
}

// Label returns empty string since all themes are unified.
func Label(entry Entry) string {
	return ""
}

// ThemeConfig is what saving an entry writes.
func ThemeConfig(entry Entry) config.ThemeConfig {
	return config.ThemeConfig{Name: entry.ThemeKey}
}

// EntryForConfig is the reverse: the entry a saved ThemeConfig selects.
func EntryForConfig(tc config.ThemeConfig) Entry {
	name := tc.Name
	if name == "" && tc.Community != "" {
		name = tc.Community
	}
	if name == "" {
		return Entry{}
	}
	t := styles.GetTheme(name)
	displayName := t.DisplayName
	if displayName == "" {
		displayName = t.Name
	}
	return Entry{Name: displayName, IsBuiltIn: true, ThemeKey: t.Name}
}

// GlobalEntry is the entry the global scope shows as current. Unlike
// EntryForConfig, an empty configuration is not "inherits from the level
// above" here — there is no level above the global scope — so it resolves to
// the same fresh-install theme ResolveTheme would apply, and the picker opens
// with its cursor on the theme actually on screen.
func GlobalEntry(tc config.ThemeConfig) Entry {
	if tc.Community == "" && tc.Name == "" {
		tc.Name = styles.FreshInstallTheme
	}
	return EntryForConfig(tc)
}

// Resolved is the theme an entry previews as. A preview deliberately carries no
// user overrides: the point of moving through the list is to see the theme
// itself, not another theme's customizations.
func Resolved(entry Entry) ResolvedTheme {
	if entry.IsBuiltIn {
		return ResolvedTheme{BaseName: entry.ThemeKey}
	}
	return ResolvedTheme{BaseName: "default", CommunityName: entry.ThemeKey}
}

// Preview applies an entry live. It is the same call every surface makes, so a
// preview looks identical wherever it is started from.
func Preview(entry Entry) {
	if entry.IsSeparator || entry.ThemeKey == "" {
		return
	}
	ApplyResolved(Resolved(entry))
}

// IndexOf finds an entry in a list, or -1.
func IndexOf(entries []Entry, target Entry) int {
	if target.IsZero() {
		return -1
	}
	for i, entry := range entries {
		if entry.Same(target) {
			return i
		}
	}
	return -1
}
