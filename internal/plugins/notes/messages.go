package notes

// NotesLoadedMsg is sent when notes are loaded from the database.
type NotesLoadedMsg struct {
	Notes       []Note
	Err         error
	RecoveryErr error
	Epoch       uint64
	RequestID   uint64
	Filter      NoteFilter
}

// GetEpoch returns the epoch for staleness detection.
func (m NotesLoadedMsg) GetEpoch() uint64 {
	return m.Epoch
}

// NoteSavedMsg is sent when a note is created or updated.
type NoteSavedMsg struct {
	Note             *Note
	Err              error
	Epoch            uint64
	EditorActivation uint64 // Non-zero only for an inline-editor save lifecycle.
	MutationID       uint64 // Non-zero for an optimistic create.
	TempID           string // Local identity replaced by Note.ID on success.
}

// GetEpoch returns the epoch for staleness detection.
func (m NoteSavedMsg) GetEpoch() uint64 {
	return m.Epoch
}

// NoteDeletedMsg is sent when a note is deleted.
type NoteDeletedMsg struct {
	ID         string
	Err        error
	Epoch      uint64
	MutationID uint64 // Non-zero for an optimistic delete.
}

// GetEpoch returns the epoch for staleness detection.
func (m NoteDeletedMsg) GetEpoch() uint64 {
	return m.Epoch
}

// NotePinToggledMsg is sent when a note's pinned state is toggled.
type NotePinToggledMsg struct {
	ID    string
	Err   error
	Epoch uint64
}

// GetEpoch returns the epoch for staleness detection.
func (m NotePinToggledMsg) GetEpoch() uint64 {
	return m.Epoch
}

// NoteArchiveToggledMsg is sent when a note's archived state is toggled.
type NoteArchiveToggledMsg struct {
	ID    string
	Err   error
	Epoch uint64
}

// GetEpoch returns the epoch for staleness detection.
func (m NoteArchiveToggledMsg) GetEpoch() uint64 {
	return m.Epoch
}

// NoteContentSavedMsg is sent when a note's content is saved from the editor.
type NoteContentSavedMsg struct {
	ID               string
	Err              error
	Epoch            uint64
	EditorActivation uint64 // Non-zero only for an inline-editor save lifecycle.
	Generation       int    // Built-in autosave generation captured at save start.
	SaveActivation   uint64 // Built-in save lifecycle captured at save start.
	RequestID        uint64 // Built-in request identity; only one may own completion.
	Content          string // Bytes this save wrote; empty for inline/export paths.
	Note             *Note  // Canonical note returned by td when available.
	External         bool   // $EDITOR read-back; not a built-in buffer save.
	Skipped          bool   // In-flight write skipped because a newer persist won.
	ExportPath       string // Retained until an external/inline save is acknowledged.
	ExportRequestID  uint64 // Owns the retained export save attempt.
	WriteSequence    uint64 // Orders all content writes for this note.
}

// GetEpoch returns the epoch for staleness detection.
func (m NoteContentSavedMsg) GetEpoch() uint64 {
	return m.Epoch
}

// AutoSaveTickMsg is sent when the auto-save debounce timer fires.
type AutoSaveTickMsg struct {
	// ID identifies which auto-save timer this is (for debounce check)
	ID int
}

// ExternalEditorPreparedMsg carries an asynchronously-created note export.
// NotePath may invoke td, so preparing it must never run on Bubble Tea Update.
type ExternalEditorPreparedMsg struct {
	ID        string
	Path      string
	Err       error
	Epoch     uint64
	RequestID uint64
}

func (m ExternalEditorPreparedMsg) GetEpoch() uint64 { return m.Epoch }

// NoteRestoredMsg is sent when a note is restored (undo delete/archive).
type NoteRestoredMsg struct {
	ID     string
	Title  string
	Err    error
	Epoch  uint64
	Action UndoAction // Popped action; restored to the stack when persistence fails.
}

// GetEpoch returns the epoch for staleness detection.
func (m NoteRestoredMsg) GetEpoch() uint64 {
	return m.Epoch
}

// InlineAutoSaveTickMsg is sent periodically during inline edit mode for auto-save.
type InlineAutoSaveTickMsg struct {
	// Generation identifies which auto-save timer this is (for staleness check)
	Generation int
}

// InlineAutoSaveResultMsg is sent after an inline auto-save completes.
type InlineAutoSaveResultMsg struct {
	Err        error
	Epoch      uint64
	Activation uint64
	Generation int
	Content    string
	Saved      bool
	Skipped    bool
	Sequence   uint64
}

// GetEpoch returns the epoch for staleness detection.
func (m InlineAutoSaveResultMsg) GetEpoch() uint64 {
	return m.Epoch
}
