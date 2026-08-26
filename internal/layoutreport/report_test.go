package layoutreport

import (
	"encoding/json"
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
)

func TestBuild_JSONShape(t *testing.T) {
	root := &panelayout.Node{ID: 1, Kind: panelayout.Primary}
	out := Build(Source{
		Surface: "shell:test",
		Root:    "/tmp/proj",
		Tree:    root,
		Floors:  panelayout.Floors{Primary: panelayout.Floor{Width: 10, Height: 3}, Doc: panelayout.Floor{Width: 8, Height: 3}},
		Layout:  &state.PaneLayoutJSON{Kind: "terminal"},
	})
	var report map[string]json.RawMessage
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "surface", "root", "grid", "caps", "floors"} {
		if _, ok := report[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
	var caps map[string]int
	if err := json.Unmarshal(report["caps"], &caps); err != nil {
		t.Fatal(err)
	}
	if caps["maxColumns"] != panelayout.MaxGridColumns || caps["liveLeaves"] != panelayout.LiveLeafCap {
		t.Fatalf("caps = %v", caps)
	}
	var floors map[string]map[string]int
	if err := json.Unmarshal(report["floors"], &floors); err != nil {
		t.Fatal(err)
	}
	if floors[panelayout.KindNamePrimary]["width"] != 10 || floors[panelayout.KindNameFile]["height"] != 3 {
		t.Fatalf("floors = %v", floors)
	}
}
