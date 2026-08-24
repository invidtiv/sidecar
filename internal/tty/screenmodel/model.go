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
	"github.com/marcus/sidecar/internal/terminalperf"
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
	Output string
	// MainOutput is the saved main-screen capture returned by `capture-pane -a`
	// while Output contains the active alternate grid. It is populated only when
	// AltScreen is true. Seeding both grids is what lets a later DECSET 1049 exit
	// restore the same main screen tmux restores.
	MainOutput    string
	MainCursorRow int
	MainCursorCol int
	// HistorySize is tmux's history_size for the pane. The absolute base of the
	// loaded history is not taken from the caller: it is derived from the rows
	// Output actually carried, so the two can never disagree (see Seed).
	HistorySize   int
	HistoryLimit  int
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
	// Output is exactly the live pane rows. LoadedHistory is kept separate so a
	// frame publication never recopies Sidecar's 600-line history window merely
	// because the cursor or one cell changed. Consumers that still need the old
	// capture-shaped string can call CombinedOutput at that compatibility edge.
	Output        string
	LoadedHistory HistorySnapshot
	CaptureBase   int
	HistorySize   int
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

// HistorySnapshot is an immutable view of the loaded main-screen history at
// one frame. The snapshot owns its slice header while the model may append to
// the shared backing array. Its byte bounds never change; compaction allocates
// a new backing buffer so older frames remain valid.
type HistorySnapshot struct {
	data       []byte
	start, end int
	rows       int
}

type historyBuffer struct{ bytes []byte }

// Rows reports the number of loaded history rows.
func (h HistorySnapshot) Rows() int { return h.rows }

// Output renders the loaded history in capture-pane shape. It allocates only
// when a compatibility consumer explicitly asks for the combined legacy form;
// ordinary frame publication and the future incremental viewport do not.
func (h HistorySnapshot) Output() string {
	if h.start >= h.end {
		return ""
	}
	end := h.end
	if h.data[end-1] == '\n' {
		end--
	}
	return string(h.data[h.start:end])
}

