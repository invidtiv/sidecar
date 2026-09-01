package contentservice

import (
	"context"
	"time"

	"github.com/marcus/sidecar/internal/noteview"
)

// NoteDTO is the explicit wire form of one td note.
type NoteDTO struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Content   string `json:"content,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	Pinned    bool   `json:"pinned,omitempty"`
	Archived  bool   `json:"archived,omitempty"`
}

// NoteDocument is the host-side note payload plus the revision a later
// conditional read will send back.
type NoteDocument struct {
	Data        *noteview.Data
	Revision    string
	NotModified bool
}

// ReadNote loads one note from workDir. If ifRevision matches the current
// payload revision, the body is omitted and NotModified is set.
func ReadNote(ctx context.Context, root, noteID, ifRevision string) (NoteDocument, error) {
	return Default().readNoteAt(ctx, root, noteID, ifRevision)
}

func (s *Service) readNoteAt(ctx context.Context, root, noteID, ifRevision string) (NoteDocument, error) {
	if err := ctx.Err(); err != nil {
		return NoteDocument{}, err
	}
	id := noteview.NormalizeID(noteID)
	if id == "" {
		return NoteDocument{}, Rejected("invalid note id %q", noteID)
	}
	lookup := s.LookupNote
	if lookup == nil {
		lookup = defaultLookupNote
	}
	data, err := lookup(ctx, root, id)
	if err != nil {
		return NoteDocument{}, Rejected("%s", err.Error())
	}
	if data == nil || data.ID == "" {
		return NoteDocument{}, Rejected("note %q not found", id)
	}
	rev := revisionForValue(noteDTOFrom(data))
	if ifRevision != "" && ifRevision == rev {
		return NoteDocument{Revision: rev, NotModified: true}, nil
	}
	return NoteDocument{Data: data, Revision: rev}, nil
}

func (s *Service) resolveNote(ctx context.Context, workspaceID, target string) (ResolveResult, error) {
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ResolveResult{}, err
	}
	id := noteview.NormalizeID(target)
	if id == "" {
		return ResolveResult{}, Rejected("invalid note id %q", target)
	}
	return ResolveResult{Kind: KindNote, Workspace: ws.ID, Target: id, Display: id}, nil
}

func defaultLookupNote(_ context.Context, workDir, noteID string) (*noteview.Data, error) {
	return noteview.Lookup(workDir, noteID)
}

func noteReadResultFrom(workspace string, doc NoteDocument) ReadResult {
	result := ReadResult{
		Kind:        KindNote,
		Operation:   OpNote,
		Workspace:   workspace,
		Revision:    doc.Revision,
		NotModified: doc.NotModified,
	}
	if doc.NotModified {
		result.Operation = ""
		result.Workspace = ""
		return result
	}
	result.Note = noteDTOFrom(doc.Data)
	if result.Note != nil {
		result.Target = result.Note.ID
		result.Display = result.Note.ID
	}
	return result
}

func noteDTOFrom(data *noteview.Data) *NoteDTO {
	if data == nil {
		return nil
	}
	return &NoteDTO{
		ID:        data.ID,
		Title:     data.Title,
		Content:   data.Content,
		CreatedAt: formatNoteTime(data.CreatedAt),
		UpdatedAt: formatNoteTime(data.UpdatedAt),
		Pinned:    data.Pinned,
		Archived:  data.Archived,
	}
}

// NoteFromDTO converts a wire note back into the shared viewer payload.
func NoteFromDTO(dto *NoteDTO) *noteview.Data {
	if dto == nil {
		return nil
	}
	data := &noteview.Data{
		ID:       dto.ID,
		Title:    dto.Title,
		Content:  dto.Content,
		Pinned:   dto.Pinned,
		Archived: dto.Archived,
	}
	if ts, err := time.Parse(time.RFC3339Nano, dto.CreatedAt); err == nil {
		data.CreatedAt = ts
	} else if ts, err := time.Parse(time.RFC3339, dto.CreatedAt); err == nil {
		data.CreatedAt = ts
	}
	if ts, err := time.Parse(time.RFC3339Nano, dto.UpdatedAt); err == nil {
		data.UpdatedAt = ts
	} else if ts, err := time.Parse(time.RFC3339, dto.UpdatedAt); err == nil {
		data.UpdatedAt = ts
	}
	return data
}

func formatNoteTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
