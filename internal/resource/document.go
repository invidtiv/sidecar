package resource

import "time"

// Tone is the coarse severity of a status pill. It is presentation-neutral on
// purpose: the host maps it to a palette, the provider does not choose colors.
type Tone string

// Stable v1 tones. Anything else coerces to ToneNeutral.
const (
	ToneNeutral Tone = "neutral"
	ToneInfo    Tone = "info"
	ToneSuccess Tone = "success"
	ToneWarning Tone = "warning"
	ToneDanger  Tone = "danger"
)

// CoerceTone maps an arbitrary provider string onto a known tone.
func CoerceTone(v string) Tone {
	switch Tone(v) {
	case ToneInfo:
		return ToneInfo
	case ToneSuccess:
		return ToneSuccess
	case ToneWarning:
		return ToneWarning
	case ToneDanger:
		return ToneDanger
	default:
		return ToneNeutral
	}
}

// Format is how a body's text should be interpreted.
type Format string

// Stable v1 formats. Anything else coerces to FormatText, which is the safer
// of the two: unknown markup is shown, never interpreted.
const (
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
)

// CoerceFormat maps an arbitrary provider string onto a known format.
func CoerceFormat(v string) Format {
	if Format(v) == FormatMarkdown {
		return FormatMarkdown
	}
	return FormatText
}

// FieldKind tells the host how to present a field value. It never changes
// validation: a timestamp that does not parse is still shown as the text the
// provider sent, because the alternative is silently hiding a value the user
// can see in the service itself.
type FieldKind string

// Stable v1 field kinds. Anything else coerces to FieldKindText.
const (
	FieldKindText      FieldKind = "text"
	FieldKindTimestamp FieldKind = "timestamp"
	FieldKindUser      FieldKind = "user"
)

// CoerceFieldKind maps an arbitrary provider string onto a known kind.
func CoerceFieldKind(v string) FieldKind {
	switch FieldKind(v) {
	case FieldKindTimestamp:
		return FieldKindTimestamp
	case FieldKindUser:
		return FieldKindUser
	default:
		return FieldKindText
	}
}

// Status is the optional single-line state of a resource.
type Status struct {
	Label string
	Tone  Tone
}

// Field is one ordered label/value pair in the bounded grid.
type Field struct {
	Label string
	Value string
	// Kind is a presentation hint. M1 owns what it does with it; M0's job is
	// only to carry and bound it, never to lose it.
	Kind FieldKind
}

// Body is the optional long-form text of a resource.
type Body struct {
	Format Format
	Text   string
	// Truncated reports that the provider's body exceeded MaxBodyBytes and was
	// cut at a rune boundary. The host shows the user what it kept and says so;
	// it does not refuse a document for being long.
	Truncated bool
}

// Document is the whole of what a provider may put on screen. Every string in
// it has already been through Sanitize*: valid UTF-8, bounded, control-free,
// and OSC-free. SourceURL, when non-empty, is a validated http/https URL.
type Document struct {
	// Identity is the provider-stable canonical ID. It may differ from the
	// locator that produced the lookup; the host re-keys the tab when it does.
	Identity string
	Title    string
	Subtitle string
	// Status is nil when the provider supplied none.
	Status *Status
	Fields []Field
	// Body is nil when the provider supplied none.
	Body      *Body
	SourceURL string
	// UpdatedAt is the zero time when absent or unparseable. An unparseable
	// timestamp is dropped, never an error.
	UpdatedAt time.Time
	// FreshFor is the clamped freshness hint the cache honors.
	FreshFor time.Duration
	// Sections are the titled blocks under the card. Empty for every document
	// a frozen-protocol provider returns.
	Sections []Section
}

// Shape names which of a Reference's alternatives is set.
type Shape int

const (
	// ShapeInvalid is a reference that is no shape, or more than one.
	ShapeInvalid Shape = iota
	// ShapeMatched is {instance, matcher, locator}: what a scanned span
	// produces and what `resolve` consumes. It is the frozen resource
	// protocol's only shape and is unchanged by the plugin protocol.
	ShapeMatched
	// ShapeCollection is {instance, collection} plus the user-owned view
	// position: what a plugin's collection tab points at. `list` consumes it.
	ShapeCollection
	// ShapeItem is {instance, collection, locator}: one row of a collection,
	// which `get` consumes. It is a distinct shape rather than a matched
	// document because a plugin row is addressed by its collection and ID, and
	// there is no matcher anywhere in that journey to invent.
	ShapeItem
)

// Reference is what a plugin-shaped tab points at, and the only plugin-shaped
// value that reaches persisted state. It carries no secret: a locator such as
// CASH-1245 and a user-typed query are the minimum needed to restore the pane
// the user had open.
type Reference struct {
	Instance string
	Matcher  string
	// Locator is the matched locator in ShapeMatched and the row ID in
	// ShapeItem. It is empty in ShapeCollection.
	Locator string

	// Collection is the plugin-declared collection ID. Non-empty is what makes
	// a reference one of the two plugin shapes.
	Collection string
	// Query, View, Sort and CursorID are a collection tab's view position,
	// restored verbatim so relaunch reopens the list the user was reading
	// rather than the collection's default page.
	Query    string
	View     string
	Sort     string
	CursorID string
}

// Shape reports which alternative this reference is, or ShapeInvalid when it is
// none or several. Deciding it in one place is what stops each caller from
// growing its own idea of what "a collection tab" means.
func (r Reference) Shape() Shape {
	switch {
	case r.Collection == "" && r.Matcher != "" && r.Locator != "":
		return ShapeMatched
	case r.Collection != "" && r.Matcher == "" && r.Locator == "":
		return ShapeCollection
	case r.Collection != "" && r.Matcher == "" && r.Locator != "":
		return ShapeItem
	default:
		return ShapeInvalid
	}
}

// IsCollection reports the collection-tab shape.
func (r Reference) IsCollection() bool { return r.Shape() == ShapeCollection }

// IsPlugin reports either of the two shapes that talk to a protocol plugin's
// list/get methods, which is what decides whether a tab renders as the shared
// browser or as the resource card.
func (r Reference) IsPlugin() bool {
	shape := r.Shape()
	return shape == ShapeCollection || shape == ShapeItem
}

// Valid reports whether a reference is well-formed enough to send to a plugin.
// It is a bounds check, not an existence check.
//
// Exactly one shape. A reference naming both a matcher and a collection, or
// neither, is refused rather than sent under a guess: which one the sender
// meant is not something the host can infer, and inferring it is how a restored
// tab silently becomes a different tab.
func (r Reference) Valid() bool {
	if r.Instance == "" || runeLen(r.Instance) > MaxInstanceIDChars {
		return false
	}
	switch r.Shape() {
	case ShapeMatched:
		return runeLen(r.Matcher) <= MaxMatcherIDChars &&
			runeLen(r.Locator) <= MaxLocatorChars
	case ShapeCollection:
		return r.viewPositionInBounds()
	case ShapeItem:
		return runeLen(r.Locator) <= MaxLocatorChars && r.viewPositionInBounds()
	default:
		return false
	}
}

func (r Reference) viewPositionInBounds() bool {
	return runeLen(r.Collection) <= MaxCollectionIDChars &&
		runeLen(r.Query) <= MaxQueryChars &&
		runeLen(r.View) <= MaxViewIDChars &&
		runeLen(r.Sort) <= MaxSortIDChars &&
		runeLen(r.CursorID) <= MaxIdentityChars
}
