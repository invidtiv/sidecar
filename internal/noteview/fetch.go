package noteview

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Data is one td note as `td note show --json` returns it.
type Data struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Pinned    bool       `json:"pinned"`
	Archived  bool       `json:"archived"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// FetchedMsg carries one td note fetch result.
type FetchedMsg struct {
	NoteID string
	Data   *Data
	Error  error
}

// Fetch runs `td note show` against workDir's project.
func Fetch(workDir, noteID string) tea.Cmd {
	return func() tea.Msg {
		data, err := loadNote(workDir, noteID)
		return FetchedMsg{NoteID: noteID, Data: data, Error: err}
	}
}

func loadNote(workDir, noteID string) (*Data, error) {
	cmd := exec.Command("td", "-w", workDir, "--json", "note", "show", noteID)
	configureReadOnlyTd(cmd)
	out, err := cmd.Output()
	if err != nil {
		if msg := extractTdError(string(out)); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			if msg := extractTdError(string(exitErr.Stderr)); msg != "" {
				return nil, fmt.Errorf("%s", msg)
			}
		}
		return nil, fmt.Errorf("note %q not found", noteID)
	}
	var data Data
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, err
	}
	if data.ID == "" {
		return nil, fmt.Errorf("note %q not found", noteID)
	}
	return &data, nil
}

func configureReadOnlyTd(cmd *exec.Cmd) {
	cmd.Env = append(cmd.Environ(),
		"TD_SYNC_AUTO_START=0",
		"TD_ANALYTICS=false",
	)
}

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
