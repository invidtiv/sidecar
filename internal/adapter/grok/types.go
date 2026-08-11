package grok

import (
	"encoding/json"
	"time"
)

// Summary is the on-disk summary.json index for a Grok session.
type Summary struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	SessionSummary  string    `json:"session_summary"`
	GeneratedTitle  string    `json:"generated_title"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	LastActiveAt    time.Time `json:"last_active_at"`
	NumMessages     int       `json:"num_messages"`
	NumChatMessages int       `json:"num_chat_messages"`
	CurrentModelID  string    `json:"current_model_id"`
	GitRootDir      string    `json:"git_root_dir"`
	AgentName       string    `json:"agent_name"`
	SessionKind     string    `json:"session_kind"`
	ParentSessionID string    `json:"parent_session_id"`
}

// ChatLine is one JSONL record from chat_history.jsonl.
// Content is heterogeneous (string or array of blocks) so it stays raw JSON.
type ChatLine struct {
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	Content       json.RawMessage `json:"content"`
	ToolCallID    string          `json:"tool_call_id"`
	ToolCalls     []ToolCall      `json:"tool_calls"`
	ModelID       string          `json:"model_id"`
	Summary       []SummaryText   `json:"summary"` // reasoning summaries
	Status        string          `json:"status"`
	SyntheticReason string        `json:"synthetic_reason"`
}

// ToolCall is an assistant tool invocation on a chat line.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string in Grok format
}

// SummaryText is a reasoning summary fragment.
type SummaryText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ContentBlock is a user content part (text or image).
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
