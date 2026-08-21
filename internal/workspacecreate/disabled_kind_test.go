package workspacecreate

import (
	"strings"
	"testing"
)

// A disabled kind is disabled everywhere the form can be asked about it: the
// row's reason, Validate, and the placement buttons that create on one click.
func TestTerminalSplitDisabledGatesEveryCreatePath(t *testing.T) {
	const reason = "Two terminals are already on screen — close one first"
	for _, tc := range []struct {
		name     string
		disabled string
		kind     Kind
		want     string
	}{
		{name: "enabled", disabled: "", kind: KindTerminalSplit, want: ""},
		{name: "disabled", disabled: reason, kind: KindTerminalSplit, want: reason},
		{name: "other kinds are unaffected", disabled: reason, kind: KindShell, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := Open(OpenOpts{
				Kind:                  tc.kind,
				AllowTerminalSplit:    true,
				TerminalSplitDisabled: tc.disabled,
				TerminalName:          "term · repo",
			})
			if got := f.KindDisabledReason(); got != tc.want {
				t.Fatalf("KindDisabledReason = %q, want %q", got, tc.want)
			}
			if tc.want != "" && f.Validate() != tc.want {
				t.Fatalf("Validate = %q, want the same refusal %q", f.Validate(), tc.want)
			}
			if got := f.ApplyPlacementAction(ActionPlaceRight); got != (tc.want == "") {
				t.Fatalf("ApplyPlacementAction = %v, want %v", got, tc.want == "")
			}
		})
	}
}

// The reason is said once — one line, no explainer — and the kind row it
// belongs to is drawn muted rather than as a selectable choice.
func TestDisabledTerminalSplitRendersOneReasonLine(t *testing.T) {
	const reason = "Two terminals are already on screen — close one first"
	f := Open(OpenOpts{
		Kind:                  KindTerminalSplit,
		AllowTerminalSplit:    true,
		TerminalSplitDisabled: reason,
	})
	rendered := f.Build(60).Render(120, 40, nil)
	if got := strings.Count(rendered, "close one first"); got != 1 {
		t.Fatalf("reason appears %d times, want once:\n%s", got, rendered)
	}
}
