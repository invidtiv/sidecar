package notifydelivery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/notify"
)

type linuxRunnerCall struct {
	name string
	args []string
}

type linuxFakeRunner struct {
	mu                     sync.Mutex
	paths                  map[string]string
	runErrors              map[string]error
	lookups                []string
	calls                  []linuxRunnerCall
	blockRun               bool
	replacementUnsupported bool
}

func (r *linuxFakeRunner) LookPath(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups = append(r.lookups, name)
	if path := r.paths[name]; path != "" {
		return path, nil
	}
	return "", errors.New("not found")
}

func (r *linuxFakeRunner) Run(ctx context.Context, name string, args ...string) error {
	r.mu.Lock()
	r.calls = append(r.calls, linuxRunnerCall{name: name, args: append([]string(nil), args...)})
	err := r.runErrors[name]
	block := r.blockRun
	replacementUnsupported := r.replacementUnsupported
	r.mu.Unlock()
	if replacementUnsupported && linuxContainsPair(args, "--replace-id", "1") && len(args) > 0 && args[len(args)-1] == "--help" {
		return errors.New("Unknown option --replace-id")
	}
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func linuxEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestLinuxNativeRequiresDisplayAndRetriesMissingProvider(t *testing.T) {
	runner := &linuxFakeRunner{paths: map[string]string{}, runErrors: map[string]error{}}
	provider := newLinuxNative(runner, linuxEnv(nil), time.Second)
	capability := provider.Probe(context.Background())
	if capability.Available || capability.Provider != linuxNativeProvider || capability.Reason != linuxDisplayReason {
		t.Fatalf("no-display capability = %+v", capability)
	}
	if len(runner.lookups) != 0 {
		t.Fatalf("no-display probe touched PATH: %v", runner.lookups)
	}

	provider = newLinuxNative(runner, linuxEnv(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}), time.Second)
	capability = provider.Probe(context.Background())
	if capability.Available || !strings.Contains(capability.Reason, "notify-send is unavailable") {
		t.Fatalf("missing provider capability = %+v", capability)
	}
	runner.mu.Lock()
	runner.paths[linuxNativeProvider] = "/usr/bin/notify-send"
	runner.mu.Unlock()
	capability = provider.Probe(context.Background())
	if !capability.Available || capability.Provider != linuxNativeProvider {
		t.Fatalf("rechecked capability = %+v", capability)
	}
}

func TestLinuxAdapterConstructionDoesNotProbeOrStartWork(t *testing.T) {
	runner := &linuxFakeRunner{paths: map[string]string{linuxNativeProvider: "/bin/notify-send", "paplay": "/bin/paplay"}, runErrors: map[string]error{}}
	_ = newLinuxNative(runner, linuxEnv(map[string]string{"DISPLAY": ":0"}), time.Second)
	_ = newLinuxSound(runner, linuxTestCache{path: "/tmp/cue.wav"}, time.Second)
	if len(runner.lookups) != 0 || len(runner.calls) != 0 {
		t.Fatalf("adapter construction performed I/O: lookups=%v calls=%v", runner.lookups, runner.calls)
	}
}

func TestLinuxNotifySendUsesSafeUrgencyExpiryAndReplacementArgv(t *testing.T) {
	runner := &linuxFakeRunner{paths: map[string]string{linuxNativeProvider: "/usr/bin/notify-send"}, runErrors: map[string]error{}}
	provider := newLinuxNative(runner, linuxEnv(map[string]string{"DISPLAY": ":0"}), time.Second)
	message := Message{
		Title: "--action=evil=$(touch /tmp/nope)", Body: "body; rm -rf /", Severity: notify.SeverityError,
		Sticky: true, Group: "sidecar-workspace-one",
	}
	receipt, err := provider.Deliver(context.Background(), message)
	if err != nil || !receipt.Delivered || receipt.Provider != linuxNativeProvider {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v", runner.calls)
	}
	call := runner.calls[1]
	if call.name != "/usr/bin/notify-send" || !linuxContainsPair(call.args, "--app-name", "Sidecar") || !linuxContainsPair(call.args, "--urgency", "critical") || !linuxContainsPair(call.args, "--expire-time", "0") {
		t.Fatalf("notify-send argv = %#v", call)
	}
	replacement := pairValue(call.args, "--replace-id")
	later := message
	later.Title = "finished later"
	if replacement == "" || replacement != pairValue(linuxNotifyArgs(later, true), "--replace-id") {
		t.Fatalf("replacement ID is not stable: %#v", call.args)
	}
	if got := call.args[len(call.args)-3:]; got[0] != "--" || got[1] != message.Title || got[2] != message.Body {
		t.Fatalf("notification text was not isolated argv: %#v", call.args)
	}
	if err := provider.Remove(context.Background(), message.Group); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("notify-send removal = %v, want unsupported", err)
	}
}

