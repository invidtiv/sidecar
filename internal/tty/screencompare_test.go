package tty

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// forceScreenCompare turns shadow comparison on (or off) for the duration of a
// test and restores the previous state afterwards.
func forceScreenCompare(t *testing.T, on bool) {
	t.Helper()
	previous := screenCompareForced.Load()
	value := on
	screenCompareForced.Store(&value)
	ResetScreenCompare()
	t.Cleanup(func() {
		screenCompareForced.Store(previous)
		ResetScreenCompare()
	})
}

// The default path must be byte-identical to the pre-slice-2 behavior. The
// command text is the part a user can actually feel: a different display-message
// would change the parsed snapshot, and a different capture-pane would change
// the delivered screen.
func TestCaptureCommandsUnchangedWhenCompareOff(t *testing.T) {
	forceScreenCompare(t, false)
	metadata, capture, err := buildControlCaptureCommands("%3", 600)
	if err != nil {
		t.Fatal(err)
	}
	wantMeta := "display-message -p -t %3 '#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height}," +
		"#{pane_width},#{history_size},#{mouse_any_flag},#{pane_current_command},#{pane_title}'"
	if metadata != wantMeta {
		t.Errorf("metadata command changed:\n got %q\nwant %q", metadata, wantMeta)
	}
	if want := "capture-pane -p -e -S -600 -t %3"; capture != want {
		t.Errorf("capture command = %q, want %q", capture, want)
	}
	if wantsModelFeed(ControlRequest{}) {
		t.Error("a subscription with no OnModelFrame must not build a model with compare off")
	}
}

func TestCompareOnAddsOnlyMetadataFieldsAndNoExtraCommand(t *testing.T) {
	forceScreenCompare(t, true)
	metadata, capture, err := buildControlCaptureCommands("%3", 600)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, "#{alternate_on}") || !strings.Contains(metadata, "#{client_discarded}") {
		t.Errorf("compare metadata is missing the fields the comparison needs: %q", metadata)
	}
	// One display-message and one capture-pane, exactly as with compare off: the
	// diagnostic must not inflate the per-burst command count it measures.
	if strings.Count(metadata, "display-message") != 1 || strings.Contains(metadata, ";") {
		t.Errorf("compare metadata is not a single command: %q", metadata)
	}
	if want := "capture-pane -p -e -S -600 -t %3"; capture != want {
		t.Errorf("capture command = %q, want %q", capture, want)
	}
	if !wantsModelFeed(ControlRequest{}) {
		t.Error("shadow mode must feed a model even for a consumer that never asked for frames")
	}
}

// Both metadata layouts must produce the same ControlSnapshot for the same
// underlying pane, or shadow mode would change the delivered frame.
func TestBothMetadataLayoutsProduceTheSameSnapshot(t *testing.T) {
	body := []string{"row one", "row two"}
	standard := append([]string{"7,3,1,24,80,120,1,zsh,my,title"}, body...)
	extended := append([]string{"7,3,1,24,80,120,1,0,1,64,zsh,my,title"}, body...)

	got, _, err := parseControlSnapshotLayout("sess", "%1", 600, standard, false)
	if err != nil {
		t.Fatal(err)
	}
	gotExt, extras, err := parseControlSnapshotLayout("sess", "%1", 600, extended, true)
	if err != nil {
		t.Fatal(err)
	}
	if got != gotExt {
		t.Errorf("shadow mode changed the delivered snapshot:\n off %+v\n  on %+v", got, gotExt)
	}
	if got.PaneTitle != "my,title" {
		t.Errorf("pane title lost its comma: %q", got.PaneTitle)
	}
	if !extras.Valid || extras.AltScreen || !extras.MouseSGR || extras.Discarded != 64 {
		t.Errorf("extras = %+v", extras)
	}
}

