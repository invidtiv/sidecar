package app

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/overlay"
	"github.com/marcus/sidecar/internal/reveal"
	"github.com/marcus/sidecar/internal/styles"
)

// Status flashes are the second tier of the notification system: a single line
// in the top-right of the content region — the same corner as a toast, for
// spatial consistency — that fades in, holds, and fades out. Nothing about a
// flash is stored: it never reaches the notification store, never appears in
// the centre, and never counts in the header. That is the whole distinction
// between the tiers, and it is why this file has no contact with notify.Store.
//
// Flashes replace, never queue: a new flash supersedes the one on screen
// immediately. Toast stacking (Phase 3) is a separate mechanism and is
// deliberately not pulled forward here.

const (
	// flashStep is one animation frame. It matches design 1h's ~90ms cadence,
	// so the two motions in the system move at one speed.
	flashStep = 90 * time.Millisecond
	// flashFadeSteps is how many interpolation steps the line takes in and out.
	// Three is the top of the 2–3 the spec allows: enough to read as a fade,
	// short enough that the line is legible within a quarter of a second.
	flashFadeSteps = 3
	// flashHold is how long the line sits at full strength between the fades.
	flashHold = 2 * time.Second
	// flashMaxWidth bounds the line on a wide terminal so it stays a corner
	// note rather than a banner.
	flashMaxWidth = 60
	// flashMinWidth is the narrowest content region worth drawing one in.
	flashMinWidth = 16
	// flashMarginX is the gap between the line and the content's right edge —
	// the toast's margin, so the two tiers line up in the same corner.
	flashMarginX = 1
)

// flashHoldFrames is flashHold expressed in animation frames.
var flashHoldFrames = int(flashHold / flashStep)

// flashState is the one flash on screen. Its zero value is "nothing showing".
type flashState struct {
	active   bool
	text     string
	source   notify.SourceID
	severity notify.Severity
	// frame counts animation steps since the flash appeared. On a terminal
	// where motion is skipped it stays at the first hold frame until the
	// flash's single expiry tick arrives.
	frame int
	// seq tags the ticks belonging to this flash. A tick carrying a stale seq
	// is a tick for a flash that has already been replaced, and is dropped —
	// the same tagging every other timer in the app uses, so a burst of
	// flashes cannot leave several animations fighting over one line.
	seq int
}

// flashTickMsg advances the flash animation. It is tagged with the flash it
// belongs to.
type flashTickMsg struct{ seq int }

// flashTick schedules the next animation frame for seq.
func flashTick(seq int, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return flashTickMsg{seq: seq} })
}

// showFlash puts a flash on screen, replacing whatever was there.
func (m *Model) showFlash(in FlashMsg) tea.Cmd {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil
	}
	source := notify.SourceID(strings.TrimSpace(in.Source))
	if source == "" {
		source = notify.SourceSystem
	}
	severity := notify.SeverityInfo
	if in.IsError {
		severity = notify.SeverityError
	}
	seq := m.flash.seq + 1
	m.flash = flashState{
		active:   true,
		text:     text,
		source:   source,
		severity: severity,
		seq:      seq,
	}
	if !flashAnimated() {
		// Degraded mode (design 1h): the line appears, stays, and disappears.
		// One timer instead of ~30, and no colour interpolation at all.
		m.flash.frame = flashFadeSteps
		return flashTick(seq, flashHold)
	}
	return flashTick(seq, flashStep)
}

// advanceFlash handles a flash tick: it drops stale ticks, advances the frame,
// and retires the flash when its last frame has been drawn.
func (m *Model) advanceFlash(tick flashTickMsg) tea.Cmd {
	if !m.flash.active || tick.seq != m.flash.seq {
		return nil
	}
	if !flashAnimated() {
		m.flash = flashState{seq: m.flash.seq}
		return nil
	}
	m.flash.frame++
	if m.flash.frame >= flashTotalFrames() {
		m.flash = flashState{seq: m.flash.seq}
		return nil
	}
	return flashTick(m.flash.seq, flashStep)
}

// flashTotalFrames is the length of the whole animation in frames.
func flashTotalFrames() int {
	return flashFadeSteps + flashHoldFrames + flashFadeSteps
}

