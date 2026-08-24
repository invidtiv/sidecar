package terminalperf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSnapshotHandlerReturnsOnlyFixedNumericCounters(t *testing.T) {
	counters := &Counters{}
	restore := Install(counters)
	for event := ModelFrameBuilt; event <= GlobalWorkspacePreviewRendered; event++ {
		Record(event)
	}
	restore()

	recorder := httptest.NewRecorder()
	SnapshotHandler(counters).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, SnapshotPath, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	wantFields := reflect.TypeOf(Snapshot{}).NumField()
	if len(got) != wantFields {
		t.Fatalf("JSON fields = %d, want %d: %s", len(got), wantFields, recorder.Body.String())
	}
	for name, value := range got {
		if value != float64(1) {
			t.Fatalf("counter %s = %#v, want numeric 1", name, value)
		}
	}
}

func TestSnapshotHandlerRejectsWrites(t *testing.T) {
	recorder := httptest.NewRecorder()
	SnapshotHandler(&Counters{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, SnapshotPath, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}
