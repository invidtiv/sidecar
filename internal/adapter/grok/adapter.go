package grok

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/adapter/cache"
)

const (
	adapterID   = "grok"
	adapterName = "Grok"
	adapterIcon = "✦"

	metaCacheMaxEntries = 2048
	msgCacheMaxEntries  = 128
)

// messageCacheEntry holds cached messages with incremental parse state.
type messageCacheEntry struct {
	messages    []adapter.Message
	toolResults map[string]string
	toolUseLocs map[string][2]int // toolCallID -> (msgIdx, toolIdx)
	byteOffset  int64
}

// metaCacheEntry caches summary.json metadata.
type metaCacheEntry struct {
	summary    Summary
	modTime    time.Time
	size       int64
	lastAccess time.Time
}

// Adapter implements adapter.Adapter for Grok Build sessions.
type Adapter struct {
	// sessionsDir is typically ~/.grok/sessions (or $GROK_HOME/sessions).
	sessionsDir  string
	sessionIndex map[string]string // sessionID -> session directory
	metaCache    map[string]metaCacheEntry
	msgCache     *cache.Cache[messageCacheEntry]
	mu           sync.RWMutex
	metaMu       sync.RWMutex
}

// New creates a Grok adapter using GROK_HOME or ~/.grok.
func New() *Adapter {
	home := os.Getenv("GROK_HOME")
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".grok")
	}
	return NewWithSessionsDir(filepath.Join(home, "sessions"))
}

// NewWithSessionsDir creates an adapter rooted at sessionsDir (for tests).
func NewWithSessionsDir(sessionsDir string) *Adapter {
	return &Adapter{
		sessionsDir:  sessionsDir,
		sessionIndex: make(map[string]string),
		metaCache:    make(map[string]metaCacheEntry),
		msgCache:     cache.New[messageCacheEntry](msgCacheMaxEntries),
	}
}

// ID returns the adapter identifier.
func (a *Adapter) ID() string { return adapterID }

// Name returns the human-readable adapter name.
func (a *Adapter) Name() string { return adapterName }

// Icon returns the adapter icon for badge display.
func (a *Adapter) Icon() string { return adapterIcon }

// Detect reports whether any Grok sessions exist for projectRoot.
func (a *Adapter) Detect(projectRoot string) (bool, error) {
	dir := a.projectDirPath(projectRoot)
	if dir == "" {
		return false, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			// A session directory with summary or chat history counts.
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "summary.json")); err == nil {
				return true, nil
			}
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "chat_history.jsonl")); err == nil {
				return true, nil
			}
		}
	}
	return false, nil
}

// Capabilities returns supported features.
func (a *Adapter) Capabilities() adapter.CapabilitySet {
	return adapter.CapabilitySet{
		adapter.CapSessions: true,
		adapter.CapMessages: true,
		adapter.CapUsage:    false, // Grok chat_history does not expose per-message tokens
		adapter.CapWatch:    true,
	}
}

// WatchScope is global: all projects live under a single sessions root.
func (a *Adapter) WatchScope() adapter.WatchScope {
	return adapter.WatchScopeGlobal
}

// WatchForProjectDiscovery keeps the watcher alive when the project has no
// sessions yet so the first session can appear without a restart.
func (a *Adapter) WatchForProjectDiscovery() bool { return true }

