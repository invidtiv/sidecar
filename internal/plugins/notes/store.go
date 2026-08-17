package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tdnotes "github.com/marcus/td/pkg/notes"

	"github.com/marcus/sidecar/internal/tdroot"
)

const maxTitleLength = 80

// Note is the plugin's view of a td note.
type Note = tdnotes.Note

// Store is a thin adapter over td's public notes API.
type Store struct {
	td *tdnotes.Store
}

// NewStore opens the project's td database for notes.
// baseDir is the project root (not the issues.db path). If sessionID is empty,
// TD_SESSION_ID or "sidecar" is recorded only for local attribution; writes
// themselves go through td/pkg/notes.
func NewStore(baseDir, sessionID string) (*Store, error) {
	s, err := tdnotes.Open(baseDir)
	if err != nil {
		return nil, err
	}
	return &Store{td: s}, nil
}

// NewTestStore creates an isolated td database for tests.
func NewTestStore(baseDir, sessionID string) (*Store, error) {
	s, err := tdnotes.Init(baseDir)
	if err != nil {
		return nil, err
	}
	return &Store{td: s}, nil
}

// DefaultDBPath returns the default database path for a given workdir.
func DefaultDBPath(workDir string) string {
	return tdroot.ResolveDBPath(workDir)
}

// Close closes the underlying td store.
func (s *Store) Close() error {
	if s == nil || s.td == nil {
		return nil
	}
	return s.td.Close()
}

// Create inserts a new note.
func (s *Store) Create(title, content string) (*Note, error) {
	return s.td.Create(title, content)
}

// Update writes title and content. Pin/archive changes should use TogglePin /
// ToggleArchive — td's Update does not touch those flags.
func (s *Store) Update(note *Note) error {
	_, err := s.td.Update(note.ID, note.Title, note.Content)
	return err
}

// Delete soft-deletes a note.
func (s *Store) Delete(id string) error {
	return s.td.Delete(id)
}

// Get retrieves a note by ID, including soft-deleted rows.
// Missing notes return (nil, nil) to match the previous store contract.
func (s *Store) Get(id string) (*Note, error) {
	n, err := s.td.GetAny(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	return n, nil
}

// List retrieves non-deleted notes. includeArchived includes archived ones.
func (s *Store) List(includeArchived bool) ([]Note, error) {
	opts := tdnotes.ListOptions{}
	if !includeArchived {
		f := false
		opts.Archived = &f
	}
	return s.td.List(opts)
}

// ListArchived retrieves archived, non-deleted notes.
func (s *Store) ListArchived() ([]Note, error) {
	t := true
	return s.td.List(tdnotes.ListOptions{Archived: &t})
}

// ListDeleted retrieves soft-deleted notes.
func (s *Store) ListDeleted() ([]Note, error) {
	all, err := s.td.List(tdnotes.ListOptions{IncludeDeleted: true})
	if err != nil {
		return nil, err
	}
	out := make([]Note, 0, len(all))
	for _, n := range all {
		if n.DeletedAt != nil {
			out = append(out, n)
		}
	}
	return out, nil
}

// TogglePin toggles the pinned state of a note.
func (s *Store) TogglePin(id string) error {
	note, err := s.Get(id)
	if err != nil {
		return err
	}
	if note == nil || note.DeletedAt != nil {
		return fmt.Errorf("note not found: %s", id)
	}
	if note.Pinned {
		return s.td.Unpin(id)
	}
	return s.td.Pin(id)
}

// ToggleArchive toggles the archived state of a note.
func (s *Store) ToggleArchive(id string) error {
	note, err := s.Get(id)
	if err != nil {
		return err
	}
	if note == nil || note.DeletedAt != nil {
		return fmt.Errorf("note not found: %s", id)
	}
	if note.Archived {
		return s.td.Unarchive(id)
	}
	return s.td.Archive(id)
}

// Restore undeletes a note.
func (s *Store) Restore(id string) error {
	_, err := s.td.Restore(id)
	return err
}

// Unarchive sets archived=false for a note.
func (s *Store) Unarchive(id string) error {
	return s.td.Unarchive(id)
}

// UpdateContent updates the content of a note, using the first line as title.
func (s *Store) UpdateContent(id, content string) error {
	note, err := s.Get(id)
	if err != nil {
		return err
	}
	if note == nil || note.DeletedAt != nil {
		return fmt.Errorf("note not found: %s", id)
	}
	title := ""
	if lines := strings.SplitN(content, "\n", 2); len(lines) > 0 {
		title = lines[0]
	}
	_, err = s.td.Update(id, title, content)
	return err
}

// NotePath writes the note to a temp file for an external editor.
func (s *Store) NotePath(id string) string {
	note, err := s.Get(id)
	if err != nil || note == nil {
		return ""
	}
	tmpFile := filepath.Join(os.TempDir(), "sidecar-note-"+id+".md")
	if err := os.WriteFile(tmpFile, []byte(note.Content), 0644); err != nil {
		return ""
	}
	return tmpFile
}
