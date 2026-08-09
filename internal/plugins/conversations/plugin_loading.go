package conversations

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/adapter/tieredwatcher"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/fdmonitor"
)

// Data loading and file watching methods

var adapterSlowLoadNoticeDelay = 5 * time.Second

// loadSessions loads sessions from the adapter.
// Queries sessions from all related worktree paths to show cross-worktree conversations.
// Sessions from deleted worktrees are marked with "(deleted)" in their worktree name.
// Caches worktree paths and names to avoid git commands on every refresh (td-e74a4aaa).
// Serialized to prevent concurrent goroutines from accumulating file descriptors (td-023577).
func (p *Plugin) loadSessions() tea.Cmd {
	// Capture epoch for stale detection on project switch
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}

	// Capture current cache state for goroutine (td-0e43c080: avoid race)
	cachedPaths := p.cachedWorktreePaths
	cachedNames := p.cachedWorktreeNames
	cacheTime := p.worktreeCacheTime
	adapters := p.adapters
	batchChan := p.adapterBatchChan

	// Handle nil context (e.g., in tests)
	var workDir string
	if p.ctx != nil {
		workDir = p.ctx.WorkDir
	}

	return func() tea.Msg {
		loadSeq, loadCtx, ok := p.beginWholeSessionLoad()
		if !ok {
			return nil
		}

		if len(adapters) == 0 {
			p.finishWholeSessionLoad(loadSeq)
			return SessionsLoadedMsg{Epoch: epoch}
		}

		// Check worktree cache (td-e74a4aaa)
		var worktreePaths []string
		var worktreeNames map[string]string
		var cacheUpdated bool
		cacheValid := cachedPaths != nil && time.Since(cacheTime) < worktreeCacheTTL

		if cacheValid {
			// Use cached data
			worktreePaths = cachedPaths
			worktreeNames = cachedNames
		} else {
			// Refresh cache - get all related worktree paths (main repo + all worktrees)
			worktreePaths = app.GetAllRelatedPaths(workDir)
			if len(worktreePaths) == 0 {
				// Not a git repo or no worktrees - just use current workdir
				worktreePaths = []string{workDir}
			}

			// Discover additional paths from adapters (finds deleted worktree conversations)
			mainPath := app.GetMainWorktreePath(workDir)
			if mainPath == "" {
				mainPath = workDir
			}
			pathSet := make(map[string]bool, len(worktreePaths))
			for _, path := range worktreePaths {
				pathSet[path] = true
			}
			for _, a := range adapters {
				if discoverer, ok := a.(adapter.ProjectDiscoverer); ok {
					discovered, _ := discoverer.DiscoverRelatedProjectDirs(mainPath)
					for _, path := range discovered {
						if !pathSet[path] {
							worktreePaths = append(worktreePaths, path)
							pathSet[path] = true
						}
					}
				}
			}

			// Compute worktree names
			worktreeNames = make(map[string]string)
			currentPath := workDir
			if absPath, err := filepath.Abs(currentPath); err == nil {
				currentPath = absPath
			}
			for _, wtPath := range worktreePaths {
				wtName := app.WorktreeNameForPath(workDir, wtPath)
				if wtName == "" && wtPath != currentPath {
					wtName = deriveWorktreeNameFromPath(wtPath, mainPath)
				}
				worktreeNames[wtPath] = wtName
			}

			// Mark cache as updated (td-0e43c080: Update() will store)
			cacheUpdated = true
		}

		// Get current working directory for worktree name comparison
		currentPath := workDir
		if absPath, err := filepath.Abs(currentPath); err == nil {
			currentPath = absPath
		}

		// Launch one worker per adapter. Paths for the same adapter are deliberately
		// serial: global adapters often share mutable indexes across Sessions calls.
		var wg sync.WaitGroup
		for id, a := range adapters {
			adapterID := id
			adpt := a
			wg.Add(1)
			go func() {
				defer wg.Done()

				var adapterSess []adapter.Session
				for _, wtPath := range worktreePaths {
					if loadCtx.Err() != nil {
						return
					}
					loadToken, ok := p.beginSessionLoad(adapterID, wtPath)
					if !ok {
						continue
					}
					path := wtPath
					timer := time.AfterFunc(adapterSlowLoadNoticeDelay, func() {
						p.sendAdapterBatch(loadCtx, batchChan, AdapterBatchMsg{
							Epoch:     epoch,
							AdapterID: adapterID,
							Notices:   []string{fmt.Sprintf("%s is still loading conversations from %s", adpt.Name(), path)},
						})
					})

					releaseCall, acquired := p.acquireSessionCall(loadCtx, adapterID)
					if !acquired {
						timer.Stop()
						p.endSessionLoad(adapterID, wtPath, loadToken)
						return
					}
					wtSessions, err := adpt.Sessions(wtPath)
					releaseCall()
					timer.Stop()
					p.endSessionLoad(adapterID, wtPath, loadToken)

					if err != nil {
						p.sendAdapterBatch(loadCtx, batchChan, AdapterBatchMsg{
							Epoch:     epoch,
							AdapterID: adapterID,
							Notices:   []string{fmt.Sprintf("%s conversations failed to load: %v", adpt.Name(), err)},
						})
						continue
					}
					if loadCtx.Err() != nil {
						return
					}

					wtName := worktreeNames[wtPath]
					for i := range wtSessions {
						if wtSessions[i].AdapterID == "" {
							wtSessions[i].AdapterID = adapterID
						}
						if wtSessions[i].AdapterName == "" {
							wtSessions[i].AdapterName = adpt.Name()
						}
						if wtSessions[i].AdapterIcon == "" {
							wtSessions[i].AdapterIcon = adpt.Icon()
						}
						absWtPath := wtPath
						if abs, err := filepath.Abs(wtPath); err == nil {
							absWtPath = abs
						}
						if absWtPath != currentPath {
							wtSessions[i].WorktreeName = wtName
							wtSessions[i].WorktreePath = absWtPath
						}
						adapterSess = append(adapterSess, wtSessions[i])
					}
				}
				// Mark sessions from deleted worktrees
				for i := range adapterSess {
					if adapterSess[i].WorktreePath != "" {
						if _, err := os.Stat(adapterSess[i].WorktreePath); os.IsNotExist(err) {
							adapterSess[i].WorktreeName = adapterSess[i].WorktreeName + " (deleted)"
						}
					}
				}
				p.sendAdapterBatch(loadCtx, batchChan, AdapterBatchMsg{
					Epoch:     epoch,
					AdapterID: adapterID,
					Sessions:  adapterSess,
				})
			}()
		}

		// Coordinator waits for real completion. A slow call remains visible instead
		// of being abandoned after five seconds while an orphan mutates the adapter.
		go func() {
			wg.Wait()
			fdmonitor.Check(nil)

			finalMsg := AdapterBatchMsg{Epoch: epoch, Final: true}
			if cacheUpdated {
				finalMsg.WorktreePaths = worktreePaths
				finalMsg.WorktreeNames = worktreeNames
			}
			p.sendAdapterBatch(loadCtx, batchChan, finalMsg)
			p.finishWholeSessionLoad(loadSeq)
		}()

		// Return immediately — adapter goroutines will send results to channel
		return LoadingStartedMsg{Epoch: epoch}
	}
}