// Sessions returns project-scoped sessions sorted by UpdatedAt desc.
func (a *Adapter) Sessions(projectRoot string) ([]adapter.Session, error) {
	dir := a.projectDirPath(projectRoot)
	if dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read grok sessions: %w", err)
	}

	sessions := make([]adapter.Session, 0, len(entries))
	newIndex := make(map[string]string, len(entries))

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionID := e.Name()
		sessionDir := filepath.Join(dir, sessionID)

		sum, err := a.readSummaryCached(filepath.Join(sessionDir, "summary.json"))
		if err != nil {
			// Fall back to directory-only discovery when summary is missing/corrupt.
			sum = Summary{}
			sum.Info.ID = sessionID
		}
		if sum.Info.ID == "" {
			sum.Info.ID = sessionID
		}

		// Prefer durable ID from summary when present (must match directory name in practice).
		id := sum.Info.ID
		if id == "" {
			id = sessionID
		}

		chatPath := filepath.Join(sessionDir, "chat_history.jsonl")
		var fileSize int64
		var chatMod time.Time
		if info, err := os.Stat(chatPath); err == nil {
			fileSize = info.Size()
			chatMod = info.ModTime()
		} else {
			// No chat history — skip empty shells
			if sum.NumChatMessages == 0 && sum.NumMessages == 0 {
				continue
			}
		}

		updated := sum.UpdatedAt
		if !sum.LastActiveAt.IsZero() && sum.LastActiveAt.After(updated) {
			updated = sum.LastActiveAt
		}
		if updated.IsZero() && !chatMod.IsZero() {
			updated = chatMod
		}
		created := sum.CreatedAt
		if created.IsZero() {
			created = updated
		}

		name := firstNonEmpty(sum.GeneratedTitle, sum.SessionSummary)
		if name == "" {
			name = shortID(id)
		}

		msgCount := sum.NumChatMessages
		if msgCount == 0 {
			msgCount = sum.NumMessages
		}
		// Prefer live count from cache when available.
		if cached := a.cachedMessageCount(chatPath); cached > 0 {
			msgCount = cached
		}

		// Skip truly empty sessions (no chat file / zero messages).
		if msgCount == 0 && fileSize == 0 {
			continue
		}

		isSub := sum.ParentSessionID != "" ||
			sum.SessionKind == "subagent" ||
			sum.SessionKind == "subagent_resume"

		sess := adapter.Session{
			ID:           id,
			Name:         name,
			Slug:         shortID(id),
			AdapterID:    a.ID(),
			AdapterName:  a.Name(),
			AdapterIcon:  a.Icon(),
			CreatedAt:    created,
			UpdatedAt:    updated,
			Duration:     updated.Sub(created),
			IsActive:     !updated.IsZero() && time.Since(updated) < 5*time.Minute,
			IsSubAgent:   isSub,
			MessageCount: msgCount,
			FileSize:     fileSize,
			Path:         chatPath,
		}
		sessions = append(sessions, sess)
		newIndex[id] = sessionDir
	}

	a.mu.Lock()
	// Merge index for this project's sessions; keep entries for other projects.
	for id, path := range newIndex {
		a.sessionIndex[id] = path
	}
	a.mu.Unlock()

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// SessionByID implements TargetedRefresher.
func (a *Adapter) SessionByID(sessionID string) (*adapter.Session, error) {
	sessionDir := a.sessionDirPath(sessionID)
	if sessionDir == "" {
		return nil, nil
	}

	sum, err := a.readSummaryCached(filepath.Join(sessionDir, "summary.json"))
	if err != nil {
		return nil, nil
	}
	id := sum.Info.ID
	if id == "" {
		id = sessionID
	}

	chatPath := filepath.Join(sessionDir, "chat_history.jsonl")
	var fileSize int64
	if info, err := os.Stat(chatPath); err == nil {
		fileSize = info.Size()
	}

	updated := sum.UpdatedAt
	if !sum.LastActiveAt.IsZero() && sum.LastActiveAt.After(updated) {
		updated = sum.LastActiveAt
	}
	created := sum.CreatedAt
	if created.IsZero() {
		created = updated
	}
	name := firstNonEmpty(sum.GeneratedTitle, sum.SessionSummary)
	if name == "" {
		name = shortID(id)
	}
	msgCount := sum.NumChatMessages
	if msgCount == 0 {
		msgCount = sum.NumMessages
	}
	if cached := a.cachedMessageCount(chatPath); cached > 0 {
		msgCount = cached
	}
	isSub := sum.ParentSessionID != "" ||
		sum.SessionKind == "subagent" ||
		sum.SessionKind == "subagent_resume"

	s := adapter.Session{
		ID:           id,
		Name:         name,
		Slug:         shortID(id),
		AdapterID:    a.ID(),
		AdapterName:  a.Name(),
		AdapterIcon:  a.Icon(),
		CreatedAt:    created,
		UpdatedAt:    updated,
		Duration:     updated.Sub(created),
		IsActive:     !updated.IsZero() && time.Since(updated) < 5*time.Minute,
		IsSubAgent:   isSub,
		MessageCount: msgCount,
		FileSize:     fileSize,
		Path:         chatPath,
	}
	return &s, nil
}

