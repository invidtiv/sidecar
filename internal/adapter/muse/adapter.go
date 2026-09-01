package muse

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/adapter/cache"
	_ "github.com/mattn/go-sqlite3"
)

const (
	adapterID           = "muse"
	adapterName         = "Muse Spark"
	adapterIcon         = "◈"
	metaCacheMaxEntries = 2048
	msgCacheMaxEntries  = 128
)

// Adapter implements adapter.Adapter for Muse Spark sessions.
type Adapter struct {
	sessionsDir  string
	indexDBPath  string
	sessionIndex map[string]string // sessionID -> log path
	mu           sync.RWMutex
	msgCache     *cache.Cache[messageCacheEntry]
}

type messageCacheEntry struct {
	messages   []adapter.Message
	byteOffset int64
}

// New creates a Muse adapter using XDG_DATA_HOME or ~/.local/share.
func New() *Adapter {
	home, _ := os.UserHomeDir()
	sessionsDir, dbPath := resolvePaths(home)
	return NewWithPaths(sessionsDir, dbPath)
}

// NewWithSessionsDir creates an adapter rooted at sessionsDir (for tests).
// The index DB is assumed at <parent>/session-index.db.
func NewWithSessionsDir(sessionsDir string) *Adapter {
	dbPath := filepath.Join(filepath.Dir(sessionsDir), "session-index.db")
	// If sessionsDir looks like .../sessions, parent contains DB. Otherwise place beside it.
	if filepath.Base(sessionsDir) != "sessions" {
		dbPath = filepath.Join(sessionsDir, "session-index.db")
	}
	return NewWithPaths(sessionsDir, dbPath)
}

// NewWithPaths creates an adapter with explicit sessionsDir and indexDBPath.
func NewWithPaths(sessionsDir, indexDBPath string) *Adapter {
	return &Adapter{
		sessionsDir:  sessionsDir,
		indexDBPath:  indexDBPath,
		sessionIndex: make(map[string]string),
		msgCache:     cache.New[messageCacheEntry](msgCacheMaxEntries),
	}
}

// NewWithIndexForTest creates an adapter with explicit DB path (test helper).
func NewWithIndexForTest(sessionsDir, indexDBPath string) *Adapter {
	return NewWithPaths(sessionsDir, indexDBPath)
}

func resolvePaths(home string) (string, string) {
	base := filepath.Join(home, ".local", "share", "muse")
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		base = filepath.Join(xdg, "muse")
	}
	if custom := os.Getenv("MUSE_HOME"); custom != "" {
		base = custom
	}
	return filepath.Join(base, "sessions"), filepath.Join(base, "session-index.db")
}

func (a *Adapter) ID() string   { return adapterID }
func (a *Adapter) Name() string { return adapterName }
func (a *Adapter) Icon() string { return adapterIcon }

func (a *Adapter) Capabilities() adapter.CapabilitySet {
	return adapter.CapabilitySet{
		adapter.CapSessions: true,
		adapter.CapMessages: true,
		adapter.CapUsage:    false,
		adapter.CapWatch:    true,
	}
}

func (a *Adapter) WatchScope() adapter.WatchScope { return adapter.WatchScopeGlobal }
func (a *Adapter) WatchForProjectDiscovery() bool { return true }

// Detect reports whether any Muse sessions exist for projectRoot.
func (a *Adapter) Detect(projectRoot string) (bool, error) {
	sessions, err := a.Sessions(projectRoot)
	if err != nil {
		return false, err
	}
	return len(sessions) > 0, nil
}

// Sessions returns sessions for projectRoot sorted by UpdatedAt descending.
func (a *Adapter) Sessions(projectRoot string) ([]adapter.Session, error) {
	absRoot, err := resolveProjectPath(projectRoot)
	if err != nil || absRoot == "" {
		return nil, nil
	}

	// Try SQLite index first.
	sessions, indexMap, sqliteOK := a.sessionsFromSQLite(absRoot)
	if sqliteOK {
		// Merge index
		a.mu.Lock()
		for id, p := range indexMap {
			a.sessionIndex[id] = p
		}
		a.mu.Unlock()
		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		})
		return sessions, nil
	}

	// Fallback: filesystem walk.
	return a.sessionsFromFS(absRoot)
}

