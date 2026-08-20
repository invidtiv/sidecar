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

// Alert builds a PostMsg for a source-specific notification. It exists so a
// producer that needs more than the generic `system` toast — a blocked action,
// a merge that unwound itself, an agent that fell back — can say which source
// it is speaking as without assembling a Notification by hand. Expiry is left
// to the store, which asks ExpiryFor for the source's configured lease.
func Alert(source SourceID, severity Severity, title string) PostMsg {
	return PostMsg{Notification: Notification{
		Source:   source,
		Severity: severity,
		Title:    title,
	}}
}
