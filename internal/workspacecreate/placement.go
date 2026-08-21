package workspacecreate

// Placement is where a created pane lands. It is the modal's face of the same
// vocabulary `sidecar open --split` uses, so the CLI and the modal share one
// placement model rather than two that agree by hand.
type Placement int

const (
	PlacementAuto Placement = iota
	PlacementRight
	PlacementBelow
)

// Placement button IDs. Each is a create action: clicking one creates
// immediately with that placement, with no second confirmation.
const (
	ActionPlaceAuto  = "create-place-auto"
	ActionPlaceRight = "create-place-right"
	ActionPlaceBelow = "create-place-below"
)

type placementRow struct {
	Placement Placement
	Label     string
	Action    string
	// Split is the `--split` value this placement maps onto, for
	// panelayout.ApplyAxisOverride.
	Split string
}

// placementCatalog is the segmented row, in order. Auto is the default and the
// one Enter uses.
var placementCatalog = []placementRow{
	{Placement: PlacementAuto, Label: "Auto", Action: ActionPlaceAuto, Split: "auto"},
	{Placement: PlacementRight, Label: "Right", Action: ActionPlaceRight, Split: "right"},
	{Placement: PlacementBelow, Label: "Below", Action: ActionPlaceBelow, Split: "below"},
}

// PlacementFromAction maps a placement button's action to its placement.
func PlacementFromAction(action string) (Placement, bool) {
	for _, row := range placementCatalog {
		if row.Action == action {
			return row.Placement, true
		}
	}
	return PlacementAuto, false
}

// PlacementSplit is the `--split` value for a placement.
func PlacementSplit(placement Placement) string {
	for _, row := range placementCatalog {
		if row.Placement == placement {
			return row.Split
		}
	}
	return "auto"
}

// IsPlacementAction reports that an action came from the placement row, which
// means "create now with this placement".
func IsPlacementAction(action string) bool {
	_, ok := PlacementFromAction(action)
	return ok
}
