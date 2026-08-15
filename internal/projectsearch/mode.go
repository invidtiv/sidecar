package projectsearch

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// Outcome is what a key or mouse event asked the caller to do. Everything the
// search can do to itself it has already done by the time it returns; the
// outcome names only the things it cannot decide, because they belong to
// whatever surface is hosting it.
type Outcome int

const (
	// OutcomeNone: the event was consumed (or ignored) and the host has
	// nothing to do.
	OutcomeNone Outcome = iota
	// OutcomeCancelled: the user dismissed the search. The host should drop it.
	OutcomeCancelled
	// OutcomeOpen: the user chose a result. The host decides what "open" means.
	OutcomeOpen
	// OutcomeOpenExternal: the user asked for the result in their editor
	// (ctrl+e, or a double-click on a row).
	OutcomeOpenExternal
)

// Result carries an Outcome plus everything the host needs to act on it.
type Result struct {
	Outcome Outcome
	// Path is relative to the search root.
	Path string
	// Line is the 1-based line of the match, or 0 for a file header.
	Line int
	// NewTab is set when the user asked for the result beside what they are
	// already looking at (shift+enter) rather than in place.
	NewTab bool
	// Query is the search text that produced the hit, so a host that wants to
	// highlight it in the opened file has it after the search is dropped.
	Query string
}

func openResult(state *State, outcome Outcome, newTab bool) Result {
	if state == nil || len(state.Results) == 0 {
		return Result{}
	}
	path, lineNo := state.GetSelectedFile()
	if path == "" {
		return Result{}
	}
	return Result{Outcome: outcome, Path: path, Line: lineNo, NewTab: newTab, Query: state.Query}
}

// Search is a project-wide search surface: the state, the modal that presents
// it, and the input handling that drives both. It renders at whatever size it
// is given, so the same type serves a full screen and a single pane.
//
// It knows nothing about what opening a result means; see Outcome.
type Search struct {
	// State is the search's data. It is exported because hosts legitimately
	// inspect it (footer counts, tests) and because the ripgrep runner takes it.
	State *State

	root  string
	epoch uint64

	width, height int

	modal      *modal.Modal
	modalWidth int
}

// New creates a search rooted at root. epoch is stamped on the commands it
// issues, so results for a root the host has since switched away from can be
// dropped on arrival (see Update).
func New(root string, epoch uint64) *Search {
	return &Search{State: NewState(), root: root, epoch: epoch}
}

// SetSize records the surface the search will be rendered at. View does this
// too; call it directly when input may arrive before the first render, as it
// does on the keystroke that opens the search.
func (s *Search) SetSize(width, height int) {
	if s.width == width && s.height == height {
		return
	}
	s.width, s.height = width, height
	// The modal caches its layout by width; a resize invalidates it.
	s.clearModal()
}

// SetRoot changes the directory searched. Pending results for the old root are
// dropped by the epoch check in Update.
func (s *Search) SetRoot(root string, epoch uint64) {
	s.root, s.epoch = root, epoch
}

// Root is the directory this search runs in.
func (s *Search) Root() string { return s.root }

// View renders the search at the given size and registers its hit regions on
// handler. The result is the modal alone; the caller composites it over its own
// background (ui.OverlayModal for a screen, panemodal.Render for a pane).
func (s *Search) View(width, height int, handler *mouse.Handler) string {
	s.SetSize(width, height)
	return s.renderModal(handler)
}

// Update handles the search's own async traffic: the debounce tick and the
// ripgrep results. Messages stamped with a different epoch are dropped.
func (s *Search) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case DebounceMsg:
		// Only run search if debounce version matches (no newer keystrokes)
		if s.State != nil && s.State.DebounceVersion == msg.Version {
			return Run(s.root, s.State, s.epoch)
		}
		return nil

	case ResultsMsg:
		if msg.Epoch != s.epoch {
			return nil
		}
		s.Apply(msg)
		return nil
	}
	return nil
}

// Apply stores a landed results message. Update calls it after the epoch check;
// a host that does its own staleness filtering can call it directly.
func (s *Search) Apply(msg ResultsMsg) {
	state := s.State
	if state == nil {
		return
	}
	state.IsSearching = false
	if msg.Error != nil {
		state.Error = msg.Error.Error()
		state.Results = nil
	} else {
		state.Error = ""
		state.Results = msg.Results
		state.ScrollOffset = 0
		// Set cursor to first match (skip file headers)
		state.Cursor = state.FirstMatchIndex()
	}
}

