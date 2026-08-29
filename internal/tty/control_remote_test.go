package tty

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNewControlManagerLocalPathUnchanged is the rollback guarantee stated in
// the plan: adding remote support must leave the local path byte-identical.
// The local constructor must still build its own factory, not route through
// the remote one with a local spawner.
func TestNewControlManagerLocalPathUnchanged(t *testing.T) {
	manager := NewControlManager()
	if manager.factory == nil {
		t.Fatal("local manager has no factory")
	}
	if manager.coalesce == 0 {
		t.Error("local manager lost its capture coalesce window")
	}
	if _, err := manager.factory(""); err == nil {
		t.Error("local factory accepted an empty session")
	}
}

func TestNewRemoteControlManagerUsesTheSpawner(t *testing.T) {
	var asked string
	manager := NewRemoteControlManager(func(session string) *exec.Cmd {
		asked = session
		// `false` exits immediately, so the channel fails to attach without
		// ever contacting a tmux server — which is the point: this test must
		// not touch any tmux, local or remote.
		return exec.Command("false")
	})
	if _, err := manager.factory("proj-claude"); err == nil {
		t.Error("a spawner producing a dead process still yielded a channel")
	}
	if asked != "proj-claude" {
		t.Errorf("spawner asked for %q, want proj-claude", asked)
	}
	if manager.coalesce != NewControlManager().coalesce {
		t.Error("remote manager uses a different coalesce window than local; that is a decision, not an accident — update the comment if intended")
	}
}

func TestRemoteFactoryRejectsMissingSpawner(t *testing.T) {
	if _, err := spawnedControlChannelFactory(nil)("s"); err == nil {
		t.Error("nil spawner accepted")
	}
	got, err := spawnedControlChannelFactory(func(string) *exec.Cmd { return nil })("s")
	if err == nil {
		t.Error("spawner returning nil command accepted")
	}
	if got != nil {
		t.Error("failed factory returned a channel")
	}
}

// TestInBandSendLiteralIsAlwaysHex. In-band there is no argv: the whole line
// goes through tmux's command parser, where semicolons, quotes, backslashes
// and newlines are all live. Hex-encoding every literal avoids re-deriving
// which characters are safe on a control line.
func TestInBandSendLiteralIsAlwaysHex(t *testing.T) {
	for _, text := range []string{
		"plain", "with;semicolon", `with"quote`, "with'quote", `with\backslash`,
		"with $var and `cmd`", "with\ttab",
	} {
		command := InBandSendLiteral("%3", text)
		if !strings.Contains(command, "-H") {
			t.Errorf("%q was not hex-encoded: %s", text, command)
		}
		hex := strings.TrimSpace(strings.SplitN(command, "-H", 2)[1])
		if got := len(strings.Fields(hex)); got != len(text) {
			t.Errorf("%q encoded to %d hex bytes, want %d: %s", text, got, len(text), command)
		}
		// A control command must never contain a newline: the transport
		// rejects multiline commands, and a smuggled newline would be a
		// second command with a callback nobody registered.
		if strings.ContainsAny(command, "\r\n") {
			t.Errorf("%q produced a multiline command: %q", text, command)
		}
	}
}

func TestInBandSendLiteralEncodesMultibyteByByte(t *testing.T) {
	// "✳" is three bytes. send-keys -H takes bytes, not runes.
	command := InBandSendLiteral("%3", "✳")
	hex := strings.Fields(strings.TrimSpace(strings.SplitN(command, "-H", 2)[1]))
	if len(hex) != 3 {
		t.Fatalf("multibyte rune encoded as %d bytes: %v", len(hex), hex)
	}
	if hex[0] != "e2" || hex[1] != "9c" || hex[2] != "b3" {
		t.Errorf("wrong UTF-8 bytes: %v", hex)
	}
}

func TestInBandSendKeysSplitsLiteralFromNamed(t *testing.T) {
	commands := InBandSendKeys("%3",
		KeySpec{Value: "hello", Literal: true},
		KeySpec{Value: "Enter"},
	)
	if len(commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(commands))
	}
	if !strings.Contains(commands[0], "-H") {
		t.Errorf("literal key was not hex-encoded: %s", commands[0])
	}
	if strings.Contains(commands[1], "-H") {
		t.Errorf("named key Enter was hex-encoded, which sends the letters: %s", commands[1])
	}
	if !strings.HasSuffix(commands[1], "Enter") {
		t.Errorf("named key lost its name: %s", commands[1])
	}
	for _, command := range commands {
		if strings.ContainsAny(command, "\r\n") {
			t.Errorf("multiline command: %q", command)
		}
	}
}

// TestInBandSendKeysPreservesOrder is the FIFO property the local send queue
// exists to guarantee (td-8fcd2e). The commands are written to one pipe in one
// write, so their order here is the order tmux executes them in.
func TestInBandSendKeysPreservesOrder(t *testing.T) {
	keys := []KeySpec{
		{Value: "a", Literal: true}, {Value: "b", Literal: true}, {Value: "c", Literal: true},
	}
	commands := InBandSendKeys("%1", keys...)
	for i, want := range []string{"61", "62", "63"} {
		if !strings.HasSuffix(commands[i], want) {
			t.Errorf("command %d = %q, want it to end in %s", i, commands[i], want)
		}
	}
}

func TestControlQuoteHandlesEveryQuotingShape(t *testing.T) {
	cases := map[string]string{
		"%3":          "%3", // plain word, left alone
		"proj-claude": "proj-claude",
		"with space":  "'with space'",
		"semi;colon":  "'semi;colon'",
		"it's":        `"it's"`, // cannot single-quote; tmux has no escape inside ''
	}
	for input, want := range cases {
		if got := controlQuote(input); got != want {
			t.Errorf("controlQuote(%q) = %q, want %q", input, got, want)
		}
	}
	if got := controlQuote(""); got != "''" {
		t.Errorf("empty string quoted as %q", got)
	}
}

// TestControlQuoteNeutralisesExpansionInDoubleQuotes. tmux expands $, ` and
// escapes inside double quotes, so a value that had to be double-quoted (it
// contained a single quote) must have those neutralised or a pane name could
// execute something.
func TestControlQuoteNeutralisesExpansionInDoubleQuotes(t *testing.T) {
	got := controlQuote("it's $HOME `id`")
	if !strings.HasPrefix(got, `"`) {
		t.Fatalf("expected double quoting, got %q", got)
	}
	if strings.Contains(got, "$HOME") && !strings.Contains(got, `\$HOME`) {
		t.Errorf("$ not escaped: %q", got)
	}
	if strings.Contains(got, "`id`") && !strings.Contains(got, "\\`id\\`") {
		t.Errorf("backtick not escaped: %q", got)
	}
}
