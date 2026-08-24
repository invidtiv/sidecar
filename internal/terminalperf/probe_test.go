package terminalperf

import (
	"reflect"
	"testing"
)

func TestProbeRecordsOnlyFixedNumericCounters(t *testing.T) {
	counters := &Counters{}
	restore := Install(counters)
	t.Cleanup(restore)

	for event := ModelFrameBuilt; event <= GlobalWorkspacePreviewRendered; event++ {
		Record(event)
	}
	snapshot := counters.Snapshot()
	v := reflect.ValueOf(snapshot)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() != reflect.Uint64 {
			t.Fatalf("diagnostic field %s has kind %s, want uint64", v.Type().Field(i).Name, v.Field(i).Kind())
		}
		if v.Field(i).Uint() != 1 {
			t.Fatalf("diagnostic field %s = %d, want 1", v.Type().Field(i).Name, v.Field(i).Uint())
		}
	}
}
