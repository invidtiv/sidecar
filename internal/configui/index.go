package configui

import "strings"

// IndexEntry is one searchable setting: the page it lives on, the label the
// user would recognize, and extra words they might type instead.
//
// This is a hand-written static index, deliberately not a schema. A phase that
// adds real controls to a page appends its entries here — one entry per control
// the user could reasonably search for — and search keeps working with no other
// change.
type IndexEntry struct {
	Page     PageID
	Label    string
	Keywords []string
}

// settingsIndex seeds search with the settings visible in the design mockup.
// Keep it grouped by page and in the order the controls appear on that page.
var settingsIndex = []IndexEntry{
	// Sidecar Setup
	{Page: PageSetup, Label: "Add a project", Keywords: []string{"project", "path", "repo", "first run", "setup"}},
	{Page: PageSetup, Label: "tmux availability and repair", Keywords: []string{"tmux", "shell", "workspace", "install", "brew"}},
	{Page: PageSetup, Label: "Terminal colors", Keywords: []string{"truecolor", "24-bit", "color", "terminal"}},
	{Page: PageSetup, Label: "Agent instructions", Keywords: []string{"agents.md", "agent", "instructions", "guidance"}},
	{Page: PageSetup, Label: "Install tmux", Keywords: []string{"tmux", "install", "brew", "homebrew", "shell", "repair"}},
	{Page: PageSetup, Label: "Terminal color setup steps", Keywords: []string{"truecolor", "24-bit", "colorterm", "iterm", "ghostty", "kitty", "alacritty", "wezterm"}},
	{Page: PageSetup, Label: "Recheck setup", Keywords: []string{"recheck", "check", "again", "refresh", "readiness"}},

	// Appearance
	{Page: PageAppearance, Label: "Theme", Keywords: []string{"theme", "colors", "community", "appearance", "dark", "light"}},
	{Page: PageAppearance, Label: "Theme scope", Keywords: []string{"theme", "global", "project", "override", "scope"}},
	{Page: PageAppearance, Label: "Nerd Font icons", Keywords: []string{"nerd", "font", "icons", "glyphs", "pills"}},
	{Page: PageAppearance, Label: "Header clock", Keywords: []string{"clock", "time", "header"}},
	{Page: PageAppearance, Label: "Terminal title", Keywords: []string{"title", "terminal", "window", "tab"}},
	{Page: PageAppearance, Label: "Community themes", Keywords: []string{"community", "scheme", "base16", "theme", "search"}},
	{Page: PageAppearance, Label: "Project theme override", Keywords: []string{"theme", "project", "override", "scope", "per-project"}},

	// Projects
	{Page: PageProjects, Label: "Add project", Keywords: []string{"project", "add", "path", "location", "new"}},
	{Page: PageProjects, Label: "Initialize this directory", Keywords: []string{"git", "init", "repository", "main", "onboarding"}},
	{Page: PageProjects, Label: "Project location", Keywords: []string{"path", "location", "directory", "folder"}},
	{Page: PageProjects, Label: "Project theme", Keywords: []string{"theme", "project", "override"}},
	{Page: PageProjects, Label: "Open in application", Keywords: []string{"open in", "editor", "ide", "application"}},
	{Page: PageProjects, Label: "Edit project", Keywords: []string{"edit", "rename", "project", "change"}},
	{Page: PageProjects, Label: "Remove project", Keywords: []string{"remove", "delete", "project"}},
	{Page: PageProjects, Label: "Reorder projects", Keywords: []string{"reorder", "order", "move", "project", "list"}},
	{Page: PageProjects, Label: "Project name", Keywords: []string{"name", "rename", "project", "label"}},
	{Page: PageProjects, Label: "Worktree setup override", Keywords: []string{"worktree", "setup", "override", "project"}},

	// Workspaces
	{Page: PageWorkspaces, Label: "Default agent", Keywords: []string{"agent", "claude", "codex", "default", "workspace"}},
	{Page: PageWorkspaces, Label: "Start with a shell", Keywords: []string{"shell", "auto", "create", "workspace"}},
	{Page: PageWorkspaces, Label: "Repository prefix", Keywords: []string{"worktree", "prefix", "naming", "repository"}},
	{Page: PageWorkspaces, Label: "Overview location", Keywords: []string{"overview", "worktree", "scope", "activity"}},
	{Page: PageWorkspaces, Label: "Sidebar display", Keywords: []string{"sidebar", "display", "agent", "task", "stats", "prefix", "workspace"}},
	{Page: PageWorkspaces, Label: "Worktree setup", Keywords: []string{"worktree", "setup", "hook", "env", "copy"}},

	// Remote Hosts
	{Page: PageRemotes, Label: "Remote hosts", Keywords: []string{"remote", "host", "ssh", "machine", "another computer", "sidecar_remote_hosts"}},
	{Page: PageRemotes, Label: "Add host", Keywords: []string{"remote", "host", "add", "register", "ssh", "target", "machine"}},
	{Page: PageRemotes, Label: "SSH target", Keywords: []string{"ssh", "target", "hostname", "ssh_config", "alias", "proxyjump"}},
	{Page: PageRemotes, Label: "Sidecar path on the host", Keywords: []string{"binary", "path", "remote", "login shell", "homebrew"}},
	{Page: PageRemotes, Label: "Remote config path", Keywords: []string{"config", "path", "remote", "host"}},
	{Page: PageRemotes, Label: "Remote environment", Keywords: []string{"env", "environment", "tmux_tmpdir", "xdg_state_home", "isolated", "proof"}},
	{Page: PageRemotes, Label: "Switch a host off", Keywords: []string{"disable", "disabled", "off", "pause", "remote", "host"}},
	{Page: PageRemotes, Label: "Remove host", Keywords: []string{"remove", "delete", "unregister", "remote", "host"}},
	{Page: PageRemotes, Label: "Host health", Keywords: []string{"unreachable", "no-sidecar", "no tmux", "protocol", "stale", "online", "health", "connection"}},

	// Agents
	{Page: PageAgents, Label: "Available agents", Keywords: []string{"agent", "claude", "codex", "opencode", "grok", "enable"}},
	{Page: PageAgents, Label: "Agent launch command", Keywords: []string{"agent", "command", "launch", "start"}},
	{Page: PageAgents, Label: "Agent instructions", Keywords: []string{"agents.md", "instructions", "guidance", "repair"}},
	{Page: PageAgents, Label: "Default launch command", Keywords: []string{"agentstart", "command", "default", "reset", "agent"}},
	{Page: PageAgents, Label: "Agent integrations", Keywords: []string{"integration", "hook", "plugin", "lifecycle", "opencode", "install", "uninstall", "repair"}},
	{Page: PageAgents, Label: "Install an agent integration", Keywords: []string{"install", "integration", "plugin", "hook", "opencode"}},

	// Notifications
	{Page: PageNotifications, Label: "System notifications", Keywords: []string{"native", "desktop", "system notification", "terminal-notifier", "osascript", "notify-send"}},
	{Page: PageNotifications, Label: "Sounds", Keywords: []string{"sound", "audio", "afplay", "paplay", "pw-play", "aplay", "ffplay", "mpv", "waiting", "finished", "failure"}},
	{Page: PageNotifications, Label: "Quiet hours", Keywords: []string{"quiet hours", "mute", "schedule"}},
	{Page: PageNotifications, Label: "Agent activity rules", Keywords: []string{"waiting", "finished", "session ended", "source", "rules"}},
	{Page: PageNotifications, Label: "Other source rules", Keywords: []string{"agent posts", "td", "tasks", "system", "toast", "expiry"}},
	{Page: PageNotifications, Label: "Custom sound choices", Keywords: []string{"attention path", "done path", "failure path", "wav", "mp3", "custom sound"}},
	{Page: PageNotifications, Label: "SSH delivery", Keywords: []string{"ssh", "remote host", "remote", "managed hosts", "forwarded", "viewer"}},
	{Page: PageNotifications, Label: "Terminal notifications over SSH", Keywords: []string{"terminal notification", "ssh", "ghostty", "iterm2", "wezterm", "kitty", "osc", "passthrough"}},
	{Page: PageNotifications, Label: "Delivery status and test", Keywords: []string{"provider", "status", "test", "native", "sound"}},

	// Terminal
	{Page: PageTerminal, Label: "Exit interactive mode", Keywords: []string{"terminal", "interactive", "exit", "shortcut", "key"}},
	{Page: PageTerminal, Label: "Attach to tmux", Keywords: []string{"tmux", "attach", "terminal", "client"}},
	{Page: PageTerminal, Label: "Copy selection", Keywords: []string{"copy", "selection", "clipboard", "terminal"}},
	{Page: PageTerminal, Label: "Paste", Keywords: []string{"paste", "clipboard", "terminal"}},
	{Page: PageTerminal, Label: "Copy on select", Keywords: []string{"copy", "select", "clipboard", "terminal"}},
	{Page: PageTerminal, Label: "Preview capture limit", Keywords: []string{"capture", "limit", "preview", "terminal", "memory"}},

	// Panels & Integrations
	{Page: PagePanels, Label: "Git panel", Keywords: []string{"git", "panel", "status", "diff"}},
	{Page: PagePanels, Label: "Files panel", Keywords: []string{"files", "browser", "panel"}},
	{Page: PagePanels, Label: "td panel", Keywords: []string{"td", "issues", "tasks", "panel"}},
	{Page: PagePanels, Label: "Notes panel", Keywords: []string{"notes", "panel", "feature", "flag", "notes_plugin"}},
	{Page: PagePanels, Label: "Conversations panel", Keywords: []string{"conversations", "history", "sessions", "panel", "feature", "flag", "conversations_plugin"}},
	{Page: PagePanels, Label: "Tasks panel", Keywords: []string{"tasks", "panel", "beta", "feature", "flag", "tasks_plugin"}},
	{Page: PagePanels, Label: "td database location", Keywords: []string{"td", "database", "dbpath", "issues", "path"}},
	{Page: PagePanels, Label: "Conversations source directory", Keywords: []string{"conversations", "claude", "directory", "history", "path"}},
	{Page: PagePanels, Label: "Panel refresh interval", Keywords: []string{"refresh", "interval", "poll", "git", "td"}},
	{Page: PagePanels, Label: "Install Tasks", Keywords: []string{"tasks", "install", "homebrew", "brew", "beta", "enable"}},

	// Diagnostics
	{Page: PageDiagnostics, Label: "Terminal color check", Keywords: []string{"truecolor", "color", "check", "diagnostics"}},
	{Page: PageDiagnostics, Label: "tmux environment check", Keywords: []string{"tmux", "check", "environment", "diagnostics"}},
	{Page: PageDiagnostics, Label: "Configuration check", Keywords: []string{"config", "configuration", "valid", "check"}},
	{Page: PageDiagnostics, Label: "Projects check", Keywords: []string{"projects", "check", "configured"}},
	{Page: PageDiagnostics, Label: "Agent instructions check", Keywords: []string{"agents.md", "instructions", "check"}},
	{Page: PageDiagnostics, Label: "Recheck environment", Keywords: []string{"recheck", "refresh", "diagnostics", "again"}},
	{Page: PageDiagnostics, Label: "Configuration recovery", Keywords: []string{"config", "invalid", "parse", "error", "repair", "recover"}},

	// Advanced
	{Page: PageAdvanced, Label: "Terminal preview capture", Keywords: []string{"capture", "limit", "performance", "terminal"}},

	// About
	{Page: PageAbout, Label: "Version", Keywords: []string{"version", "about", "build"}},
	{Page: PageAbout, Label: "Update status", Keywords: []string{"update", "upgrade", "release", "version"}},
	{Page: PageAbout, Label: "Documentation", Keywords: []string{"docs", "documentation", "help", "support"}},
	{Page: PageAbout, Label: "Installation method", Keywords: []string{"homebrew", "go install", "binary", "provenance", "installed"}},
	{Page: PageAbout, Label: "Open updater", Keywords: []string{"update", "updater", "upgrade", "install", "release"}},
	{Page: PageAbout, Label: "Command palette", Keywords: []string{"palette", "commands", "shortcuts", "help"}},
}

