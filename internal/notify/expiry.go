package notify

import (
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// ResolvedConfig is an immutable notification-policy snapshot. Its source map
// is private; callers can inspect copies but cannot mutate the live policy.
type ResolvedConfig struct {
	NativeMode config.DeliveryMode
	SoundMode  config.DeliveryMode
	QuietHours ResolvedQuietHours
	sources    map[SourceID]ResolvedSourceRule
}

type ResolvedQuietHours struct {
	Enabled     bool
	StartMinute int
	EndMinute   int
}

type ResolvedSourceRule struct {
	Toast  bool
	Native bool
	Sound  config.SoundCue
	Expiry time.Duration
}

var resolvedConfig atomic.Pointer[ResolvedConfig]

func init() {
	cfg := resolveConfig(config.DefaultNotificationsConfig())
	resolvedConfig.Store(&cfg)
}

// ApplyConfig atomically replaces the complete resolved policy. Normalize,
// delivery resolution, the TUI, and CLI callers therefore observe one version.
func ApplyConfig(cfg config.NotificationsConfig) {
	next := resolveConfig(cfg)
	resolvedConfig.Store(&next)
}

// ResolveConfig returns the immutable policy a configuration means without
// changing the process-wide snapshot. Read-only surfaces use it to report
// defaults and resolved source rules deterministically.
func ResolveConfig(cfg config.NotificationsConfig) ResolvedConfig { return resolveConfig(cfg) }

// CurrentConfig returns the current immutable snapshot by value.
func CurrentConfig() ResolvedConfig {
	if current := resolvedConfig.Load(); current != nil {
		return *current
	}
	return resolveConfig(config.DefaultNotificationsConfig())
}

func resolveConfig(cfg config.NotificationsConfig) ResolvedConfig {
	nativeMode := normalizeMode(cfg.Native.Mode)
	soundMode := normalizeMode(cfg.Sound.Mode)
	start, _ := wallClockMinute(cfg.QuietHours.Start, 22*60)
	end, _ := wallClockMinute(cfg.QuietHours.End, 8*60)

	rules := make(map[SourceID]ResolvedSourceRule, len(sources))
	for _, source := range sources {
		rules[source.ID] = defaultSourceRule(source)
	}
	for rawID, override := range cfg.Sources {
		id := SourceID(rawID)
		rule, known := rules[id]
		if !known {
			continue
		}
		if override.Toast != nil {
			rule.Toast = *override.Toast
		}
		if override.Native != nil {
			rule.Native = *override.Native
		}
		if validCue(override.Sound) {
			rule.Sound = override.Sound
		}
		if override.Expiry != "" {
			if expiry, err := config.ParseNotificationExpiry(override.Expiry); err == nil {
				rule.Expiry = expiry
			}
		}
		rules[id] = rule
	}
	return ResolvedConfig{
		NativeMode: nativeMode,
		SoundMode:  soundMode,
		QuietHours: ResolvedQuietHours{Enabled: cfg.QuietHours.Enabled, StartMinute: start, EndMinute: end},
		sources:    rules,
	}
}

func normalizeMode(mode config.DeliveryMode) config.DeliveryMode {
	switch mode {
	case config.DeliveryBackground, config.DeliveryAlways:
		return mode
	default:
		return config.DeliveryOff
	}
}

func defaultSourceRule(source Source) ResolvedSourceRule {
	rule := ResolvedSourceRule{Toast: true, Expiry: source.DefaultExpiry, Sound: config.SoundNone}
	switch source.ID {
	case SourceWaiting:
		rule.Native, rule.Sound = true, config.SoundAttention
	case SourceSession:
		rule.Native, rule.Sound = true, config.SoundEvent
	case SourceAgent:
		rule.Native = true
	}
	return rule
}

func validCue(cue config.SoundCue) bool {
	switch cue {
	case config.SoundNone, config.SoundEvent, config.SoundAttention, config.SoundDone, config.SoundFailure:
		return true
	}
	return false
}

func wallClockMinute(raw string, fallback int) (int, bool) {
	parsed, err := time.Parse("15:04", raw)
	if err != nil || parsed.Format("15:04") != raw {
		return fallback, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

// SourceRule returns the resolved rule for a registered source. Unknown future
// sources remain centre-only until this build knows their semantics.
func (c ResolvedConfig) SourceRule(id SourceID) ResolvedSourceRule {
	if rule, ok := c.sources[id]; ok {
		return rule
	}
	return ResolvedSourceRule{Toast: true, Sound: config.SoundNone, Expiry: SourceOf(id).DefaultExpiry}
}

// SourceRules returns a defensive copy for configuration summaries and future
// structured CLI output.
func (c ResolvedConfig) SourceRules() map[SourceID]ResolvedSourceRule {
	out := make(map[SourceID]ResolvedSourceRule, len(c.sources))
	for id, rule := range c.sources {
		out[id] = rule
	}
	return out
}

// ExpiryFor retains the established notification completion contract while
// reading from the same snapshot external delivery uses.
func ExpiryFor(id SourceID) time.Duration { return CurrentConfig().SourceRule(id).Expiry }

// SetSourceExpiries is retained for focused tests and older internal callers.
// It still swaps a whole immutable snapshot rather than separate mutable state.
func SetSourceExpiries(overrides map[SourceID]time.Duration) {
	cfg := CurrentConfig()
	rules := cfg.SourceRules()
	for _, source := range sources {
		rule := rules[source.ID]
		rule.Expiry = source.DefaultExpiry
		rules[source.ID] = rule
	}
	for id, expiry := range overrides {
		if expiry < 0 {
			continue
		}
		rule := rules[id]
		rule.Expiry = expiry
		rules[id] = rule
	}
	cfg.sources = rules
	resolvedConfig.Store(&cfg)
}
