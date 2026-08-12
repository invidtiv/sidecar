package workspace

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugins/filebrowser"
	"github.com/marcus/sidecar/internal/ui"
)

type terminalLinkKind int

const (
	terminalURLLink terminalLinkKind = iota + 1
	terminalPathLink
)

type terminalLink struct {
	Kind     terminalLinkKind
	StartCol int
	EndCol   int
	Value    string
	Line     int
}

var (
	terminalURLPattern  = regexp.MustCompile(`https?://[^\s<>"']+`)
	terminalPathPattern = regexp.MustCompile(
		`(?:^|[\s(\[])((?:\.{0,2}/|/)?[A-Za-z0-9_][A-Za-z0-9_./-]*\.[A-Za-z0-9_+-]+):([1-9][0-9]*)`,
	)
)

func safeHTTPURL(raw string) (string, bool) {
	raw = strings.TrimRight(raw, ".,;!?) ]}")
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	return raw, true
}

func detectTerminalLinks(line string) []terminalLink {
	plain := ansi.Strip(line)
	var links []terminalLink
	for _, loc := range terminalURLPattern.FindAllStringIndex(plain, -1) {
		value, ok := safeHTTPURL(plain[loc[0]:loc[1]])
		if !ok {
			continue
		}
		endByte := loc[0] + len(value)
		links = append(links, terminalLink{
			Kind:     terminalURLLink,
			StartCol: ansi.StringWidth(plain[:loc[0]]),
			EndCol:   ansi.StringWidth(plain[:endByte]) - 1,
			Value:    value,
		})
	}
	for _, loc := range terminalPathPattern.FindAllStringSubmatchIndex(plain, -1) {
		if len(loc) < 6 || loc[2] < 0 || loc[4] < 0 {
			continue
		}
		start, end := loc[2], loc[3]
		if terminalLinkOverlapsBytes(plain, links, start, end) {
			continue
		}
		lineNo, err := strconv.Atoi(plain[loc[4]:loc[5]])
		if err != nil {
			continue
		}
		links = append(links, terminalLink{
			Kind:     terminalPathLink,
			StartCol: ansi.StringWidth(plain[:start]),
			EndCol:   ansi.StringWidth(plain[:end]) - 1,
			Value:    plain[start:end],
			Line:     lineNo,
		})
	}
	return links
}

func terminalLinkOverlapsBytes(plain string, links []terminalLink, start, end int) bool {
	startCol := ansi.StringWidth(plain[:start])
	endCol := ansi.StringWidth(plain[:end]) - 1
	for _, link := range links {
		if startCol <= link.EndCol && endCol >= link.StartCol {
			return true
		}
	}
	return false
}

func decorateTerminalLinks(line string) string {
	// tmux output is untrusted. Remove source-supplied OSC controls and
	// synthesize OSC-8 only for URLs that pass safeHTTPURL.
	line = stripSourceOSC8(line)
	links := detectTerminalLinks(line)
	// Apply from right to left so wrappers do not disturb later visual ranges.
	for i := len(links) - 1; i >= 0; i-- {
		link := links[i]
		open, close := "\x1b[4m", "\x1b[24m"
		if link.Kind == terminalURLLink {
			open = "\x1b]8;;" + link.Value + "\x1b\\\x1b[4m"
			close = "\x1b[24m\x1b]8;;\x1b\\"
		}
		line = wrapTerminalVisualRange(line, link.StartCol, link.EndCol, open, close)
	}
	return line
}

func stripSourceOSC8(line string) string {
	out := make([]byte, 0, len(line))
	inOSC := false
	for pos := 0; pos < len(line); {
		if inOSC {
			if terminatorLen := oscTerminatorLen(line, pos); terminatorLen > 0 {
				pos += terminatorLen
				inOSC = false
				continue
			}
			if introLen := oscIntroducerLen(line, pos); introLen > 0 {
				// Real terminal parsers restart OSC parsing on a nested
				// introducer. Remain in the discard state so the nested
				// payload cannot become an active hyperlink.
				pos += introLen
				continue
			}
			_, size := utf8.DecodeRuneInString(line[pos:])
			pos += size
			continue
		}

		if introLen := oscIntroducerLen(line, pos); introLen > 0 {
			pos += introLen
			inOSC = true
			continue
		}
		_, size := utf8.DecodeRuneInString(line[pos:])
		segment := line[pos : pos+size]
		if segment[0] == ']' {
			// Removing an intervening OSC must not concatenate an ordinary
			// trailing ESC with a later ']' into a fresh OSC introducer.
			for len(out) > 0 && out[len(out)-1] == '\x1b' {
				out = out[:len(out)-1]
			}
		}
		out = append(out, segment...)
		pos += size
	}
	cleaned := string(out)
	if containsSourceOSCIntroducer(cleaned) {
		// The scan removes variable-length controls. Fail closed if bytes on
		// either side of a removal ever concatenate into a new OSC introducer.
		return ""
	}
	return cleaned
}