func TestLinuxReplacementIDFitsNotifySendSignedInteger(t *testing.T) {
	id := linuxReplacementID("sidecar-workspace-one")
	if id == 0 || id > 0x7fffffff {
		t.Fatalf("replacement ID %d is outside notify-send's signed integer range", id)
	}
	if id != linuxReplacementID("sidecar-workspace-one") {
		t.Fatal("replacement ID is not stable")
	}
}

func TestLinuxNativeDegradesWithoutReplacementSupport(t *testing.T) {
	runner := &linuxFakeRunner{
		paths: map[string]string{linuxNativeProvider: "/usr/bin/notify-send"}, runErrors: map[string]error{}, replacementUnsupported: true,
	}
	provider := newLinuxNative(runner, linuxEnv(map[string]string{"DISPLAY": ":0"}), time.Second)
	capability := provider.Probe(context.Background())
	if !capability.Available || !strings.Contains(capability.Reason, "without grouping") {
		t.Fatalf("legacy capability = %+v", capability)
	}
	runner.calls = nil
	receipt, err := provider.Deliver(context.Background(), Message{Title: "legacy", Group: "workspace"})
	if err != nil || !receipt.Delivered || len(runner.calls) != 2 {
		t.Fatalf("receipt=%+v err=%v calls=%+v", receipt, err, runner.calls)
	}
	if pairValue(runner.calls[1].args, "--replace-id") != "" {
		t.Fatalf("legacy delivery used unsupported replacement: %#v", runner.calls[1].args)
	}
}

func TestLinuxRecheckObservesRemovedProviders(t *testing.T) {
	runner := &linuxFakeRunner{paths: map[string]string{linuxNativeProvider: "/bin/notify-send", "paplay": "/bin/paplay"}, runErrors: map[string]error{}}
	native := newLinuxNative(runner, linuxEnv(map[string]string{"DISPLAY": ":0"}), time.Second)
	sound := newLinuxSound(runner, linuxTestCache{path: "/tmp/cue.wav"}, time.Second)
	if !native.Probe(context.Background()).Available || !sound.Probe(context.Background()).Available {
		t.Fatal("fixture providers did not begin available")
	}
	runner.mu.Lock()
	delete(runner.paths, linuxNativeProvider)
	delete(runner.paths, "paplay")
	runner.mu.Unlock()
	if capability := native.Probe(context.Background()); capability.Available {
		t.Fatalf("native recheck retained removed provider: %+v", capability)
	}
	if capability := sound.Probe(context.Background()); capability.Available {
		t.Fatalf("sound recheck retained removed provider: %+v", capability)
	}
}

