package theme

import (
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/community"
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

// Summary is the "12 built-in · 453 community" line.
func (c Counts) Summary() string {
	return fmt.Sprintf("%d built-in · %d community", c.BuiltIn, c.Community)
}

// LibraryCounts reports the size of each library.
func LibraryCounts() Counts {
	return Counts{BuiltIn: len(styles.ListThemes()), Community: len(community.ListSchemes())}
}

// List returns every theme: built-in first, then a separator, then community.
func List() []Entry {
	builtIn := styles.ListThemes()
	communityNames := community.ListSchemes()
	entries := make([]Entry, 0, len(builtIn)+len(communityNames)+1)
	for _, name := range builtIn {
		displayName := name
		if t := styles.GetTheme(name); t.DisplayName != "" {
			displayName = t.DisplayName
		}
		entries = append(entries, Entry{Name: displayName, IsBuiltIn: true, ThemeKey: name})
	}
	if len(communityNames) > 0 {
		entries = append(entries, Entry{
			IsSeparator:   true,
			SeparatorText: fmt.Sprintf("Community Themes (%d)", len(communityNames)),
		})
	}
	for _, name := range communityNames {
		entries = append(entries, Entry{Name: name, ThemeKey: name})
	}
	return entries
}

// Filter narrows a list by case-insensitive substring on the display name. A
// query drops the separator: with a filter running, the two libraries are one
// list of matches.
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

// Swatch is the four colors that identify a theme at a glance: a built-in shows
// its primary, success, secondary, and error; a community scheme shows red,
// green, blue, and purple. It returns nil for a separator or an unknown theme.
func Swatch(entry Entry) []string {
	if entry.IsSeparator || entry.ThemeKey == "" {
		return nil
	}
	if entry.IsBuiltIn {
		t := styles.GetTheme(entry.ThemeKey)
		return []string{t.Colors.Primary, t.Colors.Success, t.Colors.Secondary, t.Colors.Error}
	}
	scheme := community.GetScheme(entry.ThemeKey)
	if scheme == nil {
		return nil
	}
	return []string{scheme.Red, scheme.Green, scheme.Blue, scheme.Purple}
}

// Label names the library an entry belongs to, for the scope column.
func Label(entry Entry) string {
	if entry.IsBuiltIn {
		return "Built-in"
	}
	return "Community"
}

// ThemeConfig is what saving an entry writes. A community scheme is stored by
// name on top of the default base theme; the palette is computed at runtime.
func ThemeConfig(entry Entry) config.ThemeConfig {
	if entry.IsBuiltIn {
		return config.ThemeConfig{Name: entry.ThemeKey}
	}
	return config.ThemeConfig{Name: "default", Community: entry.ThemeKey}
}

// EntryForConfig is the reverse: the entry a saved ThemeConfig selects. It
// returns the zero entry for an empty configuration, which is what "inherits
// from the level above" means.
func EntryForConfig(tc config.ThemeConfig) Entry {
	if tc.Community != "" {
		return Entry{Name: tc.Community, ThemeKey: tc.Community}
	}
	if tc.Name == "" {
		return Entry{}
	}
	display := tc.Name
	if t := styles.GetTheme(tc.Name); t.DisplayName != "" {
		display = t.DisplayName
	}
	return Entry{Name: display, IsBuiltIn: true, ThemeKey: tc.Name}
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
