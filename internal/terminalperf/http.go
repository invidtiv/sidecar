package terminalperf

import (
	"encoding/json"
	"net/http"
)

// SnapshotPath is the localhost-only diagnostic route served alongside pprof
// when SIDECAR_TERMINAL_PERF is enabled by the Sidecar process.
const SnapshotPath = "/debug/terminalperf"

// SnapshotHandler exposes only the fixed numeric counter vocabulary. It is a
// read-only development diagnostic, not a product API.
func SnapshotHandler(counters *Counters) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(counters.Snapshot())
	})
}
