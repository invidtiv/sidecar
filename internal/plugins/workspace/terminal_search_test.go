package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

func terminalSearchPlugin(content string, base int) *Plugin {
	p := New()
	p.width = 100
	p.height = 30
	p.activePane = PanePreview
	p.previewTab = PreviewTabOutput
	p.shellSelected = true
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.UpdateSnapshot(content, base)
	p.shells = []*ShellSession{{
		TmuxName: "search-shell",
		Agent: &Agent{
			TmuxSession: "search-shell",
			OutputBuf:   buffer,
		},
	}}
	return p
}

func TestTerminalSearchFindsCaseInsensitiveAbsoluteMatches(t *testing.T) {
	p := terminalSearchPlugin("zero\nError one and error two\nlast", 50)
	p.beginTerminalSearch()
	p.terminalSearch.Query = "error"
	p.recomputeTerminalSearch()

	if len(p.terminalSearch.Matches) != 2 {
		t.Fatalf("matches = %#v, want two", p.terminalSearch.Matches)
	}
	first, second := p.terminalSearch.Matches[0], p.terminalSearch.Matches[1]
	if first.Line != 51 || first.StartCol != 0 || first.EndCol != 4 {
		t.Fatalf("first match = %#v, want absolute line 51 cols 0..4", first)
	}
	if second.Line != 51 || second.StartCol != 14 {
		t.Fatalf("second match = %#v, want absolute line 51 col 14", second)
	}
}

func TestTerminalSearchInputAndNavigationAreConsumed(t *testing.T) {
	p := terminalSearchPlugin(strings.Repeat("haystack\n", 50)+"needle\n"+strings.Repeat("haystack\n", 50)+"needle", 0)

	handled, _ := p.handleTerminalSearchKey(tea.KeyPressMsg{Code: '/', Text: "/"}, false)
	if !handled || !p.terminalSearch.InputActive {
		t.Fatal("/ did not enter terminal search input")
	}
	for _, r := range "needle" {
		handled, _ = p.handleTerminalSearchKey(tea.KeyPressMsg{Code: r, Text: string(r)}, false)
		if !handled {
			t.Fatalf("search rune %q was not consumed", r)
		}
	}
	handled, _ = p.handleTerminalSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter}, false)
	if !handled || p.terminalSearch.InputActive || len(p.terminalSearch.Matches) != 2 {
		t.Fatalf("completed search state = %#v", p.terminalSearch)
	}
	firstScroll := p.previewScroll
	handled, _ = p.handleTerminalSearchKey(tea.KeyPressMsg{Code: 'n', Text: "n"}, false)
	// The later match is nearer the live bottom, so the window sits fewer rows
	// back from it.
	if !handled || p.terminalSearch.Current != 1 || p.previewScroll >= firstScroll {
		t.Fatalf("next match did not advance: current=%d scroll=%d first=%d",
			p.terminalSearch.Current, p.previewScroll, firstScroll)
	}
}

func TestTerminalSearchKeepsBackslashLiteral(t *testing.T) {
	p := terminalSearchPlugin("path\\to\\file", 0)
	p.beginTerminalSearch()
	handled, _ := p.handleTerminalSearchKey(tea.KeyPressMsg{Code: '\\', Text: "\\"}, false)
	if !handled || p.terminalSearch.Query != "\\" {
		t.Fatalf("backslash handled=%v query=%q, want literal input", handled, p.terminalSearch.Query)
	}
}

func TestInteractiveTerminalSearchShortcut(t *testing.T) {
	msg := tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl | tea.ModShift}
	if !isInteractiveSearchKey(msg) {
		t.Fatalf("ctrl+shift+f was not recognized: %q", msg.String())
	}
}

func TestTerminalViewportHighlightsSearchMatch(t *testing.T) {
	buffer := tty.NewOutputBuffer(10)
	buffer.UpdateSnapshot("find needle here", 20)
	result := renderTerminalViewport(terminalViewportInput{
		Buffer: buffer,
		Width:  40,
		Height: 1,
		SearchMatches: &terminalSearchMatches{Items: []terminalSearchMatch{{
			Line:     20,
			StartCol: 5,
			EndCol:   10,
		}}},
		AbsoluteBase: 20,
	}, ui.NewTruncateCache(16))
	if !strings.Contains(result.Content, ui.GetSelectionBgANSI()) {
		t.Fatalf("search highlight missing from %q", result.Content)
	}
}

func TestTerminalSearchLoadsAndSearchesUnvisitedHistory(t *testing.T) {
	p := terminalSearchPlugin(numberedTerminalLines(600, 620), 600)
	p.previewScroll = 10
	key := terminalHistoryKey("shell", "search-shell")
	p.terminalHistory[key] = terminalHistoryState{HistorySize: 1200}

	if cmd := p.beginTerminalSearch(); cmd == nil {
		t.Fatal("search did not request unvisited history")
	}
	p.terminalSearch.Query = "line-0010"
	state := p.terminalHistory[key]
	searchGen := p.terminalSearch.Generation
	p.applyTerminalSearchHistory(terminalSearchHistoryLoadedMsg{
		Source: terminalHistorySource{
			Key:    key,
			Target: "search-shell",
			Buffer: p.shells[0].Agent.OutputBuf,
		},
		Capture: tty.CaptureRange{
			Output:      numberedTerminalLines(0, 600),
			HistorySize: 1200,
			StartLine:   0,
			EndLine:     600,
		},
		RequestGen: state.RequestGen,
		SearchGen:  searchGen,
	})

	if len(p.terminalSearch.Matches) != 1 || p.terminalSearch.Matches[0].Line != 10 {
		t.Fatalf("full-history matches = %#v, want absolute line 10", p.terminalSearch.Matches)
	}
	if p.previewScroll != 10 {
		t.Fatalf("viewport scroll = %d, want the prepend to leave a bottom-relative window alone", p.previewScroll)
	}
}

