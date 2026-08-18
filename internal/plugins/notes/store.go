package notes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tdnotes "github.com/marcus/td/pkg/notes"

	"github.com/marcus/sidecar/internal/tdroot"
)

const maxTitleLength = 80

// Note is the plugin's view of a td note.
type Note = tdnotes.Note

// Store persists notes through the td CLI. Sidecar must never open
// .todos/issues.db itself — a second long-lived SQLite writer alongside
// transient td processes corrupts the WAL (td-adbf16). Tests use the
// in-process backend via NewTestStore against an isolated database.
type Store struct {
	baseDir   string
	sessionID string

	// td is set only by NewTestStore; when non-nil it replaces the CLI.
	td *tdnotes.Store
}

// NewStore returns a CLI-backed store rooted at the project directory.
// baseDir is the project root (not the issues.db path). sessionID, when
// non-empty, is exported as TD_SESSION_ID for attribution.
func NewStore(baseDir, sessionID string) (*Store, error) {
	if _, err := exec.LookPath("td"); err != nil {
		return nil, fmt.Errorf("td binary not found in PATH: %w", err)
	}
	dbPath := tdroot.ResolveDBPath(baseDir)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("td database not found at %s: %w", dbPath, err)
	}
	return &Store{baseDir: baseDir, sessionID: sessionID}, nil
}

// newInProcessStore opens an existing test database via td's in-process API.
// Test-only: production peers must go through the CLI.
func newInProcessStore(baseDir, sessionID string) (*Store, error) {
	s, err := tdnotes.Open(baseDir)
	if err != nil {
		return nil, err
	}
	return &Store{baseDir: baseDir, sessionID: sessionID, td: s}, nil
}

// NewTestStore creates an isolated in-process td database for tests.
func NewTestStore(baseDir, sessionID string) (*Store, error) {
	s, err := tdnotes.Init(baseDir)
	if err != nil {
		return nil, err
	}
	return &Store{baseDir: baseDir, sessionID: sessionID, td: s}, nil
}

// DefaultDBPath returns the default database path for a given workdir.
func DefaultDBPath(workDir string) string {
	return tdroot.ResolveDBPath(workDir)
}

// Close closes the test backend, if any. The CLI backend holds no handles.
func (s *Store) Close() error {
	if s == nil || s.td == nil {
		return nil
	}
	return s.td.Close()
}

// run executes a td note subcommand and returns its stdout.
func (s *Store) run(args ...string) ([]byte, error) {
	cmd := exec.Command("td", append([]string{"-w", s.baseDir, "--json", "note"}, args...)...)
	cmd.Env = os.Environ()
	if s.sessionID != "" {
		cmd.Env = append(cmd.Env, "TD_SESSION_ID="+s.sessionID)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("td note %s: %s: %w", args[0], msg, err)
	}
	return stdout.Bytes(), nil
}

// noteEnvelope matches td's EmitResult payload for note mutations.
type noteEnvelope struct {
	Note *Note `json:"note"`
}

func (s *Store) runNote(args ...string) (*Note, error) {
	out, err := s.run(args...)
	if err != nil {
		return nil, err
	}
	var env noteEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("parse td note output: %w", err)
	}
	return env.Note, nil
}

// Create inserts a new note.
func (s *Store) Create(title, content string) (*Note, error) {
	if s.td != nil {
		return s.td.Create(title, content)
	}
	return s.runNote("add", "--content", content, "--", title)
}

// Update writes title and content. Pin/archive changes should use TogglePin /
// ToggleArchive — td's Update does not touch those flags.
func (s *Store) Update(note *Note) error {
	if s.td != nil {
		_, err := s.td.Update(note.ID, note.Title, note.Content)
		return err
	}
	_, err := s.runNote("edit", note.ID, "--title", note.Title, "--content", note.Content)
	return err
}

// Delete soft-deletes a note.
func (s *Store) Delete(id string) error {
	if s.td != nil {
		return s.td.Delete(id)
	}
	_, err := s.run("delete", id)
	return err
}

