package screenmodel

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Errors returned by a [Model].
var (
	// ErrConcurrentUse means two goroutines entered the model at once. The
	// model is single-actor by contract; this is a caller bug, and the second
	// caller is refused rather than allowed to interleave with emulator state.
	ErrConcurrentUse = errors.New("screenmodel: concurrent use of a single-actor model")
	// ErrClosed means the model has been closed.
	ErrClosed = errors.New("screenmodel: model is closed")
	// ErrInvalidGeometry means a caller passed impossible dimensions. It is a
	// rejection, not a fault: the model is unchanged and the call can be
	// retried with real geometry.
	ErrInvalidGeometry = errors.New("screenmodel: invalid geometry")
	// ErrModelFault means the emulator panicked or produced impossible state.
	// A faulted model stays faulted: the consumer must fall back to capture and
	// reseed. It must never take down the control reader or the UI loop.
	ErrModelFault = errors.New("screenmodel: model fault")
)

// MouseState mirrors the mouse tracking modes an application has enabled.
// These are plain bools on purpose: no ansi.Mode value escapes this package.
type MouseState struct {
	X10         bool // DECSET 9
	Normal      bool // DECSET 1000
	Highlight   bool // DECSET 1001
	ButtonEvent bool // DECSET 1002
	AnyEvent    bool // DECSET 1003
	SGR         bool // DECSET 1006, an encoding rather than a tracking mode
}

// Any reports whether any mouse *tracking* mode is on. It is the model's
// analogue of tmux's #{mouse_any_flag}, which is why the SGR encoding mode is
// excluded: enabling SGR encoding alone does not make an application
// mouse-aware.
func (m MouseState) Any() bool {
	return m.X10 || m.Normal || m.Highlight || m.ButtonEvent || m.AnyEvent
}

// CursorStyle is the shape of the cursor.
type CursorStyle uint8

