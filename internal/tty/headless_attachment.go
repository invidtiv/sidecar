package tty

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	headlessOwnerMismatch = "__sidecar_mobile_owner_mismatch__"
	headlessOwnerSuccess  = "__sidecar_mobile_owner_success__"
)

const HeadlessPresenceTimeout = 15 * time.Second

// HeadlessGeometry is the strict mobile mutation boundary over one existing
// control actor. Unlike desktop's polling lease keeper, every operation reads
// or conditionally compares the current owner at the tmux command boundary.
// Read, write, identity, or ownership uncertainty refuses the mutation.
type HeadlessGeometry struct {
	manager  *ControlManager
	expected HeadlessTargetIdentity
	ownerID  string

	mu           sync.Mutex
	token        string
	counter      uint64
	lastInput    time.Time
	lastPresence time.Time
	now          func() time.Time
}

func NewHeadlessGeometry(manager *ControlManager, expected HeadlessTargetIdentity, ownerID string) (*HeadlessGeometry, error) {
	if manager == nil || expected.Session == "" || !controlPanePattern.MatchString(expected.Pane) ||
		expected.ServerPID <= 0 || !ValidInstanceID(ownerID) {
		return nil, fmt.Errorf("tmux control: invalid headless geometry identity")
	}
	return &HeadlessGeometry{manager: manager, expected: expected, ownerID: ownerID, now: time.Now}, nil
}

// ClaimResize is a deliberate takeover. It first obtains a successful current
// identity/owner read, then atomically compares that exact observed owner before
// setting this attachment's token and resizing. A failed write is never treated
// as ownership.
func (g *HeadlessGeometry) ClaimResize(width, height int) error {
	if err := validHeadlessGeometry(width, height); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	observed, err := g.readCurrent()
	if err != nil {
		return err
	}
	now := g.now()
	g.lastInput = now
	g.lastPresence = now
	next := g.nextToken(now)
	resize := "resize-window -t " + controlQuote(g.expected.Pane) + " -x " + strconv.Itoa(width) + " -y " + strconv.Itoa(height)
	if err := g.compareAndRun(observed, next, resize); err != nil {
		return g.cleanupAttempted(next, err)
	}
	g.token = next
	return nil
}

func (g *HeadlessGeometry) Resize(width, height int) error {
	if err := validHeadlessGeometry(width, height); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token == "" {
		return fmt.Errorf("tmux control: geometry is not owned")
	}
	now := g.now()
	if err := g.requirePresenceLocked(now); err != nil {
		return err
	}
	next := g.nextToken(now)
	resize := "resize-window -t " + controlQuote(g.expected.Pane) + " -x " + strconv.Itoa(width) + " -y " + strconv.Itoa(height)
	if err := g.compareAndRun(g.token, next, resize); err != nil {
		g.token = ""
		return g.cleanupAttempted(next, err)
	}
	g.token = next
	g.lastPresence = now
	return nil
}

func (g *HeadlessGeometry) Heartbeat() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token == "" {
		return fmt.Errorf("tmux control: geometry is not owned")
	}
	now := g.now()
	if err := g.requirePresenceLocked(now); err != nil {
		return err
	}
	next := g.nextToken(now)
	if err := g.compareAndRun(g.token, next); err != nil {
		g.token = ""
		return g.cleanupAttempted(next, err)
	}
	g.token = next
	g.lastPresence = now
	return nil
}

// SendLiteral conditionally sends exact bytes only while tmux still carries
// this attachment's latest token. The response waits for tmux's command result,
// so success means tmux accepted the send-keys operation, never that the pane's
// foreground application processed it.
func (g *HeadlessGeometry) SendLiteral(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("tmux control: empty headless input")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token == "" {
		return fmt.Errorf("tmux control: geometry is not owned")
	}
	now := g.now()
	if err := g.requirePresenceLocked(now); err != nil {
		return err
	}
	g.lastInput = now
	next := g.nextToken(g.lastInput)
	if err := g.compareAndRun(g.token, next, InBandSendLiteral(g.expected.Pane, string(data))); err != nil {
		g.token = ""
		return g.cleanupAttempted(next, err)
	}
	g.token = next
	g.lastPresence = now
	return nil
}

// ExpirePresence conditionally releases this attachment once its phone has
// supplied neither heartbeat nor input within the advertised deadline.
func (g *HeadlessGeometry) ExpirePresence() (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token == "" || g.lastPresence.IsZero() || g.now().Sub(g.lastPresence) <= HeadlessPresenceTimeout {
		return false, nil
	}
	return true, g.releaseLocked()
}

// Release atomically clears only this attachment's exact latest token. If a
// different viewer took ownership first, its token is preserved.
func (g *HeadlessGeometry) Release() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.releaseLocked()
}

func (g *HeadlessGeometry) releaseLocked() error {
	if g.token == "" {
		return nil
	}
	_, err := g.conditionalClear(g.token)
	if err != nil {
		return err
	}
	g.token = ""
	return nil
}

