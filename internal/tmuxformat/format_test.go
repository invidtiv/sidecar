package tmuxformat

import (
	"reflect"
	"testing"
)

func TestFieldsQuotesEveryValue(t *testing.T) {
	if got, want := Fields("pane_id", "pane_title"), "#{q:pane_id}|#{q:pane_title}"; got != want {
		t.Fatalf("Fields() = %q, want %q", got, want)
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