// Cursor styles.
const (
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// Seed is the state a model is bootstrapped from when Sidecar attaches to a
// pane that is already running. Output is a bounded `capture-pane -e`
// rendering; everything else comes from tmux format metadata captured in the
// same transaction.
type Seed struct {
	Output        string
	CaptureBase   int
	HistorySize   int
	Width, Height int
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	AltScreen     bool
	Mouse         MouseState
}

// Frame is one rendered model snapshot. Its fields are shaped to populate
// tty.ControlSnapshot without translation, so the existing viewport, history,
// search, and selection journey keeps working unchanged.
type Frame struct {
	// Output is the ANSI-rendered loaded history followed by the live pane
	// rows, newline separated, in the same shape `capture-pane -p -e` produces.
	Output      string
	CaptureBase int
	HistorySize int
	// HasHistory reports whether any scrolled-off lines are loaded.
	HasHistory    bool
	Width, Height int
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	AltScreen     bool
	Mouse         MouseState

	// Cells is the canonical visible grid. It is the fidelity harness's unit
	// of comparison and the future shadow-mode comparison input; the viewport
	// consumes Output.
	Cells Grid

	// CursorStyle and BracketedPaste are model-observed state with no
	// capture-pane equivalent. BracketedPaste is reported for diagnostics
	// only — tmux, not this model, is the authority for paste correctness.
	CursorStyle    CursorStyle
	BracketedPaste bool

	// HardResets is the number of RIS (ESC c) sequences the model has seen
	// since the last seed. It exists so a diagnostic cannot attribute a
	// difference to the RIS defect (slice-0 GAP-8) without positive evidence
	// that a RIS actually happened.
	HardResets int
	// ScrollbackAtCap reports that the emulator's retained scrollback has
	// reached [DefaultScrollback], which is the exact precondition of the
	// absolute-history-drift defect. Without it, an absolute history
	// difference has some other cause.
	ScrollbackAtCap bool
}

// PaneModel is the behavior contract the workspace will eventually depend on.
// Only this interface may be referenced outside the package; the concrete
// [Model] and the emulator behind it stay here.
type PaneModel interface {
	Seed(Seed) error
	Write([]byte) error
	Resize(width, height int) error
	Frame() (Frame, error)
	Close()
}

// Model is a byte-fed screen model for one tmux pane.
//
// Single-actor: one goroutine at a time. See the package doc.
type Model struct {
	emu           *vt.Emulator
	replies       *replyDrain
	width, height int

	inUse  atomic.Bool
	closed bool
	failed error

	// Emulator state mirrored through callbacks. The emulator exposes mode and
	// cursor-visibility state only as change notifications, so the model keeps
	// its own copy seeded with the documented power-on defaults.
	altScreen      bool
	cursorVisible  bool
	cursorStyle    CursorStyle
	mouse          MouseState
	bracketedPaste bool

	// Absolute tmux history coordinates. The emulator's own scrollback is a
	// bounded local cache; these keep the viewport's lazy-older-history
	// requests in tmux's coordinate space.
	seedCaptureBase int
	seedHistorySize int
	baseScrollback  int

	// hardResets counts RIS sequences observed in the byte stream since the
	// last seed; pendingESC carries a trailing ESC across a Write boundary.
	// See scanHardResets.
	hardResets int
	pendingESC bool
}

var _ PaneModel = (*Model)(nil)

// DefaultScrollback is the number of scrolled-off lines the model retains.
// It bounds memory and matches the emulator's own default.
const DefaultScrollback = 10000

// New creates a model for a pane of the given size.
func New(width, height int) *Model {
	m := &Model{}
	m.reset(width, height)
	return m
}

// reset rebuilds the emulator from scratch. Reseeding always goes through here
// so that no hidden VT state (margins, saved cursor, charsets, partial parser
// state) survives from the previous pane or the previous attach.
func (m *Model) reset(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	// Release the previous emulator and its reply drain before replacing it, or
	// every reseed would leak an emulator, a 4 MB parser buffer, and a
	// goroutine blocked on a pipe.
	if m.replies != nil {
		m.replies.shutdown()
		m.replies = nil
	}
	m.width, m.height = width, height
	m.altScreen = false
	m.cursorVisible = true
	m.cursorStyle = CursorBlock
	m.mouse = MouseState{}
	m.bracketedPaste = false
	m.baseScrollback = 0
	m.hardResets = 0
	m.pendingESC = false

	emu := vt.NewEmulator(width, height)
	emu.SetScrollbackSize(DefaultScrollback)
	emu.SetCallbacks(vt.Callbacks{
		AltScreen:        func(on bool) { m.altScreen = on },
		CursorVisibility: func(v bool) { m.cursorVisible = v },
		CursorStyle: func(style vt.CursorStyle, _ bool) {
			m.cursorStyle = cursorStyle(style)
		},
		EnableMode:  func(mode ansi.Mode) { m.setMode(mode, true) },
		DisableMode: func(mode ansi.Mode) { m.setMode(mode, false) },
	})
	m.emu = emu
	m.replies = newReplyDrain(emu)
}

func cursorStyle(s vt.CursorStyle) CursorStyle {
	switch s {
	case vt.CursorUnderline:
		return CursorUnderline
	case vt.CursorBar:
		return CursorBar
	default:
		return CursorBlock
	}
}

// setMode records the modes Sidecar surfaces. Unknown modes are ignored: the
// model reports what the UI and diagnostics need, not the emulator's full mode
// table.
func (m *Model) setMode(mode ansi.Mode, on bool) {
	dec, ok := mode.(ansi.DECMode)
	if !ok {
		return
	}
	switch int(dec) {
	case 9:
		m.mouse.X10 = on
	case 1000:
		m.mouse.Normal = on
	case 1001:
		m.mouse.Highlight = on
	case 1002:
		m.mouse.ButtonEvent = on
	case 1003:
		m.mouse.AnyEvent = on
	case 1006:
		m.mouse.SGR = on
	case 2004:
		m.bracketedPaste = on
	}
}

// do runs one model operation under the single-actor guard, converting an
// emulator panic into a sticky fault instead of letting it escape.
func (m *Model) do(fn func() error) (err error) {
	if !m.inUse.CompareAndSwap(false, true) {
		return ErrConcurrentUse
	}
	defer m.inUse.Store(false)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrModelFault, r)
			m.failed = err
		}
	}()
	if m.closed {
		return ErrClosed
	}
	if m.failed != nil {
		return m.failed
	}
	return fn()
}

