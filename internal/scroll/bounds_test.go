package scroll

import "testing"

func TestBoundsMovementAndBoundaries(t *testing.T) {
	tests := []struct {
		name                  string
		bounds                Bounds
		delta, wantPosition   int
		wantChanged, wantEdge bool
	}{
		{name: "moves up", bounds: Bounds{Position: 5, Maximum: 10}, delta: -3, wantPosition: 2, wantChanged: true},
		{name: "moves down", bounds: Bounds{Position: 5, Maximum: 10}, delta: 3, wantPosition: 8, wantChanged: true},
		{name: "clamps top", bounds: Bounds{Position: 1, Maximum: 10}, delta: -3, wantPosition: 0, wantChanged: true},
		{name: "drops at top", bounds: Bounds{Position: 0, Maximum: 10}, delta: -3, wantPosition: 0, wantEdge: true},
		{name: "drops at bottom", bounds: Bounds{Position: 10, Maximum: 10}, delta: 3, wantPosition: 10, wantEdge: true},
		{name: "empty viewport is bounded", bounds: Bounds{}, delta: 3, wantPosition: 0, wantEdge: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.bounds.AtBoundary(tt.delta); got != tt.wantEdge {
				t.Fatalf("AtBoundary(%d) = %v, want %v", tt.delta, got, tt.wantEdge)
			}
			position, changed := tt.bounds.Move(tt.delta)
			if position != tt.wantPosition || changed != tt.wantChanged {
				t.Fatalf("Move(%d) = (%d, %v), want (%d, %v)", tt.delta, position, changed, tt.wantPosition, tt.wantChanged)
			}
		})
	}
}
