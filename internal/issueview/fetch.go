package issueview

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Data holds lightweight issue data fetched via the td CLI.
type Data struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	Type        string   `json:"type"`
	Priority    string   `json:"priority"`
	Points      int      `json:"points"`
	Description string   `json:"description"`
	ParentID    string   `json:"parent_id"`
	Labels      []string `json:"labels"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// FetchedMsg carries one td fetch result. Hosts that track their own request
// identity wrap it; the app's modal consumes it directly.
type FetchedMsg struct {
	IssueID string
	Data    *Data
	Error   error
}

// Fetch runs `td show <id> -f json`. workDir sets the command's working
// directory so td uses the correct project database.
func Fetch(workDir, issueID string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("td", "show", issueID, "-f", "json")
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			// stdout may contain "ERROR: <message>" from td CLI
			if msg := extractTdError(string(out)); msg != "" {
				return FetchedMsg{IssueID: issueID, Error: fmt.Errorf("%s", msg)}
			}
			// stderr may contain usage help + "Error: <message>" on last line
			if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
				if msg := extractTdError(string(exitErr.Stderr)); msg != "" {
					return FetchedMsg{IssueID: issueID, Error: fmt.Errorf("%s", msg)}
				}
			}
			return FetchedMsg{IssueID: issueID, Error: fmt.Errorf("issue %q not found", issueID)}
		}
		var data Data
		if err := json.Unmarshal(out, &data); err != nil {
			return FetchedMsg{IssueID: issueID, Error: err}
		}
		return FetchedMsg{IssueID: issueID, Data: &data}
	}
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
