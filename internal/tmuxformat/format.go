// Package tmuxformat builds and decodes multi-field tmux format output.
package tmuxformat

import (
	"strconv"
	"strings"
)

// Separator is printable because tmux versions disagree about how C0 bytes in
// formatted output are represented. Fields() q-escapes this byte in values.
const Separator = "|"

// Fields returns one tmux format expression whose values can be decoded with
// Split. The q modifier escapes separators, whitespace, backslashes, and
// control bytes without changing the number of output fields.
func Fields(names ...string) string {
	var out strings.Builder
	for i, name := range names {
		if i > 0 {
			out.WriteString(Separator)
		}
		out.WriteString("#{q:")
		out.WriteString(name)
		out.WriteByte('}')
	}
	return out.String()
}

// Split separates and unescapes output produced by Fields.
func Split(line string) []string {
	fields := [][]byte{{}}
	for i := 0; i < len(line); i++ {
		b := line[i]
		switch b {
		case '|':
			fields = append(fields, nil)
		case '\\':
			if i+1 >= len(line) {
				fields[len(fields)-1] = append(fields[len(fields)-1], '\\')
				continue
			}
			i++
			next := line[i]
			switch next {
			case 'n':
				fields[len(fields)-1] = append(fields[len(fields)-1], '\n')
			case 'r':
				fields[len(fields)-1] = append(fields[len(fields)-1], '\r')
			case 't':
				fields[len(fields)-1] = append(fields[len(fields)-1], '\t')
			default:
				if next >= '0' && next <= '7' && i+2 < len(line) &&
					line[i+1] >= '0' && line[i+1] <= '7' && line[i+2] >= '0' && line[i+2] <= '7' {
					if value, err := strconv.ParseUint(line[i:i+3], 8, 8); err == nil {
						fields[len(fields)-1] = append(fields[len(fields)-1], byte(value))
						i += 2
						continue
					}
				}
				fields[len(fields)-1] = append(fields[len(fields)-1], next)
			}
		default:
			fields[len(fields)-1] = append(fields[len(fields)-1], b)
		}
	}
	out := make([]string, len(fields))
	for i := range fields {
		out[i] = string(fields[i])
	}
	return out
}
