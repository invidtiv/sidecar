package mobile

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

func normalizedFullFrame(snapshot tty.ControlSnapshot) ([]byte, mobileproto.Modes, error) {
	if snapshot.PaneWidth < 2 || snapshot.PaneHeight < 1 || snapshot.PaneRows != snapshot.PaneHeight {
		return nil, mobileproto.Modes{}, fmt.Errorf("capture geometry is incomplete")
	}
	if snapshot.PaneWidth > mobileproto.MaxColumns || snapshot.PaneHeight > mobileproto.MaxRows {
		return nil, mobileproto.Modes{}, fmt.Errorf("capture geometry exceeds protocol bounds")
	}
	totalRows := snapshot.HistoryRows + snapshot.PaneRows
	grid := screenmodel.DecodeCapture(snapshot.Output, snapshot.PaneWidth, totalRows)
	if len(grid) < snapshot.PaneHeight {
		return nil, mobileproto.Modes{}, fmt.Errorf("capture grid is incomplete")
	}
	grid = grid[len(grid)-snapshot.PaneHeight:]

	modes := modesFromSnapshot(snapshot)
	var out bytes.Buffer
	out.WriteString("\x1bc")
	setPrivateMode(&out, 1049, snapshot.AltScreen)
	setPrivateMode(&out, 7, snapshot.Autowrap)
	setPrivateMode(&out, 1, snapshot.ApplicationCursor)
	setPrivateMode(&out, 2004, snapshot.BracketedPaste)
	if snapshot.ApplicationKeypad {
		out.WriteString("\x1b=")
	} else {
		out.WriteString("\x1b>")
	}

	var previous screenmodel.Cell
	havePrevious := false
	linkURL, linkParams := "", ""
	for row, cells := range grid {
		fmt.Fprintf(&out, "\x1b[%d;1H", row+1)
		for _, cell := range cells {
			if cell.Width == 0 {
				continue
			}
			if cell.LinkURL != linkURL || cell.LinkParams != linkParams {
				if linkURL != "" {
					out.WriteString("\x1b]8;;\x1b\\")
				}
				linkURL, linkParams = cell.LinkURL, cell.LinkParams
				if linkURL != "" {
					fmt.Fprintf(&out, "\x1b]8;%s;%s\x1b\\", linkParams, linkURL)
				}
			}
			if !havePrevious || !sameRendition(previous, cell) {
				writeRendition(&out, cell)
				previous, havePrevious = cell, true
			}
			out.WriteString(cell.Grapheme)
		}
	}
	if linkURL != "" {
		out.WriteString("\x1b]8;;\x1b\\")
	}
	out.WriteString("\x1b[0m")
	setPrivateMode(&out, 6, snapshot.OriginMode)
	setMode(&out, 4, snapshot.InsertMode)
	fmt.Fprintf(&out, "\x1b[%d;%dH", snapshot.CursorRow+1, snapshot.CursorCol+1)
	if code := cursorStyleCode(snapshot.CursorShape, snapshot.CursorBlinking); code != "" {
		fmt.Fprintf(&out, "\x1b[%s q", code)
	}
	setPrivateMode(&out, 25, snapshot.CursorVisible)
	return out.Bytes(), modes, nil
}

// normalizedHistorySnapshot creates one frozen terminal transcript: the
// captured history rows scroll above a final live grid of PaneHeight rows.
// Autowrap is disabled and the last row has no line advance, so feeding the
// bytes once produces exactly HistoryRows of emulator-owned scrollback.
func normalizedHistorySnapshot(snapshot tty.ControlSnapshot) ([]byte, error) {
	if snapshot.PaneWidth < 2 || snapshot.PaneHeight < 1 || snapshot.PaneRows != snapshot.PaneHeight || snapshot.HistoryRows < 0 {
		return nil, fmt.Errorf("history capture geometry is incomplete")
	}
	if snapshot.PaneWidth > mobileproto.MaxColumns || snapshot.PaneHeight > mobileproto.MaxRows || snapshot.HistoryRows > mobileproto.MaxHistoryRows {
		return nil, fmt.Errorf("history capture geometry exceeds protocol bounds")
	}
	totalRows := snapshot.HistoryRows + snapshot.PaneRows
	grid := screenmodel.DecodeCapture(snapshot.Output, snapshot.PaneWidth, totalRows)
	if len(grid) != totalRows {
		return nil, fmt.Errorf("history capture grid is incomplete")
	}

	var out bytes.Buffer
	out.WriteString("\x1bc")
	setPrivateMode(&out, 7, false)
	setPrivateMode(&out, 25, false)
	var previous screenmodel.Cell
	havePrevious := false
	linkURL, linkParams := "", ""
	for row, cells := range grid {
		if row > 0 {
			out.WriteString("\r\n")
		}
		for _, cell := range cells {
			if cell.Width == 0 {
				continue
			}
			if cell.LinkURL != linkURL || cell.LinkParams != linkParams {
				if linkURL != "" {
					out.WriteString("\x1b]8;;\x1b\\")
				}
				linkURL, linkParams = cell.LinkURL, cell.LinkParams
				if linkURL != "" {
					fmt.Fprintf(&out, "\x1b]8;%s;%s\x1b\\", linkParams, linkURL)
				}
			}
			if !havePrevious || !sameRendition(previous, cell) {
				writeRendition(&out, cell)
				previous, havePrevious = cell, true
			}
			out.WriteString(cell.Grapheme)
		}
	}
	if linkURL != "" {
		out.WriteString("\x1b]8;;\x1b\\")
	}
	out.WriteString("\x1b[0m")
	return out.Bytes(), nil
}

