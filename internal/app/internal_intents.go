package app

import (
	"fmt"
	"net/url"
	"regexp"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
)

// Intent is a handler-owned, validated Sidecar navigation intent.
type Intent any

// IntentAppContext is the bounded application state an intent may observe.
// Handlers return messages rather than reaching into plugin models.
type IntentAppContext struct {
	ProjectRoot string
}

// IntentHandler owns one static sidecar:// namespace.
type IntentHandler interface {
	Namespace() string
	Parse(id string, query url.Values) (Intent, error)
	Activate(IntentAppContext, Intent) tea.Cmd
}

type intentRegistry struct {
	handlers map[string]IntentHandler
}

func newIntentRegistry(handlers ...IntentHandler) (*intentRegistry, error) {
	r := &intentRegistry{handlers: make(map[string]IntentHandler, len(handlers))}
	for _, handler := range handlers {
		if handler == nil {
			return nil, fmt.Errorf("nil intent handler")
		}
		namespace := handler.Namespace()
		probe := "sidecar://" + namespace + "/probe"
		if _, err := contentlink.ParseInternalURI(probe); err != nil {
			return nil, fmt.Errorf("invalid intent namespace %q", namespace)
		}
		if _, exists := r.handlers[namespace]; exists {
			return nil, fmt.Errorf("duplicate intent namespace %q", namespace)
		}
		r.handlers[namespace] = handler
	}
	return r, nil
}

func mustIntentRegistry(handlers ...IntentHandler) *intentRegistry {
	r, err := newIntentRegistry(handlers...)
	if err != nil {
		panic(err)
	}
	return r
}

func (r *intentRegistry) namespaces() map[string]contentlink.URIOptions {
	if r == nil {
		return nil
	}
	out := make(map[string]contentlink.URIOptions, len(r.handlers))
	for namespace, handler := range r.handlers {
		h := handler
		out[namespace] = contentlink.URIOptions{ValidateID: func(id string) bool {
			_, err := h.Parse(id, nil)
			return err == nil
		}}
	}
	return out
}

func (r *intentRegistry) activate(ctx IntentAppContext, ref contentlink.Ref) (tea.Cmd, error) {
	if r == nil || ref.Kind != contentlink.KindInternal {
		return nil, fmt.Errorf("not an internal intent")
	}
	handler := r.handlers[ref.Namespace]
	if handler == nil {
		return nil, fmt.Errorf("unknown internal namespace %q", ref.Namespace)
	}
	intent, err := handler.Parse(ref.Value, nil)
	if err != nil {
		return nil, err
	}
	return handler.Activate(ctx, intent), nil
}

type noteIntent struct{ ID string }
type noteIntentHandler struct{}

var noteIDPattern = regexp.MustCompile(`^nt-[a-z0-9]{1,64}$`)

func (noteIntentHandler) Namespace() string { return "note" }
func (noteIntentHandler) Parse(id string, query url.Values) (Intent, error) {
	if len(query) != 0 || !noteIDPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid note identity")
	}
	return noteIntent{ID: id}, nil
}
func (noteIntentHandler) Activate(ctx IntentAppContext, parsed Intent) tea.Cmd {
	intent, ok := parsed.(noteIntent)
	if !ok || intent.ID == "" || ctx.ProjectRoot == "" {
		return nil
	}
	return func() tea.Msg {
		return NavigateToNoteMsg{ID: intent.ID, ProjectRoot: ctx.ProjectRoot}
	}
}

var (
	sidecarIntents          = mustIntentRegistry(noteIntentHandler{})
	sidecarIntentNamespaces = sidecarIntents.namespaces()
)
