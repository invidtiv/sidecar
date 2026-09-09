package tty

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseHeadlessTargetIdentityChecksExactSessionAndPane(t *testing.T) {
	line := "42\t$3\t1700000000\tsidecar-ws-demo\t%7\t80\t24"
	got, err := parseHeadlessTargetIdentity(line, "sidecar-ws-demo", "%7")
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerPID != 42 || got.Session != "sidecar-ws-demo" || got.Pane != "%7" || got.Width != 80 || got.Height != 24 {
		t.Fatalf("identity = %+v", got)
	}
	for _, tc := range []struct{ session, pane string }{{"other", "%7"}, {"sidecar-ws-demo", "%8"}} {
		if _, err := parseHeadlessTargetIdentity(line, tc.session, tc.pane); err == nil {
			t.Fatalf("accepted mismatched session=%q pane=%q", tc.session, tc.pane)
		}
	}
}

func TestParseHeadlessTargetIdentityRetainsGeometryBounds(t *testing.T) {
	for _, line := range []string{
		"42\t$3\t1700000000\tsession\t%7\t1\t24",
		"42\t$3\t1700000000\tsession\t%7\t80\t0",
		"0\t$3\t1700000000\tsession\t%7\t80\t24",
		"42\t$3\t1700000000\tsession\tbad\t80\t24",
	} {
		if _, err := parseHeadlessTargetIdentity(line, "session", ""); err == nil {
			t.Fatalf("accepted invalid identity %q", line)
		}
	}
}

func TestHeadlessPaneInspectionUsesAttachedControlConnection(t *testing.T) {
	g, channel := headlessGeometryHarness(t)
	t.Setenv("PATH", t.TempDir())
	done := make(chan error, 1)
	go func() {
		got, err := g.manager.InspectHeadlessPane(context.Background(), "mobile", "%7")
		if err == nil && (got.Session != "mobile" || got.Pane != "%7" || got.ServerPID != 42 || got.PaneCount != 1) {
			t.Errorf("inspection = %+v", got)
		}
		done <- err
	}()
	read := waitForControlCommand(t, channel, "#{session_created}", 0)
	respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\t80\t24\t1"}, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessPaneInspectionRefusesUnavailableAttachment(t *testing.T) {
	g, _ := headlessGeometryHarness(t)
	g.manager.mu.Lock()
	client := g.manager.clients["mobile"]
	delete(g.manager.clients, "mobile")
	g.manager.mu.Unlock()
	defer func() {
		g.manager.mu.Lock()
		g.manager.clients["mobile"] = client
		g.manager.mu.Unlock()
	}()
	if _, err := g.manager.InspectHeadlessPane(context.Background(), "mobile", "%7"); err == nil {
		t.Fatal("unavailable attachment used a fallback")
	}
}

func TestHeadlessPaneInventoryUsesAttachedControlConnection(t *testing.T) {
	g, channel := headlessGeometryHarness(t)
	t.Setenv("PATH", t.TempDir())
	done := make(chan error, 1)
	rows := []string{"%7\tmobile\t/fixture\tbash\tShell\t0\t123\t42\t24", "%8\tother\t/other\tbash\tOther\t0\t124\t42\t24"}
	go func() {
		got, err := g.manager.HeadlessPaneInventory(context.Background(), "mobile")
		if err == nil && string(got) != strings.Join(rows, "\n") {
			t.Errorf("inventory = %q", got)
		}
		done <- err
	}()
	read := waitForControlCommand(t, channel, "list-panes -a", 0)
	respondHeadless(read, rows, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessPaneInventoryDoesNotFallbackAfterActiveReadFailure(t *testing.T) {
	g, channel := headlessGeometryHarness(t)
	t.Setenv("PATH", t.TempDir())
	done := make(chan error, 1)
	go func() {
		_, err := g.manager.HeadlessPaneInventory(context.Background(), "mobile")
		done <- err
	}()
	read := waitForControlCommand(t, channel, "list-panes -a", 0)
	failure := errors.New("active control connection failed")
	respondHeadless(read, nil, failure)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatalf("failure was replaced by a fallback: %v", err)
	}
}