func (g *HeadlessGeometry) conditionalClear(token string) (bool, error) {
	condition := "#{==:#{" + leaseOptionName + "}," + token + "}"
	trueCommands := []string{
		"set-option -u -t " + controlQuote(g.expected.Session) + " " + leaseOptionName,
		"display-message -p " + controlQuote(headlessOwnerSuccess),
	}
	command := "if-shell -F -t " + controlQuote(g.expected.Pane) + " " + controlQuote(condition) + " " +
		controlQuote(strings.Join(trueCommands, " ; ")) + " " + controlQuote("display-message -p "+controlQuote(headlessOwnerMismatch))
	responses, err := g.manager.requestControlTransaction(g.expected.Session, command)
	if err != nil {
		return false, err
	}
	if responseHasLine(responses, headlessOwnerSuccess) {
		return true, nil
	}
	if responseHasLine(responses, headlessOwnerMismatch) {
		return false, nil
	}
	return false, fmt.Errorf("tmux control: release completion missing")
}

func (g *HeadlessGeometry) cleanupAttempted(token string, operationErr error) error {
	_, cleanupErr := g.conditionalClear(token)
	if cleanupErr != nil {
		return errors.Join(operationErr, fmt.Errorf("tmux control: attempted owner cleanup: %w", cleanupErr))
	}
	return operationErr
}

func (g *HeadlessGeometry) requirePresenceLocked(now time.Time) error {
	if g.lastPresence.IsZero() || now.Sub(g.lastPresence) <= HeadlessPresenceTimeout {
		return nil
	}
	if err := g.releaseLocked(); err != nil {
		return fmt.Errorf("tmux control: mobile presence expired and release failed: %w", err)
	}
	return fmt.Errorf("tmux control: mobile presence expired")
}

func (g *HeadlessGeometry) readCurrent() (string, error) {
	command := "display-message -t " + controlQuote(g.expected.Pane) + " -p " + controlQuote(
		"#{pid}\t#{session_id}\t#{session_created}\t#{session_name}\t#{pane_id}\t#{"+leaseOptionName+"}")
	responses, err := g.manager.requestControlBatch(g.expected.Session, command)
	if err != nil || len(responses) != 1 || len(responses[0].Lines) != 1 {
		if err == nil {
			err = fmt.Errorf("missing identity response")
		}
		return "", fmt.Errorf("tmux control: strict owner read failed: %w", err)
	}
	parts := strings.Split(responses[0].Lines[0], "\t")
	if len(parts) != 6 {
		return "", fmt.Errorf("tmux control: malformed identity response")
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil || pid != g.expected.ServerPID || parts[1] != g.expected.SessionID ||
		parts[2] != g.expected.SessionCreated || parts[3] != g.expected.Session || parts[4] != g.expected.Pane {
		return "", fmt.Errorf("tmux control: target identity changed")
	}
	return parts[5], nil
}

func (g *HeadlessGeometry) compareAndRun(observed, next string, operations ...string) error {
	condition := "#{==:#{" + leaseOptionName + "}," + observed + "}"
	commands := []string{"set-option -t " + controlQuote(g.expected.Session) + " " + leaseOptionName + " " + controlQuote(next)}
	commands = append(commands, operations...)
	commands = append(commands, "display-message -p "+controlQuote(headlessOwnerSuccess))
	command := "if-shell -F -t " + controlQuote(g.expected.Pane) + " " + controlQuote(condition) + " " +
		controlQuote(strings.Join(commands, " ; ")) + " " + controlQuote("display-message -p "+controlQuote(headlessOwnerMismatch))
	responses, err := g.manager.requestControlTransaction(g.expected.Session, command)
	if err != nil {
		return fmt.Errorf("tmux control: strict owner write failed: %w", err)
	}
	if responseHasLine(responses, headlessOwnerMismatch) {
		return fmt.Errorf("tmux control: geometry owner changed")
	}
	if !responseHasLine(responses, headlessOwnerSuccess) {
		return fmt.Errorf("tmux control: strict owner completion missing")
	}
	return nil
}

func responseHasLine(responses []controlResponse, line string) bool {
	for _, response := range responses {
		for _, got := range response.Lines {
			if got == line {
				return true
			}
		}
	}
	return false
}

func (g *HeadlessGeometry) nextToken(now time.Time) string {
	g.counter++
	idle := int64(0)
	if !g.lastInput.IsZero() && now.After(g.lastInput) {
		idle = int64(now.Sub(g.lastInput).Seconds())
	}
	return fmt.Sprintf("%s:%d:%d", g.ownerID, g.counter, idle)
}

func validHeadlessGeometry(width, height int) error {
	if width < 2 || height < 1 || width > 512 || height > 256 {
		return fmt.Errorf("tmux control: invalid headless geometry %dx%d", width, height)
	}
	return nil
}
