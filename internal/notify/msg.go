package notify

// The message types are plain structs so this package stays free of any
// harness dependency: they satisfy tea.Msg (which is `any`) without importing
// Bubble Tea, and the tea.Cmd helpers that wrap them live in internal/app.

// PostMsg asks the app to file a notification. It is the in-process posting
// API — the same road the CLI's request takes once it reaches the TUI.
type PostMsg struct {
	Notification Notification
}

// ReadMsg marks one notification read.
type ReadMsg struct {
	ID string
}

// DismissMsg dismisses one notification.
type DismissMsg struct {
	ID string
}

// PostedMsg is broadcast after a notification has been stored, carrying the
// completed record so a toast host can start its countdown from the same
// timestamps the store holds.
type PostedMsg struct {
	Notification Notification
}
