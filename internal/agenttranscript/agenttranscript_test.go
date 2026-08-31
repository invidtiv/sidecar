package agenttranscript

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentsession"
)

// fakeStore is a conversation adapter holding several sessions that all share
// one working directory — the situation the exact-binding rule exists for.
type fakeStore struct {
	id       string
	sessions map[string][]adapter.Message
	// order is the discovery order, newest last, so a test can name the
	// session a "newest in this directory" heuristic would have picked.
	order []string
	// asked records every session id Messages was called with.
	asked []string
	// pathIDs, when set, makes the store a SessionPathResolver.
	pathIDs map[string]string
}

func (f *fakeStore) ID() string   { return f.id }
func (f *fakeStore) Name() string { return f.id }
func (f *fakeStore) Icon() string { return "" }

func (f *fakeStore) Detect(string) (bool, error) { return true, nil }
func (f *fakeStore) Capabilities() adapter.CapabilitySet {
	return adapter.CapabilitySet{adapter.CapMessages: true}
}

func (f *fakeStore) Sessions(string) ([]adapter.Session, error) {
	out := make([]adapter.Session, 0, len(f.order))
	for _, id := range f.order {
		out = append(out, adapter.Session{ID: id, AdapterID: f.id})
	}
	return out, nil
}

func (f *fakeStore) Messages(sessionID string) ([]adapter.Message, error) {
	f.asked = append(f.asked, sessionID)
	msgs, ok := f.sessions[sessionID]
	if !ok {
		return nil, errors.New("session " + sessionID + " not found")
	}
	return msgs, nil
}

func (f *fakeStore) Usage(string) (*adapter.UsageStats, error) { return nil, nil }
func (f *fakeStore) Watch(string) (<-chan adapter.Event, io.Closer, error) {
	return nil, nil, errors.New("not supported")
}

// pathStore adds SessionPathResolver, the way the Codex adapter does, because
// its filenames are explicitly not session identities.
type pathStore struct{ *fakeStore }

func (p pathStore) SessionIDFromPath(path string) (string, error) {
	id, ok := p.pathIDs[path]
	if !ok {
		return "", errors.New("unknown transcript path")
	}
	return id, nil
}

func message(role, text string) adapter.Message {
	return adapter.Message{Role: role, Content: text, Timestamp: time.Unix(1700000000, 0).UTC()}
}

// twoSessionsInOneDirectory is the fixture the exit gate names: two
// conversations of the same provider, in the same working directory, where the
// bound one is deliberately NOT the newest.
func twoSessionsInOneDirectory() *fakeStore {
	return &fakeStore{
		id: "codex",
		sessions: map[string][]adapter.Message{
			"sess-bound": {
				message("user", "the bound conversation"),
				message("assistant", "answer from the bound conversation"),
			},
			"sess-newer": {
				message("user", "a different conversation in the same directory"),
				message("assistant", "answer from the newer conversation"),
			},
		},
		order: []string{"sess-bound", "sess-newer"},
	}
}

func readerFor(store adapter.Adapter, binding Binding, bound bool) Reader {
	return Reader{
		Lookup: func(agentcontrol.Target) (Binding, bool, error) {
			return binding, bound, nil
		},
		Adapters: func(string) (adapter.Adapter, bool) { return store, true },
	}
}

// TestTheBoundConversationIsReturnedNotTheNewestInTheSameDirectory is the M3
// exit gate for transcript reads.
func TestTheBoundConversationIsReturnedNotTheNewestInTheSameDirectory(t *testing.T) {
	store := twoSessionsInOneDirectory()
	binding := Binding{Kind: "codex", Ref: agentsession.Ref{
		Kind: agentsession.RefID, Value: "sess-bound",
		Source: "sidecar.codex.hooks", Reported: true,
	}}
	r := readerFor(store, binding, true)

	got, err := r.SessionMessages(context.Background(), agentcontrol.Target{Session: "s"}, 0)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("messages = %+v", got)
	}
	for _, m := range got {
		if !strings.Contains(m.Text, "bound conversation") {
			t.Fatalf("a message came from the wrong conversation: %+v", got)
		}
	}
	// The store must have been asked for exactly the bound session and nothing
	// else. A reader that listed sessions and chose would still pass the
	// content check above by luck of ordering; this cannot.
	if len(store.asked) != 1 || store.asked[0] != "sess-bound" {
		t.Fatalf("the reader asked for %v; it must ask for exactly the bound session", store.asked)
	}
}

func TestNoBindingIsARefusalRatherThanAGuess(t *testing.T) {
	store := twoSessionsInOneDirectory()
	r := readerFor(store, Binding{}, false)

	_, err := r.SessionMessages(context.Background(), agentcontrol.Target{Session: "s"}, 0)
	if err == nil {
		t.Fatal("an unbound shell returned a transcript")
	}
	if !strings.Contains(err.Error(), "no exact provider-reported session binding") {
		t.Fatalf("refusal = %v", err)
	}
	if len(store.asked) != 0 {
		t.Fatalf("the reader read %v despite having no binding", store.asked)
	}
}