// Seed bootstraps the model from a tmux capture plus metadata, discarding all
// previous state.
func (m *Model) Seed(s Seed) error {
	return m.do(func() error {
		if s.Width < 1 || s.Height < 1 {
			return fmt.Errorf("%w: seed geometry %dx%d", ErrInvalidGeometry, s.Width, s.Height)
		}
		m.reset(s.Width, s.Height)

		var b strings.Builder
		if s.AltScreen {
			b.WriteString("\x1b[?1049h")
		}
		b.WriteString(seedBody(s.Output))
		// tmux reports cursor_x/cursor_y zero-based; CUP is one-based.
		fmt.Fprintf(&b, "\x1b[%d;%dH", s.CursorRow+1, s.CursorCol+1)
		if s.CursorVisible {
			b.WriteString("\x1b[?25h")
		} else {
			b.WriteString("\x1b[?25l")
		}
		for _, mm := range []struct {
			on   bool
			mode int
		}{
			{s.Mouse.X10, 9},
			{s.Mouse.Normal, 1000},
			{s.Mouse.Highlight, 1001},
			{s.Mouse.ButtonEvent, 1002},
			{s.Mouse.AnyEvent, 1003},
			{s.Mouse.SGR, 1006},
		} {
			if mm.on {
				fmt.Fprintf(&b, "\x1b[?%dh", mm.mode)
			}
		}

		if _, err := m.emu.WriteString(b.String()); err != nil {
			return fmt.Errorf("%w: seed write: %w", ErrModelFault, err)
		}

		m.seedCaptureBase = s.CaptureBase
		m.seedHistorySize = s.HistorySize
		m.baseScrollback = m.emu.ScrollbackLen()
		return nil
	})
}

// seedBody converts a capture-pane rendering into bytes the emulator can
// replay. capture-pane separates rows with a bare "\n"; without the carriage
// return the emulator would stair-step them.
//
// Output is row separated, not row terminated: N-1 newlines mean N rows, and
// the seed writes exactly that many. A trailing newline therefore means a final
// blank row and is honoured, because that is what tmux reports when the cursor
// sits on an empty line below the content. An earlier version trimmed it, which
// silently wrote one row too few and shifted the whole screen up by one against
// the cursor position the same transaction reported — the first real
// mid-stream seed lost a line to it. Callers that hold a shell-style
// newline-terminated capture must strip that terminator themselves.
func seedBody(output string) string {
	return strings.ReplaceAll(output, "\n", "\r\n")
}

// Write feeds raw pane bytes to the model. Partial sequences are fine: the
// emulator's parser holds state across calls.
func (m *Model) Write(p []byte) error {
	return m.do(func() error {
		if len(p) == 0 {
			return nil
		}
		m.scanHardResets(p)
		if _, err := m.emu.Write(p); err != nil {
			return fmt.Errorf("%w: write: %w", ErrModelFault, err)
		}
		return nil
	})
}