// buildModelFrame runs bytes through a real model and returns its frame.
func buildModelFrame(t *testing.T, width, height int, payload string) screenmodel.Frame {
	t.Helper()
	model := screenmodel.New(width, height)
	t.Cleanup(model.Close)
	if err := model.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	frame, err := model.Frame()
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestCompareIsCleanWhenTheTwoSidesAgree(t *testing.T) {
	frame := buildModelFrame(t, 20, 4, "\x1b[31mred\x1b[0m plain\r\nsecond line")
	res := compareCaptureWithModel(screenCompareInput{
		CaptureOutput: frame.Output, Width: 20, Height: 4,
		CursorRow: frame.CursorRow, CursorCol: frame.CursorCol, CursorVisible: frame.CursorVisible,
		HistorySize: frame.HistorySize, AltScreen: frame.AltScreen,
		MouseAny: frame.Mouse.Any(), MouseSGR: frame.Mouse.SGR,
		CursorTrustworthy: true,
	}, frame)
	if len(res.Mismatches) != 0 {
		t.Fatalf("expected a clean comparison, got %s",
			screenmodel.FormatMismatches(res.Mismatches, 10))
	}
}

func TestCompareReportsCellCursorModeAndGeometryDifferences(t *testing.T) {
	frame := buildModelFrame(t, 10, 2, "abc")
	res := compareCaptureWithModel(screenCompareInput{
		CaptureOutput: "abd\n", Width: 10, Height: 2,
		CursorRow: 1, CursorCol: 5, CursorVisible: false,
		HistorySize: 0, AltScreen: true, MouseAny: true, MouseSGR: true,
		CursorTrustworthy: true,
	}, frame)
	want := map[string]bool{
		"cell/grapheme": false, "cursor/position": false, "cursor/visible": false,
		"mode/alt_screen": false, "mode/mouse_any": false, "mode/mouse_sgr": false,
	}
	for _, m := range res.Mismatches {
		if _, ok := want[m.Signature()]; ok {
			want[m.Signature()] = true
		}
	}
	for signature, seen := range want {
		if !seen {
			t.Errorf("comparison did not report %s", signature)
		}
	}
	if res.Unexplained == 0 {
		t.Error("a real difference must be counted as unexplained, not attributed away")
	}
}

// A cursor difference measured while the capture path's own metadata could be
// stale is not evidence about the model, and must not be scored.
func TestCursorIsNotScoredDuringACaptureMetadataRace(t *testing.T) {
	frame := buildModelFrame(t, 10, 2, "abc")
	res := compareCaptureWithModel(screenCompareInput{
		CaptureOutput: frame.Output, Width: 10, Height: 2,
		CursorRow: 1, CursorCol: 9, CursorVisible: true,
		CursorTrustworthy: false,
	}, frame)
	for _, m := range res.Mismatches {
		if m.Kind == "cursor" {
			t.Fatalf("cursor scored during a metadata race: %s", m)
		}
	}
}

func TestClassifierAttributesOnlyTheDocumentedGapShapes(t *testing.T) {
	link := func(url, params string) screenmodel.Cell {
		return screenmodel.Cell{Grapheme: "x", Width: 1, LinkURL: url, LinkParams: params}
	}
	cases := []struct {
		name      string
		field     string
		want, got screenmodel.Cell
		wantClass string
	}{
		{
			name: "GAP-3 swapped OSC 8 fields", field: "link_url",
			want: link("https://example.com/b", "id=xyz"), got: link("id=xyz", "https://example.com/b"),
			wantClass: gapClassOSC8Swap,
		},
		{
			name: "GAP-4 semicolon in the URI drops the link", field: "link_url",
			want: link("https://example.com/f?a=1;b=2", ""), got: link("", ""),
			wantClass: gapClassOSC8Semi,
		},
		{
			name: "a genuinely wrong URL is not a known gap", field: "link_url",
			want: link("https://example.com/a", ""), got: link("https://evil.example/", ""),
			wantClass: gapClassUnexplained,
		},
		{
			name: "GAP-2 SGR 21", field: "underline",
			want:      screenmodel.Cell{Grapheme: "x", Width: 1, Underline: screenmodel.UnderlineDouble},
			got:       screenmodel.Cell{Grapheme: "x", Width: 1},
			wantClass: gapClassSGR21,
		},
		{
			name: "an underline style swap is not a known gap", field: "underline",
			want:      screenmodel.Cell{Grapheme: "x", Width: 1, Underline: screenmodel.UnderlineCurly},
			got:       screenmodel.Cell{Grapheme: "x", Width: 1, Underline: screenmodel.UnderlineSingle},
			wantClass: gapClassUnexplained,
		},
		{
			name: "GAP-6/9 cluster split", field: "grapheme",
			want:      screenmodel.Cell{Grapheme: "❤️", Width: 2},
			got:       screenmodel.Cell{Grapheme: "❤", Width: 1},
			wantClass: gapClassCluster,
		},
		{
			name: "an unrelated grapheme difference is not a cluster split", field: "grapheme",
			want:      screenmodel.Cell{Grapheme: "a", Width: 1},
			got:       screenmodel.Cell{Grapheme: "b", Width: 1},
			wantClass: gapClassUnexplained,
		},
		{
			name: "a colour difference is never attributed", field: "fg",
			want:      screenmodel.Cell{Grapheme: "x", Width: 1, Fg: screenmodel.IndexedColor(1)},
			got:       screenmodel.Cell{Grapheme: "x", Width: 1},
			wantClass: gapClassUnexplained,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class := classifyCellMismatch(screenmodel.Mismatch{Kind: "cell", Field: tc.field}, tc.want, tc.got)
			if class != tc.wantClass {
				t.Errorf("class = %q, want %q", class, tc.wantClass)
			}
		})
	}
}

