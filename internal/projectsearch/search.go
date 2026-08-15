package projectsearch

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	maxResults    = 1000                   // Max total matches to display
	searchTimeout = 30 * time.Second       // Max time for search
	debounceDelay = 200 * time.Millisecond // Debounce delay before searching
)

// State holds the state for project-wide search.
type State struct {
	Query   string
	Results []SearchFileResult

	// Search options (toggle with keyboard shortcuts)
	UseRegex      bool
	CaseSensitive bool
	WholeWord     bool

	// UI state
	Cursor         int  // Index in flattened results (files + matches)
	ScrollOffset   int  // For scrolling
	IsSearching    bool // True while ripgrep is running
	Error          string
	ResultsFocused bool // When true, j/k/g/G navigate results instead of typing
	// Truncated is set when the run hit the match cap; the counts row says so.
	Truncated bool

	// Debounce: only run search when version matches
	DebounceVersion int

	// RunToken names the newest ripgrep run this state has issued. Every run
	// stamps it on the ResultsMsg it produces, and a result carrying an older
	// token is dropped: two runs inside one epoch can finish out of order (a
	// slow debounced run and the immediate re-run an option toggle issues), and
	// the epoch alone cannot tell them apart.
	RunToken int

	// For future: multiple search tabs
	TabID int
}

// DebounceMsg is sent after debounce delay to trigger search.
type DebounceMsg struct {
	Version int
	Query   string
}

// SearchFileResult represents a file with search matches.
type SearchFileResult struct {
	Path      string
	Matches   []SearchMatch
	Collapsed bool
}

// SearchMatch represents a single match within a file.
type SearchMatch struct {
	LineNo   int    // 1-indexed line number
	LineText string // Full line content
	ColStart int    // Match start column (0-indexed)
	ColEnd   int    // Match end column (0-indexed)
}

// ResultsMsg contains results from a search.
type ResultsMsg struct {
	Epoch uint64 // Epoch when request was issued (for stale detection)
	// Run is the State.RunToken the run was issued with. Apply drops a message
	// whose token is no longer the newest, so results cannot land out of order.
	// A zero token means "not issued by Run" (a host or a test building the
	// message by hand) and is always applied.
	Run     int
	Results []SearchFileResult
	// Truncated says the run hit the match cap and there were more hits behind
	// it, so the counts row can say so rather than present a cut set as the
	// whole answer.
	Truncated bool
	Error     error
}

// GetEpoch implements plugin.EpochMessage.
func (m ResultsMsg) GetEpoch() uint64 { return m.Epoch }

// NewState creates a new search state.
func NewState() *State {
	return &State{
		Cursor:  0,
		Results: make([]SearchFileResult, 0),
	}
}

// TotalMatches returns the total number of matches across all files.
func (s *State) TotalMatches() int {
	count := 0
	for _, f := range s.Results {
		count += len(f.Matches)
	}
	return count
}

// FileCount returns the number of files with matches.
func (s *State) FileCount() int {
	return len(s.Results)
}

// FlatLen returns the length of the flattened results list.
// Each file is 1 item, plus its matches if not collapsed.
func (s *State) FlatLen() int {
	count := 0
	for _, f := range s.Results {
		count++ // File header
		if !f.Collapsed {
			count += len(f.Matches)
		}
	}
	return count
}

// FlatItem returns the item at the given flat index.
// Returns (fileIndex, matchIndex, isFile).
// matchIndex is -1 if this is a file header.
func (s *State) FlatItem(idx int) (fileIdx int, matchIdx int, isFile bool) {
	pos := 0
	for fi, f := range s.Results {
		if pos == idx {
			return fi, -1, true
		}
		pos++
		if !f.Collapsed {
			for mi := range f.Matches {
				if pos == idx {
					return fi, mi, false
				}
				pos++
			}
		}
	}
	return -1, -1, false
}

// ToggleFileCollapse toggles the collapsed state of the file at cursor.
func (s *State) ToggleFileCollapse() {
	fileIdx, _, isFile := s.FlatItem(s.Cursor)
	if fileIdx >= 0 && isFile {
		s.Results[fileIdx].Collapsed = !s.Results[fileIdx].Collapsed
	}
}

