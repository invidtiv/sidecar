package modal

// Variant represents the visual style of the modal.
type Variant int

const (
	VariantDefault Variant = iota // Primary border color
	VariantDanger                 // Red border, danger button styles
	VariantWarning                // Yellow/amber border
	VariantInfo                   // Blue border
)

// Option is a functional option for configuring a Modal.
type Option func(*Modal)

// WithWidth sets the modal width.
func WithWidth(w int) Option {
	return func(m *Modal) {
		m.width = w
	}
}

// WithVariant sets the modal visual variant.
func WithVariant(v Variant) Option {
	return func(m *Modal) {
		m.variant = v
	}
}

// WithHints enables the keyboard hint line at the bottom.
func WithHints(show bool) Option {
	return func(m *Modal) {
		m.showHints = show
	}
}

// WithPrimaryAction sets the action ID returned when input submits implicitly.
func WithPrimaryAction(actionID string) Option {
	return func(m *Modal) {
		m.primaryAction = actionID
	}
}

// WithCloseOnBackdropClick controls whether clicking the backdrop dismisses the modal.
// Defaults to true.
func WithCloseOnBackdropClick(close bool) Option {
	return func(m *Modal) {
		m.closeOnBackdrop = close
	}
}

// WithCustomFooter sets a fixed footer line rendered outside the scroll viewport.
func WithCustomFooter(footer string) Option {
	return func(m *Modal) {
		m.customFooter = footer
	}
}

// WithMargin sets how much of the surface the modal leaves clear around itself.
// The default keeps the box off the edge of a screen; a modal that is meant to
// own its surface — a pane too small to show anything useful behind the modal —
// passes 0, 0 and is then rendered at exactly the surface's size.
func WithMargin(x, y int) Option {
	return func(m *Modal) {
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		m.marginX, m.marginY = x, y
	}
}

// PreferredListRows is the list length a content-sized modal aims for on a
// surface this tall: enough to be worth opening, never so much that a picker
// with three hits reserves most of a large pane. It deliberately does not
// depend on how many rows there are to show — a box that grows as results land
// breathes under the user's hands.
func PreferredListRows(surfaceHeight int) int {
	rows := surfaceHeight / 3
	if rows < MinListRows {
		rows = MinListRows
	}
	if rows > MaxListRows {
		rows = MaxListRows
	}
	return rows
}

// Default modal dimensions
const (
	DefaultWidth  = 50
	MinModalWidth = 30
	MaxModalWidth = 120
	ModalPadding  = 6 // border(2) + horizontal padding(4)

	// DefaultMarginX/Y are the cells left clear around the modal box.
	DefaultMarginX = 2
	DefaultMarginY = 1

	// MinListRows/MaxListRows bound PreferredListRows.
	MinListRows = 8
	MaxListRows = 14

	// ChromeWidth/ChromeHeight are what the box itself costs: border plus
	// padding. A caller budgeting its own content needs the same numbers.
	ChromeWidth  = 6
	ChromeHeight = 4
)