// A cursor difference on a frame that also shows a cluster split is GAP-9
// collateral, because the two wrongly committed cells advance the cursor
// differently from the one cell they should have formed.
func TestCursorMismatchWithAClusterSplitIsAttributed(t *testing.T) {
	frame := buildModelFrame(t, 10, 1, "❤")
	res := compareCaptureWithModel(screenCompareInput{
		CaptureOutput: "❤️", Width: 10, Height: 1,
		CursorRow: 0, CursorCol: 2, CursorVisible: true, CursorTrustworthy: true,
	}, frame)
	sawCursor := false
	for i, m := range res.Mismatches {
		if m.Kind == "cursor" && m.Field == "position" {
			sawCursor = true
			if res.Classes[i] != gapClassClusterCur {
				t.Errorf("cursor class = %q, want %q", res.Classes[i], gapClassClusterCur)
			}
		}
	}
	if !sawCursor {
		t.Skip("this emulator build placed the cursor identically; the collateral path is untestable here")
	}
}

// Privacy is a hard requirement: the report may be shared. Nothing that came
// off the terminal may appear in it.
func TestReportAndJSONNeverCarryTerminalText(t *testing.T) {
	forceScreenCompare(t, true)
	const secret = "hunter2-SUPERSECRET"
	const link = "https://internal.example/secret-token"
	frame := buildModelFrame(t, 40, 2, "\x1b]8;;"+link+"\x1b\\"+secret+"\x1b]8;;\x1b\\")
	res := compareCaptureWithModel(screenCompareInput{
		CaptureOutput: "totally different content here\n", Width: 40, Height: 2,
		CursorRow: 1, CursorCol: 1, CursorTrustworthy: true,
	}, frame)
	if len(res.Mismatches) == 0 {
		t.Fatal("expected mismatches to record")
	}
	screenCompareStats.recordComparison(res, true, false, 0)

	for name, text := range map[string]string{
		"JSON":   string(screenCompareStats.JSON()),
		"report": screenCompareStats.Report(),
	} {
		for _, forbidden := range []string{secret, link, "totally different", "internal.example"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s leaked terminal content %q", name, forbidden)
			}
		}
	}
}

func TestHistoryIsComparedAgainstTheCaptureWindow(t *testing.T) {
	// Six rows of output on a 2-row screen: four scroll off.
	frame := buildModelFrame(t, 12, 2, "l1\r\nl2\r\nl3\r\nl4\r\nl5\r\nl6")
	if !frame.HasHistory {
		t.Fatal("expected the model to have scrolled lines off")
	}
	res := compareCaptureWithModel(screenCompareInput{
		CaptureOutput: frame.Output, Width: 12, Height: 2,
		CursorRow: frame.CursorRow, CursorCol: frame.CursorCol, CursorVisible: frame.CursorVisible,
		HistorySize: frame.HistorySize, CursorTrustworthy: true,
	}, frame)
	if res.HistoryRows == 0 {
		t.Fatal("history was not compared at all")
	}
	if len(res.Mismatches) != 0 {
		t.Fatalf("history comparison should be clean: %s",
			screenmodel.FormatMismatches(res.Mismatches, 10))
	}

	// A corrupted history row must be reported under the history kind, not
	// silently folded into the visible grid.
	corrupt := strings.Replace(frame.Output, "l1", "XX", 1)
	res = compareCaptureWithModel(screenCompareInput{
		CaptureOutput: corrupt, Width: 12, Height: 2,
		CursorRow: frame.CursorRow, CursorCol: frame.CursorCol, CursorVisible: frame.CursorVisible,
		HistorySize: frame.HistorySize, CursorTrustworthy: true,
	}, frame)
	found := false
	for _, m := range res.Mismatches {
		if m.Kind == "history" {
			found = true
		}
	}
	if !found {
		t.Errorf("a corrupted history row was not reported: %s",
			screenmodel.FormatMismatches(res.Mismatches, 10))
	}
}