// flashAlpha is how present the line is on a given frame, from 0 (invisible)
// to 1 (full strength). The fade is real colour interpolation between the
// content background and the theme's text/source colours — not a dimmed style
// switch — which is what makes it read as motion rather than a blink.
func flashAlpha(frame int) float64 {
	if frame < 0 {
		return 0
	}
	switch {
	case frame < flashFadeSteps:
		return float64(frame+1) / float64(flashFadeSteps+1)
	case frame < flashFadeSteps+flashHoldFrames:
		return 1
	case frame < flashTotalFrames():
		out := frame - flashFadeSteps - flashHoldFrames
		return float64(flashFadeSteps-out) / float64(flashFadeSteps+1)
	}
	return 0
}

// flashAnimated reports whether this terminal gets the fade. The check itself
// now lives in internal/reveal, which is the shared home for "may this terminal
// have motion at all": the toast reveal asks the same question, and two copies
// of it could answer differently.
func flashAnimated() bool { return reveal.Animated() }

// blendColor interpolates from base toward toward by alpha, in plain sRGB. It
// is deliberately simple: two or three steps between a background and a
// foreground do not need a perceptual space, and the theme's colours are the
// endpoints either way.
func blendColor(base, toward color.Color, alpha float64) color.Color {
	if alpha >= 1 {
		return toward
	}
	if alpha <= 0 {
		return base
	}
	br, bg, bb, _ := base.RGBA()
	tr, tg, tb, _ := toward.RGBA()
	mix := func(from, to uint32) uint8 {
		v := float64(from>>8) + (float64(to>>8)-float64(from>>8))*alpha
		return uint8(max(0, min(255, int(v+0.5))))
	}
	return color.RGBA{R: mix(br, tr), G: mix(bg, tg), B: mix(bb, tb), A: 0xff}
}

// renderFlashLine draws the flash at a given width: the source glyph in its
// hue, then the text, both interpolated toward the background by the current
// alpha. It returns "" when there is nothing to draw.
func (m Model) renderFlashLine(width int) string {
	if !m.flash.active || width < flashMinWidth {
		return ""
	}
	alpha := flashAlpha(m.flash.frame)
	if alpha <= 0 {
		return ""
	}
	// The content region's background is the fade's floor: at alpha 0 the line
	// is the background it sits on, which is what "faded out" means.
	bg := color.Color(styles.BgPrimary)
	hue := blendColor(bg, notify.ChromeColor(m.flash.source, m.flash.severity), alpha)
	text := blendColor(bg, styles.TextPrimary, alpha)

	glyph := lipgloss.NewStyle().Foreground(hue).Render(notify.Glyph(m.flash.source))
	body := ansi.Truncate(m.flash.text, max(1, width-lipgloss.Width(glyph)-1), "…")
	return glyph + " " + lipgloss.NewStyle().Foreground(text).Render(body)
}

// renderFlashOverlay composites the flash onto an already-rendered screen, in
// the top-right of the content region. x0/y0/width/height describe that region
// — the same box the toast is placed against, so a narrowed content region
// (the notification centre open) moves both inward together.
//
// When a toast is on screen the flash sits directly under it rather than over
// it: they share a corner by design, and two tiers of feedback must not
// obscure each other. The toast is the louder tier, so it keeps the top row.
func (m Model) renderFlashOverlay(screen string, x0, y0, width, height int) string {
	if height <= 0 || width <= 0 || m.overlaysSuppressed() {
		return screen
	}
	line := m.renderFlashLine(min(flashMaxWidth, width-2*flashMarginX))
	if line == "" {
		return screen
	}
	y := y0 + toastMarginY
	if toast := m.toastOverlayHeight(width); toast > 0 {
		y += toast
	}
	if y >= y0+height {
		return screen
	}
	x := x0 + width - flashMarginX - lipgloss.Width(line)
	if x < x0 {
		x = x0
	}
	return overlay.Composite(screen, line, x, y)
}

// toastOverlayHeight is how many rows the current toast occupies in the
// content region, or 0 when no toast is drawn. It answers only "how much of
// the corner is taken", so the flash can sit below it.
func (m Model) toastOverlayHeight(width int) int {
	if width < toastMinWidth+2*toastMarginX {
		return 0
	}
	total := 0
	// The reveal machine's column, same as the renderer's: the flash must sit
	// below what is actually painted, including a block mid-retraction.
	for _, r := range m.toastColumnBlocks() {
		block := r.state.Clip(r.block)
		if block == "" {
			continue
		}
		total += lipgloss.Height(block) + toastGapY
	}
	return total
}
