package tty

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	headlessHolderOwner = "@sidecar-holder-owner"
	headlessHolderKind  = "@sidecar-holder-kind"
	headlessHolderLabel = "@sidecar-holder-label"
)

type headlessPresenceState struct {
	observed                           string
	observedSince, lastTick, lastWrite time.Time
	unchangedTicks, ticksSinceWrite    int
	kind, label                        string
	selfHost                           string
	alive                              func(int) bool
}

// SetHolderLabel supplies the human identity published alongside this attachment's
// ownership. Metadata is written in the same tmux transaction as the owner token.
func (g *HeadlessGeometry) SetHolderLabel(kind, label string) error {
	switch kind {
	case "tui", "browser", "ios", "cli", "unknown":
	default:
		return fmt.Errorf("tmux control: invalid holder kind")
	}
	if len(label) > 128 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return fmt.Errorf("tmux control: invalid holder label")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.presence.kind, g.presence.label = kind, label
	return nil
}

// Presence arbitrates fitted geometry using the same state-free rule as the
// desktop. A viewer without focus or visibility releases only its own token.
// force is reserved for explicit input, never a background heartbeat.
func (g *HeadlessGeometry) Presence(focused, visible bool, idle time.Duration, width, height int, force bool) (bool, error) {
	if err := validHeadlessGeometry(width, height); err != nil {
		return false, err
	}
	if idle < 0 {
		return false, fmt.Errorf("tmux control: negative presence idle")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	g.lastPresence, g.lastInput = now, now.Add(-idle)
	if !focused || !visible {
		return false, g.releaseLocked()
	}
	observed, err := g.readCurrent()
	if err != nil {
		return false, err
	}
	s := &g.presence
	if observed != s.observed || s.observedSince.IsZero() {
		s.observed, s.observedSince, s.unchangedTicks = observed, now, 0
	} else if s.lastTick.IsZero() || now.Sub(s.lastTick) >= time.Second {
		s.unchangedTicks++
	}
	if s.lastTick.IsZero() || now.Sub(s.lastTick) >= time.Second {
		s.lastTick = now
		s.ticksSinceWrite++
	}
	parsed := parseLeaseToken(observed)
	policy := DefaultLeasePolicy
	// Presence cadence is controlled by the peer, whereas desktop ticks are
	// controlled by its polling loop. Only elapsed time can establish that a
	// foreign holder has stopped refreshing across these different cadences.
	policy.StaleTicks = 0
	decision := DecideGeometryLease(LeaseObservation{
		SelfID: g.ownerID, Token: observed, Focused: true,
		UnchangedTicks: s.unchangedTicks, UnchangedFor: now.Sub(s.observedSince),
		TicksSinceWrite: s.ticksSinceWrite, SinceWrite: sinceWrite(s.lastWrite, now),
		SelfIdle: idle, OwnerIdle: parsed.idle, OwnerIdleKnown: parsed.idleKnown,
		OwnerDefunct: observed != "" && parsed.owner != g.ownerID && leaseOwnerDefunct(observed, s.selfHost, s.alive),
	}, policy)
	if !force && !decision.Resize {
		g.token = ""
		return false, nil
	}
	next := observed
	if force || decision.Write {
		next = g.nextToken(now)
	}
	operations, verify, guard, err := g.resizeOperations(width, height)
	if err != nil {
		return false, err
	}
	if err = g.presenceTransaction(observed, next, guard, false, operations...); err != nil {
		g.token = ""
		return false, g.cleanupAttempted(next, err)
	}
	if verify {
		if err = g.verifyPaneGeometry(width, height); err != nil {
			g.token = ""
			return false, g.cleanupAttempted(next, err)
		}
	}
	g.token = next
	if next != observed {
		s.lastWrite, s.ticksSinceWrite = now, 0
	}
	return true, nil
}

// ClaimInput claims geometry and delivers input in one identity-guarded tmux
// transaction. Input is deliberate activity and therefore outranks any lease;
// another viewer cannot interpose between the claim and delivery.
func (g *HeadlessGeometry) ClaimInput(data []byte, width, height int, paste bool) (err error) {
	if len(data) == 0 {
		return fmt.Errorf("tmux control: empty headless input")
	}
	if err := validHeadlessGeometry(width, height); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// Read still verifies that the existing control connection names the bound
	// incarnation. The transaction rechecks it, without refusing foreign leases.
	if _, err := g.readCurrent(); err != nil {
		return err
	}
	var input string
	var cleanup func() error
	if paste {
		input, cleanup, err = g.pasteOperation(data)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, cleanup()) }()
	} else {
		input = InBandSendLiteral(g.expected.Pane, string(data))
	}
	now := g.now()
	g.lastInput, g.lastPresence = now, now
	next := g.nextToken(now)
	operations, verify, guard, err := g.resizeOperations(width, height)
	if err != nil {
		return err
	}
	operations = append(operations, input)
	if err = g.presenceTransaction("", next, guard, true, operations...); err != nil {
		g.token = ""
		return g.cleanupAttempted(next, err)
	}
	g.token = next
	g.presence.lastWrite, g.presence.ticksSinceWrite = now, 0
	if verify {
		// Input already reached tmux. A subsequent external resize is reported
		// without retrying the input, which would duplicate a keystroke or paste.
		if err = g.verifyPaneGeometry(width, height); err != nil {
			return err
		}
	}
	return nil
}

