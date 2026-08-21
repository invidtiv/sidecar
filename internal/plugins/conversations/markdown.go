package conversations

import "github.com/marcus/sidecar/internal/markdown"

// GlamourRenderer is an alias to the shared markdown renderer.
type GlamourRenderer = markdown.Renderer

// NewGlamourRenderer creates a new renderer instance.
//
// Conversations pairs this renderer with its own indents everywhere it draws
// — the message-bubble path prefixes four spaces, the detail pane writes
// rows inside already-inset chrome — so it asks for a compact document:
// Glamour's default 2-column margin would compound with those insets and
// wrap message text several columns before the pane's right edge
// (td-65095b).
func NewGlamourRenderer() (*GlamourRenderer, error) {
	return markdown.NewRenderer(markdown.CompactDocument)
}

// wrapText wraps text to fit within maxWidth.
func wrapText(text string, maxWidth int) []string {
	return markdown.WrapText(text, maxWidth)
}
