package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
	_ "github.com/mattn/go-sqlite3"
)

const stateThreadsQuery = `
SELECT id, rollout_path, cwd, title, first_user_message,
       created_at, updated_at, tokens_used, source,
	   COALESCE(thread_source, ''), COALESCE(model, ''), has_user_event
FROM threads
WHERE archived = 0`

const stateDBReadTimeout = 500 * time.Millisecond

func codexReadOnlyDSN(path string) string {
	dsn := adapter.ReadOnlyDSN(path)
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	query := u.Query()
	query.Set("_busy_timeout", "500")
	u.RawQuery = query.Encode()
	return u.String()
}

type indexedThread struct {
	id, path, cwd, title, firstUser, source, threadSource, model string
	created, updated                                             int64
	tokens                                                       int
	hasUser                                                      int
}

// sessionsFromStateDB returns ok=false when the external Codex index is absent
// or schema-incompatible, allowing the JSONL adapter to remain compatible with
// older and future Codex versions.
func (a *Adapter) sessionsFromStateDB(projectRoot string) ([]adapter.Session, bool) {
	if !a.canUseStateDB() {
		return nil, false
	}
	db, err := sql.Open("sqlite3", codexReadOnlyDSN(a.stateDBPath))
	if err != nil {
		return nil, false
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), stateDBReadTimeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, stateThreadsQuery)
	if err != nil {
		return nil, false
	}
	defer func() { _ = rows.Close() }()

	resolved := newResolvedProjectPath(projectRoot)
	sessions := make([]adapter.Session, 0, 64)
	index := make(map[string]string)
	seen := make(map[string]struct{})
	for rows.Next() {
		var t indexedThread
		if err := rows.Scan(&t.id, &t.path, &t.cwd, &t.title, &t.firstUser,
			&t.created, &t.updated, &t.tokens, &t.source, &t.threadSource, &t.model, &t.hasUser); err != nil {
			return nil, false
		}
		if t.id == "" || t.path == "" || !resolved.matchesCWD(t.cwd) {
			continue
		}
		if _, duplicate := seen[t.id]; duplicate {
			continue
		}
		info, err := os.Stat(t.path)
		if err != nil || info.IsDir() {
			// A stale index row cannot provide a usable conversation. Let the
			// JSONL fallback rediscover files if the whole index is stale.
			continue
		}
		seen[t.id] = struct{}{}
		index[t.id] = t.path
		name := strings.TrimSpace(t.title)
		if name == "" {
			name = strings.TrimSpace(t.firstUser)
		}
		if name == "" {
			name = shortID(t.id)
		}
		created := unixCodexTime(t.created)
		updated := unixCodexTime(t.updated)
		if created.IsZero() {
			created = info.ModTime()
		}
		if updated.IsZero() {
			updated = info.ModTime()
		}
		sessions = append(sessions, adapter.Session{
			ID: t.id, Name: truncateTitle(name, 50),
			AdapterID: adapterID, AdapterName: adapterName, AdapterIcon: a.Icon(),
			CreatedAt: created, UpdatedAt: updated, Duration: updated.Sub(created),
			IsActive: time.Since(updated) < 5*time.Minute, TotalTokens: t.tokens,
			IsSubAgent:   isSubagentSource(t.source, t.threadSource),
			MessageCount: indexedMessageCount(info.Size()), FileSize: info.Size(), Path: t.path,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false
	}

	a.cacheSessionPaths(index)
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID > sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, true
}

func (a *Adapter) canUseStateDB() bool {
	return strings.TrimSpace(a.stateDBPath) != "" &&
		filepath.Clean(filepath.Dir(a.stateDBPath)) == filepath.Clean(filepath.Dir(a.sessionsDir))
}

func unixCodexTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value > 10_000_000_000 {
		return time.UnixMilli(value)
	}
	return time.Unix(value, 0)
}

func isSubagentSource(source, threadSource string) bool {
	if strings.EqualFold(strings.TrimSpace(threadSource), "subagent") {
		return true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(source), &object) == nil {
		_, ok := object["subagent"]
		return ok
	}
	return false
}

// SessionByID implements adapter.TargetedRefresher without scanning all rollout
// files on each active-session update.
func (a *Adapter) SessionByID(sessionID string) (*adapter.Session, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	if sessions, ok := a.sessionByIDFromStateDB(sessionID); ok {
		return sessions, nil
	}
	path := a.sessionFilePath(sessionID)
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	meta, err := a.sessionMetadata(path, info)
	if err != nil {
		return nil, err
	}
	s := sessionFromMetadata(a, meta, info)
	return &s, nil
}