// HandleKey processes a keypress. The returned command is the search's own
// async work (a debounce tick, a ripgrep run, a modal cursor blink).
func (s *Search) HandleKey(msg tea.KeyPressMsg) (Result, tea.Cmd) {
	key := msg.String()
	text := ui.PrintableKeyText(msg)
	state := s.State

	s.ensureModal()
	if s.modal == nil {
		return Result{Outcome: OutcomeCancelled}, nil
	}

	// Handle enter before modal to ensure it opens the result at state.Cursor
	// (modal's focus might be on an option button, but we want to open the selected result)
	if key == "enter" && state != nil && len(state.Results) > 0 {
		return openResult(state, OutcomeOpen, false), nil
	}
	if key == "shift+enter" && state != nil && len(state.Results) > 0 {
		return openResult(state, OutcomeOpen, true), nil
	}

	action, cmd := s.modal.HandleKey(msg)
	if action == "cancel" {
		s.clearModal()
		return Result{Outcome: OutcomeCancelled}, nil
	}

	if action != "" && state != nil {
		switch action {
		case OpenActionID:
			return openResult(state, OutcomeOpen, false), nil
		case ToggleRegexID:
			return Result{}, s.ToggleOption(&state.UseRegex)
		case ToggleCaseID:
			return Result{}, s.ToggleOption(&state.CaseSensitive)
		case ToggleWordID:
			return Result{}, s.ToggleOption(&state.WholeWord)
		}

		if fileIdx, ok := ParseFileID(action); ok {
			if flatIdx := state.FlatIndexForFile(fileIdx); flatIdx >= 0 {
				state.Cursor = flatIdx
			}
			return openResult(state, OutcomeOpen, false), nil
		}
		if fileIdx, matchIdx, ok := ParseMatchID(action); ok {
			if flatIdx := state.FlatIndexForMatch(fileIdx, matchIdx); flatIdx >= 0 {
				state.Cursor = flatIdx
			}
			return openResult(state, OutcomeOpen, false), nil
		}
	}

	// Tab toggles between input-focused and results-focused modes.
	// When results-focused, j/k/g/G navigate results (vim parity).
	if key == "tab" && state != nil && len(state.Results) > 0 {
		state.ResultsFocused = !state.ResultsFocused
		if state.ResultsFocused {
			// Jump to first match if cursor isn't on one
			_, _, isFile := state.FlatItem(state.Cursor)
			if isFile || state.Cursor == 0 {
				state.Cursor = state.FirstMatchIndex()
			}
		}
		s.clearModal() // Rebuild modal to reflect focus state
		return Result{}, cmd
	}

	// When results-focused, handle vim navigation keys before they reach
	// the default printable-character handler.
	if state != nil && state.ResultsFocused {
		switch key {
		case "j":
			state.Cursor = state.NextMatchIndex()
			return Result{}, cmd
		case "k":
			state.Cursor = state.PrevMatchIndex()
			return Result{}, cmd
		case "g":
			state.Cursor = state.FirstMatchIndex()
			state.ScrollOffset = 0
			return Result{}, cmd
		case "G":
			state.Cursor = state.LastMatchIndex()
			return Result{}, cmd
		}

		// Any other printable character switches back to input mode
		// and falls through to the default handler below.
		if text != "" {
			state.ResultsFocused = false
			s.clearModal()
		}
	}

	switch key {
	case "esc":
		s.clearModal()
		return Result{Outcome: OutcomeCancelled}, nil

	// Note: enter and shift+enter are handled before modal.HandleKey above

	case "left":
		// Collapse file group containing cursor's match
		if state != nil {
			fileIdx, _, _ := state.FlatItem(state.Cursor)
			if fileIdx >= 0 && fileIdx < len(state.Results) {
				state.Results[fileIdx].Collapsed = true
				// After collapse, snap to nearest visible match
				state.Cursor = state.NearestMatchIndex(state.Cursor)
			}
		}

	case "right":
		// Expand file group containing cursor's match
		if state != nil {
			fileIdx, _, _ := state.FlatItem(state.Cursor)
			if fileIdx >= 0 && fileIdx < len(state.Results) {
				state.Results[fileIdx].Collapsed = false
				// After expand, snap to first match in that file
				state.Cursor = state.NearestMatchIndex(state.Cursor)
			}
		}

	case "down", "ctrl+n":
		if state != nil {
			// Arrow navigation auto-focuses results
			state.ResultsFocused = true
			// Skip file headers, only navigate between matches
			state.Cursor = state.NextMatchIndex()
		}

	case "up", "ctrl+p":
		if state != nil {
			// Arrow navigation auto-focuses results
			state.ResultsFocused = true
			// Skip file headers, only navigate between matches
			state.Cursor = state.PrevMatchIndex()
		}

	case "ctrl+g":
		// Go to first match
		if state != nil {
			state.ResultsFocused = true
			state.Cursor = state.FirstMatchIndex()
			state.ScrollOffset = 0
		}

	case "ctrl+e":
		// Open in editor at line
		if state != nil && len(state.Results) > 0 {
			if res := openResult(state, OutcomeOpenExternal, false); res.Outcome != OutcomeNone {
				return res, nil
			}
		}

	case "ctrl+d":
		// Page down, snap to nearest match
		if state != nil {
			state.ResultsFocused = true
			state.Cursor += 10
			maxIdx := state.FlatLen() - 1
			if state.Cursor > maxIdx {
				state.Cursor = maxIdx
			}
			if state.Cursor < 0 {
				state.Cursor = 0
			}
			// Snap to nearest match (skip file headers)
			state.Cursor = state.NearestMatchIndex(state.Cursor)
		}

	case "ctrl+u":
		// Page up, snap to nearest match
		if state != nil {
			state.ResultsFocused = true
			state.Cursor -= 10
			if state.Cursor < 0 {
				state.Cursor = 0
			}
			// Snap to nearest match (skip file headers)
			state.Cursor = state.NearestMatchIndex(state.Cursor)
		}

	case "alt+r":
		// Toggle regex mode
		return Result{}, s.ToggleOption(regexOption(state))

	case "alt+c":
		// Toggle case sensitivity
		return Result{}, s.ToggleOption(caseOption(state))

	case "alt+w":
		// Toggle whole word
		return Result{}, s.ToggleOption(wordOption(state))

	case "backspace":
		if state != nil && len(state.Query) > 0 {
			state.ResultsFocused = false
			s.clearModal()
			runes := []rune(state.Query)
			state.Query = string(runes[:len(runes)-1])
			if state.Query == "" {
				state.Results = nil
				state.Error = ""
				state.DebounceVersion++ // Cancel any pending search
			} else {
				state.IsSearching = true
				state.DebounceVersion++
				return Result{}, Schedule(state.DebounceVersion, state.Query)
			}
		}

	default:
		// Append printable characters
		if state != nil && text != "" {
			state.Query += text
			state.IsSearching = true
			state.DebounceVersion++
			return Result{}, Schedule(state.DebounceVersion, state.Query)
		}
	}

	return Result{}, cmd
}

