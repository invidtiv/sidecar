package tty

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHeadlessPasteBufferNormalizesClientBytes(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"markers", "first\x1b[200~second\x1b[201~third", "firstsecondthird"},
		{"reformed end", "safe\x1b[20\x1b[201~1~tail", "safetail"},
		{"reformed start", "safe\x1b[20\x1b[200~0~tail", "safetail"},
		{"nested removal", "safe\x1b[20\x1b[20\x1b[201~1~0~tail", "safetail"},
		{"line endings", "first\r\nsecond\nthird\rfourth", "first\nsecond\nthird\rfourth"},
		{"reformed CRLF", "first\r\x1b[201~\nsecond", "first\nsecond"},
		{"CR run before LF", "first\r\r\nsecond", "first\nsecond"},
		{"reformed CR run", "first\r\x1b[201~\r\nsecond", "first\nsecond"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, channel := headlessGeometryHarness(t)
			type result struct {
				cleanup func() error
				err     error
			}
			done := make(chan result, 1)
			go func() {
				_, cleanup, err := g.pasteOperation([]byte(tc.input))
				done <- result{cleanup, err}
			}()
			load := waitForControlCommand(t, channel, "load-buffer", 0)
			words := strings.Fields(load.text)
			path := strings.Trim(words[len(words)-1], "'\"")
			data, readErr := os.ReadFile(path)
			respondHeadless(load, nil, nil)
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			cleaned := make(chan error, 1)
			go func() { cleaned <- got.cleanup() }()
			cleanup := waitForControlCommand(t, channel, "delete-buffer", 0)
			respondHeadless(cleanup, nil, nil)
			if err := <-cleaned; err != nil {
				t.Fatal(err)
			}
			if readErr != nil || string(data) != tc.want {
				t.Fatalf("loaded paste bytes = %q, want %q: %v", data, tc.want, readErr)
			}
		})
	}
}

func TestNormalizeHeadlessPasteNestedMaximumPayload(t *testing.T) {
	// 10921 nested wrappers, one marker, and "tail" fill exactly 65536 bytes.
	data := []byte(strings.Repeat("\x1b[20", 10921) + "\x1b[201~" + strings.Repeat("1~", 10921) + "tail")
	if got := NormalizeHeadlessPaste(data); string(got) != "tail" {
		t.Fatalf("deeply nested markers survived: %d bytes", len(got))
	}
	g, channel := headlessGeometryHarness(t)
	if _, _, err := g.pasteOperation([]byte("\x1b[200~\x1b[201~")); err == nil {
		t.Fatal("marker-only paste created a buffer operation")
	}
	if countControlCommands(channel, "load-buffer") != 0 {
		t.Fatal("marker-only paste loaded a buffer")
	}
}

func FuzzNormalizeHeadlessPaste(f *testing.F) {
	for _, seed := range []string{"text\r\nline", "\x1b[200~\x1b[201~", "\x1b[20\x1b[201~1~", "\r\x1b[201~\n", "\x1b[20\x1b[20\x1b[201~1~0~", "\xff\x00\r", "\r\r\n", "\r\x1b[201~\r\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// The independent repeated-replacement oracle is intentionally simple
		// and quadratic on nested payloads; large-input behavior is covered by
		// the maximum-payload stack regression above.
		if len(data) > 2048 {
			t.Skip()
		}
		original := bytes.Clone(data)
		want := string(data)
		markers := strings.NewReplacer("\x1b[200~", "", "\x1b[201~", "")
		for {
			next := markers.Replace(want)
			if next == want {
				break
			}
			want = next
		}
		for {
			next := strings.ReplaceAll(want, "\r\n", "\n")
			if next == want {
				break
			}
			want = next
		}
		got := NormalizeHeadlessPaste(data)
		if string(got) != want || !bytes.Equal(data, original) || !bytes.Equal(got, NormalizeHeadlessPaste(got)) {
			t.Fatalf("normalization input=%q got=%q want=%q", data, got, want)
		}
	})
}
