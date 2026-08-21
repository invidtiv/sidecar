package notes

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

type markdownListKind uint8

const (
	markdownListNone markdownListKind = iota
	markdownListBullet
	markdownListNumber
	markdownListTask
)

type markdownListLine struct {
	indent string
	marker string // includes the trailing space after the marker
	rest   string
	kind   markdownListKind
	number int
	delim  byte // '.' or ')'
	bullet string
}

func parseMarkdownListLine(line string) (markdownListLine, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	indent := line[:i]
	rest := line[i:]
	if rest == "" {
		return markdownListLine{}, false
	}

	if (rest[0] == '-' || rest[0] == '*') && len(rest) >= 2 && isListGap(rest[1]) {
		bullet := rest[:1]
		j := 1
		for j < len(rest) && isListGap(rest[j]) {
			j++
		}
		if j+3 <= len(rest) && rest[j] == '[' && isTaskMark(rest[j+1]) && rest[j+2] == ']' {
			k := j + 3
			if k < len(rest) && isListGap(rest[k]) {
				k++
			}
			return markdownListLine{
				indent: indent,
				marker: rest[:k],
				rest:   rest[k:],
				kind:   markdownListTask,
				bullet: bullet,
			}, true
		}
		return markdownListLine{
			indent: indent,
			marker: rest[:j],
			rest:   rest[j:],
			kind:   markdownListBullet,
			bullet: bullet,
		}, true
	}

	digitStart := 0
	for digitStart < len(rest) && rest[digitStart] >= '0' && rest[digitStart] <= '9' && digitStart < 9 {
		digitStart++
	}
	if digitStart == 0 || digitStart >= len(rest) {
		return markdownListLine{}, false
	}
	delim := rest[digitStart]
	if delim != '.' && delim != ')' {
		return markdownListLine{}, false
	}
	if digitStart+1 >= len(rest) || !isListGap(rest[digitStart+1]) {
		return markdownListLine{}, false
	}
	n, err := strconv.Atoi(rest[:digitStart])
	if err != nil || n < 1 || n > maxNotesOutlineOrdinal {
		return markdownListLine{}, false
	}
	j := digitStart + 2
	for j < len(rest) && isListGap(rest[j]) {
		j++
	}
	return markdownListLine{
		indent: indent,
		marker: rest[:j],
		rest:   rest[j:],
		kind:   markdownListNumber,
		number: n,
		delim:  delim,
	}, true
}

func isListGap(b byte) bool { return b == ' ' || b == '\t' }

func isTaskMark(b byte) bool { return b == ' ' || b == 'x' || b == 'X' }

func (l markdownListLine) empty() bool {
	return strings.TrimSpace(l.rest) == ""
}

func (l markdownListLine) nextMarker() string {
	switch l.kind {
	case markdownListTask:
		return l.bullet + " [ ] "
	case markdownListNumber:
		return strconv.Itoa(l.number+1) + string(l.delim) + " "
	default:
		return l.bullet + " "
	}
}

// continueMarkdownList implements Enter on a markdown list item: continue with
// the same indent and next marker, or exit the list when the item is empty.
func continueMarkdownList(content string, row, col int) (string, int, int, bool) {
	lines := strings.Split(content, "\n")
	if row < 0 || row >= len(lines) {
		return content, row, col, false
	}
	line := lines[row]
	parsed, ok := parseMarkdownListLine(line)
	if !ok {
		return content, row, col, false
	}

	if parsed.empty() {
		lines[row] = parsed.indent
		return strings.Join(lines, "\n"), row, utf8.RuneCountInString(parsed.indent), true
	}

	prefixRunes := utf8.RuneCountInString(parsed.indent) + utf8.RuneCountInString(parsed.marker)
	lineRunes := []rune(line)
	if col < 0 {
		col = 0
	}
	if col > len(lineRunes) {
		col = len(lineRunes)
	}
	split := col
	if split < prefixRunes {
		split = prefixRunes
	}
	lines[row] = string(lineRunes[:split])
	inserted := parsed.indent + parsed.nextMarker() + string(lineRunes[split:])
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:row+1]...)
	out = append(out, inserted)
	out = append(out, lines[row+1:]...)
	caretCol := utf8.RuneCountInString(parsed.indent + parsed.nextMarker())
	return strings.Join(out, "\n"), row + 1, caretCol, true
}