func (a *Adapter) sessionByIDFromStateDB(sessionID string) (*adapter.Session, bool) {
	if !a.canUseStateDB() {
		return nil, false
	}
	db, err := sql.Open("sqlite3", codexReadOnlyDSN(a.stateDBPath))
	if err != nil {
		return nil, false
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), stateDBReadTimeout)
	defer cancel()
	row := db.QueryRowContext(ctx, stateThreadsQuery+" AND id = ?", sessionID)
	var t indexedThread
	if err := row.Scan(&t.id, &t.path, &t.cwd, &t.title, &t.firstUser,
		&t.created, &t.updated, &t.tokens, &t.source, &t.threadSource, &t.model, &t.hasUser); err != nil {
		if err == sql.ErrNoRows {
			return nil, true
		}
		return nil, false
	}
	info, err := os.Stat(t.path)
	if err != nil {
		return nil, true
	}
	name := t.title
	if strings.TrimSpace(name) == "" {
		name = t.firstUser
	}
	if strings.TrimSpace(name) == "" {
		name = shortID(t.id)
	}
	created, updated := unixCodexTime(t.created), unixCodexTime(t.updated)
	s := &adapter.Session{ID: t.id, Name: truncateTitle(name, 50), AdapterID: adapterID,
		AdapterName: adapterName, AdapterIcon: a.Icon(), CreatedAt: created, UpdatedAt: updated,
		Duration: updated.Sub(created), IsActive: time.Since(updated) < 5*time.Minute,
		TotalTokens: t.tokens, IsSubAgent: isSubagentSource(t.source, t.threadSource),
		MessageCount: indexedMessageCount(info.Size()), FileSize: info.Size(), Path: t.path}
	a.cacheSessionPath(t.id, t.path)
	return s, true
}

// Current Codex does not reliably maintain threads.has_user_event. Treat an
// existing non-empty rollout as unknown-but-searchable; Messages remains the
// authority for whether it contains visible conversation records. A zero-byte
// rollout remains genuinely metadata-only without scanning every JSONL file.
func indexedMessageCount(fileSize int64) int {
	if fileSize > 0 {
		return 1
	}
	return 0
}

// SessionIDFromPath resolves fsnotify paths to stable Codex thread IDs. Codex
// rollout basenames are not session identities on current releases.
func (a *Adapter) SessionIDFromPath(path string) (string, error) {
	if a.canUseStateDB() {
		db, err := sql.Open("sqlite3", codexReadOnlyDSN(a.stateDBPath))
		if err == nil {
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(0)
			var id string
			ctx, cancel := context.WithTimeout(context.Background(), stateDBReadTimeout)
			err = db.QueryRowContext(ctx, `SELECT id FROM threads WHERE rollout_path = ? AND archived = 0 LIMIT 1`, path).Scan(&id)
			cancel()
			_ = db.Close()
			if err == nil && id != "" {
				a.cacheSessionPath(id, path)
				return id, nil
			}
			// Any other outcome, including schema drift, falls through to rollout metadata.
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	meta, err := a.sessionMetadata(path, info)
	if err != nil {
		return "", err
	}
	if meta.SessionID != "" {
		a.cacheSessionPath(meta.SessionID, path)
	}
	return meta.SessionID, nil
}

func (a *Adapter) rolloutPathFromStateDB(sessionID string) (string, bool) {
	if !a.canUseStateDB() {
		return "", false
	}
	db, err := sql.Open("sqlite3", codexReadOnlyDSN(a.stateDBPath))
	if err != nil {
		return "", false
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	defer func() { _ = db.Close() }()
	var path string
	ctx, cancel := context.WithTimeout(context.Background(), stateDBReadTimeout)
	defer cancel()
	err = db.QueryRowContext(ctx, `SELECT rollout_path FROM threads WHERE id = ? AND archived = 0 LIMIT 1`, sessionID).Scan(&path)
	if err == sql.ErrNoRows {
		return "", true
	}
	if err != nil {
		return "", false
	}
	return path, true
}

func sessionFromMetadata(a *Adapter, meta *SessionMetadata, info os.FileInfo) adapter.Session {
	name := meta.FirstUserMessage
	if name == "" {
		name = shortID(meta.SessionID)
	}
	return adapter.Session{ID: meta.SessionID, Name: truncateTitle(name, 50), AdapterID: adapterID,
		AdapterName: adapterName, AdapterIcon: a.Icon(), CreatedAt: meta.FirstMsg,
		UpdatedAt: meta.LastMsg, Duration: meta.LastMsg.Sub(meta.FirstMsg),
		IsActive: time.Since(meta.LastMsg) < 5*time.Minute, TotalTokens: meta.TotalTokens,
		IsSubAgent: meta.IsSubAgent, MessageCount: meta.MsgCount, FileSize: info.Size(), Path: meta.Path}
}
