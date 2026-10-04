package tty

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeadlessPresenceArbitratesAndReleases(t *testing.T) {
	g, channel := headlessGeometryHarness(t)
	now := time.Unix(1700000000, 0)
	g.now = func() time.Time { return now }
	call := func(token string, idle time.Duration, wantOwn bool, index int) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			owned, err := g.Presence(true, true, idle, 100, 30, false)
			if err == nil && owned != wantOwn {
				err = fmt.Errorf("owned = %v, want %v", owned, wantOwn)
			}
			done <- err
		}()
		read := waitForControlCommand(t, channel, "#{session_created}", index)
		respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\t" + token}, nil)
		if wantOwn {
			write := waitForControlCommand(t, channel, "if-shell", 0)
			if !strings.Contains(write.text, "resize-window -t %7 -x 100 -y 30") || !strings.Contains(write.text, "@sidecar-holder-owner") {
				t.Fatalf("missing fitted geometry or bound metadata: %s", write.text)
			}
			respondHeadlessCommand(channel, write.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	call("another-123:7:0", 20*time.Second, false, 0)
	call("another-123:8:20", 0, true, 1)
	if g.token == "" {
		t.Fatal("preempting recent activity did not retain ownership")
	}
	old := g.token
	done := make(chan error, 1)
	go func() {
		owned, err := g.Presence(false, true, 0, 100, 30, false)
		if owned {
			t.Error("unfocused viewer retained geometry")
		}
		done <- err
	}()
	release := waitForControlCommand(t, channel, "set-option -u", 0)
	if !strings.Contains(release.text, "#{==:#{@sidecar-owner},"+old+"}") {
		t.Fatal("blur does not guard its exact token")
	}
	respondHeadlessCommand(channel, release.text, controlResponse{Lines: []string{headlessOwnerMismatch}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if g.token != "" {
		t.Fatal("blur retained token")
	}
}

func TestHeadlessPresenceCannotAgeFreshOwnerWithBurstHeartbeats(t *testing.T) {
	g, channel := headlessGeometryHarness(t)
	now := time.Unix(1700000000, 0)
	g.now = func() time.Time { return now }
	for i := 0; i < 8; i++ {
		done := make(chan bool, 1)
		go func() {
			owned, err := g.Presence(true, true, time.Minute, 80, 24, false)
			if err != nil {
				t.Error(err)
			}
			done <- owned
		}()
		read := waitForControlCommand(t, channel, "#{session_created}", i)
		respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\tother-123:1:60"}, nil)
		if <-done {
			t.Fatal("burst heartbeat stole fresh geometry")
		}
	}
	if countControlCommands(channel, "if-shell") != 0 {
		t.Fatal("non-owning heartbeat mutated geometry")
	}
}

func TestHeadlessPresenceStalenessUsesElapsedTime(t *testing.T) {
	g, channel := headlessGeometryHarness(t)
	now := time.Unix(1700000000, 0)
	g.now = func() time.Time { return now }
	for i := 0; i < 12; i++ {
		done := make(chan bool, 1)
		go func() {
			owned, err := g.Presence(true, true, time.Minute, 80, 24, false)
			if err != nil {
				t.Error(err)
			}
			done <- owned
		}()
		read := waitForControlCommand(t, channel, "#{session_created}", i)
		respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\tother-123:1:60"}, nil)
		if <-done {
			t.Fatal("headless tick cadence preempted a desktop before the elapsed stale budget")
		}
		now = now.Add(time.Second)
	}
	now = now.Add(DefaultLeasePolicy.StaleAfter)
	done := make(chan bool, 1)
	go func() {
		owned, err := g.Presence(true, true, time.Minute, 80, 24, false)
		if err != nil {
			t.Error(err)
		}
		done <- owned
	}()
	read := waitForControlCommand(t, channel, "#{session_created}", 12)
	respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\tother-123:1:60"}, nil)
	write := waitForControlCommand(t, channel, "if-shell", 0)
	respondHeadlessCommand(channel, write.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
	if !<-done {
		t.Fatal("expired owner prevented presence from claiming geometry")
	}
}

func TestHeadlessPresenceReclaimsOnlyDeadLocalOwner(t *testing.T) {
	for _, tc := range []struct {
		name, token                  string
		alive, wantOwned, wantProbed bool
	}{
		{"dead local desktop", "aerie-9012:37:0", false, true, true},
		{"live local desktop", "aerie-9012:37:0", true, false, true},
		{"foreign desktop", "other-host-9012:37:0", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, channel := headlessGeometryHarness(t)
			g.presence.selfHost = "aerie"
			probed := false
			g.presence.alive = func(pid int) bool {
				if pid != 9012 {
					t.Errorf("probed PID %d", pid)
				}
				probed = true
				return tc.alive
			}
			type result struct {
				owned bool
				err   error
			}
			done := make(chan result, 1)
			go func() { owned, err := g.Presence(true, true, time.Minute, 80, 24, false); done <- result{owned, err} }()
			read := waitForControlCommand(t, channel, "#{session_created}", 0)
			respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\t" + tc.token}, nil)
			if tc.wantOwned {
				claim := waitForControlCommand(t, channel, "if-shell", 0)
				respondHeadlessCommand(channel, claim.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
			}
			got := <-done
			if got.err != nil || got.owned != tc.wantOwned || probed != tc.wantProbed {
				t.Fatalf("owned=%v probed=%v error=%v, want owned=%v probed=%v", got.owned, probed, got.err, tc.wantOwned, tc.wantProbed)
			}
		})
	}
}

func TestHeadlessClaimInputIgnoresLeaseRaceButGuardsIncarnation(t *testing.T) {
	g, channel := headlessGeometryHarness(t)
	if err := g.SetHolderLabel("browser", "Marcus’s laptop"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- g.ClaimInput([]byte("proof\r"), 90, 28, false) }()
	read := waitForControlCommand(t, channel, "#{session_created}", 0)
	respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\tcompetitor-456:8:0"}, nil)
	write := waitForControlCommand(t, channel, "send-keys", 0)
	if strings.Contains(write.text, "#{==:#{@sidecar-owner}") {
		t.Fatal("input can still be refused due to a foreign lease")
	}
	if !strings.Contains(write.text, "#{pid}|#{session_id}|#{session_created}|#{session_name}|#{pane_id},42|$3|1700000000|mobile|%7") {
		t.Fatal("input does not recheck bound incarnation")
	}
	if strings.Index(write.text, "set-option -t mobile @sidecar-owner") > strings.Index(write.text, "send-keys") {
		t.Fatal("input runs before claim")
	}
	respondHeadlessCommand(channel, write.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessHolderMetadataMustBelongToCurrentOwner(t *testing.T) {
	for _, tc := range []struct{ token, metadataOwner, kind, label, wantKind, wantLabel string }{
		{"macbook-123:4:0", "ios-456", "ios", "iPhone", "tui", "TUI on macbook"},
		{"ios-456:4:0", "ios-456", "ios", "iPhone", "ios", "iPhone"},
		{"", "ios-456", "ios", "iPhone", "", ""},
		{"aerie-mobile-abcdef-123:4:0", "", "", "", "unknown", "Another viewer"},
	} {
		g, channel := headlessGeometryHarness(t)
		done := make(chan error, 1)
		go func() {
			kind, label, err := g.Holder()
			if kind != tc.wantKind || label != tc.wantLabel {
				t.Errorf("holder = %q %q, want %q %q", kind, label, tc.wantKind, tc.wantLabel)
			}
			done <- err
		}()
		read := waitForControlCommand(t, channel, "@sidecar-holder-label", 0)
		respondHeadless(read, []string{strings.Join([]string{"42", "$3", "1700000000", "mobile", "%7", tc.token, tc.metadataOwner, tc.kind, tc.label}, "\t")}, nil)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestHeadlessPasteLoadsBeforeGuardAndCleansUpOnOwnershipLoss(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	g, channel := headlessGeometryHarness(t)
	g.token = "aerie-mobile-abcdef-123:1:0"
	done := make(chan error, 1)
	go func() { done <- g.Paste([]byte("private\nbytes")) }()
	load := waitForControlCommand(t, channel, "load-buffer", 0)
	if strings.Contains(load.text, "if-shell") {
		t.Fatal("asynchronous buffer load is inside ownership transaction")
	}
	words := strings.Fields(load.text)
	path := strings.Trim(words[len(words)-1], "'\"")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("paste file permissions = %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "private\nbytes" {
		t.Fatalf("private paste file = %q: %v", data, err)
	}
	respondHeadless(load, nil, nil)
	paste := waitForControlCommand(t, channel, "paste-buffer", 0)
	if !strings.Contains(paste.text, "#{==:#{@sidecar-owner},aerie-mobile-abcdef-123:1:0}") {
		t.Fatal("paste delivery is not exact-token guarded")
	}
	if strings.Contains(paste.text, "load-buffer") {
		t.Fatal("claim can be interleaved during file loading")
	}
	respondHeadlessCommand(channel, paste.text, controlResponse{Lines: []string{headlessOwnerMismatch}})
	release := waitForControlCommand(t, channel, "set-option -u", 0)
	respondHeadlessCommand(channel, release.text, controlResponse{Lines: []string{headlessOwnerMismatch}})
	cleanup := waitForControlCommand(t, channel, "delete-buffer", 0)
	if !strings.Contains(cleanup.text, words[2]) {
		t.Fatal("paste cleanup did not name its own private buffer")
	}
	respondHeadless(cleanup, nil, nil)
	if err = <-done; err == nil {
		t.Fatal("paste accepted ownership loss")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("private paste file survived failed delivery: %v", err)
	}
}

func TestHeadlessPresenceAndBracketedPasteAgainstPrivateTmux(t *testing.T) {
	srv := startPasteTmux(t)
	socket := srv.sock
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", srv.root)
	t.Setenv("XDG_STATE_HOME", filepath.Join(srv.root, "state"))
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	run := func(args ...string) string { return strings.TrimSpace(srv.run(args...)) }
	output := srv.startSink("presence", true)
	pane := run("display-message", "-p", "-t", "presence", "#{pane_id}")
	// Inspect on this explicit socket, without consulting any ambient server.
	identity, err := parseHeadlessTargetIdentity(run("display-message", "-p", "-t", pane, headlessTargetFormat), "presence", pane)
	if err != nil {
		t.Fatal(err)
	}
	manager := newControlManager(func(session string) (controlChannel, error) {
		return newProcessControlChannelForSocket(socket, session)
	}, 5*time.Millisecond)
	t.Cleanup(manager.Stop)
	sub, err := manager.Subscribe(ControlRequest{Session: identity.Session, Pane: pane, Visible: true, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	waitFor(t, sub.UsingControl)
	g, err := NewHeadlessGeometry(manager, identity, "browser-proof-123")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.SetHolderLabel("browser", "Proof browser"); err != nil {
		t.Fatal(err)
	}
	owned, err := g.Presence(true, true, 0, 100, 30, false)
	if err != nil || !owned {
		t.Fatalf("presence owned=%v: %v", owned, err)
	}
	kind, label, err := g.Holder()
	if err != nil || kind != "browser" || label != "Proof browser" {
		t.Fatalf("holder %q %q: %v", kind, label, err)
	}
	run("set-option", "-t", "presence", leaseOptionName, "competitor-456:1:0")
	want := "\x1b[200~first\rsecond\x1b[201~"
	waitFor(t, func() bool {
		return run("display-message", "-p", "-t", pane, "#{pane_in_mode}\t#{pane_width}") == "0\t100"
	})
	if err = g.ClaimInput([]byte("first\nsecond"), 100, 30, true); err != nil {
		t.Fatal(err)
	}
	waitForPasteContent(t, output, want)
	if err = g.Paste([]byte("third")); err != nil {
		t.Fatal(err)
	}
	waitForPasteContent(t, output, want+"\x1b[200~third\x1b[201~")
	if buffers := run("list-buffers", "-F", "#{buffer_name}"); buffers != "" {
		t.Fatalf("paste leaked buffers: %q", buffers)
	}
	if _, err = g.Presence(false, true, 0, 100, 30, false); err != nil {
		t.Fatal(err)
	}
	if owner := run("show-options", "-qv", "-t", "presence", leaseOptionName); owner != "" {
		t.Fatalf("blur retained owner %q", owner)
	}
}

func TestLeaseOwnerDefunctRecognizesHeadlessAttachmentHost(t *testing.T) {
	for _, tc := range []struct {
		token             string
		live, want, probe bool
	}{
		{"aerie-mobile-123456abcdef-123:1:0", false, true, true},
		{"aerie-mobile-123456abcdef-123:1:0", true, false, true},
		{"other-mobile-123456abcdef-123:1:0", false, false, false},
		{"aerie-mobile-not-a-token-123:1:0", false, false, false},
	} {
		called := false
		got := leaseOwnerDefunct(tc.token, "aerie", func(pid int) bool {
			called = true
			if pid != 123 {
				t.Fatalf("pid=%d", pid)
			}
			return tc.live
		})
		if got != tc.want || called != tc.probe {
			t.Fatalf("%s got=%v probe=%v", tc.token, got, called)
		}
	}
}

func TestExplicitLabelledClaimPublishesDeclaredViewer(t *testing.T) {
	g, channel := headlessGeometryHarness(t)
	if err := g.SetHolderLabel("browser", "Browser on aerie"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- g.ClaimResize(80, 24) }()
	read := waitForControlCommand(t, channel, "#{session_created}", 0)
	respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\t"}, nil)
	write := waitForControlCommand(t, channel, "if-shell", 0)
	if !strings.Contains(write.text, headlessHolderKind) || !strings.Contains(write.text, "Browser on aerie") {
		t.Fatal("explicit claim did not atomically publish viewer label")
	}
	respondHeadlessCommand(channel, write.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFocusedAttachmentsDoNotPingPongAndPasteCannotInjectCommands(t *testing.T) {
	srv := startPasteTmux(t)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	output := srv.startSink("peers", true)
	run := func(args ...string) string { return strings.TrimSpace(srv.run(args...)) }
	pane := run("display-message", "-p", "-t", "peers", "#{pane_id}")
	identity, err := parseHeadlessTargetIdentity(run("display-message", "-p", "-t", pane, headlessTargetFormat), "peers", pane)
	if err != nil {
		t.Fatal(err)
	}
	manager := newControlManager(func(session string) (controlChannel, error) {
		return newProcessControlChannelForSocket(srv.sock, session)
	}, 5*time.Millisecond)
	t.Cleanup(manager.Stop)
	sub, err := manager.Subscribe(ControlRequest{Session: identity.Session, Pane: pane, Visible: true, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	waitFor(t, sub.UsingControl)
	host, pid := hostAndPID()
	now := time.Now()
	newViewer := func(discriminator, kind, label string) *HeadlessGeometry {
		t.Helper()
		g, e := NewHeadlessGeometry(manager, identity, fmt.Sprintf("%s-mobile-%s-%d", host, discriminator, pid))
		if e != nil {
			t.Fatal(e)
		}
		g.now = func() time.Time { return now }
		if e = g.SetHolderLabel(kind, label); e != nil {
			t.Fatal(e)
		}
		return g
	}
	a := newViewer("012345abcdef", "browser", "Browser")
	b := newViewer("abcdef012345", "ios", "iPhone")
	if owned, e := a.Presence(true, true, 0, 100, 30, false); e != nil || !owned {
		t.Fatalf("first viewer claim: %v %v", owned, e)
	}
	// Both attachments share a process but must retain separate lease identities.
	// Advance beyond the stale budget while the owner keeps heartbeating.
	for i := 0; i < 15; i++ {
		idle := time.Duration(i) * 5 * time.Second
		if owned, e := a.Presence(true, true, idle, 100, 30, false); e != nil || !owned {
			t.Fatalf("owner heartbeat %d: %v %v", i, owned, e)
		}
		if owned, e := b.Presence(true, true, idle, 80, 24, false); e != nil || owned {
			t.Fatalf("competing heartbeat %d: %v %v", i, owned, e)
		}
		if got := run("display-message", "-p", "-t", pane, "#{pane_width}x#{pane_height}"); got != "100x30" {
			t.Fatalf("focused peers changed geometry: %s", got)
		}
		now = now.Add(5 * time.Second)
	}
	payload := "\"; set-option -g @sidecar-review-injected yes ; '#{pid}"
	if err = b.ClaimInput([]byte(payload), 80, 24, true); err != nil {
		t.Fatal(err)
	}
	waitForPasteContent(t, output, "\x1b[200~"+payload+"\x1b[201~")
	if got := run("show-options", "-gqv", "@sidecar-review-injected"); got != "" {
		t.Fatalf("paste injected a tmux command: %q", got)
	}
	if buffers := run("list-buffers", "-F", "#{buffer_name}"); buffers != "" {
		t.Fatalf("paste leaked buffers: %q", buffers)
	}
	if _, err = a.Presence(false, true, 0, 100, 30, false); err != nil {
		t.Fatal(err)
	}
	if owner := leaseOwner(run("show-options", "-qv", "-t", "peers", leaseOptionName)); owner != b.ownerID {
		t.Fatalf("losing viewer blur cleared winner's lease: %q", owner)
	}
	if kind, label, e := a.Holder(); e != nil || kind != "ios" || label != "iPhone" {
		t.Fatalf("holder must expose only the winner's label: %q %q %v", kind, label, e)
	}
	if _, err = b.Presence(false, true, 0, 80, 24, false); err != nil {
		t.Fatal(err)
	}
	if owner := run("show-options", "-qv", "-t", "peers", leaseOptionName); owner != "" {
		t.Fatalf("winning viewer blur retained ownership: %q", owner)
	}
}
