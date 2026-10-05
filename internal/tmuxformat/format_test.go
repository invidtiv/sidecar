package tmuxformat

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFieldsQuotesEveryValue(t *testing.T) {
	if got, want := Fields("pane_id", "pane_title"), "#{q:pane_id}|#{q:pane_title}"; got != want {
		t.Fatalf("Fields() = %q, want %q", got, want)
	}
}

func TestRecordFieldsRoundTripPrivateTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "scfmt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "socket")
	run := func(args ...string) string {
		t.Helper()
		args = append([]string{"-u", "-S", socket, "-f", "/dev/null"}, args...)
		out, err := exec.Command("tmux", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("private tmux: %v: %s", err, out)
		}
		return strings.TrimSuffix(string(out), "\n")
	}
	run("new-session", "-d", "-s", "records", "sleep", "300")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	values := []string{"", "a|b", `$HOME \$HOME \\044`, "'\"; #{pid} `id`", "行 🦉", "tab\tline\nreturn\r"}
	var controls strings.Builder
	for b := 1; b < 32; b++ {
		controls.WriteByte(byte(b))
	}
	controls.WriteByte(127)
	values = append(values, controls.String())
	keys := make([]string, len(values))
	for i, value := range values {
		keys[i] = fmt.Sprintf("@record%d", i)
		run("set-option", "-t", "records", keys[i], value)
	}
	line := run("display-message", "-p", "-t", "records", RecordFields(keys...))
	if strings.IndexFunc(line, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		t.Fatalf("record contains a raw control byte: %q", line)
	}
	if got := Split(line); !reflect.DeepEqual(got, values) {
		t.Fatalf("record round trip = %#v, want %#v", got, values)
	}
}

func TestClientArgsForcesUTF8WithoutMutatingInput(t *testing.T) {
	input := []string{"list-panes", "-a"}
	got := ClientArgs(input...)
	want := []string{"-u", "list-panes", "-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClientArgs() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(input, []string{"list-panes", "-a"}) {
		t.Fatalf("ClientArgs mutated input: %#v", input)
	}
}

func TestSplitDecodesTmuxQuotedValues(t *testing.T) {
	want := []string{"%1", "999999", `a|b c\d`, "line\nnext", "\x1b"}
	got := Split(`\%1|999999|a\|b\ c\\d|line\nnext|\033`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Split() = %#v, want %#v", got, want)
	}
}
