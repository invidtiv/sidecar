//go:build darwin

package notifydelivery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/notify"
)

const (
	terminalNotifierProvider = "terminal-notifier"
	osaScriptProvider        = "osascript"
	afplayProvider           = "afplay"
	providerTimeout          = 10 * time.Second
)

type darwinNative struct {
	runner Runner
	mu     sync.Mutex
	probe  *Capability
}

func NewPlatformNative(runner Runner) NativeNotifier {
	return &darwinNative{runner: runner}
}

func (n *darwinNative) Probe(ctx context.Context) Capability {
	if n == nil || n.runner == nil {
		return Capability{Reason: "no command runner"}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.probe != nil {
		return *n.probe
	}
	resolved := Capability{}
	if path, err := n.runner.LookPath(terminalNotifierProvider); err == nil {
		probeCtx, cancel := context.WithTimeout(ctx, providerTimeout)
		err = n.runner.Run(probeCtx, path, "-version")
		cancel()
		if err == nil {
			resolved = Capability{Available: true, Provider: terminalNotifierProvider}
			n.probe = &resolved
			return resolved
		}
	}
	if path, err := n.runner.LookPath("/usr/bin/osascript"); err == nil && path != "" {
		resolved = Capability{Available: true, Provider: osaScriptProvider, Reason: "fallback has no click activation or replacement removal"}
		n.probe = &resolved
		return resolved
	}
	resolved = Capability{Reason: "terminal-notifier and /usr/bin/osascript are unavailable"}
	n.probe = &resolved
	return resolved
}

func (n *darwinNative) Deliver(ctx context.Context, message Message) (ProviderReceipt, error) {
	capability := n.Probe(ctx)
	if !capability.Available {
		return ProviderReceipt{Provider: capability.Provider, At: time.Now().UTC()}, fmt.Errorf("native notifications unavailable: %s", capability.Reason)
	}
	callCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	if capability.Provider == terminalNotifierProvider {
		path, err := n.runner.LookPath(terminalNotifierProvider)
		if err != nil {
			return ProviderReceipt{Provider: terminalNotifierProvider, At: time.Now().UTC()}, err
		}
		args := terminalNotifierArgs(message)
		err = n.runner.Run(callCtx, path, args...)
		return ProviderReceipt{Provider: terminalNotifierProvider, Delivered: err == nil, At: time.Now().UTC()}, err
	}
	path, err := n.runner.LookPath("/usr/bin/osascript")
	if err != nil {
		return ProviderReceipt{Provider: osaScriptProvider, At: time.Now().UTC()}, err
	}
	err = n.runner.Run(callCtx, path, "-e", osascriptNotificationScript, "--", message.Title, message.Body)
	return ProviderReceipt{Provider: osaScriptProvider, Delivered: err == nil, At: time.Now().UTC()}, err
}

func (n *darwinNative) Remove(ctx context.Context, group string) error {
	capability := n.Probe(ctx)
	if !capability.Available || capability.Provider != terminalNotifierProvider {
		return ErrUnsupported
	}
	path, err := n.runner.LookPath(terminalNotifierProvider)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	return n.runner.Run(callCtx, path, "-remove", terminalNotifierValue(group))
}

func terminalNotifierArgs(message Message) []string {
	args := []string{
		"-title", terminalNotifierValue(message.Title),
		"-message", terminalNotifierValue(message.Body),
	}
	if subtitle := severitySubtitle(message.Severity); subtitle != "" {
		args = append(args, "-subtitle", subtitle)
	}
	if message.Group != "" {
		args = append(args, "-group", terminalNotifierValue(message.Group))
	}
	if message.ActivationBundleID != "" {
		args = append(args, "-activate", terminalNotifierValue(message.ActivationBundleID))
	}
	return args
}

func severitySubtitle(severity notify.Severity) string {
	switch severity {
	case notify.SeverityError:
		return "Session ended"
	case notify.SeverityWarning:
		return "Needs attention"
	default:
		return ""
	}
}

// terminal-notifier reads values through NSUserDefaults/property-list parsing.
// A value beginning with a property-list delimiter must escape that first
// character even though it is already a separate argv element.
func terminalNotifierValue(value string) string {
	if value == "" {
		return value
	}
	if value[0] == '-' {
		return " " + value
	}
	if strings.ContainsRune("[({\"'", rune(value[0])) {
		return "\\" + value
	}
	return value
}

const osascriptNotificationScript = `on run argv
set notificationTitle to item 1 of argv
set notificationBody to item 2 of argv
display notification notificationBody with title notificationTitle
end run`

type darwinSound struct {
	runner Runner
	cache  AssetCache
}

func NewPlatformSound(runner Runner, cache AssetCache) SoundPlayer {
	return &darwinSound{runner: runner, cache: cache}
}

func (p *darwinSound) Probe(context.Context) Capability {
	if p == nil || p.runner == nil || p.cache == nil {
		return Capability{Provider: afplayProvider, Reason: "sound adapter is incomplete"}
	}
	if _, err := p.runner.LookPath("/usr/bin/afplay"); err != nil {
		return Capability{Provider: afplayProvider, Reason: "/usr/bin/afplay is unavailable"}
	}
	return Capability{Available: true, Provider: afplayProvider}
}

func (p *darwinSound) Play(ctx context.Context, cue Cue) (ProviderReceipt, error) {
	path, err := p.cache.Materialize(cue)
	if err != nil {
		return ProviderReceipt{Provider: afplayProvider, At: time.Now().UTC()}, err
	}
	player, err := p.runner.LookPath("/usr/bin/afplay")
	if err != nil {
		return ProviderReceipt{Provider: afplayProvider, At: time.Now().UTC()}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	err = p.runner.Run(callCtx, player, path)
	return ProviderReceipt{Provider: afplayProvider, Delivered: err == nil, At: time.Now().UTC()}, err
}
