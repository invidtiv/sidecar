package noteview

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/markdown"
	sharedscroll "github.com/marcus/sidecar/internal/scroll"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	tabStopWidth             = 8
	horizontalContentPadding = 1
)

// LoadedMsg is the result of a Model load. Identity fields ensure a result
// can only be applied to the model, request, and plugin epoch that issued it.
type LoadedMsg struct {
	ModelID           int
	RequestGeneration uint64
	Epoch             uint64
	NoteID            string
	Data              *Data
	Error             error
	Refresh           bool
	// NotModified completes an in-flight refresh without replacing content.
	NotModified bool
	// Revision is the last adopted source revision, for a later conditional read.
	Revision string
}

// NotModified is returned by an injected loader when a refresh found no change.
type NotModified struct {
	NoteID   string
	Epoch    uint64
	Revision string
}

// NoteLoader produces a note load command. IfRevision is the last adopted
// source revision and may be empty. The command should return FetchedMsg,
// NotModified, or LoadedMsg.
type NoteLoader func(workDir, noteID string, epoch uint64, ifRevision string) tea.Cmd

// GetEpoch allows callers to apply the normal plugin epoch checks if desired.
func (m LoadedMsg) GetEpoch() uint64 { return m.Epoch }

type row struct {
	text string
}

// Model is one td note in one content box.
type Model struct {
	renderer *markdown.Renderer

	modelID           int
	requestGeneration uint64
	epoch             uint64
	noteID            string
	workDir           string

	width  int
	height int
	scroll int

	pendingScroll    int
	hasPendingScroll bool

	loading bool
	data    *Data
	err     error
	live    livewatch.Refresher

	loader   NoteLoader
	revision string

	focused    bool
	rows       []row
	buildFor   int
	buildStyle string
}

// SetLoader replaces the default td fetch. Nil restores it.
func (m *Model) SetLoader(loader NoteLoader) {
	if m == nil {
		return
	}
	m.loader = loader
}

// New creates an empty note viewer.
//
// The card owns its wrap contract: it insets each side by
// horizontalContentPadding itself and reserves a scrollbar column in
// contentWidth, so its markdown must render without Glamour's document
// margin — the same pairing Notes uses. A nil renderer, or one built without
// markdown.CompactDocument, is replaced by the card's own compact renderer
// rather than mutated: an injected instance may be shared with viewers
// (docview, resourceview) whose inset is Glamour's margin. This is what keeps
// every surface that shows a note — workspace leaf, overview preview, app
// content deck — wrapping identically no matter what it injects; without it,
// body text wraps several columns before the frame's right edge (td-65095b).
func New(renderer *markdown.Renderer) *Model {
	if renderer == nil || !renderer.CompactsDocument() {
		renderer, _ = markdown.NewRenderer(markdown.CompactDocument)
	}
	return &Model{renderer: renderer, buildFor: -1}
}

// Load retargets the model at noteID and returns a command that fetches it.
func (m *Model) Load(modelID int, workDir, noteID string, epoch uint64) tea.Cmd {
	m.modelID = modelID
	m.requestGeneration++
	m.epoch = epoch
	m.noteID = noteID
	m.workDir = workDir
	m.scroll = 0
	m.loading = true
	m.data = nil
	m.err = nil
	m.revision = ""
	m.live.Reset()
	m.invalidateRender()

	generation := m.requestGeneration
	base := LoadedMsg{
		ModelID: modelID, RequestGeneration: generation, Epoch: epoch, NoteID: noteID,
	}
	if m.loader != nil {
		load := m.loader(workDir, noteID, epoch, "")
		return func() tea.Msg { return adoptNoteMsg(load, base) }
	}
	fetch := Fetch(workDir, noteID)
	return func() tea.Msg {
		msg, _ := fetch().(FetchedMsg)
		base.Data, base.Error = msg.Data, msg.Error
		return base
	}
}