// refreshSessions updates only specific sessions in-place (td-2b8ebe).
// Returns only the refreshed sessions as a delta to avoid overwriting concurrent
// session list updates from loadSessions/AdapterBatchMsg.
func (p *Plugin) refreshSessions(sessionIDs []string) tea.Cmd {
	// Capture epoch for stale detection on project switch
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}

	adapters := p.adapters
	refreshCtx := p.refreshCtx
	knownAdapters := make(map[string]string, len(p.sessions))
	for _, s := range p.sessions {
		knownAdapters[s.ID] = s.AdapterID
	}

	return func() tea.Msg {
		if len(adapters) == 0 || refreshCtx == nil || refreshCtx.Err() != nil {
			return nil
		}

		var refreshed []adapter.Session

		for _, sessionID := range sessionIDs {
			// Targeted refresh is only safe for a session already admitted by the
			// project-filtered Sessions load. Unknown IDs from global watchers must
			// take the full-load path instead of importing a foreign project by ID.
			knownAdapterID, known := knownAdapters[sessionID]
			if !known {
				continue
			}
			for adapterID, a := range adapters {
				if knownAdapterID != "" && adapterID != knownAdapterID {
					continue
				}
				if tr, ok := a.(adapter.TargetedRefresher); ok {
					releaseCall, acquired := p.tryAcquireSessionCall(refreshCtx, adapterID)
					if !acquired {
						continue
					}
					s, err := tr.SessionByID(sessionID)
					releaseCall()
					if refreshCtx.Err() != nil {
						return nil
					}
					if err == nil && s != nil && s.ID == sessionID &&
						(s.AdapterID == "" || knownAdapterID == "" || s.AdapterID == knownAdapterID) {
						refreshed = append(refreshed, *s)
						break
					}
				}
			}
		}

		if len(refreshed) == 0 {
			return nil // No changes; avoid overwriting concurrent session list updates
		}

		return SessionsRefreshedMsg{Epoch: epoch, Refreshed: refreshed}
	}
}