// Messages returns conversation messages for sessionID.
func (a *Adapter) Messages(sessionID string) ([]adapter.Message, error) {
	sessionDir := a.sessionDirPath(sessionID)
	if sessionDir == "" {
		return nil, nil
	}

	chatPath := filepath.Join(sessionDir, "chat_history.jsonl")
	info, err := os.Stat(chatPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	if a.msgCache != nil {
		cached, offset, cachedSize, cachedModTime, ok := a.msgCache.GetWithOffset(chatPath)
		if ok {
			if info.Size() == cachedSize && info.ModTime().Equal(cachedModTime) {
				return copyMessages(cached.messages), nil
			}
			if info.Size() > cachedSize && offset > 0 {
				messages, entry, err := a.parseMessagesIncremental(chatPath, cached, offset)
				if err == nil {
					a.msgCache.Set(chatPath, entry, info.Size(), info.ModTime(), entry.byteOffset)
					return copyMessages(messages), nil
				}
			}
		}
	}

	messages, entry, err := a.parseMessagesFull(chatPath)
	if err != nil {
		return nil, err
	}
	if a.msgCache != nil {
		a.msgCache.Set(chatPath, entry, info.Size(), info.ModTime(), entry.byteOffset)
	}
	return copyMessages(messages), nil
}

// Usage is not available from Grok chat history; returns empty stats.
func (a *Adapter) Usage(sessionID string) (*adapter.UsageStats, error) {
	return &adapter.UsageStats{}, nil
}

// Watch watches the sessions root for changes, filtering to projectRoot.
func (a *Adapter) Watch(projectRoot string) (<-chan adapter.Event, io.Closer, error) {
	return newWatcher(a, projectRoot)
}

// DiscoverRelatedProjectDirs finds other Grok-encoded project dirs under the
// same sessions root that look like worktrees of mainWorktreePath.
func (a *Adapter) DiscoverRelatedProjectDirs(mainWorktreePath string) ([]string, error) {
	entries, err := os.ReadDir(a.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	mainAbs, err := resolveProjectPath(mainWorktreePath)
	if err != nil || mainAbs == "" {
		return nil, nil
	}
	mainAbs = strings.TrimRight(mainAbs, string(filepath.Separator))
	mainPrefix := mainAbs + string(filepath.Separator)

	var related []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		decoded, err := decodeProjectKey(e.Name())
		if err != nil || decoded == "" {
			continue
		}
		decoded = strings.TrimRight(decoded, string(filepath.Separator))
		if decoded == mainAbs {
			continue
		}
		// Worktree-ish: sibling path sharing basename prefix, or nested under main.
		if strings.HasPrefix(decoded, mainPrefix) ||
			strings.HasPrefix(filepath.Base(decoded), filepath.Base(mainAbs)+"-") ||
			strings.Contains(decoded, filepath.Base(mainAbs)+"-") {
			related = append(related, decoded)
		}
	}
	return related, nil
}

// SessionIDFromPath resolves a chat_history path to its session ID.
func (a *Adapter) SessionIDFromPath(path string) (string, error) {
	// .../sessions/<encoded>/<session-id>/chat_history.jsonl
	dir := filepath.Dir(path)
	id := filepath.Base(dir)
	if id == "" || id == "." || id == string(filepath.Separator) {
		return "", fmt.Errorf("cannot resolve grok session ID from %q", path)
	}
	return id, nil
}

// projectDirPath maps a project root to its encoded sessions subdirectory.
func (a *Adapter) projectDirPath(projectRoot string) string {
	abs, err := resolveProjectPath(projectRoot)
	if err != nil || abs == "" {
		return ""
	}
	return filepath.Join(a.sessionsDir, encodeProjectKey(abs))
}

// sessionDirPath looks up a session directory by ID.
func (a *Adapter) sessionDirPath(sessionID string) string {
	a.mu.RLock()
	if dir, ok := a.sessionIndex[sessionID]; ok {
		a.mu.RUnlock()
		return dir
	}
	a.mu.RUnlock()

	// Fallback: scan all project directories once.
	entries, err := os.ReadDir(a.sessionsDir)
	if err != nil {
		return ""
	}
	for _, proj := range entries {
		if !proj.IsDir() {
			continue
		}
		candidate := filepath.Join(a.sessionsDir, proj.Name(), sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			a.mu.Lock()
			a.sessionIndex[sessionID] = candidate
			a.mu.Unlock()
			return candidate
		}
	}
	return ""
}

func (a *Adapter) readSummaryCached(path string) (Summary, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Summary{}, err
	}

	a.metaMu.RLock()
	cached, ok := a.metaCache[path]
	a.metaMu.RUnlock()
	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		a.metaMu.Lock()
		cached.lastAccess = time.Now()
		a.metaCache[path] = cached
		a.metaMu.Unlock()
		return cached.summary, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, err
	}
	var sum Summary
	if err := json.Unmarshal(data, &sum); err != nil {
		return Summary{}, err
	}

	a.metaMu.Lock()
	a.metaCache[path] = metaCacheEntry{
		summary:    sum,
		modTime:    info.ModTime(),
		size:       info.Size(),
		lastAccess: time.Now(),
	}
	if len(a.metaCache) > metaCacheMaxEntries {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range a.metaCache {
			if oldestKey == "" || v.lastAccess.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.lastAccess
			}
		}
		delete(a.metaCache, oldestKey)
	}
	a.metaMu.Unlock()

	return sum, nil
}