func oscIntroducerLen(value string, pos int) int {
	switch {
	case pos+1 < len(value) && value[pos] == '\x1b' && value[pos+1] == ']':
		return 2
	case value[pos] == '\x9d':
		return 1
	case pos+1 < len(value) && value[pos] == '\xc2' && value[pos+1] == '\x9d':
		return 2
	default:
		return 0
	}
}

func oscTerminatorLen(value string, pos int) int {
	switch {
	case value[pos] == '\x07' || value[pos] == '\x9c':
		return 1
	case pos+1 < len(value) && value[pos] == '\x1b' && value[pos+1] == '\\':
		return 2
	case pos+1 < len(value) && value[pos] == '\xc2' && value[pos+1] == '\x9c':
		return 2
	default:
		return 0
	}
}

func containsSourceOSCIntroducer(value string) bool {
	for pos := 0; pos < len(value); {
		if oscIntroducerLen(value, pos) > 0 {
			return true
		}
		_, size := utf8.DecodeRuneInString(value[pos:])
		pos += size
	}
	return false
}

func wrapTerminalVisualRange(line string, startCol, endCol int, open, close string) string {
	var out strings.Builder
	state := ansi.NormalState
	col := 0
	wrapping := false
	for len(line) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(line, state, nil)
		if n <= 0 {
			out.WriteString(line)
			break
		}
		inRange := width > 0 && col >= startCol && col <= endCol
		if inRange && !wrapping {
			out.WriteString(open)
			wrapping = true
		} else if !inRange && wrapping && width > 0 {
			out.WriteString(close)
			wrapping = false
		}
		out.WriteString(seq)
		col += width
		state = newState
		line = line[n:]
	}
	if wrapping {
		out.WriteString(close)
	}
	return out.String()
}

func (p *Plugin) activateTerminalLink(action mouse.MouseAction) (tea.Cmd, bool) {
	point, line, ok := p.terminalPointAndLine(action)
	if !ok {
		return nil, false
	}
	for _, link := range detectTerminalLinks(ui.ExpandTabs(line, tabStopWidth)) {
		if point.Col < link.StartCol || point.Col > link.EndCol {
			continue
		}
		if link.Kind == terminalURLLink {
			p.clearTerminalSelection()
			return openInBrowser(link.Value), true
		}
		cmd := p.openTerminalPath(link.Value, link.Line)
		if cmd != nil {
			p.clearTerminalSelection()
		}
		return cmd, cmd != nil
	}
	return nil, false
}

func (p *Plugin) openTerminalPath(raw string, line int) tea.Cmd {
	if p.ctx == nil {
		return nil
	}
	base := p.ctx.WorkDir
	if !p.shellSelected {
		if wt := p.selectedWorktree(); wt != nil {
			base = wt.Path
		}
	}
	baseResolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil
	}
	rel, _, ok := resolveTerminalPath(baseResolved, raw)
	if !ok {
		return nil
	}
	if p.paneRoot != nil && docPaneTarget(rel, true) {
		return p.openDocPane(filepath.Clean(baseResolved), filepath.ToSlash(rel), line)
	}
	navigate := func() tea.Msg {
		return filebrowser.NavigateToFileMsg{Path: filepath.ToSlash(rel), Line: line}
	}
	ctxResolved, err := filepath.EvalSymlinks(p.ctx.WorkDir)
	if err != nil {
		ctxResolved = filepath.Clean(p.ctx.WorkDir)
	}
	if filepath.Clean(baseResolved) != filepath.Clean(ctxResolved) {
		return tea.Sequence(
			app.SwitchWorktree(baseResolved),
			app.FocusPlugin("file-browser"),
			navigate,
		)
	}
	return tea.Batch(app.FocusPlugin("file-browser"), navigate)
}

func resolveTerminalPath(base, raw string) (relative, absolute string, ok bool) {
	baseResolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", "", false
	}
	target := raw
	if !filepath.IsAbs(target) {
		target = filepath.Join(baseResolved, target)
	}
	targetResolved, err := filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return "", "", false
	}
	rel, err := filepath.Rel(baseResolved, targetResolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	info, err := os.Stat(targetResolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", false
	}
	return filepath.ToSlash(rel), targetResolved, true
}