// globalEventNeedsProjectRefresh prevents a global adapter's event stream from
// admitting sessions belonging to another project. Known sessions were already
// filtered through Sessions(projectRoot); unknown IDs require that same full
// project-filtered load before targeted refresh becomes safe.
func (p *Plugin) globalEventNeedsProjectRefresh(adapterID, sessionID string) bool {
	if adapterID == "" || sessionID == "" {
		return false
	}
	a := p.adapters[adapterID]
	scope, ok := a.(adapter.WatchScopeProvider)
	if !ok || scope.WatchScope() != adapter.WatchScopeGlobal {
		return false
	}
	for _, s := range p.sessions {
		if s.ID == sessionID && (s.AdapterID == "" || s.AdapterID == adapterID) {
			return false
		}
	}
	return true
}

// loadMessages loads messages for a session with pagination support (td-313ea851).
func (p *Plugin) loadMessages(sessionID string) tea.Cmd {
	// Capture epoch for stale detection on project switch
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}

	offset := p.messageOffset
	return func() tea.Msg {
		if len(p.adapters) == 0 {
			return MessagesLoadedMsg{Epoch: epoch}
		}
		adapter := p.adapterForSession(sessionID)
		if adapter == nil {
			return MessagesLoadedMsg{Epoch: epoch}
		}
		messages, err := adapter.Messages(sessionID)
		if err != nil {
			return ErrorMsg{Err: err}
		}

		totalCount := len(messages)
		resultOffset := 0

		// Apply pagination: load a window of maxMessagesInMemory messages
		if len(messages) > maxMessagesInMemory {
			// offset indicates how many messages to skip from the end (most recent)
			// offset=0 means show the most recent messages
			// offset=100 means skip the 100 most recent and show older ones
			endIdx := len(messages) - offset
			if endIdx < 0 {
				endIdx = 0
			}
			startIdx := endIdx - maxMessagesInMemory
			if startIdx < 0 {
				startIdx = 0
			}
			resultOffset = startIdx
			messages = messages[startIdx:endIdx]
		}

		return MessagesLoadedMsg{
			Epoch:      epoch,
			SessionID:  sessionID,
			Messages:   messages,
			TotalCount: totalCount,
			Offset:     resultOffset,
		}
	}
}