func adoptNoteMsg(load tea.Cmd, base LoadedMsg) LoadedMsg {
	if load == nil {
		base.Error = fmt.Errorf("unexpected note load result")
		return base
	}
	switch msg := load().(type) {
	case FetchedMsg:
		base.Data = msg.Data
		base.Error = msg.Error
		return base
	case NotModified:
		base.Refresh = true
		base.NotModified = true
		base.Revision = msg.Revision
		if msg.NoteID != "" {
			base.NoteID = msg.NoteID
		}
		if msg.Epoch != 0 {
			base.Epoch = msg.Epoch
		}
		return base
	case LoadedMsg:
		msg.ModelID = base.ModelID
		msg.RequestGeneration = base.RequestGeneration
		if msg.Epoch == 0 {
			msg.Epoch = base.Epoch
		}
		if msg.NoteID == "" {
			msg.NoteID = base.NoteID
		}
		if base.Refresh {
			msg.Refresh = true
		}
		return msg
	default:
		base.Error = fmt.Errorf("unexpected note load result")
		return base
	}
}

// ResultMatches reports whether msg belongs to this model's current load.
func (m *Model) ResultMatches(msg LoadedMsg) bool {
	if m == nil {
		return false
	}
	return msg.ModelID == m.modelID &&
		msg.RequestGeneration == m.requestGeneration &&
		msg.Epoch == m.epoch &&
		msg.NoteID == m.noteID
}

// SetResult applies msg if it belongs to the current load.
func (m *Model) SetResult(msg LoadedMsg) bool {
	if !m.ResultMatches(msg) {
		return false
	}
	if msg.Revision != "" {
		m.revision = msg.Revision
	}
	if msg.NotModified {
		if !msg.Refresh {
			return false
		}
		return m.applyRefresh(msg)
	}
	if msg.Refresh {
		return m.applyRefresh(msg)
	}
	m.live.Reset()
	m.live.Adopt(fingerprintData(msg.Data))
	m.loading = false
	m.data = msg.Data
	m.err = msg.Error
	m.invalidateRender()
	if m.hasPendingScroll {
		m.scroll = m.pendingScroll
		m.hasPendingScroll = false
	}
	m.clampScroll()
	return true
}

// Arm shows the loading placeholder for a restored tab without issuing a load.
func (m *Model) Arm(modelID int, noteID string, epoch uint64) {
	m.modelID = modelID
	m.epoch = epoch
	m.noteID = noteID
	m.loading = true
}

// NeedsLoad reports whether this model has never been asked to Load.
func (m *Model) NeedsLoad() bool { return m.requestGeneration == 0 }

// Data returns the current note, or nil before a successful fetch.
func (m *Model) Data() *Data { return m.data }

// Err returns the last fetch error, if any.
func (m *Model) Err() error { return m.err }

// SetSize sets the content box dimensions.
func (m *Model) SetSize(width, height int) {
	width = max(width, 0)
	height = max(height, 0)
	if width != m.width {
		m.invalidateRender()
	}
	m.width = width
	m.height = height
	m.clampScroll()
}

// SetFocused marks the card as the current tab stop.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
}

// Focused reports whether the card is the current tab stop.
func (m *Model) Focused() bool { return m.focused }

// View returns exactly the configured number of rows, each no wider than the
// configured width in terminal cells.
func (m *Model) View() string {
	if m.height <= 0 {
		return ""
	}
	rows := m.visibleRows()
	bodyWidth := m.contentWidth()
	useBar := m.needsScrollbar()

	out := make([]string, m.height)
	for i := range out {
		line := ""
		if i < len(rows) {
			line = rows[i].text
		}
		out[i] = fitLine(line, bodyWidth)
	}
	if useBar {
		params := m.ScrollbarParams()
		bar, _ := ui.RenderScrollbarWithGeometry(params)
		barLines := strings.Split(bar, "\n")
		for i := range out {
			s := " "
			if i < len(barLines) {
				s = barLines[i]
			}
			out[i] += s
		}
	}
	for i := range out {
		out[i] = strings.Repeat(" ", m.leftPadding()) +
			fitLine(out[i], m.innerWidth()) +
			strings.Repeat(" ", m.rightPadding())
	}
	return strings.Join(out, "\n")
}

