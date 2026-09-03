package resource

import "time"

// Sections are the one shape the plugin protocol adds to a resource document.
// They live here rather than in internal/pluginhost because they are part of
// the document, and a document has exactly one sanitizer: a second one would be
// a second set of bounds for the same card.
//
// A document with no sections renders exactly as a resource v1 card does today,
// which is how a frozen-protocol provider keeps working unchanged.

// Section limits, from the plugin protocol's Limits table.
const (
	// MaxSections bounds how many sections one document may carry.
	MaxSections = 8
	// MaxTimelineItems bounds one timeline section.
	MaxTimelineItems = 200
	// MaxSectionTitleChars bounds a section heading.
	MaxSectionTitleChars = 64
	// MaxTimelineTitleChars and MaxTimelineTextChars bound one timeline entry.
	MaxTimelineTitleChars = 120
	MaxTimelineTextChars  = 512
)

// Section is one titled block under a document. It is exactly one of a body, a
// field grid, or a timeline — never two — because the host draws each with a
// different renderer and a section that claimed to be both would have to pick
// one anyway. Sanitization picks in the declared order rather than refusing, so
// a plugin that sends two still shows the user something.
type Section struct {
	Title string
	// Body is nil unless this is a body section.
	Body *Body
	// Fields is empty unless this is a field-grid section.
	Fields []Field
	// Items is empty unless this is a timeline section.
	Items []TimelineItem
}

// TimelineItem is one entry in a timeline section. When is rendered relatively
// and is the zero time when absent or unparseable.
type TimelineItem struct {
	When  time.Time
	Title string
	Text  string
}

// WireSection is the `sections[]` element of a get or resolve response.
type WireSection struct {
	Title  string             `json:"title,omitempty"`
	Body   *WireBody          `json:"body,omitempty"`
	Fields []WireField        `json:"fields,omitempty"`
	Items  []WireTimelineItem `json:"items,omitempty"`
}

// WireTimelineItem is `{when, title, text}`.
type WireTimelineItem struct {
	When  string `json:"when,omitempty"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
}

// SanitizeSections enforces every section bound and returns blocks safe to
// render. Like the rest of Sanitize* it truncates rather than refusing: a
// section past the count limit is dropped, an over-long heading is cut, and a
// section carrying nothing at all disappears instead of drawing an empty rule.
func SanitizeSections(wire []WireSection) []Section {
	if len(wire) == 0 {
		return nil
	}
	out := make([]Section, 0, min(len(wire), MaxSections))
	for _, w := range wire {
		if len(out) == MaxSections {
			break
		}
		section := Section{Title: SanitizeLine(w.Title, MaxSectionTitleChars)}
		switch {
		case w.Body != nil:
			text, truncated := SanitizeBodyText(w.Body.Text, MaxBodyBytes)
			if text != "" {
				section.Body = &Body{Format: CoerceFormat(w.Body.Format), Text: text, Truncated: truncated}
			}
		case len(w.Fields) > 0:
			section.Fields = sanitizeFields(w.Fields)
		case len(w.Items) > 0:
			section.Items = sanitizeTimeline(w.Items)
		}
		if section.Body == nil && len(section.Fields) == 0 && len(section.Items) == 0 {
			continue
		}
		out = append(out, section)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sanitizeTimeline(wire []WireTimelineItem) []TimelineItem {
	out := make([]TimelineItem, 0, min(len(wire), MaxTimelineItems))
	for _, w := range wire {
		if len(out) == MaxTimelineItems {
			break
		}
		item := TimelineItem{
			When:  parseTimestamp(w.When),
			Title: SanitizeLine(w.Title, MaxTimelineTitleChars),
			Text:  SanitizeLine(w.Text, MaxTimelineTextChars),
		}
		if item.Title == "" && item.Text == "" && item.When.IsZero() {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
