package muse

import "time"

// rawRecord is one line from session.jsonl.
type rawRecord struct {
	SchemaVersion        int        `json:"schema_version"`
	ID                   string     `json:"id"`
	Sequence             int64      `json:"sequence"`
	RecordedAt           int64      `json:"recorded_at"`
	PayloadType          string     `json:"payload_type"`
	PayloadSchemaVersion int        `json:"payload_schema_version"`
	Payload              rawPayload `json:"payload"`
	// retained_frame wraps multiple records; handled specially.
	RetainedFrame string `json:"retained_frame"`
	Children      []struct {
		RecordJSON string `json:"record_json"`
	} `json:"children"`
}

type rawPayload struct {
	Kind   string `json:"kind"`
	Record struct {
		WorkspaceRoot string `json:"workspace_root"`
	} `json:"record"`
	Event struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"event"`
	IntentID        string `json:"intent_id"`
	SourceSessionID string `json:"source_session_id"`
	RefillBlocks    []struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"refill_blocks"`
	ModelMessages []struct {
		Content []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"model_messages"`
}

// sessionIndexRow maps a SQLite sessions row.
type sessionIndexRow struct {
	SessionID      string
	SessionLogPath string
	WorkspaceRoot  string
	WorkspaceKey   string
	Title          string
	FirstPrompt    string
	CreatedAtUs    *int64
	UpdatedAtUs    *int64
	PromptCount    int
	Status         string
}

// toTime converts microseconds since epoch to time.Time.
func usToTime(us *int64) time.Time {
	if us == nil || *us == 0 {
		return time.Time{}
	}
	sec := *us / 1_000_000
	nsec := (*us % 1_000_000) * 1000
	return time.Unix(sec, nsec).In(time.Local)
}

func usToTimeVal(us int64) time.Time {
	if us == 0 {
		return time.Time{}
	}
	sec := us / 1_000_000
	nsec := (us % 1_000_000) * 1000
	return time.Unix(sec, nsec).In(time.Local)
}