// TestAnUnavailableAdapterRefusesRatherThanFallingBack proves the Conversations
// stack being absent is a refusal, not a reason to reach for the terminal or a
// nearby session.
func TestAnUnavailableAdapterRefusesRatherThanFallingBack(t *testing.T) {
	r := Reader{
		Lookup: func(agentcontrol.Target) (Binding, bool, error) {
			return Binding{Kind: "codex", Ref: agentsession.Ref{Kind: agentsession.RefID, Value: "x", Reported: true}}, true, nil
		},
		Adapters: func(string) (adapter.Adapter, bool) { return nil, false },
	}
	_, err := r.SessionMessages(context.Background(), agentcontrol.Target{}, 0)
	if err == nil || !strings.Contains(err.Error(), "no conversation adapter") {
		t.Fatalf("err = %v, wanted an adapter-unavailable refusal", err)
	}
}

func TestAnUnknownProviderRefuses(t *testing.T) {
	store := twoSessionsInOneDirectory()
	r := readerFor(store, Binding{Kind: "nosuchprovider", Ref: agentsession.Ref{
		Kind: agentsession.RefID, Value: "sess-bound", Reported: true,
	}}, true)
	_, err := r.SessionMessages(context.Background(), agentcontrol.Target{}, 0)
	if err == nil || !strings.Contains(err.Error(), "no provider named") {
		t.Fatalf("err = %v", err)
	}
}

// TestAPathBindingResolvesThroughTheAdaptersOwnRule matters because Codex says
// its rollout basenames are not session identities: the adapter's resolver has
// to be asked rather than the filename parsed here.
func TestAPathBindingResolvesThroughTheAdaptersOwnRule(t *testing.T) {
	base := twoSessionsInOneDirectory()
	base.pathIDs = map[string]string{
		"/home/u/.codex/sessions/2026/08/30/rollout-2026-08-30T13-39-00-sess-bound.jsonl": "sess-bound",
	}
	store := pathStore{base}

	r := readerFor(store, Binding{Kind: "codex", Ref: agentsession.Ref{
		Kind:     agentsession.RefPath,
		Value:    "/home/u/.codex/sessions/2026/08/30/rollout-2026-08-30T13-39-00-sess-bound.jsonl",
		Reported: true,
	}}, true)

	got, err := r.SessionMessages(context.Background(), agentcontrol.Target{}, 0)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(got) != 2 || !strings.Contains(got[0].Text, "bound conversation") {
		t.Fatalf("messages = %+v", got)
	}
	if len(base.asked) != 1 || base.asked[0] != "sess-bound" {
		t.Fatalf("asked = %v; the adapter's own path resolver should have named the session", base.asked)
	}
}

// TestAPathBindingFallsBackToTheBasenameRule covers the adapters whose files
// genuinely are named for their session, like Claude Code's <uuid>.jsonl.
func TestAPathBindingFallsBackToTheBasenameRule(t *testing.T) {
	store := &fakeStore{
		id:       "claude-code",
		sessions: map[string][]adapter.Message{"sess-bound": {message("user", "hello from the bound conversation")}},
		order:    []string{"sess-bound"},
	}
	r := readerFor(store, Binding{Kind: "claude", Ref: agentsession.Ref{
		Kind:     agentsession.RefPath,
		Value:    "/home/u/.claude/projects/-home-u-code/sess-bound.jsonl",
		Reported: true,
	}}, true)

	got, err := r.SessionMessages(context.Background(), agentcontrol.Target{}, 0)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Text, "bound conversation") {
		t.Fatalf("messages = %+v", got)
	}
}

// TestTheCatalogChoosesTheAdapterForTheProvider pins the kind -> adapter-id
// mapping, which is not identity: the catalog kind is "claude" and the
// conversation adapter registers as "claude-code".
func TestTheCatalogChoosesTheAdapterForTheProvider(t *testing.T) {
	var askedFor string
	store := &fakeStore{
		id:       "claude-code",
		sessions: map[string][]adapter.Message{"x": {message("user", "hi")}},
	}
	r := Reader{
		Lookup: func(agentcontrol.Target) (Binding, bool, error) {
			return Binding{Kind: "claude", Ref: agentsession.Ref{Kind: agentsession.RefID, Value: "x", Reported: true}}, true, nil
		},
		Adapters: func(id string) (adapter.Adapter, bool) {
			askedFor = id
			return store, true
		},
	}
	if _, err := r.SessionMessages(context.Background(), agentcontrol.Target{}, 0); err != nil {
		t.Fatal(err)
	}
	if askedFor != "claude-code" {
		t.Fatalf("looked up adapter %q; the claude family's conversation adapter is claude-code", askedFor)
	}
}

func TestTheLimitTakesTheRecentEnd(t *testing.T) {
	store := &fakeStore{
		id: "codex",
		sessions: map[string][]adapter.Message{"x": {
			message("user", "one"), message("assistant", "two"),
			message("user", "three"), message("assistant", "four"),
		}},
	}
	r := readerFor(store, Binding{Kind: "codex", Ref: agentsession.Ref{
		Kind: agentsession.RefID, Value: "x", Reported: true,
	}}, true)

	got, err := r.SessionMessages(context.Background(), agentcontrol.Target{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "three" || got[1].Text != "four" {
		t.Fatalf("messages = %+v, wanted the last two", got)
	}
}

// TestTheReaderSatisfiesTheInjectedSeam is the compile-time half of the
// contract: agentcontrol asks for this interface and nothing more, which is
// what keeps terminal control independent of the conversation stores.
func TestTheReaderSatisfiesTheInjectedSeam(t *testing.T) {
	var _ agentcontrol.TranscriptReader = Reader{}
}
