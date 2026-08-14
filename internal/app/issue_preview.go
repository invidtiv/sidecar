package app

import (
	"encoding/json"
	"os/exec"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/issueview"
)

// IssueSearchResult holds a single search result from td search.
type IssueSearchResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Priority string `json:"priority"`
}

// tdSearchResultWrapper wraps td search JSON output: {"Issue": {...}, "Score": N}.
type tdSearchResultWrapper struct {
	Issue struct {
		IssueSearchResult
		UpdatedAt string `json:"updated_at"`
	} `json:"Issue"`
	Score int `json:"Score"`
}

// IssueSearchResultMsg carries search results back to the app.
type IssueSearchResultMsg struct {
	Query   string
	Results []IssueSearchResult
	Error   error
}

// issueSearchCmd runs `td search <query> --json -n 50` asynchronously.
// When includeClosed is false, filters to non-closed statuses.
// workDir sets the command's working directory so td uses the correct project database.
func issueSearchCmd(workDir, query string, includeClosed bool) tea.Cmd {
	return func() tea.Msg {
		args := []string{"search", query, "--json", "-n", "50"}
		if !includeClosed {
			args = append(args, "-s", "open", "-s", "in_progress", "-s", "blocked", "-s", "in_review")
		}
		cmd := exec.Command("td", args...)
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			return IssueSearchResultMsg{Query: query, Error: err}
		}
		var wrappers []tdSearchResultWrapper
		if err := json.Unmarshal(out, &wrappers); err != nil {
			return IssueSearchResultMsg{Query: query, Error: err}
		}
		// Sort by updated_at descending (most recently updated first).
		sort.Slice(wrappers, func(i, j int) bool {
			ti, _ := time.Parse(time.RFC3339Nano, wrappers[i].Issue.UpdatedAt)
			tj, _ := time.Parse(time.RFC3339Nano, wrappers[j].Issue.UpdatedAt)
			return ti.After(tj)
		})
		results := make([]IssueSearchResult, len(wrappers))
		for i, w := range wrappers {
			results[i] = w.Issue.IssueSearchResult
		}
		return IssueSearchResultMsg{Query: query, Results: results}
	}
}

// IssuePreviewData is the issue component's data; the modal is one host of it.
type IssuePreviewData = issueview.Data

// IssuePreviewResultMsg carries fetched issue data back to the app.
type IssuePreviewResultMsg struct {
	Data  *IssuePreviewData
	Error error
}

// OpenFullIssueMsg is broadcast to plugins to open the full rich issue view.
// Currently handled by the TD monitor plugin via monitor.OpenIssueByIDMsg.
type OpenFullIssueMsg struct {
	IssueID string
}

// fetchIssuePreviewCmd fetches an issue through the shared component and
// reports it in the app's own message type.
func fetchIssuePreviewCmd(workDir, issueID string) tea.Cmd {
	fetch := issueview.Fetch(workDir, issueID)
	return func() tea.Msg {
		msg, _ := fetch().(issueview.FetchedMsg)
		return IssuePreviewResultMsg{Data: msg.Data, Error: msg.Error}
	}
}
