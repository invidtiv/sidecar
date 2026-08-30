package notifydelivery

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/notify"
)

const (
	linuxNativeProvider = "notify-send"
	linuxDisplayReason  = "DISPLAY and WAYLAND_DISPLAY are unset; native notifications require a local display session"
)

type linuxNative struct {
	runner  Runner
	getenv  func(string) string
	timeout time.Duration
}

func newLinuxNative(runner Runner, getenv func(string) string, timeout time.Duration) *linuxNative {
	if timeout <= 0 {
		timeout = providerTimeout
	}
	return &linuxNative{runner: runner, getenv: getenv, timeout: timeout}
}

func (n *linuxNative) Probe(ctx context.Context) Capability {
	capability, _, _ := n.capability(ctx)
	return capability
}

func (n *linuxNative) capability(ctx context.Context) (Capability, bool, string) {
	if n == nil || n.runner == nil {
		return Capability{Provider: linuxNativeProvider, Reason: "native notification adapter is incomplete"}, false, ""
	}
	if n.getenv == nil || (strings.TrimSpace(n.getenv("DISPLAY")) == "" && strings.TrimSpace(n.getenv("WAYLAND_DISPLAY")) == "") {
		return Capability{Provider: linuxNativeProvider, Reason: linuxDisplayReason}, false, ""
	}
	path, err := n.runner.LookPath(linuxNativeProvider)
	if err != nil {
		resolved := Capability{Provider: linuxNativeProvider}
		resolved.Reason = "notify-send is unavailable; install a desktop notification client for this distribution"
		return resolved, false, ""
	}

	resolved := Capability{Available: true, Provider: linuxNativeProvider}
	probeCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	if err := n.runner.Run(probeCtx, path, "--replace-id", "1", "--help"); err != nil {
		resolved.Reason = "replacement is unavailable; notifications remain available without grouping or explicit removal"
		return resolved, false, path
	}
	resolved.Reason = "replacement is supported; explicit removal is unavailable"
	return resolved, true, path
}

func (n *linuxNative) Deliver(ctx context.Context, message Message) (ProviderReceipt, error) {
	capability, replacement, path := n.capability(ctx)
	receipt := ProviderReceipt{Provider: linuxNativeProvider, At: time.Now().UTC()}
	if !capability.Available {
		return receipt, fmt.Errorf("native notifications unavailable: %s", capability.Reason)
	}
	callCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	err := n.runner.Run(callCtx, path, linuxNotifyArgs(message, replacement)...)
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return receipt, fmt.Errorf("notify-send timed out: %w", context.DeadlineExceeded)
		}
		return receipt, fmt.Errorf("notify-send failed: %w", err)
	}
	receipt.Delivered = true
	return receipt, nil
}

func (n *linuxNative) Remove(context.Context, string) error { return ErrUnsupported }

func linuxNotifyArgs(message Message, replacement bool) []string {
	expiry := "10000"
	if message.Sticky {
		expiry = "0"
	}
	args := []string{
		"--app-name", "Sidecar",
		"--urgency", linuxUrgency(message.Severity),
		"--expire-time", expiry,
	}
	if replacement && message.Group != "" {
		args = append(args, "--replace-id", strconv.FormatUint(uint64(linuxReplacementID(message.Group)), 10))
	}
	// End option parsing before provider-retained user text. Separate argv
	// elements prevent shell expansion; -- also makes a title beginning with a
	// dash data rather than a notify-send option.
	return append(args, "--", message.Title, message.Body)
}

func linuxUrgency(severity notify.Severity) string {
	switch severity {
	case notify.SeverityError:
		return "critical"
	case notify.SeverityWarning:
		return "normal"
	default:
		return "low"
	}
}

func linuxReplacementID(group string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(group))
	id := h.Sum32() & 0x7fffffff
	if id == 0 {
		return 1
	}
	return id
}

type linuxPlayerSpec struct {
	name    string
	formats map[string]bool
	args    func(string) []string
}

var linuxPlayers = []linuxPlayerSpec{
	{name: "paplay", formats: map[string]bool{"wav": true}, args: onePathArg},
	{name: "pw-play", formats: map[string]bool{"wav": true}, args: onePathArg},
	{name: "aplay", formats: map[string]bool{"wav": true}, args: func(path string) []string { return []string{"--quiet", path} }},
	{name: "ffplay", formats: map[string]bool{"wav": true, "mp3": true}, args: func(path string) []string { return []string{"-nodisp", "-autoexit", "-loglevel", "quiet", path} }},
	{name: "mpv", formats: map[string]bool{"wav": true, "mp3": true}, args: func(path string) []string { return []string{"--no-video", "--really-quiet", path} }},
}

func onePathArg(path string) []string { return []string{path} }