// FirstMatchIndex returns the flat index of the first match (skipping file headers).
// Returns 0 if no matches exist.
func (s *State) FirstMatchIndex() int {
	pos := 0
	for _, f := range s.Results {
		pos++ // Skip file header
		if !f.Collapsed && len(f.Matches) > 0 {
			return pos // First match in first non-collapsed file
		}
		if !f.Collapsed {
			pos += len(f.Matches)
		}
	}
	return 0 // Fallback to 0 if no matches visible
}

// LastMatchIndex returns the flat index of the last visible match.
// Skips file headers. Returns 0 if no matches are visible.
func (s *State) LastMatchIndex() int {
	last := 0
	found := false
	pos := 0
	for _, f := range s.Results {
		pos++ // Skip file header
		if !f.Collapsed && len(f.Matches) > 0 {
			found = true
			last = pos + len(f.Matches) - 1
		}
		if !f.Collapsed {
			pos += len(f.Matches)
		}
	}
	if !found {
		return 0
	}
	return last
}

// NextMatchIndex returns the flat index of the next match after current cursor.
// Skips file headers. Returns current cursor if no next match exists.
func (s *State) NextMatchIndex() int {
	maxIdx := s.FlatLen() - 1
	for idx := s.Cursor + 1; idx <= maxIdx; idx++ {
		_, _, isFile := s.FlatItem(idx)
		if !isFile {
			return idx
		}
	}
	return s.Cursor // No next match, stay at current
}

// PrevMatchIndex returns the flat index of the previous match before current cursor.
// Skips file headers. Returns current cursor if no previous match exists.
func (s *State) PrevMatchIndex() int {
	for idx := s.Cursor - 1; idx >= 0; idx-- {
		_, _, isFile := s.FlatItem(idx)
		if !isFile {
			return idx
		}
	}
	return s.Cursor // No previous match, stay at current
}

// NearestMatchIndex returns the flat index of the nearest match to the given index.
// Searches forward first, then backward. Returns 0 if no matches exist.
func (s *State) NearestMatchIndex(fromIdx int) int {
	maxIdx := s.FlatLen() - 1
	if maxIdx < 0 {
		return 0
	}

	// Check current position first
	if fromIdx >= 0 && fromIdx <= maxIdx {
		_, _, isFile := s.FlatItem(fromIdx)
		if !isFile {
			return fromIdx
		}
	}

	// Search forward
	for idx := fromIdx + 1; idx <= maxIdx; idx++ {
		_, _, isFile := s.FlatItem(idx)
		if !isFile {
			return idx
		}
	}

	// Search backward
	for idx := fromIdx - 1; idx >= 0; idx-- {
		_, _, isFile := s.FlatItem(idx)
		if !isFile {
			return idx
		}
	}

	return 0 // No matches found
}

// GetSelectedFile returns the currently selected file path and line number.
// If a match is selected, returns file path and line number.
// If a file header is selected, returns file path and line 0.
func (s *State) GetSelectedFile() (path string, lineNo int) {
	fileIdx, matchIdx, isFile := s.FlatItem(s.Cursor)
	if fileIdx < 0 || fileIdx >= len(s.Results) {
		return "", 0
	}

	file := s.Results[fileIdx]
	if isFile {
		return file.Path, 0
	}

	if matchIdx >= 0 && matchIdx < len(file.Matches) {
		return file.Path, file.Matches[matchIdx].LineNo
	}

	return file.Path, 0
}

// Schedule schedules a debounced search. The returned command fires a
// DebounceMsg after the debounce delay; the caller runs the search only if the
// version still matches, so newer keystrokes cancel older ones.
func Schedule(version int, query string) tea.Cmd {
	return tea.Tick(debounceDelay, func(t time.Time) tea.Msg {
		return DebounceMsg{Version: version, Query: query}
	})
}

// ripgrepBin is the search binary. It is a variable so a test can point the
// runner at a stand-in and watch what happens to the process — cancellation,
// above all, which is invisible if the only observable is ripgrep's own output.
var ripgrepBin = "rg"

