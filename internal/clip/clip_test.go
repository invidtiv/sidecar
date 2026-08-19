package clip

import (
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// noticeMsg stands in for whatever message a host wraps a copy result in.
type noticeMsg struct{ result Result }

// stubNative replaces the system clipboard write for the duration of a test,
// and reports what it was asked to write.
func stubNative(t *testing.T, err error) *string {
	t.Helper()
	previous := writeNative
	var written string
	writeNative = func(text string) error {
		written = text
		return err
	}
	t.Cleanup(func() { writeNative = previous })
	return &written
}

// drain runs a command tree to completion and returns the messages it produced.
// Bubble Tea keeps the members of a batch or sequence in an unexported message
// whose underlying type is []tea.Cmd, so the walk is by shape.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	value := reflect.ValueOf(msg)
	if value.Kind() == reflect.Slice && value.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		var msgs []tea.Msg
		for i := range value.Len() {
			inner, _ := value.Index(i).Interface().(tea.Cmd)
			msgs = append(msgs, drain(inner)...)
		}
		return msgs
	}
	return []tea.Msg{msg}
}

// osc52Wrote reports whether the drained messages include the OSC 52 write of
// text. tea.SetClipboard's message type is unexported; its payload is the text.
func osc52Wrote(msgs []tea.Msg, text string) bool {
	for _, msg := range msgs {
		value := reflect.ValueOf(msg)
		if value.Kind() == reflect.String && value.String() == text {
			return true
		}
	}
	return false
}

func notice(t *testing.T, msgs []tea.Msg) noticeMsg {
	t.Helper()
	for _, msg := range msgs {
		if n, ok := msg.(noticeMsg); ok {
			return n
		}
	}
	t.Fatalf("messages = %#v, want the host's notice", msgs)
	return noticeMsg{}
}

func TestCopyWritesBothClipboards(t *testing.T) {
	written := stubNative(t, nil)

	msgs := drain(Copy("hello", func(r Result) tea.Msg { return noticeMsg{result: r} }))

	if *written != "hello" {
		t.Errorf("native clipboard = %q, want %q", *written, "hello")
	}
	if !osc52Wrote(msgs, "hello") {
		t.Errorf("messages = %#v, want an OSC 52 write of the text", msgs)
	}
	if err := notice(t, msgs).result.NativeErr; err != nil {
		t.Errorf("native error = %v, want none", err)
	}
}

func TestCopyReportsNativeFailureAndStillWritesOSC52(t *testing.T) {
	boom := errors.New("no clipboard utilities available")
	stubNative(t, boom)

	msgs := drain(Copy("hello", func(r Result) tea.Msg { return noticeMsg{result: r} }))

	if !osc52Wrote(msgs, "hello") {
		t.Errorf("messages = %#v, want the OSC 52 write a failed native write makes the only one", msgs)
	}
	if err := notice(t, msgs).result.NativeErr; !errors.Is(err, boom) {
		t.Errorf("native error = %v, want %v", err, boom)
	}
}

func TestCopyWithoutANoticeStillWrites(t *testing.T) {
	written := stubNative(t, nil)
	msgs := drain(Copy("quiet", nil))
	if *written != "quiet" {
		t.Errorf("native clipboard = %q, want %q", *written, "quiet")
	}
	if !osc52Wrote(msgs, "quiet") {
		t.Errorf("messages = %#v, want an OSC 52 write of the text", msgs)
	}
}

func TestMessageNamesTheClipboardAReachableCopyLanded(t *testing.T) {
	if got := (Result{}).Message("Yanked: td-1"); got != "Yanked: td-1" {
		t.Errorf("message = %q, want the caller's own wording", got)
	}
	// The native write is the half that can fail; the OSC 52 half still went
	// out, so the wording says where the text was sent rather than that the
	// copy failed.
	got := Result{NativeErr: errors.New("no clipboard utilities available")}.Message("Yanked: td-1")
	if got != "Yanked: td-1 — sent to the terminal clipboard" {
		t.Errorf("message = %q, want the terminal clipboard named", got)
	}
}

func TestCopyFromProducesTheTextWhenTheCommandRuns(t *testing.T) {
	written := stubNative(t, nil)
	produced := 0

	cmd := CopyFrom(
		func() (string, tea.Msg) {
			produced++
			return "read off disk", nil
		},
		func(r Result, text string) tea.Msg { return noticeMsg{result: r} },
	)
	if produced != 0 {
		t.Fatal("the text was produced before the command ran")
	}

	msgs := drain(cmd)
	if *written != "read off disk" {
		t.Errorf("native clipboard = %q, want the produced text", *written)
	}
	if !osc52Wrote(msgs, "read off disk") {
		t.Errorf("messages = %#v, want an OSC 52 write of the produced text", msgs)
	}
	notice(t, msgs)
}

func TestCopyFromShowsTheHostsOwnMessageWhenThereIsNothingToCopy(t *testing.T) {
	written := stubNative(t, nil)
	empty := noticeMsg{}

	msgs := drain(CopyFrom(
		func() (string, tea.Msg) { return "", empty },
		func(Result, string) tea.Msg { return nil },
	))

	if *written != "" {
		t.Errorf("native clipboard = %q, want an untouched clipboard", *written)
	}
	if len(msgs) != 1 || msgs[0] != tea.Msg(empty) {
		t.Errorf("messages = %#v, want only the host's own message", msgs)
	}
}
