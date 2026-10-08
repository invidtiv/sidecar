package accessmodal

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/uiapi"
)

type fakeClient struct {
	requests []uiapi.AccessRequestInfo
	codes    []string
	surfaces []string
	refuse   *uiapi.APIError
}

func (f *fakeClient) ListAccessRequests(context.Context) (uiapi.AccessRequestList, error) {
	return uiapi.AccessRequestList{Requests: f.requests}, nil
}

func (f *fakeClient) ApproveAccess(_ context.Context, code, surface string) (uiapi.AccessApproval, error) {
	f.codes = append(f.codes, code)
	f.surfaces = append(f.surfaces, surface)
	if f.refuse != nil {
		return uiapi.AccessApproval{}, f.refuse
	}
	return uiapi.AccessApproval{RequestID: "r1", Label: "Safari on macOS", Address: "127.0.0.1", ApprovedVia: "tui"}, nil
}

func typeText(t *testing.T, h *Host, text string) {
	t.Helper()
	for _, r := range text {
		h.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)}, 120)
	}
}

func TestModalListsRequestsWithoutApprovingOnSelection(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{requests: []uiapi.AccessRequestInfo{{RequestID: "r1", Label: "Safari on macOS", Origin: "http://127.0.0.1:7861", Address: "127.0.0.1", CreatedAt: now, ExpiresAt: now.Add(4 * time.Minute)}}}
	h := New(t.TempDir(), client)
	h.now = func() time.Time { return now }
	if !strings.Contains(h.Render(120, 40), "Loading") {
		t.Fatal("no loading state before the list arrives")
	}
	h.ApplyLoaded(h.Load()().(LoadedMsg))
	view := h.Render(120, 40)
	for _, want := range []string{`"Safari on macOS"`, "127.0.0.1", "expires in 4m0s", "Code:", "Approve"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	// Enter with no code approves nothing and asks for the code.
	action, _ := h.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}, 120)
	if action != ActionApprove {
		t.Fatalf("enter action = %q", action)
	}
	if cmd := h.Submit(); cmd != nil || len(client.codes) != 0 {
		t.Fatal("an empty code reached the server")
	}
	if !strings.Contains(h.Render(120, 40), "six-character code") {
		t.Fatal("no hint for a missing code")
	}
	// A stale message from another modal is ignored.
	h.ApplyLoaded(LoadedMsg{Host: New(t.TempDir(), client)})
}

func TestModalApprovesTheTypedCodeAsTheTUI(t *testing.T) {
	client := &fakeClient{}
	h := New(t.TempDir(), client)
	h.Ensure(120)
	typeText(t, h, "k7q-4mx")
	cmd := h.Submit()
	if cmd == nil {
		t.Fatal("a well-formed code was not submitted")
	}
	if h.Submit() != nil {
		t.Fatal("a second submit while approving")
	}
	msg := cmd().(ApprovedMsg)
	if len(client.codes) != 1 || client.codes[0] != "k7q-4mx" || client.surfaces[0] != "tui" {
		t.Fatalf("approve call = %v %v", client.codes, client.surfaces)
	}
	done, flash, _ := h.ApplyApproved(msg)
	if !done || !strings.Contains(flash, `"Safari on macOS"`) {
		t.Fatalf("done=%v flash=%q", done, flash)
	}
}

func TestModalKeepsARefusalOnScreenAndReloads(t *testing.T) {
	client := &fakeClient{refuse: &uiapi.APIError{Status: 404, ErrorDetail: uiapi.ErrorDetail{Code: uiapi.CodeAccessCodeInvalid, Message: "No waiting browser shows that code."}}}
	h := New(t.TempDir(), client)
	h.Ensure(120)
	typeText(t, h, "000000")
	done, _, reload := h.ApplyApproved(h.Submit()().(ApprovedMsg))
	if done || reload == nil {
		t.Fatalf("a refusal closed the modal or skipped the reload (done=%v)", done)
	}
	if !strings.Contains(h.Render(120, 40), "No waiting browser shows that code.") {
		t.Fatal("refusal not shown")
	}
	if h.input.Value() != "" {
		t.Fatal("the refused code was left in the field")
	}
	typeText(t, h, "1")
	if strings.Contains(h.Render(120, 40), "No waiting browser shows that code.") {
		t.Fatal("typing a new code did not clear the refusal")
	}
}