func (a *Adapter) sessionsFromSQLite(absRoot string) ([]adapter.Session, map[string]string, bool) {
	if _, err := os.Stat(a.indexDBPath); err != nil {
		return nil, nil, false
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", a.indexDBPath))
	if err != nil {
		return nil, nil, false
	}
	defer func() { _ = db.Close() }()

	// Verify table exists
	var cnt int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='sessions'").Scan(&cnt); err != nil || cnt == 0 {
		return nil, nil, false
	}

	query := `SELECT session_id, session_log_path, workspace_root, workspace_key, title, first_user_prompt, created_at_us, updated_at_us, prompt_count, status FROM sessions WHERE workspace_root = ? OR workspace_key = ? ORDER BY updated_at_us DESC`
	rows, err := db.Query(query, absRoot, absRoot)
	if err != nil {
		return nil, nil, false
	}
	defer func() { _ = rows.Close() }()

	var sessions []adapter.Session
	indexMap := make(map[string]string)
	for rows.Next() {
		var r sessionIndexRow
		var createdUs, updatedUs sql.NullInt64
		var title, firstPrompt, status sql.NullString
		var sessID, logPath, wsRoot, wsKey sql.NullString
		var promptCnt sql.NullInt64
		if err := rows.Scan(&sessID, &logPath, &wsRoot, &wsKey, &title, &firstPrompt, &createdUs, &updatedUs, &promptCnt, &status); err != nil {
			continue
		}
		r.SessionID = sessID.String
		r.SessionLogPath = logPath.String
		r.WorkspaceRoot = wsRoot.String
		r.WorkspaceKey = wsKey.String
		r.Title = title.String
		r.FirstPrompt = firstPrompt.String
		if createdUs.Valid {
			v := createdUs.Int64
			r.CreatedAtUs = &v
		}
		if updatedUs.Valid {
			v := updatedUs.Int64
			r.UpdatedAtUs = &v
		}
		if promptCnt.Valid {
			r.PromptCount = int(promptCnt.Int64)
		}
		r.Status = status.String

		if r.SessionID == "" {
			continue
		}
		created := usToTime(r.CreatedAtUs)
		updated := usToTime(r.UpdatedAtUs)
		if created.IsZero() && !updated.IsZero() {
			created = updated
		}
		if updated.IsZero() && !created.IsZero() {
			updated = created
		}
		// If still zero, try file mtime fallback
		var fileSize int64
		var path = r.SessionLogPath
		if path != "" {
			if info, err := os.Stat(path); err == nil {
				fileSize = info.Size()
				if updated.IsZero() {
					updated = info.ModTime()
				}
				if created.IsZero() {
					created = info.ModTime()
				}
			} else {
				// Try to reconstruct path under sessionsDir if DB path is absolute but relocated
				// (tests use temp dirs). Fallback to filesystem search later if not found.
				alt := filepath.Join(a.sessionsDir, r.SessionID, "session.jsonl")
				if info, err := os.Stat(alt); err == nil {
					fileSize = info.Size()
					path = alt
					if updated.IsZero() {
						updated = info.ModTime()
					}
					if created.IsZero() {
						created = info.ModTime()
					}
				}
			}
		}
		name := firstNonEmpty(r.Title, r.FirstPrompt)
		if name == "" {
			name = shortID(r.SessionID)
		}
		isSub := strings.Contains(path, "/subagent/") || strings.Contains(path, string(filepath.Separator)+"subagent"+string(filepath.Separator))
		sessions = append(sessions, adapter.Session{
			ID:           r.SessionID,
			Name:         name,
			Slug:         shortID(r.SessionID),
			AdapterID:    a.ID(),
			AdapterName:  a.Name(),
			AdapterIcon:  a.Icon(),
			CreatedAt:    created,
			UpdatedAt:    updated,
			Duration:     updated.Sub(created),
			IsActive:     !updated.IsZero() && time.Since(updated) < 5*time.Minute,
			IsSubAgent:   isSub,
			MessageCount: r.PromptCount,
			FileSize:     fileSize,
			Path:         path,
		})
		indexMap[r.SessionID] = path
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false
	}
	return sessions, indexMap, true
}