// request is everything one ripgrep run needs, copied out of the live State at
// the moment the run is issued. The command runs on the Bubble Tea runtime's
// goroutine while the update loop keeps mutating State on every keystroke, so
// the goroutine must not read State at all — it reads this instead.
type request struct {
	workDir string
	query   string
	args    []string
	epoch   uint64
	run     int
}

// Run executes ripgrep and returns results. It snapshots everything the run
// needs before returning, so the command it hands back touches no shared state.
func Run(workDir string, state *State, epoch uint64) tea.Cmd {
	return RunContext(context.Background(), workDir, state, epoch)
}

// RunContext is Run with a caller-owned context, so a host can kill the ripgrep
// process when its search surface closes or is superseded rather than leaving it
// to the timeout. Search.run does exactly that; see Search.Close.
func RunContext(ctx context.Context, workDir string, state *State, epoch uint64) tea.Cmd {
	if state == nil {
		return nil
	}
	// Claiming the token here, on the update loop, is what makes "newest run
	// wins" true regardless of the order the results come back in.
	state.RunToken++
	req := request{
		workDir: workDir,
		query:   state.Query,
		args:    buildRipgrepArgs(state),
		epoch:   epoch,
		run:     state.RunToken,
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg { return req.exec(ctx) }
}

func (r request) exec(parent context.Context) tea.Msg {
	if r.query == "" {
		return ResultsMsg{Epoch: r.epoch, Run: r.run, Results: nil}
	}

	ctx, cancel := context.WithTimeout(parent, searchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ripgrepBin, r.args...)
	cmd.Dir = r.workDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ResultsMsg{Epoch: r.epoch, Run: r.run, Error: err}
	}

	if err := cmd.Start(); err != nil {
		// Check if rg is not installed
		if strings.Contains(err.Error(), "executable file not found") {
			return ResultsMsg{Epoch: r.epoch, Run: r.run, Error: &ripgrepNotFoundError{}}
		}
		return ResultsMsg{Epoch: r.epoch, Run: r.run, Error: err}
	}

	// Cancelling the context kills the direct child, but anything that child
	// forked inherits the write end of this pipe, so the read below can
	// outlive the process it was reading from. Closing the read end on
	// cancellation is what actually unblocks the parse — without it a
	// cancelled search sits here until the grandchild exits on its own, which
	// is platform-dependent (it shows up on Linux, not on macOS).
	go func() {
		<-ctx.Done()
		_ = stdout.Close()
	}()

	results, truncated := parseRipgrepOutput(stdout, maxResults, len(r.query))

	// Kill ripgrep early if we hit our limit - don't wait for it to finish
	// This is critical for queries with many matches (e.g., common words)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	return ResultsMsg{Epoch: r.epoch, Run: r.run, Results: results, Truncated: truncated}
}

// buildRipgrepArgs constructs the ripgrep command arguments.
func buildRipgrepArgs(state *State) []string {
	args := []string{
		"--line-number",     // Include line numbers
		"--column",          // Include column numbers for match position
		"--no-heading",      // Don't group by file (simpler parsing)
		"--with-filename",   // Always include filename
		"--max-count=100",   // Limit matches per file
		"--max-filesize=1M", // Skip very large files
	}

	if !state.CaseSensitive {
		args = append(args, "--ignore-case")
	}

	if state.WholeWord {
		args = append(args, "--word-regexp")
	}

	if !state.UseRegex {
		args = append(args, "--fixed-strings")
	}

	args = append(args, "--", state.Query)

	return args
}