// HandleKey applies note keys: j/k, arrows, paging, and g/G scroll.
func (m *Model) HandleKey(k tea.KeyMsg) (bool, tea.Cmd) {
	return m.handleKeyString(k.String())
}

func (m *Model) handleKeyString(key string) (bool, tea.Cmd) {
	switch key {
	case "j", "down":
		m.Scroll(1)
		return true, nil
	case "k", "up":
		m.Scroll(-1)
		return true, nil
	case "ctrl+d", "pgdown":
		m.Scroll(max(m.height/2, 1))
		return true, nil
	case "ctrl+u", "pgup":
		m.Scroll(-max(m.height/2, 1))
		return true, nil
	case "g", "home":
		m.scroll = 0
		return true, nil
	case "G", "end":
		m.scroll = m.maxScroll()
		return true, nil
	default:
		return false, nil
	}
}

// ModelID is the load identity last passed to Load.
func (m *Model) ModelID() int { return m.modelID }

// ScrollbarParams reports the renderer inputs the card draws its bar with:
// one row per rendered line, a viewport of height rows. Hosts that want to
// make the bar interactive feed these to ui.RenderScrollbarWithGeometry for
// region registration and ui.OffsetAtRow/RowForOffset for press and drag
// mapping — no scrollbar math lives outside internal/ui.
func (m *Model) ScrollbarParams() ui.ScrollbarParams {
	return ui.ScrollbarParams{
		TotalItems:   len(m.ensureRows()),
		ScrollOffset: m.scroll,
		VisibleItems: m.height,
		TrackHeight:  m.height,
	}
}

// HasScrollbar reports whether the card currently draws a bar (content
// overflows the viewport). When false, hosts must register no regions: the
// reserved column is an anti-jitter spacer, not a control.
func (m *Model) HasScrollbar() bool {
	_, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	return geom.HasThumb
}

// ScrollToOffset pins the viewport at offset, clamped into range by the same
// bounds the renderer maps onto thumb travel. Reports whether it moved.
func (m *Model) ScrollToOffset(offset int) bool {
	params := m.ScrollbarParams()
	offset = min(max(offset, 0), max(params.TotalItems-params.VisibleItems, 0))
	if offset == m.scroll {
		return false
	}
	m.scroll = offset
	m.clampScroll()
	return true
}

// OffsetAtTrackRow maps a track row (pointer Y minus the bar's top) to the
// scroll offset whose thumb top anchors there — the shared core every other
// interactive scrollbar uses for track clicks and drags.
func (m *Model) OffsetAtTrackRow(row int) int {
	return ui.OffsetAtRow(m.ScrollbarParams(), row)
}

func (m *Model) contentWidth() int {
	w := m.innerWidth()
	if m.needsScrollbar() {
		w--
	}
	if w < 0 {
		return 0
	}
	return w
}

func (m *Model) innerWidth() int {
	return max(m.width-m.leftPadding()-m.rightPadding(), 0)
}

func (m *Model) leftPadding() int {
	if m.width <= 0 {
		return 0
	}
	return horizontalContentPadding
}

func (m *Model) rightPadding() int {
	if m.width <= horizontalContentPadding {
		return 0
	}
	return horizontalContentPadding
}

func (m *Model) needsScrollbar() bool {
	return m.width >= 8 && m.height > 0
}

func (m *Model) visibleRows() []row {
	all := m.ensureRows()
	if m.scroll > len(all) {
		m.scroll = max(len(all)-m.height, 0)
	}
	end := m.scroll + m.height
	if end > len(all) {
		end = len(all)
	}
	if m.scroll < 0 || m.scroll > end {
		return nil
	}
	return all[m.scroll:end]
}

