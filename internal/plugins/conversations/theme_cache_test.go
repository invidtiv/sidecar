package conversations

import (
	"testing"

	"github.com/marcus/sidecar/internal/markdown"
)

// Message bodies are markdown rendered through the shared renderer, so their
// cache entries are only valid for the theme they were rendered under.
func TestRenderCacheIsScopedToMarkdownStyle(t *testing.T) {
	p := New()
	p.setCachedRender("msg-1", 80, false, "current-theme ansi")

	got, ok := p.getCachedRender("msg-1", 80, false)
	if !ok || got != "current-theme ansi" {
		t.Fatalf("cache miss under the theme it was stored in: %q ok=%v", got, ok)
	}

	style := markdown.CurrentThemeSnapshot().StyleKey()
	if style == "" {
		t.Fatal("expected a non-empty markdown style key")
	}

	// An entry stored under a different theme must not be served.
	p.clearRenderCache()
	p.renderCache[renderCacheKey{messageID: "msg-1", width: 80, expanded: false, styleKey: "stale-theme"}] = "stale ansi"
	if got, ok := p.getCachedRender("msg-1", 80, false); ok {
		t.Fatalf("stale-theme entry was served: %q", got)
	}
}
