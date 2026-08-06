package antigravity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
)

const (
	adapterID           = "antigravity"
	adapterName         = "Antigravity"
	metaCacheMaxEntries = 2048
)

// Adapter implements the adapter.Adapter interface for Antigravity CLI sessions.
type Adapter struct {
	brainDir     string
	sessionIndex map[string]string // sessionID -> transcript.jsonl path cache
	indexMu      sync.RWMutex
	metaCache    map[string]sessionMetaCacheEntry
	metaMu       sync.RWMutex
}

type sessionMetaCacheEntry struct {
	meta       *SessionMetadata
	modTime    time.Time
	size       int64
	lastAccess time.Time
}

// New creates a new Antigravity adapter.
func New() *Adapter {
	home, _ := os.UserHomeDir()
	return &Adapter{
		brainDir:     filepath.Join(home, ".gemini", "antigravity-cli", "brain"),
		sessionIndex: make(map[string]string),
		metaCache:    make(map[string]sessionMetaCacheEntry),
	}
}

// NewWithBrainDir creates an adapter with a custom brain directory (for testing).
func NewWithBrainDir(brainDir string) *Adapter {
	return &Adapter{
		brainDir:     brainDir,
		sessionIndex: make(map[string]string),
		metaCache:    make(map[string]sessionMetaCacheEntry),
	}
}

// ID returns the adapter identifier.
func (a *Adapter) ID() string { return adapterID }

// Name returns the human-readable adapter name.
func (a *Adapter) Name() string { return adapterName }

// Icon returns the adapter icon.
func (a *Adapter) Icon() string { return "★" }

// WatchScope indicates this adapter watches a global path.
func (a *Adapter) WatchScope() adapter.WatchScope {
	return adapter.WatchScopeGlobal
}

