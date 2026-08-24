package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

func TestStreamerRedrawAdvanceAndResize(t *testing.T) {
	var out bytes.Buffer
	stream := newStreamer(&out, 100, 20)
	if err := stream.redraw(100, 20); err != nil {
		t.Fatal(err)
	}
	if err := stream.advance(); err != nil {
		t.Fatal(err)
	}
	if err := stream.redraw(120, 24); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"\x1b[?1049h\x1b[2J",
		"\x1b[1;1H",
		"OpenCode synthetic workspace",
		"internal/runtime/frame.go:42",
		"https://docs.example.test/terminal/performance",
		"working  frame 001",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream output lacks %q", want)
		}
	}
	if stream.fixture.Width != 120 || stream.fixture.Height != 24 {
		t.Fatalf("resized fixture = %dx%d, want 120x24", stream.fixture.Width, stream.fixture.Height)
	}
}

type screenWriter struct {
	model *screenmodel.Model
}

func (w screenWriter) Write(p []byte) (int, error) {
	if err := w.model.Write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func TestStreamerKeepsRepresentativeGridAfterSustainedOutputAndResize(t *testing.T) {
	model := screenmodel.New(100, 20)
	defer model.Close()
	stream := newStreamer(screenWriter{model: model}, 100, 20)
	if err := stream.redraw(100, 20); err != nil {
		t.Fatal(err)
	}
	// At the default 8 ms interval, 700 updates represent 5.6 seconds.
	for range 700 {
		if err := stream.advance(); err != nil {
			t.Fatal(err)
		}
	}
	assertRepresentativeFrame(t, model, 100, 20)

	if err := model.Resize(120, 24); err != nil {
		t.Fatal(err)
	}
	if err := stream.redraw(120, 24); err != nil {
		t.Fatal(err)
	}
	for range 80 {
		if err := stream.advance(); err != nil {
			t.Fatal(err)
		}
	}
	assertRepresentativeFrame(t, model, 120, 24)
}

func assertRepresentativeFrame(t *testing.T, model *screenmodel.Model, width, height int) {
	t.Helper()
	frame, err := model.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width != width || frame.Height != height {
		t.Fatalf("frame size = %dx%d, want %dx%d", frame.Width, frame.Height, width, height)
	}
	for _, want := range []string{
		"OpenCode synthetic workspace",
		"context",
		"internal/runtime/frame.go:42",
		"https://docs.example.test/terminal/performance",
		"■■⬝⬝ synthetic progress",
	} {
		if !strings.Contains(frame.Output, want) {
			t.Fatalf("steady-state frame lacks %q:\n%s", want, frame.Output)
		}
	}
}