// regexOption and friends return a pointer to the toggle, or nil when there is
// no state, so ToggleOption's nil guard keeps doing the work.
func regexOption(state *State) *bool {
	if state == nil {
		return nil
	}
	return &state.UseRegex
}

func caseOption(state *State) *bool {
	if state == nil {
		return nil
	}
	return &state.CaseSensitive
}

func wordOption(state *State) *bool {
	if state == nil {
		return nil
	}
	return &state.WholeWord
}

// ToggleOption flips one of the search options and re-runs the search
// immediately if there is a query. option must point into the search's own
// State (see State.UseRegex and friends).
func (s *Search) ToggleOption(option *bool) tea.Cmd {
	state := s.State
	if state == nil || option == nil {
		return nil
	}

	*option = !*option
	if state.Query != "" {
		state.IsSearching = true
		state.DebounceVersion++ // Cancel any pending debounced search
		return Run(s.root, state, s.epoch)
	}
	return nil
}

// HandleMouse processes a mouse event against the regions the last View
// registered on handler.
func (s *Search) HandleMouse(msg tea.MouseMsg, handler *mouse.Handler) (Result, tea.Cmd) {
	s.ensureModal()
	state := s.State
	if state == nil || s.modal == nil {
		return Result{}, nil
	}

	if _, ok := msg.(tea.MouseMotionMsg); ok {
		s.modal.HandleMouse(msg, handler)
		return Result{}, nil
	}

	action := handler.HandleMouse(msg)

	switch action.Type {
	case mouse.ActionClick:
		return s.handleClick(action)

	case mouse.ActionDoubleClick:
		return s.handleDoubleClick(action)

	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		// Scroll results list
		delta := 3
		if action.Type == mouse.ActionScrollUp {
			delta = -3
		}
		maxIdx := state.FlatLen() - 1
		state.Cursor += delta
		if state.Cursor < 0 {
			state.Cursor = 0
		} else if state.Cursor > maxIdx {
			state.Cursor = maxIdx
		}
		return Result{}, nil
	}

	return Result{}, nil
}