// Paste delivers bytes through a private tmux buffer while retaining the exact
// ownership checks of SendLiteral. paste-buffer -p asks tmux to bracket the paste
// only when the target application has enabled bracketed-paste mode.
func (g *HeadlessGeometry) Paste(data []byte) (err error) {
	if len(data) == 0 {
		return fmt.Errorf("tmux control: empty headless paste")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token == "" {
		return fmt.Errorf("tmux control: geometry is not owned")
	}
	now := g.now()
	if err = g.requirePresenceLocked(now); err != nil {
		return err
	}
	operation, cleanup, err := g.pasteOperation(data)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cleanup()) }()
	g.lastInput = now
	next := g.nextToken(now)
	if err = g.presenceTransaction(g.token, next, "", false, operation); err != nil {
		g.token = ""
		return g.cleanupAttempted(next, err)
	}
	g.token, g.lastPresence = next, now
	return nil
}

func (g *HeadlessGeometry) pasteOperation(data []byte) (string, func() error, error) {
	file, err := os.CreateTemp("", "sidecar-paste-*") // CreateTemp creates mode 0600.
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", nil, err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(path)
		return "", nil, err
	}
	// The random filename is also a unique buffer identity, so no client can
	// overwrite another attachment's paste buffer during an interleaving.
	name := "sidecar-" + strings.TrimPrefix(path[strings.LastIndex(path, "/")+1:], "sidecar-")
	operation := "paste-buffer -d -p -b " + controlQuote(name) + " -t " + controlQuote(g.expected.Pane)
	cleanup := func() error {
		defer func() { _ = os.Remove(path) }()
		// The buffer normally disappeared via -d; absence is success. A failed
		// paste may leave a loaded buffer, which this exact-name delete removes.
		_, err := g.manager.requestControlBatch(g.expected.Session, "delete-buffer -b "+controlQuote(name))
		if err != nil && (strings.Contains(err.Error(), "no buffer") || strings.Contains(err.Error(), "unknown buffer: "+name)) {
			return nil
		}
		return err
	}
	// File loading may suspend tmux's command queue. Finish it before the
	// ownership transaction, so a foreign client cannot resize during a file
	// read between our claim and the eventual paste. The named buffer is not
	// delivered until the guarded paste-buffer command runs.
	_, err = g.manager.requestControlBatch(g.expected.Session, "load-buffer -b "+controlQuote(name)+" "+controlQuote(path))
	if err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return operation, cleanup, nil
}

