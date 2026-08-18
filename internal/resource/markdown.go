package resource

import (
	"strings"

	"github.com/marcus/sidecar/internal/markdown"
)

// The shared Markdown renderer is not a trust boundary. Glamour synthesizes an
// OSC-8 hyperlink for every link, autolink, image, and linkified bare URL it
// parses — with the provider's destination verbatim, including javascript:,
// file:, ssh:, and data: — even when the input bytes contain no escape at all.
// It also prints the destination as visible text beside the label.
//
// So a resource body goes through three steps, and only three steps:
//
//  1. SafeMarkdownSource rewrites the source so no destination survives
//     parsing: raw HTML and autolinks are neutered, images become their alt
//     text, and links become their label.
//  2. the shared renderer renders that source.
//  3. StripOSC runs over every rendered line as defense in depth, which also
//     catches the one construct step 1 deliberately leaves alone — a bare URL
//     in running text, whose visible form is the URL itself.
//
// In v1 the separately typed and validated SourceURL is the only resource
// action that can open anything.

// RenderSafeMarkdown renders provider body text through the shared renderer
// with the resource sanitizer on both sides. The renderer is passed in so this
// package does not own renderer lifetime or caching.
func RenderSafeMarkdown(r *markdown.Renderer, text string, width int) []string {
	safe := SafeMarkdownSource(text)
	var lines []string
	if r == nil {
		lines = markdown.WrapText(safe, width)
	} else {
		lines = r.RenderContent(safe, width)
	}
	return StripRenderedOSC(lines)
}

// StripRenderedOSC removes every OSC sequence from already-rendered lines. It
// is the last gate before a provider body reaches a view.
func StripRenderedOSC(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = StripOSC(line)
	}
	return out
}

// SafeMarkdownSource rewrites Markdown source so that parsing it cannot
// produce a link destination:
//
//   - `<` is escaped, which removes raw HTML, HTML comments, and `<...>`
//     autolinks in one move;
//   - `![alt](dest)` and `![alt][ref]` collapse to their alt text;
//   - `[label](dest)`, `[label][ref]` and `[label][]` collapse to their label;
//   - link reference definitions are dropped, and a shortcut `[label]` whose
//     definition was dropped loses its brackets;
//   - fenced code blocks and inline code spans are copied verbatim, since code
//     cannot create a link and rewriting it would corrupt what it shows.
//
// A bare URL in running text is deliberately left alone: its visible form is
// the URL, so rewriting it would change what the user reads. The post-render
// StripOSC pass is what makes it inert.
//
// One known cosmetic limit: an indented (four-space) code block is not
// detected, so bracket syntax inside one is rewritten like ordinary text. That
// is a display wart, never a safety hole.
func SafeMarkdownSource(text string) string {
	if text == "" {
		return ""
	}
	body, defs := stripLinkDefinitions(text)
	// Dropping definition lines can leave trailing blank lines behind. They
	// render as nothing, but trimming them keeps the sanitizer's output stable
	// enough to assert on.
	return strings.TrimRight(rewriteInline(body, defs), "\n")
}

// stripLinkDefinitions removes link reference definition lines and returns the
// set of labels they defined, folded for case-insensitive matching.
func stripLinkDefinitions(text string) (string, map[string]bool) {
	defs := make(map[string]bool)
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	fence := ""
	for _, line := range lines {
		if f, ok := fenceDelimiter(line); ok {
			if fence == "" {
				fence = f
			} else if strings.HasPrefix(f, fence[:1]) && len(f) >= len(fence) {
				fence = ""
			}
			kept = append(kept, line)
			continue
		}
		if fence != "" {
			kept = append(kept, line)
			continue
		}
		if label, ok := linkDefinitionLabel(line); ok {
			defs[strings.ToLower(strings.TrimSpace(label))] = true
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), defs
}

// fenceDelimiter reports whether a line opens or closes a fenced code block and
// returns the run of fence characters.
func fenceDelimiter(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) >= 4 {
		return "", false
	}
	var ch byte
	switch {
	case strings.HasPrefix(trimmed, "```"):
		ch = '`'
	case strings.HasPrefix(trimmed, "~~~"):
		ch = '~'
	default:
		return "", false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	return trimmed[:n], true
}

// linkDefinitionLabel matches `[label]: destination ...` at the start of a
// line, allowing up to three leading spaces.
func linkDefinitionLabel(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) >= 4 || !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	end, ok := matchBracket(trimmed, 0, '[', ']')
	if !ok {
		return "", false
	}
	rest := trimmed[end+1:]
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	if strings.TrimSpace(rest[1:]) == "" {
		return "", false
	}
	return trimmed[1:end], true
}

