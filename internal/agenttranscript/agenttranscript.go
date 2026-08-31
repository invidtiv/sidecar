// Package agenttranscript reads the conversation a managed shell is exactly
// bound to.
//
// It exists to keep two independent seams apart. The conversation-history
// adapters know how to read a provider's transcripts; `agentcontrol` knows how
// to address a managed agent. Joining them inside either one would make a
// terminal read depend on a history store, or a history store depend on tmux.
// This package is the join, and it is the only thing that has to know both.
//
// It never guesses. The binding comes from an official integration's report, so
// a shell with no reported session gets `transcript_unavailable` rather than the
// newest conversation that happens to share its working directory — which is a
// different conversation often enough to be dangerous, and indistinguishable
// from the right one when it is wrong.
package agenttranscript

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentsession"
)

// Binding is the exact conversation one managed shell is bound to.
type Binding struct {
	// Kind is the catalog family id running in the shell.
	Kind string
	// Ref is the reported session reference.
	Ref agentsession.Ref
}

// Lookup answers which conversation a target is bound to.
//
// It is injected because resolving a target to its project manifest is the
// CLI's job, and giving this package its own project resolver would be a second
// answer to a question the CLI already answers.
type Lookup func(agentcontrol.Target) (Binding, bool, error)

// Adapters resolves a conversation-history adapter by its adapter id.
//
// Injected so a test can supply a fake, and so the default can construct only
// the one adapter it needs rather than every registered adapter.
type Adapters func(adapterID string) (adapter.Adapter, bool)

// Reader is an agentcontrol.TranscriptReader over the adapter stack.
type Reader struct {
	Lookup   Lookup
	Adapters Adapters
	// Limit caps how many of the most recent messages are returned when the
	// caller asks for no specific number.
	Limit int
}

// DefaultLimit is the number of trailing messages returned when a caller does
// not choose. A transcript can be very long, and the recent end is the part a
// caller reaching for a transcript is almost always asking about.
const DefaultLimit = 50

var _ agentcontrol.TranscriptReader = Reader{}

// DefaultAdapters resolves a registered conversation adapter by id.
//
// It reads the same registry the Conversations plugin does, but it does not
// construct that plugin, and it does not call Detect: the binding already names
// the exact session, so whether the adapter would have discovered this project
// on its own is not a question worth asking. That is what lets a transcript read
// work with the Conversations plugin disabled.
func DefaultAdapters(adapterID string) (adapter.Adapter, bool) {
	store, ok := adapter.AllAdapters()[adapterID]
	return store, ok
}

// unavailable builds the refusal agentcontrol turns into transcript_unavailable.
//
// Every failure here is the same refusal with a different reason, deliberately:
// a caller that cannot read the exact transcript must fall back to a terminal
// source, and telling it "unavailable, because X" keeps that decision simple
// while still saying what went wrong.
func unavailable(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}

// SessionMessages returns the messages of the conversation bound to target.
func (r Reader) SessionMessages(ctx context.Context, target agentcontrol.Target, limit int) ([]agentcontrol.TranscriptMessage, error) {
	if r.Lookup == nil {
		return nil, unavailable("no session binding lookup is configured")
	}
	binding, ok, err := r.Lookup(target)
	if err != nil {
		return nil, unavailable("could not read this shell's session binding: %v", err)
	}
	if !ok || binding.Ref.Empty() {
		return nil, unavailable("this managed shell has no exact provider-reported session binding")
	}

	family, ok := agentcatalog.Lookup(binding.Kind)
	if !ok {
		return nil, unavailable("no provider named %q is known, so its transcripts cannot be read", binding.Kind)
	}
	adapterID := family.ConversationAdapterID()

	resolve := r.Adapters
	if resolve == nil {
		resolve = DefaultAdapters
	}
	store, ok := resolve(adapterID)
	if !ok {
		return nil, unavailable("no conversation adapter is available for %s", family.Name)
	}

	sessionID, err := sessionIDFor(store, binding.Ref)
	if err != nil {
		return nil, unavailable("%v", err)
	}

	messages, err := store.Messages(sessionID)
	if err != nil {
		return nil, unavailable("the bound %s conversation could not be read: %v", family.Name, err)
	}

	if limit <= 0 {
		limit = r.Limit
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}

	out := make([]agentcontrol.TranscriptMessage, 0, len(messages))
	for _, m := range messages {
		out = append(out, agentcontrol.TranscriptMessage{
			Role: m.Role,
			Text: m.Content,
			At:   m.Timestamp,
		})
	}
	return out, nil
}

// sessionIDFor turns a reference into the id the adapter indexes by.
//
// An id reference is already that. A path reference is converted through the
// adapter's own path resolver when it has one, because only the adapter knows
// whether its filenames are identities — Codex says explicitly that its rollout
// basenames are not, on current releases — and falls back to the basename rule
// that is correct for the adapters whose files are named for their session.
func sessionIDFor(store adapter.Adapter, ref agentsession.Ref) (string, error) {
	switch ref.Kind {
	case agentsession.RefID:
		return ref.Value, nil
	case agentsession.RefPath:
		id, err := adapter.ResolveSessionID(store, ref.Value)
		if err != nil {
			return "", fmt.Errorf("the bound transcript path could not be resolved to a session: %v", err)
		}
		if strings.TrimSpace(id) == "" {
			return "", fmt.Errorf("the bound transcript path %q named no session", filepath.Base(ref.Value))
		}
		return id, nil
	default:
		return "", fmt.Errorf("unknown session reference kind %q", ref.Kind)
	}
}