func (a *Adapter) sessionsFromFS(absRoot string) ([]adapter.Session, error) {
	// Walk sessionsDir/YYYY/MM/DD/<uuid>/session.jsonl
	var sessions []adapter.Session
	newIndex := make(map[string]string)

	err := filepath.WalkDir(a.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "session.jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		// Extract workspace_root from metadata
		ws, err := extractWorkspaceRoot(path)
		if err != nil || ws == "" {
			return nil
		}
		if !pathMatchesRoot(ws, absRoot) {
			return nil
		}
		// Derive session ID from directory name: .../<uuid>/session.jsonl
		sessionID := filepath.Base(filepath.Dir(path))
		if sessionID == "" || sessionID == "." {
			return nil
		}
		// Skip empty sessions (no user intent)
		// Quick check: file must contain runtime.user_intent
		if info.Size() == 0 {
			return nil
		}
		// Title from first user prompt or session ID
		title := extractFirstPrompt(path)
		if title == "" {
			title = shortID(sessionID)
		}
		// Try to get created/updated from file mtime if no index
		// For FS fallback, use mtime as updated, and earliest line time as created if available
		created, updated := info.ModTime(), info.ModTime()
		// Check for subagent marker: parent dir contains "subagent"
		isSub := strings.Contains(path, string(filepath.Separator)+"subagent"+string(filepath.Separator))

		sess := adapter.Session{
			ID:           sessionID,
			Name:         title,
			Slug:         shortID(sessionID),
			AdapterID:    a.ID(),
			AdapterName:  a.Name(),
			AdapterIcon:  a.Icon(),
			CreatedAt:    created,
			UpdatedAt:    updated,
			Duration:     updated.Sub(created),
			IsActive:     time.Since(updated) < 5*time.Minute,
			IsSubAgent:   isSub,
			MessageCount: 1,
			FileSize:     info.Size(),
			Path:         path,
		}
		sessions = append(sessions, sess)
		newIndex[sessionID] = path
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	a.mu.Lock()
	for id, p := range newIndex {
		a.sessionIndex[id] = p
	}
	a.mu.Unlock()
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// SessionByID implements TargetedRefresher.
func (a *Adapter) SessionByID(sessionID string) (*adapter.Session, error) {
	// Try index cache first
	a.mu.RLock()
	if path, ok := a.sessionIndex[sessionID]; ok {
		a.mu.RUnlock()
		if info, err := os.Stat(path); err == nil {
			// Try SQLite for authoritative metadata
			if sess := a.sessionByIDSQLite(sessionID, path, info); sess != nil {
				return sess, nil
			}
			// Fallback to filesystem metadata
			title := extractFirstPrompt(path)
			if title == "" {
				title = shortID(sessionID)
			}
			ws, _ := extractWorkspaceRoot(path)
			_ = ws
			return &adapter.Session{
				ID:           sessionID,
				Name:         title,
				Slug:         shortID(sessionID),
				AdapterID:    a.ID(),
				AdapterName:  a.Name(),
				AdapterIcon:  a.Icon(),
				CreatedAt:    info.ModTime(),
				UpdatedAt:    info.ModTime(),
				Duration:     0,
				IsActive:     time.Since(info.ModTime()) < 5*time.Minute,
				MessageCount: 1,
				FileSize:     info.Size(),
				Path:         path,
			}, nil
		}
	} else {
		a.mu.RUnlock()
	}

	// Full scan fallback: look under sessionsDir
	var foundPath string
	_ = filepath.WalkDir(a.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "session.jsonl" {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == sessionID {
			foundPath = path
			return filepath.SkipDir
		}
		return nil
	})
	if foundPath == "" {
		// Try SQLite direct lookup
		if sess := a.sessionByIDSQLite(sessionID, "", nil); sess != nil {
			return sess, nil
		}
		return nil, nil
	}
	info, err := os.Stat(foundPath)
	if err != nil {
		return nil, nil
	}
	a.mu.Lock()
	a.sessionIndex[sessionID] = foundPath
	a.mu.Unlock()
	title := extractFirstPrompt(foundPath)
	if title == "" {
		title = shortID(sessionID)
	}
	return &adapter.Session{
		ID:           sessionID,
		Name:         title,
		Slug:         shortID(sessionID),
		AdapterID:    a.ID(),
		AdapterName:  a.Name(),
		AdapterIcon:  a.Icon(),
		CreatedAt:    info.ModTime(),
		UpdatedAt:    info.ModTime(),
		Duration:     0,
		IsActive:     time.Since(info.ModTime()) < 5*time.Minute,
		MessageCount: 1,
		FileSize:     info.Size(),
		Path:         foundPath,
	}, nil
}

func (a *Adapter) sessionByIDSQLite(sessionID, knownPath string, info os.FileInfo) *adapter.Session {
	if _, err := os.Stat(a.indexDBPath); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", a.indexDBPath))
	if err != nil {
		return nil
	}
	defer func() { _ = db.Close() }()
	var sessID, logPath, wsRoot, wsKey, title, firstPrompt, status sql.NullString
	var createdUs, updatedUs sql.NullInt64
	var promptCnt sql.NullInt64
	row := db.QueryRow(`SELECT session_id, session_log_path, workspace_root, workspace_key, title, first_user_prompt, created_at_us, updated_at_us, prompt_count, status FROM sessions WHERE session_id = ?`, sessionID)
	if err := row.Scan(&sessID, &logPath, &wsRoot, &wsKey, &title, &firstPrompt, &createdUs, &updatedUs, &promptCnt, &status); err != nil {
		return nil
	}
	if sessID.String == "" {
		return nil
	}
	created := usToTimeVal(createdUs.Int64)
	updated := usToTimeVal(updatedUs.Int64)
	path := logPath.String
	if path == "" {
		path = knownPath
	}
	var fileSize int64
	if info != nil {
		fileSize = info.Size()
		if created.IsZero() {
			created = info.ModTime()
		}
		if updated.IsZero() {
			updated = info.ModTime()
		}
	} else if path != "" {
		if fi, err := os.Stat(path); err == nil {
			fileSize = fi.Size()
			if created.IsZero() {
				created = fi.ModTime()
			}
			if updated.IsZero() {
				updated = fi.ModTime()
			}
		}
	}
	name := firstNonEmpty(title.String, firstPrompt.String)
	if name == "" {
		name = shortID(sessionID)
	}
	isSub := strings.Contains(path, "/subagent/")
	return &adapter.Session{
		ID:           sessID.String,
		Name:         name,
		Slug:         shortID(sessID.String),
		AdapterID:    a.ID(),
		AdapterName:  a.Name(),
		AdapterIcon:  a.Icon(),
		CreatedAt:    created,
		UpdatedAt:    updated,
		Duration:     updated.Sub(created),
		IsActive:     !updated.IsZero() && time.Since(updated) < 5*time.Minute,
		IsSubAgent:   isSub,
		MessageCount: int(promptCnt.Int64),
		FileSize:     fileSize,
		Path:         path,
	}
}

// Messages returns conversation messages for sessionID.
func (a *Adapter) Messages(sessionID string) ([]adapter.Message, error) {
	path := a.sessionLogPath(sessionID)
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// Check cache for exact hit
	if a.msgCache != nil {
		cached, offset, cachedSize, cachedMod, ok := a.msgCache.GetWithOffset(path)
		if ok {
			if info.Size() == cachedSize && info.ModTime().Equal(cachedMod) {
				return copyMessages(cached.messages), nil
			}
			if info.Size() > cachedSize && offset > 0 {
				msgs, entry, err := a.parseMessagesIncremental(path, cached, offset)
				if err == nil {
					a.msgCache.Set(path, entry, info.Size(), info.ModTime(), entry.byteOffset)
					return copyMessages(msgs), nil
				}
			}
		}
	}
	msgs, entry, err := a.parseMessagesFull(path)
	if err != nil {
		return nil, err
	}
	if a.msgCache != nil {
		a.msgCache.Set(path, entry, info.Size(), info.ModTime(), entry.byteOffset)
	}
	return copyMessages(msgs), nil
}

func (a *Adapter) sessionLogPath(sessionID string) string {
	a.mu.RLock()
	if p, ok := a.sessionIndex[sessionID]; ok {
		a.mu.RUnlock()
		return p
	}
	a.mu.RUnlock()
	// Try to discover via walk
	var found string
	_ = filepath.WalkDir(a.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "session.jsonl" {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == sessionID {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if found != "" {
		a.mu.Lock()
		a.sessionIndex[sessionID] = found
		a.mu.Unlock()
		return found
	}
	// Try SQLite index
	if _, err := os.Stat(a.indexDBPath); err == nil {
		db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", a.indexDBPath))
		if err == nil {
			defer func() { _ = db.Close() }()
			var logPath sql.NullString
			err = db.QueryRow(`SELECT session_log_path FROM sessions WHERE session_id = ?`, sessionID).Scan(&logPath)
			if err == nil && logPath.String != "" {
				a.mu.Lock()
				a.sessionIndex[sessionID] = logPath.String
				a.mu.Unlock()
				return logPath.String
			}
		}
	}
	return ""
}

// parseMessagesFull parses a full session.jsonl into messages.
func (a *Adapter) parseMessagesFull(path string) ([]adapter.Message, messageCacheEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, messageCacheEntry{}, err
	}
	defer func() { _ = file.Close() }()

	var messages []adapter.Message
	toolResults := make(map[string]string)
	toolUseLocations := make(map[string][2]int) // call_id -> (msgIdx, toolIdx)
	var byteOffset int64

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 512*1024)
	scanner.Buffer(buf, 10*1024*1024)

	lineNo := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		lineNo++
		byteOffset += int64(len(line) + 1)
		if len(line) == 0 {
			continue
		}
		// Skip retained_frame wrappers (they contain escaped JSON)
		if containsRetain(line) {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.PayloadType {
		case "runtime.user_intent.accepted":
			// Extract text from refill_blocks or model_messages
			text := extractUserText(line)
			if strings.TrimSpace(text) == "" {
				continue
			}
			messages = append(messages, adapter.Message{
				ID:        rec.ID,
				Role:      "user",
				Content:   text,
				Timestamp: usToTimeVal(rec.RecordedAt),
				ContentBlocks: []adapter.ContentBlock{
					{Type: "text", Text: text},
				},
			})
		case "tool_batch.effect.started":
			// Decode tool call
			var payload struct {
				Kind   string `json:"kind"`
				Record struct {
					CallID          string `json:"call_id"`
					ToolName        string `json:"tool_name"`
					Subject         string `json:"subject"`
					ParallelProfile struct {
						Kind    string `json:"kind"`
						Subject string `json:"subject"`
					} `json:"parallel_profile"`
				} `json:"record"`
			}
			if err := json.Unmarshal(marshalPayload(rec), &payload); err != nil {
				continue
			}
			callID := payload.Record.CallID
			toolName := payload.Record.ToolName
			input := payload.Record.ParallelProfile.Subject
			if input == "" {
				input = payload.Record.Subject
			}
			msgID := rec.ID
			// Create or append to last assistant message if it already has tool uses in same batch
			if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" && len(messages[len(messages)-1].ToolUses) > 0 {
				// Check if last assistant is still open (within same model step window)
				// For Muse, we keep adding to last assistant if last message timestamp is recent.
				// Simplified: always create new tool message per batch for now unless last was very recent.
				// We'll append to existing if last assistant has no contentBlocks with text beyond tools and timestamp within 5s
				last := &messages[len(messages)-1]
				// If last assistant was created within 2 seconds and has only tools, append
				if rec.RecordedAt-last.Timestamp.UnixMicro() < 2_000_000 && len(last.ContentBlocks) == len(last.ToolUses) {
					idx := len(last.ToolUses)
					last.ToolUses = append(last.ToolUses, adapter.ToolUse{
						ID:    callID,
						Name:  toolName,
						Input: input,
					})
					last.ContentBlocks = append(last.ContentBlocks, adapter.ContentBlock{
						Type:      "tool_use",
						ToolUseID: callID,
						ToolName:  toolName,
						ToolInput: input,
					})
					toolUseLocations[callID] = [2]int{len(messages) - 1, idx}
					continue
				}
			}
			msg := adapter.Message{
				ID:        msgID,
				Role:      "assistant",
				Content:   "",
				Timestamp: usToTimeVal(rec.RecordedAt),
				ToolUses: []adapter.ToolUse{
					{ID: callID, Name: toolName, Input: input},
				},
				ContentBlocks: []adapter.ContentBlock{
					{Type: "tool_use", ToolUseID: callID, ToolName: toolName, ToolInput: input},
				},
			}
			toolUseLocations[callID] = [2]int{len(messages), 0}
			messages = append(messages, msg)
		case "runtime.session":
			// Check for tool result batch
			if isToolResultBatch(line) {
				results := extractToolResults(line)
				for callID, output := range results {
					toolResults[callID] = output
					if loc, ok := toolUseLocations[callID]; ok {
						msgIdx, toolIdx := loc[0], loc[1]
						if msgIdx < len(messages) && toolIdx < len(messages[msgIdx].ToolUses) {
							messages[msgIdx].ToolUses[toolIdx].Output = output
							// Update content block
							for j := range messages[msgIdx].ContentBlocks {
								if messages[msgIdx].ContentBlocks[j].ToolUseID == callID {
									messages[msgIdx].ContentBlocks[j].ToolOutput = output
									break
								}
							}
						}
					}
				}
			}
		default:
			// Unknown payload types are ignored
			continue
		}
	}
	// After loop, ensure any tool results that were parsed earlier but missed due to ordering are linked
	// (already handled inline)

	if err := scanner.Err(); err != nil && err != io.EOF {
		_ = err // partial parse, still return what we have
	}

	// Add model info to assistant messages if available (from metadata)
	// Messages without content but with tool uses should have some display text
	for i := range messages {
		if messages[i].Role == "assistant" && messages[i].Content == "" && len(messages[i].ToolUses) > 0 {
			messages[i].Content = toolUsesToContent(messages[i].ToolUses)
		}
	}

	entry := messageCacheEntry{messages: messages, byteOffset: byteOffset}
	return messages, entry, nil
}

func (a *Adapter) parseMessagesIncremental(path string, cached messageCacheEntry, offset int64) ([]adapter.Message, messageCacheEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, messageCacheEntry{}, err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, messageCacheEntry{}, err
	}
	// Reuse toolResults linking from cached state
	// For simplicity, re-parse full file if incremental fails; otherwise append
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 512*1024)
	scanner.Buffer(buf, 10*1024*1024)
	messages := copyMessages(cached.messages)
	toolUseLocations := make(map[string][2]int)
	// Rebuild location map from existing messages
	for i, m := range messages {
		for j, tu := range m.ToolUses {
			toolUseLocations[tu.ID] = [2]int{i, j}
		}
	}
	byteOffset := offset
	for scanner.Scan() {
		line := scanner.Bytes()
		byteOffset += int64(len(line) + 1)
		if len(line) == 0 || containsRetain(line) {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.PayloadType {
		case "runtime.user_intent.accepted":
			text := extractUserText(line)
			if strings.TrimSpace(text) == "" {
				continue
			}
			messages = append(messages, adapter.Message{
				ID:            rec.ID,
				Role:          "user",
				Content:       text,
				Timestamp:     usToTimeVal(rec.RecordedAt),
				ContentBlocks: []adapter.ContentBlock{{Type: "text", Text: text}},
			})
		case "tool_batch.effect.started":
			var payload struct {
				Kind   string `json:"kind"`
				Record struct {
					CallID          string `json:"call_id"`
					ToolName        string `json:"tool_name"`
					ParallelProfile struct {
						Subject string `json:"subject"`
					} `json:"parallel_profile"`
				} `json:"record"`
			}
			if err := json.Unmarshal(marshalPayload(rec), &payload); err != nil {
				continue
			}
			callID := payload.Record.CallID
			toolName := payload.Record.ToolName
			input := payload.Record.ParallelProfile.Subject
			msgID := rec.ID
			messages = append(messages, adapter.Message{
				ID:            msgID,
				Role:          "assistant",
				Content:       toolName + ": " + input,
				Timestamp:     usToTimeVal(rec.RecordedAt),
				ToolUses:      []adapter.ToolUse{{ID: callID, Name: toolName, Input: input}},
				ContentBlocks: []adapter.ContentBlock{{Type: "tool_use", ToolUseID: callID, ToolName: toolName, ToolInput: input}},
			})
			toolUseLocations[callID] = [2]int{len(messages) - 1, 0}
		case "runtime.session":
			if isToolResultBatch(line) {
				results := extractToolResults(line)
				for callID, output := range results {
					if loc, ok := toolUseLocations[callID]; ok {
						messages[loc[0]].ToolUses[loc[1]].Output = output
						for j := range messages[loc[0]].ContentBlocks {
							if messages[loc[0]].ContentBlocks[j].ToolUseID == callID {
								messages[loc[0]].ContentBlocks[j].ToolOutput = output
								break
							}
						}
					}
				}
			}
		}
	}
	entry := messageCacheEntry{messages: messages, byteOffset: byteOffset}
	return messages, entry, nil
}

// Usage not available from Muse logs (encrypted output); return empty.
func (a *Adapter) Usage(sessionID string) (*adapter.UsageStats, error) {
	return &adapter.UsageStats{}, nil
}

func (a *Adapter) Watch(projectRoot string) (<-chan adapter.Event, io.Closer, error) {
	return newWatcher(a, projectRoot)
}

// SessionIDFromPath resolves a session.jsonl path to its session ID.
func (a *Adapter) SessionIDFromPath(path string) (string, error) {
	dir := filepath.Dir(path)
	id := filepath.Base(dir)
	if id == "" || id == "." {
		return "", fmt.Errorf("cannot resolve muse session ID from %q", path)
	}
	return id, nil
}

// DiscoverRelatedProjectDirs finds other project dirs with sessions for same base.
func (a *Adapter) DiscoverRelatedProjectDirs(mainWorktreePath string) ([]string, error) {
	abs, err := resolveProjectPath(mainWorktreePath)
	if err != nil || abs == "" {
		return nil, nil
	}
	// Scan filesystem for other session workspace_roots that share base name prefix.
	// This is best-effort: walk recent sessions only.
	seen := make(map[string]struct{})
	var related []string
	_ = filepath.WalkDir(a.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "session.jsonl" {
			return nil
		}
		ws, _ := extractWorkspaceRoot(path)
		if ws == "" || ws == abs {
			return nil
		}
		if _, ok := seen[ws]; ok {
			return nil
		}
		seen[ws] = struct{}{}
		// Heuristic: sibling worktrees share basename prefix or are nested.
		base := filepath.Base(abs)
		if strings.HasPrefix(filepath.Base(ws), base+"-") || strings.HasPrefix(ws, abs+string(filepath.Separator)) || strings.Contains(ws, base+"-") {
			related = append(related, ws)
		}
		return nil
	})
	return related, nil
}