func (a *Adapter) cachedMessageCount(chatPath string) int {
	if a.msgCache == nil {
		return 0
	}
	info, err := os.Stat(chatPath)
	if err != nil {
		return 0
	}
	cached, _, cachedSize, cachedModTime, ok := a.msgCache.GetWithOffset(chatPath)
	if !ok || info.Size() != cachedSize || !info.ModTime().Equal(cachedModTime) {
		return 0
	}
	return countDisplayMessages(cached.messages)
}

func countDisplayMessages(msgs []adapter.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == "user" || m.Role == "assistant" {
			n++
		}
	}
	return n
}

// parseMessagesFull parses an entire chat_history.jsonl file.
func (a *Adapter) parseMessagesFull(path string) ([]adapter.Message, messageCacheEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, messageCacheEntry{}, fmt.Errorf("open chat history: %w", err)
	}
	defer func() { _ = f.Close() }()

	var messages []adapter.Message
	toolResults := make(map[string]string)
	toolUseLocs := make(map[string][2]int)
	var pendingThinking []adapter.ThinkingBlock
	var bytesRead int64

	scanner, buf := cache.NewScanner(f)
	defer cache.PutScannerBuffer(buf)

	for scanner.Scan() {
		line := scanner.Bytes()
		bytesRead += int64(len(line)) + 1

		var chat ChatLine
		if err := json.Unmarshal(line, &chat); err != nil {
			continue
		}
		a.processChatLine(chat, &messages, toolResults, toolUseLocs, &pendingThinking)
	}
	if err := scanner.Err(); err != nil {
		return nil, messageCacheEntry{}, fmt.Errorf("read chat history: %w", err)
	}

	entry := messageCacheEntry{
		messages:    copyMessages(messages),
		toolResults: copyStringMap(toolResults),
		toolUseLocs: copyLocMap(toolUseLocs),
		byteOffset:  bytesRead,
	}
	return messages, entry, nil
}

// parseMessagesIncremental appends newly written lines.
func (a *Adapter) parseMessagesIncremental(path string, cached messageCacheEntry, startOffset int64) ([]adapter.Message, messageCacheEntry, error) {
	reader, err := cache.NewIncrementalReader(path, startOffset)
	if err != nil {
		return nil, messageCacheEntry{}, err
	}
	defer func() { _ = reader.Close() }()

	messages := copyMessages(cached.messages)
	toolResults := copyStringMap(cached.toolResults)
	toolUseLocs := copyLocMap(cached.toolUseLocs)
	var pendingThinking []adapter.ThinkingBlock

	for {
		line, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, messageCacheEntry{}, err
		}
		var chat ChatLine
		if err := json.Unmarshal(line, &chat); err != nil {
			continue
		}
		a.processChatLine(chat, &messages, toolResults, toolUseLocs, &pendingThinking)
	}

	entry := messageCacheEntry{
		messages:    copyMessages(messages),
		toolResults: copyStringMap(toolResults),
		toolUseLocs: copyLocMap(toolUseLocs),
		byteOffset:  reader.Offset(),
	}
	return messages, entry, nil
}