func (s *Search) handleClick(action mouse.MouseAction) (Result, tea.Cmd) {
	state := s.State
	if action.Region == nil || state == nil {
		return Result{}, nil
	}

	switch action.Region.ID {
	case "modal-backdrop":
		s.clearModal()
		return Result{Outcome: OutcomeCancelled}, nil
	case "modal-body":
		return Result{}, nil
	}

	s.modal.SetFocus(action.Region.ID)

	switch action.Region.ID {
	case ToggleRegexID:
		return Result{}, s.ToggleOption(&state.UseRegex)
	case ToggleCaseID:
		return Result{}, s.ToggleOption(&state.CaseSensitive)
	case ToggleWordID:
		return Result{}, s.ToggleOption(&state.WholeWord)
	}

	// Single click on file header: toggle collapse/expand
	if fileIdx, ok := ParseFileID(action.Region.ID); ok {
		if flatIdx := state.FlatIndexForFile(fileIdx); flatIdx >= 0 {
			state.Cursor = flatIdx
		}
		if fileIdx >= 0 && fileIdx < len(state.Results) {
			state.Results[fileIdx].Collapsed = !state.Results[fileIdx].Collapsed
		}
		return Result{}, nil
	}

	// Single click on match: open file in preview
	if fileIdx, matchIdx, ok := ParseMatchID(action.Region.ID); ok {
		if flatIdx := state.FlatIndexForMatch(fileIdx, matchIdx); flatIdx >= 0 {
			state.Cursor = flatIdx
		}
		return openResult(state, OutcomeOpen, false), nil
	}

	return Result{}, nil
}

func (s *Search) handleDoubleClick(action mouse.MouseAction) (Result, tea.Cmd) {
	state := s.State
	if action.Region == nil || state == nil {
		return Result{}, nil
	}

	switch action.Region.ID {
	case "modal-backdrop", "modal-body":
		return Result{}, nil
	}

	// Double click on file header: open first match in external editor
	if fileIdx, ok := ParseFileID(action.Region.ID); ok {
		if fileIdx >= 0 && fileIdx < len(state.Results) {
			file := state.Results[fileIdx]
			lineNo := 0
			if len(file.Matches) > 0 {
				lineNo = file.Matches[0].LineNo
			}
			return Result{
				Outcome: OutcomeOpenExternal,
				Path:    file.Path,
				Line:    lineNo,
				Query:   state.Query,
			}, nil
		}
		return Result{}, nil
	}

	// Double click on match: open in external editor
	if fileIdx, matchIdx, ok := ParseMatchID(action.Region.ID); ok {
		if fileIdx >= 0 && fileIdx < len(state.Results) {
			file := state.Results[fileIdx]
			if matchIdx >= 0 && matchIdx < len(file.Matches) {
				match := file.Matches[matchIdx]
				return Result{
					Outcome: OutcomeOpenExternal,
					Path:    file.Path,
					Line:    match.LineNo,
					Query:   state.Query,
				}, nil
			}
		}
	}

	return Result{}, nil
}

// SetFocus focuses one of the modal's elements by ID (ToggleRegexID and
// friends, or a row ID from ParseFileID/ParseMatchID).
func (s *Search) SetFocus(id string) {
	s.ensureModal()
	if s.modal != nil {
		s.modal.SetFocus(id)
	}
}