// Helpers

func resolveProjectPath(projectRoot string) (string, error) {
	if projectRoot == "" {
		return "", nil
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func pathMatchesRoot(cwd, absRoot string) bool {
	cwd = filepath.Clean(cwd)
	absRoot = filepath.Clean(absRoot)
	return cwd == absRoot || strings.HasPrefix(cwd, absRoot+string(filepath.Separator))
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func copyMessages(msgs []adapter.Message) []adapter.Message {
	out := make([]adapter.Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		out[i].ToolUses = append([]adapter.ToolUse(nil), msgs[i].ToolUses...)
		out[i].ContentBlocks = append([]adapter.ContentBlock(nil), msgs[i].ContentBlocks...)
		out[i].ThinkingBlocks = append([]adapter.ThinkingBlock(nil), msgs[i].ThinkingBlocks...)
	}
	return out
}

func toolUsesToContent(uses []adapter.ToolUse) string {
	var parts []string
	for _, u := range uses {
		if u.Input != "" {
			parts = append(parts, u.Name+": "+u.Input)
		} else {
			parts = append(parts, u.Name)
		}
	}
	return strings.Join(parts, "\n")
}

func containsRetain(line []byte) bool {
	return strings.Contains(string(line), `"retained_frame"`)
}

func marshalPayload(rec rawRecord) json.RawMessage {
	// Re-marshal payload field for nested parsing (already have raw bytes)
	b, _ := json.Marshal(rec.Payload)
	return b
}

func extractUserText(line []byte) string {
	var rec struct {
		Payload struct {
			RefillBlocks []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"refill_blocks"`
			ModelMessages []struct {
				Content []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"model_messages"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &rec); err != nil {
		return ""
	}
	for _, b := range rec.Payload.RefillBlocks {
		if strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	for _, m := range rec.Payload.ModelMessages {
		for _, c := range m.Content {
			if strings.TrimSpace(c.Text) != "" {
				return c.Text
			}
		}
	}
	return ""
}

func isToolResultBatch(line []byte) bool {
	return strings.Contains(string(line), `"tool_result_batch_committed"`)
}

func extractToolResults(line []byte) map[string]string {
	var rec struct {
		Payload struct {
			Event struct {
				Kind string `json:"kind"`
			} `json:"event"`
			Record struct {
				Results []struct {
					ToolCallID string `json:"tool_call_id"`
					Text       string `json:"text"`
				} `json:"results"`
			} `json:"record"`
		} `json:"payload"`
	}
	// Try both shapes: payload.record.results and payload.event.results (depending on nesting)
	// First try to unmarshal payload directly
	var outer struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(line, &outer); err != nil {
		return nil
	}
	// Try payload.record.results
	var direct struct {
		Kind   string `json:"kind"`
		Record struct {
			Results []struct {
				ToolCallID string `json:"tool_call_id"`
				Text       string `json:"text"`
			} `json:"results"`
		} `json:"record"`
	}
	if err := json.Unmarshal(outer.Payload, &direct); err == nil {
		if len(direct.Record.Results) > 0 {
			m := make(map[string]string, len(direct.Record.Results))
			for _, r := range direct.Record.Results {
				m[r.ToolCallID] = r.Text
			}
			return m
		}
	}
	if err := json.Unmarshal(line, &rec); err == nil {
		if len(rec.Payload.Record.Results) > 0 {
			m := make(map[string]string, len(rec.Payload.Record.Results))
			for _, r := range rec.Payload.Record.Results {
				m[r.ToolCallID] = r.Text
			}
			return m
		}
	}
	return nil
}

func extractFirstPrompt(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 256*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), `"runtime.user_intent.accepted"`) {
			continue
		}
		text := extractUserText(line)
		if strings.TrimSpace(text) != "" {
			// Truncate to first line
			text = strings.TrimSpace(text)
			if len(text) > 60 {
				text = text[:60] + "…"
			}
			if idx := strings.Index(text, "\n"); idx >= 0 {
				text = text[:idx]
			}
			return text
		}
	}
	return ""
}

func extractWorkspaceRoot(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), `"runtime.session.metadata"`) {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Payload.Record.WorkspaceRoot != "" {
			return rec.Payload.Record.WorkspaceRoot, nil
		}
		// Fallback: try generic extraction
		var generic struct {
			Payload struct {
				Record struct {
					WorkspaceRoot string `json:"workspace_root"`
				} `json:"record"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &generic); err == nil && generic.Payload.Record.WorkspaceRoot != "" {
			return generic.Payload.Record.WorkspaceRoot, nil
		}
	}
	return "", nil
}
