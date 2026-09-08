package tty

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

const mobileSeedFixtureDir = "../../testdata/mobile-protocol/v0/experimental-terminal"

type mobileSeedFixture struct {
	Cases []struct {
		Name       string                      `json:"name"`
		Geometry   struct{ Columns, Rows int } `json:"geometry"`
		SeedInputs struct {
			MetadataLine          string   `json:"metadata_line"`
			SavedMainCaptureLines []string `json:"saved_main_capture_lines"`
			ActiveCaptureLines    []string `json:"active_capture_lines"`
		} `json:"seed_inputs"`
		Candidate struct {
			ResetAndSeedVT string `json:"reset_and_seed_vt_base64"`
		} `json:"candidate"`
		PreAttachVariants []struct {
			Name string `json:"name"`
			VT   string `json:"vt_base64"`
		} `json:"pre_attach_variants"`
		Continuation []struct {
			Sequence int    `json:"output_sequence"`
			VT       string `json:"vt_base64"`
		} `json:"continuation"`
	} `json:"cases"`
}

func readMobileSeedFixture[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(mobileSeedFixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var fixture T
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return fixture
}

func decodeMobileVT(t *testing.T, value string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode VT fixture: %v", err)
	}
	return data
}

type mobileFixtureManifest struct {
	Files []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func TestMobileSeedSpikeManifestPinsFixtureBytes(t *testing.T) {
	manifest := readMobileSeedFixture[mobileFixtureManifest](t, "manifest.json")
	if len(manifest.Files) == 0 {
		t.Fatal("fixture manifest is empty")
	}
	for _, file := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(mobileSeedFixtureDir, file.Path))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != file.SHA256 {
			t.Fatalf("%s sha256 = %s, want %s", file.Path, got, file.SHA256)
		}
	}
}

func mobileSeedCase(t *testing.T, name string) (mobileSeedFixture, int) {
	t.Helper()
	fixture := readMobileSeedFixture[mobileSeedFixture](t, "seed-cases.json")
	for i := range fixture.Cases {
		if fixture.Cases[i].Name == name {
			return fixture, i
		}
	}
	t.Fatalf("missing seed case %q", name)
	return fixture, 0
}

func TestMobileSeedSpikePreservesDeclaredCaptureState(t *testing.T) {
	fixture, index := mobileSeedCase(t, "preserved_main_alternate_cursor_mouse")
	c := fixture.Cases[index]
	seed, _, err := seedFromResponses(
		[]string{c.SeedInputs.MetadataLine},
		c.SeedInputs.SavedMainCaptureLines,
		c.SeedInputs.ActiveCaptureLines,
		DefaultScrollbackLines,
	)
	if err != nil {
		t.Fatal(err)
	}
	if seed.Width != 8 || seed.Height != 3 || seed.CursorCol != 2 || seed.CursorRow != 1 || seed.CursorVisible {
		t.Fatalf("active seed state = %+v", seed)
	}
	if !seed.AltScreen || !seed.Mouse.AnyEvent || !seed.Mouse.SGR || seed.MainCursorCol != 4 || seed.MainCursorRow != 2 {
		t.Fatalf("alternate/mouse seed state = %+v", seed)
	}

	model := screenmodel.New(seed.Width, seed.Height)
	defer model.Close()
	if err := model.Seed(seed); err != nil {
		t.Fatal(err)
	}
	frame, err := model.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(frame.Output, "AGENT\nworking") || frame.CursorCol != 2 || frame.CursorRow != 1 || frame.CursorVisible {
		t.Fatalf("seeded active frame = %+v", frame)
	}
	if !frame.AltScreen || !frame.Mouse.AnyEvent || !frame.Mouse.SGR {
		t.Fatalf("seeded modes = %+v", frame)
	}
	if err := model.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}
	mainFrame, err := model.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mainFrame.Output, "main>\ndraft") || mainFrame.CursorCol != 4 || mainFrame.CursorRow != 2 {
		t.Fatalf("restored main frame = %+v", mainFrame)
	}
}