// Detect checks if Antigravity sessions exist.
func (a *Adapter) Detect(projectRoot string) (bool, error) {
	entries, err := os.ReadDir(a.brainDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		logPath := filepath.Join(a.brainDir, e.Name(), ".system_generated", "logs", "transcript.jsonl")
		if _, err := os.Stat(logPath); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// Capabilities returns the supported features.
func (a *Adapter) Capabilities() adapter.CapabilitySet {
	return adapter.CapabilitySet{
		adapter.CapSessions: true,
		adapter.CapMessages: true,
		adapter.CapUsage:    true,
		adapter.CapWatch:    true,
	}
}

// Sessions returns all Antigravity sessions sorted by update time.
func (a *Adapter) Sessions(projectRoot string) ([]adapter.Session, error) {
	entries, err := os.ReadDir(a.brainDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	sessions := make([]adapter.Session, 0, len(entries))

	a.indexMu.Lock()
	a.sessionIndex = make(map[string]string)
	a.indexMu.Unlock()

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionID := e.Name()
		logPath := filepath.Join(a.brainDir, sessionID, ".system_generated", "logs", "transcript.jsonl")

		info, err := os.Stat(logPath)
		if err != nil {
			continue
		}

		meta, err := a.sessionMetadata(sessionID, logPath, info)
		if err != nil {
			continue
		}

		a.indexMu.Lock()
		a.sessionIndex[sessionID] = logPath
		a.indexMu.Unlock()

		name := ""
		if meta.FirstUserMessage != "" {
			name = truncateTitle(cleanPromptContent(meta.FirstUserMessage), 50)
		}
		if name == "" {
			name = shortID(sessionID)
		}

		sessions = append(sessions, adapter.Session{
			ID:           sessionID,
			Name:         name,
			Slug:         shortID(sessionID),
			AdapterID:    adapterID,
			AdapterName:  adapterName,
			AdapterIcon:  a.Icon(),
			CreatedAt:    meta.StartTime,
			UpdatedAt:    meta.LastUpdated,
			Duration:     meta.LastUpdated.Sub(meta.StartTime),
			IsActive:     time.Since(meta.LastUpdated) < 5*time.Minute,
			TotalTokens:  meta.TotalTokens,
			MessageCount: meta.MessageCount,
			FileSize:     meta.FileSize,
			Path:         logPath,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

// SessionByID returns a single session by ID.
func (a *Adapter) SessionByID(sessionID string) (*adapter.Session, error) {
	logPath := filepath.Join(a.brainDir, sessionID, ".system_generated", "logs", "transcript.jsonl")
	info, err := os.Stat(logPath)
	if err != nil {
		return nil, err
	}

	meta, err := a.sessionMetadata(sessionID, logPath, info)
	if err != nil {
		return nil, err
	}

	name := ""
	if meta.FirstUserMessage != "" {
		name = truncateTitle(cleanPromptContent(meta.FirstUserMessage), 50)
	}
	if name == "" {
		name = shortID(sessionID)
	}

	return &adapter.Session{
		ID:           sessionID,
		Name:         name,
		Slug:         shortID(sessionID),
		AdapterID:    adapterID,
		AdapterName:  adapterName,
		AdapterIcon:  a.Icon(),
		CreatedAt:    meta.StartTime,
		UpdatedAt:    meta.LastUpdated,
		Duration:     meta.LastUpdated.Sub(meta.StartTime),
		IsActive:     time.Since(meta.LastUpdated) < 5*time.Minute,
		TotalTokens:  meta.TotalTokens,
		MessageCount: meta.MessageCount,
		FileSize:     meta.FileSize,
		Path:         logPath,
	}, nil
}

// Messages loads and parses message transcript for a session.
func (a *Adapter) Messages(sessionID string) ([]adapter.Message, error) {
	logPath := a.findSessionPath(sessionID)
	if logPath == "" {
		logPath = filepath.Join(a.brainDir, sessionID, ".system_generated", "logs", "transcript.jsonl")
	}

	file, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()

	var messages []adapter.Message
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry LogLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339, entry.CreatedAt)
		if ts.IsZero() {
			ts = time.Now()
		}

		switch entry.Type {
		case "USER_INPUT":
			content := cleanPromptContent(entry.Content)
			messages = append(messages, adapter.Message{
				ID:        fmt.Sprintf("%s-%d", sessionID, entry.StepIndex),
				Role:      "user",
				Timestamp: ts,
				Content:   content,
			})
		case "PLANNER_RESPONSE":
			msg := adapter.Message{
				ID:        fmt.Sprintf("%s-%d", sessionID, entry.StepIndex),
				Role:      "assistant",
				Timestamp: ts,
				Content:   entry.Content,
			}
			for _, tc := range entry.ToolCalls {
				name := tc.ToolName
				if name == "" {
					name = tc.Name
				}
				argsStr := ""
				if len(tc.Args) > 0 {
					b, _ := json.Marshal(tc.Args)
					argsStr = string(b)
				}
				msg.ContentBlocks = append(msg.ContentBlocks, adapter.ContentBlock{
					Type:     "tool_use",
					ToolName: name,
					Text:     argsStr,
				})
			}
			messages = append(messages, msg)
		}
	}

	return messages, nil
}

// Usage estimates usage metrics for a session.
func (a *Adapter) Usage(sessionID string) (*adapter.UsageStats, error) {
	msgs, err := a.Messages(sessionID)
	if err != nil {
		return nil, err
	}

	totalTokens := 0
	for _, m := range msgs {
		totalTokens += len(m.Content) / 4
	}

	return &adapter.UsageStats{
		TotalInputTokens:  totalTokens / 2,
		TotalOutputTokens: totalTokens / 2,
		MessageCount:      len(msgs),
	}, nil
}

func (a *Adapter) findSessionPath(sessionID string) string {
	a.indexMu.RLock()
	path, ok := a.sessionIndex[sessionID]
	a.indexMu.RUnlock()
	if ok {
		return path
	}
	return filepath.Join(a.brainDir, sessionID, ".system_generated", "logs", "transcript.jsonl")
}

func (a *Adapter) sessionMetadata(sessionID, path string, info os.FileInfo) (*SessionMetadata, error) {
	a.metaMu.RLock()
	cached, ok := a.metaCache[path]
	a.metaMu.RUnlock()

	if ok && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.meta, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	meta := &SessionMetadata{
		SessionID: sessionID,
		FileSize:  info.Size(),
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var firstTS, lastTS time.Time

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry LogLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339, entry.CreatedAt)
		if !ts.IsZero() {
			if firstTS.IsZero() || ts.Before(firstTS) {
				firstTS = ts
			}
			if ts.After(lastTS) {
				lastTS = ts
			}
		}

		meta.TotalTokens += len(entry.Content) / 4

		if entry.Type == "USER_INPUT" {
			meta.MessageCount++
			if meta.FirstUserMessage == "" {
				meta.FirstUserMessage = entry.Content
			}
		} else if entry.Type == "PLANNER_RESPONSE" {
			meta.MessageCount++
		}
	}

	if firstTS.IsZero() {
		firstTS = info.ModTime()
	}
	if lastTS.IsZero() {
		lastTS = info.ModTime()
	}

	meta.StartTime = firstTS
	meta.LastUpdated = lastTS

	a.metaMu.Lock()
	if len(a.metaCache) >= metaCacheMaxEntries {
		a.metaCache = make(map[string]sessionMetaCacheEntry)
	}
	a.metaCache[path] = sessionMetaCacheEntry{
		meta:       meta,
		modTime:    info.ModTime(),
		size:       info.Size(),
		lastAccess: time.Now(),
	}
	a.metaMu.Unlock()

	return meta, nil
}

func cleanPromptContent(s string) string {
	if idx := strings.Index(s, "<USER_REQUEST>"); idx != -1 {
		endIdx := strings.Index(s, "</USER_REQUEST>")
		if endIdx > idx {
			s = s[idx+len("<USER_REQUEST>") : endIdx]
		}
	}
	return strings.TrimSpace(s)
}

func truncateTitle(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