// rewriteInline walks the source once, copying code verbatim and collapsing
// every link-bearing construct to inert text.
func rewriteInline(s string, defs map[string]bool) string {
	var out strings.Builder
	out.Grow(len(s) + len(s)/8)

	atLineStart := true
	fence := ""
	i := 0
	for i < len(s) {
		if atLineStart {
			end := strings.IndexByte(s[i:], '\n')
			line := s[i:]
			if end >= 0 {
				line = s[i : i+end]
			}
			if f, ok := fenceDelimiter(line); ok {
				if fence == "" {
					fence = f
				} else if f[0] == fence[0] && len(f) >= len(fence) {
					fence = ""
				}
				out.WriteString(line)
				if end < 0 {
					return out.String()
				}
				out.WriteByte('\n')
				i += end + 1
				continue
			}
			if fence != "" {
				out.WriteString(line)
				if end < 0 {
					return out.String()
				}
				out.WriteByte('\n')
				i += end + 1
				continue
			}
		}

		c := s[i]
		switch {
		case c == '\n':
			out.WriteByte('\n')
			atLineStart = true
			i++
			continue
		case c == '\\' && i+1 < len(s):
			out.WriteString(s[i : i+2])
			i += 2
		case c == '`':
			n := runLength(s, i, '`')
			if closeIdx := findCodeSpanClose(s, i+n, n); closeIdx >= 0 {
				out.WriteString(s[i : closeIdx+n])
				i = closeIdx + n
			} else {
				out.WriteString(s[i : i+n])
				i += n
			}
		case c == '<':
			// Escaping the introducer is what removes raw HTML, HTML comments,
			// and autolinks — all three are `<`-led and nothing else is.
			out.WriteString("\\<")
			i++
		case c == '!' && i+1 < len(s) && s[i+1] == '[':
			if text, next, ok := collapseLink(s, i+1, defs); ok {
				out.WriteString(text)
				i = next
			} else {
				out.WriteByte(c)
				i++
			}
		case c == '[':
			if text, next, ok := collapseLink(s, i, defs); ok {
				out.WriteString(text)
				i = next
			} else {
				out.WriteByte(c)
				i++
			}
		default:
			out.WriteByte(c)
			i++
		}
		atLineStart = false
	}
	return out.String()
}

// collapseLink consumes a bracketed construct starting at s[start] == '[' and
// returns the inert replacement text and the index just past what it consumed.
func collapseLink(s string, start int, defs map[string]bool) (string, int, bool) {
	end, ok := matchBracket(s, start, '[', ']')
	if !ok {
		return "", 0, false
	}
	label := rewriteInline(s[start+1:end], defs)
	next := end + 1

	if next < len(s) && s[next] == '(' {
		if close, ok := matchBracket(s, next, '(', ')'); ok {
			return label, close + 1, true
		}
	}
	if next < len(s) && s[next] == '[' {
		if close, ok := matchBracket(s, next, '[', ']'); ok {
			return label, close + 1, true
		}
	}
	// Shortcut reference: only strip the brackets when a definition existed,
	// so ordinary bracketed prose is left as the author wrote it.
	if defs[strings.ToLower(strings.TrimSpace(s[start+1:end]))] {
		return label, next, true
	}
	return "[" + label + "]", next, true
}

// matchBracket finds the index of the closing delimiter matching the opener at
// s[start], honoring nesting and backslash escapes.
func matchBracket(s string, start int, open, close byte) (int, bool) {
	if start >= len(s) || s[start] != open {
		return 0, false
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func runLength(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// findCodeSpanClose returns the index of a backtick run of exactly n backticks
// at or after from, which is how CommonMark closes a code span.
func findCodeSpanClose(s string, from, n int) int {
	for i := from; i < len(s); i++ {
		if s[i] != '`' {
			continue
		}
		run := runLength(s, i, '`')
		if run == n {
			return i
		}
		i += run - 1
	}
	return -1
}
