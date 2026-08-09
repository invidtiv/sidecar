package screenmodel

import (
	"sync/atomic"

	"github.com/charmbracelet/x/vt"
)

// replyDrain consumes the emulator's reply stream.
//
// WHY THIS EXISTS. `vt.Emulator` answers device queries — DSR (`CSI 5 n`,
// `CSI 6 n`), primary and secondary device attributes, DECRQM mode reports,
// OSC 10/11/12 colour queries, in-band resize — by writing to an **unbuffered
// io.Pipe** that `Emulator.Read` is the only drain for. Nothing in the emulator
// buffers those bytes. If no goroutine is reading, the very first query makes
// `Emulator.Write` block forever, in the middle of the byte stream, holding
// whatever goroutine fed it.
//
// That is not a corner case. Every full-screen application Sidecar hosts emits
// such a query in its first paint: nvim sends `CSI ? 69 $p`, `CSI ? 2026 $p`,
// `OSC 11 ; ? BEL` and `CSI 5 n` before drawing a single cell. In slice 1's
// design the writer is the control client's single ordered actor, so the
// deadlock stops that client's entire event loop: no more pane bytes, no more
// capture responses, and — because the reader goroutine then fills its event
// channel and blocks too — the pane simply freezes for the user with no error,
// no fallback, and no diagnostic.
//
// The replies are **discarded, never forwarded**. tmux is the real terminal for
// this pane and has already answered the application's query itself; injecting
// a second answer into the pane's input would corrupt the application's parser.
// Sidecar's model is a passive observer of the byte stream, so the correct
// behavior for anything it "would have replied" is to drop it.
//
// This is an adapter defect, not an application-specific escape repair: the
// adapter simply has to honour the emulator's io.ReadWriter contract. Slice 0's
// corpus never wrote a query, which is why it was not found there.
type replyDrain struct {
	emu  *vt.Emulator
	stop atomic.Bool
	done chan struct{}
}

// drainBuffer is the reply read size. Replies are tens of bytes.
const drainBuffer = 4096

func newReplyDrain(emu *vt.Emulator) *replyDrain {
	d := &replyDrain{emu: emu, done: make(chan struct{})}
	go d.run()
	return d
}

// run is the only goroutine that ever calls Emulator.Read or Emulator.Close.
// Keeping both on one goroutine is what makes teardown race-free: the emulator
// tracks its closed state in an unsynchronised field, so a Close from the model
// actor would race an in-flight Read here.
func (d *replyDrain) run() {
	defer close(d.done)
	buf := make([]byte, drainBuffer)
	for {
		if _, err := d.emu.Read(buf); err != nil {
			return
		}
		if d.stop.Load() {
			_ = d.emu.Close()
			return
		}
	}
}

// shutdown releases the emulator. It wakes the blocked reader with one byte
// rather than closing the pipe from this goroutine, then waits for the drain to
// own the close.
func (d *replyDrain) shutdown() {
	if d == nil {
		return
	}
	d.stop.Store(true)
	// SendText writes straight to the reply pipe without consulting the
	// emulator's closed flag, so it is safe to call from here. If the drain has
	// already exited the pipe is closed and this returns immediately.
	d.emu.SendText("\x00")
	<-d.done
}