// Index returns the full settings index: the static entries above plus one per
// registered feature flag.
func Index() []IndexEntry { return append(append([]IndexEntry{}, settingsIndex...), flagIndex()...) }

// flagIndex is Feature Flags' share of the index, derived from the registry
// rather than written out. Search is the main way a flag gets found by name, so
// a hand-written list here would put every new flag one forgotten line away
// from being unsearchable — the same failure that kept five flags off the
// surface entirely. The flag's own name is a keyword because that is the string
// a user reads in config.json and comes here to look up.
func flagIndex() []IndexEntry {
	items := previews()
	entries := make([]IndexEntry, 0, len(items))
	for _, item := range items {
		if item.owner != "" {
			// The owning page already has a hand-written entry under the same
			// label, and search renders one row per entry: emitting another
			// would show the user "Notes panel" twice under Panels &
			// Integrations and inflate the "N matching settings" count.
			continue
		}
		entries = append(entries, IndexEntry{
			Page:     PageFlags,
			Label:    item.label,
			Keywords: []string{"feature", "flag", "preview", item.flag},
		})
	}
	return entries
}

// Search returns the index entries matching a query, in index order. An empty
// or whitespace-only query matches nothing: search is a filter the user opts
// into, not a default view.
func Search(query string) []IndexEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var matches []IndexEntry
	for _, entry := range Index() {
		if entry.matches(q) {
			matches = append(matches, entry)
		}
	}
	return matches
}

// SearchPages returns the pages that have at least one matching setting, in
// sidebar order.
func SearchPages(query string) []PageID {
	matches := Search(query)
	if len(matches) == 0 {
		return nil
	}
	hit := make(map[PageID]bool, len(matches))
	for _, entry := range matches {
		hit[entry.Page] = true
	}
	var pages []PageID
	for _, page := range AllPages() {
		if hit[page.ID] {
			pages = append(pages, page.ID)
		}
	}
	return pages
}

func (e IndexEntry) matches(lowerQuery string) bool {
	if strings.Contains(strings.ToLower(e.Label), lowerQuery) {
		return true
	}
	if strings.Contains(strings.ToLower(PageTitle(e.Page)), lowerQuery) {
		return true
	}
	for _, keyword := range e.Keywords {
		if strings.Contains(strings.ToLower(keyword), lowerQuery) {
			return true
		}
	}
	return false
}