func (m *Model) ensureRows() []row {
	if m.loading {
		return []row{
			{text: styles.Muted.Render("Loading note…")},
			{text: styles.Subtle.Render(m.noteID)},
		}
	}
	if m.err != nil {
		return []row{
			{text: styles.ToastError.Render(" Note unavailable ")},
			{text: styles.Subtle.Render(m.noteID)},
			{text: styles.Muted.Render(m.err.Error())},
		}
	}
	if m.data == nil {
		return []row{{text: styles.Muted.Render("No note")}}
	}
	if style := m.renderer.StyleKey(); m.buildFor != m.width || m.buildStyle != style || m.rows == nil {
		m.rows = m.buildRows()
		m.buildFor = m.width
		m.buildStyle = style
	}
	return m.rows
}

func (m *Model) buildRows() []row {
	if m.data == nil {
		return nil
	}
	d := m.data
	width := m.contentWidth()
	var rows []row
	add := func(text string) {
		rows = append(rows, row{text: text})
	}

	title := d.Title
	if title == "" {
		title = d.ID
	}
	add(styles.Title.Render(fitLine(title, width)))
	add(styles.Subtle.Render(d.ID + metaSuffix(d)))
	add("")

	body := d.Content
	if body == "" {
		add(styles.Muted.Render("Empty note"))
		return rows
	}
	if m.renderer != nil {
		for _, line := range m.renderer.RenderContent(body, width) {
			add(line)
		}
		return rows
	}
	for _, line := range strings.Split(body, "\n") {
		add(line)
	}
	return rows
}

func metaSuffix(d *Data) string {
	var parts []string
	if d.Pinned {
		parts = append(parts, "pinned")
	}
	if d.Archived {
		parts = append(parts, "archived")
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " · ")
}

// Scroll moves the viewport by delta rows and clamps it.
func (m *Model) Scroll(delta int) {
	m.scroll += delta
	m.clampScroll()
}

// ScrollOffset is the current (or still-pending restore) viewport offset.
func (m *Model) ScrollOffset() int {
	if m.hasPendingScroll {
		return m.pendingScroll
	}
	return m.scroll
}

// ScrollAtBoundary reports whether delta would leave this viewport unchanged.
func (m *Model) ScrollAtBoundary(delta int) bool {
	if m == nil {
		return true
	}
	return (sharedscroll.Bounds{Position: m.ScrollOffset(), Maximum: m.maxScroll()}).AtBoundary(delta)
}

// SetPendingScroll remembers an offset for the current load generation.
func (m *Model) SetPendingScroll(offset int) {
	m.pendingScroll = max(offset, 0)
	m.hasPendingScroll = true
}

// NoteID returns the note this model is targeting.
func (m *Model) NoteID() string { return m.noteID }

// Title returns the note's headline, or its ID before data arrives.
func (m *Model) Title() string {
	if m.data != nil && m.data.Title != "" {
		return m.data.Title
	}
	return m.noteID
}

// Loading reports whether a fetch is outstanding.
func (m *Model) Loading() bool { return m.loading }

func (m *Model) invalidateRender() {
	m.buildFor = -1
	m.buildStyle = ""
	m.rows = nil
}

func (m *Model) maxScroll() int {
	return max(len(m.ensureRows())-m.height, 0)
}

func (m *Model) clampScroll() {
	m.scroll = min(max(m.scroll, 0), m.maxScroll())
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ui.ExpandTabs(line, tabStopWidth)
	line = ansi.Truncate(line, width, "")
	if padding := width - ansi.StringWidth(line); padding > 0 {
		line += strings.Repeat(" ", padding)
	}
	return line
}

// Heading is the note's chrome headline.
func Heading(d *Data) string {
	if d == nil {
		return ""
	}
	if d.Title != "" {
		return fmt.Sprintf("%s: %s", d.ID, d.Title)
	}
	return d.ID
}
