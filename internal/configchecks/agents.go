package configchecks

import (
	"os"
	"path/filepath"
	"strings"
)

// AgentInstructionLine is the entire recommended addition. `sidecar agents` is
// the canonical, always-current reference, so the project file points at it
// rather than duplicating guidance that would go stale in every repository that
// copied it.
const AgentInstructionLine = "For Sidecar capabilities, run sidecar agents."

// agentInstructionMarkers are what detection looks for. They match any phrasing
// that already sends an agent to the command — including the `--agents` flag
// form — so a user who wrote their own sentence is not nagged to add ours.
var agentInstructionMarkers = []string{"sidecar agents", "sidecar --agents"}

func mentionsSidecarAgents(content string) bool {
	lower := strings.ToLower(content)
	for _, marker := range agentInstructionMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// AgentInstructionsFile returns the file the repair would touch: an existing
// AGENTS.md, else an existing CLAUDE.md, else the AGENTS.md that would be
// created. It returns "" when there is no project directory.
func AgentInstructionsFile(env Env, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	env = env.withDefaults()
	agents := filepath.Join(dir, "AGENTS.md")
	if fileExists(env, agents) {
		return agents
	}
	claude := filepath.Join(dir, "CLAUDE.md")
	if fileExists(env, claude) {
		return claude
	}
	return agents
}

// HasAgentInstructions reports whether a file already points agents at
// `sidecar agents`.
func HasAgentInstructions(env Env, path string) bool {
	env = env.withDefaults()
	content, err := env.ReadFile(path)
	if err != nil {
		return false
	}
	return mentionsSidecarAgents(string(content))
}

func fileExists(env Env, path string) bool {
	info, err := env.Stat(path)
	return err == nil && !info.IsDir()
}

// AgentInstructionsAddition is the exact text a write would add, shown to the
// user before anything is written. There is nothing else in it: what is
// reviewed is what lands.
func AgentInstructionsAddition() string { return AgentInstructionLine + "\n" }

// AddAgentInstructions inserts the Sidecar line into a project's agent file.
//
// It is never called by a check. A caller reaches it only after showing the
// user AgentInstructionsAddition and receiving confirmation, and it never
// overwrites: existing content is preserved and the line is inserted after any
// YAML frontmatter and the file's opening heading, which is where a reader
// looking for project instructions starts.
func AddAgentInstructions(path string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(AgentInstructionsAddition()), 0o644)
	}
	content := string(existing)
	if mentionsSidecarAgents(content) {
		return nil
	}
	insert := insertionPoint(content)
	var out strings.Builder
	out.WriteString(content[:insert])
	if insert > 0 && !strings.HasSuffix(content[:insert], "\n") {
		out.WriteString("\n")
	}
	out.WriteString(AgentInstructionsAddition())
	rest := content[insert:]
	if rest != "" && !strings.HasPrefix(rest, "\n") {
		out.WriteString("\n")
	}
	out.WriteString(rest)
	return os.WriteFile(path, []byte(out.String()), 0o644)
}

// insertionPoint finds the byte offset the line belongs at: after YAML
// frontmatter (which must stay the first thing in the file) and after an
// opening `#` heading (so the file still opens with its own title).
func insertionPoint(content string) int {
	pos := 0
	if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
		if end := strings.Index(content[3:], "\n---"); end != -1 {
			pos = 3 + end + len("\n---")
			if nl := strings.IndexByte(content[pos:], '\n'); nl != -1 {
				pos += nl + 1
			} else {
				pos = len(content)
			}
			pos = skipBlankLines(content, pos)
		}
	}
	if pos < len(content) && content[pos] == '#' {
		if nl := strings.IndexByte(content[pos:], '\n'); nl != -1 {
			pos += nl + 1
			pos = skipBlankLines(content, pos)
		} else {
			pos = len(content)
		}
	}
	return pos
}

func skipBlankLines(content string, pos int) int {
	for pos < len(content) && (content[pos] == '\n' || content[pos] == '\r') {
		pos++
	}
	return pos
}

func checkAgentInstructions(in Input) Result {
	env := in.env()
	result := Result{ID: CheckAgentInstructions, Title: "Agent instructions"}
	path := AgentInstructionsFile(env, in.ProjectDir)
	if path == "" {
		// Nothing to check yet. This is not a problem the user can act on: it
		// resolves itself when a project exists.
		result.OK = true
		result.Summary = "No project selected"
		result.Evidence = []string{"Sidecar has no active project directory."}
		return result
	}

	name := in.ProjectName
	if name == "" {
		name = filepath.Base(in.ProjectDir)
	}
	base := filepath.Base(path)
	// The route is always reachable: a healthy file is still something a user
	// may want to open, so the row navigates rather than going quiet.
	result.Repair = RepairAgentInstructions

	if HasAgentInstructions(env, path) {
		result.OK = true
		result.Summary = base + " connected"
		result.Evidence = []string{path + " already points agents at `sidecar agents`."}
		result.Badge = BadgeOpen
		return result
	}
	if !fileExists(env, path) {
		result.Summary = base + " does not exist for " + name
		result.Evidence = []string{"No AGENTS.md or CLAUDE.md in " + in.ProjectDir + "."}
	} else {
		result.Summary = base + " needs Sidecar guidance"
		result.Evidence = []string{path + " does not mention `sidecar agents`."}
	}
	result.Action = "Connect agent instructions"
	result.ActionDetail = "Add one line so agents can self-serve Sidecar guidance"
	result.Badge = BadgeOpen
	return result
}