// startWatcher starts watching for session changes.
// Uses tiered watching (td-dca6fe) to reduce FD count:
// - HOT tier: recently active sessions use real-time fsnotify
// - COLD tier: All other sessions use periodic polling (every 30s)
// File-based adapters (claudecode, codex, antigravity, opencode) use tiered watcher.
// Database adapters (cursor, warp) still use their own Watch() methods.
func (p *Plugin) startWatcher() tea.Cmd {
	var epoch uint64
	var workDir string
	if p.ctx != nil {
		epoch = p.ctx.Epoch
		workDir = p.ctx.WorkDir
	}
	adapters := p.adapters
	sessions := append([]adapter.Session(nil), p.sessions...)
	worktreePaths := append([]string(nil), p.cachedWorktreePaths...)
	return func() tea.Msg {
		if len(adapters) == 0 {
			return WatchStartedMsg{Epoch: epoch, Channel: nil}
		}

		// Create context for cancellation (td-eb2699b4)
		ctx, cancel := context.WithCancel(context.Background())

		// Get all related worktree paths (main repo + all worktrees)
		if len(worktreePaths) == 0 {
			worktreePaths = app.GetAllRelatedPaths(workDir)
			if len(worktreePaths) == 0 {
				worktreePaths = []string{workDir}
			}
		}

		// Create tiered watcher manager (td-dca6fe)
		manager := tieredwatcher.NewManager()

		merged := make(chan adapter.Event, 32)
		var wg sync.WaitGroup
		watchCount := 0
		var notices []string

		// Collect all file-based sessions for tiered watching (td-dca6fe)
		// Sessions with a Path field use the tiered watcher
		type adapterTieredConfig struct {
			sessions    []tieredwatcher.SessionInfo
			exts        map[string]bool
			activeCount int
		}
		adapterConfigs := make(map[string]*adapterTieredConfig)
		fileBasedAdapters := make(map[string]bool) // adapters with file paths

		for _, s := range sessions {
			if s.Path == "" || adapters[s.AdapterID] == nil {
				continue
			}
			adapterID := s.AdapterID
			fileBasedAdapters[adapterID] = true
			cfg := adapterConfigs[adapterID]
			if cfg == nil {
				cfg = &adapterTieredConfig{exts: make(map[string]bool)}
				adapterConfigs[adapterID] = cfg
			}
			cfg.exts[filepath.Ext(s.Path)] = true
			lastHot := time.Time{}
			if s.IsActive {
				lastHot = s.UpdatedAt
				cfg.activeCount++
			}
			cfg.sessions = append(cfg.sessions, tieredwatcher.SessionInfo{
				ID: s.ID, Path: s.Path, ModTime: s.UpdatedAt, LastHot: lastHot, FileSize: s.FileSize,
			})
		}

		// Create tiered watchers for file-based sessions (td-dca6fe)
		// This replaces a.Watch() calls for file-based adapters
		if len(adapterConfigs) > 0 {
			scale := p.hotTargetScale()

			for adapterID, cfg := range adapterConfigs {
				if len(cfg.sessions) == 0 {
					continue
				}

				adpt := adapters[adapterID]
				extractID := func(path string) string {
					id, err := adapter.ResolveSessionID(adpt, path)
					if err != nil {
						return ""
					}
					return id
				}
				extFilter := func(path string) bool { return true }
				if len(cfg.exts) > 0 {
					extFilter = func(path string) bool {
						return cfg.exts[filepath.Ext(path)]
					}
				}

				scanDir := func(dir string) ([]tieredwatcher.SessionInfo, error) {
					entries, err := os.ReadDir(dir)
					if err != nil {
						return nil, err
					}
					result := make([]tieredwatcher.SessionInfo, 0, len(entries))
					for _, entry := range entries {
						if entry.IsDir() {
							continue
						}
						name := entry.Name()
						path := filepath.Join(dir, name)
						if !extFilter(path) {
							continue
						}
						info, err := entry.Info()
						if err != nil {
							continue
						}
						id := extractID(path)
						if id == "" {
							continue
						}
						result = append(result, tieredwatcher.SessionInfo{
							ID:       id,
							Path:     path,
							ModTime:  info.ModTime(),
							FileSize: info.Size(),
						})
					}
					return result, nil
				}

				tw, ch, err := tieredwatcher.New(tieredwatcher.Config{
					FilePattern: "",
					Filter:      extFilter,
					ExtractID:   extractID,
					ScanDir:     scanDir,
				})
				if err != nil {
					notices = append(notices, fmt.Sprintf("%s conversation watcher failed: %v", adpt.Name(), err))
					continue
				}

				// Register all sessions with this watcher
				tw.RegisterSessions(cfg.sessions)
				manager.AddWatcher(adapterID, tw, ch)
				manager.SetHotTarget(adapterID, applyHotTargetScale(cfg.activeCount, scale))
				watchCount++
			}

			// Forward tiered watcher events to merged channel
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case evt, ok := <-manager.Events():
						if !ok {
							return
						}
						select {
						case merged <- evt:
						default:
						}
					}
				}
			}()
		}

		// For adapters without file paths (database-based like cursor, warp),
		// still use their Watch() methods
		for adapterID, a := range adapters {

			// Check if adapter has global watch scope
			isGlobal := false
			if scopeProvider, ok := a.(adapter.WatchScopeProvider); ok {
				isGlobal = scopeProvider.WatchScope() == adapter.WatchScopeGlobal
			}
			if fileBasedAdapters[adapterID] && !isGlobal {
				continue // Project-scoped files are fully covered by tiered watching.
			}

			pathsToWatch := worktreePaths
			if isGlobal {
				pathsToWatch = worktreePaths[:1]
			}

			for _, wtPath := range pathsToWatch {
				ch, closer, err := a.Watch(wtPath)
				if err != nil || ch == nil || closer == nil {
					if err != nil {
						notices = append(notices, fmt.Sprintf("%s conversation watcher failed: %v", a.Name(), err))
					}
					if closer != nil {
						_ = closer.Close()
					}
					continue
				}

				watchCount++
				wg.Add(1)
				go func(c <-chan adapter.Event, cl io.Closer, aid string) {
					defer wg.Done()
					defer func() { _ = cl.Close() }()
					for {
						select {
						case <-ctx.Done():
							return
						case evt, ok := <-c:
							if !ok {
								return
							}
							evt.AdapterID = aid
							select {
							case merged <- evt:
							default:
							}
						}
					}
				}(ch, closer, adapterID)
			}
		}

		if watchCount == 0 {
			cancel()
			_ = manager.Close()
			return WatchStartedMsg{Epoch: epoch, Channel: nil, Notices: notices}
		}

		// Close merged channel when all source channels are done
		go func() {
			wg.Wait()
			close(merged)
		}()

		return WatchStartedMsg{Epoch: epoch, Channel: merged, Cancel: cancel, Manager: manager, Notices: notices}
	}
}

