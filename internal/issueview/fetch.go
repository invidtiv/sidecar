package issueview

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/tdroot"
)

// Data holds one td issue plus the related rows the card needs to render
// and navigate: children (subtasks), parent, siblings, and recent logs.
type Data struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	Type        string   `json:"type"`
	Priority    string   `json:"priority"`
	Points      int      `json:"points"`
	Description string   `json:"description"`
	Acceptance  string   `json:"acceptance"`
	ParentID    string   `json:"parent_id"`
	Labels      []string `json:"labels"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Logs        []Log    `json:"logs"`

	// Filled by a follow-up tree fetch; not present on `td show` JSON.
	Parent   *Ref  `json:"-"`
	Children []Ref `json:"-"`
	Siblings []Ref `json:"-"`
}

// Ref is a lightweight issue pointer used for parent, children, and siblings.
type Ref struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Priority string `json:"priority"`
}

// Log is one progress line from `td show`.
type Log struct {
	Timestamp string `json:"timestamp"`
	Session   string `json:"session"`
	Message   string `json:"message"`
	Type      string `json:"type"`
}

type treeNode struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Status   string     `json:"status"`
	Type     string     `json:"type"`
	Priority string     `json:"priority"`
	Children []treeNode `json:"children"`
}

// FetchedMsg carries one td fetch result. Hosts that track their own request
// identity wrap it; the app's modal consumes it directly.
//
// FoundIn is nil for a local result. A non-nil Owner means the issue lives in
// another configured project's store; the card adopts that store as its own
// (refreshes address it directly) and shows the project name as a badge.
type FetchedMsg struct {
	IssueID string
	Data    *Data
	Error   error
	FoundIn *Owner
}

// Fetch runs `td show` and, when that succeeds, `td tree` so the card can
// show subtasks and walk an epic's children. workDir sets the command's
// working directory so td uses the correct project database.
func Fetch(workDir, issueID string) tea.Cmd {
	return FetchWithFallbacks(workDir, issueID, nil)
}

// FetchWithFallbacks is Fetch with cross-project candidates. The refs are raw
// config projects; resolving them into verified candidates happens here,
// inside the command, because it stats files and may shell out to git.
func FetchWithFallbacks(workDir, issueID string, fallbacks []ProjectRef) tea.Cmd {
	return func() tea.Msg {
		data, owner, err := loadIssue(workDir, issueID, fallbacks)
		return FetchedMsg{IssueID: issueID, Data: data, Error: err, FoundIn: owner}
	}
}

// loadIssue fetches an issue locally first. Only on a genuine "not found" —
// never a corrupt store or another real td error — does it search the
// fallbacks, so a broken local database cannot silently masquerade as a
// foreign issue.
func loadIssue(workDir, issueID string, fallbacks []ProjectRef) (*Data, *Owner, error) {
	data, err := showIssue(workDir, issueID)
	if err != nil {
		if len(fallbacks) > 0 && isNotFound(err) {
			if h := findAcrossProjects(context.Background(), issueID,
				BuildCandidates(tdroot.ResolveTDRoot(workDir), fallbacks)); h != nil {
				attachTree(h.Cand.Root, h.Data)
				owner := Owner{Name: h.Cand.Name, Root: h.Cand.Root}
				return h.Data, &owner, nil
			}
			// Total miss: say what was actually searched rather than letting
			// the card claim the issue does not exist at all.
			return nil, nil, fmt.Errorf("issue %q not found in %d project(s)",
				issueID, 1+len(fallbacks))
		}
		return nil, nil, err
	}
	attachTree(workDir, data)
	return data, nil, nil
}

// isNotFound reports whether err is td's genuine missing-issue answer. Real
// failures (corrupt store, locked database) surface other messages and must
// short-circuit rather than fall through to the cross-project search.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

func showIssue(workDir, issueID string) (*Data, error) {
	return showIssueContext(context.Background(), workDir, issueID)
}

// showIssueContext is showIssue bound to ctx: a cancelled or timed-out
// context kills the td subprocess, which is how the cross-project fan-out
// retracts its losers when a winner is found.
func showIssueContext(ctx context.Context, workDir, issueID string) (*Data, error) {
	cmd := exec.CommandContext(ctx, "td", "show", issueID, "-f", "json")
	cmd.Dir = workDir
	configureReadOnlyTd(cmd)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if msg := extractTdError(string(out)); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			if msg := extractTdError(string(exitErr.Stderr)); msg != "" {
				return nil, fmt.Errorf("%s", msg)
			}
		}
		return nil, fmt.Errorf("issue %q not found", issueID)
	}
	var data Data
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func showTree(workDir, issueID string) (*treeNode, error) {
	cmd := exec.Command("td", "tree", issueID, "--json", "--depth", "1")
	cmd.Dir = workDir
	configureReadOnlyTd(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var node treeNode
	if err := json.Unmarshal(out, &node); err != nil {
		return nil, err
	}
	return &node, nil
}

// configureReadOnlyTd keeps preview reads local and cheap. td's default
// process hooks may start background sync and record analytics on every show
// and tree invocation; one issue card can run three of those processes. Cmd's
// existing environment is retained so PATH and project-specific configuration
// still resolve normally, while the preview-owned overrides win if callers had
// either variable set already.
func configureReadOnlyTd(cmd *exec.Cmd) {
	cmd.Env = append(cmd.Environ(),
		"TD_SYNC_AUTO_START=0",
		"TD_ANALYTICS=false",
	)
}

// attachTree fills children from the issue's own tree and, when the issue
// has a parent, the parent's tree for sibling navigation. A tree failure
// leaves the issue visible; the card just omits those sections.
func attachTree(workDir string, data *Data) {
	if data == nil || data.ID == "" {
		return
	}
	if self, err := showTree(workDir, data.ID); err == nil && self != nil {
		data.Children = refsFromNodes(self.Children)
	}
	if data.ParentID == "" {
		return
	}
	parent, err := showTree(workDir, data.ParentID)
	if err != nil || parent == nil {
		return
	}
	ref := refFromNode(*parent)
	data.Parent = &ref
	data.Siblings = refsFromNodes(parent.Children)
}

func refFromNode(n treeNode) Ref {
	return Ref{ID: n.ID, Title: n.Title, Status: n.Status, Type: n.Type, Priority: n.Priority}
}

func refsFromNodes(nodes []treeNode) []Ref {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]Ref, len(nodes))
	for i, n := range nodes {
		out[i] = refFromNode(n)
	}
	return out
}

// extractTdError finds the last "ERROR: ..." or "Error: ..." line in td output.
func extractTdError(output string) string {
	for _, line := range reverseLines(strings.TrimSpace(output)) {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "error:") {
			return strings.TrimSpace(line[len("error:"):])
		}
	}
	return ""
}

func reverseLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}
