package screenmodel

import (
	"testing"

	"github.com/marcus/sidecar/internal/terminalperf"
	terminalfixture "github.com/marcus/sidecar/internal/testfixture/terminal"
)

var benchmarkFrame Frame

func seededOpenCodeModel(b testing.TB) (*Model, terminalfixture.OpenCode) {
	b.Helper()
	fixture := terminalfixture.NewOpenCode(160, 44)
	model := New(fixture.Width, fixture.Height)
	if err := model.Seed(Seed{
		Output: fixture.Frame(0), Width: fixture.Width, Height: fixture.Height,
		CursorVisible: true, HistorySize: 3, HistoryLimit: 600,
	}); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(model.Close)
	return model, fixture
}

func TestOpenCodeFixtureBuildsRepeatedFramesWithoutTmux(t *testing.T) {
	model, fixture := seededOpenCodeModel(t)
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)

	var previous string
	for step := range 8 {
		if err := model.Write(fixture.Burst(step)); err != nil {
			t.Fatal(err)
		}
		frame, err := model.Frame()
		if err != nil {
			t.Fatal(err)
		}
		if step > 0 && frame.Output == previous {
			t.Fatalf("fixture update %d produced no presentation change", step)
		}
		previous = frame.Output
	}
	if got := counters.Snapshot().ModelFramesBuilt; got != 8 {
		t.Fatalf("model frames built = %d, want 8", got)
	}
}

func BenchmarkFrameOpenCodeFixture(b *testing.B) {
	model, _ := seededOpenCodeModel(b)
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	b.Cleanup(restore)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		frame, err := model.Frame()
		if err != nil {
			b.Fatal(err)
		}
		benchmarkFrame = frame
	}
	b.StopTimer()
	snapshot := counters.Snapshot()
	b.ReportMetric(float64(snapshot.ModelFramesBuilt)/float64(b.N), "frames_built/op")
}
