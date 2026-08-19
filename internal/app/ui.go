package app

import "time"

// UIState holds header/footer state information.
type UIState struct {
	Clock       time.Time
	LastRefresh time.Time
	WorkDir     string
	ProjectRoot string // Main repo root for shared state (same as WorkDir for non-worktrees)
}

// NewUIState creates a new UI state.
func NewUIState() *UIState {
	now := time.Now()
	return &UIState{
		Clock:       now,
		LastRefresh: now,
	}
}

// UpdateClock updates the current clock time.
func (u *UIState) UpdateClock() {
	u.Clock = time.Now()
}

// MarkRefresh updates the last refresh timestamp.
func (u *UIState) MarkRefresh() {
	u.LastRefresh = time.Now()
}
