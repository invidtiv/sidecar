package filefind

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

func TestBestAlignmentRanking(t *testing.T) {
	for _, tc := range []struct {
		name, query, winner, loser string
	}{
		{"basename", "dsk", "docs/design/desktop-shell.md", "docs/sidekick.md"},
		{"contiguous", "readme", "README.md", "docs/readers/me.go"},
		{"directory", "uiapi", "internal/uiapi/server.go", "utilities/input/adapters/pipeline.go"},
		{"segments", "uiapi/ser", "internal/uiapi/server.go", "utilities/input/adapters/pipeline/server.go"},
		{"case", "ReAdMe", "README.md", "docs/readers/me.go"},
		{"unicode", "CAFÉ", "文档/café-menu.md", "文档/ca/fé.md"},
		{"camel", "fbp", "internal/FileBrowserPlugin.go", "internal/xfbxxpx.go"},
		{"basename start", "view", "internal/view.go", "internal/preview.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches := FuzzyFilter([]string{tc.loser, tc.winner}, tc.query, 2)
			if len(matches) != 2 || matches[0].Path != tc.winner {
				t.Fatalf("%q: %+v, want %q first", tc.query, matches, tc.winner)
			}
		})
	}
}

func TestBestAlignmentHighlights(t *testing.T) {
	for _, tc := range []struct {
		query, path, marked string
		positions           []int
	}{
		{"dsk", "docs/design/desktop-shell.md", "docs/design/[d]e[sk]top-shell.md", []int{12, 14, 15}},
		{"dk", "docs/design/desktop-shell.md", "docs/design/[d]es[k]top-shell.md", []int{12, 15}},
		{"readme", "README.md", "[README].md", []int{0, 1, 2, 3, 4, 5}},
		{"uiapi/ser", "internal/uiapi/server.go", "internal/[uiapi/ser]ver.go", []int{9, 10, 11, 12, 13, 14, 15, 16, 17}},
		{"dsk", "文档/desktop-shell.md", "文档/[d]e[sk]top-shell.md", []int{3, 5, 6}},
		{"dk", "文档/desktop-shell.md", "文档/[d]es[k]top-shell.md", []int{3, 6}},
		{"CAFÉ", "文档/café-menu.md", "文档/[café]-menu.md", []int{3, 4, 5, 6}},
	} {
		t.Run(tc.path+"/"+tc.query, func(t *testing.T) {
			f := NewFinder(&Cache{Files: []string{tc.path}, OK: true}, "/root", 1)
			f.SetQuery(tc.query)
			if len(f.Matches()) != 1 {
				t.Fatal("no match")
			}
			match := f.Matches()[0]
			var positions []int
			n := 0
			for off := range tc.path {
				for _, r := range match.MatchRanges {
					if off >= r.Start && off < r.End {
						positions = append(positions, n)
						break
					}
				}
				n++
			}
			if !reflect.DeepEqual(positions, tc.positions) {
				t.Errorf("positions = %v, want %v", positions, tc.positions)
			}
			// Both the single-row renderer and modal list must preserve every
			// matched character, including isolated letters inside a word.
			text, ranges := elideMatch(match, 100)
			mark := func(s string) string { return "[" + s + "]" }
			if got := highlightRanges(text, ranges, nil, mark); got != tc.marked {
				t.Errorf("row highlights = %s, want %s", got, tc.marked)
			}
			row := elideMatches(f.Matches(), 100)[0]
			if got := highlightRanges(row.text, row.ranges, nil, mark); got != tc.marked {
				t.Errorf("modal highlights = %s, want %s", got, tc.marked)
			}
			score, _ := FuzzyMatch(tc.query, tc.path)
			var m matcher
			m.setQuery(tc.query)
			if score != m.scoreOnly(tc.path) {
				t.Fatal("score and highlight passes disagree")
			}
		})
	}
}

// Exhaustive enumeration is deliberately independent of the DP recurrence.
// It verifies both the maximum score and the score of the returned alignment
// on repeated letters, boundaries and camel humps, where greedy choices fail.
func TestBestAlignmentMatchesExhaustiveSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(315))
	alphabet := []rune("abAB/._-éÉ")
	for trial := 0; trial < 3000; trial++ {
		path := make([]rune, 3+rng.Intn(10))
		for i := range path {
			path[i] = alphabet[rng.Intn(len(alphabet))]
		}
		query := make([]rune, 1+rng.Intn(5))
		for i := range query {
			query[i] = unicode.ToLower(alphabet[rng.Intn(len(alphabet))])
		}
		base := 0
		for i, r := range path {
			if r == '/' {
				base = i + 1
			}
		}
		alignmentScore := func(positions []int) int {
			score := 0
			for i, pos := range positions {
				prev := rune('/')
				if pos > 0 {
					prev = path[pos-1]
				}
				bonus := positionBonus(prev, path[pos])
				if i == 0 {
					bonus *= bonusFirstCharMultiplier
				}
				score += scoreMatch + bonus
				if pos >= base {
					score += bonusBasename
				}
				if i > 0 {
					gap := pos - positions[i-1] - 1
					if gap == 0 {
						score += bonusConsecutive
					} else {
						score += scoreGapStart + (gap-1)*scoreGapExtend
					}
				}
			}
			depth := min(strings.Count(string(path), "/"), penaltyDepthMax)
			return max(1, score-depth*penaltyDepth)
		}
		best := 0
		var enumerate func([]int, int)
		enumerate = func(positions []int, start int) {
			if len(positions) == len(query) {
				best = max(best, alignmentScore(positions))
				return
			}
			for pos := start; pos < len(path); pos++ {
				if runesEqual(query[len(positions)], unicode.ToLower(path[pos])) {
					enumerate(append(positions, pos), pos+1)
				}
			}
		}
		enumerate(nil, 0)
		var m matcher
		m.setQuery(string(query))
		got, _ := m.match(string(path))
		if got != best {
			t.Fatalf("%q on %q: score %d, exhaustive maximum %d", string(query), string(path), got, best)
		}
		if got > 0 && alignmentScore(m.positions) != got {
			t.Fatalf("%q on %q: positions %v do not earn score %d", string(query), string(path), m.positions, got)
		}
		if score := m.scoreOnly(string(path)); score != got {
			t.Fatalf("score-only %d differs from traceback %d", score, got)
		}
	}
}

// Run on the parent and changed revision with identical corpus and query
// prefixes to compare whole-keystroke latency, including top-K selection.
func BenchmarkBestAlignment50k(b *testing.B) {
	files := syntheticTree(50_000)
	for _, query := range []string{"d", "ds", "dsk", "u", "ui", "uia", "uiap", "uiapi", "uiapi/ser", "r", "re", "read", "readme"} {
		b.Run(query, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				FuzzyFilter(files, query, MaxMatches+1)
			}
		})
	}
	// Dense repeats exercise many competing alignments rather than relying
	// on a mostly non-matching corpus to make the DP look fast.
	for i := range files {
		files[i] = "docs/design/" + strings.Repeat("desktop-shell/", 4) + "desktop-shell.md"
	}
	b.Run("dense-dsk", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			FuzzyFilter(files, "dsk", MaxMatches+1)
		}
	})
}
