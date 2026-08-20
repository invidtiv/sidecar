package app

import (
	"net/url"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
)

type testIntentHandler struct{ namespace string }

func (h testIntentHandler) Namespace() string { return h.namespace }
func (h testIntentHandler) Parse(id string, _ url.Values) (Intent, error) {
	return id, nil
}
func (h testIntentHandler) Activate(IntentAppContext, Intent) tea.Cmd { return nil }

func TestIntentRegistryRejectsDuplicateAndInvalidNamespaces(t *testing.T) {
	if _, err := newIntentRegistry(testIntentHandler{"note"}, testIntentHandler{"note"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate namespace error = %v", err)
	}
	for _, namespace := range []string{"", "Note", "note-", "note/path"} {
		if _, err := newIntentRegistry(testIntentHandler{namespace}); err == nil {
			t.Errorf("invalid namespace %q was accepted", namespace)
		}
	}
}

func TestNoteIntentValidationAndActivationUseStableIDOnly(t *testing.T) {
	namespaces := sidecarIntents.namespaces()
	valid := namespaces["note"].ValidateID
	if valid == nil || !valid("nt-4jdj4e") {
		t.Fatal("canonical note ID was not registered")
	}
	for _, id := range []string{"4jdj4e", "NT-4jdj4e", "nt-a/b", "nt-a.b", "nt-" + strings.Repeat("a", 65)} {
		if valid(id) {
			t.Errorf("invalid note ID %q was accepted", id)
		}
	}
	ref := contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: "nt-4jdj4e"}
	cmd, err := sidecarIntents.activate(IntentAppContext{ProjectRoot: "/project"}, ref)
	if err != nil || cmd == nil {
		t.Fatalf("activate: cmd=%v err=%v", cmd != nil, err)
	}
	got, ok := cmd().(NavigateToNoteMsg)
	if !ok || got != (NavigateToNoteMsg{ID: "nt-4jdj4e", ProjectRoot: "/project"}) {
		t.Fatalf("activation message = %#v", cmd())
	}
	if _, err := sidecarIntents.activate(IntentAppContext{ProjectRoot: "/project"}, contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "missing", Value: "nt-4jdj4e"}); err == nil {
		t.Fatal("unknown namespace activated")
	}
}
