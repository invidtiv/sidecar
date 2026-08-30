// Package agentcatalog is the single description of the agent families Sidecar
// can start: their order in a creation picker, what to call them, and the
// command each one launches by default.
//
// It is a leaf package on purpose. The workspace plugin builds its creation
// pickers from it, and Configuration lists the same families without importing
// the plugin (the plugin imports the app, and the app imports Configuration).
// One table, two readers, no drift.
package agentcatalog

import (
	"fmt"
	"strings"
)

// Family is one agent family Sidecar offers when work is created.
type Family struct {
	// ID is the stored identity — the value written to plugins.workspace.agents,
	// agentStart, and defaultAgentType.
	ID string
	// Name is the full display name.
	Name string
	// Short is the compact label a settings row or a selector uses.
	Short string
	// Command is the executable Sidecar launches when no override is configured.
	Command string
	// SkipPermissionsArg is appended as one argv entry when the caller
	// explicitly requests the provider's unsafe/auto-approve mode.
	SkipPermissionsArg string
}

// families is the ordered list a creation picker offers. Order is the picker's
// order; adding a family here adds it everywhere.
var families = []Family{
	{ID: "claude", Name: "Claude Code", Short: "Claude", Command: "claude", SkipPermissionsArg: "--dangerously-skip-permissions"},
	{ID: "codex", Name: "Codex CLI", Short: "Codex", Command: "codex", SkipPermissionsArg: "--dangerously-bypass-approvals-and-sandbox"},
	{ID: "copilot", Name: "GitHub Copilot CLI", Short: "Copilot", Command: "copilot"},
	{ID: "antigravity", Name: "Antigravity", Short: "Antigravity", Command: "agy", SkipPermissionsArg: "--dangerously-skip-permissions"},
	{ID: "cursor", Name: "Cursor Agent", Short: "Cursor", Command: "cursor-agent", SkipPermissionsArg: "-f"},
	{ID: "opencode", Name: "OpenCode", Short: "OpenCode", Command: "opencode", SkipPermissionsArg: "--auto"},
	{ID: "pi", Name: "Pi Agent", Short: "Pi", Command: "pi"},
	{ID: "amp", Name: "Amp", Short: "Amp", Command: "amp", SkipPermissionsArg: "--dangerously-allow-all"},
	{ID: "grok", Name: "Grok", Short: "Grok", Command: "grok", SkipPermissionsArg: "--always-approve"},
}

// legacyLaunchFamilies remain launchable for persisted/configured creation
// settings but are deliberately absent from Families and every new picker.
// Aider is the sole compatibility case; unknown ids never fall back to it or
// Claude.
var legacyLaunchFamilies = map[string]Family{
	"aider": {ID: "aider", Name: "Aider", Short: "Aider", Command: "aider", SkipPermissionsArg: "--yes"},
}

// LaunchArgv builds the provider launch as structured arguments. Shell quoting
// belongs to the terminal adapter, at the one boundary where argv becomes a
// command line; callers must not concatenate these values themselves.
func (f Family) LaunchArgv(extra []string, skipPermissions bool) ([]string, error) {
	if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.Command) == "" {
		return nil, fmt.Errorf("provider has no launch capability")
	}
	argv := []string{f.Command}
	if skipPermissions && f.SkipPermissionsArg != "" {
		argv = append(argv, f.SkipPermissionsArg)
	}
	argv = append(argv, extra...)
	for _, arg := range argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return nil, fmt.Errorf("provider argument contains NUL")
		}
	}
	return argv, nil
}

// BuildLaunch resolves a catalog id and builds its structured launch argv.
func BuildLaunch(id string, extra []string, skipPermissions bool) ([]string, error) {
	family, ok := FindLaunch(strings.TrimSpace(id))
	if !ok {
		return nil, fmt.Errorf("unknown agent kind %q", id)
	}
	return family.LaunchArgv(extra, skipPermissions)
}

// FindLaunch resolves selectable and explicitly supported legacy launch
// families. Use Find for UI selection; use FindLaunch only at an execution
// boundary that must honor persisted older configuration.
func FindLaunch(id string) (Family, bool) {
	if family, ok := Find(id); ok {
		return family, true
	}
	family, ok := legacyLaunchFamilies[strings.TrimSpace(id)]
	return family, ok
}

// OpaqueLaunchArgv wraps a legacy .sidecar-agent-start/config command without
// parsing or reclassifying it as catalog argv. The returned wrapper is for this
// launch only; callers must not persist it as replayable structured metadata.
func OpaqueLaunchArgv(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" || strings.IndexByte(command, 0) >= 0 {
		return nil, fmt.Errorf("opaque launch command is empty or contains NUL")
	}
	return []string{"sh", "-lc", command}, nil
}

// Families returns every selectable family in picker order.
func Families() []Family {
	out := make([]Family, len(families))
	copy(out, families)
	return out
}

// Find returns the family with an ID, if it is one Sidecar knows.
func Find(id string) (Family, bool) {
	for _, family := range families {
		if family.ID == id {
			return family, true
		}
	}
	return Family{}, false
}

// Known reports whether an ID names a family Sidecar can start.
func Known(id string) bool {
	_, ok := Find(id)
	return ok
}

// Resolve is the allowlist rule creation uses, stated once.
//
// An empty allowlist means every family: a user who has never touched the
// setting is offered everything, and so is a user whose allowlist names nothing
// Sidecar recognizes. Otherwise the allowlist is honoured in its own order,
// with unknown and duplicate entries dropped.
func Resolve(allowlist []string) []Family {
	if len(allowlist) == 0 {
		return Families()
	}
	seen := make(map[string]bool, len(allowlist))
	var out []Family
	for _, raw := range allowlist {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		family, ok := Find(id)
		if !ok {
			continue
		}
		seen[id] = true
		out = append(out, family)
	}
	if len(out) == 0 {
		return Families()
	}
	return out
}

// ResolvePicker is the creation-picker list: Resolve's allowlist, then None
// (empty string) placed first for shells and last for worktrees.
//
// Empty and unrecognized allowlists follow Resolve: every catalog family.
func ResolvePicker(allowlist []string, shellMode bool) []string {
	families := Resolve(allowlist)
	out := make([]string, 0, len(families)+1)
	if shellMode {
		out = append(out, "")
	}
	for _, family := range families {
		out = append(out, family.ID)
	}
	if !shellMode {
		out = append(out, "")
	}
	return out
}

// Label is the picker display name for an agent id.
//
// "" is "None (attach only)". Catalog IDs use Family.Name. "shell" is
// "Project Shell". Unknown IDs pass through.
func Label(id string) string {
	switch id {
	case "":
		return "None (attach only)"
	case "shell":
		return "Project Shell"
	}
	if family, ok := Find(id); ok {
		return family.Name
	}
	return id
}
