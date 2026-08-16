package configui

// ChildID identifies a focused route hosted by a page — a repair, an add form,
// an inline picker. Later phases add their own; the empty ChildID means "the
// page itself".
type ChildID string

const (
	// ChildNone is the page's own route.
	ChildNone ChildID = ""
)

// Route is one explicit Configuration state: a page, optionally with a focused
// child route open on top of it. Routes are a stack with parent-return, not
// stacked modals — a child knows the page it returns to, and Escape or the
// visible Back control returns there without applying anything.
type Route struct {
	Page  PageID
	Child ChildID
	// Title is what the child route calls itself. Empty for a page route.
	Title string
}

// IsChild reports that this route is a focused child of its page.
func (r Route) IsChild() bool { return r.Child != ChildNone }

// router is the navigation stack. The bottom entry is always a page route, so
// there is always somewhere to return to.
type router struct {
	stack []Route
}

func newRouter(page PageID) *router {
	return &router{stack: []Route{{Page: page}}}
}

// current returns the visible route.
func (r *router) current() Route {
	if len(r.stack) == 0 {
		return Route{Page: DefaultPage}
	}
	return r.stack[len(r.stack)-1]
}

// page returns the page that owns the visible route. A child route keeps its
// parent's sidebar destination highlighted.
func (r *router) page() PageID { return r.current().Page }

// navigate replaces the whole stack with a page route. Moving to another
// sidebar destination abandons any child route rather than nesting under it.
func (r *router) navigate(page PageID) {
	r.stack = []Route{{Page: page}}
}

// push opens a focused child route on the current page.
func (r *router) push(child ChildID, title string) {
	if child == ChildNone {
		return
	}
	r.stack = append(r.stack, Route{Page: r.page(), Child: child, Title: title})
}

// back returns to the parent route. It reports false when the visible route is
// already a page route, which is the caller's signal that Escape means
// something else (clear the search, or close Configuration).
func (r *router) back() bool {
	if len(r.stack) <= 1 {
		return false
	}
	r.stack = r.stack[:len(r.stack)-1]
	return true
}

// parentLabel names the route the Back control returns to.
func (r *router) parentLabel() string {
	if len(r.stack) < 2 {
		return ""
	}
	parent := r.stack[len(r.stack)-2]
	if parent.IsChild() {
		return parent.Title
	}
	return PageTitle(parent.Page)
}
