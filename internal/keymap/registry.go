package keymap

import (
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

const sequenceTimeout = 500 * time.Millisecond

// Command represents a registered command handler.
type Command struct {
	ID      string
	Name    string
	Handler func() tea.Cmd
	Context string
}

// Binding maps a key or key sequence to a command.
type Binding struct {
	Key     string // e.g., "tab", "ctrl+s", "g g"
	Command string // Command ID
	Context string // "global", plugin ID, etc.
}

// Registry manages key bindings and command dispatch.
type Registry struct {
	commands      map[string]Command   // ID -> Command
	bindings      map[string][]Binding // context -> bindings
	userOverrides map[string]string    // key -> command ID
	pendingKey    string
	pendingTime   time.Time
	mu            sync.RWMutex
}

// NewRegistry creates a new keymap registry.
func NewRegistry() *Registry {
	return &Registry{
		commands:      make(map[string]Command),
		bindings:      make(map[string][]Binding),
		userOverrides: make(map[string]string),
	}
}

// RegisterCommand adds a command to the registry.
func (r *Registry) RegisterCommand(cmd Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[cmd.ID] = cmd
}

// RegisterBinding adds a key binding. Registering the same binding again is a
// no-op.
//
// Idempotence matters because the registry lives for the process lifetime while
// plugins are re-initialized on every project switch: a plugin that publishes
// its table on adoption would otherwise grow the context's slice without bound.
// Nothing downstream wants duplicates either — the palette already has to
// de-duplicate, and lookup takes the first match.
func (r *Registry) RegisterBinding(b Binding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.bindings[b.Context] {
		if existing == b {
			return
		}
	}
	r.bindings[b.Context] = append(r.bindings[b.Context], b)
}

// RegisterPluginBinding satisfies plugin.BindingRegistrar interface.
// It converts plugin.Binding to keymap.Binding and registers it.
func (r *Registry) RegisterPluginBinding(key, command, context string) {
	r.RegisterBinding(Binding{Key: key, Command: command, Context: context})
}

// SetUserOverride sets a user-configured key override.
func (r *Registry) SetUserOverride(key, commandID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.userOverrides[key] = commandID
}

// UserOverride returns the command a user override binds to this key, if any.
//
// It exists so a caller that runs *before* Handle in the key ladder can still
// honour the user's mapping. Handle consults the same overrides first
// (findCommand step 1); this exposes only that step.
//
// The bool reports that an override exists and resolved to a runnable command.
// An override naming an unknown command is not a claim on the key: the caller
// should carry on rather than swallow the keystroke.
func (r *Registry) UserOverride(key tea.KeyMsg) (tea.Cmd, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cmdID, ok := r.userOverrides[keyToString(key)]
	if !ok {
		return nil, false
	}
	cmd, ok := r.commands[cmdID]
	if !ok || cmd.Handler == nil {
		return nil, false
	}
	return cmd.Handler(), true
}

// Handle dispatches a key event to the appropriate command handler.
// Returns nil if no matching binding is found.
func (r *Registry) Handle(key tea.KeyMsg, activeContext string) tea.Cmd {
	r.mu.Lock()
	defer r.mu.Unlock()

	keyStr := keyToString(key)

	// Check for pending key sequence
	if r.pendingKey != "" {
		if time.Since(r.pendingTime) < sequenceTimeout {
			seq := r.pendingKey + " " + keyStr
			r.pendingKey = ""
			if cmd := r.findCommand(seq, activeContext); cmd != nil {
				return cmd
			}
			// Sequence didn't match, try just the new key
		} else {
			r.pendingKey = ""
		}
	}

	// Check if this key starts a sequence
	if r.isSequenceStart(keyStr, activeContext) {
		r.pendingKey = keyStr
		r.pendingTime = time.Now()
		return nil
	}

	return r.findCommand(keyStr, activeContext)
}

// findCommand looks up a command for the given key in order of precedence.
func (r *Registry) findCommand(key, activeContext string) tea.Cmd {
	// 1. Check user overrides first
	if cmdID, ok := r.userOverrides[key]; ok {
		if cmd, ok := r.commands[cmdID]; ok && cmd.Handler != nil {
			return cmd.Handler()
		}
	}

	// 2. Check active context bindings
	if activeContext != "" && activeContext != "global" {
		if cmd, found := r.findInContext(key, activeContext); found {
			return cmd
		}
	}

	// 3. Fall back to global bindings
	cmd, _ := r.findInContext(key, "global")
	return cmd
}

// findInContext finds a command for a key in a specific context.
// Returns the command result and whether a binding was found.
func (r *Registry) findInContext(key, context string) (tea.Cmd, bool) {
	for _, b := range r.bindings[context] {
		if b.Key == key {
			if cmd, ok := r.commands[b.Command]; ok && cmd.Handler != nil {
				return cmd.Handler(), true
			}
		}
	}
	return nil, false
}

// isSequenceStart checks if this key could start a multi-key sequence.
func (r *Registry) isSequenceStart(key, activeContext string) bool {
	prefix := key + " "

	// Check all contexts that could be active
	contexts := []string{"global"}
	if activeContext != "" && activeContext != "global" {
		contexts = append(contexts, activeContext)
	}

	for _, ctx := range contexts {
		for _, b := range r.bindings[ctx] {
			if strings.HasPrefix(b.Key, prefix) {
				return true
			}
		}
	}

	// Also check user overrides
	for k := range r.userOverrides {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}

	return false
}

// ResetPending clears any pending key sequence.
func (r *Registry) ResetPending() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingKey = ""
}

// GetCommand retrieves a command by ID.
// Returns the command and true if found, or zero value and false otherwise.
func (r *Registry) GetCommand(id string) (Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cmd, ok := r.commands[id]
	return cmd, ok
}

// BindingsForContext returns all bindings for a given context.
func (r *Registry) BindingsForContext(context string) []Binding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.bindings[context]
}

// CommandForContextKey returns the command a key is bound to in exactly this
// context. It does not fall back to the global context and does not consult
// user overrides: callers use it to ask "did this context opt in to this key?",
// which a global binding or an override would answer for a different question.
func (r *Registry) CommandForContextKey(context, key string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, b := range r.bindings[context] {
		if b.Key == key {
			return b.Command, true
		}
	}
	return "", false
}

// AllContexts returns all contexts that have bindings.
func (r *Registry) AllContexts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	contexts := make([]string, 0, len(r.bindings))
	for ctx := range r.bindings {
		contexts = append(contexts, ctx)
	}
	return contexts
}

// HasPending returns true if there's a pending key sequence.
func (r *Registry) HasPending() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pendingKey != "" && time.Since(r.pendingTime) < sequenceTimeout
}

// keyToString converts a tea.KeyMsg to a string representation.
//
// In bubbletea v2, KeyMsg.String() already produces the canonical names this
// registry matches against ("ctrl+c", "tab", "enter", "esc", "space",
// "shift+tab", "pgup", printable runes, etc.), so we delegate to it directly.
func keyToString(key tea.KeyMsg) string {
	return key.String()
}
