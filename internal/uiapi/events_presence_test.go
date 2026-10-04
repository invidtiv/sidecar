package uiapi

import (
	"context"
	"testing"
	"time"
)

func TestEventTerminalsProjectsNegotiatedHolderAndExplicitRelease(t *testing.T) {
	b := &eventBackend{fakeBackend: newFakeBackend(), holder: &GeometryHolder{Kind: "api", Label: "API terminal"}}
	s := &Server{opts: Options{Backend: b}, clients: newClientRegistry(time.Now)}
	who := caller{listener: ListenerLocal, auth: "local", client: "local"}
	client, ok := s.clients.add("terminal", who)
	if !ok {
		t.Fatal("add")
	}
	client.observe([]byte(`{"version":0,"type":"opened","target":{"session":"one","pane":"%1"}}`))
	check := func(want *GeometryHolder) {
		t.Helper()
		terms := s.eventTerminals(context.Background(), who)
		if len(terms) != 1 {
			t.Fatal(terms)
		}
		got := terms[0].Holder
		if want == nil {
			if got != nil {
				t.Fatal("released holder inferred from legacy source", got)
			}
			return
		}
		if got == nil || *got != *want {
			t.Fatalf("holder=%v want=%v", got, want)
		}
	}
	check(b.holder)
	client.observe([]byte(`{"version":0,"type":"holder","holder":{"kind":"ios","label":"iPhone"}}`))
	check(&GeometryHolder{Kind: "ios", Label: "iPhone"})
	client.observe([]byte(`{"version":0,"type":"holder","holder":{"kind":"","label":""}}`))
	check(nil)
	// Credential scoping remains in the shared registry projection.
	if terms := s.eventTerminals(context.Background(), caller{client: "another"}); len(terms) != 0 {
		t.Fatal(terms)
	}
}
