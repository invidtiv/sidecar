package filefind

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// The clara-home corpus, as the finder saw it on 2026-09-22 when "use-cases"
// returned six near-misses. The file that was missing then is here now; these
// tests are about what the ranking does once it is.
func claraHomeSample() []string {
	return []string{
		"adapters/recall-go/claracorpus/conformance/cancel-inflight/fixture/signals-archive.jsonl",
		"adapters/recall-go/claracorpus/conformance/cancel-inflight/fixture/state.json",
		"adapters/recall-go/claracorpus/conformance/version-rejection/fixture/signals-archive.jsonl",
		"bin/brief",
		"briefs/2026/08/19/2026-08-19-joe-td-sync/BRIEF.md",
		"briefs/2026/08/24/2026-08-24-dad-td-sync/BRIEF.md",
		"briefs/2026/09/22/2026-09-22-jev-system-one-local-trials/BRIEF.md",
		"briefs/2026/09/22/2026-09-22-jev-system-one-local-trials/USE-CASES.md",
		"briefs/pinned/2026-08-25-backblaze-removal/BRIEF.md",
		"eval/recall/packs/firstuse/cases.jsonl",
		"eval/recall/packs/firstuse/sources/clara-home/research/retina-specialists.md",
		"projects/sidecar-launch-video/assets/logos/claude-code/official/Claude Code logo - Slate.svg",
		"projects/sidecar-launch-video/assets/logos/claude-code/official/ClaudeIcon-Square.svg",
		"projects/sidecar-launch-video/assets/logos/grok/official/spacexai-symbol-black-transparent.svg",
		"projects/sidecar-launch-video/assets/logos/grok/official/spacexai-symbol-white.png",
		"projects/sidecar-launch-video/assets/logos/grok/official/spacexai-wordmark-black-transparent.svg",
	}
}

func topPath(t *testing.T, files []string, query string) string {
	t.Helper()
	matches := FuzzyFilter(files, query, 5)
	if len(matches) == 0 {
		t.Fatalf("%q matched nothing", query)
	}
	return matches[0].Path
}

func TestRankExactBasenameWinsFromAnyDepth(t *testing.T) {
	files := claraHomeSample()
	want := "briefs/2026/09/22/2026-09-22-jev-system-one-local-trials/USE-CASES.md"
	for _, query := range []string{"use-cases", "USE-CASES", "use_cases", "use cases", "usecases", "use-cases.md"} {
		if got := topPath(t, files, query); got != want {
			t.Errorf("%q ranked %q first, want %q", query, got, want)
		}
	}
}

// The greedy matcher aligned "cases" to the first c, a, s, e, s it met, which
// in a long path is three directories' worth of stray letters, and scored the
// row by that. The alignment that wins now is the word.
func TestRankPrefersTheWordOverScatteredLetters(t *testing.T) {
	path := "eval/recall/packs/firstuse/cases.jsonl"
	score, ranges := FuzzyMatch("cases", path)
	if score == 0 {
		t.Fatal("no match")
	}
	if len(ranges) != 1 || path[ranges[0].Start:ranges[0].End] != "cases" {
		t.Errorf("ranges = %v, want the single word %q", ranges, "cases")
	}

	// And the row outranks a file that only has the letters scattered.
	files := claraHomeSample()
	if got := topPath(t, files, "cases"); got != path {
		t.Errorf("\"cases\" ranked %q first, want %q", got, path)
	}
}

func TestRankMultipleTermsAllMustMatch(t *testing.T) {
	files := claraHomeSample()
	if got := FuzzyFilter(files, "brief nothere", 5); len(got) != 0 {
		t.Errorf("a term that matches nothing still produced %v", got)
	}
	// Terms match independently and in any order.
	matches := FuzzyFilter(files, "md jev", 5)
	for _, m := range matches {
		if !strings.Contains(m.Path, "jev") || !strings.HasSuffix(m.Path, ".md") {
			t.Errorf("%q does not satisfy both terms", m.Path)
		}
	}
	if len(matches) != 2 {
		t.Errorf("got %d matches for \"md jev\", want 2: %v", len(matches), matches)
	}
}

func TestRankShallowerPathBreaksTies(t *testing.T) {
	files := claraHomeSample()
	if got := topPath(t, files, "brief"); got != "bin/brief" {
		t.Errorf("\"brief\" ranked %q first, want the shallow bin/brief", got)
	}
}

func TestRankBasenameBeatsDirectory(t *testing.T) {
	files := []string{"test/something.go", "src/test.go"}
	if got := topPath(t, files, "test"); got != "src/test.go" {
		t.Errorf("ranked %q first, want the basename match", got)
	}
}

func TestRankBoundaryStartBeatsMidWord(t *testing.T) {
	files := []string{"internal/app/preview.go", "internal/app/view.go"}
	if got := topPath(t, files, "view"); got != "internal/app/view.go" {
		t.Errorf("ranked %q first, want the word at the start of a basename", got)
	}
}

func TestRankCamelCaseHumps(t *testing.T) {
	score, ranges := FuzzyMatch("fbp", "internal/plugins/FileBrowserPlugin.go")
	if score == 0 {
		t.Fatal("no match")
	}
	var got []string
	path := "internal/plugins/FileBrowserPlugin.go"
	for _, r := range ranges {
		got = append(got, path[r.Start:r.End])
	}
	if strings.Join(got, "") != "FBP" {
		t.Errorf("highlighted %v, want the three humps", got)
	}
}