// listenForWatchEvents waits for the next file system event.
func (p *Plugin) listenForWatchEvents() tea.Cmd {
	if p.watchChan == nil {
		return nil
	}
	// Capture epoch for stale detection on project switch
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	return func() tea.Msg {
		evt, ok := <-p.watchChan
		if !ok {
			// Channel closed
			return nil
		}
		return WatchEventMsg{Epoch: epoch, AdapterID: evt.AdapterID, SessionID: evt.SessionID}
	}
}

// AdapterBatchMsg delivers sessions from a single adapter incrementally (td-7198a5).
type AdapterBatchMsg struct {
	Epoch         uint64
	AdapterID     string
	Sessions      []adapter.Session
	Notices       []string
	Final         bool // true when all adapters are done
	WorktreePaths []string
	WorktreeNames map[string]string
}

// GetEpoch implements plugin.EpochMessage.
func (m AdapterBatchMsg) GetEpoch() uint64 { return m.Epoch }

// LoadingStartedMsg signals that adapter goroutines have been launched (td-7198a5).
type LoadingStartedMsg struct {
	Epoch uint64
}

// GetEpoch implements plugin.EpochMessage.
func (m LoadingStartedMsg) GetEpoch() uint64 { return m.Epoch }

// listenForAdapterBatch waits for incremental adapter session batches (td-7198a5).
func (p *Plugin) listenForAdapterBatch() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-p.adapterBatchChan
		if !ok {
			return nil
		}
		return msg
	}
}

func (p *Plugin) beginWholeSessionLoad() (uint64, context.Context, bool) {
	p.loadingMu.Lock()
	defer p.loadingMu.Unlock()
	if p.loadingSessions {
		return 0, nil, false
	}
	p.loadingSessions = true
	p.sessionLoadSeq++
	p.activeLoadSeq = p.sessionLoadSeq
	ctx, cancel := context.WithCancel(context.Background())
	p.loadCancel = cancel
	return p.activeLoadSeq, ctx, true
}

func (p *Plugin) finishWholeSessionLoad(seq uint64) {
	p.loadingMu.Lock()
	defer p.loadingMu.Unlock()
	if p.activeLoadSeq != seq {
		return
	}
	p.loadingSessions = false
	if p.loadCancel != nil {
		p.loadCancel()
	}
	p.loadCancel = nil
}

func (p *Plugin) sendAdapterBatch(ctx context.Context, ch chan<- AdapterBatchMsg, msg AdapterBatchMsg) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case ch <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *Plugin) acquireSessionCall(ctx context.Context, adapterID string) (func(), bool) {
	gate := p.sessionCallGateFor(adapterID)
	select {
	case <-gate:
		return func() { gate <- struct{}{} }, true
	case <-ctx.Done():
		return nil, false
	}
}

func (p *Plugin) tryAcquireSessionCall(ctx context.Context, adapterID string) (func(), bool) {
	gate := p.sessionCallGateFor(adapterID)
	if ctx.Err() != nil {
		return nil, false
	}
	select {
	case <-gate:
		return func() { gate <- struct{}{} }, true
	default:
		return nil, false
	}
}

func (p *Plugin) sessionCallGateFor(adapterID string) chan struct{} {
	p.sessionCallMapMu.Lock()
	gate := p.sessionCallGate[adapterID]
	if gate == nil {
		gate = make(chan struct{}, 1)
		gate <- struct{}{}
		p.sessionCallGate[adapterID] = gate
	}
	p.sessionCallMapMu.Unlock()
	return gate
}