func modesFromSnapshot(snapshot tty.ControlSnapshot) mobileproto.Modes {
	shape := "block"
	switch snapshot.CursorShape {
	case "2", "underline":
		shape = "underline"
	case "3", "bar":
		shape = "bar"
	case "block", "default":
		shape = "block"
	}
	return mobileproto.Modes{
		InputKnown: snapshot.InputModesKnown, BracketedPaste: snapshot.BracketedPaste,
		ApplicationCursor: snapshot.ApplicationCursor, ApplicationKeypad: snapshot.ApplicationKeypad,
		Autowrap: snapshot.Autowrap, Origin: snapshot.OriginMode, Insert: snapshot.InsertMode,
		MouseAny: snapshot.MouseReporting, MouseSGR: snapshot.MouseSGR, AlternateScreen: snapshot.AltScreen,
		CursorVisible: snapshot.CursorVisible, CursorShape: shape, CursorBlinking: snapshot.CursorBlinking,
	}
}

func setPrivateMode(out *bytes.Buffer, mode int, enabled bool) {
	verb := "l"
	if enabled {
		verb = "h"
	}
	fmt.Fprintf(out, "\x1b[?%d%s", mode, verb)
}

func setMode(out *bytes.Buffer, mode int, enabled bool) {
	verb := "l"
	if enabled {
		verb = "h"
	}
	fmt.Fprintf(out, "\x1b[%d%s", mode, verb)
}

func cursorStyleCode(shape string, blinking bool) string {
	switch shape {
	case "2", "underline":
		if blinking {
			return "3"
		}
		return "4"
	case "3", "bar":
		if blinking {
			return "5"
		}
		return "6"
	case "", "0", "1", "block", "default":
		if blinking {
			return "1"
		}
		return "2"
	default:
		return ""
	}
}

func sameRendition(a, b screenmodel.Cell) bool {
	return a.Fg == b.Fg && a.Bg == b.Bg && a.UnderlineColor == b.UnderlineColor &&
		a.Underline == b.Underline && a.Attrs == b.Attrs
}

func writeRendition(out *bytes.Buffer, cell screenmodel.Cell) {
	codes := []string{"0"}
	attrs := []struct {
		bit  screenmodel.Attr
		code string
	}{
		{screenmodel.AttrBold, "1"}, {screenmodel.AttrFaint, "2"}, {screenmodel.AttrItalic, "3"},
		{screenmodel.AttrBlink, "5"}, {screenmodel.AttrRapidBlink, "6"}, {screenmodel.AttrReverse, "7"},
		{screenmodel.AttrConceal, "8"}, {screenmodel.AttrStrikethrough, "9"},
	}
	for _, attr := range attrs {
		if cell.Attrs&attr.bit != 0 {
			codes = append(codes, attr.code)
		}
	}
	switch cell.Underline {
	case screenmodel.UnderlineSingle:
		codes = append(codes, "4")
	case screenmodel.UnderlineDouble:
		codes = append(codes, "4:2")
	case screenmodel.UnderlineCurly:
		codes = append(codes, "4:3")
	case screenmodel.UnderlineDotted:
		codes = append(codes, "4:4")
	case screenmodel.UnderlineDashed:
		codes = append(codes, "4:5")
	}
	codes = append(codes, colorCodes(cell.Fg, "38", "39")...)
	codes = append(codes, colorCodes(cell.Bg, "48", "49")...)
	codes = append(codes, colorCodes(cell.UnderlineColor, "58", "59")...)
	out.WriteString("\x1b[")
	out.WriteString(strings.Join(codes, ";"))
	out.WriteByte('m')
}

func colorCodes(color screenmodel.Color, extended, defaultCode string) []string {
	value := string(color)
	if value == "" {
		return []string{defaultCode}
	}
	if strings.HasPrefix(value, "i") {
		if n, err := strconv.Atoi(value[1:]); err == nil && n >= 0 && n <= 255 {
			return []string{extended, "5", strconv.Itoa(n)}
		}
	}
	if len(value) == 7 && value[0] == '#' {
		if rgb, err := strconv.ParseUint(value[1:], 16, 24); err == nil {
			return []string{extended, "2", strconv.Itoa(int(rgb >> 16)), strconv.Itoa(int((rgb >> 8) & 0xff)), strconv.Itoa(int(rgb & 0xff))}
		}
	}
	return []string{defaultCode}
}
