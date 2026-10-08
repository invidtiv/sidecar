package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/accessmodal"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uiapi"
)

type fakeAccessClient struct{ approved []string }

func (f *fakeAccessClient) ListAccessRequests(context.Context) (uiapi.AccessRequestList, error) {
	return uiapi.AccessRequestList{Requests: []uiapi.AccessRequestInfo{{RequestID: "r1", Label: "Firefox on Linux", Origin: "http://127.0.0.1:7861", Address: "127.0.0.1"}}}, nil
}

func (f *fakeAccessClient) ApproveAccess(_ context.Context, code, surface string) (uiapi.AccessApproval, error) {
	f.approved = append(f.approved, code+"/"+surface)
	return uiapi.AccessApproval{Label: "Firefox on Linux", Address: "127.0.0.1"}, nil
}

func accessTestModel(m tea.Model) Model {
	if pointer, ok := m.(*Model); ok {
		return *pointer
	}
	return m.(Model)
}

func runCmdMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	return cmd()
}

// Activating an access_request notification opens the approval modal, which
// approves through the Local socket client as the TUI. Other notifications
// keep their own activation.
func TestActivatingAnAccessRequestOpensTheApprovalModal(t *testing.T) {
	client := &fakeAccessClient{}
	previous := accessApprovalClient
	accessApprovalClient = client
	t.Cleanup(func() { accessApprovalClient = previous })

	m := centreTestModel(t, &sizingPlugin{id: "files"})
	postCentreNotification(t, &m, notify.SourceAccessRequest, "A browser is asking for access")
	m.toggleNotificationCentre()
	m.notificationCentreCursor = 0
	handled, cmd := m.notificationCentreKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || m.activeModal() != ModalAccessApproval || m.activeContext != accessApprovalContext {
		t.Fatalf("enter on an access request: handled=%v modal=%v context=%q", handled, m.activeModal(), m.activeContext)
	}
	updated, _ := m.Update(runCmdMsg(t, cmd))
	m = accessTestModel(updated)
	if view := m.View().Content; !strings.Contains(view, `"Firefox on Linux"`) || !strings.Contains(view, "Browser access requested") {
		t.Fatalf("modal does not list the waiting browser:\n%s", view)
	}
	// Keys go to the code field while the modal is open; a typed q does not
	// quit or reach the centre.
	for _, r := range "k7q4mx" {
		updated, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = accessTestModel(updated)
	}
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = accessTestModel(updated)
	approved := runCmdMsg(t, cmd)
	if _, ok := approved.(accessmodal.ApprovedMsg); !ok {
		t.Fatalf("enter produced %T", approved)
	}
	updated, _ = m.Update(approved)
	m = accessTestModel(updated)
	if len(client.approved) != 1 || client.approved[0] != "k7q4mx/tui" {
		t.Fatalf("approvals = %v", client.approved)
	}
	if m.activeModal() != ModalNone {
		t.Fatal("the modal stayed open after approval")
	}

	// Esc closes it without approving.
	if cmd := m.openAccessApproval(); cmd == nil || m.activeModal() != ModalAccessApproval {
		t.Fatal("reopen failed")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = accessTestModel(updated)
	if m.activeModal() != ModalNone || len(client.approved) != 1 {
		t.Fatal("esc did not close the modal cleanly")
	}
}
