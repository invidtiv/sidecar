package antigravity

import "time"

// LogLine represents a single step in transcript.jsonl.
type LogLine struct {
	StepIndex int        `json:"step_index"`
	Source    string     `json:"source"`
	Type      string     `json:"type"`
	Status    string     `json:"status"`
	CreatedAt string     `json:"created_at"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a tool call in a log line.
type ToolCall struct {
	ToolName string                 `json:"tool_name,omitempty"`
	Name     string                 `json:"name,omitempty"`
	Args     map[string]interface{} `json:"args,omitempty"`
}

// SessionMetadata stores parsed session header info.
type SessionMetadata struct {
	SessionID        string
	StartTime        time.Time
	LastUpdated      time.Time
	FirstUserMessage string
	MessageCount     int
	TotalTokens      int
	FileSize         int64
}
