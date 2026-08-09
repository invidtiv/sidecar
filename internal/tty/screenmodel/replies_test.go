package screenmodel

import (
	"runtime"
	"testing"
	"time"
)

// These are the exact query sequences a real full-screen application emits
// before its first paint. Without the reply drain, the first of them blocks
// Emulator.Write forever — and in slice 1's transport that write happens on the
// control client's single ordered actor, so the pane freezes with no error and
// no fallback.
var deviceQueries = []struct {
	name string
	// bytes are written as "before" + bytes + "after".
	bytes string
	// wantRow0 is the resulting top row. It differs only for the nvim burst,
	// which also clears the screen.
	wantRow0 string
}{
	{"DSR operating status", "\x1b[5n", "beforeafter"},
	{"DSR cursor position", "\x1b[6n", "beforeafter"},
	{"primary device attributes", "\x1b[c", "beforeafter"},
	{"secondary device attributes", "\x1b[>c", "beforeafter"},
	{"DECRQM 69", "\x1b[?69$p", "beforeafter"},
	{"DECRQM 2026", "\x1b[?2026$p", "beforeafter"},
	{"OSC 11 background query", "\x1b]11;?\x07", "beforeafter"},
	{"nvim's opening burst", "\x1b[?1049h\x1b[?1h\x1b=\x1b[H\x1b[J\x1b[?2004h" +
		"\x1b[?69$p\x1b[?2026$p\x1b[?2027$p\x1b[?2031$p\x1b[?2048$p\x1b[?u\x1b]11;?\x07\x1b[5n",
		// ESC[H ESC[J in the same burst clears "before" off the screen.
		"after      "},
}

func TestDeviceQueriesDoNotBlockTheModel(t *testing.T) {
	for _, tc := range deviceQueries {
		t.Run(tc.name, func(t *testing.T) {
			m := New(80, 24)
			defer m.Close()
			done := make(chan error, 1)
			go func() { done <- m.Write([]byte("before" + tc.bytes + "after")) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("write: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Emulator.Write blocked: the reply pipe is not being drained")
			}
			frame, err := m.Frame()
			if err != nil {
				t.Fatal(err)
			}
			// The query itself must leave nothing on the screen.
			got := ""
			for _, cell := range frame.Cells[0][:len(tc.wantRow0)] {
				got += cell.Grapheme
			}
			if got != tc.wantRow0 {
				t.Errorf("row 0 = %q, want %q", got, tc.wantRow0)
			}
		})
	}
}

// Reseeding rebuilds the emulator. Each rebuild must release the previous one
// and its drain goroutine, or a long-lived pane leaks one goroutine and one
// 4 MB parser buffer per resync.
func TestReseedingDoesNotLeakDrainGoroutines(t *testing.T) {
	m := New(40, 10)
	if err := m.Write([]byte("warm up\x1b[5n")); err != nil {
		t.Fatal(err)
	}
	settle := func() int {
		for range 20 {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()
	for range 50 {
		if err := m.Seed(Seed{Output: "seeded", Width: 40, Height: 10}); err != nil {
			t.Fatal(err)
		}
		if err := m.Write([]byte("\x1b[6n\x1b[c")); err != nil {
			t.Fatal(err)
		}
	}
	after := settle()
	if after > before+5 {
		t.Errorf("goroutines grew across 50 reseeds: %d -> %d", before, after)
	}
	m.Close()
	if final := settle(); final > before+2 {
		t.Errorf("goroutines still elevated after Close: %d -> %d", before, final)
	}
}

// Close must release a model whose application left a query unanswered, and it
// must stay idempotent.
func TestCloseReleasesAModelThatEmittedReplies(t *testing.T) {
	m := New(20, 5)
	if err := m.Write([]byte("\x1b[c\x1b[5n\x1b[6n")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		m.Close()
		m.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on a model that had emitted replies")
	}
}
