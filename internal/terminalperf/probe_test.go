package terminalperf

import (
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestProbeRecordsOnlyFixedNumericCounters(t *testing.T) {
	counters := &Counters{}
	restore := Install(counters)
	t.Cleanup(restore)

	for event := ModelFrameBuilt; event < eventMax; event++ {
		Record(event)
	}
	snapshot := counters.Snapshot()
	v := reflect.ValueOf(snapshot)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() != reflect.Uint64 {
			t.Fatalf("diagnostic field %s has kind %s, want uint64", v.Type().Field(i).Name, v.Field(i).Kind())
		}
	}
	if snapshot.ModelFramesBuilt != 1 || snapshot.RowAnalyzerBypasses != 1 {
		t.Fatalf("fixed event counters not recorded: %+v", snapshot)
	}
	if snapshot.OutputToFrameSamples != 0 || snapshot.OutputToFrameP95US != 0 || snapshot.OutputToFrameMaxUS != 0 {
		t.Fatalf("latency changed without an observation: %+v", snapshot)
	}
}

func TestOutputToFrameP95UsesFixedNumericBuckets(t *testing.T) {
	counters := &Counters{}
	restore := Install(counters)
	t.Cleanup(restore)

	for sample := 1; sample <= 100; sample++ {
		RecordOutputToFrame(time.Duration(sample) * time.Millisecond)
	}
	snapshot := counters.Snapshot()
	if snapshot.OutputToFrameSamples != 100 {
		t.Fatalf("samples = %d, want 100", snapshot.OutputToFrameSamples)
	}
	if snapshot.OutputToFrameP95US != 95_000 {
		t.Fatalf("p95 = %dus, want 95000us", snapshot.OutputToFrameP95US)
	}
	if snapshot.OutputToFrameMaxUS != 100_000 {
		t.Fatalf("max = %dus, want 100000us", snapshot.OutputToFrameMaxUS)
	}
}

func TestOutputToFrameSnapshotIsCoherentDuringRecording(t *testing.T) {
	counters := &Counters{}
	restore := Install(counters)
	t.Cleanup(restore)

	const duration = 250 * time.Millisecond
	start := make(chan struct{})
	writersDone := make(chan struct{})
	violation := make(chan Snapshot, 1)
	var writers sync.WaitGroup
	for range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			for range 5_000 {
				RecordOutputToFrame(duration)
				runtime.Gosched()
			}
		}()
	}
	go func() {
		writers.Wait()
		close(writersDone)
	}()
	close(start)

	for {
		snapshot := counters.Snapshot()
		if snapshot.OutputToFrameSamples > 0 &&
			(snapshot.OutputToFrameP95US != 250_000 || snapshot.OutputToFrameMaxUS != 250_000) {
			select {
			case violation <- snapshot:
			default:
			}
		}
		select {
		case <-writersDone:
			final := counters.Snapshot()
			if final.OutputToFrameSamples != 40_000 || final.OutputToFrameP95US != 250_000 || final.OutputToFrameMaxUS != 250_000 {
				t.Fatalf("final latency snapshot = %+v", final)
			}
			select {
			case inconsistent := <-violation:
				t.Fatalf("concurrent latency snapshot mixed publication phases: %+v", inconsistent)
			default:
			}
			return
		default:
			runtime.Gosched()
		}
	}
}