// CombinedOutput returns loaded history followed by the live grid, matching
// capture-pane -p -e. It is the compatibility seam for diagnostics and the
// current OutputBuffer until that consumer accepts the two pieces directly.
func (f Frame) CombinedOutput() string {
	history := f.LoadedHistory.Output()
	if f.LoadedHistory.Rows() == 0 {
		return f.Output
	}
	return history + "\n" + f.Output
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
	absoluteHistory int
	historyLimit    int
	seedCaptureBase int
	seedLoaded      int
	pushedSinceSeed int
	historyData     *historyBuffer
	historySpans    [][2]int

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
	m.absoluteHistory = 0
	m.historyLimit = 600
	m.seedCaptureBase = 0
	m.seedLoaded = 0
	m.pushedSinceSeed = 0
	m.historyData = &historyBuffer{}
	m.historySpans = nil
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

		if s.HistoryLimit > 0 {
			m.historyLimit = s.HistoryLimit
		}
		// Paint the main grid first. When tmux is currently on the alternate
		// screen MainOutput contains its saved main grid and Output contains the
		// active alternate grid. Switching before painting would leave the main
		// screen empty and make the first DECSET 1049 exit diverge permanently.
		var b strings.Builder
		if s.AltScreen {
			b.WriteString(seedBody(s.MainOutput))
			fmt.Fprintf(&b, "\x1b[%d;%dH", s.MainCursorRow+1, s.MainCursorCol+1)
			b.WriteString("\x1b[?1049h\x1b[H")
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

		m.emu.SetScrollbackSize(m.historyLimit)
		// Anchor the absolute coordinate system on the rows the capture actually
		// carried. HistorySize and Output are observed at different instants — the
		// capture path issues its display-message and its capture-pane as separate
		// writes — so a capture can hold more history rows than the metadata that
		// accompanies it knew about. Taking either number on its own would leave
		// HistorySize - CaptureBase disagreeing with the loaded row count for the
		// rest of the pane's life, because Seed freezes both and only Write's
		// pushed-row accounting moves them afterwards. Deriving the base from the
		// loaded rows makes the invariant true at the seed, and therefore true for
		// every frame after it (td-d29821).
		m.seedLoaded = m.emu.ScrollbackLen()
		m.seedCaptureBase = max(s.HistorySize-m.seedLoaded, 0)
		m.absoluteHistory = m.seedCaptureBase + m.seedLoaded
		m.rebuildLoadedHistory()
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
		before := m.emu.ScrollbackLen()
		// x/vt's ScrollbackLen is bounded, but tmux history_size is absolute. Give
		// this write enough temporary headroom that every pushed row is observable,
		// then trim back to Sidecar's loaded-history window. A single input byte can
		// scroll at most one screenful (for example a compact CSI SU), so this bound
		// scales with the byte delta and geometry rather than session lifetime.
		headroom := before + len(p)*max(m.height, 1)
		if headroom < before { // integer overflow from a hostile payload
			headroom = int(^uint(0) >> 1)
		}
		m.emu.SetScrollbackSize(max(headroom, m.historyLimit))
		if _, err := m.emu.Write(p); err != nil {
			return fmt.Errorf("%w: write: %w", ErrModelFault, err)
		}
		after := m.emu.ScrollbackLen()
		if after > before {
			pushed := after - before
			m.absoluteHistory += pushed
			m.pushedSinceSeed += pushed
			m.appendLoadedHistory(after-pushed, after)
		} else if after < before {
			// The only byte-stream operation x/vt exposes that shrinks main
			// scrollback is ED 3 (CSI 3 J), which tmux also defines as clearing
			// history_size. `after` may be non-zero when the same payload clears
			// and then scrolls new rows, so reset the absolute coordinate system to
			// the post-write buffer rather than blindly setting it to zero.
			m.absoluteHistory = after
			m.seedCaptureBase = 0
			m.seedLoaded = after
			m.pushedSinceSeed = 0
			m.rebuildLoadedHistory()
		}
		m.emu.SetScrollbackSize(m.historyLimit)
		m.trimLoadedHistory(m.historyLimit)
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
	if err == nil {
		terminalperf.Record(terminalperf.ModelFrameBuilt)
	}
	return f, err
}

func (m *Model) frame() Frame {
	f := Frame{
		Width:          m.width,
		Height:         m.height,
		CursorVisible:  m.cursorVisible,
		AltScreen:      m.altScreen,
		Mouse:          m.mouse,
		CursorStyle:    m.cursorStyle,
		BracketedPaste: m.bracketedPaste,
		CaptureBase: max(m.seedCaptureBase+
			(m.seedLoaded+m.pushedSinceSeed-m.emu.ScrollbackLen()), 0),
		HardResets:      m.hardResets,
		ScrollbackAtCap: false,
	}
	pos := m.emu.CursorPosition()
	f.CursorCol, f.CursorRow = pos.X, pos.Y

	screen := ensureOutputRows(m.emu.Render(), m.height)
	// The viewport's capture-shaped input always contains the loaded tail of the
	// main-screen history above the live grid, including while a full-screen
	// application owns the alternate screen. tmux freezes history_size in that
	// mode but capture-pane -S still returns those rows; omitting them here made
	// the user's scrollback disappear exactly when less/vim/top was active.
	f.HistorySize = m.absoluteHistory
	f.HasHistory = f.HistorySize > 0
	f.Output = screen
	f.LoadedHistory = m.loadedHistorySnapshot()

	f.Cells = m.grid()
	return f
}

func ensureOutputRows(output string, height int) string {
	if height <= 0 {
		return ""
	}
	rows := strings.Count(output, "\n") + 1
	if rows < height {
		return output + strings.Repeat("\n", height-rows)
	}
	if rows > height {
		lines := strings.Split(output, "\n")
		return strings.Join(lines[len(lines)-height:], "\n")
	}
	return output
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
		out = lines*int64(m.width)*approxCellBytes + int64(len(m.historyData.bytes))
		return nil
	})
	return out
}

func (m *Model) rebuildLoadedHistory() {
	m.historyData = &historyBuffer{}
	m.historySpans = nil
	sb := m.emu.Scrollback()
	if sb == nil {
		return
	}
	m.appendLoadedHistory(0, sb.Len())
}

func (m *Model) appendLoadedHistory(start, end int) {
	sb := m.emu.Scrollback()
	if sb == nil || start >= end {
		return
	}
	for i := start; i < end; i++ {
		from := len(m.historyData.bytes)
		m.historyData.bytes = append(m.historyData.bytes, sb.Line(i).Render()...)
		m.historyData.bytes = append(m.historyData.bytes, '\n')
		m.historySpans = append(m.historySpans, [2]int{from, len(m.historyData.bytes)})
	}
}

func (m *Model) trimLoadedHistory(limit int) {
	if limit < 0 {
		limit = 0
	}
	if drop := len(m.historySpans) - limit; drop > 0 {
		m.historySpans = m.historySpans[drop:]
	}
	if len(m.historySpans) == 0 {
		m.historyData = &historyBuffer{}
		return
	}
	// Compact only after the dead prefix dominates. This keeps retained memory
	// bounded while making rolling-window maintenance amortized O(byte delta).
	start := m.historySpans[0][0]
	if start > 64<<10 && start > len(m.historyData.bytes)/2 {
		old := m.historyData.bytes
		fresh := &historyBuffer{bytes: append([]byte(nil), old[start:]...)}
		for i := range m.historySpans {
			m.historySpans[i][0] -= start
			m.historySpans[i][1] -= start
		}
		m.historyData = fresh
	}
}

func (m *Model) loadedHistorySnapshot() HistorySnapshot {
	if len(m.historySpans) == 0 {
		return HistorySnapshot{}
	}
	return HistorySnapshot{
		data: m.historyData.bytes, start: m.historySpans[0][0],
		end: m.historySpans[len(m.historySpans)-1][1], rows: len(m.historySpans),
	}
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
