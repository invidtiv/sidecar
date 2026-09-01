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
	"regexp"
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
	// Aliases are other identifiers that name this same family — chiefly the
	// conversation adapter ids, which do not all match the catalog id
	// ("claude-code", "cursor-cli", "pi-agent"). They exist so the adapter
	// vocabulary resolves here instead of in a switch statement per consumer.
	Aliases []string
	// ResumeArgs are the argv entries between Command and the session value.
	// Empty means the family has no native resume.
	//
	// Every provider Sidecar knows takes the session value last, so this plus a
	// trailing value expresses all three real shapes: a subcommand
	// ("codex resume"), a chain of them ("amp threads continue"), and a flag
	// pair ("opencode --continue -s").
	ResumeArgs []string
	// ResumeKinds are the session reference kinds this family can resume from,
	// as the bare kind names "id" and "path". They are plain strings so this
	// package stays a leaf.
	ResumeKinds []string
	// AdapterID is the id this family's conversation-history adapter registers
	// under, when it differs from ID. Empty means the two are the same.
	//
	// It is stated rather than inferred from Aliases because "which adapter
	// reads this provider's transcripts" is a specific fact, and deriving it
	// from "what else is this provider called" would be right by coincidence.
	AdapterID string
}

// ConversationAdapterID is the id of the conversation-history adapter that can
// read this family's transcripts.
func (f Family) ConversationAdapterID() string {
	if f.AdapterID != "" {
		return f.AdapterID
	}
	return f.ID
}

// families is the ordered list a creation picker offers. Order is the picker's
// order; adding a family here adds it everywhere.
var families = []Family{
	{ID: "claude", Name: "Claude Code", Short: "Claude", Command: "claude", SkipPermissionsArg: "--dangerously-skip-permissions",
		Aliases: []string{"claude-code"}, AdapterID: "claude-code", ResumeArgs: []string{"--resume"}, ResumeKinds: []string{"id"}},
	{ID: "codex", Name: "Codex CLI", Short: "Codex", Command: "codex", SkipPermissionsArg: "--dangerously-bypass-approvals-and-sandbox",
		ResumeArgs: []string{"resume"}, ResumeKinds: []string{"id"}},
	{ID: "copilot", Name: "GitHub Copilot CLI", Short: "Copilot", Command: "copilot"},
	{ID: "antigravity", Name: "Antigravity", Short: "Antigravity", Command: "agy", SkipPermissionsArg: "--dangerously-skip-permissions",
		Aliases: []string{"agy"}, ResumeArgs: []string{"--conversation"}, ResumeKinds: []string{"id"}},
	{ID: "cursor", Name: "Cursor Agent", Short: "Cursor", Command: "cursor-agent", SkipPermissionsArg: "-f",
		Aliases: []string{"cursor-cli"}, AdapterID: "cursor-cli", ResumeArgs: []string{"--resume"}, ResumeKinds: []string{"id"}},
	{ID: "opencode", Name: "OpenCode", Short: "OpenCode", Command: "opencode", SkipPermissionsArg: "--auto",
		ResumeArgs: []string{"--continue", "-s"}, ResumeKinds: []string{"id"}},
	{ID: "pi", Name: "Pi Agent", Short: "Pi", Command: "pi",
		Aliases: []string{"pi-agent"}, AdapterID: "pi-agent", ResumeArgs: []string{"--session"}, ResumeKinds: []string{"id"}},
	{ID: "amp", Name: "Amp", Short: "Amp", Command: "amp", SkipPermissionsArg: "--dangerously-allow-all",
		ResumeArgs: []string{"threads", "continue"}, ResumeKinds: []string{"id"}},
	{ID: "grok", Name: "Grok", Short: "Grok", Command: "grok", SkipPermissionsArg: "--always-approve",
		ResumeArgs: []string{"--resume"}, ResumeKinds: []string{"id"}},
	{ID: "muse", Name: "Muse Spark", Short: "Muse", Command: "muse", SkipPermissionsArg: "--yolo",
		ResumeArgs: []string{"resume"}, ResumeKinds: []string{"id"}},
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

// ShellCommand renders an argument vector as a single shell command line.
//
// This is the one place structured argv becomes a string, and it exists so
// there is exactly one such place. Every entry is single-quoted with embedded
// quotes escaped, so a session identifier, path, or provider argument cannot
// end an argument and start a command however it is spelled. Callers that have
// argv should keep argv; this is for the two boundaries that genuinely need a
// command line — typing into an interactive shell, and showing a human a line
// they can paste.
func ShellCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

// displayCommandUnsafe matches any character a shell acts on. The complement —
// letters, digits, and @%+=:,./_- — is the same set shlex.quote treats as safe:
// it contains no whitespace, no quote, no backslash, no expansion character
// ($ ` ~ !), no glob character (* ? [ ]), no grouping ( ) { }, no redirection
// (< >), and no separator (| & ; #). An entry containing anything else is
// quoted, and so is an empty entry.
var displayCommandUnsafe = regexp.MustCompile(`[^A-Za-z0-9@%+=:,./_-]`)

// DisplayCommand renders an argument vector as a shell command line, quoting
// only the entries a shell would otherwise read as more than one plain word.
//
// It is a conservative quoter, not a cosmetic one: every entry either survives
// bare because no character in it means anything to the shell, or goes through
// ShellCommand. That makes it safe on the execution path while keeping the line
// a human reads — and the line typed at a shell prompt — the unadorned
// `claude --resume <id>` Sidecar has always shown.
//
// Use ShellCommand when nothing will ever read the result but a shell. Use this
// when a human sees it too.
func DisplayCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		if arg == "" || displayCommandUnsafe.MatchString(arg) {
			parts[i] = ShellCommand([]string{arg})
			continue
		}
		parts[i] = arg
	}
	return strings.Join(parts, " ")
}