// Get retrieves a note by ID, including soft-deleted rows.
// Missing notes return (nil, nil) to match the previous store contract.
func (s *Store) Get(id string) (*Note, error) {
	if s.td != nil {
		n, err := s.td.GetAny(id)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil, nil
			}
			return nil, err
		}
		return n, nil
	}
	out, err := s.run("show", id, "--include-deleted")
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	var n Note
	if err := json.Unmarshal(out, &n); err != nil {
		return nil, fmt.Errorf("parse td note show output: %w", err)
	}
	return &n, nil
}

func (s *Store) runList(args ...string) ([]Note, error) {
	out, err := s.run(append([]string{"list", "--limit", "0"}, args...)...)
	if err != nil {
		return nil, err
	}
	var notes []Note
	if err := json.Unmarshal(out, &notes); err != nil {
		return nil, fmt.Errorf("parse td note list output: %w", err)
	}
	return notes, nil
}

// List retrieves non-deleted notes. includeArchived includes archived ones.
func (s *Store) List(includeArchived bool) ([]Note, error) {
	if s.td != nil {
		opts := tdnotes.ListOptions{}
		if !includeArchived {
			f := false
			opts.Archived = &f
		}
		return s.td.List(opts)
	}
	if includeArchived {
		return s.runList("--all")
	}
	return s.runList()
}

// ListArchived retrieves archived, non-deleted notes.
func (s *Store) ListArchived() ([]Note, error) {
	if s.td != nil {
		t := true
		return s.td.List(tdnotes.ListOptions{Archived: &t})
	}
	return s.runList("--archived")
}

// ListDeleted retrieves soft-deleted notes.
func (s *Store) ListDeleted() ([]Note, error) {
	if s.td != nil {
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
	return s.runList("--deleted")
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
	verb := "pin"
	if note.Pinned {
		verb = "unpin"
	}
	if s.td != nil {
		if note.Pinned {
			return s.td.Unpin(id)
		}
		return s.td.Pin(id)
	}
	_, err = s.run(verb, id)
	return err
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
	verb := "archive"
	if note.Archived {
		verb = "unarchive"
	}
	if s.td != nil {
		if note.Archived {
			return s.td.Unarchive(id)
		}
		return s.td.Archive(id)
	}
	_, err = s.run(verb, id)
	return err
}

// Restore undeletes a note.
func (s *Store) Restore(id string) error {
	if s.td != nil {
		_, err := s.td.Restore(id)
		return err
	}
	_, err := s.run("restore", id)
	return err
}

// Unarchive sets archived=false for a note.
func (s *Store) Unarchive(id string) error {
	if s.td != nil {
		return s.td.Unarchive(id)
	}
	_, err := s.run("unarchive", id)
	return err
}

// UpdateContent updates the body of a note and leaves Title unchanged.
// Title is set at create time (NV query / first line); content-only saves
// must not clobber a title written via td note edit --title.
func (s *Store) UpdateContent(id, content string) error {
	note, err := s.Get(id)
	if err != nil {
		return err
	}
	if note == nil || note.DeletedAt != nil {
		return fmt.Errorf("note not found: %s", id)
	}
	if s.td != nil {
		_, err = s.td.Update(id, note.Title, content)
		return err
	}
	_, err = s.runNote("edit", id, "--content", content)
	return err
}

// NotePath writes the note to a unique 0600 temp file for an external editor.
func (s *Store) NotePath(id string) string {
	note, err := s.Get(id)
	if err != nil || note == nil {
		return ""
	}
	f, err := os.CreateTemp("", "sidecar-note-*.md")
	if err != nil {
		return ""
	}
	path := f.Name()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return ""
	}
	if _, err := f.Write([]byte(note.Content)); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return ""
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return ""
	}
	return path
}

// removeNoteExport deletes a temp note export. Missing paths are ignored.
func removeNoteExport(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}