func TestRankSeparatorsAreInterchangeable(t *testing.T) {
	for _, query := range []string{"quick-open", "quick_open", "quick open"} {
		score, _ := FuzzyMatch(query, "docs/quick_open.md")
		if score == 0 {
			t.Errorf("%q did not match docs/quick_open.md", query)
		}
	}
	// A dot is not a separator the user would swap: an extension is typed as
	// typed.
	if score, _ := FuzzyMatch("main-go", "main.go"); score != 0 {
		t.Error("'-' matched '.'")
	}
}

func TestRankIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	files := claraHomeSample()
	want := FuzzyFilter(files, "brief", 50)

	shuffled := append([]string(nil), files...)
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 10; trial++ {
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := FuzzyFilter(shuffled, "brief", 50)
		if len(got) != len(want) {
			t.Fatalf("trial %d: %d matches, want %d", trial, len(got), len(want))
		}
		for i := range want {
			if got[i].Path != want[i].Path {
				t.Fatalf("trial %d: row %d = %q, want %q", trial, i, got[i].Path, want[i].Path)
			}
		}
	}
}

func TestRankRecentLeadsEmptyQueryAndBreaksTies(t *testing.T) {
	files := []string{"a/one.go", "a/two.go", "b/three.go", "README.md"}
	recent := []string{"b/three.go", "gone.go", "a/two.go"}

	empty := Filter(files, "", 10, FilterOptions{Recent: recent})
	got := make([]string, len(empty))
	for i, m := range empty {
		got[i] = m.Path
	}
	want := []string{"b/three.go", "a/two.go", "README.md", "a/one.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("empty query = %v, want %v", got, want)
	}

	// An equal match: recency decides.
	tie := Filter([]string{"a/one.go", "a/two.go"}, "a/", 10, FilterOptions{Recent: []string{"a/two.go"}})
	if tie[0].Path != "a/two.go" {
		t.Errorf("tie went to %q, want the recent file", tie[0].Path)
	}
	// A clearly better match: recency does not.
	better := Filter([]string{"a/one.go", "notes/o.txt"}, "one", 10, FilterOptions{Recent: []string{"notes/o.txt"}})
	if better[0].Path != "a/one.go" {
		t.Errorf("recency outranked the real match: %q", better[0].Path)
	}
}

func TestRankEmptyQueryShowsShallowestFirst(t *testing.T) {
	files := []string{"z/deep/file.go", "b.go", "a/mid.go", "a.go"}
	matches := FuzzyFilter(files, "", 3)
	got := make([]string, len(matches))
	for i, m := range matches {
		got[i] = m.Path
	}
	if strings.Join(got, ",") != "a.go,b.go,a/mid.go" {
		t.Errorf("empty query = %v", got)
	}
}

func TestRankUnicodeRangesAreByteOffsets(t *testing.T) {
	path := "docs/café-menu.md"
	score, ranges := FuzzyMatch("menu", path)
	if score == 0 {
		t.Fatal("no match")
	}
	if len(ranges) != 1 || path[ranges[0].Start:ranges[0].End] != "menu" {
		t.Errorf("ranges = %v, want the bytes of %q", ranges, "menu")
	}
	if score, _ := FuzzyMatch("café", path); score == 0 {
		t.Error("a non-ASCII query did not match")
	}
	if score, _ := FuzzyMatch("CAFÉ", path); score == 0 {
		t.Error("case folding is ASCII-only")
	}
}

func TestRankQueryLongerThanTargetDoesNotMatch(t *testing.T) {
	if score, _ := FuzzyMatch("main.go.bak", "main.go"); score != 0 {
		t.Error("a query longer than the path matched it")
	}
}

// syntheticTree is a 50k-file tree in the shape of a large monorepo, for the
// benchmarks: the matcher runs on every keystroke, so its cost against the cap
// the scanner allows is what the finder's latency is.
func syntheticTree(n int) []string {
	rng := rand.New(rand.NewSource(42))
	words := []string{"internal", "app", "plugins", "filebrowser", "workspace", "overview", "model", "view", "update", "keys", "mouse", "render", "cache", "scan", "search", "finder", "tree", "preview", "tabs", "git", "status", "diff", "blame", "config", "theme", "styles", "modal", "layout", "pane", "frame", "live", "watch", "session", "shell", "agent", "control", "remote", "host", "catalog", "repo"}
	exts := []string{".go", "_test.go", ".md", ".json", ".yaml", ".txt", ".svg", ".png"}
	files := make([]string, 0, n)
	for len(files) < n {
		depth := 1 + rng.Intn(6)
		var sb strings.Builder
		for d := 0; d < depth; d++ {
			sb.WriteString(words[rng.Intn(len(words))])
			sb.WriteByte('/')
		}
		sb.WriteString(words[rng.Intn(len(words))])
		if rng.Intn(2) == 0 {
			sb.WriteByte('_')
			sb.WriteString(words[rng.Intn(len(words))])
		}
		sb.WriteString(exts[rng.Intn(len(exts))])
		files = append(files, sb.String())
	}
	return files
}

func BenchmarkFilter50k(b *testing.B) {
	files := syntheticTree(MaxFiles)
	for _, query := range []string{"v", "vi", "view", "fbp", "workspace view", "filebrowser_test"} {
		b.Run(fmt.Sprintf("q=%s", strings.ReplaceAll(query, " ", "+")), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				FuzzyFilter(files, query, MaxMatches+1)
			}
		})
	}
	b.Run("q=empty", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			FuzzyFilter(files, "", MaxMatches+1)
		}
	})
}
