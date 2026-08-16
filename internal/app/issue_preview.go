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

// issuePreviewModelID is reserved for the app's preview modal so a workspace
// issue leaf can never have its load stolen by SetResult identity.
const issuePreviewModelID = -1

func (m *Model) previewIssueData() *IssuePreviewData {
	if m.issuePreviewView != nil && m.issuePreviewView.Data() != nil {
		return m.issuePreviewView.Data()
	}
	return m.issuePreviewData
}

func (m *Model) claimIssuePreviewLoad(msg issueview.LoadedMsg) bool {
	if !m.showIssuePreview || m.issuePreviewView == nil {
		return false
	}
	if !m.issuePreviewView.SetResult(msg) {
		// A refresh that found nothing new returns false here, and must still be
		// claimed: it was this modal's own result, and letting it fall through
		// would offer a card the plugins have no reason to see. Claiming it
		// without touching the modal is exactly the no-repaint behaviour.
		return msg.Refresh && msg.ModelID == issuePreviewModelID
	}
	if msg.Refresh {
		// The refresh changed the card in place. Only the cached modal has to be
		// rebuilt; the surrounding loading and error state is already correct.
		m.issuePreviewData = m.issuePreviewView.Data()
		m.invalidateIssuePreviewModal()
		return true
	}
	m.applyIssuePreviewData(m.issuePreviewView.Data(), msg.Error)
	return true
}

func (m *Model) applyIssuePreviewData(data *IssuePreviewData, err error) {
	m.issuePreviewLoading = false
	m.issuePreviewError = err
	m.issuePreviewData = data
	if m.issuePreviewView != nil && data != nil && err == nil && m.issuePreviewView.Data() == nil {
		m.issuePreviewView.SetData(data)
	}
	m.issuePreviewModal = nil
	m.issuePreviewModalWidth = 0
	m.issuePreviewModalHeight = 0
}