// scanHardResets counts RIS (ESC c) sequences in a write, carrying a trailing
// ESC across the write boundary.
//
// It is a byte scan, not a parse: a RIS is exactly two bytes and cannot legally
// appear inside a string payload, but a hostile OSC could still contain the
// pair. That asymmetry is deliberate. The count is only ever used to *withhold*
// an amnesty — a mismatch is attributed to the RIS defect only when a RIS was
// actually seen — so a miscount can make a result look worse, never better.
func (m *Model) scanHardResets(p []byte) {
	i := 0
	if m.pendingESC {
		m.pendingESC = false
		if p[0] == 'c' {
			m.hardResets++
			i = 1
		}
	}
	for i < len(p) {
		j := bytes.IndexByte(p[i:], 0x1b)
		if j < 0 {
			return
		}
		i += j + 1
		if i >= len(p) {
			m.pendingESC = true
			return
		}
		if p[i] == 'c' {
			m.hardResets++
			i++
		}
	}
}

// Resize resizes the model's grid. Callers resize tmux first and pass the
// authoritative resulting geometry.
func (m *Model) Resize(width, height int) error {
	return m.do(func() error {
		if width < 1 || height < 1 {
			return fmt.Errorf("%w: resize geometry %dx%d", ErrInvalidGeometry, width, height)
		}
		m.emu.Resize(width, height)
		m.width, m.height = width, height
		return nil
	})
}

// Frame renders the current model state.
func (m *Model) Frame() (Frame, error) {
	var f Frame
	err := m.do(func() error {
		f = m.frame()
		return nil
	})
	return f, err
}

func (m *Model) frame() Frame {
	f := Frame{
		Width:           m.width,
		Height:          m.height,
		CursorVisible:   m.cursorVisible,
		AltScreen:       m.altScreen,
		Mouse:           m.mouse,
		CursorStyle:     m.cursorStyle,
		BracketedPaste:  m.bracketedPaste,
		CaptureBase:     m.seedCaptureBase,
		HardResets:      m.hardResets,
		ScrollbackAtCap: m.emu.ScrollbackLen() >= DefaultScrollback,
	}
	pos := m.emu.CursorPosition()
	f.CursorCol, f.CursorRow = pos.X, pos.Y

	screen := m.emu.Render()
	if m.altScreen {
		// The alternate screen has no scrollback, and tmux freezes
		// history_size while an application owns it — at the value it had when
		// the application switched, which includes everything that scrolled off
		// the main screen since the seed. Reporting the seed's value alone made
		// the model claim a history of 1 where tmux reported 202 for the same
		// pane (slice 2 shadow run).
		f.Output = screen
		f.HistorySize = m.seedHistorySize + m.scrolledOff()
	} else {
		history := m.historyLines()
		f.HistorySize = m.seedHistorySize + m.scrolledOff()
		f.HasHistory = f.HistorySize > 0
		if len(history) > 0 {
			f.Output = strings.Join(history, "\n") + "\n" + screen
		} else {
			f.Output = screen
		}
	}

	f.Cells = m.grid()
	return f
}

// approxCellBytes is a fixed per-cell accounting weight for [Model.Footprint].
// It is an estimate, not a measurement: the emulator's cell holds a style, a
// link, and a small string, and the exact layout is a dependency detail. The
// number exists so retained memory can be tracked as a *bounded, comparable*
// series across a soak, which is what the decision gate asks for; it is not a
// substitute for a heap profile.
const approxCellBytes = 64

// Footprint estimates the memory the model retains: the live grid plus the
// scrollback it has accumulated. Reported by the shadow-mode diagnostics.
func (m *Model) Footprint() int64 {
	var out int64
	_ = m.do(func() error {
		lines := int64(m.height)
		if sb := m.emu.Scrollback(); sb != nil {
			lines += int64(sb.Len())
		}
		out = lines * int64(m.width) * approxCellBytes
		return nil
	})
	return out
}

// scrolledOff is the number of lines the model has pushed into scrollback
// since seeding.
//
// Known limitation (slice 0): once the emulator's scrollback reaches
// DefaultScrollback the oldest line is dropped on every push, so this delta
// stops growing and the absolute history count under-reports. Sidecar's
// capture window is 600 lines, far below the cap, so nothing in this slice is
// affected; slice 1 owns real absolute-coordinate tracking.
func (m *Model) scrolledOff() int {
	n := m.emu.ScrollbackLen() - m.baseScrollback
	if n < 0 {
		return 0
	}
	return n
}