func TestMobileSeedSpikeCurrentRenditionIsNotSerializable(t *testing.T) {
	fixture, index := mobileSeedCase(t, "indistinguishable_current_rendition")
	c := fixture.Cases[index]
	if len(c.PreAttachVariants) != 2 || len(c.Continuation) != 1 {
		t.Fatal("rendition fixture must contain two histories and one continuation")
	}

	reference := screenmodel.New(c.Geometry.Columns, c.Geometry.Rows)
	defer reference.Close()
	if err := reference.Write(decodeMobileVT(t, c.PreAttachVariants[0].VT)); err != nil {
		t.Fatal(err)
	}
	candidate := screenmodel.New(c.Geometry.Columns, c.Geometry.Rows)
	defer candidate.Close()
	seed, _, err := seedFromResponses([]string{c.SeedInputs.MetadataLine}, nil, c.SeedInputs.ActiveCaptureLines, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Seed(seed); err != nil {
		t.Fatal(err)
	}
	beforeReference, _ := reference.Frame()
	beforeCandidate, _ := candidate.Frame()
	if !beforeReference.SamePresentation(beforeCandidate) {
		t.Fatalf("histories must be indistinguishable at attach: reference=%+v candidate=%+v", beforeReference, beforeCandidate)
	}

	continuation := decodeMobileVT(t, c.Continuation[0].VT)
	if err := reference.Write(continuation); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Write(continuation); err != nil {
		t.Fatal(err)
	}
	referenceAfter, _ := reference.DiagnosticFrame()
	candidateAfter, _ := candidate.DiagnosticFrame()
	if referenceAfter.Cells[0][0].Fg != screenmodel.IndexedColor(1) {
		t.Fatalf("reference continuation foreground = %q", referenceAfter.Cells[0][0].Fg)
	}
	if candidateAfter.Cells[0][0].Fg != screenmodel.ColorDefault {
		t.Fatalf("candidate continuation foreground = %q", candidateAfter.Cells[0][0].Fg)
	}
}

func TestMobileSeedSpikeAutowrapIsNotSerializable(t *testing.T) {
	fixture, index := mobileSeedCase(t, "indistinguishable_autowrap")
	c := fixture.Cases[index]
	reference := screenmodel.New(c.Geometry.Columns, c.Geometry.Rows)
	defer reference.Close()
	if err := reference.Write(decodeMobileVT(t, c.PreAttachVariants[0].VT)); err != nil {
		t.Fatal(err)
	}
	candidate := screenmodel.New(c.Geometry.Columns, c.Geometry.Rows)
	defer candidate.Close()
	seed, _, err := seedFromResponses([]string{c.SeedInputs.MetadataLine}, nil, c.SeedInputs.ActiveCaptureLines, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Seed(seed); err != nil {
		t.Fatal(err)
	}
	beforeReference, _ := reference.Frame()
	beforeCandidate, _ := candidate.Frame()
	if !beforeReference.SamePresentation(beforeCandidate) {
		t.Fatalf("histories must be indistinguishable at attach: reference=%+v candidate=%+v", beforeReference, beforeCandidate)
	}

	continuation := decodeMobileVT(t, c.Continuation[0].VT)
	if err := reference.Write(continuation); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Write(continuation); err != nil {
		t.Fatal(err)
	}
	referenceAfter, _ := reference.DiagnosticFrame()
	candidateAfter, _ := candidate.DiagnosticFrame()
	if referenceAfter.SamePresentation(candidateAfter.Frame) {
		t.Fatal("autowrap histories did not diverge after the same continuation")
	}
	if candidateAfter.Cells[1][0].Grapheme != "B" {
		t.Fatalf("candidate did not use reset-default autowrap: %+v", candidateAfter.Cells)
	}
}

func TestMobileSeedSpikeMissingInputModesResetToDefaults(t *testing.T) {
	model := screenmodel.New(4, 2)
	defer model.Close()
	if err := model.Write([]byte("\x1b[?2004h\x1b[?1h\x1b=")); err != nil {
		t.Fatal(err)
	}
	before, _ := model.Frame()
	if !before.BracketedPaste {
		t.Fatal("reference pre-attach bracketed paste mode was not set")
	}
	if err := model.Seed(screenmodel.Seed{Width: 4, Height: 2, CursorVisible: true}); err != nil {
		t.Fatal(err)
	}
	after, _ := model.Frame()
	if after.BracketedPaste {
		t.Fatal("current seed unexpectedly preserved bracketed paste")
	}
}

type mobileGapFixture struct {
	Messages []struct {
		Sequence int `json:"output_sequence"`
	} `json:"messages"`
}

func experimentalSequenceVerdict(previous, next int) string {
	if next == previous+1 {
		return "accept"
	}
	return "suspend-and-reseed"
}

func TestMobileSeedSpikeSequenceGapRequiresReset(t *testing.T) {
	fixture := readMobileSeedFixture[mobileGapFixture](t, "stream-gap.json")
	if got := experimentalSequenceVerdict(40, fixture.Messages[0].Sequence); got != "accept" {
		t.Fatalf("first output verdict = %q", got)
	}
	if got := experimentalSequenceVerdict(fixture.Messages[0].Sequence, fixture.Messages[1].Sequence); got != "suspend-and-reseed" {
		t.Fatalf("gap verdict = %q", got)
	}
}

type mobileNormalizedFixture struct {
	Geometry struct{ Columns, Rows int } `json:"geometry"`
	Frames   []struct {
		Kind            string `json:"kind"`
		Sequence        int    `json:"output_sequence"`
		ResetGeneration int    `json:"reset_generation"`
		RenderVT        string `json:"render_vt_base64"`
	} `json:"frames"`
	FidelityFrame struct {
		Geometry     struct{ Columns, Rows int } `json:"geometry"`
		CaptureLines []string                    `json:"capture_lines"`
		RenderVT     string                      `json:"render_vt_base64"`
	} `json:"fidelity_frame"`
}

func TestMobileSeedSpikeNormalizedFramesReplaceAfterGap(t *testing.T) {
	fixture := readMobileSeedFixture[mobileNormalizedFixture](t, "normalized-frames.json")
	if len(fixture.Frames) != 3 {
		t.Fatalf("normalized fixture has %d frames", len(fixture.Frames))
	}
	model := screenmodel.New(fixture.Geometry.Columns, fixture.Geometry.Rows)
	if err := model.Write(decodeMobileVT(t, fixture.Frames[0].RenderVT)); err != nil {
		model.Close()
		t.Fatal(err)
	}
	first, _ := model.Frame()
	if !first.AltScreen || first.CursorVisible || first.CursorStyle != screenmodel.CursorBar || !first.BracketedPaste ||
		first.CursorCol != 2 || first.CursorRow != 1 || !strings.Contains(first.Output, "AGENT\nworking") {
		model.Close()
		t.Fatalf("first normalized frame = %+v", first)
	}
	if experimentalSequenceVerdict(fixture.Frames[0].Sequence, fixture.Frames[1].Sequence) != "accept" {
		model.Close()
		t.Fatal("contiguous changed-row frame was rejected")
	}
	if err := model.Write(decodeMobileVT(t, fixture.Frames[1].RenderVT)); err != nil {
		model.Close()
		t.Fatal(err)
	}
	second, _ := model.Frame()
	if !strings.Contains(second.Output, "DONE") || !second.CursorVisible || second.CursorCol != 4 || second.CursorRow != 1 {
		model.Close()
		t.Fatalf("changed-row frame = %+v", second)
	}
	if experimentalSequenceVerdict(fixture.Frames[1].Sequence, fixture.Frames[2].Sequence) != "suspend-and-reseed" ||
		fixture.Frames[2].Kind != "full" || fixture.Frames[2].ResetGeneration == fixture.Frames[1].ResetGeneration {
		model.Close()
		t.Fatal("gap did not require a replacement full frame and new generation")
	}
	model.Close()

	// Replacement, rather than appending to the stale emulator, is the contract.
	model = screenmodel.New(fixture.Geometry.Columns, fixture.Geometry.Rows)
	defer model.Close()
	if err := model.Write(decodeMobileVT(t, fixture.Frames[2].RenderVT)); err != nil {
		t.Fatal(err)
	}
	afterGap, _ := model.Frame()
	if afterGap.AltScreen || afterGap.BracketedPaste || !afterGap.CursorVisible ||
		afterGap.CursorStyle != screenmodel.CursorBlock || afterGap.CursorCol != 4 || afterGap.CursorRow != 1 ||
		!strings.Contains(afterGap.Output, "main>\ndone") {
		t.Fatalf("replacement frame = %+v", afterGap)
	}
}

func TestMobileSeedSpikeNormalizedFramePreservesUnicodeAndColoredBlanks(t *testing.T) {
	fixture := readMobileSeedFixture[mobileNormalizedFixture](t, "normalized-frames.json")
	frame := fixture.FidelityFrame
	model := screenmodel.New(frame.Geometry.Columns, frame.Geometry.Rows)
	defer model.Close()
	if err := model.Write(decodeMobileVT(t, frame.RenderVT)); err != nil {
		t.Fatal(err)
	}
	got, err := model.DiagnosticFrame()
	if err != nil {
		t.Fatal(err)
	}
	if got.CursorCol != 2 || got.CursorRow != 1 || !got.CursorVisible {
		t.Fatalf("cursor = (%d,%d) visible=%v", got.CursorCol, got.CursorRow, got.CursorVisible)
	}
	// The mobile producer starts from tmux's authoritative capture. Keep that
	// oracle separate from the Go emulator consuming its own normalized VT:
	// x/vt currently drops this combining mark, while SwiftTerm is the candidate
	// consumer being proved independently in M0-B.
	oracle := screenmodel.DecodeCapture(strings.Join(frame.CaptureLines, "\n"), frame.Geometry.Columns, frame.Geometry.Rows)
	if cell := oracle[0][0]; cell.Grapheme != "界" || cell.Width != 2 || cell.Fg != screenmodel.IndexedColor(6) {
		t.Fatalf("wide cell = %s", cell.Describe())
	}
	if cell := oracle[0][1]; cell.Grapheme != "" || cell.Width != 0 {
		t.Fatalf("wide continuation = %s", cell.Describe())
	}
	if cell := oracle[0][3]; cell.Grapheme != "é" || cell.Width != 1 {
		t.Fatalf("combining cell = %s", cell.Describe())
	}
	for col := 5; col <= 7; col++ {
		if cell := oracle[0][col]; cell.Grapheme != " " || cell.Width != 1 || cell.Bg != screenmodel.IndexedColor(4) {
			t.Fatalf("trailing blank %d = %s", col, cell.Describe())
		}
		if cell := got.Cells[0][col]; cell.Bg != screenmodel.IndexedColor(4) {
			t.Fatalf("normalized consumer trailing blank %d = %s", col, cell.Describe())
		}
	}
}

type mobileLeaseFixture struct {
	IdentityCandidate struct {
		AttachmentA string `json:"attachment_a"`
		AttachmentB string `json:"attachment_b"`
	} `json:"identity_candidate"`
	Policy struct {
		StaleTicks               int `json:"stale_ticks"`
		StaleAfterMilliseconds   int `json:"stale_after_milliseconds"`
		RefreshTicks             int `json:"refresh_ticks"`
		RefreshAfterMilliseconds int `json:"refresh_after_milliseconds"`
		PreemptIdleMilliseconds  int `json:"preempt_idle_milliseconds"`
	} `json:"policy"`
	Cases []struct {
		Name                     string `json:"name"`
		SelfID                   string `json:"self_id"`
		Token                    string `json:"token"`
		Focused                  bool   `json:"focused"`
		UnchangedTicks           int    `json:"unchanged_ticks"`
		UnchangedForMilliseconds int    `json:"unchanged_for_milliseconds"`
		ExpectedReason           string `json:"expected_reason"`
		ExpectedResize           bool   `json:"expected_resize"`
		ExpectedWrite            bool   `json:"expected_write"`
	} `json:"cases"`
	FencedReleaseCases []struct {
		Name                     string `json:"name"`
		OpenedServerIncarnation  string `json:"opened_server_incarnation"`
		CurrentServerIncarnation string `json:"current_server_incarnation"`
		OpenedTargetGeneration   int    `json:"opened_target_generation"`
		CurrentTargetGeneration  int    `json:"current_target_generation"`
		ReleasingOwner           string `json:"releasing_owner"`
		CurrentToken             string `json:"current_token"`
		LeaseReadOK              bool   `json:"lease_read_ok"`
		ExpectedClear            bool   `json:"expected_clear"`
	} `json:"fenced_release_cases"`
}

func experimentalReleaseAllowed(readOK bool, openedServer, currentServer string, openedGeneration, currentGeneration int, releasingOwner, currentToken string) bool {
	return readOK && openedServer == currentServer && openedGeneration == currentGeneration && leaseOwner(currentToken) == releasingOwner
}

func TestMobileSeedSpikeGeometryLeaseAndStaleRelease(t *testing.T) {
	fixture := readMobileSeedFixture[mobileLeaseFixture](t, "geometry-lease.json")
	if fixture.IdentityCandidate.AttachmentA == fixture.IdentityCandidate.AttachmentB ||
		!ValidInstanceID(fixture.IdentityCandidate.AttachmentA) || !ValidInstanceID(fixture.IdentityCandidate.AttachmentB) {
		t.Fatal("candidate attachment identities must be distinct and accepted by the existing token parser")
	}
	policy := LeasePolicy{
		StaleTicks:   fixture.Policy.StaleTicks,
		StaleAfter:   time.Duration(fixture.Policy.StaleAfterMilliseconds) * time.Millisecond,
		RefreshTicks: fixture.Policy.RefreshTicks,
		RefreshAfter: time.Duration(fixture.Policy.RefreshAfterMilliseconds) * time.Millisecond,
		PreemptIdle:  time.Duration(fixture.Policy.PreemptIdleMilliseconds) * time.Millisecond,
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := DecideGeometryLease(LeaseObservation{
				SelfID: c.SelfID, Token: c.Token, Focused: c.Focused,
				UnchangedTicks: c.UnchangedTicks,
				UnchangedFor:   time.Duration(c.UnchangedForMilliseconds) * time.Millisecond,
			}, policy)
			if got.Reason != c.ExpectedReason || got.Resize != c.ExpectedResize || got.Write != c.ExpectedWrite {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
	for _, c := range fixture.FencedReleaseCases {
		t.Run(c.Name, func(t *testing.T) {
			got := experimentalReleaseAllowed(c.LeaseReadOK, c.OpenedServerIncarnation, c.CurrentServerIncarnation,
				c.OpenedTargetGeneration, c.CurrentTargetGeneration, c.ReleasingOwner, c.CurrentToken)
			if got != c.ExpectedClear {
				t.Fatalf("release clear = %v, want %v", got, c.ExpectedClear)
			}
		})
	}
}
