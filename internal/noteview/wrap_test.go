package noteview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
)

// TestModelWrapsBodyToFullContentWidth pins the card's wrap contract at a
// representative pane width: body text must reach toward the right chrome
// edge, because New pairs the card's own 1+1 inset and reserved scrollbar
// column with a compact-document renderer. Before that pairing a
// host-injected renderer kept Glamour's 2-column document margin, which
// compounded with the inset — body text wrapped several columns before the
// frame's right edge while those columns were free (td-65095b).
func TestModelWrapsBodyToFullContentWidth(t *testing.T) {
	const width = 80
	m := New(nil)
	m.SetSize(width, 24)
	data := &Data{ID: "nt-abc123", Title: "T"}
	// "aaaa" tokens: greedy wrap at the card's content width fills a body line
	// well past any margin-compounded render. Any longest line below the
	// threshold means a hidden inset came back.
	data.Content = stringsRepeat("aaaa ", 60)
	m.Load(1, t.TempDir(), data.ID, 7)
	if !m.SetResult(LoadedMsg{
		ModelID: 1, RequestGeneration: m.requestGeneration, Epoch: 7,
		NoteID: data.ID, Data: data,
	}) {
		t.Fatal("SetResult() = false for the initial load")
	}

	longest := 0
	for _, line := range strings.Split(m.View(), "\n") {
		plain := ansi.Strip(line)
		// Drop the card's left padding column and its trailing scrollbar +
		// right padding columns; what remains is the body the renderer wrapped.
		body := strings.TrimRight(plain[1:len(plain)-2], " ")
		if body == "" {
			continue // title/separator rows
		}
		if strings.HasPrefix(body, " ") {
			t.Fatalf("body row carries an extra indent: %q", body)
		}
		if w := ansi.StringWidth(body); w > longest {
			longest = w
		}
	}
	if threshold := width - 7; longest <= threshold {
		t.Fatalf("longest body line is %d cells, want > %d: body still wraps early",
			longest, threshold)
	}
}

// TestNewRendersHostAndNilRenderersIdentically is the parity contract for the
// surfaces that show a note (workspace leaf, overview preview, app content
// deck): whatever renderer a host injects — including one shared with viewers
// that want Glamour's default inset — the card wraps identically.
func TestNewRendersHostAndNilRenderersIdentically(t *testing.T) {
	host, err := markdown.NewRenderer() // deliberately NOT compact
	if err != nil {
		t.Fatal(err)
	}
	if host.CompactsDocument() {
		t.Fatal("precondition: the host renderer should not be compact")
	}

	data := &Data{
		ID:      "nt-abc123",
		Title:   "Wrap contract",
		Content: "# Heading\n\n" + strings.Repeat("body text ", 40) + "\n\n- one\n- two\n",
	}
	load := func(m *Model) {
		t.Helper()
		m.SetSize(80, 24)
		m.Load(1, t.TempDir(), data.ID, 7)
		if !m.SetResult(LoadedMsg{
			ModelID: 1, RequestGeneration: m.requestGeneration, Epoch: 7,
			NoteID: data.ID, Data: data,
		}) {
			t.Fatal("SetResult() = false for the initial load")
		}
	}

	mHost := New(host)
	mNil := New(nil)
	compact, err := markdown.NewRenderer(markdown.CompactDocument)
	if err != nil {
		t.Fatal(err)
	}
	mCompact := New(compact)
	load(mHost)
	load(mNil)
	load(mCompact)

	want := mCompact.View()
	if got := mHost.View(); got != want {
		t.Fatalf("host-renderer view differs from the compact reference:\n%s", ansi.Strip(got))
	}
	if got := mNil.View(); got != want {
		t.Fatalf("nil-renderer view differs from the compact reference:\n%s", ansi.Strip(got))
	}
	if !mHost.renderer.CompactsDocument() || !mNil.renderer.CompactsDocument() {
		t.Fatal("the card ended up with a non-compact renderer")
	}
	if host.CompactsDocument() {
		t.Fatal("the host's shared renderer was mutated")
	}
}