func (g *HeadlessGeometry) identityGuard() string {
	want := strconv.Itoa(g.expected.ServerPID) + "|" + g.expected.SessionID + "|" + g.expected.SessionCreated + "|" + g.expected.Session + "|" + g.expected.Pane
	return "#{==:#{pid}|#{session_id}|#{session_created}|#{session_name}|#{pane_id}," + want + "}"
}

func (g *HeadlessGeometry) presenceTransaction(observed, next, layoutGuard string, force bool, operations ...string) error {
	guard := g.identityGuard()
	if layoutGuard != "" {
		guard = "#{&&:" + guard + "," + layoutGuard + "}"
	}
	if !force {
		guard = "#{&&:" + guard + ",#{==:#{" + leaseOptionName + "}," + observed + "}}"
	}
	kind, label := g.presence.kind, g.presence.label
	if kind == "" {
		kind = "unknown"
	}
	commands := []string{
		"set-option -t " + controlQuote(g.expected.Session) + " " + leaseOptionName + " " + controlQuote(next),
		"set-option -t " + controlQuote(g.expected.Session) + " " + headlessHolderOwner + " " + controlQuote(g.ownerID),
		"set-option -t " + controlQuote(g.expected.Session) + " " + headlessHolderKind + " " + controlQuote(kind),
		"set-option -t " + controlQuote(g.expected.Session) + " " + headlessHolderLabel + " " + controlQuote(label),
	}
	commands = append(commands, operations...)
	commands = append(commands, "display-message -p "+controlQuote(headlessOwnerSuccess))
	command := "if-shell -F -t " + controlQuote(g.expected.Pane) + " " + controlQuote(guard) + " " + controlQuote(strings.Join(commands, " ; ")) + " " + controlQuote("display-message -p "+controlQuote(headlessOwnerMismatch))
	responses, err := g.manager.requestControlTransaction(g.expected.Session, command)
	if err != nil {
		return fmt.Errorf("tmux control: strict presence mutation failed: %w", err)
	}
	if responseHasLine(responses, headlessOwnerMismatch) {
		return fmt.Errorf("tmux control: target identity, layout or geometry owner changed")
	}
	if !responseHasLine(responses, headlessOwnerSuccess) {
		return fmt.Errorf("tmux control: strict presence completion missing")
	}
	return nil
}

// Holder reads owner and label together. Labels from a previous owner are never
// attributed to a new owner; desktop instances fall back to their host name.
func (g *HeadlessGeometry) Holder() (kind, label string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	command := "display-message -t " + controlQuote(g.expected.Pane) + " -p " + controlQuote("#{pid}\t#{session_id}\t#{session_created}\t#{session_name}\t#{pane_id}\t#{"+leaseOptionName+"}\t#{"+headlessHolderOwner+"}\t#{"+headlessHolderKind+"}\t#{"+headlessHolderLabel+"}")
	responses, err := g.manager.requestControlBatch(g.expected.Session, command)
	if err != nil {
		return "", "", err
	}
	if len(responses) != 1 || len(responses[0].Lines) != 1 {
		return "", "", fmt.Errorf("tmux control: holder response missing")
	}
	parts := strings.Split(responses[0].Lines[0], "\t")
	if len(parts) != 9 || parts[0] != strconv.Itoa(g.expected.ServerPID) || parts[1] != g.expected.SessionID || parts[2] != g.expected.SessionCreated || parts[3] != g.expected.Session || parts[4] != g.expected.Pane {
		return "", "", fmt.Errorf("tmux control: holder identity changed")
	}
	owner := leaseOwner(parts[5])
	if parts[5] == "" {
		return "", "", nil
	}
	if parts[6] == owner && parts[7] != "" {
		return parts[7], parts[8], nil
	}
	if strings.Contains(owner, "-mobile-") {
		return "unknown", "Another viewer", nil
	}
	if host, _, ok := splitInstanceID(owner); ok {
		return "tui", "TUI on " + host, nil
	}
	return "unknown", "Another viewer", nil
}
