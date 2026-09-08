package tty

import "testing"

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