type linuxSound struct {
	runner  Runner
	cache   AssetCache
	timeout time.Duration
}

func newLinuxSound(runner Runner, cache AssetCache, timeout time.Duration) *linuxSound {
	if timeout <= 0 {
		timeout = providerTimeout
	}
	return &linuxSound{runner: runner, cache: cache, timeout: timeout}
}

func (p *linuxSound) Probe(context.Context) Capability {
	if p == nil || p.runner == nil || p.cache == nil {
		return Capability{Reason: "sound adapter is incomplete"}
	}
	capability := p.probeFormat("wav")
	if !capability.Available {
		return capability
	}
	inspector, ok := p.cache.(fallbackAssetCache)
	if !ok {
		return capability
	}
	var notes []string
	for _, selection := range inspector.Selections() {
		if !selection.Custom {
			continue
		}
		if selection.Err != nil {
			notes = append(notes, fmt.Sprintf("%s custom sound is unavailable and will use the built-in WAV", selection.Cue))
			continue
		}
		custom := p.probeFormat(selection.Format)
		if !custom.Available {
			notes = append(notes, fmt.Sprintf("%s custom .%s sound is unsupported and will use the built-in WAV", selection.Cue, formatLabel(selection.Format)))
		} else if custom.Provider != capability.Provider {
			notes = append(notes, fmt.Sprintf("%s custom .%s sound uses %s", selection.Cue, formatLabel(selection.Format), custom.Provider))
		}
	}
	if len(notes) > 0 {
		capability.Reason = strings.Join(notes, "; ")
	}
	return capability
}

func (p *linuxSound) probeFormat(format string) Capability {
	format = strings.ToLower(strings.TrimPrefix(format, "."))
	resolved := Capability{}
	if format != "wav" && format != "mp3" {
		resolved.Reason = fmt.Sprintf("custom .%s sound is unsupported; Linux delivery supports WAV and MP3", formatLabel(format))
		return resolved
	}
	var tried []string
	for _, candidate := range linuxPlayers {
		if !candidate.formats[format] {
			continue
		}
		tried = append(tried, candidate.name)
		if _, err := p.runner.LookPath(candidate.name); err == nil {
			resolved = Capability{Available: true, Provider: candidate.name}
			return resolved
		}
	}
	resolved.Reason = fmt.Sprintf("no Linux sound player supports %s (tried %s)", strings.ToUpper(format), strings.Join(tried, ", "))
	return resolved
}

func formatLabel(format string) string {
	if strings.TrimSpace(format) == "" {
		return "unknown-format"
	}
	return format
}

func (p *linuxSound) Play(ctx context.Context, cue Cue) (ProviderReceipt, error) {
	path, err := p.cache.Materialize(cue)
	if err != nil {
		return ProviderReceipt{At: time.Now().UTC()}, err
	}
	receipt, err := p.playPath(ctx, path)
	if err == nil {
		return receipt, nil
	}
	fallback, ok := p.cache.(fallbackAssetCache)
	if !ok {
		return receipt, err
	}
	builtIn, useFallback, fallbackErr := fallback.Fallback(cue, path, err)
	if !useFallback {
		return receipt, err
	}
	if fallbackErr != nil {
		return receipt, errors.Join(err, fallbackErr)
	}
	fallbackReceipt, fallbackErr := p.playPath(ctx, builtIn)
	if fallbackErr != nil {
		return fallbackReceipt, errors.Join(err, fmt.Errorf("built-in sound fallback failed: %w", fallbackErr))
	}
	fallbackReceipt.Reason = "custom_sound_fallback"
	return fallbackReceipt, nil
}

func (p *linuxSound) playPath(ctx context.Context, path string) (ProviderReceipt, error) {
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	capability := p.probeFormat(format)
	receipt := ProviderReceipt{Provider: capability.Provider, At: time.Now().UTC()}
	if !capability.Available {
		return receipt, fmt.Errorf("sound unavailable: %s", capability.Reason)
	}
	playerPath, err := p.runner.LookPath(capability.Provider)
	if err != nil {
		return receipt, fmt.Errorf("%s disappeared after capability probe: %w", capability.Provider, err)
	}
	spec := linuxPlayer(capability.Provider)
	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	err = p.runner.Run(callCtx, playerPath, spec.args(path)...)
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return receipt, fmt.Errorf("%s timed out: %w", capability.Provider, context.DeadlineExceeded)
		}
		return receipt, fmt.Errorf("%s failed: %w", capability.Provider, err)
	}
	receipt.Delivered = true
	return receipt, nil
}

func linuxPlayer(name string) linuxPlayerSpec {
	for _, candidate := range linuxPlayers {
		if candidate.name == name {
			return candidate
		}
	}
	return linuxPlayerSpec{args: onePathArg}
}