// CanResume reports whether this family has a native resume command.
func (f Family) CanResume() bool {
	return strings.TrimSpace(f.Command) != "" && len(f.ResumeArgs) > 0 && len(f.ResumeKinds) > 0
}

// ResumesKind reports whether this family can resume from a reference of kind.
func (f Family) ResumesKind(kind string) bool {
	for _, known := range f.ResumeKinds {
		if known == kind {
			return true
		}
	}
	return false
}

// ResumeArgv builds the provider resume as structured arguments.
//
// It deliberately does not take a skip-permissions flag. Resuming reproduces
// exactly the command shape Sidecar has shipped and verified, and the safe
// position for a global flag differs per provider once a subcommand is involved
// ("codex resume", "amp threads continue"): guessing one would emit a command
// line no test has ever run. A caller that wants an auto-approving resume
// launches the provider and resumes from inside it.
func (f Family) ResumeArgv(kind, value string, extra []string) ([]string, error) {
	if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.Command) == "" {
		return nil, fmt.Errorf("provider has no launch capability")
	}
	if !f.CanResume() {
		return nil, fmt.Errorf("provider %q has no native resume command", f.ID)
	}
	if !f.ResumesKind(kind) {
		return nil, fmt.Errorf("provider %q cannot resume from a %q reference; it resumes from: %s",
			f.ID, kind, strings.Join(f.ResumeKinds, ", "))
	}
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("session reference value is empty")
	}
	// A value starting with a dash is refused here as well as by the session
	// validator, because this is the boundary that builds the command.
	//
	// A "--" terminator is deliberately NOT used instead. It only works where
	// the value is positional; for the families whose resume passes it to a
	// flag ("claude --resume <id>", "opencode --continue -s <id>") inserting
	// "--" changes what the flag receives, and for the positional families it
	// would be syntax no test here has ever run. Refusing the value is
	// provider-agnostic and provably closes the same hole: quoting cannot help,
	// since the value is already a correct separate argv entry and the provider
	// would still read it as an option.
	if strings.HasPrefix(value, "-") {
		return nil, fmt.Errorf("session reference %q starts with a dash, which %s would read as a flag", value, f.ID)
	}
	argv := []string{f.Command}
	argv = append(argv, f.ResumeArgs...)
	argv = append(argv, value)
	argv = append(argv, extra...)
	for _, arg := range argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return nil, fmt.Errorf("provider argument contains NUL")
		}
	}
	return argv, nil
}

// BuildResume resolves a catalog id — canonical, alias, or legacy — and builds
// its structured resume argv.
func BuildResume(id, kind, value string, extra []string) ([]string, error) {
	family, ok := Lookup(id)
	if !ok {
		return nil, fmt.Errorf("unknown agent kind %q", id)
	}
	return family.ResumeArgv(kind, value, extra)
}

// Lookup resolves any identifier that names a family: its canonical id, one of
// its aliases (the conversation adapter ids), or a legacy launch id.
//
// Find stays exact on purpose — it answers "is this the stored setting?" for
// pickers and configuration. Lookup answers "which family is this?" for code
// translating another vocabulary into ours.
func Lookup(id string) (Family, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Family{}, false
	}
	if family, ok := FindLaunch(id); ok {
		return family, true
	}
	for _, family := range families {
		for _, alias := range family.Aliases {
			if alias == id {
				return family, true
			}
		}
	}
	return Family{}, false
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