// parseRipgrepOutput reads ripgrep line output (filename:line:col:content) and
// builds results. It also reports whether the cap cut the run short, because a
// capped result set that says nothing about it is a wrong answer: "1000 matches
// in 107 files" reads as the whole truth about the project.
func parseRipgrepOutput(reader interface{ Read([]byte) (int, error) }, maxMatches int, queryLen int) ([]SearchFileResult, bool) {
	scanner := bufio.NewScanner(reader)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	fileMap := make(map[string]*SearchFileResult)
	var fileOrder []string
	totalMatches := 0

	truncated := false
	for scanner.Scan() {
		if totalMatches >= maxMatches {
			// Scan already read a line this run will not show, which is exactly
			// what makes the cap observable rather than guessed at.
			truncated = true
			break
		}
		line := scanner.Text()
		if len(line) == 0 {
			continue
		}

		// Parse format: filename:line:column:content
		// Need to handle filenames that might contain colons (Windows paths, etc.)
		// ripgrep guarantees line and column are numeric, so we parse from the content backwards
		path, lineNo, colNo, content := parseRipgrepLine(line)
		if path == "" {
			continue
		}

		// Get or create file result
		file, exists := fileMap[path]
		if !exists {
			file = &SearchFileResult{
				Path:    path,
				Matches: make([]SearchMatch, 0, 8),
			}
			fileMap[path] = file
			fileOrder = append(fileOrder, path)
		}

		// Calculate match end from query length (column is 1-indexed)
		colStart := colNo - 1
		colEnd := colStart + queryLen

		file.Matches = append(file.Matches, SearchMatch{
			LineNo:   lineNo,
			LineText: content,
			ColStart: colStart,
			ColEnd:   colEnd,
		})
		totalMatches++
	}

	// Build ordered results
	results := make([]SearchFileResult, 0, len(fileOrder))
	for _, path := range fileOrder {
		results = append(results, *fileMap[path])
	}

	return results, truncated
}

// parseRipgrepLine parses a ripgrep output line in format: filename:line:column:content
// Returns empty path if parsing fails.
func parseRipgrepLine(line string) (path string, lineNo int, colNo int, content string) {
	// Find first colon (end of filename)
	// Then find next two colons for line and column numbers
	// Everything after third colon is content

	firstColon := strings.Index(line, ":")
	if firstColon < 0 {
		return "", 0, 0, ""
	}

	rest := line[firstColon+1:]
	secondColon := strings.Index(rest, ":")
	if secondColon < 0 {
		return "", 0, 0, ""
	}

	lineStr := rest[:secondColon]
	rest = rest[secondColon+1:]

	thirdColon := strings.Index(rest, ":")
	if thirdColon < 0 {
		return "", 0, 0, ""
	}

	colStr := rest[:thirdColon]
	content = rest[thirdColon+1:]

	lineNo, err1 := strconv.Atoi(lineStr)
	colNo, err2 := strconv.Atoi(colStr)
	if err1 != nil || err2 != nil {
		return "", 0, 0, ""
	}

	return line[:firstColon], lineNo, colNo, content
}

// ripgrepNotFoundError indicates rg is not installed.
type ripgrepNotFoundError struct{}

func (e *ripgrepNotFoundError) Error() string {
	return "ripgrep (rg) not found - install with: brew install ripgrep"
}

// FlatIndexForFile returns the flattened-list index of a file header, or -1
// when there is no such file.
func (s *State) FlatIndexForFile(fileIdx int) int {
	if s == nil || fileIdx < 0 || fileIdx >= len(s.Results) {
		return -1
	}

	flatIdx := 0
	for fi := range s.Results {
		if fi == fileIdx {
			return flatIdx
		}
		flatIdx++ // file header
		if !s.Results[fi].Collapsed {
			flatIdx += len(s.Results[fi].Matches)
		}
	}
	return -1
}

// FlatIndexForMatch returns the flattened-list index of one match, or -1 when
// it is not visible (no such match, or its file is collapsed).
func (s *State) FlatIndexForMatch(fileIdx, matchIdx int) int {
	if s == nil || fileIdx < 0 || fileIdx >= len(s.Results) {
		return -1
	}

	flatIdx := 0
	for fi, file := range s.Results {
		flatIdx++ // file header
		if fi == fileIdx {
			if file.Collapsed || matchIdx < 0 || matchIdx >= len(file.Matches) {
				return -1
			}
			return flatIdx + matchIdx
		}
		if !file.Collapsed {
			flatIdx += len(file.Matches)
		}
	}
	return -1
}
