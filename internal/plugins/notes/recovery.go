package notes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const noteDraftDir = "sidecar-note-drafts"

type noteDraft struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

func draftPath(projectRoot, noteID string) string {
	sum := sha256.Sum256([]byte(noteID))
	name := hex.EncodeToString(sum[:]) + ".json"
	return filepath.Join(projectRoot, ".todos", noteDraftDir, name)
}

// writeNoteDraft is the lifecycle's small synchronous durability boundary.
// It never opens td/SQLite: an atomic 0600 JSON checkpoint lets Stop return
// immediately while the actual td write continues in the background.
func writeNoteDraft(projectRoot, noteID, content string) (string, error) {
	if projectRoot == "" || noteID == "" {
		return "", errors.New("note draft is missing project or note identity")
	}
	dir := filepath.Dir(draftPath(projectRoot, noteID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create note draft directory: %w", err)
	}
	data, err := json.Marshal(noteDraft{ID: noteID, Content: content})
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".draft-*")
	if err != nil {
		return "", fmt.Errorf("create note draft: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	path := draftPath(projectRoot, noteID)
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("commit note draft: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		err = d.Sync()
		_ = d.Close()
		if err != nil {
			return "", fmt.Errorf("sync note draft directory: %w", err)
		}
	} else {
		return "", fmt.Errorf("open note draft directory: %w", err)
	}
	return path, nil
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
	if err == nil && got == want {
		_ = os.Remove(path)
	}
}

// recoverNoteDrafts replays checkpoints before a list read. A failed replay
// leaves its file intact so refresh/relaunch remains a deterministic retry.
func recoverNoteDrafts(projectRoot string, store noteStore) error {
	dir := filepath.Join(projectRoot, ".todos", noteDraftDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
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
		if _, err := store.SaveContent(draft.ID, draft.Content); err != nil {
			return fmt.Errorf("recover unsaved note %s: %w", draft.ID, err)
		}
		removeDraftIfCurrent(path, draft)
	}
	return nil
}
