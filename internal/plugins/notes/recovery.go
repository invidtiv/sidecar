package notes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const noteDraftDir = "sidecar-note-drafts"

type noteDraft struct {
	ID              string `json:"id"`
	Content         string `json:"content"`
	BaseContent     string `json:"base_content,omitempty"`
	InFlightContent string `json:"in_flight_content,omitempty"`
	ProjectRoot     string `json:"project_root,omitempty"`
	CreatedAt       int64  `json:"created_at,omitempty"`
}

var noteDraftMu sync.Mutex

func draftPath(projectRoot, noteID string) string {
	sum := sha256.Sum256([]byte(noteID))
	name := hex.EncodeToString(sum[:]) + ".json"
	return filepath.Join(projectRoot, ".todos", noteDraftDir, name)
}

// writeNoteDraft is the lifecycle's small synchronous durability boundary.
// It never opens td/SQLite: an atomic 0600 JSON checkpoint lets Stop return
// immediately while the actual td write continues in the background.
func writeNoteDraft(projectRoot, noteID, content string) (string, error) {
	return writeNoteDraftState(projectRoot, noteDraft{ID: noteID, Content: content})
}

func writeNoteDraftState(projectRoot string, draft noteDraft) (string, error) {
	noteID := draft.ID
	if projectRoot == "" || noteID == "" {
		return "", errors.New("note draft is missing project or note identity")
	}
	draft.ProjectRoot = projectRoot
	if draft.CreatedAt == 0 {
		draft.CreatedAt = time.Now().UnixNano()
	}
	path := draftPath(projectRoot, noteID)
	if err := writeDraftFile(path, draft); err == nil {
		return path, nil
	} else {
		primaryErr := err
		fallback, fallbackErr := fallbackDraftPath(projectRoot, noteID)
		if fallbackErr != nil {
			return "", fmt.Errorf("write primary note draft: %v; choose fallback: %w", primaryErr, fallbackErr)
		}
		if err := writeDraftFile(fallback, draft); err != nil {
			return "", fmt.Errorf("write primary note draft: %v; write fallback: %w", primaryErr, err)
		}
		return fallback, nil
	}
}

func fallbackDraftPath(projectRoot, noteID string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(projectRoot + "\x00" + noteID))
	return filepath.Join(cache, "sidecar", noteDraftDir, hex.EncodeToString(sum[:])+".json"), nil
}

func writeDraftFile(path string, draft noteDraft) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create note draft directory: %w", err)
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".draft-*")
	if err != nil {
		return fmt.Errorf("create note draft: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("commit note draft: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		err = d.Sync()
		_ = d.Close()
		if err != nil {
			return fmt.Errorf("sync note draft directory: %w", err)
		}
	} else {
		return fmt.Errorf("open note draft directory: %w", err)
	}
	return nil
}

func readNoteDraft(path string) (noteDraft, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return noteDraft{}, err
	}
	var draft noteDraft
	if err := json.Unmarshal(data, &draft); err != nil {
		return noteDraft{}, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	if draft.ID == "" {
		return noteDraft{}, fmt.Errorf("decode %s: missing note id", filepath.Base(path))
	}
	return draft, nil
}

func removeDraftIfCurrent(path string, want noteDraft) {
	got, err := readNoteDraft(path)
	if err == nil && got.ID == want.ID && got.Content == want.Content &&
		got.BaseContent == want.BaseContent && got.InFlightContent == want.InFlightContent &&
		(want.ProjectRoot == "" || got.ProjectRoot == want.ProjectRoot) {
		_ = os.Remove(path)
	}
}

// recoverNoteDrafts replays checkpoints before a list read. A failed replay
// leaves its file intact so refresh/relaunch remains a deterministic retry.
func recoverNoteDrafts(projectRoot string, store noteStore) error {
	noteDraftMu.Lock()
	defer noteDraftMu.Unlock()
	dirs := []string{filepath.Join(projectRoot, ".todos", noteDraftDir)}
	if fallback, err := fallbackDraftPath(projectRoot, "_"); err == nil {
		dirs = append(dirs, filepath.Dir(fallback))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read note drafts: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			draft, err := readNoteDraft(path)
			if err != nil {
				return err
			}
			if draft.ProjectRoot != "" && draft.ProjectRoot != projectRoot {
				continue
			}
			current, err := store.Get(draft.ID)
			if err != nil {
				return fmt.Errorf("inspect unsaved note %s: %w", draft.ID, err)
			}
			if current == nil {
				return fmt.Errorf("recover unsaved note %s: note not found", draft.ID)
			}
			if current.Content == draft.Content {
				removeDraftIfCurrent(path, draft)
				continue
			}
			if current.Content != draft.BaseContent &&
				(draft.InFlightContent == "" || current.Content != draft.InFlightContent) {
				return fmt.Errorf("recover unsaved note %s: note changed outside this recovery", draft.ID)
			}
			if _, err := store.SaveContent(draft.ID, draft.Content); err != nil {
				return fmt.Errorf("recover unsaved note %s: %w", draft.ID, err)
			}
			removeDraftIfCurrent(path, draft)
		}
	}
	return nil
}

func saveContentAndRetire(projectRoot string, store noteStore, noteID, content string, startedAt int64) (*Note, error) {
	noteDraftMu.Lock()
	defer noteDraftMu.Unlock()
	note, err := store.SaveContent(noteID, content)
	if err == nil && projectRoot != "" {
		retireDraftAfterSave(draftPath(projectRoot, noteID), content, startedAt)
		if fallback, fallbackErr := fallbackDraftPath(projectRoot, noteID); fallbackErr == nil {
			retireDraftAfterSave(fallback, content, startedAt)
		}
	}
	return note, err
}

func retireDraftAfterSave(path, content string, startedAt int64) {
	draft, err := readNoteDraft(path)
	if err != nil {
		return
	}
	// A checkpoint written after this save began owns a newer user intent. An
	// older in-flight completion must not retire it unless it wrote that exact
	// final content. Saves begun after the checkpoint supersede it normally.
	if draft.Content == content || draft.CreatedAt == 0 || (startedAt != 0 && draft.CreatedAt <= startedAt) {
		_ = os.Remove(path)
	}
}
