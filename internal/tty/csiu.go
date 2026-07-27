package tty

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// ExtractUnknownCSIBytes extracts an unparsed CSI sequence from Bubble Tea's
// current ultraviolet event types. The reflection fallback preserves
// compatibility with Bubble Tea v1's unexported []byte message.
func ExtractUnknownCSIBytes(msg interface{}) []byte {
	var raw []byte
	switch msg := msg.(type) {
	case uv.UnknownEvent:
		raw = []byte(msg)
	case uv.UnknownCsiEvent:
		raw = []byte(msg)
	}
	if raw != nil {
		if len(raw) < 4 || raw[0] != '\x1b' || raw[1] != '[' {
			return nil
		}
		return raw
	}

	rv := reflect.ValueOf(msg)
	if rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() != reflect.Uint8 {
		return nil
	}
	raw = rv.Bytes()
	if len(raw) < 4 || raw[0] != '\x1b' || raw[1] != '[' {
		return nil
	}
	return raw
}

// NormalizeToCSIu converts an unknown CSI sequence to CSI u format for
// forwarding to tmux. It handles:
//
//	CSI u:             ESC [ keycode ; modifier u  → passed through
//	modifyOtherKeys:   ESC [ 27 ; modifier ; keycode ~  → converted to CSI u
//
// Returns the CSI u formatted string, or empty string if unrecognized.
func NormalizeToCSIu(raw []byte) string {
	if len(raw) < 4 || raw[0] != '\x1b' || raw[1] != '[' {
		return ""
	}

	// Extract params and final byte
	params := string(raw[2 : len(raw)-1])
	final := raw[len(raw)-1]

	switch final {
	case 'u':
		// Already CSI u format — pass through as-is
		return string(raw)

	case '~':
		// Possibly modifyOtherKeys: ESC [ 27 ; modifier ; keycode ~
		parts := strings.Split(params, ";")
		if len(parts) == 3 && parts[0] == "27" {
			modifier, err1 := strconv.Atoi(parts[1])
			keycode, err2 := strconv.Atoi(parts[2])
			if err1 == nil && err2 == nil {
				return fmt.Sprintf("\x1b[%d;%du", keycode, modifier)
			}
		}
	}

	return ""
}