func (a *Adapter) processChatLine(
	chat ChatLine,
	messages *[]adapter.Message,
	toolResults map[string]string,
	toolUseLocs map[string][2]int,
	pendingThinking *[]adapter.ThinkingBlock,
) {
	switch chat.Type {
	case "system":
		// Skip system prompts — they are huge and not useful in conversation view.
		return

	case "user":
		raw := extractTextContent(chat.Content)
		text := cleanUserContent(raw)
		if text == "" {
			// Harness-only injections (system-reminder, user_info, …) — skip.
			return
		}
		*pendingThinking = nil
		*messages = append(*messages, adapter.Message{
			Role:    "user",
			Content: text,
			ContentBlocks: []adapter.ContentBlock{{
				Type: "text",
				Text: text,
			}},
		})

	case "reasoning":
		thinking := extractReasoningText(chat)
		if thinking == "" {
			return
		}
		*pendingThinking = append(*pendingThinking, adapter.ThinkingBlock{
			Content:    thinking,
			TokenCount: len(thinking) / 4,
		})

	case "assistant":
		text := extractTextContent(chat.Content)
		msg := adapter.Message{
			ID:    chat.ID,
			Role:  "assistant",
			Model: chat.ModelID,
		}
		if text != "" {
			msg.Content = text
			msg.ContentBlocks = append(msg.ContentBlocks, adapter.ContentBlock{
				Type: "text",
				Text: text,
			})
		}
		if len(*pendingThinking) > 0 {
			msg.ThinkingBlocks = append(msg.ThinkingBlocks, (*pendingThinking)...)
			for _, tb := range *pendingThinking {
				msg.ContentBlocks = append(msg.ContentBlocks, adapter.ContentBlock{
					Type:       "thinking",
					Text:       tb.Content,
					TokenCount: tb.TokenCount,
				})
			}
			*pendingThinking = nil
		}
		msgIdx := len(*messages)
		for _, tc := range chat.ToolCalls {
			if tc.ID == "" && tc.Name == "" {
				continue
			}
			input := tc.Arguments
			if input == "" {
				input = "{}"
			}
			output := toolResults[tc.ID]
			tu := adapter.ToolUse{
				ID:     tc.ID,
				Name:   tc.Name,
				Input:  input,
				Output: output,
			}
			msg.ToolUses = append(msg.ToolUses, tu)
			msg.ContentBlocks = append(msg.ContentBlocks, adapter.ContentBlock{
				Type:       "tool_use",
				ToolUseID:  tc.ID,
				ToolName:   tc.Name,
				ToolInput:  input,
				ToolOutput: output,
			})
			if tc.ID != "" {
				toolUseLocs[tc.ID] = [2]int{msgIdx, len(msg.ToolUses) - 1}
			}
		}
		*messages = append(*messages, msg)

	case "tool_result":
		if chat.ToolCallID == "" {
			return
		}
		result := extractTextContent(chat.Content)
		toolResults[chat.ToolCallID] = result
		if loc, ok := toolUseLocs[chat.ToolCallID]; ok {
			msgIdx, toolIdx := loc[0], loc[1]
			if msgIdx < len(*messages) && toolIdx < len((*messages)[msgIdx].ToolUses) {
				(*messages)[msgIdx].ToolUses[toolIdx].Output = result
				// Update matching content block
				for i := range (*messages)[msgIdx].ContentBlocks {
					cb := &(*messages)[msgIdx].ContentBlocks[i]
					if cb.Type == "tool_use" && cb.ToolUseID == chat.ToolCallID {
						cb.ToolOutput = result
					}
				}
			}
		} else {
			// Orphan result: emit as standalone tool_result message block
			*messages = append(*messages, adapter.Message{
				Role:    "tool",
				Content: result,
				ContentBlocks: []adapter.ContentBlock{{
					Type:       "tool_result",
					ToolUseID:  chat.ToolCallID,
					ToolOutput: result,
				}},
			})
		}
	}
}

// extractTextContent turns chat content (string or [{type,text},...]) into text.
func extractTextContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// String
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Array of blocks
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if bl.Type == "text" || bl.Type == "" {
				b.WriteString(bl.Text)
			}
		}
		return b.String()
	}
	return ""
}

// cleanUserContent prefers <user_query> text and drops harness-only injections.
// Grok (like Claude/Cursor) wraps workspace context in XML tags that should not
// surface as user bubbles in the conversation view.
func cleanUserContent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if q := extractUserQuery(s); q != "" {
		return q
	}
	if isHarnessOnlyUserMessage(s) {
		return ""
	}
	// Strip residual XML tags from mixed plain/harness content.
	cleaned := xmlTagRegex.ReplaceAllString(s, " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return strings.TrimSpace(cleaned)
}

// extractUserQuery returns the body of the first <user_query>...</user_query> pair.
func extractUserQuery(s string) string {
	const open, close = "<user_query>", "</user_query>"
	start := strings.Index(s, open)
	if start < 0 {
		return ""
	}
	end := strings.Index(s[start+len(open):], close)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(s[start+len(open) : start+len(open)+end])
}

