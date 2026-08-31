package terminalperf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestSnapshotHandlerReturnsOnlyFixedNumericCounters(t *testing.T) {
	counters := &Counters{}
	restore := Install(counters)
	for event := ModelFrameBuilt; event < eventMax; event++ {
		Record(event)
	}
	RecordOutputToFrame(1500 * time.Microsecond)
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
	wantKeys := []string{
		"model_frames_built", "model_frames_published", "terminal_views_rendered",
		"row_cache_hits", "row_cache_misses", "content_link_resolution_requests",
		"content_link_resolution_cache_hits", "synchronous_resolver_calls",
		"global_workspace_list_rendered", "global_workspace_preview_rendered",
		"application_views_rendered", "project_workspace_views_rendered",
		"project_sidebar_rendered", "project_preview_composes", "project_preview_cache_hits",
		"document_frames_built", "document_frame_cache_hits", "document_link_scans",
		"document_resolution_requests", "document_resolution_cache_hits", "row_analyzer_bypasses",
		"output_to_frame_samples", "output_to_frame_p95_us", "output_to_frame_max_us",
	}
	for _, key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Fatalf("JSON lacks fixed counter %q: %s", key, recorder.Body.String())
		}
	}
	wantLatency := map[string]float64{
		"output_to_frame_samples": 1,
		"output_to_frame_p95_us":  1500,
		"output_to_frame_max_us":  1500,
	}
	for name, value := range got {
		want := float64(1)
		if latency, ok := wantLatency[name]; ok {
			want = latency
		}
		if value != want {
			t.Fatalf("counter %s = %#v, want numeric %v", name, value, want)
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