func TestClearedTerminalSearchRejectsLateHistoryWithoutChangingFollow(t *testing.T) {
	p := terminalSearchPlugin(numberedTerminalLines(600, 620), 600)
	key := terminalHistoryKey("shell", "search-shell")
	p.terminalHistory[key] = terminalHistoryState{HistorySize: 1200}
	if p.beginTerminalSearch() == nil {
		t.Fatal("search did not request unvisited history")
	}
	state := p.terminalHistory[key]
	searchGen := p.terminalSearch.Generation
	p.terminalSearch.Query = "line"
	p.clearTerminalSearch()

	p.applyTerminalSearchHistory(terminalSearchHistoryLoadedMsg{
		Source: terminalHistorySource{
			Key:    key,
			Target: "search-shell",
			Buffer: p.shells[0].Agent.OutputBuf,
		},
		Capture: tty.CaptureRange{
			Output:      numberedTerminalLines(0, 600),
			HistorySize: 1200,
			StartLine:   0,
			EndLine:     600,
		},
		RequestGen: state.RequestGen,
		SearchGen:  searchGen,
	})
	start, _, _ := p.shells[0].Agent.OutputBuf.AbsoluteRange()
	if start != 600 || p.previewScroll != 0 || p.terminalSearch.Query != "" {
		t.Fatalf("late cleared search changed state: base=%d scroll=%d search=%#v",
			start, p.previewScroll, p.terminalSearch)
	}
}

func TestClearTerminalSearchCancelsStoredSourceAfterSelectionSwitch(t *testing.T) {
	p := terminalSearchPlugin(numberedTerminalLines(600, 620), 600)
	keyA := terminalHistoryKey("shell", "search-shell")
	p.terminalHistory[keyA] = terminalHistoryState{HistorySize: 1200}
	if p.beginTerminalSearch() == nil {
		t.Fatal("search on shell A did not start history load")
	}
	genA := p.terminalHistory[keyA].RequestGen

	bufferB := tty.NewOutputBuffer(20)
	bufferB.UpdateSnapshot("shell b", 0)
	p.shells = append(p.shells, &ShellSession{
		TmuxName: "shell-b",
		Agent:    &Agent{TmuxSession: "shell-b", OutputBuf: bufferB},
	})
	p.selectedShellIdx = 1
	p.clearTerminalSearch()

	stateA := p.terminalHistory[keyA]
	if stateA.Loading || stateA.RequestGen <= genA {
		t.Fatalf("stored source A was not cancelled after switch: %#v", stateA)
	}
}

func TestBeginTerminalSearchCancelsPreviousSourceLoad(t *testing.T) {
	p := terminalSearchPlugin(numberedTerminalLines(600, 620), 600)
	keyA := terminalHistoryKey("shell", "search-shell")
	p.terminalHistory[keyA] = terminalHistoryState{HistorySize: 1200}
	if p.beginTerminalSearch() == nil {
		t.Fatal("search on shell A did not start history load")
	}
	stateA := p.terminalHistory[keyA]
	searchGenA := p.terminalSearch.Generation

	bufferB := tty.NewOutputBuffer(outputBufferCap)
	bufferB.UpdateSnapshot(numberedTerminalLines(600, 620), 600)
	p.shells = append(p.shells, &ShellSession{
		TmuxName: "shell-b",
		Agent:    &Agent{TmuxSession: "shell-b", OutputBuf: bufferB},
	})
	keyB := terminalHistoryKey("shell", "shell-b")
	p.terminalHistory[keyB] = terminalHistoryState{HistorySize: 1200}
	p.selectedShellIdx = 1
	if p.beginTerminalSearch() == nil {
		t.Fatal("search on shell B did not start history load")
	}

	cancelledA := p.terminalHistory[keyA]
	if cancelledA.Loading || cancelledA.RequestGen <= stateA.RequestGen {
		t.Fatalf("source A load was not cancelled on begin switch: %#v", cancelledA)
	}
	p.applyTerminalSearchHistory(terminalSearchHistoryLoadedMsg{
		Source: terminalHistorySource{
			Key:    keyA,
			Target: "search-shell",
			Buffer: p.shells[0].Agent.OutputBuf,
		},
		Capture: tty.CaptureRange{
			Output:      numberedTerminalLines(0, 600),
			HistorySize: 1200,
			StartLine:   0,
			EndLine:     600,
		},
		RequestGen: stateA.RequestGen,
		SearchGen:  searchGenA,
	})
	if got := p.terminalHistory[keyA]; got.Loading || got.RequestGen != cancelledA.RequestGen {
		t.Fatalf("late source A result changed cancelled state: %#v", got)
	}
	if p.terminalSearch.SourceKey != keyB {
		t.Fatalf("search source = %q, want %q", p.terminalSearch.SourceKey, keyB)
	}
}

func TestTerminalSearchUnicodeCaseFoldMapsVisualColumns(t *testing.T) {
	p := terminalSearchPlugin("x Kelvin", 0)
	p.beginTerminalSearch()
	p.terminalSearch.Query = "kelvin"
	p.recomputeTerminalSearch()
	if len(p.terminalSearch.Matches) != 1 {
		t.Fatalf("Unicode fold matches = %#v, want one", p.terminalSearch.Matches)
	}
	match := p.terminalSearch.Matches[0]
	if match.StartCol != 2 || match.EndCol != 7 {
		t.Fatalf("Unicode fold columns = %d..%d, want 2..7", match.StartCol, match.EndCol)
	}
}