// isHarnessOnlyUserMessage reports messages that are only agent harness context
// (no real user text). These are common as synthetic user turns in Grok sessions.
func isHarnessOnlyUserMessage(s string) bool {
	hasHarness := strings.Contains(s, "<system-reminder>") ||
		strings.Contains(s, "<user_info>") ||
		strings.Contains(s, "<git_status>") ||
		strings.Contains(s, "<agent_skills>") ||
		strings.Contains(s, "<available_skills>") ||
		strings.Contains(s, "<mcp_servers>") ||
		strings.Contains(s, "<open_and_recently_viewed_files>") ||
		strings.Contains(s, "<project_layout>")
	if !hasHarness {
		return false
	}
	// If a user_query is present, not harness-only (caller already checked).
	if strings.Contains(s, "<user_query>") {
		return false
	}
	// Strip known harness blocks; if nothing meaningful remains, drop the message.
	stripped := s
	for _, tag := range []string{
		"system-reminder", "user_info", "git_status", "agent_skills",
		"available_skills", "mcp_servers", "open_and_recently_viewed_files",
		"project_layout", "environment_details", "attached_files",
	} {
		stripped = stripTaggedBlocks(stripped, tag)
	}
	stripped = xmlTagRegex.ReplaceAllString(stripped, " ")
	stripped = strings.Join(strings.Fields(stripped), " ")
	return strings.TrimSpace(stripped) == ""
}

func stripTaggedBlocks(s, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	for {
		start := strings.Index(s, open)
		if start < 0 {
			break
		}
		endRel := strings.Index(s[start+len(open):], close)
		if endRel < 0 {
			// Unclosed block — drop from open tag to end.
			return strings.TrimSpace(s[:start])
		}
		end := start + len(open) + endRel + len(close)
		s = s[:start] + s[end:]
	}
	return s
}

// xmlTagRegex strips residual XML-ish tags after block extraction.
var xmlTagRegex = regexp.MustCompile(`(?s)<[^>]+>`)

func extractReasoningText(chat ChatLine) string {
	var b strings.Builder
	for _, s := range chat.Summary {
		if s.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(s.Text)
		}
	}
	if b.Len() > 0 {
		return b.String()
	}
	// Fallback: content as text
	return extractTextContent(chat.Content)
}

// encodeProjectKey percent-encodes a path like Python quote(path, safe=”).
func encodeProjectKey(abs string) string {
	var b strings.Builder
	b.Grow(len(abs) * 3)
	for i := 0; i < len(abs); i++ {
		c := abs[i]
		if isUnreserved(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '~'
}

// decodeProjectKey reverses encodeProjectKey.
func decodeProjectKey(key string) (string, error) {
	var b strings.Builder
	b.Grow(len(key))
	for i := 0; i < len(key); {
		if key[i] == '%' && i+2 < len(key) {
			var v byte
			for j := 1; j <= 2; j++ {
				c := key[i+j]
				var n byte
				switch {
				case c >= '0' && c <= '9':
					n = c - '0'
				case c >= 'A' && c <= 'F':
					n = c - 'A' + 10
				case c >= 'a' && c <= 'f':
					n = c - 'a' + 10
				default:
					return "", fmt.Errorf("invalid percent encoding in %q", key)
				}
				v = v<<4 | n
			}
			b.WriteByte(v)
			i += 3
			continue
		}
		b.WriteByte(key[i])
		i++
	}
	return b.String(), nil
}

func resolveProjectPath(projectRoot string) (string, error) {
	if projectRoot == "" {
		return "", fmt.Errorf("empty project root")
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func copyMessages(msgs []adapter.Message) []adapter.Message {
	if msgs == nil {
		return nil
	}
	cp := make([]adapter.Message, len(msgs))
	for i, m := range msgs {
		cp[i] = m
		if m.ToolUses != nil {
			cp[i].ToolUses = make([]adapter.ToolUse, len(m.ToolUses))
			copy(cp[i].ToolUses, m.ToolUses)
		}
		if m.ThinkingBlocks != nil {
			cp[i].ThinkingBlocks = make([]adapter.ThinkingBlock, len(m.ThinkingBlocks))
			copy(cp[i].ThinkingBlocks, m.ThinkingBlocks)
		}
		if m.ContentBlocks != nil {
			cp[i].ContentBlocks = make([]adapter.ContentBlock, len(m.ContentBlocks))
			copy(cp[i].ContentBlocks, m.ContentBlocks)
		}
	}
	return cp
}

func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func copyLocMap(m map[string][2]int) map[string][2]int {
	if m == nil {
		return nil
	}
	cp := make(map[string][2]int, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
