// Package terminalfixture provides synthetic terminal workloads for tests and
// benchmarks. Its data is authored for Sidecar and contains no captured user
// terminal content.
package terminalfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ExistingGoPath  = "internal/runtime/frame.go"
	ExistingDocPath = "docs/terminal.md"
	MissingPath     = "build/missing-output.log"
)

const (
	canvas = "\x1b[48;2;18;18;22m\x1b[38;2;220;220;225m"
	panel  = "\x1b[48;2;35;38;47m\x1b[38;2;200;205;214m"
	reset  = "\x1b[0m"
)

// OpenCode is a deterministic OpenCode-shaped full-screen terminal workload.
// Frames include a canvas, a full-height side panel, changing status cells,
// safe URLs, file candidates, negative candidates, and bounded history rows.
type OpenCode struct {
	Width  int
	Height int
	frames []string
	bursts [][]byte
}

// NewOpenCode builds a reusable fixture at the requested pane geometry.
func NewOpenCode(width, height int) OpenCode {
	if width < 80 {
		width = 80
	}
	if height < 16 {
		height = 16
	}
	f := OpenCode{Width: width, Height: height}
	for step := range 8 {
		f.frames = append(f.frames, f.renderFrame(step))
		f.bursts = append(f.bursts, f.renderBurst(step))
	}
	return f
}

// Frame returns one capture-shaped snapshot. Every fourth frame carries a few
// history rows above the live grid, reproducing occasional scroll movement.
func (f OpenCode) Frame(step int) string { return f.frames[positiveMod(step, len(f.frames))] }

// Burst returns one ordered ANSI update for a seeded screen model.
func (f OpenCode) Burst(step int) []byte { return f.bursts[positiveMod(step, len(f.bursts))] }

// PopulateRoot creates only the fixture's positive file candidates beneath a
// caller-owned temporary root. The negative candidate remains absent.
func (f OpenCode) PopulateRoot(root string) error {
	for _, rel := range []string{ExistingGoPath, ExistingDocPath} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("synthetic terminal performance fixture\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (f OpenCode) renderFrame(step int) string {
	rows := make([]string, 0, f.Height+3)
	if step%4 == 3 {
		for i := range 3 {
			rows = append(rows, paintRow(f.Width, fmt.Sprintf("completed synthetic turn %02d", step*3+i), "history"))
		}
	}
	spinner := []string{"|", "/", "-", "\\"}[step%4]
	for row := range f.Height {
		main, side := fixtureRow(row, step, spinner)
		rows = append(rows, paintRow(f.Width, main, side))
	}
	return strings.Join(rows, "\n")
}

func fixtureRow(row, step int, spinner string) (main, side string) {
	switch row {
	case 0:
		main = " OpenCode synthetic workspace"
		side = "context"
	case 1:
		main = fmt.Sprintf(" %s working  frame %03d", spinner, step)
		side = "tokens 12.4k"
	case 3:
		main = " Reading " + ExistingGoPath + ":42"
		side = "modified"
	case 5:
		main = " Plan in " + ExistingDocPath
		side = "tracked"
	case 7:
		main = " Waiting on " + MissingPath
		side = "not found"
	case 9:
		main = " Docs https://docs.example.test/terminal/performance"
		side = "reference"
	case 11:
		main = " Follow-up td-a1b2c3 and nt-fixture01"
		side = "queued"
	case 13:
		main = " Session sidecar-ws-synthetic"
		side = "attached"
	default:
		main = fmt.Sprintf(" %-2d synthetic output cell %04d", row, step*100+row)
		side = fmt.Sprintf("event %02d", row)
	}
	return main, side
}

func paintRow(width int, main, side string) string {
	panelWidth := min(34, max(20, width/4))
	mainWidth := width - panelWidth
	return canvas + fitASCII(main, mainWidth) + panel + fitASCII(side, panelWidth) + reset
}

func fitASCII(value string, width int) string {
	if len(value) > width {
		return value[:width]
	}
	return value + strings.Repeat(" ", width-len(value))
}

func (f OpenCode) renderBurst(step int) []byte {
	spinner := []string{"|", "/", "-", "\\"}[step%4]
	var out strings.Builder
	fmt.Fprintf(&out, "\x1b[2;2H%s %s working  frame %03d%s", canvas, spinner, step, reset)
	fmt.Fprintf(&out, "\x1b[15;2H%s status cell %04d%s", canvas, step, reset)
	fmt.Fprintf(&out, "\x1b[%d;2H%s ■■⬝⬝ synthetic progress %04d%s", f.Height-1, canvas, step, reset)
	return []byte(out.String())
}

func positiveMod(value, modulus int) int {
	if modulus == 0 {
		return 0
	}
	value %= modulus
	if value < 0 {
		value += modulus
	}
	return value
}