// historyLines renders the loaded scrolled-off lines.
func (m *Model) historyLines() []string {
	sb := m.emu.Scrollback()
	if sb == nil {
		return nil
	}
	n := sb.Len()
	if n == 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, sb.Line(i).Render())
	}
	return out
}

// grid converts the visible screen into canonical cells.
func (m *Model) grid() Grid {
	g := make(Grid, m.height)
	for y := range m.height {
		row := make([]Cell, m.width)
		for x := range m.width {
			row[x] = convertCell(m.emu.CellAt(x, y))
		}
		g[y] = row
	}
	return g
}

func convertCell(c *uv.Cell) Cell {
	if c == nil {
		return BlankCell
	}
	if c.Content == "" && c.Width == 0 {
		return Continuation
	}
	out := Cell{
		Grapheme:       c.Content,
		Width:          c.Width,
		Fg:             canonicalColor(c.Style.Fg),
		Bg:             canonicalColor(c.Style.Bg),
		UnderlineColor: canonicalColor(c.Style.UnderlineColor),
		Underline:      convertUnderline(c.Style.Underline),
		Attrs:          convertAttrs(c.Style.Attrs),
		LinkURL:        c.Link.URL,
		LinkParams:     c.Link.Params,
	}
	if out.Grapheme == "" {
		out.Grapheme = " "
	}
	if out.Width == 0 {
		out.Width = 1
	}
	return out
}

func convertUnderline(u uv.Underline) Underline {
	switch u {
	case uv.UnderlineSingle:
		return UnderlineSingle
	case uv.UnderlineDouble:
		return UnderlineDouble
	case uv.UnderlineCurly:
		return UnderlineCurly
	case uv.UnderlineDotted:
		return UnderlineDotted
	case uv.UnderlineDashed:
		return UnderlineDashed
	default:
		return UnderlineNone
	}
}

func convertAttrs(a uint8) Attr {
	var out Attr
	for _, p := range []struct {
		from uint8
		to   Attr
	}{
		{uv.AttrBold, AttrBold},
		{uv.AttrFaint, AttrFaint},
		{uv.AttrItalic, AttrItalic},
		{uv.AttrBlink, AttrBlink},
		{uv.AttrRapidBlink, AttrRapidBlink},
		{uv.AttrReverse, AttrReverse},
		{uv.AttrConceal, AttrConceal},
		{uv.AttrStrikethrough, AttrStrikethrough},
	} {
		if a&p.from != 0 {
			out |= p.to
		}
	}
	return out
}

// Close releases the model. It is idempotent and terminal.
//
// Close deliberately does not go through [Model.do]: do refuses a closed or
// faulted model before it runs the closure, and a fault is precisely the state
// a consumer closes from. Release therefore has to bypass the sticky-error
// check, or a faulted pane would keep its emulator's pipe and buffers alive for
// the lifetime of the process. The single-actor guard still applies — calling
// Close concurrently with another operation is the same caller bug it always
// was — and an emulator panic during release is swallowed rather than escaping
// into the caller's goroutine.
func (m *Model) Close() {
	if !m.inUse.CompareAndSwap(false, true) {
		return
	}
	defer m.inUse.Store(false)
	defer func() { _ = recover() }()
	if m.closed {
		return
	}
	// Marked closed first, so a panic inside the emulator's own teardown still
	// leaves the model refusing further use.
	m.closed = true
	if m.replies != nil {
		// The drain goroutine owns Emulator.Close: it is the only goroutine that
		// touches the emulator's I/O side, so closing from here would race its
		// in-flight Read.
		m.replies.shutdown()
		m.replies = nil
	}
}
