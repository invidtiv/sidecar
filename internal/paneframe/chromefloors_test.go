package paneframe

import (
	"reflect"
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
)

// TestChromeFloorsGrowsEveryKind is a drift guard, not a behavior test.
//
// ChromeFloors rebuilds panelayout.Floors as a struct literal, so a kind added
// to Floors and forgotten here is silently dropped: the new leaf gets a zero
// minimum, and a tree claims to fit at sizes where that leaf has no room for
// its border. panelayout.Resource was added and dropped exactly this way.
//
// Reflection is what makes this catch the NEXT kind rather than only the last
// one. A literal list of fields would need the same edit the bug forgets.
func TestChromeFloorsGrowsEveryKind(t *testing.T) {
	var content panelayout.Floors
	v := reflect.ValueOf(&content).Elem()
	for i := 0; i < v.NumField(); i++ {
		// A distinct non-zero value per field, so a field copied from the
		// wrong source is caught as well as one omitted entirely.
		v.Field(i).Set(reflect.ValueOf(panelayout.Floor{Width: 10 + i, Height: 20 + i}))
	}

	got := reflect.ValueOf(ChromeFloors(content))
	typ := got.Type()
	for i := 0; i < got.NumField(); i++ {
		floor := got.Field(i).Interface().(panelayout.Floor)
		wantW := 10 + i + Overhead
		wantH := 20 + i + BorderWidth
		if floor.Width != wantW || floor.Height != wantH {
			t.Errorf("ChromeFloors dropped or mismatched %s: got %+v, want {Width:%d Height:%d}",
				typ.Field(i).Name, floor, wantW, wantH)
		}
	}
}