func TestLinuxNativeReportsTimeoutAndProviderFailure(t *testing.T) {
	for name, test := range map[string]struct {
		runner *linuxFakeRunner
		want   string
	}{
		"timeout": {
			runner: &linuxFakeRunner{paths: map[string]string{linuxNativeProvider: "/bin/notify-send"}, runErrors: map[string]error{}, blockRun: true},
			want:   "timed out",
		},
		"failure": {
			runner: &linuxFakeRunner{paths: map[string]string{linuxNativeProvider: "/bin/notify-send"}, runErrors: map[string]error{"/bin/notify-send": errors.New("daemon refused")}},
			want:   "daemon refused",
		},
	} {
		t.Run(name, func(t *testing.T) {
			provider := newLinuxNative(test.runner, linuxEnv(map[string]string{"DISPLAY": ":0"}), 5*time.Millisecond)
			receipt, err := provider.Deliver(context.Background(), Message{Title: "test"})
			if err == nil || receipt.Delivered || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

type linuxTestCache struct {
	path string
	err  error
}

func (c linuxTestCache) Materialize(Cue) (string, error) { return c.path, c.err }

func TestLinuxSoundPlayerOrderAndFormatSpecificArgv(t *testing.T) {
	for name, test := range map[string]struct {
		extension    string
		paths        map[string]string
		wantProvider string
		wantArgs     []string
	}{
		"wav prefers paplay": {
			extension: "wav", paths: map[string]string{"paplay": "/bin/paplay", "pw-play": "/bin/pw-play", "ffplay": "/bin/ffplay"},
			wantProvider: "paplay", wantArgs: []string{"/tmp/cue.wav"},
		},
		"wav falls through to aplay": {
			extension: "wav", paths: map[string]string{"aplay": "/bin/aplay", "ffplay": "/bin/ffplay"},
			wantProvider: "aplay", wantArgs: []string{"--quiet", "/tmp/cue.wav"},
		},
		"mp3 skips pcm players": {
			extension: "mp3", paths: map[string]string{"paplay": "/bin/paplay", "aplay": "/bin/aplay", "ffplay": "/bin/ffplay", "mpv": "/bin/mpv"},
			wantProvider: "ffplay", wantArgs: []string{"-nodisp", "-autoexit", "-loglevel", "quiet", "/tmp/cue.mp3"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &linuxFakeRunner{paths: test.paths, runErrors: map[string]error{}}
			player := newLinuxSound(runner, linuxTestCache{path: "/tmp/cue." + test.extension}, time.Second)
			receipt, err := player.Play(context.Background(), CueDone)
			if err != nil || !receipt.Delivered || receipt.Provider != test.wantProvider {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			if len(runner.calls) != 1 || runner.calls[0].name != test.paths[test.wantProvider] || !equalStrings(runner.calls[0].args, test.wantArgs) {
				t.Fatalf("calls=%+v want provider=%s args=%v", runner.calls, test.wantProvider, test.wantArgs)
			}
		})
	}
}

func TestLinuxSoundMissingPlayerRetriesAndNamesAttemptedOrder(t *testing.T) {
	runner := &linuxFakeRunner{paths: map[string]string{}, runErrors: map[string]error{}}
	player := newLinuxSound(runner, linuxTestCache{path: "/tmp/cue.wav"}, time.Second)
	capability := player.Probe(context.Background())
	if capability.Available || capability.Reason != "no Linux sound player supports WAV (tried paplay, pw-play, aplay, ffplay, mpv)" {
		t.Fatalf("missing capability = %+v", capability)
	}
	runner.mu.Lock()
	runner.paths["pw-play"] = "/bin/pw-play"
	runner.mu.Unlock()
	capability = player.Probe(context.Background())
	if !capability.Available || capability.Provider != "pw-play" {
		t.Fatalf("rechecked capability = %+v", capability)
	}
}

func TestLinuxSoundStatusUsesResolvedFormatAndFallsBackFromUnsupportedCustom(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "actual.wav")
	if err := os.WriteFile(wav, []byte("fake wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "looks-like.mp3")
	if err := os.Symlink(wav, symlink); err != nil {
		t.Fatal(err)
	}
	fallback := linuxTestCache{path: "/cache/done.wav"}
	cache := NewConfiguredAssetCache(fallback, func() (SoundPaths, error) {
		return SoundPaths{Done: symlink, ConfigPath: filepath.Join(dir, "config.json")}, nil
	})
	runner := &linuxFakeRunner{paths: map[string]string{"paplay": "/bin/paplay"}, runErrors: map[string]error{}}
	player := newLinuxSound(runner, cache, time.Second)
	capability := player.Probe(context.Background())
	if !capability.Available || capability.Provider != "paplay" || capability.Reason != "" {
		t.Fatalf("resolved symlink capability = %+v", capability)
	}
	receipt, err := player.Play(context.Background(), CueDone)
	resolvedWAV, _ := filepath.EvalSymlinks(wav)
	if err != nil || !receipt.Delivered || runner.calls[0].args[0] != resolvedWAV {
		t.Fatalf("receipt=%+v err=%v calls=%+v", receipt, err, runner.calls)
	}

	flac := filepath.Join(dir, "attention.flac")
	if err := os.WriteFile(flac, []byte("fake flac"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache = NewConfiguredAssetCache(fallback, func() (SoundPaths, error) {
		return SoundPaths{Attention: flac, ConfigPath: filepath.Join(dir, "config.json")}, nil
	})
	runner = &linuxFakeRunner{paths: map[string]string{"paplay": "/bin/paplay"}, runErrors: map[string]error{}}
	player = newLinuxSound(runner, cache, time.Second)
	capability = player.Probe(context.Background())
	if !capability.Available || !strings.Contains(capability.Reason, "custom .flac sound is unsupported") {
		t.Fatalf("unsupported custom capability = %+v", capability)
	}
	receipt, err = player.Play(context.Background(), CueAttention)
	if err != nil || !receipt.Delivered || receipt.Reason != "custom_sound_fallback" || len(runner.calls) != 1 || runner.calls[0].args[0] != fallback.path {
		t.Fatalf("fallback receipt=%+v err=%v calls=%+v", receipt, err, runner.calls)
	}

	missing := filepath.Join(dir, "missing.wav")
	cache = NewConfiguredAssetCache(fallback, func() (SoundPaths, error) {
		return SoundPaths{Failure: missing, ConfigPath: filepath.Join(dir, "config.json")}, nil
	})
	runner = &linuxFakeRunner{paths: map[string]string{"paplay": "/bin/paplay"}, runErrors: map[string]error{}}
	player = newLinuxSound(runner, cache, time.Second)
	capability = player.Probe(context.Background())
	if !capability.Available || !strings.Contains(capability.Reason, "failure custom sound is unavailable") {
		t.Fatalf("invalid custom capability = %+v", capability)
	}
	receipt, err = player.Play(context.Background(), CueFailure)
	if err != nil || !receipt.Delivered || len(runner.calls) != 1 || runner.calls[0].args[0] != fallback.path {
		t.Fatalf("invalid custom fallback receipt=%+v err=%v calls=%+v", receipt, err, runner.calls)
	}
}

func TestLinuxCustomPlayerFailureFallsBackAndSoundTimeoutIsBounded(t *testing.T) {
	dir := t.TempDir()
	mp3 := filepath.Join(dir, "attention.mp3")
	if err := os.WriteFile(mp3, []byte("fake mp3"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := NewConfiguredAssetCache(linuxTestCache{path: "/cache/attention.wav"}, func() (SoundPaths, error) {
		return SoundPaths{Attention: mp3, ConfigPath: filepath.Join(dir, "config.json")}, nil
	})
	runner := &linuxFakeRunner{
		paths:     map[string]string{"paplay": "/bin/paplay", "ffplay": "/bin/ffplay"},
		runErrors: map[string]error{"/bin/ffplay": errors.New("decoder failed")},
	}
	player := newLinuxSound(runner, cache, time.Second)
	receipt, err := player.Play(context.Background(), CueAttention)
	if err != nil || !receipt.Delivered || receipt.Provider != "paplay" || receipt.Reason != "custom_sound_fallback" || len(runner.calls) != 2 {
		t.Fatalf("fallback receipt=%+v err=%v calls=%+v", receipt, err, runner.calls)
	}

	blocking := &linuxFakeRunner{paths: map[string]string{"paplay": "/bin/paplay"}, runErrors: map[string]error{}, blockRun: true}
	player = newLinuxSound(blocking, linuxTestCache{path: "/cache/done.wav"}, 5*time.Millisecond)
	receipt, err = player.Play(context.Background(), CueDone)
	if err == nil || receipt.Delivered || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout receipt=%+v err=%v", receipt, err)
	}

	failing := &linuxFakeRunner{paths: map[string]string{"paplay": "/bin/paplay"}, runErrors: map[string]error{"/bin/paplay": errors.New("audio service refused")}}
	player = newLinuxSound(failing, linuxTestCache{path: "/cache/done.wav"}, time.Second)
	receipt, err = player.Play(context.Background(), CueDone)
	if err == nil || receipt.Delivered || !strings.Contains(err.Error(), "audio service refused") {
		t.Fatalf("failure receipt=%+v err=%v", receipt, err)
	}
}

func linuxContainsPair(args []string, key, value string) bool { return pairValue(args, key) == value }

func pairValue(args []string, key string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key {
			return args[i+1]
		}
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