func sessionLoadKey(adapterID, worktreePath string) string {
	return adapterID + "\x00" + worktreePath
}

func (p *Plugin) beginSessionLoad(adapterID, worktreePath string) (uint64, bool) {
	key := sessionLoadKey(adapterID, worktreePath)
	p.sessionLoadMu.Lock()
	defer p.sessionLoadMu.Unlock()

	if _, inFlight := p.sessionLoads[key]; inFlight {
		return 0, false
	}

	p.sessionPathSeq++
	token := p.sessionPathSeq
	p.sessionLoads[key] = token
	return token, true
}

func (p *Plugin) endSessionLoad(adapterID, worktreePath string, token uint64) {
	key := sessionLoadKey(adapterID, worktreePath)
	p.sessionLoadMu.Lock()
	defer p.sessionLoadMu.Unlock()

	current, inFlight := p.sessionLoads[key]
	if !inFlight || current != token {
		return
	}
	delete(p.sessionLoads, key)
}

// listenForCoalescedRefresh waits for coalesced refresh messages.
func (p *Plugin) listenForCoalescedRefresh() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-p.coalesceChan
		if !ok {
			return nil // Channel closed (td-e2791614)
		}
		return msg
	}
}

const hotTargetMinScale = 0.25

func (p *Plugin) hotTargetScale() float64 {
	count := fdmonitor.Count()
	if count < 0 {
		return 1.0
	}
	warn, crit := fdmonitor.Thresholds()
	if warn <= 0 || crit <= warn {
		return 1.0
	}
	if count < warn {
		return 1.0
	}
	if count >= crit {
		return hotTargetMinScale
	}
	progress := float64(count-warn) / float64(crit-warn)
	return 1.0 - (1.0-hotTargetMinScale)*progress
}

func applyHotTargetScale(activeCount int, scale float64) int {
	if activeCount <= 0 {
		return 0
	}
	if scale >= 0.999 {
		return activeCount
	}
	target := int(math.Ceil(float64(activeCount) * scale))
	if target < 1 {
		target = 1
	}
	if target > activeCount {
		target = activeCount
	}
	return target
}

func (p *Plugin) updateTieredHotTargets() {
	if p.tieredManager == nil || len(p.sessions) == 0 {
		return
	}

	activeCounts := make(map[string]int)
	hasSessions := make(map[string]bool)

	selectedAdapter := ""
	selectedActive := false

	for _, s := range p.sessions {
		if s.AdapterID == "" || s.Path == "" {
			continue
		}
		hasSessions[s.AdapterID] = true
		if s.IsActive {
			activeCounts[s.AdapterID]++
		}
		if s.ID == p.selectedSession {
			selectedAdapter = s.AdapterID
			selectedActive = s.IsActive
		}
	}

	if selectedAdapter != "" && !selectedActive {
		activeCounts[selectedAdapter]++
		hasSessions[selectedAdapter] = true
	}

	scale := p.hotTargetScale()
	for adapterID := range hasSessions {
		target := applyHotTargetScale(activeCounts[adapterID], scale)
		p.tieredManager.SetHotTarget(adapterID, target)
	}
}

// loadUsage loads usage stats for a session (placeholder for future implementation).
func (p *Plugin) loadUsage(sessionID string) tea.Cmd {
	// Usage is already computed from messages in MessagesLoadedMsg handler
	return nil
}

// formatSessionCount formats a session count.
func formatSessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

// shortID returns the first 8 characters of an ID, or the full ID if shorter.
func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// deriveWorktreeNameFromPath extracts the worktree name from a directory path.
// For paths like '/Users/foo/code/myrepo-feature-auth' with main repo 'myrepo',
// returns 'feature-auth'. This is used for deleted worktrees where git no longer
// has branch information.
func deriveWorktreeNameFromPath(wtPath, mainPath string) string {
	dirName := filepath.Base(wtPath)
	repoName := filepath.Base(mainPath)

	// If directory starts with repo name + hyphen, strip it
	// This handles the {repo}-{name} naming convention
	prefix := repoName + "-"
	if strings.HasPrefix(dirName, prefix) {
		return strings.TrimPrefix(dirName, prefix)
	}

	// Fallback: just use directory name
	return dirName
}
