package markdown

import (
	"log"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"github.com/cespare/xxhash/v2"
)

const (
	// MinWidthForMarkdown is the minimum terminal width for markdown rendering.
	// Below this, falls back to plain text wrapping.
	MinWidthForMarkdown = 30

	// MaxCacheEntries is the maximum number of cached renders before eviction.
	MaxCacheEntries = 100
)

// Renderer wraps Glamour for markdown rendering with caching.
type Renderer struct {
	mu              sync.RWMutex
	renderer        *glamour.TermRenderer
	lastWidth       int
	styleKey        string
	cache           map[uint64][]string
	mappedCache     map[uint64]MappedRender
	compactDocument bool
}

// RendererOption configures a Renderer at construction. Options are applied
// once; the resulting renderer is safe for concurrent use.
type RendererOption func(*Renderer)

// CompactDocument drops Glamour's document margin and block prefix/suffix.
// Hosts that already pad the pane (Notes) use this so the body origin matches
// a plain wrap of the same width — entering edit then changes the cursor,
// not the frame.
func CompactDocument(r *Renderer) {
	r.compactDocument = true
}

// CompactsDocument reports whether this renderer drops Glamour's document
// margin and block prefix/suffix (see CompactDocument). Viewers that pad their
// own frames ask this before adopting an injected renderer: pairing their
// padding with a non-compact renderer compounds two insets onto one width and
// wraps the body well before the frame's right edge. Such a viewer builds its
// own compact renderer instead of flipping this one — the instance may be
// shared with viewers (docview) whose inset IS Glamour's margin.
func (r *Renderer) CompactsDocument() bool { return r.compactDocument }

// NewRenderer creates a new markdown renderer instance.
func NewRenderer(opts ...RendererOption) (*Renderer, error) {
	r := &Renderer{
		cache:       make(map[uint64][]string),
		mappedCache: make(map[uint64]MappedRender),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// StyleKey returns the style key of the current theme snapshot. Consumers that
// keep their own caches above this renderer should record it as a dependency.
func (r *Renderer) StyleKey() string {
	return r.effectiveStyleKey(CurrentThemeSnapshot().StyleKey())
}

func (r *Renderer) effectiveStyleKey(base string) string {
	if r.compactDocument {
		return base + "|compact-doc"
	}
	return base
}

// RenderContent renders markdown content to styled lines using the current
// theme snapshot.
func (r *Renderer) RenderContent(content string, width int) []string {
	return r.renderContent(content, width, CurrentThemeSnapshot())
}

// renderContent renders against one explicit theme snapshot so a concurrent
// theme change cannot mix one palette's cache key with another palette's style.
func (r *Renderer) renderContent(content string, width int, snapshot ThemeSnapshot) []string {
	if width < MinWidthForMarkdown {
		return WrapText(content, width)
	}

	if content == "" {
		return []string{}
	}

	styleKey := r.effectiveStyleKey(snapshot.StyleKey())
	key := r.cacheKey(content, width, styleKey)

	// Check cache first (read lock)
	r.mu.RLock()
	if cached, ok := r.cache[key]; ok {
		r.mu.RUnlock()
		return cached
	}
	r.mu.RUnlock()

	// Need to render (write lock)
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check cache after acquiring write lock
	if cached, ok := r.cache[key]; ok {
		return cached
	}

	// Get or create renderer for this width
	renderer, err := r.getOrCreateRenderer(width, snapshot, styleKey)
	if err != nil {
		log.Printf("glamour renderer error: %v", err)
		return WrapText(content, width)
	}

	// Render markdown
	rendered, err := renderer.Render(content)
	if err != nil {
		log.Printf("glamour render error: %v", err)
		return WrapText(content, width)
	}

	// Trim trailing whitespace and split into lines
	rendered = strings.TrimRight(rendered, "\n\r\t ")
	lines := strings.Split(rendered, "\n")

	// Cache eviction if needed
	if len(r.cache) >= MaxCacheEntries {
		r.cache = make(map[uint64][]string)
	}
	r.cache[key] = lines

	return lines
}

// cacheKey generates a cache key from content, width, and the active style key.
func (r *Renderer) cacheKey(content string, width int, styleKey string) uint64 {
	h := xxhash.New()
	_, _ = h.WriteString(content)
	_, _ = h.Write([]byte{byte(width >> 8), byte(width)})
	_, _ = h.WriteString("\x00")
	_, _ = h.WriteString(styleKey)
	return h.Sum64()
}

// getOrCreateRenderer lazily creates or recreates the renderer for the given
// width and theme snapshot. Must be called with write lock held.
func (r *Renderer) getOrCreateRenderer(width int, snapshot ThemeSnapshot, styleKey string) (*glamour.TermRenderer, error) {
	if r.renderer != nil && r.lastWidth == width && r.styleKey == styleKey {
		return r.renderer, nil
	}

	// Width or theme changed (or first use) - rebuild and clear caches
	style, _ := BuildStyle(snapshot)
	if r.compactDocument {
		style = compactDocumentStyle(style)
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}

	r.renderer = renderer
	r.lastWidth = width
	r.styleKey = styleKey
	r.cache = make(map[uint64][]string) // Clear caches on width/theme change
	r.mappedCache = make(map[uint64]MappedRender)

	return renderer, nil
}

// WrapText wraps text to fit within maxWidth.
// Used as fallback when terminal is too narrow for markdown rendering.
func WrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{text}
	}

	// Replace newlines with spaces for simpler wrapping
	text = strings.ReplaceAll(text, "\n", " ")

	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return lines
	}

	currentLine := words[0]
	for _, word := range words[1:] {
		if len(currentLine)+1+len(word) <= maxWidth {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}
