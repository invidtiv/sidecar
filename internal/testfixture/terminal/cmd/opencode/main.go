// Command opencode emits a deterministic OpenCode-shaped terminal workload for
// isolated Sidecar performance proofs. It contains no captured user content.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	terminalfixture "github.com/marcus/sidecar/internal/testfixture/terminal"
	"golang.org/x/term"
)

const (
	defaultWidth    = 160
	defaultHeight   = 44
	defaultInterval = 8 * time.Millisecond
)

type streamer struct {
	out     io.Writer
	fixture terminalfixture.OpenCode
	step    int
}

func newStreamer(out io.Writer, width, height int) *streamer {
	return &streamer{out: out, fixture: terminalfixture.NewOpenCode(width, height)}
}

func (s *streamer) redraw(width, height int) error {
	s.fixture = terminalfixture.NewOpenCode(width, height)
	if _, err := io.WriteString(s.out, "\x1b[?1049h\x1b[2J"); err != nil {
		return err
	}
	rows := strings.Split(s.fixture.Frame(s.step), "\n")
	// Frame may prepend bounded synthetic history for capture-shaped benchmark
	// inputs. A live redraw writes only the authored grid at explicit cursor
	// positions so resize cannot turn those history rows into visible content.
	rows = rows[len(rows)-s.fixture.Height:]
	for row, content := range rows {
		if _, err := fmt.Fprintf(s.out, "\x1b[%d;1H%s", row+1, content); err != nil {
			return err
		}
	}
	return nil
}

func (s *streamer) advance() error {
	s.step++
	_, err := s.out.Write(s.fixture.Burst(s.step))
	return err
}

func terminalSize() (int, int) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		return defaultWidth, defaultHeight
	}
	return width, height
}

func main() {
	interval := flag.Duration("interval", defaultInterval, "delay between deterministic ANSI updates")
	flag.Parse()
	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "interval must be positive")
		os.Exit(2)
	}

	width, height := terminalSize()
	stream := newStreamer(os.Stdout, width, height)
	if err := stream.redraw(width, height); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := stream.advance(); err != nil {
				return
			}
		case <-resize:
			width, height = terminalSize()
			if err := stream.redraw(width, height); err != nil {
				return
			}
		}
	}
}
